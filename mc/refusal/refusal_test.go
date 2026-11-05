package refusal_test

import (
	"encoding/json"
	"strings"
	"testing"

	"mc/boundary"
	"mc/refusal"
)

// The consequence classes are closed. These tests pin the whole table by code,
// not a sample: a code added to the taxonomy without a class fails here rather
// than defaulting into somebody's consequence.

func TestClassifyStaleProtocol(t *testing.T) {
	// Error and retry; no durable mutation and no effect. Authority is not a
	// dimension of a protocol failure, so carrying one is incoherent.
	for _, code := range []string{
		refusal.CodeStale,
		refusal.CodeFrameInvalid,
		refusal.CodeVersionMismatch,
		refusal.CodeCandidateMismatch,
		refusal.CodeInventoryChanged,
		refusal.CodeOversize,
	} {
		got, err := refusal.Classify(refusal.Refusal{Code: code})
		if err != nil {
			t.Errorf("Classify(%q): unexpected error: %v", code, err)
			continue
		}
		if got != refusal.ClassStale {
			t.Errorf("Classify(%q) = %q, want %q", code, got, refusal.ClassStale)
		}
	}
}

func TestClassifyDeploymentHealth(t *testing.T) {
	// The health column, whole — including the two allowlist codes that live
	// in the mount.* namespace but are deployment-owned.
	for _, code := range []string{
		// The deployment cannot record its own decision — the effect it built
		// does not fit the receipt column or the crossing that carries it.
		// Health, not stale: a retry of an identical decision that cannot be
		// committed is a silent loop, and health is the only class with a
		// budget that ends one.
		refusal.CodeEffectOversize,
		refusal.CodeRuntimeUnavailable,
		refusal.CodeHelperUnavailable,
		refusal.CodeImageUnavailable,
		refusal.CodeGatewayUnavailable,
		refusal.CodeNetworkCapabilityUnavailable,
		refusal.CodeResourceCapabilityUnavailable,
		refusal.CodeResourceConfigInvalid,
		refusal.CodeFilePlaneUnavailable,
		refusal.CodeProjectionUnavailable,
		refusal.CodeConfigInvalid,
		refusal.CodeRoutingInvalid,
	} {
		got, err := refusal.Classify(refusal.Refusal{Code: code})
		if err != nil {
			t.Errorf("Classify(%q): unexpected error: %v", code, err)
			continue
		}
		if got != refusal.ClassHealth {
			t.Errorf("Classify(%q) = %q, want %q", code, got, refusal.ClassHealth)
		}
	}
}

// Allowlist trust/invalid refusals record deployment health. The taxonomy
// carves these two out of the candidate-policy set explicitly, so they are
// health even when the attester hands us a candidate authority. Fail-closed:
// the deployment's own allowlist file cannot be made a task's fault by a
// mislabeled frame.
func TestAllowlistTrustIsAlwaysHealth(t *testing.T) {
	for _, code := range []string{
		boundary.CodeAllowlistUntrusted,
		boundary.CodeAllowlistInvalid,
	} {
		for _, auth := range []refusal.Authority{
			refusal.AuthorityDeployment,
			refusal.AuthorityCandidate,
			"",
		} {
			got, err := refusal.Classify(refusal.Refusal{Code: code, Authority: auth})
			if err != nil {
				t.Errorf("Classify(%q, auth=%q): unexpected error: %v", code, auth, err)
				continue
			}
			if got != refusal.ClassHealth {
				t.Errorf("Classify(%q, auth=%q) = %q, want %q (the allowlist trust codes stay out of candidate policy)",
					code, auth, got, refusal.ClassHealth)
			}
		}
	}
}

