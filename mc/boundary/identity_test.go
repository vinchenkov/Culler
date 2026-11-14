package boundary_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"mc/boundary"
)

func codeOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var me *boundary.MountError
	if !errors.As(err, &me) {
		t.Fatalf("error %v is not a *boundary.MountError", err)
	}
	return me.Code
}

func writeFile(t *testing.T, path string, mode os.FileMode) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile is umask-masked; force the exact mode under test.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func mkdir(t *testing.T, path string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(path, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// The allowlist is a non-symlink regular file owned by the
// real operator uid, owner read/write, no group/other bits.
func TestTrustPolicyFile(t *testing.T) {
	dir := t.TempDir()
	uid := os.Getuid()

	t.Run("ordinary 0600 accepted", func(t *testing.T) {
		path := writeFile(t, filepath.Join(dir, "ok"), 0o600)
		if err := boundary.TrustPolicyFile(path, uid); err != nil {
			t.Fatalf("TrustPolicyFile() = %v, want accept", err)
		}
	})

	t.Run("stricter owner mode is not a grant", func(t *testing.T) {
		path := writeFile(t, filepath.Join(dir, "readonly"), 0o400)
		if err := boundary.TrustPolicyFile(path, uid); err != nil {
			t.Fatalf("TrustPolicyFile() = %v, want accept", err)
		}
	})

	t.Run("no owner permissions is still not a grant", func(t *testing.T) {
		path := writeFile(t, filepath.Join(dir, "no-access"), 0o000)
		if err := boundary.TrustPolicyFile(path, uid); err != nil {
			t.Fatalf("TrustPolicyFile() = %v, want accept", err)
		}
	})

	rejects := []struct {
		name string
		make func(t *testing.T) string
	}{
		{"group readable", func(t *testing.T) string { return writeFile(t, filepath.Join(dir, "group"), 0o640) }},
		{"world readable", func(t *testing.T) string { return writeFile(t, filepath.Join(dir, "world"), 0o604) }},
		{"world writable", func(t *testing.T) string { return writeFile(t, filepath.Join(dir, "ww"), 0o602) }},
		{"directory", func(t *testing.T) string { return mkdir(t, filepath.Join(dir, "asdir"), 0o700) }},
		{"missing", func(t *testing.T) string { return filepath.Join(dir, "absent") }},
		{"symlink to a trusted file", func(t *testing.T) string {
			target := writeFile(t, filepath.Join(dir, "real"), 0o600)
			link := filepath.Join(dir, "link")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			return link
		}},
	}
	for _, tt := range rejects {
		t.Run(tt.name, func(t *testing.T) {
			err := boundary.TrustPolicyFile(tt.make(t), uid)
			if err == nil {
				t.Fatal("TrustPolicyFile() = nil, want rejection")
			}
			if got := codeOf(t, err); got != boundary.CodeAllowlistUntrusted {
				t.Fatalf("code = %q, want %q", got, boundary.CodeAllowlistUntrusted)
			}
		})
	}

	t.Run("another owner rejects", func(t *testing.T) {
		path := writeFile(t, filepath.Join(dir, "otheruid"), 0o600)
		if err := boundary.TrustPolicyFile(path, uid+1); err == nil {
			t.Fatal("TrustPolicyFile() accepted a file owned by another uid")
		}
	})
}

// MC_HOME is a non-symlink operator-owned directory with owner rwx and no
// group/other bits.
func TestTrustHomeDir(t *testing.T) {
	dir := t.TempDir()
	uid := os.Getuid()

	t.Run("ordinary 0700 accepted", func(t *testing.T) {
		if err := boundary.TrustHomeDir(mkdir(t, filepath.Join(dir, "home"), 0o700), uid); err != nil {
			t.Fatalf("TrustHomeDir() = %v, want accept", err)
		}
	})

	rejects := map[string]func(t *testing.T) string{
		"group accessible": func(t *testing.T) string { return mkdir(t, filepath.Join(dir, "g"), 0o750) },
		"world accessible": func(t *testing.T) string { return mkdir(t, filepath.Join(dir, "w"), 0o705) },
		"regular file":     func(t *testing.T) string { return writeFile(t, filepath.Join(dir, "file"), 0o600) },
		"missing":          func(t *testing.T) string { return filepath.Join(dir, "absent") },
		"symlink to a trusted dir": func(t *testing.T) string {
			link := filepath.Join(dir, "homelink")
			if err := os.Symlink(mkdir(t, filepath.Join(dir, "realhome"), 0o700), link); err != nil {
				t.Fatal(err)
			}
			return link
		},
	}
	for name, make := range rejects {
		t.Run(name, func(t *testing.T) {
			if err := boundary.TrustHomeDir(make(t), uid); err == nil {
				t.Fatalf("TrustHomeDir() = nil, want rejection")
			}
		})
	}
}

// Reject non-absolute, missing, NUL/newline
// sources; compute Abs -> Clean -> EvalSymlinks and retain identity.
func TestResolveSource(t *testing.T) {
	dir := t.TempDir()
	real := mkdir(t, filepath.Join(dir, "real"), 0o700)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	t.Run("canonicalizes through a symlink", func(t *testing.T) {
		got, err := boundary.ResolveSource(filepath.Join(dir, "link", ".", "..", "link"))
		if err != nil {
			t.Fatal(err)
		}
		wantCanonical, err := filepath.EvalSymlinks(real)
		if err != nil {
			t.Fatal(err)
		}
		if got.Canonical != wantCanonical {
			t.Errorf("Canonical = %q, want %q", got.Canonical, wantCanonical)
		}
		if !got.IsDir {
			t.Error("IsDir = false, want true")
		}
	})

	t.Run("retains raw-clean separately from canonical", func(t *testing.T) {
		got, err := boundary.ResolveSource(link)
		if err != nil {
			t.Fatal(err)
		}
		if got.RawClean != link {
			t.Errorf("RawClean = %q, want %q", got.RawClean, link)
		}
		if got.Canonical == got.RawClean {
			t.Error("Canonical did not resolve the symlink")
		}
	})

	t.Run("identity is the resolved object", func(t *testing.T) {
		viaLink, err := boundary.ResolveSource(link)
		if err != nil {
			t.Fatal(err)
		}
		direct, err := boundary.ResolveSource(real)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(viaLink.Info, direct.Info) {
			t.Error("symlink and direct resolution disagree on filesystem identity")
		}
	})

	for name, tc := range map[string]struct{ source, code string }{
		"relative": {"relative/path", boundary.CodeSourceWrongKind},
		"empty":    {"", boundary.CodeSourceWrongKind},
		"NUL":      {"/tmp/a\x00b", boundary.CodeSourceWrongKind},
		"newline":  {"/tmp/a\nb", boundary.CodeSourceWrongKind},
		"missing":  {filepath.Join(dir, "absent"), boundary.CodeSourceMissing},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := boundary.ResolveSource(tc.source)
			if err == nil {
				t.Fatal("ResolveSource() = nil, want rejection")
			}
			if got := codeOf(t, err); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
		})
	}
}

