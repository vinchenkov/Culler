package boundary_test

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"mc/boundary"
)

// The injected HOME is validated, not trusted. The validation has five legs
// and deliberately no mode leg and no ACL leg.
//
// The permit side comes first. A suite that pins only the deny side can stay
// green under a design in which no container launches. TrustHomeDir has a
// mode leg that rejects the real macOS HOME, so the validation must not
// route through it.

func homeOf(t *testing.T, in boundary.JurisdictionInput, uid int) error {
	t.Helper()
	_, err := boundary.ResolveJurisdiction(in, uid)
	return err
}

// THE test of this decision. If validateHome ever acquires a mode leg, or is
// routed through TrustHomeDir/trustedOwnerMode, this fails — and nothing else in
// the package would.
//
// A stock macOS HOME is drwxr-x--- (0750). trustedOwnerMode rejects
// perm&0o077 != 0, and 0750 & 0o077 = 0o050. So the "obvious" implementation
// refuses the operator's real HOME and every mount plan on the machine dies.
// Every other HOME in this package is a 0700 temp dir, which is precisely why a
// 0700-only fixture would leave the suite structurally unable to see it.
func TestValidateHomeAcceptsTheRealOperatorHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no real HOME available: %v", err)
	}
	info, err := os.Lstat(home)
	if err != nil {
		t.Skipf("cannot stat real HOME %q: %v", home, err)
	}

	if err := homeOf(t, boundary.JurisdictionInput{Home: home}, os.Getuid()); err != nil {
		t.Fatalf("the REAL operator HOME %q (mode %v) was REJECTED: %v\n"+
			"Every mount plan on this machine would fail. The validation has five legs and NO mode leg; "+
			"it must not route through TrustHomeDir or trustedOwnerMode.",
			home, info.Mode(), err)
	}

	// Non-inertness: prove the trap is actually armed on this machine, so a future
	// reader knows the test above is load-bearing rather than incidentally green.
	if err := boundary.TrustHomeDir(home, os.Getuid()); err == nil {
		t.Logf("NOTE: TrustHomeDir accepts this HOME (mode %v), so the TrustHomeDir trap is "+
			"NOT exercised on this machine. The test above still pins the permit side, but a "+
			"reroute through TrustHomeDir would not be caught here.", info.Mode())
	} else {
		t.Logf("trap armed: TrustHomeDir rejects the real HOME (mode %v) with %v — "+
			"routing the validation through it would break the assertion above", info.Mode(), err)
	}
}

// The mode legs the validation does not have, stated as behaviour: HOME's mode is the
// operator's business, exactly as HOME's ACL is.
func TestValidateHomeAcceptsEveryOrdinaryHomeMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o700, 0o750, 0o755} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			home := mkdir(t, filepath.Join(dir, "home"), mode)
			if err := homeOf(t, boundary.JurisdictionInput{Home: home}, os.Getuid()); err != nil {
				t.Fatalf("HOME at mode %v rejected: %v — the validation has no mode leg", mode, err)
			}
		})
	}
}

