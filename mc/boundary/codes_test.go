package boundary_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mc/boundary"
	"mc/refusal"
)

// The rejection namespace is closed. This test is deliberately an exact
// equality check rather than a prefix check: a newly invented mount.* code has
// no dispatch consequence and therefore cannot safely reach dispatch.
func TestMountRejectionCodesAreExactAndClosed(t *testing.T) {
	codes := []string{
		boundary.CodeAllowlistUntrusted,
		boundary.CodeAllowlistInvalid,
		boundary.CodeSourceMissing,
		boundary.CodeSourceWrongKind,
		boundary.CodeSourceBlocked,
		boundary.CodeSymlinkEscape,
		boundary.CodeNotAllowlisted,
		boundary.CodeDeniedRoot,
		boundary.CodeCrossProject,
		boundary.CodeRWNotPermitted,
		boundary.CodeTargetInvalid,
		boundary.CodeSourceAlias,
		boundary.CodeTargetCollision,
		boundary.CodeIdentityChanged,
		boundary.CodeRuntimeUnappliable,
		boundary.CodeGateUnhealthy,
	}
	want := map[string]bool{
		"mount.allowlist_untrusted": true,
		"mount.allowlist_invalid":   true,
		"mount.source_missing":      true,
		"mount.source_wrong_kind":   true,
		"mount.source_blocked":      true,
		"mount.symlink_escape":      true,
		"mount.not_allowlisted":     true,
		"mount.denied_root":         true,
		"mount.cross_project":       true,
		"mount.rw_not_permitted":    true,
		"mount.target_invalid":      true,
		"mount.source_alias":        true,
		"mount.target_collision":    true,
		"mount.identity_changed":    true,
		"mount.runtime_unappliable": true,
		"mount.gate_unhealthy":      true,
	}
	if len(codes) != len(want) {
		t.Fatalf("stable code count = %d, want %d", len(codes), len(want))
	}
	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		if !want[code] {
			t.Errorf("code %q is outside the closed set", code)
		}
		if seen[code] {
			t.Errorf("duplicate stable code %q", code)
		}
		seen[code] = true
	}

	declared := declaredMountCodes(t)
	if len(declared) != len(want) {
		t.Fatalf("declared mount-code constant count = %d, want %d: %#v", len(declared), len(want), declared)
	}
	for name, value := range declared {
		if !want[value] {
			t.Errorf("declared constant %s = %q is outside the closed set", name, value)
		}
	}
}

// TestEveryDeclaredMountCodeHasAConsequenceClass makes good on the promise the test
// above states in prose: a newly invented mount.* code has no
// consequence and therefore cannot safely reach dispatch." It is enforced here,
// against the AST-declared constants, rather than in mc/refusal, because this is
// the file an author touches when inventing a code — the failure has to land
// where the mistake is made, not one package downstream at run time.
//
// The dependency direction is deliberate: mc/refusal imports mc/boundary, so the
// production package stays a leaf and only this external test reaches back.
func TestEveryDeclaredMountCodeHasAConsequenceClass(t *testing.T) {
	for name, code := range declaredMountCodes(t) {
		// Authority is accepted for all sixteen: it decides the class for the
		// fourteen candidate-ownable codes and is deliberately irrelevant to
		// the two allowlist trust codes.
		if _, err := refusal.Classify(refusal.Refusal{
			Code:      code,
			Authority: refusal.AuthorityCandidate,
		}); err != nil {
			t.Errorf("declared constant %s = %q has no consequence class: %v", name, code, err)
		}
	}
}

func declaredMountCodes(t *testing.T) map[string]string {
	t.Helper()
	packages, err := parser.ParseDir(token.NewFileSet(), ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse boundary package: %v", err)
	}
	pkg := packages["boundary"]
	if pkg == nil {
		t.Fatal("parsed boundary package not found")
	}

	declared := make(map[string]string)
	for _, file := range pkg.Files {
		ast.Inspect(file, func(node ast.Node) bool {
			declaration, ok := node.(*ast.GenDecl)
			if !ok || declaration.Tok != token.CONST {
				return true
			}
			for _, spec := range declaration.Specs {
				values, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range values.Names {
					if i >= len(values.Values) {
						if strings.HasPrefix(name.Name, "Code") {
							t.Fatalf("declared constant %s does not have an explicit value", name.Name)
						}
						continue
					}
					literal, ok := values.Values[i].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						if strings.HasPrefix(name.Name, "Code") {
							t.Fatalf("declared constant %s is not a string literal", name.Name)
						}
						continue
					}
					value, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatalf("unquote declared constant %s: %v", name.Name, err)
					}
					if strings.HasPrefix(name.Name, "Code") || strings.HasPrefix(value, "mount.") {
						declared[name.Name] = value
					}
				}
			}
			return false
		})
	}
	return declared
}

