//go:build docker_boundary

package boundarydocker

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mc/substrate"
)

// A21 + A18 joint boundary proof (production mount layout):
// the agent-user container (uid 10002) reading a sidecar-less spine over the
// SAME shared RW volume the writer uses — the ACTIVE path (substrate.OpenReadOnly
// after A18: never an immutable fallback) must succeed, because the layout
// provides shared, sidecar-writable storage while the db file itself stays
// 0600 and the DSN stays mode=ro. The :ro twin documents the honest failure
// window C1 accepts: an unwritable sidecar location must FAIL, not degrade.
func TestA21AgentUserSpineReadsOnProductionLayout(t *testing.T) {
	requireDocker(t)
	if out, err := exec.Command("docker", "image", "inspect", "alpine:latest").CombinedOutput(); err != nil {
		if _, pullErr := exec.Command("docker", "pull", "alpine:latest").CombinedOutput(); pullErr != nil {
			t.Skipf("alpine:latest unavailable (pull failed): %s", firstLine(string(out)))
		}
	}

	// Cross-build the branch mc for the container (same discipline as the
	// operator transport suite).
	buildDir := t.TempDir()
	mcBin := filepath.Join(buildDir, "mc")
	build := exec.Command("go", "build", "-o", mcBin, "./cmd/mc")
	build.Dir = ".."
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build linux mc: %v\n%s", err, out)
	}

	// Provision a spine on the host and close the writer so the sidecars are
	// checkpointed away — the exact state that used to trigger the fallback.
	hostDir := t.TempDir()
	spinePath := filepath.Join(hostDir, "spine.db")
	db, err := substrate.Open(spinePath)
	if err != nil {
		t.Fatalf("provision spine: %v", err)
	}
	if err := substrate.Init(db); err != nil {
		t.Fatalf("init spine: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO meta (id, deployment_uuid, schema_id) VALUES (1, 'a21-joint', ?)`, substrate.CurrentSchemaID); err != nil {
		t.Fatalf("seed meta: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO activity (actor, kind, subject, detail) VALUES ('a21', 'a21.probe', 'joint', 'seeded for the mount proof')`); err != nil {
		t.Fatalf("seed activity: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range []string{spinePath + "-wal", spinePath + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			t.Fatalf("%s survived the writer close; the fixture needs the sidecar-less state", sidecar)
		}
	}
	raw, err := os.ReadFile(spinePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	hostHash := hex.EncodeToString(sum[:])

	docker := func(args ...string) (string, error) {
		out, err := exec.Command("docker", args...).CombinedOutput()
		return string(out), err
	}
	volumeName := func(kind string) string {
		return "mc-a21-" + kind + "-" + hostHash[:12]
	}
	seedVolume := func(t *testing.T, volume string) {
		t.Helper()
		out, err := docker("run", "--rm", "-v", volume+":/spine", "-v", hostDir+":/src:ro", "alpine:latest",
			"sh", "-c", "cp /src/spine.db /spine/spine.db && chown 10002:10002 /spine /spine/spine.db && chmod 700 /spine && chmod 600 /spine/spine.db")
		if err != nil {
			t.Fatalf("seed volume: %v\n%s", err, out)
		}
	}
	// --- positive: production layout (shared RW volume) ---
	rwVol := volumeName("rw")
	out, err := docker("volume", "create", rwVol)
	if err != nil {
		t.Fatalf("create rw volume: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", rwVol).Run() })
	seedVolume(t, rwVol)

	out, err = docker("run", "--rm", "--user", "10002:10002",
		"-v", rwVol+":/mc/spine", "-v", mcBin+":/mnt/mc:ro", "alpine:latest",
		"sh", "-c", "MC_SPINE=/mc/spine/spine.db /mnt/mc activity list --limit 5")
	if err != nil {
		t.Fatalf("A18 ACTIVE read failed on the production RW layout: %v\n%s", err, out)
	}
	if !strings.Contains(out, "a21.probe") {
		t.Fatalf("activity list output missing the seeded row: %s", out)
	}

	// Least-privilege invariants after the container read: the db file is
	// byte-identical and still 0600.
	out, err = docker("run", "--rm", "-v", rwVol+":/spine:ro", "alpine:latest",
		"sh", "-c", "sha256sum /spine/spine.db | cut -d' ' -f1 && stat -c %a /spine/spine.db")
	if err != nil {
		t.Fatalf("verify volume db: %v\n%s", err, out)
	}
	lines := strings.Fields(out)
	if len(lines) < 2 || lines[0] != hostHash || lines[1] != "600" {
		t.Fatalf("db file integrity/mode drifted after the container read: %q want %s then 600", out, hostHash)
	}

	// --- negative: read-only spine directory must fail honestly, never
	// silently degrade to an immutable read (C1 window, asserted not skipped) ---
	roVol := volumeName("ro")
	out, err = docker("volume", "create", roVol)
	if err != nil {
		t.Fatalf("create ro volume: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", roVol).Run() })
	seedVolume(t, roVol)
	if out, err := docker("run", "--rm", "--user", "10002:10002",
		"-v", roVol+":/mc/spine:ro", "-v", mcBin+":/mnt/mc:ro", "alpine:latest",
		"sh", "-c", "MC_SPINE=/mc/spine/spine.db /mnt/mc activity list --limit 5"); err == nil {
		t.Fatalf("the read-only directory unexpectedly served the sidecar-less spine (fallback regression?): %s", out)
	} else if !strings.Contains(strings.ToLower(out), "readonly") && !strings.Contains(strings.ToLower(out), "read-only") && !strings.Contains(strings.ToLower(out), "cantopen") {
		t.Fatalf("refusal did not name a driver read-only condition: %s", out)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
