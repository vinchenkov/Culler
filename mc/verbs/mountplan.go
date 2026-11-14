package verbs

// The mount-plan validator. mc/boundary ships no plan orchestrator: this file
// owns the composition (trust, parse, resolve, jurisdiction-checked per-source
// authorization, cross-request destination uniqueness) and the adapter from
// *boundary.MountError to a typed refusal.Refusal.
//
// Two rules carry the fail-closed posture across the seam:
//
//   - Any rejection aborts the WHOLE plan. No mount is dropped, no access is
//     downgraded.
//   - Authority is the caller's knowledge, never derived from the error: which
//     source was candidate-authored vs deployment-shared is a fact about the
//     request, and mc/refusal deliberately errors on a guessed one.

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"mc/boundary"
	"mc/refusal"
)

// maxPlanMounts mirrors the private frame bound: a plan permits at most 256
// mounts. The write-side budgeter owns admission; this is the seam's own
// backstop, and it also keeps ItemIndex inside Detail's [0,255] domain.
const maxPlanMounts = 256

// mountClass names the destination family of a request. The container path
// is derived, never operator-chosen: the allowlist target is the stable
// external-root name and the class supplies its fixed prefix.
type mountClass string

const classArtifact mountClass = "artifact"

// The artifact family sits on `/mc`, NOT nested under `/workspace`, and that
// is a correctness requirement rather than a taste: every task-scoped role
// binds the task root at `/workspace` READ-ONLY, so a nested bind at
// `/workspace/artifacts/<target>` asks Docker to create a mountpoint inside a
// read-only filesystem and the container never starts —
//
//	create mountpoint …: mkdirat …/merged/workspace/artifacts: read-only file system
//
// `/mc` is image rootfs rather than a read-only bind, so its mountpoints are
// created in the writable overlay exactly as `/mc/outputs` and `/mc/scratch`
// already are on every spawn. Only the container destination is fixed here.
var mountClassPrefixes = map[mountClass]string{
	classArtifact: "mc/artifacts",
}

// mountRequest is one requested bind: the raw source spelling, the requested
// access, whose authority the request rides on (candidate-authored
// profile/Project state vs deployment-shared configuration), which is the
// sole class discriminator for the mount codes, and the destination class.
type mountRequest struct {
	Source    string
	Access    boundary.Access
	Authority refusal.Authority
	Class     mountClass
}

// PrivateDispatchMountEntry is one authorized bind of the closed plan carrier:
// ordered {logical_id,source,destination,kind,access} with captured host
// identity evidence. Device and inode are decimal strings so a JavaScript
// consumer can carry the plan without 2^53 truncation; the resident treats
// them as opaque and only the host recheck compares them.
//
// Fields are declared in alphabetical json-key order ON PURPOSE: the plan
// rides inside the spawn effect, whose exact-replay path round-trips through
// map[string]any (alphabetical re-encoding). Any other declared order breaks
// byte-for-byte lost-response replay.
type PrivateDispatchMountEntry struct {
	Access      string `json:"access"`
	Destination string `json:"destination"`
	Device      string `json:"device"`
	Inode       string `json:"inode"`
	Kind        string `json:"kind"`
	LogicalID   string `json:"logical_id"`
	Mode        int    `json:"mode"`
	OwnerUID    int    `json:"owner_uid"`
	Source      string `json:"source"`
}

// PrivateDispatchPathIdentity is host identity evidence carried as decimal
// strings so the JavaScript resident never truncates device/inode values.
// Field order is alphabetical because the containing plan is replayed through
// canonical JSON.
type PrivateDispatchPathIdentity struct {
	Canonical string `json:"canonical"`
	Device    string `json:"device"`
	Inode     string `json:"inode"`
	OwnerUID  int    `json:"owner_uid"`
}

// PrivateDispatchSeedingProjection authorizes one post-claim, run-scoped
// committed-tree projection for a Git-backed Project. The resident derives
// the projection path from run id + Project id; only the exact source root,
// target ref and object format cross this carrier.
type PrivateDispatchSeedingProjection struct {
	GitCoverKind   string                      `json:"git_cover_kind"`
	ObjectFormat   string                      `json:"object_format"`
	ProjectID      string                      `json:"project_id"`
	ProjectionKind string                      `json:"projection_kind"`
	TargetRef      string                      `json:"target_ref"`
	WorkspaceRoot  PrivateDispatchPathIdentity `json:"workspace_root"`
}

