//go:build docker_e2e

// The lease-free Collie conversation loop, end to end. No pipeline task exists,
// so every tick's pipeline Decide() is idle and the dispatch crossing runs the
// Collie wake selector instead: an operator message mints a launch generation,
// the resident spawns and binds a tier:"collie" container (no lease, no
// heartbeat), the in-container runner reports runner-started, claims the
// pending turn, runs the fake harness, and posts the reply. The test invokes
// only host verbs; the wake, the container, and the reply are all timer-driven.
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Docker Desktop reports a bind's Source two ways for the same directory: the
// host path passed to --mount, or that path under /host_mnt, the VM's own mount
// point for shared host storage. Which one comes back is not stable — it
// differs per mount within one container. Production already collapses the two
// spellings (mc/cmd/mc/attachment_canary_prod.go, and the account in
// resident/src/collie-envelope.ts); a fixture that compares the raw string
// refuses a correct container, which is what this one did.
//
// Nothing is widened past the prefix: the remainder must begin at a separator
// and must then match exactly.
func normalizeVMBindSource(source string) string {
	const vmPrefix = "/host_mnt"
	if remainder := strings.TrimPrefix(source, vmPrefix); remainder != source &&
		strings.HasPrefix(remainder, "/") {
		return remainder
	}
	return source
}