// Every exported operation that produces a mount rejection returns a
// MountError in the closed namespace. Callers classify it with errors.As;
// parsing human-facing text is never part of the contract. NewBlockPolicy is
// intentionally outside this table: its input is config.toml, whose
// consequence is health.config_invalid rather than a forged mount code.
func TestEveryPublicMountRejectionIsCoded(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	parsedMissing, err := boundary.ParseMountAllowlist([]byte("version = 1\n[[allow]]\npath = " + quotedTOML(missing) + "\ntarget = 'safe'\naccess = 'ro'\n"))
	if err != nil {
		t.Fatalf("fixture ParseMountAllowlist: %v", err)
	}

	tests := []struct {
		name string
		want string
		run  func() error
	}{
		{name: "ParseMountAllowlist", want: boundary.CodeAllowlistInvalid, run: func() error {
			_, err := boundary.ParseMountAllowlist([]byte("version = 2\n"))
			return err
		}},
		{name: "ValidateTarget", want: boundary.CodeTargetInvalid, run: func() error {
			return boundary.ValidateTarget("../escape")
		}},
		{name: "ResolveAccess", want: boundary.CodeRWNotPermitted, run: func() error {
			_, err := boundary.ResolveAccess(boundary.AccessRW, boundary.AccessRO)
			return err
		}},
		{name: "TrustPolicyFile", want: boundary.CodeAllowlistUntrusted, run: func() error {
			return boundary.TrustPolicyFile(missing, os.Getuid())
		}},
		{name: "TrustHomeDir", want: boundary.CodeAllowlistUntrusted, run: func() error {
			return boundary.TrustHomeDir(missing, os.Getuid())
		}},
		{name: "ResolveSource", want: boundary.CodeSourceWrongKind, run: func() error {
			_, err := boundary.ResolveSource("")
			return err
		}},
		{name: "ResolveAllowlist", want: boundary.CodeSourceMissing, run: func() error {
			_, err := boundary.ResolveAllowlist(parsedMissing)
			return err
		}},
		{name: "ResolvedAllowlist.Authorize", want: boundary.CodeSourceWrongKind, run: func() error {
			_, err := (boundary.ResolvedAllowlist{}).Authorize("", boundary.AccessRO, boundary.BlockPolicy{}, boundary.Jurisdiction{})
			return err
		}},
		{name: "ResolveJurisdiction", want: boundary.CodeDeniedRoot, run: func() error {
			_, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{}, os.Getuid())
			return err
		}},
		{name: "ResolveProtectedEvidence", want: boundary.CodeDeniedRoot, run: func() error {
			_, err := boundary.ResolveProtectedEvidence(boundary.ProtectedID{Canonical: "relative"})
			return err
		}},
		{name: "Jurisdiction.Rejects", want: boundary.CodeDeniedRoot, run: func() error {
			return (boundary.Jurisdiction{}).Rejects(boundary.SourceIdentity{})
		}},
	}
	mountAPIs := map[string]bool{
		"ParseMountAllowlist":         true,
		"ValidateTarget":              true,
		"ResolveAccess":               true,
		"TrustPolicyFile":             true,
		"TrustHomeDir":                true,
		"ResolveSource":               true,
		"ResolveAllowlist":            true,
		"ResolvedAllowlist.Authorize": true,
		"ResolveJurisdiction":         true,
		"ResolveProtectedEvidence":    true,
		"Jurisdiction.Rejects":        true,
	}
	exportedErrorAPIs := exportedErrorReturningAPIs(t)
	wantExportedErrorAPIs := make(map[string]bool, len(mountAPIs)+1)
	for name := range mountAPIs {
		wantExportedErrorAPIs[name] = true
	}
	// The deliberate exceptions: NewBlockPolicy and NewEnvGuard validate
	// operator configuration, so the frame classifies their eventual
	// integration errors as health.config_invalid. BuildEnvPlan is not a
	// mount API at all: its EnvPolicyError kinds are the candidate codes
	// env.invalid and env.forbidden.
	wantExportedErrorAPIs["NewBlockPolicy"] = true
	wantExportedErrorAPIs["NewEnvGuard"] = true
	wantExportedErrorAPIs["BuildEnvPlan"] = true
	if len(exportedErrorAPIs) != len(wantExportedErrorAPIs) {
		t.Fatalf("exported error-returning API count = %d, want %d: %#v",
			len(exportedErrorAPIs), len(wantExportedErrorAPIs), exportedErrorAPIs)
	}
	for name := range exportedErrorAPIs {
		if !wantExportedErrorAPIs[name] {
			t.Errorf("exported error-returning API %s has no mount-code or config-health classification", name)
		}
	}

	closed := map[string]bool{
		boundary.CodeAllowlistUntrusted: true,
		boundary.CodeAllowlistInvalid:   true,
		boundary.CodeSourceMissing:      true,
		boundary.CodeSourceWrongKind:    true,
		boundary.CodeSourceBlocked:      true,
		boundary.CodeSymlinkEscape:      true,
		boundary.CodeNotAllowlisted:     true,
		boundary.CodeDeniedRoot:         true,
		boundary.CodeCrossProject:       true,
		boundary.CodeRWNotPermitted:     true,
		boundary.CodeTargetInvalid:      true,
		boundary.CodeSourceAlias:        true,
		boundary.CodeTargetCollision:    true,
		boundary.CodeIdentityChanged:    true,
		boundary.CodeRuntimeUnappliable: true,
		boundary.CodeGateUnhealthy:      true,
	}

	tested := make(map[string]bool, len(tests))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil {
				t.Fatal("rejection error = nil")
			}
			var mountErr *boundary.MountError
			if !errors.As(err, &mountErr) {
				t.Fatalf("error type = %T, want *boundary.MountError", err)
			}
			if mountErr.Code != tt.want {
				t.Fatalf("code = %q, want %q", mountErr.Code, tt.want)
			}
			if !closed[mountErr.Code] {
				t.Fatalf("code %q is outside the closed set", mountErr.Code)
			}
		})
		tested[tt.name] = true
	}
	if len(tested) != len(mountAPIs) {
		t.Fatalf("coded public mount API fixture count = %d, want %d", len(tested), len(mountAPIs))
	}
	for name := range mountAPIs {
		if !tested[name] {
			t.Errorf("public mount rejector %s has no coded rejection fixture", name)
		}
	}

	t.Run("NewBlockPolicy remains config health rather than a forged mount code", func(t *testing.T) {
		_, err := boundary.NewBlockPolicy([]boundary.BlockPattern{{Kind: "regex", Pattern: "secret"}})
		if err == nil {
			t.Fatal("invalid config extension accepted")
		}
		var mountErr *boundary.MountError
		if errors.As(err, &mountErr) {
			t.Fatalf("config.toml error forged a mount code %q", mountErr.Code)
		}
	})
}