// PrivateDispatchMountPlan is the bounded authorization carrier: the complete
// validated plan the attestation and the committed spawn effect carry, and the
// only mount authority the resident may consume. Entries are sorted by
// destination (the declared key of a semantically unordered set). The
// alphabetical field order is load-bearing; see the entry type.
type PrivateDispatchMountPlan struct {
	Entries            []PrivateDispatchMountEntry        `json:"entries"`
	JurisdictionDigest string                             `json:"jurisdiction_digest,omitempty"`
	Repository         *PrivateRepositoryPlan             `json:"repository,omitempty"`
	SeedingProjections []PrivateDispatchSeedingProjection `json:"seeding_projections,omitempty"`
	ToolHome           *PrivateDispatchPathIdentity       `json:"tool_home,omitempty"`
	Version            int                                `json:"version"`
}

// MountAllowlistPath names the one file `MC_HOME/mount-allowlist` for every
// actor that touches it: the attester that feeds planMounts, the onboarding
// section that creates it, and the doctor check that proves it is still there.
// It is a single definition on purpose — the gap this closes existed because
// the only code that knew the name was the consumer, so nothing else could
// create or check the file, and a real install shipped without it.
func MountAllowlistPath(home string) string {
	return filepath.Join(home, "mount-allowlist")
}

type mountPlanInputs struct {
	AllowlistPath string
	OwnerUID      int
	Blocked       boundary.BlockPolicy
	Jurisdiction  boundary.Jurisdiction
}

// planMounts authorizes every requested source against the operator's
// allowlist, the blocked floor, and jurisdiction, then requires the derived
// absolute container destinations to be mutually collision-free (equal or
// ancestor-overlapping destinations reject, the same grammar the allowlist's
// own parse-time target set enforces). It returns exactly one of: the full
// evidence-backed plan entry set sorted by destination, a classified refusal,
// or a protocol error.
func planMounts(requests []mountRequest, in mountPlanInputs) ([]PrivateDispatchMountEntry, *refusal.Refusal, error) {
	if len(requests) == 0 {
		return nil, nil, nil
	}
	if len(requests) > maxPlanMounts {
		return nil, nil, Domainf("a mount plan permits at most %d mounts, got %d", maxPlanMounts, len(requests))
	}

	// The allowlist stages are request-independent: their two codes are the
	// authority-irrelevant carve-out (health whatever the attester says), so
	// no request authority or index attaches.
	if err := boundary.TrustPolicyFile(in.AllowlistPath, in.OwnerUID); err != nil {
		r, aerr := adaptMountError(err, "", nil)
		return nil, r, aerr
	}
	data, err := os.ReadFile(in.AllowlistPath)
	if err != nil {
		// Trusted an instant ago but unreadable now: the file cannot be the
		// one that was vouched for. Same consequence as untrusted.
		r, rerr := refusalForMountError(&boundary.MountError{
			Code: boundary.CodeAllowlistUntrusted, Msg: err.Error()}, "", nil)
		if rerr != nil {
			return nil, nil, rerr
		}
		return nil, &r, nil
	}
	allowlist, err := boundary.ParseMountAllowlist(data)
	if err != nil {
		r, aerr := adaptMountError(err, "", nil)
		return nil, r, aerr
	}
	// ResolveAllowlist fails with authority-DECIDES codes (source_alias for
	// overlapping roots; source_missing/wrong_kind/symlink_escape for a bad
	// root path). The allowlist and its roots are deployment-authored, so
	// the deployment carries the authority and the class lands on health —
	// an empty authority here would be unclassifiable and wedge dispatch on
	// a recoverable operator misconfig.
	resolved, err := boundary.ResolveAllowlist(allowlist)
	if err != nil {
		r, aerr := adaptMountError(err, refusal.AuthorityDeployment, nil)
		return nil, r, aerr
	}

	entries := make([]PrivateDispatchMountEntry, 0, len(requests))
	for i, req := range requests {
		i := i
		prefix, ok := mountClassPrefixes[req.Class]
		if !ok {
			return nil, nil, Domainf("mount request %d carries no destination class", i)
		}
		auth, err := resolved.Authorize(req.Source, req.Access, in.Blocked, in.Jurisdiction)
		if err != nil {
			r, aerr := adaptMountError(err, req.Authority, &i)
			return nil, r, aerr
		}
		dest := "/" + path.Join(prefix, auth.Target, auth.Suffix)
		logicalID := string(req.Class) + ":" + path.Join(auth.Target, auth.Suffix)
		identity, access := auth.Source, auth.Access
		for _, prior := range entries {
			overlap := dest == prior.Destination ||
				strings.HasPrefix(dest, prior.Destination+"/") ||
				strings.HasPrefix(prior.Destination, dest+"/")
			if overlap {
				r, rerr := refusalForMountError(&boundary.MountError{
					Code: boundary.CodeTargetCollision,
					Msg:  "destination " + dest + " collides with " + prior.Destination,
				}, req.Authority, &i)
				if rerr != nil {
					return nil, nil, rerr
				}
				return nil, &r, nil
			}
		}
		entry, err := planEntry(logicalID, dest, identity, access)
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Destination < entries[j].Destination })
	return entries, nil, nil
}