func TestCollieConversationDockerBoundary(t *testing.T) {
	f := setup(t)
	// Keep the first turn alive long enough to inspect the production Collie
	// create envelope and exercise its already-live attachment mounts.
	if err := os.WriteFile(filepath.Join(f.base, "behaviors", "collie.json"), []byte(`{"steps":[
		{"do":"sleep","seconds":8},
		{"do":"succeed","output":"collie ack"}]}`), 0o644); err != nil {
		t.Fatalf("write attachment-mount Collie behavior: %v", err)
	}

	// A Collie session bound to a dashboard channel. It starts active with
	// last_activity_at = now, so it is well inside collie_idle_timeout_s.
	start := f.mcOK("", "collie", "start", "--from", "dashboard:chan-1")
	session, ok := start["session_id"].(string)
	if !ok || session == "" {
		t.Fatalf("mc collie start returned no session_id: %v", start)
	}

	// From here the real timer drives.
	//
	// The Collie tier is lease-free, but this fixture does NOT get a quiet
	// dispatcher, and believing otherwise is what made this test flaky. With no
	// task on the board, dispatch step (4) has nothing else to do and spawns a
	// real Strategist container EVERY tick — the park-don't-pause trap,
	// measured here as 29 `strategist(propose)` runs in one 40-second run. The
	// Collie stage pre-empts none of those: a Collie wake is the first element of
	// a tick's action list beside that tick's pipeline effect, and at a 500 ms
	// tick a cold `docker run` holds the very first tick for around twelve
	// seconds while "tick skipped: previous tick still in flight" repeats. So
	// the Collie container's arrival is not a fixed cost from test start; it is
	// one cold container start behind it.
	f.startResident()

	// The operator sends one inbound turn.
	send := f.mcOK("", "collie", "send", session, "--from", "dashboard:chan-1", "--body", "ping")
	if send["message_id"] == nil {
		t.Fatalf("mc collie send returned no message_id: %v", send)
	}

	containerName := "mc-collie-" + session
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
	})
	// When this wait times out, the ONE fact that separates the two causes is
	// arithmetic the log cannot show: a `collie-stop … reason: idle` under a
	// 1800 s idle timeout is only reachable if the spine clock stepped forward
	// past `last_activity_at`, which is the known Docker-VM clock class and not
	// a defect in the wake path. Capture both, because the alternative is
	// inferring it from a resident line that says only "idle".
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		row := f.mcOK("", "collie", "list")
		t.Logf("collie registry at failure: %v", row)
	})
	// This wait is where the test fails when it fails, and 30 s is not the
	// reason. The known cause is outside Culler: the Docker daemon on the host
	// degrades intermittently under repeated lane load, and a `docker desktop
	// restart` clears it. A sick daemon is not slow by a bounded factor; it is
	// unavailable. So on timeout the test asks the daemon how it is, and says so.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		out, err := exec.Command("docker", "info", "--format",
			"server={{.ServerVersion}} containers={{.Containers}} running={{.ContainersRunning}}").CombinedOutput()
		if err != nil {
			t.Logf("docker daemon health at failure: UNHEALTHY — `docker info` failed: %v: %s",
				err, strings.TrimSpace(string(out)))
			t.Log("this is the known cause of this test's flake; try `docker desktop restart` before believing a Culler defect")
			return
		}
		t.Logf("docker daemon health at failure: %s", strings.TrimSpace(string(out)))
	})
	f.waitFor(30*time.Second, "the production Collie carries exact attachment binds", func() (bool, string) {
		// CombinedOutput, not Output: `.Output()` returns a non-nil error for ANY
		// non-zero exit, so "no such object" and "the daemon refused / was busy /
		// the context is wrong" produced the identical note. Every failure of this
		// test has been read through that note, which means the recorded evidence
		// — "the container is never observed" — was only ever the weaker claim
		// "inspect never succeeded". Carry docker's own words so the next failure
		// says which one it was.
		out, err := exec.Command("docker", "inspect", containerName).CombinedOutput()
		if err != nil {
			note := fmt.Sprintf("docker inspect %s failed: %v: %s",
				containerName, err, strings.TrimSpace(string(out)))
			// A missing container consumes the wait budget. A daemon error does not describe the state of Culler.
			// Docker versions use different letter cases for the missing-container message.
			if !strings.Contains(strings.ToLower(string(out)), "no such object") {
				return false, outageNote + note
			}
			return false, note
		}
		var records []struct {
			Mounts []struct {
				Destination string
				RW          bool
				Source      string
				Type        string
			}
		}
		if err := json.Unmarshal(out, &records); err != nil || len(records) != 1 {
			return false, "Collie inspect is malformed"
		}
		want := map[string]struct {
			source string
			rw     bool
		}{
			"/mc/attachments/in": {
				source: filepath.Join(f.home, "attachments", session, "in", "published"),
				rw:     false,
			},
			"/mc/private/attachments/out": {
				source: filepath.Join(f.home, "attachments", session, "out"),
				rw:     true,
			},
		}
		seen := map[string]bool{}
		for _, mount := range records[0].Mounts {
			if mount.Destination == "/mc/private" {
				return false, "Collie replaced the image private gate"
			}
			expected, ok := want[mount.Destination]
			if !ok {
				continue
			}
			if mount.Type != "bind" || normalizeVMBindSource(mount.Source) != expected.source ||
				mount.RW != expected.rw {
				// Name what drifted. `waitFor` keeps only the LAST note, and the
				// container is `--rm`: once it exits, every later poll reports
				// "no such object", which is what the failure then reads as. A
				// mismatch that says only "drifted" is therefore invisible —
				// the recorded evidence becomes "the container was never
				// created" for a container that ran.
				return false, fmt.Sprintf(
					"attachment bind drifted at %s: type=%s source=%q rw=%v, want source=%q rw=%v",
					mount.Destination, mount.Type, mount.Source, mount.RW, expected.source, expected.rw)
			}
			seen[mount.Destination] = true
		}
		if len(seen) != len(want) {
			return false, "one or both attachment binds are absent"
		}
		return true, ""
	})
	f.waitFor(15*time.Second, "the attachment-bound Collie starts", func() (bool, string) {
		out, err := exec.Command(
			"docker", "inspect", "--format", "{{.State.Running}}", containerName,
		).CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "true" {
			return false, "Collie is not running yet"
		}
		return true, ""
	})
	const inboundProbeDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	inboundShard := filepath.Join(
		f.home, "attachments", session, "in", "published", "sha256", "aa",
	)
	if err := os.Mkdir(inboundShard, 0o700); err != nil {
		t.Fatalf("create production Collie inbound probe shard: %v", err)
	}
	inboundProbe := filepath.Join(inboundShard, inboundProbeDigest)
	if err := os.WriteFile(inboundProbe, []byte("production attachment bind\n"), 0o444); err != nil {
		t.Fatalf("publish production Collie inbound probe: %v", err)
	}
	if out, err := exec.Command(
		"docker", "exec", "--user", "10002:10002", containerName,
		"cat", "/mc/attachments/in/sha256/aa/"+inboundProbeDigest,
	).CombinedOutput(); err != nil || string(out) != "production attachment bind\n" {
		t.Fatalf("warm Collie inbound attachment read = err %v output %q", err, out)
	}
	if out, err := exec.Command(
		"docker", "exec", "--user", "10002:10002", containerName,
		"ls", "/mc/private/attachments/out",
	).CombinedOutput(); err == nil || !strings.Contains(strings.ToLower(string(out)), "permission denied") {
		t.Fatalf("model-user outbound traverse = err %v output %q", err, out)
	}

	// The trace mount: the session folder of a pipeline run is readable below
	// /mc/traces, and the mount refuses each write. The trace is synthetic.
	const tracedRun = "trace-mount-probe"
	const traceRecord = `{"type":"assistant","message":{"content":[{"type":"text","text":"synthetic trace"}]}}` + "\n"
	tracedSession := filepath.Join(f.home, "sessions", tracedRun)
	if err := os.MkdirAll(tracedSession, 0o755); err != nil {
		t.Fatalf("create the synthetic session folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tracedSession, "native.jsonl"), []byte(traceRecord), 0o644); err != nil {
		t.Fatalf("write the synthetic trace: %v", err)
	}
	tracePath := "/mc/traces/" + tracedRun + "/native.jsonl"
	if out, err := exec.Command(
		"docker", "exec", "--user", "10002:10002", containerName, "cat", tracePath,
	).CombinedOutput(); err != nil || string(out) != traceRecord {
		t.Fatalf("Collie trace read = err %v output %q", err, out)
	}
	for _, write := range []string{
		"echo changed >> " + tracePath,
		"echo new > /mc/traces/" + tracedRun + "/new.jsonl",
		"mkdir /mc/traces/new-session",
	} {
		out, err := exec.Command(
			"docker", "exec", "--user", "10002:10002", containerName, "sh", "-c", write,
		).CombinedOutput()
		if err == nil || !strings.Contains(strings.ToLower(string(out)), "read-only file system") {
			t.Fatalf("Collie trace write %q = err %v output %q, want a read-only refusal", write, err, out)
		}
	}
	if body, err := os.ReadFile(filepath.Join(tracedSession, "native.jsonl")); err != nil || string(body) != traceRecord {
		t.Fatalf("the synthetic trace changed: err %v body %q", err, body)
	}

	// The lease-free tick wakes a container off pipeline-idle; the runner claims
	// the turn and answers with the fake collie behavior's fixed reply. The
	// pipeline lease must stay free throughout — a Collie wake never takes it.
	f.waitFor(120*time.Second, "the collie reply lands in the conversation", func() (bool, string) {
		if lease := f.lease(project); lease["run_id"] != nil {
			return false, "pipeline lease is held by a Collie wake"
		}
		h := f.mcOK("", "collie", "history", session)
		messages, _ := h["messages"].([]any)
		for _, m := range messages {
			row, _ := m.(map[string]any)
			if row["direction"] == "reply" && row["body"] == "collie ack" {
				return true, ""
			}
		}
		return false, "no reply yet"
	})

	// The wake generation is recorded on the session, and it started (the runner
	// reported runner-started, which stamps launch_started_at via the fence).
	list := f.mcOK("", "collie", "list")
	sessions, _ := list["sessions"].([]any)
	found := false
	for _, s := range sessions {
		row, _ := s.(map[string]any)
		if row["session_id"] == session || row["id"] == session {
			found = true
		}
	}
	if !found {
		t.Fatalf("collie list does not show the active session %q: %v", session, list)
	}
}

