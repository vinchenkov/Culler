// Package refusal holds the classified-refusal taxonomy: the closed code set,
// the consequence class each code carries, and the closed stored/public detail
// object.
//
// It is deliberately pure and storage-free. Deciding a class is a total
// function of (code, source authority); applying the consequence — block the
// subject task, record deployment health, or launch-fence-end a Collie — is the
// dispatch seam's job, under its own transaction. Keeping the two apart is what
// lets the whole table be proven by table-driven tests with no spine at all.
//
// The package imports mc/boundary for the mount.* constants rather than
// restating them: the mount vocabulary has one home, and a second copy would
// drift. boundary/codes_test.go closes the set from the other side by requiring
// every declared mount code to have a class here.
package refusal

import (
	"encoding/json"
	"fmt"

	"mc/boundary"
)

// Class is one of the three closed consequence classes.
type Class string

const (
	// ClassStale is the stale/protocol row: error/retry, no durable mutation
	// or effect. A concurrent operator edit lands here, which is why it can
	// never punish a task.
	ClassStale Class = "stale"

	// ClassHealth is the deployment-health row: one health action, no claim
	// and no task charge/block.
	ClassHealth Class = "health"

	// ClassCandidate is the candidate-policy row. The consequence is
	// subject-dependent — block the subject task, health when subjectless, or a
	// launch-fenced Collie end — so the class alone does not name it.
	ClassCandidate Class = "candidate"
)

// Authority names who owns the source a mount refusal failed on.
//
// It is meaningful for exactly the fourteen candidate-ownable mount codes, and
// is the sole discriminator between the health and candidate-policy rows for
// them: the same code against a shared typed-system source is the deployment's
// problem, and against the candidate's own profile/Project/record reference is
// the candidate's. The table fixes every other code's class, whoever observed
// it, so carrying an authority there is a category error.
type Authority string

const (
	AuthorityDeployment Authority = "deployment"
	AuthorityCandidate  Authority = "candidate"
)

// The stale/protocol codes.
const (
	CodeStale             = "preflight.stale"
	CodeFrameInvalid      = "preflight.frame_invalid"
	CodeVersionMismatch   = "preflight.version_mismatch"
	CodeCandidateMismatch = "preflight.candidate_mismatch"
	CodeInventoryChanged  = "preflight.inventory_changed"
	CodeOversize          = "preflight.oversize"
)

// The deployment-health codes. The two allowlist trust codes are also health
// but live in the mount.* namespace; they come from mc/boundary.
const (
	CodeRuntimeUnavailable            = "health.runtime_unavailable"
	CodeHelperUnavailable             = "health.helper_unavailable"
	CodeImageUnavailable              = "health.image_unavailable"
	CodeGatewayUnavailable            = "health.gateway_unavailable"
	CodeNetworkCapabilityUnavailable  = "health.network_capability_unavailable"
	CodeResourceCapabilityUnavailable = "health.resource_capability_unavailable"
	CodeResourceConfigInvalid         = "health.resource_config_invalid"
	CodeFilePlaneUnavailable          = "health.file_plane_unavailable"
	CodeProjectionUnavailable         = "health.projection_unavailable"
	CodeConfigInvalid                 = "health.config_invalid"
	CodeRoutingInvalid                = "health.routing_invalid"

	// CodeEffectOversize: the deployment decided something it cannot record.
	// The effect built for a committed decision exceeds the receipt column or
	// the crossing that must carry it, so the decision cannot be stored or
	// delivered.
	//
	// Health rather than stale, and the distinction is the whole point. A stale
	// refusal means "re-read and decide again", which is right when the world
	// moved. Here nothing moved: selection is furthest-first and
	// time-invariant, so the next tick re-decides identically and fails
	// identically, forever. Health is the only class that carries a budget,
	// and the budget is what ends it.
	CodeEffectOversize = "health.effect_oversize"
)

// The non-mount candidate-policy codes. Only a current candidate's own inputs
// can produce these, so authority is not one of their dimensions.
const (
	CodeEnvInvalid                  = "env.invalid"
	CodeEnvForbidden                = "env.forbidden"
	CodeAuthBindingInvalid          = "auth.binding_invalid"
	CodeAuthDeliveryInvalid         = "auth.delivery_invalid"
	CodeAuthCABindingMismatch       = "auth.ca_binding_mismatch"
	CodeNetworkRuleInvalid          = "network.rule_invalid"
	CodeNetworkRuleUnresolved       = "network.rule_unresolved"
	CodeNetworkDestinationForbidden = "network.destination_forbidden"
	CodeNetworkPolicyUnappliable    = "network.policy_unappliable"
	CodeNetworkPolicyMismatch       = "network.policy_mismatch"
	CodeIdentityNameInvalid         = "identity.name_invalid"
)

