package verbs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// artifactPlaneFixture builds the two halves the resolver reads: a real host
// artifact root holding a real file, and a trusted mount allowlist that maps a
// target onto it. It returns the home and the host root.
func artifactPlaneFixture(t *testing.T, target string) (home, hostRoot string) {
	t.Helper()
	base := t.TempDir()
	home = filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	hostRoot = filepath.Join(base, "workspace", ".mc-artifacts")
	if err := os.MkdirAll(filepath.Join(hostRoot, "37", "packet"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostRoot, "37", "packet", "packet.md"), []byte("# packet 37\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	allowlist := fmt.Sprintf("version = 1\n\n[[allow]]\npath = %q\ntarget = %q\naccess = \"rw\"\n", hostRoot, target)
	if err := os.WriteFile(MountAllowlistPath(home), []byte(allowlist), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, hostRoot
}

// The round trip the dashboard depends on: a container path a Packager
// recorded resolves to the host bytes the operator can open. The allowlist is
// the ONLY reverse map, which is why the target must be one it carries.
func TestResolveArtifactHostPathMapsAContainerPathThroughTheAllowlist(t *testing.T) {
	home, hostRoot := artifactPlaneFixture(t, "llm-council")
	root := ArtifactsMountRoot()

	got, err := ResolveArtifactHostPath(home, root+"/llm-council/37/packet/packet.md")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	wantDir, err := filepath.EvalSymlinks(hostRoot)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wantDir, "37", "packet", "packet.md"); got != want {
		t.Fatalf("host path = %q, want %q", got, want)
	}
	body, err := os.ReadFile(got)
	if err != nil || string(body) != "# packet 37\n" {
		t.Fatalf("resolved path does not hold the artifact bytes: %q %v", body, err)
	}
}

// The refusals, each proving a distinct thing the resolver must not do. A
// target the allowlist does not carry is the load-bearing one: a fabricated
// `<root>/<made-up-target>/x` is shape-identical to a real plane, so nothing
// but allowlist membership can tell them apart.
func TestResolveArtifactHostPathRefusals(t *testing.T) {
	root := ArtifactsMountRoot()
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"outside the artifact root", "/tmp/packet.md", "not a container artifact-plane path"},
		{"the root itself", root, "not a container artifact-plane path"},
		{"a target with no file", root + "/llm-council", "names no artifact target and file"},
		{"an unlisted target", root + "/invented/37/packet/packet.md", "no mount allowlist entry carries artifact target"},
		{"a file that is not there", root + "/llm-council/99/packet/packet.md", "does not exist on the host"},
		{"traversal out of the plane", root + "/llm-council/../../etc/passwd", "not a container artifact-plane path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := artifactPlaneFixture(t, "llm-council")
			_, err := ResolveArtifactHostPath(home, tc.path)
			if err == nil {
				t.Fatalf("resolved %q, want refusal", tc.path)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// An agent holds the artifact plane read-write, so it can plant a symlink
// there aimed anywhere on the host. Without the resolve-then-contain check the
// dashboard would happily serve whatever it points at.
func TestResolveArtifactHostPathRefusesASymlinkOutOfThePlane(t *testing.T) {
	home, hostRoot := artifactPlaneFixture(t, "llm-council")
	secret := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(hostRoot, "37", "packet", "escape.md")
	if err := os.Symlink(secret, planted); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveArtifactHostPath(home, ArtifactsMountRoot()+"/llm-council/37/packet/escape.md")
	if err == nil {
		t.Fatal("resolved a symlink out of the plane")
	}
	if !strings.Contains(err.Error(), "outside its own plane") {
		t.Fatalf("refusal = %v, want it to name the plane escape", err)
	}

	// Mutation check in the other direction: a symlink that stays INSIDE the
	// plane is ordinary and must resolve, or the fence above would be
	// indistinguishable from refusing every symlink.
	inside := filepath.Join(hostRoot, "37", "packet", "alias.md")
	if err := os.Symlink(filepath.Join(hostRoot, "37", "packet", "packet.md"), inside); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveArtifactHostPath(home, ArtifactsMountRoot()+"/llm-council/37/packet/alias.md"); err != nil {
		t.Fatalf("an in-plane symlink must resolve: %v", err)
	}
}

// A directory is not an artifact. The dashboard serves what this returns, so
// "a path that exists" is not the question — "a file to send" is.
func TestResolveArtifactHostPathRefusesADirectory(t *testing.T) {
	home, _ := artifactPlaneFixture(t, "llm-council")
	_, err := ResolveArtifactHostPath(home, ArtifactsMountRoot()+"/llm-council/37/packet")
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("refusal = %v, want it to name the non-file", err)
	}
}

// An untrusted allowlist refuses rather than being read anyway: it is the
// authority the whole mapping rests on, and dispatch fences it identically.
func TestResolveArtifactHostPathRefusesAnUntrustedAllowlist(t *testing.T) {
	home, _ := artifactPlaneFixture(t, "llm-council")
	if err := os.Chmod(MountAllowlistPath(home), 0o666); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveArtifactHostPath(home, ArtifactsMountRoot()+"/llm-council/37/packet/packet.md")
	if err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("refusal = %v, want it to name the untrusted allowlist", err)
	}
}
