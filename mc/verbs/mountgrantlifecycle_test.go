package verbs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/boundary"
)

// One dangling allow root stopped the whole live board.
//
// `boundary.ResolveAllowlist` resolves EVERY allow root, because its
// uniqueness law compares filesystem identity rather than strings. So a grant
// that outlives the directory it authorizes is not a leftover — it refuses
// every mounted dispatch in the deployment, for every Project and every
// role, permanently. A smoke run created exactly that: it deleted its
// transient workspace and left the artifact grant behind, and from that moment
// the repo Project's Worker refused `mount.source_missing` naming a path
// inside the archived smoke Project. The refusal envelope drops the message,
// so the board stopped with no readable cause and the fault looked like it
// belonged to the Project that was working.
//
// Two halves are fixed and each is proved here against its real consumer: the
// deleting side retracts its grant, and doctor runs the resolve gate it had
// been stopping one short of.

func grantFixture(t *testing.T) (home, workspace, artifact string) {
	t.Helper()
	home = t.TempDir()
	workspace = filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	artifact = ProjectArtifactRoot(workspace)
	if err := os.MkdirAll(artifact, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureMountAllowlist(home); err != nil {
		t.Fatal(err)
	}
	if granted, err := EnsureProjectArtifactGrant(home, "transient", artifact); err != nil || !granted {
		t.Fatalf("grant = %v, %v", granted, err)
	}
	return home, workspace, artifact
}

func resolvesAllowlist(t *testing.T, home string) error {
	t.Helper()
	body, err := os.ReadFile(MountAllowlistPath(home))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := boundary.ParseMountAllowlist(body)
	if err != nil {
		t.Fatalf("allowlist stopped parsing: %v", err)
	}
	_, err = boundary.ResolveAllowlist(parsed)
	return err
}

func TestRetractingAGrantRestoresEveryMountInTheDeployment(t *testing.T) {
	home, workspace, artifact := grantFixture(t)

	// A second Project that is entirely healthy, to prove the blast radius
	// is the DEPLOYMENT and not the Project whose directory went away.
	other := filepath.Join(t.TempDir(), "keeper")
	otherArtifact := ProjectArtifactRoot(other)
	if err := os.MkdirAll(otherArtifact, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureProjectArtifactGrant(home, "keeper", otherArtifact); err != nil {
		t.Fatal(err)
	}
	if err := resolvesAllowlist(t, home); err != nil {
		t.Fatalf("a healthy allowlist must resolve: %v", err)
	}

	// The teardown deletes the workspace — and, before the fix, stopped there.
	if err := os.RemoveAll(filepath.Dir(workspace)); err != nil {
		t.Fatal(err)
	}
	if err := resolvesAllowlist(t, home); err == nil {
		t.Fatal("a dangling allow root must refuse; without that this test proves nothing")
	}

	removed, err := RemoveProjectMountGrant(home, "transient", artifact)
	if err != nil || !removed {
		t.Fatalf("retract = %v, %v", removed, err)
	}
	if err := resolvesAllowlist(t, home); err != nil {
		t.Fatalf("retracting the dangling grant must restore the deployment: %v", err)
	}

	// The healthy Project's own grant survived intact — the retraction is
	// narrow, not a rewrite of operator policy.
	body, err := os.ReadFile(MountAllowlistPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), otherArtifact) {
		t.Fatalf("retraction removed an unrelated grant:\n%s", body)
	}
	if strings.Contains(string(body), artifact) {
		t.Fatalf("retraction left the dangling grant:\n%s", body)
	}
}

func TestRetractingAGrantIsNarrowAndIdempotent(t *testing.T) {
	home, _, artifact := grantFixture(t)

	// Wrong target: this is somebody else's row under the same path, and the
	// retraction may never touch it. Matching on path alone would.
	if removed, err := RemoveProjectMountGrant(home, "someone-else", artifact); err != nil || removed {
		t.Fatalf("retract with a foreign target = %v, %v; want no-op", removed, err)
	}
	if err := resolvesAllowlist(t, home); err != nil {
		t.Fatalf("a no-op retraction disturbed the allowlist: %v", err)
	}

	if removed, err := RemoveProjectMountGrant(home, "transient", artifact); err != nil || !removed {
		t.Fatalf("retract = %v, %v", removed, err)
	}
	// Absence is success: a teardown re-run must not fail on work already done.
	if removed, err := RemoveProjectMountGrant(home, "transient", artifact); err != nil || removed {
		t.Fatalf("second retract = %v, %v; want a silent no-op", removed, err)
	}
}

// Doctor was stopping one gate short of the dispatch planner: it trusted and
// PARSED the allowlist but never RESOLVED it, which is the gate that fails.
// Parsing is not resolving, and that distance is the whole outage.
func TestDoctorNamesAnAllowRootThatNoLongerExists(t *testing.T) {
	home, workspace, artifact := grantFixture(t)
	if err := checkMountAllowlist(home); err != nil {
		t.Fatalf("a healthy allowlist must pass doctor: %v", err)
	}

	if err := os.RemoveAll(filepath.Dir(workspace)); err != nil {
		t.Fatal(err)
	}
	err := checkMountAllowlist(home)
	if err == nil {
		t.Fatal("doctor must fail on a dangling allow root; it refuses every mounted dispatch")
	}
	// Naming the path is the point. The operator's recovery is to remove that
	// exact line, and the dispatch refusal they are staring at does not carry it.
	if !strings.Contains(err.Error(), artifact) {
		t.Fatalf("doctor must name the dangling root; got %v", err)
	}

	if _, rmErr := RemoveProjectMountGrant(home, "transient", artifact); rmErr != nil {
		t.Fatal(rmErr)
	}
	if err := checkMountAllowlist(home); err != nil {
		t.Fatalf("doctor must pass once the dangling grant is retracted: %v", err)
	}
}