// authorityRule says how a code treats the Authority field.
type authorityRule int

const (
	// authorityForbidden: not a mount refusal. The table fixes the class, so an
	// authority is a producer confusion — refused, not ignored.
	authorityForbidden authorityRule = iota

	// authorityDecides: one of the fourteen candidate-ownable mount codes.
	// Authority is the class, so omitting it is a protocol error rather than a
	// silent default into either consequence.
	authorityDecides

	// authorityIrrelevant: the two allowlist trust codes. The taxonomy carves
	// them out of candidate policy explicitly, so they are health whatever the
	// attester says. Accepting any authority here is deliberate and
	// fail-closed: the deployment's own allowlist file must never become a
	// task's fault because a frame was mislabeled.
	authorityIrrelevant
)

type rule struct {
	class Class // the class when authority does not decide
	auth  authorityRule
}

// table is the consequence table, whole. It is the package's single source of
// truth: Classify, Detail validation, and boundary's closure guard all read it.
var table = map[string]rule{
	// Stale/protocol.
	CodeStale:             {ClassStale, authorityForbidden},
	CodeFrameInvalid:      {ClassStale, authorityForbidden},
	CodeVersionMismatch:   {ClassStale, authorityForbidden},
	CodeCandidateMismatch: {ClassStale, authorityForbidden},
	CodeInventoryChanged:  {ClassStale, authorityForbidden},
	CodeOversize:          {ClassStale, authorityForbidden},

	// Deployment health.
	CodeRuntimeUnavailable:            {ClassHealth, authorityForbidden},
	CodeHelperUnavailable:             {ClassHealth, authorityForbidden},
	CodeImageUnavailable:              {ClassHealth, authorityForbidden},
	CodeGatewayUnavailable:            {ClassHealth, authorityForbidden},
	CodeNetworkCapabilityUnavailable:  {ClassHealth, authorityForbidden},
	CodeResourceCapabilityUnavailable: {ClassHealth, authorityForbidden},
	CodeResourceConfigInvalid:         {ClassHealth, authorityForbidden},
	CodeFilePlaneUnavailable:          {ClassHealth, authorityForbidden},
	CodeProjectionUnavailable:         {ClassHealth, authorityForbidden},
	CodeConfigInvalid:                 {ClassHealth, authorityForbidden},
	CodeRoutingInvalid:                {ClassHealth, authorityForbidden},
	CodeEffectOversize:                {ClassHealth, authorityForbidden},

	// Deployment health, mount.* namespace: allowlist file trust is the
	// deployment's, never a candidate's.
	boundary.CodeAllowlistUntrusted: {ClassHealth, authorityIrrelevant},
	boundary.CodeAllowlistInvalid:   {ClassHealth, authorityIrrelevant},

	// The fourteen candidate-ownable mount codes: authority decides.
	boundary.CodeSourceMissing:      {"", authorityDecides},
	boundary.CodeSourceWrongKind:    {"", authorityDecides},
	boundary.CodeSourceBlocked:      {"", authorityDecides},
	boundary.CodeSymlinkEscape:      {"", authorityDecides},
	boundary.CodeNotAllowlisted:     {"", authorityDecides},
	boundary.CodeDeniedRoot:         {"", authorityDecides},
	boundary.CodeCrossProject:       {"", authorityDecides},
	boundary.CodeRWNotPermitted:     {"", authorityDecides},
	boundary.CodeTargetInvalid:      {"", authorityDecides},
	boundary.CodeSourceAlias:        {"", authorityDecides},
	boundary.CodeTargetCollision:    {"", authorityDecides},
	boundary.CodeIdentityChanged:    {"", authorityDecides},
	boundary.CodeRuntimeUnappliable: {"", authorityDecides},
	boundary.CodeGateUnhealthy:      {"", authorityDecides},

	// Candidate policy, non-mount.
	CodeEnvInvalid:                  {ClassCandidate, authorityForbidden},
	CodeEnvForbidden:                {ClassCandidate, authorityForbidden},
	CodeAuthBindingInvalid:          {ClassCandidate, authorityForbidden},
	CodeAuthDeliveryInvalid:         {ClassCandidate, authorityForbidden},
	CodeAuthCABindingMismatch:       {ClassCandidate, authorityForbidden},
	CodeNetworkRuleInvalid:          {ClassCandidate, authorityForbidden},
	CodeNetworkRuleUnresolved:       {ClassCandidate, authorityForbidden},
	CodeNetworkDestinationForbidden: {ClassCandidate, authorityForbidden},
	CodeNetworkPolicyUnappliable:    {ClassCandidate, authorityForbidden},
	CodeNetworkPolicyMismatch:       {ClassCandidate, authorityForbidden},
	CodeIdentityNameInvalid:         {ClassCandidate, authorityForbidden},
}

