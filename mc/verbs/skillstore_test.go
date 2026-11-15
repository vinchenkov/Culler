package verbs

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"mc/dispatch"
)

// The store preserves the Strategist directory for existing operator skills.
func TestSkillStoreRoleDirsPreserveOperatorSkillPaths(t *testing.T) {
	entries, err := fs.ReadDir(roleSkillFS, "skills")
	if err != nil {
		t.Fatal(err)
	}
	var shipped []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != CollieSkillBasename && e.Name() != SharedSkillBasename {
			name := e.Name()
			if name == "strategist" {
				name = "strategist-propose"
			}
			shipped = append(shipped, name)
		}
	}
	sort.Strings(shipped)
	if got := SkillStoreRoleDirs(); !reflect.DeepEqual(got, shipped) {
		t.Fatalf("SkillStoreRoleDirs = %v, shipped = %v", got, shipped)
	}
	want := []string{"arbiter", "packager", "scribe", "setup",
		"strategist-propose", "verifier", "worker"}
	if !reflect.DeepEqual(shipped, want) {
		t.Fatalf("operator skill directories = %v, want %v", shipped, want)
	}
}

func TestEnsureSkillStoreCreatesTheScaffoldShape(t *testing.T) {
	home := t.TempDir()
	changed, err := EnsureSkillStore(home)
	if err != nil || !changed {
		t.Fatalf("first ensure: changed=%v err=%v", changed, err)
	}
	for _, dir := range []string{"skills", "skills/collie"} {
		st, err := os.Stat(filepath.Join(home, dir))
		if err != nil || !st.IsDir() {
			t.Fatalf("%s: stat=%v err=%v", dir, st, err)
		}
	}
	changed, err = EnsureSkillStore(home)
	if err != nil || changed {
		t.Fatalf("second ensure: changed=%v err=%v", changed, err)
	}
	// The scaffold leaves the Project half to registration.
	if _, err := os.Stat(filepath.Join(home, "skills", "projects")); !os.IsNotExist(err) {
		t.Fatalf("projects directory exists before any Project: %v", err)
	}
}

func TestExtendSkillStoreCreatesOneDirectoryPerRole(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureSkillStore(home); err != nil {
		t.Fatal(err)
	}
	changed, err := ExtendSkillStore(home, "ws-one")
	if err != nil || !changed {
		t.Fatalf("extend: changed=%v err=%v", changed, err)
	}
	entries, err := os.ReadDir(filepath.Join(home, "skills", "projects", "ws-one"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		if !e.IsDir() {
			t.Fatalf("%s is not a directory", e.Name())
		}
		got = append(got, e.Name())
	}
	if !reflect.DeepEqual(got, SkillStoreRoleDirs()) {
		t.Fatalf("role directories = %v, want %v", got, SkillStoreRoleDirs())
	}
	// Idempotent, and an operator's skill in one of them survives a rerun.
	skill := filepath.Join(home, "skills", "projects", "ws-one", "worker", "deploy", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: deploy\ndescription: d\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err = ExtendSkillStore(home, "ws-one")
	if err != nil || changed {
		t.Fatalf("rerun: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("operator skill lost on rerun: %v", err)
	}
}

// A home without a store is not a deployment home as seen from this
// process — the helper container and the agent containers resolve a private
// MC_HOME that was never scaffolded — so nothing is grown there.
func TestExtendSkillStoreLeavesAHomeWithoutAStoreAlone(t *testing.T) {
	home := t.TempDir()
	changed, err := ExtendSkillStore(home, "ws-one")
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(filepath.Join(home, "skills")); !os.IsNotExist(err) {
		t.Fatalf("store grown without a root: %v", err)
	}
}

func TestSkillStoreRefusesAnIdentifierThatCannotNameADirectory(t *testing.T) {
	home := t.TempDir()
	if _, err := EnsureSkillStore(home); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "a\x00b"} {
		if _, err := ExtendSkillStore(home, id); err == nil {
			t.Errorf("ExtendSkillStore(%q) accepted", id)
		}
		if _, err := RoleSkillStoreRelDir(id, dispatch.RoleWorker); err == nil {
			t.Errorf("RoleSkillStoreRelDir(%q) accepted", id)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "projects")); !os.IsNotExist(err) {
		t.Fatalf("a refused identifier still grew the store: %v", err)
	}
}

// The effect names the operator skill directory relative to MC_HOME.
func TestRoleSkillStoreRelDirPreservesOperatorSkillPaths(t *testing.T) {
	cases := map[dispatch.Role]string{
		dispatch.RoleWorker:     "skills/projects/ws-one/worker",
		dispatch.RoleStrategist: "skills/projects/ws-one/strategist-propose",
	}
	for role, want := range cases {
		got, err := RoleSkillStoreRelDir("ws-one", role)
		if err != nil || got != want {
			t.Errorf("%s: got %q err=%v, want %q", role, got, err, want)
		}
	}
	if _, err := RoleSkillStoreRelDir("ws-one", dispatch.Role("janitor")); err == nil {
		t.Fatal("unknown role got a store directory")
	}
	if CollieSkillStoreRelDir != "skills/collie" {
		t.Fatalf("CollieSkillStoreRelDir = %q", CollieSkillStoreRelDir)
	}
}

func TestCollieWakeEffectCarriesTheCollieStoreDir(t *testing.T) {
	effect, err := collieWakeEffect("h-1", "l-1", "fresh", "fake/fake", "mc-collie-h-1", "sessions/h-1",
		sql.NullString{}, sql.NullString{}, PrivateRuntimeBinding{}, []string{}, 1800)
	if err != nil {
		t.Fatal(err)
	}
	if effect["skill_store_dir"] != "skills/collie" {
		t.Fatalf("skill_store_dir = %v", effect["skill_store_dir"])
	}
}