// collieReplyCount counts reply-direction turns carrying the fixed fake reply.
func collieReplyCount(f *fixture, session string) int {
	h := f.mcOK("", "collie", "history", session)
	messages, _ := h["messages"].([]any)
	n := 0
	for _, m := range messages {
		row, _ := m.(map[string]any)
		if row["direction"] == "reply" && row["body"] == "collie ack" {
			n++
		}
	}
	return n
}

// collieSessionRow reads one session's `mc collie list` row (nil if absent).
func collieSessionRow(f *fixture, session string) map[string]any {
	list := f.mcOK("", "collie", "list")
	sessions, _ := list["sessions"].([]any)
	for _, s := range sessions {
		row, _ := s.(map[string]any)
		if row["id"] == session {
			return row
		}
	}
	return nil
}

// TestCollieResumeDockerBoundary proves the record-only resume path end to
// end: a conversation is answered, its native locator gets
// registered, the session is ended, then `mc collie resume` reactivates it and
// a fresh inbound turn is answered on a new wake — proving the resume clears
// the dead launch generation so the reactivated session can re-wake.
func TestCollieResumeDockerBoundary(t *testing.T) {
	f := setup(t)
	start := f.mcOK("", "collie", "start", "--from", "dashboard:chan-1")
	session := start["session_id"].(string)
	f.startResident()

	// First turn: the runner answers and registers the native locator.
	f.mcOK("", "collie", "send", session, "--from", "dashboard:chan-1", "--body", "ping")
	f.waitFor(120*time.Second, "first reply lands", func() (bool, string) {
		if collieReplyCount(f, session) >= 1 {
			return true, ""
		}
		return false, "no reply yet"
	})
	f.waitFor(30*time.Second, "native locator registered (enables resume)", func() (bool, string) {
		if row := collieSessionRow(f, session); row != nil && row["native_session_ref"] != nil {
			return true, ""
		}
		return false, "native_session_ref still null"
	})

	// End the session, then resume it (host record-only transition).
	f.mcOK("", "collie", "end", session, "--reason", "resume test")
	f.waitFor(15*time.Second, "session ends", func() (bool, string) {
		if row := collieSessionRow(f, session); row != nil && row["status"] == "ended" {
			return true, ""
		}
		return false, "still active"
	})
	res := f.mcOK("", "collie", "resume", session, "--from", "dashboard:chan-1")
	if res["status"] != "active" {
		t.Fatalf("resume did not reactivate: %v", res)
	}

	// A fresh turn on the resumed session is answered on a new wake — this only
	// works if resume cleared the ended generation's stale launch id.
	f.mcOK("", "collie", "send", session, "--from", "dashboard:chan-1", "--body", "ping2")
	f.waitFor(120*time.Second, "second reply after resume", func() (bool, string) {
		if collieReplyCount(f, session) >= 2 {
			return true, ""
		}
		return false, "resumed session has not answered the new turn"
	})
}