// Canonically identical and ancestor/descendant-overlapping
// allow roots reject, so every source has one authorization root and one
// maximum mode. This is filesystem identity, not string arithmetic.
func TestResolveAllowlistRejectsOverlappingRoots(t *testing.T) {
	dir := t.TempDir()
	parent := mkdir(t, filepath.Join(dir, "parent"), 0o700)
	child := mkdir(t, filepath.Join(parent, "child"), 0o700)
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Fatal(err)
	}
	sibling := mkdir(t, filepath.Join(dir, "parent-evil"), 0o700)

	t.Run("disjoint roots accepted", func(t *testing.T) {
		if _, err := boundary.ResolveAllowlist(allowlistOf(t, entry(parent, "a", "ro"), entry(sibling, "b", "ro"))); err != nil {
			t.Fatalf("ResolveAllowlist() = %v, want accept for sibling-prefix roots", err)
		}
	})

	t.Run("byte-identical roots reject", func(t *testing.T) {
		_, err := boundary.ResolveAllowlist(allowlistOf(t, entry(parent, "a", "ro"), entry(parent, "b", "rw")))
		if got := codeOf(t, err); got != boundary.CodeSourceAlias {
			t.Fatalf("code = %q, want %q", got, boundary.CodeSourceAlias)
		}
	})

	t.Run("symlink-aliased roots reject on identity", func(t *testing.T) {
		_, err := boundary.ResolveAllowlist(allowlistOf(t, entry(parent, "a", "ro"), entry(alias, "b", "rw")))
		if got := codeOf(t, err); got != boundary.CodeSourceAlias {
			t.Fatalf("code = %q, want %q", got, boundary.CodeSourceAlias)
		}
	})

	t.Run("ancestor-overlapping roots reject", func(t *testing.T) {
		_, err := boundary.ResolveAllowlist(allowlistOf(t, entry(parent, "a", "ro"), entry(child, "b", "rw")))
		if got := codeOf(t, err); got != boundary.CodeSourceAlias {
			t.Fatalf("code = %q, want %q", got, boundary.CodeSourceAlias)
		}
	})

	t.Run("missing root rejects", func(t *testing.T) {
		_, err := boundary.ResolveAllowlist(allowlistOf(t, entry(filepath.Join(dir, "absent"), "a", "ro")))
		if got := codeOf(t, err); got != boundary.CodeSourceMissing {
			t.Fatalf("code = %q, want %q", got, boundary.CodeSourceMissing)
		}
	})
}

