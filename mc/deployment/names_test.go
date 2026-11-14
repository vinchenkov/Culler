package deployment

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeNamesPinDomainAndGrammar(t *testing.T) {
	names, err := RuntimeNames("/Users/alice/.culler")
	if err != nil {
		t.Fatal(err)
	}
	if names.Suffix != "676ccb230491" {
		t.Fatalf("suffix = %q, want pinned domain hash", names.Suffix)
	}
	if names.Volume != "mc-spine-676ccb230491" || names.Helper != "mc-helper-676ccb230491" || names.RuntimeVolume != "mc-runtime-676ccb230491" {
		t.Fatalf("names = %#v", names)
	}
	if _, err := RuntimeNames("relative/home"); err == nil {
		t.Fatal("relative MC_HOME received a deployment identity")
	}
}

func TestCanonicalHomeMakesSymlinkAliasesOneDeployment(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "real")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realParent, alias); err != nil {
		t.Fatal(err)
	}
	realHome, err := CanonicalHome(filepath.Join(realParent, "missing", "home"))
	if err != nil {
		t.Fatal(err)
	}
	aliasHome, err := CanonicalHome(filepath.Join(alias, "missing", "home"))
	if err != nil {
		t.Fatal(err)
	}
	if realHome != aliasHome {
		t.Fatalf("canonical homes differ: %q != %q", realHome, aliasHome)
	}
	realNames, _ := RuntimeNames(realHome)
	aliasNames, _ := RuntimeNames(aliasHome)
	if realNames != aliasNames {
		t.Fatalf("symlink aliases split deployment identity: %#v != %#v", realNames, aliasNames)
	}
}

func TestDifferentCanonicalHomesCannotShareRuntimeObjects(t *testing.T) {
	a, err := RuntimeNames("/Users/alice/.culler")
	if err != nil {
		t.Fatal(err)
	}
	b, err := RuntimeNames("/Users/alice/.culler-two")
	if err != nil {
		t.Fatal(err)
	}
	if a.Suffix == b.Suffix || a.Volume == b.Volume || a.Helper == b.Helper || a.RuntimeVolume == b.RuntimeVolume {
		t.Fatalf("different homes collided: %#v %#v", a, b)
	}
}

func TestSmokeWorkspaceBaseIsDeterministicSiblingNotHomeDescendant(t *testing.T) {
	home := "/Users/alice/.culler"
	base, err := SmokeWorkspaceBase(home)
	if err != nil {
		t.Fatal(err)
	}
	if base != "/Users/alice/.culler-smoke-workspaces" {
		t.Fatalf("smoke base = %q", base)
	}
	if base == home || filepath.Dir(base) != filepath.Dir(home) {
		t.Fatalf("smoke base %q is not a deployment sibling of %q", base, home)
	}
	if _, err := SmokeWorkspaceBase("/"); err == nil {
		t.Fatal("filesystem-root MC_HOME received a smoke workspace")
	}
}
