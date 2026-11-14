package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	operatorconfig "mc/config"
	"mc/verbs"
)

type attachmentPipeRPCChannel struct {
	input  *io.PipeWriter
	output *io.PipeReader
	done   <-chan error
	mu     sync.Mutex
	closed bool
}

func (c *attachmentPipeRPCChannel) Exchange(
	_ context.Context,
	request attachmentSpineRPCRequest,
) (attachmentSpineRPCResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var response attachmentSpineRPCResponse
	if err := writePrivateFrame(c.input, request); err != nil {
		return response, err
	}
	if err := readPrivateMessage(c.output, &response); err != nil {
		return response, err
	}
	return response, nil
}

func (c *attachmentPipeRPCChannel) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	_ = c.input.Close()
	_ = c.output.Close()
	c.mu.Unlock()
	return <-c.done
}

func attachmentPipeRPCDialer(
	t *testing.T,
	spine string,
	active *atomic.Int64,
) func() (attachmentRPCChannel, error) {
	t.Helper()
	return func() (attachmentRPCChannel, error) {
		clientOutput, serverOutput := io.Pipe()
		serverInput, clientInput := io.Pipe()
		done := make(chan error, 1)
		active.Add(1)
		go func() {
			defer active.Add(-1)
			db, err := verbs.OpenSpine(spine)
			if err == nil {
				err = servePrivateAttachmentSpineRPC(db, serverInput, serverOutput)
				err = errors.Join(err, db.Close())
			}
			_ = serverInput.Close()
			_ = serverOutput.Close()
			done <- err
		}()
		return &attachmentPipeRPCChannel{
			input: clientInput, output: clientOutput, done: done,
		}, nil
	}
}

func attachmentTerminalPipeRPCDialer(
	t *testing.T,
	spine string,
	active *atomic.Int64,
) func() (attachmentRPCChannel, error) {
	t.Helper()
	return func() (attachmentRPCChannel, error) {
		clientOutput, serverOutput := io.Pipe()
		serverInput, clientInput := io.Pipe()
		done := make(chan error, 1)
		active.Add(1)
		go func() {
			defer active.Add(-1)
			db, err := verbs.OpenSpine(spine)
			if err == nil {
				err = servePrivateAttachmentTerminalSpineRPC(db, serverInput, serverOutput)
				err = errors.Join(err, db.Close())
			}
			_ = serverInput.Close()
			_ = serverOutput.Close()
			done <- err
		}()
		return &attachmentPipeRPCChannel{
			input: clientInput, output: clientOutput, done: done,
		}, nil
	}
}

type attachmentStreamingProbe struct {
	t      *testing.T
	active *atomic.Int64
	reader io.Reader
	once   sync.Once
}

func (r *attachmentStreamingProbe) Read(body []byte) (int, error) {
	r.once.Do(func() {
		if active := r.active.Load(); active != 0 {
			r.t.Errorf("attachment byte stream began with %d helper connections open", active)
		}
	})
	return r.reader.Read(body)
}

