package verbs

// The launch-time recheck must catch every observable host swap between
// commit and Docker create/start: a canonical-path drift (a symlink component
// swap), an object replacement (a new inode), a kind, owner-uid, or mode-grant
// change. It must trust nothing about the plan file itself.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/boundary"
)

// mrWritePlan attests real temp sources into a plan and writes it as the
// resident would: an operator-owned 0600 sibling file.
func mrWritePlan(t *testing.T, dir string, entries []PrivateDispatchMountEntry) string {
	t.Helper()
	plan := PrivateDispatchMountPlan{Version: 1, Entries: entries}
	body, err := json.Marshal(&plan)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "run.mounts.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mrWriteFullPlan(t *testing.T, dir string, plan PrivateDispatchMountPlan) string {
	t.Helper()
	body, err := json.Marshal(&plan)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "run.mounts.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mrEntry(t *testing.T, source, dest, logical, access string) PrivateDispatchMountEntry {
	t.Helper()
	identity, err := boundary.ResolveSource(source)
	if err != nil {
		t.Fatalf("ResolveSource(%q): %v", source, err)
	}
	device, inode, uid, mode := maEvidence(t, source)
	kind := "file"
	if identity.IsDir {
		kind = "dir"
	}
	return PrivateDispatchMountEntry{
		Access: access, Destination: dest, Device: device, Inode: inode,
		Kind: kind, LogicalID: logical, Mode: mode, OwnerUID: uid, Source: identity.Canonical,
	}
}

func mrAssertDrift(t *testing.T, path, wantCode string) {
	t.Helper()
	_, err := MountRecheck(path)
	if err == nil {
		t.Fatal("drifted plan passed the recheck")
	}
	var de *DomainError
	if !errors.As(err, &de) || de.Code != wantCode {
		t.Fatalf("recheck error = %v, want code %s", err, wantCode)
	}
}

func TestMountRecheckPassesUnchangedEvidence(t *testing.T) {
	root := t.TempDir()
	dir := maMkdir(t, root, "artifact")
	file := filepath.Join(root, "note.md")
	if err := os.WriteFile(file, []byte("note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := mrWritePlan(t, root, []PrivateDispatchMountEntry{
		mrEntry(t, dir, "/mc/artifacts/art", "artifact:art", "rw"),
		mrEntry(t, file, "/mc/artifacts/note", "artifact:note", "ro"),
	})
	out, err := MountRecheck(path)
	if err != nil {
		t.Fatalf("unchanged evidence failed the recheck: %v", err)
	}
	if out["status"] != "ok" || out["entries"] != 2 {
		t.Fatalf("recheck result = %v", out)
	}
	if empty, err := MountRecheck(mrWritePlan(t, maMkdir(t, root, "empty-dir"), []PrivateDispatchMountEntry{})); err != nil || empty["entries"] != 0 {
		t.Fatalf("explicit empty plan = (%v, %v), want ok", empty, err)
	}
}

func TestMountRecheckFencesTheSyntheticHomeIdentityAndMode(t *testing.T) {
	root := t.TempDir()
	home := maID(t, maMkdir(t, root, "tool-home")).Canonical
	device, inode, uid, _ := maEvidence(t, home)
	plan := PrivateDispatchMountPlan{
		Version: 1, Entries: []PrivateDispatchMountEntry{},
		ToolHome: &PrivateDispatchPathIdentity{
			Canonical: home, Device: device, Inode: inode, OwnerUID: uid,
		},
	}
	path := mrWriteFullPlan(t, root, plan)
	if _, err := MountRecheck(path); err != nil {
		t.Fatalf("unchanged synthetic home failed recheck: %v", err)
	}
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	mrAssertDrift(t, path, boundary.CodeIdentityChanged)
}

