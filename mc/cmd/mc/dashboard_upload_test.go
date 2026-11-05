package main_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDashboardUploadResumesSessionPublishesFilesAndPreservesAnExactRetry(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(strconv.FormatBool(native), func(t *testing.T) {
			spine := initSpine(t)
			started := runMC(t, spineEnv(spine), "", "homie", "start", "--from", "dashboard:upload-test")
			if started.code != 0 {
				t.Fatal(started.stderr)
			}
			session := started.json["session_id"].(string)
			if native {
				registered := runMC(t, runJSONEnv(t, spine, session, "homie", "homie"), "",
					"run", "register-session", session, "--native-ref", "upload-native", "--file", "native.jsonl")
				if registered.code != 0 {
					t.Fatal(registered.stderr)
				}
			}
			ended := runMC(t, spineEnv(spine), "", "homie", "end", session, "--reason", "idle timeout")
			if ended.code != 0 {
				t.Fatal(ended.stderr)
			}
			generation := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
			writeGreenAttachmentCanaryReceipt(t, spine, generation)
			holdAttachmentHealthGuard(t, spine, generation)
			request := map[string]any{"session_id": session, "request_id": "stable-upload-1", "body": "Use these notes.", "files": []any{
				map[string]any{"name": "notes.txt", "media_type": "text/plain", "data": base64.StdEncoding.EncodeToString([]byte("A useful Project goal."))},
				map[string]any{"name": "image.png", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 255})},
			}}
			encoded, _ := json.Marshal(request)
			digest := sha256.Sum256([]byte{0, 1, 2, 255})
			name := hex.EncodeToString(digest[:])
			corrupt := filepath.Join(filepath.Dir(spine), "attachments", session, "in", "published", "sha256", name[:2], name)
			prepared := runMC(t, spineEnv(spine), "", "__attachment-tree-prepare", session)
			if prepared.code != 0 {
				t.Fatal(prepared.stderr)
			}
			if err := os.MkdirAll(filepath.Dir(corrupt), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(corrupt, []byte("external bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			failed := runMC(t, spineEnv(spine), string(encoded), "__dashboard-upload")
			if failed.code == 0 {
				t.Fatal("the upload accepted a corrupt destination")
			}
			db := openDB(t, spine)
			if count := queryInt(t, db, `SELECT count(*) FROM attachment_publications WHERE state='materialized'`); count != 1 {
				t.Fatalf("the failure did not follow the first file publication: %d, %s", count, failed.stderr)
			}
			if err := os.Remove(corrupt); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				result := runMC(t, spineEnv(spine), string(encoded), "__dashboard-upload")
				if result.code != 0 {
					t.Fatalf("upload %d: %s %s", i, result.stdout, result.stderr)
				}
			}
			history := runMC(t, spineEnv(spine), "", "homie", "history", session)
			if history.code != 0 {
				t.Fatal(history.stderr)
			}
			messages := history.json["messages"].([]any)
			if len(messages) != 1 {
				t.Fatalf("an upload retry created extra messages: %v", messages)
			}
			message := messages[0].(map[string]any)
			if message["body"] != "Use these notes." {
				t.Fatalf("message: %v", message)
			}
			attachments := message["attachments"].([]any)
			if len(attachments) != 2 {
				t.Fatalf("attachments: %v", attachments)
			}
			for i, raw := range attachments {
				attachment := raw.(map[string]any)
				served := runMC(t, spineEnv(spine), "", "__attachment-bytes", session, "in", attachment["sha256"].(string), strconv.Itoa(int(attachment["size"].(float64))))
				if served.code != 0 {
					t.Fatal(served.stderr)
				}
				body, err := os.ReadFile(served.json["host_path"].(string))
				want := []byte("A useful Project goal.")
				if i == 1 {
					want = []byte{0, 1, 2, 255}
				}
				if err != nil || string(body) != string(want) {
					t.Fatalf("attachment bytes: %q %v", body, err)
				}
			}
		})
	}
}

func TestDashboardUploadRejectsUnavailableAccessAndInvalidFiles(t *testing.T) {
	spine := initSpine(t)
	for _, input := range []string{
		`{"session_id":"../outside","request_id":"1","files":[{"name":"file","data":"YQ=="}]}`,
		`{"session_id":"h-test","request_id":"1","files":[{"name":"file","data":"invalid"}]}`,
		`{"session_id":"h-test","request_id":"1","files":[{"name":"file","data":"YQ=="}]}`,
	} {
		result := runMC(t, spineEnv(spine), input, "__dashboard-upload")
		if result.code == 0 {
			t.Fatalf("invalid upload passed: %s", input)
		}
	}
	agent := runMC(t, runJSONEnv(t, spine, "r-upload-agent", "agent", "Worker"), `{"body":"private upload"}`, "__dashboard-upload")
	if agent.code != 1 || strings.Contains(agent.stdout+agent.stderr, "private upload") {
		t.Fatalf("agent upload: %d %s %s", agent.code, agent.stdout, agent.stderr)
	}
}

func TestDashboardUploadRefusesAnEndedSessionWithoutADashboardBinding(t *testing.T) {
	spine := initSpine(t)
	started := runMC(t, spineEnv(spine), "", "homie", "start", "--from", "cli:upload-test")
	if started.code != 0 {
		t.Fatal(started.stderr)
	}
	session := started.json["session_id"].(string)
	ended := runMC(t, spineEnv(spine), "", "homie", "end", session, "--reason", "idle timeout")
	if ended.code != 0 {
		t.Fatal(ended.stderr)
	}
	generation := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	writeGreenAttachmentCanaryReceipt(t, spine, generation)
	holdAttachmentHealthGuard(t, spine, generation)
	encoded, _ := json.Marshal(map[string]any{
		"session_id": session, "request_id": "unbound-upload", "files": []any{
			map[string]any{"name": "notes.txt", "media_type": "text/plain", "data": base64.StdEncoding.EncodeToString([]byte("Notes."))},
		},
	})
	result := runMC(t, spineEnv(spine), string(encoded), "__dashboard-upload")
	if result.code == 0 {
		t.Fatal("The upload accepted a session without a dashboard binding.")
	}
	history := runMC(t, spineEnv(spine), "", "homie", "history", session)
	if history.code != 0 || history.json["status"] != "ended" {
		t.Fatalf("The session must remain ended: %s %s", history.stdout, history.stderr)
	}
	if len(history.json["messages"].([]any)) != 0 {
		t.Fatal("The refused upload created a message.")
	}
}