// Refusal is one classified boundary refusal, as the attester hands it to the
// dispatch seam.
type Refusal struct {
	// Code is one of the closed codes.
	Code string

	// Authority is required for the fourteen candidate-ownable mount codes and
	// must be empty otherwise. See Authority.
	Authority Authority

	// Field and Summary are the enumerated identifiers that will be stored.
	Field   Field
	Summary Summary

	// ItemIndex names a position in the offending collection, or nil.
	ItemIndex *int

	// Message is a host-side diagnostic. It is deliberately NOT part of the
	// stored detail and must never be logged or rendered: it carries the raw
	// producer text, which the stored detail must never carry (supplied paths,
	// env values, credentials, nonces). DetailFor drops it, and that drop is
	// the sanitizer.
	Message string
}

// Classify returns the consequence class for a refusal.
//
// Unknown codes and incoherent (code, authority) pairs are rejected rather than
// guessed: a class this function cannot derive is a protocol error, never a
// default. That is the fail-closed direction — a refusal that does not classify
// applies no consequence at all, where a guessed class could block a task for
// the deployment's mistake.
func Classify(r Refusal) (Class, error) {
	ru, ok := table[r.Code]
	if !ok {
		return "", fmt.Errorf("refusal: %q is not a classified refusal code", r.Code)
	}
	switch ru.auth {
	case authorityIrrelevant:
		// The carve-out: health whatever the attester claims.
		return ru.class, nil
	case authorityForbidden:
		if r.Authority != "" {
			return "", fmt.Errorf("refusal: code %q fixes its own class; authority %q is not one of its dimensions", r.Code, r.Authority)
		}
		return ru.class, nil
	case authorityDecides:
		switch r.Authority {
		case AuthorityCandidate:
			return ClassCandidate, nil
		case AuthorityDeployment:
			return ClassHealth, nil
		case "":
			return "", fmt.Errorf("refusal: code %q needs a source authority; its class is not derivable without one", r.Code)
		default:
			return "", fmt.Errorf("refusal: %q is not a known source authority", r.Authority)
		}
	}
	return "", fmt.Errorf("refusal: code %q has no authority rule", r.Code)
}

// Field is an enumerated identifier naming the structural position a refusal is
// about. It is a closed set, not free text — see Detail.
type Field string

const (
	FieldMountSource  Field = "mount.source"
	FieldMountTarget  Field = "mount.target"
	FieldAllowlist    Field = "allowlist"
	FieldConfig       Field = "config"
	FieldRouting      Field = "routing"
	FieldEnvName      Field = "env.name"
	FieldAuthBinding  Field = "auth.binding"
	FieldNetworkRule  Field = "network.rule"
	FieldIdentityName Field = "identity.name"
	FieldRuntime      Field = "runtime"
	FieldImage        Field = "image"
	FieldResource     Field = "resource"
	FieldGateway      Field = "gateway"
	FieldFilePlane    Field = "file_plane"
	FieldProjection   Field = "projection"
	FieldNone         Field = "none"
)

// Summary is an enumerated identifier naming what went wrong. Closed set.
type Summary string

const (
	SummaryUnparsable          Summary = "unparsable"
	SummaryUntrusted           Summary = "untrusted"
	SummaryMissing             Summary = "missing"
	SummaryWrongKind           Summary = "wrong_kind"
	SummaryBlockedFloor        Summary = "blocked_floor"
	SummaryNotAllowlisted      Summary = "not_allowlisted"
	SummaryOutsideJurisdiction Summary = "outside_jurisdiction"
	SummaryEscapesRoot         Summary = "escapes_root"
	SummaryCollision           Summary = "collision"
	SummaryIdentityChanged     Summary = "identity_changed"
	SummaryForbidden           Summary = "forbidden"
	SummaryUnresolved          Summary = "unresolved"
	SummaryUnappliable         Summary = "unappliable"
	SummaryMismatch            Summary = "mismatch"
	SummaryUnavailable         Summary = "unavailable"
	SummaryOversize            Summary = "oversize"
)