func exportedErrorReturningAPIs(t *testing.T) map[string]bool {
	t.Helper()
	packages, err := parser.ParseDir(token.NewFileSet(), ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse boundary package: %v", err)
	}
	pkg := packages["boundary"]
	if pkg == nil {
		t.Fatal("parsed boundary package not found")
	}

	apis := make(map[string]bool)
	for _, file := range pkg.Files {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || !ast.IsExported(fn.Name.Name) || !returnsError(fn.Type.Results) {
				continue
			}
			name := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) == 1 {
				name = receiverTypeName(t, fn.Recv.List[0].Type) + "." + name
			}
			apis[name] = true
		}
	}
	return apis
}

func returnsError(results *ast.FieldList) bool {
	if results == nil {
		return false
	}
	for _, result := range results.List {
		if ident, ok := result.Type.(*ast.Ident); ok && ident.Name == "error" {
			return true
		}
	}
	return false
}

func receiverTypeName(t *testing.T, expression ast.Expr) string {
	t.Helper()
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return receiverTypeName(t, typed.X)
	default:
		t.Fatalf("unsupported exported method receiver %T", expression)
		return ""
	}
}

func quotedTOML(path string) string {
	// Temp paths on the supported hosts contain no single quote; a literal TOML
	// string keeps the fixture independent of Go/TOML escape differences.
	return "'" + path + "'"
}