// TestMountAuthoritySplit is the one authority-dependent rule: the fourteen
// non-allowlist mount codes are candidate policy only when the failing source
// authority is the candidate's own profile/Project/record reference. The
// same code against a shared typed-system source is deployment health and must
// never charge, block, or end the selected subject.
func TestMountAuthoritySplit(t *testing.T) {
	candidateOwnable := []string{
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
	for _, code := range candidateOwnable {
		got, err := refusal.Classify(refusal.Refusal{Code: code, Authority: refusal.AuthorityCandidate})
		if err != nil {
			t.Errorf("Classify(%q, candidate): unexpected error: %v", code, err)
		} else if got != refusal.ClassCandidate {
			t.Errorf("Classify(%q, candidate) = %q, want %q", code, got, refusal.ClassCandidate)
		}

		got, err = refusal.Classify(refusal.Refusal{Code: code, Authority: refusal.AuthorityDeployment})
		if err != nil {
			t.Errorf("Classify(%q, deployment): unexpected error: %v", code, err)
		} else if got != refusal.ClassHealth {
			t.Errorf("Classify(%q, deployment) = %q, want %q (a shared typed-system source's failure is health, never the candidate's fault)",
				code, got, refusal.ClassHealth)
		}
	}
}

func TestClassifyCandidatePolicy(t *testing.T) {
	// The non-mount candidate-policy codes. Authority is not a dimension: only
	// a current candidate's own inputs can produce these.
	for _, code := range []string{
		refusal.CodeEnvInvalid,
		refusal.CodeEnvForbidden,
		refusal.CodeAuthBindingInvalid,
		refusal.CodeAuthDeliveryInvalid,
		refusal.CodeAuthCABindingMismatch,
		refusal.CodeNetworkRuleInvalid,
		refusal.CodeNetworkRuleUnresolved,
		refusal.CodeNetworkDestinationForbidden,
		refusal.CodeNetworkPolicyUnappliable,
		refusal.CodeNetworkPolicyMismatch,
		refusal.CodeIdentityNameInvalid,
	} {
		got, err := refusal.Classify(refusal.Refusal{Code: code})
		if err != nil {
			t.Errorf("Classify(%q): unexpected error: %v", code, err)
			continue
		}
		if got != refusal.ClassCandidate {
			t.Errorf("Classify(%q) = %q, want %q", code, got, refusal.ClassCandidate)
		}
	}
}

// TestAuthorityIsRequiredExactlyWhereItIsMeaningful keeps the authority field
// from becoming decorative. It is the sole discriminator for the fourteen
// candidate-ownable mount codes, so omitting it there is a protocol error
// rather than a silent default into either consequence; supplying it anywhere
// else is a producer confusion we refuse rather than ignore.
func TestAuthorityIsRequiredExactlyWhereItIsMeaningful(t *testing.T) {
	if _, err := refusal.Classify(refusal.Refusal{Code: boundary.CodeCrossProject}); err == nil {
		t.Errorf("Classify(mount.cross_project) with no authority: want error, got nil — the class is not derivable without it")
	}
	for _, code := range []string{
		refusal.CodeStale,
		refusal.CodeConfigInvalid,
		refusal.CodeEnvInvalid,
	} {
		_, err := refusal.Classify(refusal.Refusal{Code: code, Authority: refusal.AuthorityCandidate})
		if err == nil {
			t.Errorf("Classify(%q, candidate): want error, got nil — authority is not a dimension of this code", code)
		}
	}
}

func TestClassifyRejectsUnknownCode(t *testing.T) {
	// Fail-closed: an unrecognized code has no consequence, so it can never be
	// guessed into one. A new code is a taxonomy edit, not a runtime surprise.
	for _, code := range []string{
		"",
		"mount.not_a_real_code",
		"health.invented",
		"preflight",
		"MOUNT.SOURCE_MISSING",
		"mount.source_missing ",
		"task.block",
	} {
		if _, err := refusal.Classify(refusal.Refusal{Code: code, Authority: refusal.AuthorityCandidate}); err == nil {
			t.Errorf("Classify(%q): want error, got nil", code)
		}
	}
}

// The anti-drift guard — that every declared mount code has a class here —
// lives in boundary/codes_test.go, beside the closure check it strengthens.
// It belongs there because that test already owns "the mount.* set is exactly
// these sixteen"; adding a seventeenth must fail at the point of invention.

// --- The closed detail object ---------------------------------------------
//
// The stored and public error detail is the closed object {code,field,
// item_index,summary}: field and summary are enumerated identifiers,
// item_index is a bounded integer or null, and the whole canonical JSON has a
// cap of 512 bytes. It never contains a supplied path/value, home/username,
// env value, credential, nonce, URL query/body, header, raw hostname answer,
// or secret-derived prefix.

func TestDetailCanonicalShape(t *testing.T) {
	idx := 3
	d := refusal.Detail{
		Code:      boundary.CodeCrossProject,
		Field:     refusal.FieldMountSource,
		ItemIndex: &idx,
		Summary:   refusal.SummaryOutsideJurisdiction,
	}
	b, err := d.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("canonical detail is not JSON: %v (%s)", err, b)
	}
	if len(got) != 4 {
		t.Errorf("canonical detail has %d keys, want exactly 4 {code,field,item_index,summary}: %s", len(got), b)
	}
	for _, k := range []string{"code", "field", "item_index", "summary"} {
		if _, ok := got[k]; !ok {
			t.Errorf("canonical detail is missing key %q: %s", k, b)
		}
	}
	if got["code"] != boundary.CodeCrossProject {
		t.Errorf("code = %v, want %q", got["code"], boundary.CodeCrossProject)
	}
	if got["item_index"] != float64(3) {
		t.Errorf("item_index = %v, want 3", got["item_index"])
	}
}

func TestDetailNullItemIndex(t *testing.T) {
	d := refusal.Detail{
		Code:    refusal.CodeConfigInvalid,
		Field:   refusal.FieldConfig,
		Summary: refusal.SummaryUnparsable,
	}
	b, err := d.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if !strings.Contains(string(b), `"item_index":null`) {
		t.Errorf("a nil ItemIndex must encode as an explicit null, got %s", b)
	}
}

