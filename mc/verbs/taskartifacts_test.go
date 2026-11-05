package verbs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// taFixture grants the artifact root of a workspace under its target and
// returns the canonical artifact root.
func taFixture(t *testing.T, grantWorkspace bool) (home, workspace, artifact string) {
	t.Helper()
	home = t.TempDir()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifact = ProjectArtifactRoot(workspace)
	if err := os.MkdirAll(artifact, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureMountAllowlist(home); err != nil {
		t.Fatal(err)
	}
	granted := artifact
	if grantWorkspace {
		granted = workspace
	}
	if _, err := EnsureProjectArtifactGrant(home, "project-53d8af33-b12", granted); err != nil {
		t.Fatal(err)
	}
	return home, workspace, artifact
}

func TestTaskArtifactMountResolvesTheTaskFolder(t *testing.T) {
	for name, tc := range map[string]struct {
		grantWorkspace bool
		destination    string
	}{
		"an artifact root grant": {false, "/mc/artifacts/project-53d8af33-b12/21"},
		"a workspace grant":      {true, "/mc/artifacts/project-53d8af33-b12/.mc-artifacts/21"},
	} {
		t.Run(name, func(t *testing.T) {
			home, workspace, artifact := taFixture(t, tc.grantWorkspace)
			folder := filepath.Join(artifact, "21")
			if err := os.MkdirAll(filepath.Join(folder, "packet"), 0o755); err != nil {
				t.Fatal(err)
			}
			// A host override of the artifact root must not move the container path.
			t.Setenv("MC_ARTIFACTS_ROOT", t.TempDir())
			bind, err := TaskArtifactMount(home, workspace, 21)
			if err != nil {
				t.Fatal(err)
			}
			if bind.Destination != tc.destination || bind.Source == nil || *bind.Source != folder {
				t.Fatalf("the bind is %s from %v, want %s from %s", bind.Destination, bind.Source, tc.destination, folder)
			}
		})
	}
}

func TestTaskArtifactMountRefusesAFolderOutsideTheRoot(t *testing.T) {
	home, workspace, artifact := taFixture(t, false)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(artifact, "21")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(artifact, "22"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(artifact, "22"), filepath.Join(artifact, "23")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifact, "24"), []byte("a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	for task, want := range map[int64]string{
		21: "The artifact files of task 21 resolve outside the artifact root.",
		23: "The artifact files of task 23 resolve outside the artifact root.",
		24: "The artifact files of task 24 are not a folder.",
	} {
		bind, err := TaskArtifactMount(home, workspace, task)
		var refusal *DomainError
		if !errors.As(err, &refusal) || refusal.Msg != want {
			t.Errorf("task %d gave %+v, %v, want the refusal %q", task, bind, err, want)
		}
	}
	unlisted := t.TempDir()
	if err := os.MkdirAll(ProjectArtifactRoot(unlisted), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := TaskArtifactMount(home, unlisted, 21); err == nil || !strings.Contains(err.Error(), "exactly one entry") {
		t.Fatalf("a Project outside the mount allowlist gave %v", err)
	}
}

func TestTaskArtifactMountReportsAnAbsentFolder(t *testing.T) {
	home, workspace, _ := taFixture(t, false)
	bind, err := TaskArtifactMount(home, workspace, 21)
	if err != nil {
		t.Fatal(err)
	}
	if bind.Destination != "/mc/artifacts/project-53d8af33-b12/21" || bind.Source != nil {
		t.Fatalf("the bind of an absent task folder is %s from %v, want no source", bind.Destination, bind.Source)
	}
}