// Blocked matching against raw-clean AND
// resolved; identity ancestry with exactly one allow root; suffix derived from
// that same walk and validated with the target grammar.
func TestAuthorizeIdentityWalk(t *testing.T) {
	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "safe-root"), 0o700)
	inside := mkdir(t, filepath.Join(root, "project", "src"), 0o700)
	evil := mkdir(t, filepath.Join(dir, "safe-root-evil"), 0o700)
	outside := mkdir(t, filepath.Join(dir, "outside"), 0o700)

	resolved := resolveOf(t, allowlistOf(t, entry(root, "workspace", "rw")))
	var blocked boundary.BlockPolicy
	// A real constructed jurisdiction with no members: it permits ordinary
	// sources, while a bare Jurisdiction{} would reject every one of them
	// under the zero-value law. The compiler cannot tell these apart — only this
	// choice does.
	j := resolvedEmpty(t, dir)

	t.Run("descendant accepted with derived suffix", func(t *testing.T) {
		got, err := resolved.Authorize(inside, boundary.AccessRO, blocked, j)
		if err != nil {
			t.Fatal(err)
		}
		if got.Suffix != "project/src" {
			t.Errorf("Suffix = %q, want %q", got.Suffix, "project/src")
		}
		if got.Target != "workspace" {
			t.Errorf("Target = %q, want %q", got.Target, "workspace")
		}
		if got.Access != boundary.AccessRO {
			t.Errorf("Access = %q, want ro", got.Access)
		}
	})

	t.Run("the root itself has an empty suffix", func(t *testing.T) {
		got, err := resolved.Authorize(root, boundary.AccessRW, blocked, j)
		if err != nil {
			t.Fatal(err)
		}
		if got.Suffix != "" {
			t.Errorf("Suffix = %q, want empty", got.Suffix)
		}
	})

	t.Run("in-root symlink accepted", func(t *testing.T) {
		link := filepath.Join(dir, "into-root")
		if err := os.Symlink(inside, link); err != nil {
			t.Fatal(err)
		}
		got, err := resolved.Authorize(link, boundary.AccessRO, blocked, j)
		if err != nil {
			t.Fatalf("Authorize() = %v, want accept for an in-root symlink", err)
		}
		if got.Suffix != "project/src" {
			t.Errorf("Suffix = %q, want the resolved suffix", got.Suffix)
		}
	})

	t.Run("escaping symlink rejects", func(t *testing.T) {
		link := filepath.Join(root, "escape")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		_, err := resolved.Authorize(link, boundary.AccessRO, blocked, j)
		if got := codeOf(t, err); got != boundary.CodeSymlinkEscape {
			t.Fatalf("code = %q, want %q", got, boundary.CodeSymlinkEscape)
		}
	})

	t.Run("sibling prefix never satisfies the root", func(t *testing.T) {
		_, err := resolved.Authorize(evil, boundary.AccessRO, blocked, j)
		if got := codeOf(t, err); got != boundary.CodeNotAllowlisted {
			t.Fatalf("code = %q, want %q", got, boundary.CodeNotAllowlisted)
		}
	})

	t.Run("unrelated source is not allowlisted", func(t *testing.T) {
		_, err := resolved.Authorize(outside, boundary.AccessRO, blocked, j)
		if got := codeOf(t, err); got != boundary.CodeNotAllowlisted {
			t.Fatalf("code = %q, want %q", got, boundary.CodeNotAllowlisted)
		}
	})

	t.Run("RW over an RO maximum rejects", func(t *testing.T) {
		roRoot := mkdir(t, filepath.Join(dir, "ro-root"), 0o700)
		roOnly := resolveOf(t, allowlistOf(t, entry(roRoot, "reference", "ro")))
		_, err := roOnly.Authorize(roRoot, boundary.AccessRW, blocked, j)
		if got := codeOf(t, err); got != boundary.CodeRWNotPermitted {
			t.Fatalf("code = %q, want %q", got, boundary.CodeRWNotPermitted)
		}
	})
}