func TestValidateHomeDenyLegs(t *testing.T) {
	dir := t.TempDir()
	uid := os.Getuid()

	// Leg 1: absent.
	t.Run("absent HOME", func(t *testing.T) {
		err := homeOf(t, boundary.JurisdictionInput{Home: filepath.Join(dir, "nope")}, uid)
		if err == nil {
			t.Fatal("absent HOME accepted")
		}
		if got := codeOf(t, err); got != boundary.CodeDeniedRoot {
			t.Errorf("code = %q, want %q — HOME is a jurisdiction refusal, not an allowlist one", got, boundary.CodeDeniedRoot)
		}
	})

	// Leg 2: a symlink is REFUSED, not resolved through. This is the leg that
	// cannot exist if Home arrives pre-resolved: EvalSymlinks destroys the only
	// evidence that it was ever a symlink. A $HOME pointed at an
	// attacker-controlled directory would otherwise silently relocate the entire
	// ~/.ssh-class member set and broad_root's anchor — the real ~/.ssh left
	// unprotected, the attacker's protected instead.
	//
	// The decoy is owned by the SAME uid, or the ownership leg would mask the
	// symlink leg and this would prove nothing.
	t.Run("symlinked HOME is refused, not resolved through", func(t *testing.T) {
		real := mkdir(t, filepath.Join(dir, "real-home"), 0o750)
		link := filepath.Join(dir, "link-home")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		// Sanity: the target is same-uid and would itself be accepted, so the
		// rejection below can only come from the symlink leg.
		if err := homeOf(t, boundary.JurisdictionInput{Home: real}, uid); err != nil {
			t.Fatalf("fixture broken: the symlink target is not itself acceptable: %v", err)
		}
		if err := homeOf(t, boundary.JurisdictionInput{Home: link}, uid); err == nil {
			t.Fatal("symlinked HOME accepted; the validation refuses a symlink rather than resolving through it")
		}
	})

	// Leg 3: not a directory.
	t.Run("HOME is a regular file", func(t *testing.T) {
		f := writeFile(t, filepath.Join(dir, "file-home"), 0o600)
		if err := homeOf(t, boundary.JurisdictionInput{Home: f}, uid); err == nil {
			t.Fatal("a regular file was accepted as HOME")
		}
	})

	// Leg 4: a filesystem root. A HOME of "/" would make broad_root reject every
	// source on the machine — fail-closed, but useless.
	t.Run("HOME is the filesystem root", func(t *testing.T) {
		if err := homeOf(t, boundary.JurisdictionInput{Home: "/"}, uid); err == nil {
			t.Fatal(`HOME "/" accepted; broad_root would then reject every source on the machine`)
		}
	})

	// Leg 5: foreign-owned. Injection relocates trust; it does not remove it.
	t.Run("HOME owned by another uid", func(t *testing.T) {
		home := mkdir(t, filepath.Join(dir, "foreign-home"), 0o750)
		if err := homeOf(t, boundary.JurisdictionInput{Home: home}, 0); err == nil {
			t.Fatal("HOME owned by uid 501 accepted for ownerUID 0; the validation checks ownership")
		}
	})

	// Not a leg, but the same principle: this package refuses ambient identity.
	t.Run("empty HOME", func(t *testing.T) {
		if err := homeOf(t, boundary.JurisdictionInput{Home: ""}, uid); err == nil {
			t.Fatal("empty HOME accepted; it must never fall back to $HOME or os.UserHomeDir")
		}
	})
}

// The required non-"/" witness. On macOS /System/Volumes/VM is a real volume
// root whose lexical parent is an ordinary directory, so Dir-self and
// SameFile(parent) are both false. Its differing device number is the portable
// leg; Darwin's mount-point query is complementary evidence for the same fact.
func TestValidateHomeRejectsNonSlashFilesystemRoot(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the repository's measured non-/ volume witness is macOS-specific")
	}

	home := "/System/Volumes/VM"
	info, err := os.Lstat(home)
	if err != nil {
		t.Skipf("measured volume root %q is unavailable: %v", home, err)
	}
	parentInfo, err := os.Lstat(filepath.Dir(home))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(home) == home || os.SameFile(info, parentInfo) {
		t.Fatalf("fixture inert: %q is detectable by Dir-self or parent identity", home)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("cannot read device/owner identity for %q", home)
	}
	parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("cannot read parent device identity for %q", home)
	}
	if stat.Dev == parentStat.Dev {
		t.Fatalf("fixture inert: %q and parent have the same device %d", home, stat.Dev)
	}

	err = homeOf(t, boundary.JurisdictionInput{Home: home}, int(stat.Uid))
	if err == nil {
		t.Fatalf("non-/ filesystem root %q accepted as HOME", home)
	}
	if got := codeOf(t, err); got != boundary.CodeDeniedRoot {
		t.Fatalf("code = %q, want %q", got, boundary.CodeDeniedRoot)
	}
}

// HOME is injected, never ambient: mc/boundary must not read $HOME even when one
// is set. TrustHomeDir takes an explicit ownerUID for exactly this reason —
// a boundary an environment variable can relocate is not a boundary.
func TestValidateHomeNeverReadsTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	decoy := mkdir(t, filepath.Join(dir, "decoy-home"), 0o750)
	t.Setenv("HOME", decoy)

	if err := homeOf(t, boundary.JurisdictionInput{Home: ""}, os.Getuid()); err == nil {
		t.Fatal("empty HOME was accepted while $HOME was set; mc/boundary read the environment")
	}
}