func TestAttachmentPublisherRunsUnchangedAcrossClosedSpineProxy(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)
	if _, err := verbs.Init(verbs.InitArgs{
		Spine: spine, Project: "ws-test",
		WorkspaceRoot: filepath.Join(home, "workspace"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := operatorconfig.Ensure(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), enableReferenceBindings); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "routing.md"), []byte(`# proxy test routing
| role | harness | binding |
| --- | --- | --- |
| strategist | claude-sdk | claude |
| arbiter | codex | chatgpt |
| worker | claude-sdk | endpoint |
| verifier | codex | chatgpt |
| packager | claude-sdk | endpoint |
| collie | claude-sdk | claude |
`), 0o600); err != nil {
		t.Fatal(err)
	}
	direct, err := verbs.OpenSpine(spine)
	if err != nil {
		t.Fatal(err)
	}
	startedAny, err := verbs.CollieStart(
		direct, nil, verbs.CollieStartArgs{From: "discord:proxy-test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	session := startedAny.(verbs.CollieStartResult).SessionID
	var binding int64
	if err := direct.QueryRow(`
		SELECT id FROM collie_bindings
		WHERE session_id=? AND surface='discord' AND active=1`,
		session).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	if _, err := verbs.AttachmentIngressEnqueue(
		direct, nil, verbs.AttachmentIngressEnqueueArgs{
			Session: session, BindingID: binding, Surface: "discord",
			ExternalMessageID: "proxy-message", Body: "attachment follows",
			Manifest: `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"note.txt"}]`,
		},
	); err != nil {
		t.Fatal(err)
	}
	generation := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	reservation, err := verbs.AttachmentIngressReserve(
		direct, nil, verbs.AttachmentIngressReserveArgs{
			Session: session, OwnerGeneration: generation,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := direct.Close(); err != nil {
		t.Fatal(err)
	}

	var active atomic.Int64
	proxy, err := openAttachmentSpineProxy(attachmentPipeRPCDialer(t, spine, &active))
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	publication := reservation.Publications[0]
	source := &attachmentStreamingProbe{
		t: t, active: &active, reader: bytes.NewBufferString("publish"),
	}
	receipt, err := verbs.AttachmentPublicationPublish(
		proxy, nil, home, verbs.AttachmentPublicationPublishArgs{
			Session: session, AttemptID: reservation.AttemptID,
			AttemptOwnerToken:     reservation.OwnerToken,
			PublicationID:         publication.PublicationID,
			PublicationOwnerToken: publication.OwnerToken,
			OwnerGeneration:       generation,
		}, source,
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != "materialized" || receipt.Size != 7 ||
		receipt.PublicationID != publication.PublicationID ||
		receipt.SHA256 != "a5d47a4311d759db69e576d9eedd6a02fcfd9cd214129fa8492ee1e9c7343def" {
		t.Fatalf("proxy publisher receipt = %#v", receipt)
	}
	if active.Load() != 0 {
		t.Fatalf("proxy retained %d helper connections after publish", active.Load())
	}

	replay, err := verbs.AttachmentPublicationPublish(
		proxy, nil, home, verbs.AttachmentPublicationPublishArgs{
			Session: session, AttemptID: reservation.AttemptID,
			AttemptOwnerToken:     reservation.OwnerToken,
			PublicationID:         publication.PublicationID,
			PublicationOwnerToken: publication.OwnerToken,
			OwnerGeneration:       generation,
		}, bytes.NewBufferString("publish"),
	)
	if err != nil || replay != receipt {
		t.Fatalf("proxy publisher replay = %#v err=%v, first=%#v", replay, err, receipt)
	}
}

func TestAttachmentSpineRPCRejectsGeneralSQLAndPreservesExactValues(t *testing.T) {
	values := []any{nil, int64(9_223_372_036_854_775_807), "text", []byte{0, 1, 255}, true}
	encoded, err := encodeAttachmentSQLValues(values)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeAttachmentSQLValues(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(values) ||
		decoded[0] != nil ||
		decoded[1] != values[1] ||
		decoded[2] != values[2] ||
		!bytes.Equal(decoded[3].([]byte), values[3].([]byte)) ||
		decoded[4] != true {
		t.Fatalf("exact SQL value round trip = %#v", decoded)
	}
	expanded := encoded[1]
	expanded.String = "smuggled"
	if _, err := decodeAttachmentSQLValues([]attachmentSQLValue{expanded}); err == nil {
		t.Fatal("expanded SQL value was accepted")
	}

	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)
	if _, err := verbs.Init(verbs.InitArgs{
		Spine: spine, Project: "ws-test",
		WorkspaceRoot: filepath.Join(home, "workspace"),
	}); err != nil {
		t.Fatal(err)
	}
	var active atomic.Int64
	channel, err := attachmentPipeRPCDialer(t, spine, &active)()
	if err != nil {
		t.Fatal(err)
	}
	response, err := channel.Exchange(context.Background(), attachmentSpineRPCRequest{
		Operation: "query", Statement: "SELECT 1", Version: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Error, "not in the publisher allowlist") {
		t.Fatalf("general SQL response = %#v", response)
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAttachmentTerminalReconciliationRunsAcrossItsOwnClosedSpineProxy(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)
	if _, err := verbs.Init(verbs.InitArgs{
		Spine: spine, Project: "ws-test",
		WorkspaceRoot: filepath.Join(home, "workspace"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := operatorconfig.Ensure(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), enableReferenceBindings); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "routing.md"), []byte(`# terminal proxy routing
| role | harness | binding |
| --- | --- | --- |
| strategist | claude-sdk | claude |
| arbiter | codex | chatgpt |
| worker | claude-sdk | endpoint |
| verifier | codex | chatgpt |
| packager | claude-sdk | endpoint |
| collie | claude-sdk | claude |
`), 0o600); err != nil {
		t.Fatal(err)
	}
	direct, err := verbs.OpenSpine(spine)
	if err != nil {
		t.Fatal(err)
	}
	startedAny, err := verbs.CollieStart(
		direct, nil, verbs.CollieStartArgs{From: "discord:terminal-proxy"},
	)
	if err != nil {
		t.Fatal(err)
	}
	session := startedAny.(verbs.CollieStartResult).SessionID
	var binding int64
	if err := direct.QueryRow(`
		SELECT id FROM collie_bindings
		WHERE session_id=? AND surface='discord' AND active=1`,
		session).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	if _, err := verbs.AttachmentIngressEnqueue(
		direct, nil, verbs.AttachmentIngressEnqueueArgs{
			Session: session, BindingID: binding, Surface: "discord",
			ExternalMessageID: "terminal-proxy-message", Body: "attachment follows",
			Manifest: `[{"ordinal":1,"size":7,"media_type":"text/plain","display_name":"note.txt"}]`,
		},
	); err != nil {
		t.Fatal(err)
	}
	generation := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	reservation, err := verbs.AttachmentIngressReserve(
		direct, nil, verbs.AttachmentIngressReserveArgs{
			Session: session, OwnerGeneration: generation,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	publication := reservation.Publications[0]
	if _, err := verbs.AttachmentPublicationPublish(
		direct, nil, home, verbs.AttachmentPublicationPublishArgs{
			Session: session, AttemptID: reservation.AttemptID,
			AttemptOwnerToken:     reservation.OwnerToken,
			PublicationID:         publication.PublicationID,
			PublicationOwnerToken: publication.OwnerToken,
			OwnerGeneration:       generation,
		}, bytes.NewBufferString("publish"),
	); err != nil {
		t.Fatal(err)
	}
	terminal, err := verbs.AttachmentAttemptAbort(
		direct, nil, verbs.AttachmentAttemptAbortArgs{
			Session: session, AttemptID: reservation.AttemptID,
			OwnerToken: reservation.OwnerToken, OwnerGeneration: generation,
		},
	)
	if err != nil || terminal.Outcome != "requeued" {
		t.Fatalf("terminalize fixture = %#v err=%v", terminal, err)
	}
	if err := direct.Close(); err != nil {
		t.Fatal(err)
	}

	var active atomic.Int64
	proxy, err := openAttachmentSpineProxy(
		attachmentTerminalPipeRPCDialer(t, spine, &active))
	if err != nil {
		t.Fatal(err)
	}
	page, err := verbs.AttachmentTerminalsReconcile(proxy, nil, home, 0, 128)
	closeErr := proxy.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("terminal proxy reconciliation err=%v close=%v", err, closeErr)
	}
	if len(page.Batches) != 1 || page.Batches[0].AttemptID != reservation.AttemptID ||
		page.Batches[0].Outcome != "requeued" || page.Next != terminal.BatchID {
		t.Fatalf("terminal proxy page = %#v", page)
	}
	if active.Load() != 0 {
		t.Fatalf("terminal proxy retained %d helper connections", active.Load())
	}

	check, err := verbs.OpenSpine(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	for table, query := range map[string]string{
		"attempts":     `SELECT COUNT(*) FROM attachment_attempts WHERE id=?`,
		"batches":      `SELECT COUNT(*) FROM attachment_batches WHERE id=?`,
		"publications": `SELECT COUNT(*) FROM attachment_publications WHERE batch_id=?`,
	} {
		value := any(reservation.AttemptID)
		if table != "attempts" {
			value = reservation.BatchID
		}
		var count int
		if err := check.QueryRow(query, value).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s residue count=%d err=%v", table, count, err)
		}
	}
}

// enableReferenceBindings adds the endpoint agent profile and enables it with
// the chatgpt and claude reference bindings, the set the test routing tables
// name. The shipped catalog enables no binding until onboarding records the
// operator's selection.
func enableReferenceBindings(cfg *operatorconfig.Config) error {
	if cfg.Runtimes.Credentials == nil {
		cfg.Runtimes.Credentials = map[string]operatorconfig.CredentialSource{}
	}
	if cfg.Runtimes.Bindings == nil {
		cfg.Runtimes.Bindings = map[string]operatorconfig.RuntimeBinding{}
	}
	profile := cfg.Runtimes.Bindings["chatgpt"]
	profile.Model = "fixture-model"
	cfg.Runtimes.Bindings["chatgpt"] = profile
	cfg.Runtimes.Credentials["endpoint"] = cfg.Runtimes.Credentials["claude"]
	cfg.Runtimes.Bindings["endpoint"] = operatorconfig.RuntimeBinding{
		Harness: "claude-sdk", Model: "endpoint-model", Credential: "endpoint",
	}
	cfg.Runtimes.EnabledBindings = []string{"chatgpt", "claude", "endpoint"}
	return nil
}