var fieldSet = map[Field]bool{
	FieldMountSource: true, FieldMountTarget: true, FieldAllowlist: true,
	FieldConfig: true, FieldRouting: true, FieldEnvName: true,
	FieldAuthBinding: true, FieldNetworkRule: true, FieldIdentityName: true,
	FieldRuntime: true, FieldImage: true, FieldResource: true,
	FieldGateway: true, FieldFilePlane: true, FieldProjection: true,
	FieldNone: true,
}

var summarySet = map[Summary]bool{
	SummaryUnparsable: true, SummaryUntrusted: true, SummaryMissing: true,
	SummaryWrongKind: true, SummaryBlockedFloor: true, SummaryNotAllowlisted: true,
	SummaryOutsideJurisdiction: true, SummaryEscapesRoot: true, SummaryCollision: true,
	SummaryIdentityChanged: true, SummaryForbidden: true, SummaryUnresolved: true,
	SummaryUnappliable: true, SummaryMismatch: true, SummaryUnavailable: true,
	SummaryOversize: true,
}

// AllFields returns every enumerated Field, sorted, for exhaustive tests.
func AllFields() []Field {
	out := make([]Field, 0, len(fieldSet))
	for f := range fieldSet {
		out = append(out, f)
	}
	sortStrings(out)
	return out
}

// AllSummaries returns every enumerated Summary, sorted, for exhaustive tests.
func AllSummaries() []Summary {
	out := make([]Summary, 0, len(summarySet))
	for s := range summarySet {
		out = append(out, s)
	}
	sortStrings(out)
	return out
}

func sortStrings[T ~string](xs []T) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// MaxItemIndex is the bound on item_index. It is the largest plan collection
// (256 mounts, 256 environment entries) minus one, so every legal position in
// the widest bounded collection encodes and nothing else does.
const MaxItemIndex = 255

// maxDetailBytes is the hard cap on the canonical detail JSON.
const maxDetailBytes = 512

// Detail is the closed stored/public error detail: {code,field,item_index,
// summary}.
//
// There is deliberately no free-text member. The stored detail must never carry
// a supplied path/value, home/username, env value, credential, nonce, URL
// query/body, header, raw hostname answer, or secret-derived prefix — and
// the way to guarantee that is to leave nowhere to put one. A sanitizer over
// free text would be one regex away from leaking; a closed enum cannot leak at
// all. Field and Summary are therefore enumerated identifiers, not strings.
type Detail struct {
	Code      string
	Field     Field
	ItemIndex *int
	Summary   Summary
}

// canonicalDetail fixes the declared key order the canonical encoding
// requires. Field order here IS the wire order.
type canonicalDetail struct {
	Code      string `json:"code"`
	Field     string `json:"field"`
	ItemIndex *int   `json:"item_index"`
	Summary   string `json:"summary"`
}

// DetailFor projects a Refusal onto its storable Detail, dropping Message.
// That drop is the sanitizer: the raw producer text never reaches the spine.
func DetailFor(r Refusal) (Detail, error) {
	if _, err := Classify(r); err != nil {
		return Detail{}, err
	}
	d := Detail{Code: r.Code, Field: r.Field, ItemIndex: r.ItemIndex, Summary: r.Summary}
	if err := d.validate(); err != nil {
		return Detail{}, err
	}
	return d, nil
}

// Canonical encodes the detail as the capped canonical JSON.
func (d Detail) Canonical() ([]byte, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(canonicalDetail{
		Code:      d.Code,
		Field:     string(d.Field),
		ItemIndex: d.ItemIndex,
		Summary:   string(d.Summary),
	})
	if err != nil {
		return nil, err
	}
	// The enumerated cross-product is proven to fit, so this is a backstop
	// against a future enum entry widening the object past the cap unnoticed.
	if len(b) > maxDetailBytes {
		return nil, fmt.Errorf("refusal: canonical detail is %d bytes, over the %d-byte cap", len(b), maxDetailBytes)
	}
	return b, nil
}

func (d Detail) validate() error {
	if _, ok := table[d.Code]; !ok {
		return fmt.Errorf("refusal: %q is not a classified refusal code", d.Code)
	}
	if !fieldSet[d.Field] {
		return fmt.Errorf("refusal: field %q is not an enumerated identifier", d.Field)
	}
	if !summarySet[d.Summary] {
		return fmt.Errorf("refusal: summary %q is not an enumerated identifier", d.Summary)
	}
	if d.ItemIndex != nil && (*d.ItemIndex < 0 || *d.ItemIndex > MaxItemIndex) {
		return fmt.Errorf("refusal: item_index %d is outside the bound [0,%d]", *d.ItemIndex, MaxItemIndex)
	}
	return nil
}