// planEntry captures the host identity evidence for one authorized
// source: the canonical resolved path plus the (device, inode, kind, owner,
// mode) identity the pre-create and pre-start rechecks compare.
func planEntry(logicalID, dest string, source boundary.SourceIdentity, access boundary.Access) (PrivateDispatchMountEntry, error) {
	st, ok := source.Info.Sys().(*syscall.Stat_t)
	if !ok {
		return PrivateDispatchMountEntry{}, Domainf("mount source %q has no native identity evidence", source.Canonical)
	}
	kind := "file"
	if source.IsDir {
		kind = "dir"
	}
	return PrivateDispatchMountEntry{
		LogicalID:   logicalID,
		Source:      source.Canonical,
		Destination: dest,
		Kind:        kind,
		Access:      string(access),
		Device:      strconv.FormatUint(uint64(st.Dev), 10),
		Inode:       strconv.FormatUint(st.Ino, 10),
		OwnerUID:    int(st.Uid),
		Mode:        int(source.Info.Mode().Perm()),
	}, nil
}

// adaptMountError unwraps a boundary rejection into (nil refusal, error) or
// (refusal, nil error) for planMounts' return shape. A non-MountError failure
// is a protocol error, never guessed into a code.
func adaptMountError(err error, authority refusal.Authority, itemIndex *int) (*refusal.Refusal, error) {
	var me *boundary.MountError
	if !errors.As(err, &me) {
		return nil, err
	}
	r, rerr := refusalForMountError(me, authority, itemIndex)
	if rerr != nil {
		return nil, rerr
	}
	return &r, nil
}

// refusalForMountError shapes one coded boundary rejection into the closed
// refusal enums. The pairing table is append-only alongside the boundary code
// set; an undeclared code refuses to shape (fail-closed, the same posture as
// boundary/codes_test.go's anti-drift guard). Msg — hostile input, it
// carries raw host paths — rides only in Message, which DetailFor drops.
func refusalForMountError(me *boundary.MountError, authority refusal.Authority, itemIndex *int) (refusal.Refusal, error) {
	shape, ok := mountErrorShapes[me.Code]
	if !ok {
		return refusal.Refusal{}, Domainf("boundary code %q has no refusal shape — a new code needs its shape row before production", me.Code)
	}
	return refusal.Refusal{
		Code:      me.Code,
		Authority: authority,
		Field:     shape.field,
		Summary:   shape.summary,
		ItemIndex: itemIndex,
		Message:   me.Msg,
	}, nil
}

var mountErrorShapes = map[string]struct {
	field   refusal.Field
	summary refusal.Summary
}{
	boundary.CodeAllowlistUntrusted: {refusal.FieldAllowlist, refusal.SummaryUntrusted},
	boundary.CodeAllowlistInvalid:   {refusal.FieldAllowlist, refusal.SummaryUnparsable},
	boundary.CodeSourceMissing:      {refusal.FieldMountSource, refusal.SummaryMissing},
	boundary.CodeSourceWrongKind:    {refusal.FieldMountSource, refusal.SummaryWrongKind},
	boundary.CodeSourceBlocked:      {refusal.FieldMountSource, refusal.SummaryBlockedFloor},
	boundary.CodeSymlinkEscape:      {refusal.FieldMountSource, refusal.SummaryEscapesRoot},
	boundary.CodeNotAllowlisted:     {refusal.FieldMountSource, refusal.SummaryNotAllowlisted},
	boundary.CodeDeniedRoot:         {refusal.FieldMountSource, refusal.SummaryOutsideJurisdiction},
	boundary.CodeCrossProject:       {refusal.FieldMountSource, refusal.SummaryOutsideJurisdiction},
	boundary.CodeRWNotPermitted:     {refusal.FieldMountSource, refusal.SummaryForbidden},
	boundary.CodeTargetInvalid:      {refusal.FieldMountTarget, refusal.SummaryUnparsable},
	boundary.CodeSourceAlias:        {refusal.FieldMountSource, refusal.SummaryCollision},
	boundary.CodeTargetCollision:    {refusal.FieldMountTarget, refusal.SummaryCollision},
	boundary.CodeIdentityChanged:    {refusal.FieldMountSource, refusal.SummaryIdentityChanged},
	boundary.CodeRuntimeUnappliable: {refusal.FieldRuntime, refusal.SummaryUnappliable},
	boundary.CodeGateUnhealthy:      {refusal.FieldGateway, refusal.SummaryUnavailable},
}