// TestDetailRejectsUnenumeratedIdentifiers is the leak-proof property, stated
// as a type rule rather than a scrub: Field and Summary are drawn from closed
// sets, so a supplied path, env value, or credential has nowhere in the object
// to land. A sanitizer that filtered free text would be one bug away from
// leaking; a closed enum is leak-proof by construction.
func TestDetailRejectsUnenumeratedIdentifiers(t *testing.T) {
	hostile := []string{
		"/Users/vinchenkov/Documents/dev/ai/homie",
		"AWS_SECRET_ACCESS_KEY=hunter2",
		"sk-ant-api03-abcdef",
		"https://evil.example/?token=deadbeef",
		"vinchenkov",
		"",
		"mount.sourc",       // a near-miss of a real identifier
		"mount.source_path", // identifier-shaped, deliberately not enumerated
	}
	for _, h := range hostile {
		d := refusal.Detail{
			Code:    boundary.CodeSourceMissing,
			Field:   refusal.Field(h),
			Summary: refusal.SummaryUnparsable,
		}
		if _, err := d.Canonical(); err == nil {
			t.Errorf("Detail.Canonical with Field=%q: want error, got nil", h)
		}
		d = refusal.Detail{
			Code:    boundary.CodeSourceMissing,
			Field:   refusal.FieldMountSource,
			Summary: refusal.Summary(h),
		}
		if _, err := d.Canonical(); err == nil {
			t.Errorf("Detail.Canonical with Summary=%q: want error, got nil", h)
		}
	}
}

// TestDetailNeverCarriesHostileInput asserts the exact code and detail bytes
// against hostile path, env, and credential-shaped inputs. It drives the real
// producer seam rather than a hand-built Detail: a boundary rejection whose
// message is full of secrets must render a detail with none of them.
func TestDetailNeverCarriesHostileInput(t *testing.T) {
	secrets := []string{
		"/Users/vinchenkov/.culler/auth.json",
		"vinchenkov",
		"hunter2",
		"sk-ant-api03-abcdef",
		"deadbeefcafe",
	}
	msg := "mount source /Users/vinchenkov/.culler/auth.json rejected " +
		"for user vinchenkov with token sk-ant-api03-abcdef nonce deadbeefcafe password hunter2"

	d, err := refusal.DetailFor(refusal.Refusal{
		Code:      boundary.CodeSourceBlocked,
		Authority: refusal.AuthorityCandidate,
		Field:     refusal.FieldMountSource,
		Summary:   refusal.SummaryBlockedFloor,
		Message:   msg,
	})
	if err != nil {
		t.Fatalf("DetailFor: %v", err)
	}
	b, err := d.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	for _, s := range secrets {
		if strings.Contains(string(b), s) {
			t.Errorf("canonical detail leaked %q: %s", s, b)
		}
	}
	want := `{"code":"mount.source_blocked","field":"mount.source","item_index":null,"summary":"blocked_floor"}`
	if string(b) != want {
		t.Errorf("canonical detail bytes:\n got %s\nwant %s", b, want)
	}
}

// TestDetailItemIndexIsBounded pins that item_index is a bounded integer or
// null. The bound is the largest plan collection (256 mounts / 256 env
// entries), so the widest legal index is 255; anything else is a producer bug
// refused rather than stored.
func TestDetailItemIndexIsBounded(t *testing.T) {
	for _, idx := range []int{-1, refusal.MaxItemIndex + 1, 1 << 30} {
		i := idx
		d := refusal.Detail{
			Code:      boundary.CodeSourceMissing,
			Field:     refusal.FieldMountSource,
			ItemIndex: &i,
			Summary:   refusal.SummaryMissing,
		}
		if _, err := d.Canonical(); err == nil {
			t.Errorf("Detail.Canonical with ItemIndex=%d: want error, got nil", idx)
		}
	}
	ok := refusal.MaxItemIndex
	d := refusal.Detail{
		Code:      boundary.CodeSourceMissing,
		Field:     refusal.FieldMountSource,
		ItemIndex: &ok,
		Summary:   refusal.SummaryMissing,
	}
	if _, err := d.Canonical(); err != nil {
		t.Errorf("Detail.Canonical at the declared max index %d: %v", ok, err)
	}
}

func TestDetailCanonicalCap(t *testing.T) {
	// The 512-byte cap is a hard bound on every enumerated combination, not a
	// runtime hope: prove the whole cross-product fits at the widest legal
	// index rather than trusting one sample.
	for _, f := range refusal.AllFields() {
		for _, s := range refusal.AllSummaries() {
			idx := refusal.MaxItemIndex
			d := refusal.Detail{Code: boundary.CodeRuntimeUnappliable, Field: f, ItemIndex: &idx, Summary: s}
			b, err := d.Canonical()
			if err != nil {
				t.Fatalf("Canonical(field=%q, summary=%q): %v", f, s, err)
			}
			if len(b) > 512 {
				t.Errorf("canonical detail for (field=%q, summary=%q) is %d bytes, over the 512-byte cap", f, s, len(b))
			}
		}
	}
}
