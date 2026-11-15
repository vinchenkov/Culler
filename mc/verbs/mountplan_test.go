package verbs

// The attest step's mount-plan validator: the one composition of
// mc/boundary's seams the dispatch seam owns (boundary deliberately ships no
// orchestrator), plus the adapter that turns a *boundary.MountError into a
// typed refusal.Refusal. Every rejection aborts the whole plan — no mount is
// ever silently dropped or downgraded — and the
// producer's raw text rides only in Message, which DetailFor drops.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/boundary"
	"mc/refusal"
)

// mpFixture builds a trusted allowlist naming root under target "data" (max
// access rw unless overridden), a resolved empty-member jurisdiction, and the
// zero BlockPolicy (which still enforces the shipped floor).
type mpFixture struct {
	root   string
	inputs mountPlanInputs
}

func mpSetup(t *testing.T, access string) mpFixture {
	t.Helper()
	if access == "" {
		access = "rw"
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	allowlistTOML := "version = 1\n\n[[allow]]\npath = \"" + root + "\"\ntarget = \"data\"\naccess = \"" + access + "\"\n"
	path := filepath.Join(dir, "mount-allowlist.toml")
	if err := os.WriteFile(path, []byte(allowlistTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	j, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{Home: home}, os.Getuid())
	if err != nil {
		t.Fatalf("ResolveJurisdiction: %v", err)
	}
	return mpFixture{root: root, inputs: mountPlanInputs{
		AllowlistPath: path,
		OwnerUID:      os.Getuid(),
		Blocked:       boundary.BlockPolicy{},
		Jurisdiction:  j,
	}}
}

func (f mpFixture) mkdir(t *testing.T, rel string) string {
	t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlanMountsEmptyRequestsValidateNothing(t *testing.T) {
	entries, r, err := planMounts(nil, mountPlanInputs{AllowlistPath: "/nonexistent"})
	if entries != nil || r != nil || err != nil {
		t.Fatalf("empty plan = (%v, %v, %v), want all nil", entries, r, err)
	}
}

func TestPlanMountsAuthorizesAndDerivesClassedDestinations(t *testing.T) {
	f := mpSetup(t, "")
	one := f.mkdir(t, "one")
	two := f.mkdir(t, "two")
	entries, r, err := planMounts([]mountRequest{
		{Source: two, Access: boundary.AccessRW, Authority: refusal.AuthorityCandidate, Class: classArtifact},
		{Source: one, Access: boundary.AccessRW, Authority: refusal.AuthorityCandidate, Class: classArtifact},
	}, f.inputs)
	if err != nil || r != nil {
		t.Fatalf("planMounts = refusal %+v err %v, want success", r, err)
	}
	if len(entries) != 2 {
		t.Fatalf("authorized %d mounts, want 2", len(entries))
	}
	// Sorted by destination, not by request order.
	if entries[0].Destination != "/mc/artifacts/data/one" || entries[0].Access != "rw" ||
		entries[0].LogicalID != "artifact:data/one" || entries[0].Kind != "dir" {
		t.Fatalf("first entry = %+v", entries[0])
	}
	if entries[1].Destination != "/mc/artifacts/data/two" || entries[1].Access != "rw" ||
		entries[1].LogicalID != "artifact:data/two" || entries[1].Kind != "dir" {
		t.Fatalf("second entry = %+v", entries[1])
	}
	for i, entry := range entries {
		if entry.Device == "" || entry.Inode == "" || entry.OwnerUID != os.Getuid() || entry.Mode != 0o700 {
			t.Fatalf("entry %d carries no host identity evidence: %+v", i, entry)
		}
	}
}

// A destinationless request is a protocol error, never guessed: the class is
// what derives the destination, so a request carrying neither is unplannable.
func TestPlanMountsRequireADestinationClass(t *testing.T) {
	f := mpSetup(t, "")
	x := f.mkdir(t, "x")
	entries, r, err := planMounts([]mountRequest{
		{Source: x, Access: boundary.AccessRW, Authority: refusal.AuthorityCandidate, Class: classArtifact},
	}, f.inputs)
	if err != nil || r != nil || len(entries) != 1 {
		t.Fatalf("classed plan = (%v, %+v, %v), want one entry", entries, r, err)
	}
	if _, _, err := planMounts([]mountRequest{
		{Source: x, Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate},
	}, f.inputs); err == nil {
		t.Fatalf("a request without a destination class must be a protocol error")
	}
}

func TestPlanMountsUntrustedAllowlistIsHealth(t *testing.T) {
	f := mpSetup(t, "")
	if err := os.Chmod(f.inputs.AllowlistPath, 0o644); err != nil {
		t.Fatal(err)
	}
	_, r, err := planMounts([]mountRequest{
		{Source: f.root, Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact},
	}, f.inputs)
	if err != nil || r == nil {
		t.Fatalf("planMounts = %v, want a refusal", err)
	}
	if r.Code != boundary.CodeAllowlistUntrusted || r.Field != refusal.FieldAllowlist || r.Summary != refusal.SummaryUntrusted {
		t.Fatalf("refusal = %+v", r)
	}
	if class, err := refusal.Classify(*r); err != nil || class != refusal.ClassHealth {
		t.Fatalf("Classify = %v/%v, want health (the carve-out: whatever the attester says)", class, err)
	}
}

func TestPlanMountsInvalidAllowlistIsHealth(t *testing.T) {
	f := mpSetup(t, "")
	if err := os.WriteFile(f.inputs.AllowlistPath, []byte("not toml at all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, r, err := planMounts([]mountRequest{
		{Source: f.root, Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact},
	}, f.inputs)
	if err != nil || r == nil {
		t.Fatalf("planMounts = %v, want a refusal", err)
	}
	if r.Code != boundary.CodeAllowlistInvalid || r.Field != refusal.FieldAllowlist || r.Summary != refusal.SummaryUnparsable {
		t.Fatalf("refusal = %+v", r)
	}
}

// A trusted, syntactically valid allowlist whose roots overlap (or are
// absent) fails at ResolveAllowlist with authority-DECIDES codes. The
// allowlist and its roots are deployment-authored, so the adapter must carry
// AuthorityDeployment there — an empty authority is unclassifiable, and an
// unclassifiable refusal hard-errors every tick instead of recording the
// recoverable health action.
func TestPlanMountsOverlappingAllowRootsAreDeploymentHealth(t *testing.T) {
	f := mpSetup(t, "")
	sub := f.mkdir(t, "sub")
	overlapping := "version = 1\n\n[[allow]]\npath = \"" + f.root + "\"\ntarget = \"data\"\naccess = \"rw\"\n" +
		"\n[[allow]]\npath = \"" + sub + "\"\ntarget = \"other\"\naccess = \"ro\"\n"
	if err := os.WriteFile(f.inputs.AllowlistPath, []byte(overlapping), 0o600); err != nil {
		t.Fatal(err)
	}
	_, r, err := planMounts([]mountRequest{
		{Source: sub, Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact},
	}, f.inputs)
	if err != nil || r == nil {
		t.Fatalf("planMounts = %v, want a refusal", err)
	}
	if r.Code != boundary.CodeSourceAlias || r.Authority != refusal.AuthorityDeployment {
		t.Fatalf("refusal = {%s %s}, want {%s %s}", r.Code, r.Authority, boundary.CodeSourceAlias, refusal.AuthorityDeployment)
	}
	if class, cerr := refusal.Classify(*r); cerr != nil || class != refusal.ClassHealth {
		t.Fatalf("Classify = %v/%v, want health — a broken allowlist is the deployment's fault, never a wedge", class, cerr)
	}
}

func TestPlanMountsRejectionTable(t *testing.T) {
	idx := func(i int) *int { return &i }
	cases := []struct {
		name     string
		access   string
		requests func(t *testing.T, f mpFixture) []mountRequest
		code     string
		field    refusal.Field
		summary  refusal.Summary
		item     *int
	}{
		{
			name: "blocked_floor_component",
			requests: func(t *testing.T, f mpFixture) []mountRequest {
				return []mountRequest{
					{Source: f.mkdir(t, "ok"), Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact},
					{Source: f.mkdir(t, ".ssh"), Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact},
				}
			},
			code: boundary.CodeSourceBlocked, field: refusal.FieldMountSource,
			summary: refusal.SummaryBlockedFloor, item: idx(1),
		},
		{
			name: "missing_source",
			requests: func(t *testing.T, f mpFixture) []mountRequest {
				return []mountRequest{
					{Source: filepath.Join(f.root, "absent"), Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact},
				}
			},
			code: boundary.CodeSourceMissing, field: refusal.FieldMountSource,
			summary: refusal.SummaryMissing, item: idx(0),
		},
		{
			name: "not_allowlisted",
			requests: func(t *testing.T, f mpFixture) []mountRequest {
				outside := t.TempDir()
				return []mountRequest{
					{Source: outside, Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact},
				}
			},
			code: boundary.CodeNotAllowlisted, field: refusal.FieldMountSource,
			summary: refusal.SummaryNotAllowlisted, item: idx(0),
		},
		{
			name:   "rw_over_ro_maximum_never_downgrades",
			access: "ro",
			requests: func(t *testing.T, f mpFixture) []mountRequest {
				return []mountRequest{
					{Source: f.root, Access: boundary.AccessRW, Authority: refusal.AuthorityCandidate, Class: classArtifact},
				}
			},
			code: boundary.CodeRWNotPermitted, field: refusal.FieldMountSource,
			summary: refusal.SummaryForbidden, item: idx(0),
		},
		{
			// Nested destinations shadow each other (data/x vs data/x/y):
			// rejected whole, the same grammar the allowlist's parse-time
			// target set enforces.
			name: "nested_destination_collision_across_requests",
			requests: func(t *testing.T, f mpFixture) []mountRequest {
				x := f.mkdir(t, "x")
				y := f.mkdir(t, "x/y")
				return []mountRequest{
					{Source: x, Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact},
					{Source: y, Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact},
				}
			},
			code: boundary.CodeTargetCollision, field: refusal.FieldMountTarget,
			summary: refusal.SummaryCollision, item: idx(1),
		},
		{
			name: "duplicate_source_is_a_collision",
			requests: func(t *testing.T, f mpFixture) []mountRequest {
				x := f.mkdir(t, "x")
				return []mountRequest{
					{Source: x, Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact},
					{Source: x, Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact},
				}
			},
			code: boundary.CodeTargetCollision, field: refusal.FieldMountTarget,
			summary: refusal.SummaryCollision, item: idx(1),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := mpSetup(t, tc.access)
			auths, r, err := planMounts(tc.requests(t, f), f.inputs)
			if err != nil {
				t.Fatalf("planMounts errored: %v", err)
			}
			if auths != nil {
				t.Fatalf("a rejected plan returned authorizations: %+v (no drop, no downgrade)", auths)
			}
			if r == nil {
				t.Fatalf("planMounts returned no refusal")
			}
			if r.Code != tc.code || r.Field != tc.field || r.Summary != tc.summary {
				t.Fatalf("refusal = {%s %s %s}, want {%s %s %s}", r.Code, r.Field, r.Summary, tc.code, tc.field, tc.summary)
			}
			if r.Authority != refusal.AuthorityCandidate {
				t.Fatalf("authority = %q, want the request's own", r.Authority)
			}
			if tc.item != nil && (r.ItemIndex == nil || *r.ItemIndex != *tc.item) {
				t.Fatalf("item index = %v, want %d", r.ItemIndex, *tc.item)
			}
			if _, err := refusal.Classify(*r); err != nil {
				t.Fatalf("adapter emitted an unclassifiable refusal: %v", err)
			}
		})
	}
}

func TestPlanMountsZeroJurisdictionRejectsOutsideJurisdiction(t *testing.T) {
	f := mpSetup(t, "")
	f.inputs.Jurisdiction = boundary.Jurisdiction{} // fails closed by design
	_, r, err := planMounts([]mountRequest{
		{Source: f.root, Access: boundary.AccessRO, Authority: refusal.AuthorityDeployment, Class: classArtifact},
	}, f.inputs)
	if err != nil || r == nil {
		t.Fatalf("planMounts = %v, want a refusal", err)
	}
	if r.Code != boundary.CodeDeniedRoot || r.Field != refusal.FieldMountSource || r.Summary != refusal.SummaryOutsideJurisdiction {
		t.Fatalf("refusal = %+v", r)
	}
	if class, err := refusal.Classify(*r); err != nil || class != refusal.ClassHealth {
		t.Fatalf("deployment-authority mount refusal must classify health, got %v/%v", class, err)
	}
}

// The raw MountError message (which carries host paths) reaches only
// Refusal.Message; the stored detail is built from closed enums.
func TestPlanMountsHostileTextStaysOutOfDetail(t *testing.T) {
	f := mpSetup(t, "")
	secret := f.mkdir(t, ".ssh")
	_, r, err := planMounts([]mountRequest{
		{Source: secret, Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact},
	}, f.inputs)
	if err != nil || r == nil {
		t.Fatalf("planMounts = %v, want a refusal", err)
	}
	if r.Message == "" {
		t.Fatalf("the host-side diagnostic should carry the boundary error")
	}
	detail, err := refusal.DetailFor(*r)
	if err != nil {
		t.Fatalf("DetailFor: %v", err)
	}
	canonical, err := detail.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if strings.Contains(string(canonical), secret) || strings.Contains(string(canonical), "root") {
		t.Fatalf("stored detail leaks a host path: %s", canonical)
	}
}

// Every declared boundary code has a shape in the adapter, and the shape
// classifies — a seventeenth code fails here at the point of invention, the
// same posture as boundary/codes_test.go.
func TestMountErrorShapeCoversEveryCode(t *testing.T) {
	codes := []string{
		boundary.CodeAllowlistUntrusted, boundary.CodeAllowlistInvalid,
		boundary.CodeSourceMissing, boundary.CodeSourceWrongKind, boundary.CodeSourceBlocked,
		boundary.CodeSymlinkEscape, boundary.CodeNotAllowlisted, boundary.CodeDeniedRoot,
		boundary.CodeCrossProject, boundary.CodeRWNotPermitted, boundary.CodeTargetInvalid,
		boundary.CodeSourceAlias, boundary.CodeTargetCollision, boundary.CodeIdentityChanged,
		boundary.CodeRuntimeUnappliable, boundary.CodeGateUnhealthy,
	}
	for _, code := range codes {
		r, err := refusalForMountError(&boundary.MountError{Code: code, Msg: "raw host text"}, refusal.AuthorityCandidate, nil)
		if err != nil {
			t.Fatalf("no shape for declared code %s: %v", code, err)
		}
		if _, err := refusal.Classify(r); err != nil {
			t.Fatalf("shape for %s does not classify: %v", code, err)
		}
		if _, err := refusal.DetailFor(r); err != nil {
			t.Fatalf("shape for %s fails detail validation: %v", code, err)
		}
	}
	if _, err := refusalForMountError(&boundary.MountError{Code: "mount.invented", Msg: "x"}, refusal.AuthorityCandidate, nil); err == nil {
		t.Fatalf("an undeclared code must refuse to shape, never default")
	}
}

func TestPlanMountsBoundsRequests(t *testing.T) {
	f := mpSetup(t, "")
	requests := make([]mountRequest, 257)
	for i := range requests {
		requests[i] = mountRequest{Source: f.root, Access: boundary.AccessRO, Authority: refusal.AuthorityCandidate, Class: classArtifact}
	}
	if _, _, err := planMounts(requests, f.inputs); err == nil {
		t.Fatalf("a plan over 256 mounts must refuse at the frame bound")
	}
}

// The destination sort is load-bearing determinism (semantically unordered
// arrays sort by their declared key): allowlist targets that invert
// the request order must still produce ascending destinations, or the
// helper's order validator wedges a valid operator config as a protocol
// error every tick.
func TestPlanMountsSortsEntriesByDestinationNotRequestOrder(t *testing.T) {
	dir := t.TempDir()
	zetaRoot := filepath.Join(dir, "first")
	alphaRoot := filepath.Join(dir, "second")
	for _, p := range []string{zetaRoot, alphaRoot} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	allowlistTOML := "version = 1\n\n[[allow]]\npath = \"" + zetaRoot + "\"\ntarget = \"zeta\"\naccess = \"rw\"\n" +
		"\n[[allow]]\npath = \"" + alphaRoot + "\"\ntarget = \"alpha\"\naccess = \"rw\"\n"
	path := filepath.Join(dir, "mount-allowlist.toml")
	if err := os.WriteFile(path, []byte(allowlistTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	j, err := boundary.ResolveJurisdiction(boundary.JurisdictionInput{Home: home}, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	entries, r, err := planMounts([]mountRequest{
		{Source: zetaRoot, Access: boundary.AccessRW, Authority: refusal.AuthorityCandidate, Class: classArtifact},
		{Source: alphaRoot, Access: boundary.AccessRW, Authority: refusal.AuthorityCandidate, Class: classArtifact},
	}, mountPlanInputs{AllowlistPath: path, OwnerUID: os.Getuid(), Blocked: boundary.BlockPolicy{}, Jurisdiction: j})
	if err != nil || r != nil || len(entries) != 2 {
		t.Fatalf("planMounts = (%v, %+v, %v)", entries, r, err)
	}
	if entries[0].Destination != "/mc/artifacts/alpha" || entries[1].Destination != "/mc/artifacts/zeta" {
		t.Fatalf("entries not sorted by destination: %q, %q", entries[0].Destination, entries[1].Destination)
	}
}