func TestMountRecheckFencesASeedingWorkspaceIdentity(t *testing.T) {
	root := t.TempDir()
	source := maMkdir(t, root, "repo")
	canonical := maID(t, source).Canonical
	device, inode, uid, _ := maEvidence(t, canonical)
	plan := PrivateDispatchMountPlan{
		Version: 1, Entries: []PrivateDispatchMountEntry{},
		SeedingProjections: []PrivateDispatchSeedingProjection{{
			GitCoverKind: "seeding-source-git-cover", ObjectFormat: "sha1",
			ProjectionKind: "seeding-committed-projection", TargetRef: "HEAD",
			WorkspaceRoot: PrivateDispatchPathIdentity{
				Canonical: canonical, Device: device, Inode: inode, OwnerUID: uid,
			},
			ProjectID: "repo",
		}},
	}
	path := mrWriteFullPlan(t, root, plan)
	if _, err := MountRecheck(path); err != nil {
		t.Fatalf("unchanged seeding workspace failed recheck: %v", err)
	}
	if err := os.Rename(source, source+".retained"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	mrAssertDrift(t, path, boundary.CodeIdentityChanged)
}

func TestMountRecheckCatchesEveryDriftClass(t *testing.T) {
	cases := map[string]struct {
		drift func(t *testing.T, source string)
	}{
		"mode_grant_change": {func(t *testing.T, source string) {
			if err := os.Chmod(source, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		"object_replacement_same_path": {func(t *testing.T, source string) {
			if err := os.Rename(source, source+".retained"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(source, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		"kind_change_dir_to_file": {func(t *testing.T, source string) {
			if err := os.RemoveAll(source); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte("now a file"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		"symlink_component_swap": {func(t *testing.T, source string) {
			elsewhere := maMkdir(t, filepath.Dir(source), "elsewhere")
			if err := os.RemoveAll(source); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, source); err != nil {
				t.Fatal(err)
			}
		}},
		"source_removed": {func(t *testing.T, source string) {
			if err := os.RemoveAll(source); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			source := maMkdir(t, root, "artifact")
			path := mrWritePlan(t, root, []PrivateDispatchMountEntry{
				mrEntry(t, source, "/mc/artifacts/art", "artifact:art", "rw"),
			})
			tc.drift(t, source)
			mrAssertDrift(t, path, boundary.CodeIdentityChanged)
		})
	}
}

func TestMountRecheckRefusesUntrustedOrMalformedPlanFile(t *testing.T) {
	root := t.TempDir()
	source := maMkdir(t, root, "artifact")
	path := mrWritePlan(t, root, []PrivateDispatchMountEntry{
		mrEntry(t, source, "/mc/artifacts/art", "artifact:art", "rw"),
	})

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	mrAssertDrift(t, path, boundary.CodeAllowlistUntrusted)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "plan-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	mrAssertDrift(t, link, boundary.CodeAllowlistUntrusted)

	for name, body := range map[string]string{
		"unknown_field": `{"entries":[],"version":1,"extra":true}`,
		"trailing_data": `{"entries":[],"version":1}{"entries":[],"version":1}`,
		"not_json":      "not a plan\n",
		"wrong_version": `{"entries":[],"version":2}`,
	} {
		t.Run(name, func(t *testing.T) {
			bad := filepath.Join(root, name+".json")
			if err := os.WriteFile(bad, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := MountRecheck(bad); err == nil {
				t.Fatal("malformed plan file passed the recheck")
			}
		})
	}
}

// The recheck's own byte budget: a structurally valid plan file past 32 KiB
// refuses before any per-entry work, whatever the resident wrote.
func TestMountRecheckRefusesOversizedPlanFile(t *testing.T) {
	root := t.TempDir()
	source := maMkdir(t, root, "artifact")
	entries := make([]PrivateDispatchMountEntry, 0, 9)
	for i := 0; i < 9; i++ {
		entry := mrEntry(t, source,
			fmt.Sprintf("/mc/artifacts/bulk-%d", i),
			fmt.Sprintf("artifact:bulk-%d:%s", i, strings.Repeat("x", 4000)), "rw")
		entries = append(entries, entry)
	}
	path := mrWritePlan(t, root, entries)
	if _, err := MountRecheck(path); err == nil {
		t.Fatal("an oversized plan file passed the recheck")
	}
}
