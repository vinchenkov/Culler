package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mc/substrate"
	"mc/verbs"
)

func runMCRaw(t *testing.T, env []string, stdin []byte, args ...string) (int, []byte, string) {
	t.Helper()
	cmd := exec.Command(mcBin, args...)
	cmd.Env = hermeticEnv(env...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	return code, stdout.Bytes(), stderr.String()
}

func TestPrivateSpineTransferCommandsStayFixedScopeAndRoundTrip(t *testing.T) {
	spine := filepath.Join(t.TempDir(), "spine.db")
	initialized := runMC(t, spineEnv(spine), onboardSpineFrame(t), "__onboard-spine")
	if initialized.code != 0 {
		t.Fatalf("initialize: %s", initialized.stderr)
	}
	uuid := initialized.json["deployment_uuid"].(string)
	frame, err := json.Marshal(map[string]any{
		"protocol_version": 1, "release_build_id": "development",
		"control_version": verbs.GatewayControlVersion, "spine_schema_id": substrate.CurrentSchemaID,
		"config_schema_version": 1, "deployment_uuid": uuid,
		// Retention is host authority the helper echoes; a frame that omits it
		// is refused rather than defaulted.
		"backup_retain": 48,
	})
	if err != nil {
		t.Fatal(err)
	}

	withoutScope := runMC(t, nil, string(frame), "__backup-spine")
	if withoutScope.code != 1 || !strings.Contains(withoutScope.stderr, "fixed spine scope") {
		t.Fatalf("backup without scope: code=%d stderr=%q", withoutScope.code, withoutScope.stderr)
	}
	code, stream, diagnostics := runMCRaw(t, spineEnv(spine), frame, "__backup-spine")
	if code != 0 || len(stream) < 4096 || diagnostics != "" {
		t.Fatalf("backup stream: code=%d bytes=%d stderr=%q", code, len(stream), diagnostics)
	}
	if err := os.Remove(spine); err != nil {
		t.Fatal(err)
	}
	for _, sibling := range []string{spine + "-wal", spine + "-shm"} {
		_ = os.Remove(sibling)
	}
	code, restored, diagnostics := runMCRaw(t, spineEnv(spine), stream, "__restore-spine")
	if code != 0 || diagnostics != "" {
		t.Fatalf("restore stream: code=%d stdout=%q stderr=%q", code, restored, diagnostics)
	}
	var receipt map[string]any
	if err := json.Unmarshal(restored, &receipt); err != nil || receipt["deployment_uuid"] != uuid {
		t.Fatalf("restore receipt=%v err=%v", receipt, err)
	}
	replayCode, _, replayDiagnostics := runMCRaw(t, spineEnv(spine), stream, "__restore-spine")
	if replayCode != 0 || replayDiagnostics != "" {
		t.Fatalf("restore replay code=%d stderr=%q", replayCode, replayDiagnostics)
	}
}
