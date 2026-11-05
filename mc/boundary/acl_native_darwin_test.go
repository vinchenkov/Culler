//go:build darwin

package boundary_test

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"

	"mc/boundary"
)

func changeNativeACL(t *testing.T, path string, args ...string) {
	t.Helper()
	cmd := exec.Command("chmod", append(args, path)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("chmod %v = %v: %s", args, err, out)
	}
}

func TestTrustPolicyFileReadsNativeDarwinACL(t *testing.T) {
	uid := os.Getuid()

	t.Run("non-owner read grant changes and restores verdict", func(t *testing.T) {
		path := writeFile(t, filepath.Join(t.TempDir(), "policy"), 0o600)
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		beforeBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := boundary.TrustPolicyFile(path, uid); err != nil {
			t.Fatalf("ordinary policy = %v", err)
		}

		changeNativeACL(t, path, "+a", "everyone allow read")
		after, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		afterBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(before, after) || before.Mode().Perm() != after.Mode().Perm() || string(beforeBytes) != string(afterBytes) {
			t.Fatal("fixture changed inode, mode, or bytes instead of ACL only")
		}
		if err := boundary.TrustPolicyFile(path, uid); codeOf(t, err) != boundary.CodeAllowlistUntrusted {
			t.Fatalf("granting ACL code = %q, want %q (error: %v)",
				codeOf(t, err), boundary.CodeAllowlistUntrusted, err)
		}

		changeNativeACL(t, path, "-N")
		if err := boundary.TrustPolicyFile(path, uid); err != nil {
			t.Fatalf("policy after ACL removal = %v", err)
		}
	})

	t.Run("deny-only ACL is not a grant", func(t *testing.T) {
		path := writeFile(t, filepath.Join(t.TempDir(), "policy"), 0o600)
		changeNativeACL(t, path, "+a", "everyone deny read")
		if err := boundary.TrustPolicyFile(path, uid); err != nil {
			t.Fatalf("deny-only ACL = %v, want accept", err)
		}
	})

	t.Run("owner-only allow is not a non-owner grant", func(t *testing.T) {
		current, err := user.Current()
		if err != nil {
			t.Fatal(err)
		}
		path := writeFile(t, filepath.Join(t.TempDir(), "policy"), 0o600)
		// chmod's ACL grammar accepts bare user-or-group names. Keep the
		// fixture deterministic by selecting the user namespace explicitly;
		// a group ACE is correctly a non-owner grant and must be rejected.
		changeNativeACL(t, path, "+a", "user:"+current.Username+" allow read")
		if err := boundary.TrustPolicyFile(path, uid); err != nil {
			listing, _ := exec.Command("ls", "-lden", path).CombinedOutput()
			lookup, _ := exec.Command("dsmemberutil", "getuuid", "-U", current.Username).CombinedOutput()
			t.Fatalf("owner-only allow ACL = %v, want accept; native ACL:\n%slookup UUID: %s", err, listing, lookup)
		}
	})
}

func TestTrustHomeDirReadsNativeDarwinACL(t *testing.T) {
	path := mkdir(t, filepath.Join(t.TempDir(), "mc-home"), 0o700)
	changeNativeACL(t, path, "+a", "everyone allow list,search")
	if err := boundary.TrustHomeDir(path, os.Getuid()); codeOf(t, err) != boundary.CodeAllowlistUntrusted {
		t.Fatalf("MC_HOME granting ACL code = %q, want %q (error: %v)",
			codeOf(t, err), boundary.CodeAllowlistUntrusted, err)
	}
}

func TestOperatorHomeDoesNotAcquireMCHomeACLSeam(t *testing.T) {
	home := mkdir(t, filepath.Join(t.TempDir(), "operator-home"), 0o700)
	changeNativeACL(t, home, "+a", "everyone allow list,search")
	if _, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{Home: home}, os.Getuid()); err != nil {
		t.Fatalf("operator HOME with granting ACL = %v, want accepted outside MC_HOME trust seam", err)
	}
}