// The blocked floor matches raw-clean and resolved
// components, so a symlink cannot launder a denied name.
func TestAuthorizeBlockedMatching(t *testing.T) {
	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "safe-root"), 0o700)
	secret := mkdir(t, filepath.Join(root, ".ssh"), 0o700)
	ordinary := mkdir(t, filepath.Join(root, "ordinary"), 0o700)
	resolved := resolveOf(t, allowlistOf(t, entry(root, "workspace", "rw")))
	var blocked boundary.BlockPolicy
	// A real constructed jurisdiction with no members: it permits ordinary
	// sources, while a bare Jurisdiction{} would reject every one of them
	// under the zero-value law. The compiler cannot tell these apart — only this
	// choice does.
	j := resolvedEmpty(t, dir)

	t.Run("blocked component in the raw-clean address", func(t *testing.T) {
		_, err := resolved.Authorize(secret, boundary.AccessRO, blocked, j)
		if got := codeOf(t, err); got != boundary.CodeSourceBlocked {
			t.Fatalf("code = %q, want %q", got, boundary.CodeSourceBlocked)
		}
	})

	t.Run("blocked component laundered behind a symlink", func(t *testing.T) {
		link := filepath.Join(root, "innocent")
		if err := os.Symlink(secret, link); err != nil {
			t.Fatal(err)
		}
		_, err := resolved.Authorize(link, boundary.AccessRO, blocked, j)
		if got := codeOf(t, err); got != boundary.CodeSourceBlocked {
			t.Fatalf("code = %q, want %q — the resolved address carries .ssh", got, boundary.CodeSourceBlocked)
		}
	})

	t.Run("a workspace merely containing a blocked name is fine", func(t *testing.T) {
		if _, err := resolved.Authorize(ordinary, boundary.AccessRO, blocked, j); err != nil {
			t.Fatalf("Authorize() = %v; Rejects classifies address components, not contents", err)
		}
	})
}

// A colon in the matched allow root's own spelling
// is legal; a colon in any requested descendant suffix rejects.
func TestAuthorizeSuffixGrammar(t *testing.T) {
	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "ref:one"), 0o700)
	child := mkdir(t, filepath.Join(root, "child:two"), 0o700)
	plain := mkdir(t, filepath.Join(root, "plain"), 0o700)
	resolved := resolveOf(t, allowlistOf(t, entry(root, "reference", "ro")))
	var blocked boundary.BlockPolicy
	// A real constructed jurisdiction with no members: it permits ordinary
	// sources, while a bare Jurisdiction{} would reject every one of them
	// under the zero-value law. The compiler cannot tell these apart — only this
	// choice does.
	j := resolvedEmpty(t, dir)

	t.Run("colon in the allow root itself is legal", func(t *testing.T) {
		if _, err := resolved.Authorize(root, boundary.AccessRO, blocked, j); err != nil {
			t.Fatalf("Authorize() = %v, want accept for an exact colon-carrying root", err)
		}
	})

	t.Run("ordinary descendant of a colon root is legal", func(t *testing.T) {
		if _, err := resolved.Authorize(plain, boundary.AccessRO, blocked, j); err != nil {
			t.Fatalf("Authorize() = %v, want accept", err)
		}
	})

	t.Run("colon in a descendant suffix rejects", func(t *testing.T) {
		_, err := resolved.Authorize(child, boundary.AccessRO, blocked, j)
		if got := codeOf(t, err); got != boundary.CodeTargetInvalid {
			t.Fatalf("code = %q, want %q", got, boundary.CodeTargetInvalid)
		}
	})
}

func entry(path, target, access string) string {
	return "[[allow]]\npath = '" + path + "'\ntarget = '" + target + "'\naccess = '" + access + "'\n"
}

func allowlistOf(t *testing.T, entries ...string) boundary.MountAllowlist {
	t.Helper()
	input := "version = 1\n"
	for _, e := range entries {
		input += e
	}
	parsed, err := boundary.ParseMountAllowlist([]byte(input))
	if err != nil {
		t.Fatalf("ParseMountAllowlist() = %v", err)
	}
	return parsed
}

func resolveOf(t *testing.T, a boundary.MountAllowlist) boundary.ResolvedAllowlist {
	t.Helper()
	resolved, err := boundary.ResolveAllowlist(a)
	if err != nil {
		t.Fatalf("ResolveAllowlist() = %v", err)
	}
	return resolved
}
