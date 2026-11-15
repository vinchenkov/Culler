package verbs

// The launch-time identity legs: the resident re-runs authorization
// identity/trust for the committed plan immediately before Docker create and
// again after create/before start, via `mc __mount-recheck <plan-file>`. The
// verb is host-scope and read-only: it opens no spine and mutates nothing;
// its whole consequence is the exit code the resident acts on (any rejection
// removes the unstarted container).

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"syscall"

	"mc/boundary"
)

// MountRecheck validates the plan file's own trust seam, decodes the closed
// carrier strictly, and requires every entry's source to still resolve to the
// same canonical path and (device,inode,kind,owner,mode) identity captured at
// attest. Drift is a coded domain rejection, never repaired or downgraded.
func MountRecheck(path string) (map[string]any, error) {
	if err := boundary.TrustPolicyFile(path, os.Getuid()); err != nil {
		return nil, &DomainError{Code: boundary.CodeAllowlistUntrusted,
			Msg: "mount recheck: plan file is untrusted: " + err.Error()}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &DomainError{Code: boundary.CodeAllowlistUntrusted,
			Msg: "mount recheck: plan file vanished after trust: " + err.Error()}
	}
	if len(data) > maxDispatchMountPlanBytes {
		return nil, Domainf("mount recheck: plan file exceeds its byte budget")
	}
	var plan PrivateDispatchMountPlan
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return nil, Domainf("mount recheck: plan file is not a closed mount plan: %v", err)
	}
	if dec.More() {
		return nil, Domainf("mount recheck: plan file carries trailing data")
	}
	if err := validatePrivateMountPlan(&plan); err != nil {
		return nil, err
	}
	for i, entry := range plan.Entries {
		if err := recheckMountEntry(entry); err != nil {
			return nil, &DomainError{Code: boundary.CodeIdentityChanged,
				Msg: "mount recheck: entry " + strconv.Itoa(i) + " (" + entry.LogicalID + "): " + err.Error()}
		}
	}
	for i, step := range plan.SeedingProjections {
		if err := recheckSeedingWorkspace(step.WorkspaceRoot); err != nil {
			return nil, &DomainError{Code: boundary.CodeIdentityChanged,
				Msg: "mount recheck: seeding projection " + strconv.Itoa(i) + " (" + step.ProjectID + "): " + err.Error()}
		}
	}
	if plan.ToolHome != nil {
		if err := recheckSyntheticHome(*plan.ToolHome); err != nil {
			return nil, &DomainError{Code: boundary.CodeIdentityChanged,
				Msg: "mount recheck: synthetic home: " + err.Error()}
		}
	}
	return map[string]any{"action": "mount-recheck", "status": "ok", "entries": len(plan.Entries)}, nil
}

func recheckSeedingWorkspace(expected PrivateDispatchPathIdentity) error {
	identity, err := boundary.ResolveSource(expected.Canonical)
	if err != nil {
		return err
	}
	st, ok := identity.Info.Sys().(*syscall.Stat_t)
	if !ok || !identity.IsDir || identity.Canonical != expected.Canonical ||
		strconv.FormatUint(uint64(st.Dev), 10) != expected.Device ||
		strconv.FormatUint(st.Ino, 10) != expected.Inode || int(st.Uid) != expected.OwnerUID {
		return Domainf("source root identity or ownership changed after attestation")
	}
	return nil
}

func recheckSyntheticHome(expected PrivateDispatchPathIdentity) error {
	if err := boundary.TrustHomeDir(expected.Canonical, expected.OwnerUID); err != nil {
		return err
	}
	identity, err := boundary.ResolveSource(expected.Canonical)
	if err != nil {
		return err
	}
	st, ok := identity.Info.Sys().(*syscall.Stat_t)
	if !ok || !identity.IsDir || identity.Canonical != expected.Canonical ||
		identity.Info.Mode().Perm() != 0o700 ||
		strconv.FormatUint(uint64(st.Dev), 10) != expected.Device ||
		strconv.FormatUint(st.Ino, 10) != expected.Inode ||
		int(st.Uid) != expected.OwnerUID || int(st.Uid) != os.Getuid() {
		return Domainf("identity, ownership, or mode changed after attestation")
	}
	return nil
}

// recheckMountEntry repeats the identity half of the attest predicate for one
// carried source: same canonical resolution (a swapped symlink component
// changes it), same filesystem object, same kind, owner, and mode grant bits.
func recheckMountEntry(entry PrivateDispatchMountEntry) error {
	identity, err := boundary.ResolveSource(entry.Source)
	if err != nil {
		return err
	}
	if identity.Canonical != entry.Source {
		return Domainf("source resolves to %q, not its attested canonical path", identity.Canonical)
	}
	st, ok := identity.Info.Sys().(*syscall.Stat_t)
	if !ok {
		return Domainf("source has no native identity evidence")
	}
	kind := "file"
	if identity.IsDir {
		kind = "dir"
	}
	if strconv.FormatUint(uint64(st.Dev), 10) != entry.Device ||
		strconv.FormatUint(st.Ino, 10) != entry.Inode {
		return Domainf("source is a different filesystem object than the attested one")
	}
	if kind != entry.Kind {
		return Domainf("source kind changed from the attested %q", entry.Kind)
	}
	if int(st.Uid) != entry.OwnerUID {
		return Domainf("source owner changed from the attested uid")
	}
	if int(identity.Info.Mode().Perm()) != entry.Mode {
		return Domainf("source mode grant changed from the attested bits")
	}
	return nil
}
