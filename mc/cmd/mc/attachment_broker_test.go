package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mc/verbs"
)

type blockingAttachmentReader struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func newBlockingAttachmentReader() *blockingAttachmentReader {
	return &blockingAttachmentReader{
		started: make(chan struct{}), closed: make(chan struct{}),
	}
}

func (r *blockingAttachmentReader) Read([]byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.closed
	return 0, errors.New("attachment input closed")
}

func (r *blockingAttachmentReader) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

func writeBrokerCanaryReceipt(t *testing.T, home, generation string) {
	t.Helper()
	root := filepath.Join(home, "health", "attachment-canary")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{home, filepath.Join(home, "health"), root} {
		if err := os.Chmod(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	body, err := json.Marshal(verbs.AttachmentCanaryReceipt{
		Version: 1, Generation: generation,
		InboundSHA256:  "1a" + strings.Repeat("1", 62),
		OutboundSHA256: "2b" + strings.Repeat("2", 62),
		RealUID:        10002, EffectiveUID: 10001, SavedUID: 10001, FSUID: 10001,
		WarmInboundRead: true, HostOutboundRead: true, PrivateGateHealthy: true,
		DeniedOutbound: []string{
			"create:EACCES", "open:EACCES", "rename:EACCES", "traverse:EACCES", "unlink:EACCES",
		},
		RestartInboundRead: true, RestartOutboundRead: true, OutsideSessions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "last.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateAttachmentBrokerAuthenticatesCanaryAndUsesHelperFrame(t *testing.T) {
	generation := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	home := t.TempDir()
	writeBrokerCanaryReceipt(t, home, generation)
	t.Setenv("MC_HOME", home)
	t.Setenv("MC_RUN_JSON", "")
	t.Setenv("MC_SPINE", "")
	stopGuard := startAttachmentGuardForTest(t, generation)
	t.Cleanup(stopGuard)

	oldEnsure, oldCall, oldOpen, oldTerminalOpen := ensurePrivateAttachmentHelper,
		callPrivateAttachmentHelper, openPrivateAttachmentSpine,
		openPrivateAttachmentTerminalSpine
	t.Cleanup(func() {
		ensurePrivateAttachmentHelper, callPrivateAttachmentHelper = oldEnsure, oldCall
		openPrivateAttachmentSpine = oldOpen
		openPrivateAttachmentTerminalSpine = oldTerminalOpen
	})
	ensurePrivateAttachmentHelper = func() (string, error) { return "mc-helper-test", nil }
	var called bool
	callPrivateAttachmentHelper = func(verb string, request, response any) error {
		called = true
		switch verb {
		case "__attachment-ingress-reserve-spine":
			got := request.(privateAttachmentIngressReserveRequest)
			if got.OwnerGeneration != generation || got.Session != "h-broker" {
				t.Fatal("helper received a mismatched reservation frame")
			}
			out := response.(*privateAttachmentIngressReservation)
			*out = privateAttachmentIngressReservation{
				AttemptID: "b123456789abcdef", BatchID: "9007199254740993",
				ByteCount: 7, HardDeadline: "2026-07-30 18:15:00",
				IngressID: "9007199254740992", LeaseUntil: "2026-07-30 18:00:30",
				ObjectCount: 1, OwnerToken: strings.Repeat("c", 32),
				Publications: []privateAttachmentPublicationReservation{{
					DisplayName: "capture.png", MediaType: "image/png", Ordinal: 1,
					OwnerToken:    strings.Repeat("d", 32),
					PublicationID: "e123456789abcdef", Size: 7,
				}},
			}
		case "__attachment-publication-renew-spine":
			got := request.(privateAttachmentPublicationRenewRequest)
			if !got.valid() || got.OwnerGeneration != generation || got.Session != "h-broker" {
				t.Fatal("helper received a mismatched renewal frame")
			}
			out := response.(*verbs.AttachmentPublicationLeaseReceipt)
			*out = verbs.AttachmentPublicationLeaseReceipt{
				AttemptID: got.AttemptID, PublicationID: got.PublicationID,
				LeaseUntil: "2026-07-30 18:01:00", HardDeadline: "2026-07-30 18:15:00",
			}
		case "__attachment-attempt-abort-spine":
			got := request.(privateAttachmentAttemptAbortRequest)
			if !got.valid() || got.OwnerGeneration != generation || got.Session != "h-broker" {
				t.Fatal("helper received a mismatched abort frame")
			}
			out := response.(*privateAttachmentAttemptTerminalReceipt)
			*out = privateAttachmentAttemptTerminalFrame(verbs.AttachmentAttemptTerminalReceipt{
				AttemptID: got.AttemptID, IngressID: 9_007_199_254_740_992,
				BatchID: 9_007_199_254_740_993, Attempt: 1, Outcome: "requeued",
				Reason: "explicit_abort", Receipt: `{"outcome":"requeued"}`,
			})
		case "__attachment-attempts-expire-due-spine":
			got := request.(privateAttachmentAttemptsExpireDueRequest)
			if !got.valid() || got.After != "" || got.Limit != 128 ||
				got.OwnerGeneration != generation {
				t.Fatal("helper received a mismatched expiry frame")
			}
			out := response.(*privateAttachmentAttemptExpiryPage)
			*out = privateAttachmentExpiryPageFrame(verbs.AttachmentAttemptExpiryPage{
				Receipts: []verbs.AttachmentAttemptTerminalReceipt{{
					AttemptID: "b123456789abcdef", IngressID: 9_007_199_254_740_992,
					BatchID: 9_007_199_254_740_993, Attempt: 1, Outcome: "failed",
					Reason: "hard_expired", Receipt: `{"outcome":"failed"}`,
				}},
				Next: "b123456789abcdef",
			})
		default:
			t.Fatalf("unexpected helper verb %q", verb)
		}
		return nil
	}
	var input, stdout, stderr bytes.Buffer
	if err := writePrivateFrame(&input, privateAttachmentIngressReserveRequest{
		OwnerGeneration: generation, Session: "h-broker",
	}); err != nil {
		t.Fatal(err)
	}
	if code := brokerPrivateAttachment(
		[]string{"__attachment-ingress-reserve"}, &input, &stdout, &stderr,
	); code != 0 {
		t.Fatalf("reservation broker code=%d stderr=%q", code, stderr.String())
	}
	if !called || !strings.Contains(stdout.String(), `"ingress_id":"9007199254740992"`) ||
		!strings.Contains(stdout.String(), `"batch_id":"9007199254740993"`) {
		t.Fatalf("reservation broker result mismatch; called=%v bytes=%d", called, stdout.Len())
	}

	renew := privateAttachmentPublicationRenewRequest{
		AttemptID: "b123456789abcdef", AttemptOwnerToken: strings.Repeat("c", 32),
		OwnerGeneration: generation, PublicationID: "e123456789abcdef",
		PublicationOwnerToken: strings.Repeat("d", 32), Session: "h-broker",
	}
	input.Reset()
	if err := writePrivateFrame(&input, renew); err != nil {
		t.Fatal(err)
	}
	called = false
	stdout.Reset()
	stderr.Reset()
	if code := brokerPrivateAttachment(
		[]string{"__attachment-publication-renew"}, &input, &stdout, &stderr,
	); code != 0 || !called ||
		!strings.Contains(stdout.String(), `"publication_id":"e123456789abcdef"`) {
		t.Fatalf("renewal broker code=%d called=%v stderr=%q",
			code, called, stderr.String())
	}

	abort := privateAttachmentAttemptAbortRequest{
		AttemptID: renew.AttemptID, OwnerGeneration: generation,
		OwnerToken: renew.AttemptOwnerToken, Session: renew.Session,
	}
	input.Reset()
	if err := writePrivateFrame(&input, abort); err != nil {
		t.Fatal(err)
	}
	called = false
	stdout.Reset()
	stderr.Reset()
	if code := brokerPrivateAttachment(
		[]string{"__attachment-attempt-abort"}, &input, &stdout, &stderr,
	); code != 0 || !called ||
		!strings.Contains(stdout.String(), `"ingress_id":"9007199254740992"`) ||
		!strings.Contains(stdout.String(), `"batch_id":"9007199254740993"`) {
		t.Fatalf("abort broker code=%d called=%v stderr=%q",
			code, called, stderr.String())
	}

	expiry := privateAttachmentAttemptsExpireDueRequest{
		OwnerGeneration: generation, After: "", Limit: 128,
	}
	input.Reset()
	if err := writePrivateFrame(&input, expiry); err != nil {
		t.Fatal(err)
	}
	called = false
	stdout.Reset()
	stderr.Reset()
	if code := brokerPrivateAttachment(
		[]string{"__attachment-attempts-expire-due"}, &input, &stdout, &stderr,
	); code != 0 || !called ||
		!strings.Contains(stdout.String(), `"reason":"hard_expired"`) {
		t.Fatalf("expiry broker code=%d called=%v stderr=%q",
			code, called, stderr.String())
	}

	spine := filepath.Join(home, "spine.db")
	if _, err := verbs.Init(verbs.InitArgs{
		Spine: spine, Project: "ws-test",
		WorkspaceRoot: filepath.Join(home, "workspace"),
	}); err != nil {
		t.Fatal(err)
	}
	openPrivateAttachmentTerminalSpine = func() (*sql.DB, error) {
		return verbs.OpenSpine(spine)
	}
	input.Reset()
	if err := writePrivateFrame(&input, privateAttachmentTerminalsReconcileRequest{
		AfterBatchID: "0", Limit: 128, OwnerGeneration: generation,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := brokerPrivateAttachment(
		[]string{"__attachment-terminals-reconcile"}, &input, &stdout, &stderr,
	); code != 0 || !strings.Contains(stdout.String(), `"batches":[]`) {
		t.Fatalf("reconciliation broker code=%d stderr=%q stdout=%q",
			code, stderr.String(), stdout.String())
	}

	input.Reset()
	if err := writePrivateFrame(&input, privateAttachmentIngressReserveRequest{
		OwnerGeneration: strings.Repeat("0", 32), Session: "h-broker",
	}); err != nil {
		t.Fatal(err)
	}
	called = false
	stdout.Reset()
	stderr.Reset()
	if code := brokerPrivateAttachment(
		[]string{"__attachment-ingress-reserve"}, &input, &stdout, &stderr,
	); code != 1 || called {
		t.Fatalf("fabricated generation code=%d called=%v stderr=%q",
			code, called, stderr.String())
	}

	callPrivateAttachmentHelper = func(string, any, any) error {
		return &privateHelperExitError{message: "fenced renewal", code: 1}
	}
	input.Reset()
	if err := writePrivateFrame(&input, renew); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := brokerPrivateAttachment(
		[]string{"__attachment-publication-renew"}, &input, &stdout, &stderr,
	); code != 1 || !strings.Contains(stdout.String(), `"code":"domain-rejection"`) {
		t.Fatalf("renewal helper domain translation code=%d stderr=%q", code, stderr.String())
	}

	var opened bool
	openPrivateAttachmentSpine = func() (*sql.DB, error) {
		opened = true
		return nil, errors.New("publisher spine must not open after renewal refusal")
	}
	callPrivateAttachmentHelper = func(verb string, request, response any) error {
		if verb != "__attachment-publication-renew-spine" {
			t.Fatalf("first publisher helper verb = %q", verb)
		}
		return &privateHelperExitError{message: "expired publisher lease", code: 1}
	}
	input.Reset()
	if err := writePrivateFrame(&input, privateAttachmentPublicationPublishRequest{
		AttemptID: renew.AttemptID, AttemptOwnerToken: renew.AttemptOwnerToken,
		OwnerGeneration: generation, PublicationID: renew.PublicationID,
		PublicationOwnerToken: renew.PublicationOwnerToken,
		Session:               renew.Session, Size: 7,
	}); err != nil {
		t.Fatal(err)
	}
	input.WriteString("publish")
	stdout.Reset()
	stderr.Reset()
	if code := brokerPrivateAttachment(
		[]string{"__attachment-publication-publish"}, &input, &stdout, &stderr,
	); code != 1 || opened ||
		!strings.Contains(stderr.String(), "expired publisher lease") {
		t.Fatalf("publisher immediate renewal code=%d opened=%v stderr=%q",
			code, opened, stderr.String())
	}
}

func TestPrivateAttachmentReservationFrameKeepsInt64IdentitiesExact(t *testing.T) {
	frame := privateAttachmentReservationFrame(verbs.AttachmentIngressReservation{
		IngressID: 9_007_199_254_740_992,
		BatchID:   9_007_199_254_740_993,
	})
	if frame.IngressID != "9007199254740992" || frame.BatchID != "9007199254740993" {
		t.Fatalf("exact identities = ingress %q batch %q", frame.IngressID, frame.BatchID)
	}
}

func TestPrivateAttachmentPublisherRenewsUntilStoppedAndFencesOnFailure(t *testing.T) {
	oldCall, oldInterval := callPrivateAttachmentHelper, privateAttachmentRenewEvery
	t.Cleanup(func() {
		callPrivateAttachmentHelper = oldCall
		privateAttachmentRenewEvery = oldInterval
	})
	privateAttachmentRenewEvery = time.Millisecond
	called := make(chan struct{}, 1)
	callPrivateAttachmentHelper = func(verb string, request, response any) error {
		if verb != "__attachment-publication-renew-spine" {
			t.Fatalf("renewal helper verb = %q", verb)
		}
		renewal := request.(privateAttachmentPublicationRenewRequest)
		if renewal.AttemptOwnerToken != strings.Repeat("c", 32) ||
			renewal.PublicationOwnerToken != strings.Repeat("d", 32) {
			t.Fatal("renewal helper lost the paired publication capabilities")
		}
		called <- struct{}{}
		return errors.New("lease fenced")
	}
	request := privateAttachmentPublicationPublishRequest{
		AttemptID: "b123456789abcdef", AttemptOwnerToken: strings.Repeat("c", 32),
		OwnerGeneration:       "a1b2c3d4e5f60718293a4b5c6d7e8f90",
		PublicationID:         "e123456789abcdef",
		PublicationOwnerToken: strings.Repeat("d", 32),
		Session:               "h-broker", Size: 7,
	}
	fence := newPrivateAttachmentRenewalFence()
	stop := maintainPrivateAttachmentPublicationLease(request, fence)
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("publisher lease was not renewed")
	}
	if err := stop(); err == nil || !strings.Contains(err.Error(), "lease fenced") {
		t.Fatalf("renewal fence error = %v", err)
	}
	stream := &privateAttachmentByteStream{
		source: bytes.NewBufferString("publish"), fence: fence,
	}
	var body [1]byte
	if _, err := stream.Read(body[:]); err == nil {
		t.Fatal("publisher stream remained writable after renewal failure")
	}
}

func TestPrivateAttachmentPublisherFenceInterruptsBlockedBodyRead(t *testing.T) {
	source := newBlockingAttachmentReader()
	fence := newPrivateAttachmentRenewalFence()
	stream := &privateAttachmentByteStream{source: source, fence: fence}
	result := make(chan error, 1)
	go func() {
		var body [1]byte
		_, err := stream.Read(body[:])
		result <- err
	}()
	select {
	case <-source.started:
	case <-time.After(time.Second):
		t.Fatal("attachment body read did not start")
	}
	fence.Fail(errors.New("lease fenced"))
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "lease fenced") {
			t.Fatalf("blocked read fence error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked attachment body read outlived its lease fence")
	}
}

func TestPrivateAttachmentSpineRejectsExpandedFrame(t *testing.T) {
	request := privateAttachmentIngressReserveRequest{
		OwnerGeneration: "a1b2c3d4e5f60718293a4b5c6d7e8f90",
		Session:         "h-private",
	}
	var input bytes.Buffer
	if err := writePrivateFrame(&input, request); err != nil {
		t.Fatalf("The valid attachment frame failed to encode: %v.", err)
	}
	var decoded privateAttachmentIngressReserveRequest
	if err := readPrivateFrame(&input, &decoded); err != nil || decoded != request {
		t.Fatalf("The valid attachment frame failed to decode: value=%+v error=%v.", decoded, err)
	}

	input.Reset()
	if err := writePrivateFrame(&input, map[string]any{
		"owner_generation": request.OwnerGeneration,
		"session":          request.Session,
		"smuggled":         true,
	}); err != nil {
		t.Fatalf("The expanded attachment frame failed to encode: %v.", err)
	}
	if err := readPrivateFrame(&input, &decoded); err == nil || err.Error() != "private frame is not the closed canonical encoding" {
		t.Fatalf("The expanded attachment frame returned an unexpected error: %v.", err)
	}
}

// A host-minted deadline read back against the VM clock must be CLAMPED, never
// refused. The four private helpers previously each refused an over-long
// remaining budget, so ordinary host/VM clock skew — routine on macOS
// dark-wake, where the VM is paused while the host clock runs on — became a
// usage refusal. The resident's attachment sweep aborts its whole tick when a
// leg refuses, so dispatch stopped: 214 aborted ticks over 42 hours on the
// operator's deployment.
//
// This asserts the policy function rather than the four call sites on purpose,
// and the reason is itself the finding. `privateHelperScopeOK` requires
// GOOS=linux plus /.dockerenv under the default build tags, so on the
// development Mac every one of those four guards returns at its scope check
// before the deadline is ever read. A call-site test here passes without
// executing a single line of what it claims to cover — I wrote that test first
// and it passed against the unfixed code. Centralizing the policy is what
// makes it assertable at all: no call site can reintroduce a refusal without
// first deleting the shared helper.
func TestHelperDeadlineBudgetClampsClockSkewInsteadOfRefusing(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name     string
		deadline time.Time
		want     time.Duration
		wantOK   bool
	}{{
		// The regression: a VM lagging the host by half a minute. Before the
		// fix this whole branch was a refusal.
		name: "skewed_far_future_is_capped", deadline: now.Add(privateHelperSelfTimeout + 30*time.Second),
		want: privateHelperSelfTimeout, wantOK: true,
	}, {
		name: "exactly_the_allowance_is_kept", deadline: now.Add(privateHelperSelfTimeout),
		want: privateHelperSelfTimeout, wantOK: true,
	}, {
		name: "ordinary_budget_is_untouched", deadline: now.Add(time.Second),
		want: time.Second, wantOK: true,
	}, {
		// Preserved deliberately: never start work whose deadline has passed.
		// Its 124 is handled as a transient timeout, not a refusal.
		name: "already_expired_still_refuses_to_start", deadline: now.Add(-time.Millisecond),
		want: 0, wantOK: false,
	}, {
		name: "exactly_now_still_refuses_to_start", deadline: now,
		want: 0, wantOK: false,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			budget, ok := helperDeadlineBudget(tc.deadline, now)
			if ok != tc.wantOK || budget != tc.want {
				t.Fatalf("helperDeadlineBudget = (%v, %v), want (%v, %v)", budget, ok, tc.want, tc.wantOK)
			}
			if budget > privateHelperSelfTimeout {
				t.Fatalf("budget %v exceeds the allowance the broker granted", budget)
			}
		})
	}
}
