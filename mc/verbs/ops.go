package verbs

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mc/boundary"
	"mc/routing"
	"mc/substrate"
)

// checkMountAllowlist puts doctor through the SAME THREE gates the dispatch
// mount planner uses (mountplan.go: TrustPolicyFile, ParseMountAllowlist,
// ResolveAllowlist), so a deployment doctor calls healthy is one those gates
// will accept. A weaker restatement — "the file exists" — would have passed
// every shape that actually broke this deployment except the missing one.
//
// The third gate was missing and cost a whole board. ResolveAllowlist resolves
// EVERY allow root, because its uniqueness law compares filesystem identity
// rather than strings — so ONE root that no longer exists refuses every mount
// in the deployment, for every Project and every role, permanently. A smoke
// test once deleted its transient workspace and left its artifact grant
// behind, and from that moment every filesystem-bearing dispatch refused
// `mount.source_missing` naming a path inside an archived Project nobody was
// dispatching. The refusal envelope
// drops the message, so the board simply stopped with no readable cause.
//
// Parsing is not resolving, and that distance is the entire failure. Doctor now
// names the offending roots.
func checkMountAllowlist(home string) error {
	path := MountAllowlistPath(home)
	// Absence is checked ahead of trust rather than recovered from it:
	// TrustPolicyFile wraps its lstat in a *boundary.MountError, so os.IsNotExist
	// is false there and the operator would be told the file is "untrusted"
	// — advice to inspect permissions on a file that is simply not there.
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return fmt.Errorf("no mount-allowlist at %q — every mounted dispatch refuses "+
			"until it exists; repair: run install.sh, which writes the file during setup", path)
	}
	if err := boundary.TrustPolicyFile(path, os.Getuid()); err != nil {
		return fmt.Errorf("mount-allowlist %q is untrusted: %v", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read mount-allowlist %q: %v", path, err)
	}
	allowlist, err := boundary.ParseMountAllowlist(data)
	if err != nil {
		return fmt.Errorf("mount-allowlist %q does not parse: %v", path, err)
	}
	if _, err := boundary.ResolveAllowlist(allowlist); err != nil {
		// Name the dangling roots outright. The resolver reports only the first
		// one it hits, and an operator told to fix one path at a time against a
		// dead board will run doctor once per entry.
		var missing []string
		for _, entry := range allowlist.Entries() {
			if _, statErr := os.Lstat(entry.Path); os.IsNotExist(statErr) {
				missing = append(missing, entry.Path)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("mount-allowlist %q grants %d root(s) that do not exist, "+
				"which refuses EVERY mounted dispatch in the deployment until removed: %v",
				path, len(missing), missing)
		}
		return fmt.Errorf("mount-allowlist %q does not resolve, so every mounted dispatch refuses: %v", path, err)
	}
	return nil
}

// Operational verbs: doctor, backup, reset. All three are strictly host scope
// — the refusal precedes any spine open.

// mcHomeDir resolves the deployment root: the MC_HOME override or
// the ~/.culler default, always absolute.
func mcHomeDir() (string, error) {
	home := os.Getenv("MC_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", Usagef("resolve default MC_HOME: %v", err)
		}
		home = filepath.Join(userHome, ".culler")
	}
	if !filepath.IsAbs(home) {
		return "", Usagef("MC_HOME must be absolute, got %q", home)
	}
	return filepath.Clean(home), nil
}

// snapshotSpine writes one consistent spine snapshot into MC_HOME/backups/
// via VACUUM INTO — a fresh copy, never the live locked file — to
// a temp name, renamed on completion. Retention is the resident tick
// chore's job, not this verb's.
func snapshotSpine(db *sql.DB, home string) (string, int64, error) {
	// Retention is the operator's number, and there is no compiled
	// fallback: pruning on a guess deletes durability nobody asked to lose.
	cfg, _, err := loadOperatorConfig()
	if err != nil {
		return "", 0, err
	}
	dir, final, size, err := writeSpineSnapshot(db, home, "spine-")
	if err != nil {
		return "", 0, err
	}
	if err := pruneBackups(dir, cfg.Timing.BackupRetain); err != nil {
		return "", 0, Domainf("prune backups: %v", err)
	}
	return final, size, nil
}

// writeSpineSnapshot writes a complete snapshot before publication. The caller applies retention.
func writeSpineSnapshot(db *sql.DB, home, prefix string) (string, string, int64, error) {
	dir, err := requireSecureBackupDir(home)
	if err != nil {
		return "", "", 0, Domainf("secure backups dir: %v", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	var final string
	for i := 0; ; i++ {
		name := prefix + stamp
		if i > 0 {
			name = fmt.Sprintf("%s-%d", name, i)
		}
		final = filepath.Join(dir, name+".db")
		if _, err := os.Stat(final); os.IsNotExist(err) {
			break
		} else if err != nil {
			return "", "", 0, Domainf("probe snapshot name %q: %v", final, err)
		}
	}
	tmp := fmt.Sprintf("%s.tmp-%d", final, os.Getpid())
	if _, err := db.Exec(`VACUUM INTO ?`, tmp); err != nil {
		os.Remove(tmp)
		return "", "", 0, Domainf("snapshot spine: %v", err)
	}
	staged, err := os.OpenFile(tmp, os.O_RDWR, 0)
	if err != nil {
		os.Remove(tmp)
		return "", "", 0, Domainf("open staged snapshot: %v", err)
	}
	if err := staged.Chmod(0o600); err != nil {
		staged.Close()
		os.Remove(tmp)
		return "", "", 0, Domainf("secure staged snapshot: %v", err)
	}
	if err := staged.Sync(); err != nil {
		staged.Close()
		os.Remove(tmp)
		return "", "", 0, Domainf("sync staged snapshot: %v", err)
	}
	if err := staged.Close(); err != nil {
		os.Remove(tmp)
		return "", "", 0, Domainf("close staged snapshot: %v", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", "", 0, Domainf("finalize snapshot: %v", err)
	}
	if err := syncDir(dir); err != nil {
		return "", "", 0, Domainf("sync backups dir: %v", err)
	}
	st, err := os.Stat(final)
	if err != nil {
		return "", "", 0, Domainf("stat snapshot: %v", err)
	}
	return dir, final, st.Size(), nil
}

// requireSpineFile refuses before creating bytes: opening a missing path
// would materialize an empty spine, which no operational verb may do: the
// empty file hides the loss instead of reporting it.
func requireSpineFile(spine string) error {
	if spine == "" {
		return Usagef("no spine: set MC_SPINE (or MC_HELPER to delegate)")
	}
	if _, err := os.Stat(spine); err != nil {
		return Domainf("no spine at %q: %v", spine, err)
	}
	return nil
}

// Backup is the snapshot verb. The resident tick chore runs it on a schedule,
// and it also runs on demand.
func Backup(id *RunIdentity, spine string) (any, error) {
	if err := RequireHostScope(id, "mc backup"); err != nil {
		return nil, err
	}
	if err := requireSpineFile(spine); err != nil {
		return nil, err
	}
	home, err := mcHomeDir()
	if err != nil {
		return nil, err
	}
	db, err := substrate.Open(spine)
	if err != nil {
		return nil, Usagef("%v", err)
	}
	defer db.Close()
	snapshot, size, err := snapshotSpine(db, home)
	if err != nil {
		return nil, err
	}
	return map[string]any{"snapshot": snapshot, "bytes": size}, nil
}

// Reset is the one destructive verb: confirmation-gated, and the
// snapshot must complete before anything is deleted — a failed snapshot
// aborts with the spine untouched. Deleting the spine file (+ WAL/SHM
// siblings) is the dev-tier stand-in for volume teardown. Output carries
// paths only, never config or secrets.
func Reset(id *RunIdentity, spine string, confirm bool) (any, error) {
	if err := RequireHostScope(id, "mc __reset"); err != nil {
		return nil, err
	}
	if err := requireSpineFile(spine); err != nil {
		return nil, err
	}
	if !confirm {
		return nil, Domainf("mc __reset is destructive: it snapshots the spine into MC_HOME/backups/ and then deletes %q; re-run with --confirm", spine)
	}
	home, err := mcHomeDir()
	if err != nil {
		return nil, err
	}
	db, err := substrate.Open(spine)
	if err != nil {
		return nil, Usagef("%v", err)
	}
	snapshot, _, err := snapshotSpine(db, home)
	db.Close()
	if err != nil {
		return nil, err
	}
	if err := os.Remove(spine); err != nil {
		return nil, Domainf("delete spine %q (snapshot %q is safe): %v", spine, snapshot, err)
	}
	for _, sibling := range []string{spine + "-wal", spine + "-shm"} {
		if err := os.Remove(sibling); err != nil && !os.IsNotExist(err) {
			return nil, Domainf("delete %q (snapshot %q is safe): %v", sibling, snapshot, err)
		}
	}
	return map[string]any{"spine": spine, "snapshot": snapshot, "reset": true}, nil
}

type DoctorFinding struct {
	Check  string `json:"check"`
	Status string `json:"status"` // ok | fail | deferred
	Detail string `json:"detail"`
	// OnboardSection names the setup section that repairs the finding.
	// Empty means the repair is release mechanics and belongs to the front
	// doors (install.sh / redeploy.sh), never to an agent-facing verb;
	// the detail then carries that pointer.
	OnboardSection string                `json:"onboard_section"`
	SetupComplete  bool                  `json:"setup_complete,omitempty"`
	ProjectVision  []ProjectVisionStatus `json:"project_vision,omitempty"`
}

// frontDoorRepairDetail is the repair pointer for findings whose fix is
// release application rather than a setup section: image/helper
// envelope, supervision units. Agents relay it; the front doors act on it.
const frontDoorRepairDetail = "; repair: run install.sh (or redeploy.sh) from the release checkout"

type doctorFinding = DoctorFinding

// DoctorRuntimeReport is the path-free helper→host half of production doctor.
// It contains only spine-derived facts and the kernel capability result; no
// MC_HOME path or configuration byte crosses into or out of the helper.
type DoctorRuntimeReport struct {
	ProtocolVersion     int             `json:"protocol_version"`
	ReleaseBuildID      string          `json:"release_build_id"`
	ControlVersion      int             `json:"control_version"`
	SpineSchemaID       string          `json:"spine_schema_id"`
	ConfigSchemaVersion int             `json:"config_schema_version"`
	SpineUUID           string          `json:"spine_uuid,omitempty"`
	Findings            []DoctorFinding `json:"findings"`
}

type DoctorRuntimeRequest struct {
	ProtocolVersion     int    `json:"protocol_version"`
	ReleaseBuildID      string `json:"release_build_id"`
	ControlVersion      int    `json:"control_version"`
	SpineSchemaID       string `json:"spine_schema_id"`
	ConfigSchemaVersion int    `json:"config_schema_version"`
}

func DoctorRuntimeUnavailable(req DoctorRuntimeRequest, detail string) DoctorRuntimeReport {
	runes := []rune(detail)
	if len(runes) > 2048 {
		detail = string(runes[:2048]) + "…"
	}
	return DoctorRuntimeReport{Findings: []DoctorFinding{
		{Check: "spine", Status: "fail", Detail: "not checked: " + detail, OnboardSection: "home"},
		{Check: "projects", Status: "fail", Detail: "not checked: spine unavailable", OnboardSection: "project"},
		{Check: "surfaces", Status: "fail", Detail: "not checked: spine unavailable", OnboardSection: "surfaces"},
		{Check: "container-runtime", Status: "fail", Detail: detail + frontDoorRepairDetail, OnboardSection: ""},
	}, ProtocolVersion: req.ProtocolVersion, ReleaseBuildID: req.ReleaseBuildID,
		ControlVersion: req.ControlVersion, SpineSchemaID: req.SpineSchemaID,
		ConfigSchemaVersion: req.ConfigSchemaVersion}
}

// DoctorRuntime filters the existing total doctor surface down to facts whose
// authority lives in the runtime kernel. The helper has no MC_HOME bind, so
// host findings computed by Doctor are discarded and recomputed on Darwin.
func DoctorRuntime(spine string, req DoctorRuntimeRequest, releaseBuildID string, controlVersion, configSchemaVersion int) (DoctorRuntimeReport, error) {
	if req.ProtocolVersion != 1 || req.ReleaseBuildID != releaseBuildID ||
		req.ControlVersion != controlVersion || req.SpineSchemaID != substrate.CurrentSchemaID ||
		req.ConfigSchemaVersion != configSchemaVersion {
		return DoctorRuntimeReport{}, Domainf("private doctor-runtime build/schema identity mismatch: expected protocol=%d release=%s control=%d schema=%s config=%d; observed protocol=1 release=%s control=%d schema=%s config=%d",
			req.ProtocolVersion, req.ReleaseBuildID, req.ControlVersion, req.SpineSchemaID, req.ConfigSchemaVersion,
			releaseBuildID, controlVersion, substrate.CurrentSchemaID, configSchemaVersion)
	}
	res, err := doctor(nil, spine, false)
	if err != nil {
		return DoctorRuntimeReport{}, err
	}
	report := res.(map[string]any)
	all := report["findings"].([]doctorFinding)
	keep := map[string]bool{
		"spine": true, "projects": true, "surfaces": true, "container-runtime": true,
	}
	runtimeFindings := make([]DoctorFinding, 0, len(keep))
	spineOK := false
	for _, finding := range all {
		if keep[finding.Check] {
			runtimeFindings = append(runtimeFindings, finding)
			if finding.Check == "spine" && finding.Status == "ok" {
				spineOK = true
			}
		}
	}
	result := DoctorRuntimeReport{
		ProtocolVersion: req.ProtocolVersion, ReleaseBuildID: req.ReleaseBuildID,
		ControlVersion: req.ControlVersion, SpineSchemaID: req.SpineSchemaID,
		ConfigSchemaVersion: req.ConfigSchemaVersion, Findings: runtimeFindings,
	}
	if spineOK {
		inspection, err := inspectSpineReadOnly(spine)
		if err != nil {
			return DoctorRuntimeReport{}, err
		}
		result.SpineUUID = inspection.uuid
	}
	return result, nil
}

// DoctorHostFacts computes only Darwin-authoritative facts. Passing no spine
// is deliberate: it makes a host database open impossible, and the filtered
// result contains only MC_HOME/routing, runtime-auth, and the host-service
// findings.
func DoctorHostFacts() ([]DoctorFinding, error) {
	res, err := Doctor(nil, "")
	if err != nil {
		return nil, err
	}
	report := res.(map[string]any)
	all := report["findings"].([]doctorFinding)
	keep := map[string]bool{
		"mc-home": true, "routing": true, "runtime-auth": true, "supervision": true,
		"agentsview": true, "wiki": true,
	}
	findings := make([]DoctorFinding, 0, len(keep))
	for _, finding := range all {
		if keep[finding.Check] {
			findings = append(findings, finding)
		}
	}
	return findings, nil
}

// ComposeDoctor joins the two authority-local reports and performs the only
// cross-kernel comparison: the small deployment UUID mirror versus the
// helper-proved spine UUID. It preserves Doctor's public finding order.
func ComposeDoctor(host []DoctorFinding, runtimeReport DoctorRuntimeReport) (any, error) {
	wantSection := map[string]string{
		"mc-home": "home", "spine": "home", "projects": "project",
		"surfaces": "surfaces", "routing": "routing", "container-runtime": "",
		"runtime-auth": "runtime-auth", "supervision": "", "agentsview": "", "wiki": "",
	}
	if len(host) != 6 || len(runtimeReport.Findings) != 4 {
		return nil, Domainf("composed doctor halves have invalid finding counts")
	}
	byCheck := map[string]DoctorFinding{}
	for _, finding := range append(append([]DoctorFinding{}, host...), runtimeReport.Findings...) {
		section, known := wantSection[finding.Check]
		if !known || finding.OnboardSection != section ||
			(finding.Status != "ok" && finding.Status != "fail" && finding.Status != "deferred") {
			return nil, Domainf("invalid composed doctor finding %q", finding.Check)
		}
		if _, duplicate := byCheck[finding.Check]; duplicate {
			return nil, Domainf("duplicate composed doctor finding %q", finding.Check)
		}
		byCheck[finding.Check] = finding
	}
	for _, required := range []string{"mc-home", "spine", "projects", "surfaces", "routing", "container-runtime", "runtime-auth", "supervision", "agentsview", "wiki"} {
		if _, ok := byCheck[required]; !ok {
			return nil, Domainf("missing composed doctor finding %q", required)
		}
	}

	identity := DoctorFinding{Check: "deployment-identity", Status: "fail", OnboardSection: "home"}
	switch {
	case byCheck["mc-home"].Status != "ok":
		identity.Detail = "not checked: MC_HOME unresolved"
	case byCheck["spine"].Status != "ok" || runtimeReport.SpineUUID == "":
		identity.Detail = "not checked: spine unavailable"
	default:
		home, err := mcHomeDir()
		if err != nil {
			identity.Detail = err.Error()
			break
		}
		mirrored, exists, mirrorErr := readDeploymentMirror(home)
		switch {
		case mirrorErr != nil:
			identity.Detail = mirrorErr.Error()
		case !exists:
			identity.Detail = fmt.Sprintf("%s is missing; run the home repair section", filepath.Join(home, deploymentUUIDFilename))
		case mirrored != runtimeReport.SpineUUID:
			identity.Detail = fmt.Sprintf("MC_HOME identity %s does not match spine identity %s — restore the matching backup", mirrored, runtimeReport.SpineUUID)
		default:
			identity.Status = "ok"
			identity.Detail = fmt.Sprintf("MC_HOME and spine agree on %s", runtimeReport.SpineUUID)
		}
	}

	order := []string{"mc-home", "spine", "deployment-identity", "projects", "surfaces", "routing", "container-runtime", "runtime-auth", "supervision", "agentsview", "wiki"}
	findings := make([]DoctorFinding, 0, len(order))
	for _, check := range order {
		if check == "deployment-identity" {
			findings = append(findings, identity)
		} else {
			findings = append(findings, byCheck[check])
		}
	}
	ok := true
	for _, finding := range findings {
		if finding.Status == "fail" {
			ok = false
		}
	}
	return map[string]any{"ok": ok, "findings": findings}, nil
}

// Doctor validates the state it can read — MC_HOME shape, spine
// identity/schema + MC_HOME mirror, routing, configured surfaces, and
// project/sandbox references — and reports the probes it cannot run
// (container runtime, runtime auth, supervision, AgentsView, wiki address)
// as deferred so the finding surface is total from the start. Every finding
// names its repairing onboarding section. Doctor mutates nothing and
// never creates spine bytes; it always exits 0 with `ok` carrying the
// verdict, so a failing deployment still yields the full findings list.
func Doctor(id *RunIdentity, spine string) (any, error) {
	return doctor(id, spine, true)
}

func doctor(id *RunIdentity, spine string, inspectVision bool) (any, error) {
	if err := RequireHostScope(id, "mc doctor"); err != nil {
		return nil, err
	}
	findings := []doctorFinding{}
	add := func(check, status, detail, section string) {
		findings = append(findings, doctorFinding{
			Check: check, Status: status, Detail: detail, OnboardSection: section,
		})
	}

	home, err := mcHomeDir()
	switch {
	case err != nil:
		add("mc-home", "fail", err.Error(), "home")
	default:
		if st, statErr := os.Stat(home); statErr != nil || !st.IsDir() {
			add("mc-home", "fail", fmt.Sprintf("MC_HOME %q is not a directory: %v", home, statErr), "home")
		} else if allowErr := checkMountAllowlist(home); allowErr != nil {
			// Without this, doctor certified a deployment whose Worker was
			// refusing every tick with mount.allowlist_untrusted: the file is
			// consulted only on a dispatch that requests mounts, so a
			// deployment can look healthy right up to its first Worker.
			add("mc-home", "fail", allowErr.Error(), "home")
		} else {
			add("mc-home", "ok", home, "home")
		}
	}

	spineOK := false
	spineUUID := ""
	switch {
	case spine == "":
		add("spine", "fail", "MC_SPINE is not set", "home")
	default:
		if _, statErr := os.Stat(spine); statErr != nil {
			add("spine", "fail", fmt.Sprintf("no spine at %q; apply the release through redeploy.sh", spine), "home")
		} else if db, openErr := substrate.Open(spine); openErr != nil {
			add("spine", "fail", fmt.Sprintf("open spine: %v", openErr), "home")
		} else {
			func() {
				defer db.Close()
				var uuid string
				err := db.QueryRow(`SELECT deployment_uuid FROM meta WHERE id = 1`).Scan(&uuid)
				if err == nil {
					err = substrate.CheckSchema(db)
				}
				if err != nil {
					add("spine", "fail", fmt.Sprintf("database requires deployment repair: %v", err), "home")
				} else {
					add("spine", "ok", fmt.Sprintf("deployment %s schema %s", uuid, substrate.CurrentSchemaID), "home")
					spineOK = true
					spineUUID = uuid
				}
			}()
		}
	}

	switch {
	case err != nil:
		add("deployment-identity", "fail", "not checked: MC_HOME unresolved", "home")
	case !spineOK:
		add("deployment-identity", "fail", "not checked: spine unavailable", "home")
	default:
		mirrored, exists, mirrorErr := readDeploymentMirror(home)
		switch {
		case mirrorErr != nil:
			add("deployment-identity", "fail", mirrorErr.Error(), "home")
		case !exists:
			add("deployment-identity", "fail", fmt.Sprintf("%s is missing; run the home repair section", filepath.Join(home, deploymentUUIDFilename)), "home")
		case mirrored != spineUUID:
			add("deployment-identity", "fail", fmt.Sprintf("MC_HOME identity %s does not match spine identity %s — restore the matching backup", mirrored, spineUUID), "home")
		default:
			add("deployment-identity", "ok", fmt.Sprintf("MC_HOME and spine agree on %s", spineUUID), "home")
		}
	}

	if spineOK {
		db, openErr := substrate.Open(spine)
		if openErr != nil {
			add("projects", "fail", fmt.Sprintf("open spine: %v", openErr), "project")
		} else {
			func() {
				defer db.Close()
				// Name the broken rows, not just their count, so the
				// operator can act on the named section.
				var total int
				var brokenIDs []string
				err := db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&total)
				if err == nil {
					var rows *sql.Rows
					rows, err = db.Query(`
						SELECT w.id FROM projects w
						JOIN sandbox_profiles p ON p.id = w.sandbox_profile
						WHERE substr(p.workspace_root, 1, 1) <> '/'
						ORDER BY w.id`)
					if err == nil {
						for rows.Next() {
							var id string
							if err = rows.Scan(&id); err != nil {
								break
							}
							brokenIDs = append(brokenIDs, id)
						}
						if err == nil {
							err = rows.Err()
						}
						rows.Close()
					}
				}
				switch {
				case err != nil:
					add("projects", "fail", fmt.Sprintf("inspect projects: %v", err), "project")
				case total == 0:
					add("projects", "ok", "Add a Project in the dashboard to start project work.", "project")
				case len(brokenIDs) != 0:
					add("projects", "fail", fmt.Sprintf(
						"%d of %d Projects have a non-absolute workspace root: %s",
						len(brokenIDs), total, strings.Join(brokenIDs, ", ")), "project")
				default:
					add("projects", "ok", fmt.Sprintf("%d Projects; workspace setup is complete", total), "project")
				}
				if inspectVision && err == nil {
					listed, listErr := ProjectList(db)
					if listErr != nil {
						for i := range findings {
							if findings[i].Check == "projects" {
								markVisionUnavailable(&findings[i])
							}
						}
						return
					}
					statuses := InspectProjectListVision(listed)
					for i := range findings {
						if findings[i].Check == "projects" {
							applyProjectVisionFinding(&findings[i], statuses)
						}
					}
				}
			}()
		}
	} else {
		add("projects", "fail", "not checked: spine unavailable", "project")
	}

	if spineOK {
		db, openErr := substrate.Open(spine)
		if openErr != nil {
			add("surfaces", "fail", fmt.Sprintf("open spine: %v", openErr), "surfaces")
		} else {
			func() {
				defer db.Close()
				var blocked, health int
				queryErr := db.QueryRow(`
					SELECT COALESCE(SUM(class = 'blocked'), 0), COALESCE(SUM(class = 'health'), 0)
					FROM alert_routes`).Scan(&blocked, &health)
				switch {
				case queryErr != nil:
					add("surfaces", "fail", fmt.Sprintf("read alert routing: %v", queryErr), "surfaces")
				case blocked == 0 || health == 0:
					add("surfaces", "fail", "alert routing is not configured for both classes (blocked, health)", "surfaces")
				default:
					add("surfaces", "ok", fmt.Sprintf("alert routing configured (%d blocked, %d health destinations)", blocked, health), "surfaces")
				}
			}()
		}
	} else {
		add("surfaces", "fail", "not checked: spine unavailable", "surfaces")
	}

	var runtimeRegistry routing.Registry
	configOK := false
	if err != nil {
		add("config", "fail", "not checked: MC_HOME unresolved", "home")
	} else {
		path := filepath.Join(home, "config.toml")
		authority, configErr := loadRuntimeAuthority(home)
		if configErr != nil {
			add("config", "fail", fmt.Sprintf("invalid config.toml at %q: %v", path, configErr), "home")
		} else {
			runtimeRegistry = authority.registry
			configOK = true
			add("config", "ok", path, "home")
		}
	}

	if err != nil {
		add("routing", "fail", "not checked: MC_HOME unresolved", "routing")
	} else if !configOK {
		add("routing", "fail", "not checked: config.toml invalid", "routing")
	} else {
		path := filepath.Join(home, "routing.md")
		if routeErr := validateRoutingPath(path, runtimeRegistry); routeErr != nil {
			add("routing", "fail", routeErr.Error(), "routing")
		} else {
			add("routing", "ok", path, "routing")
		}
	}

	// The gateway finding is retired, not deferred: the deployment has no
	// egress gateway, so there is no probe to defer. Credential-projection
	// health is the runtime-auth row's job.
	{
		status, detail := containerRuntimeFinding(spine)
		if status == "fail" {
			detail += frontDoorRepairDetail
		}
		add("container-runtime", status, detail, "")
	}
	// Runtime-auth health is a live provider turn that spends on a real
	// subscription. The operator runs it as the live acceptance, one binding
	// at a time; the doctor spends nothing.
	add("runtime-auth", "deferred", "runtime-auth health is the live provider acceptance, run for one binding at a time", "runtime-auth")
	for _, check := range []string{"supervision", "agentsview", "wiki"} {
		add(check, "deferred", "the supervision check runs with the operator present", "")
	}

	ok := true
	for _, f := range findings {
		if f.Status == "fail" {
			ok = false
		}
	}
	return map[string]any{"ok": ok, "findings": findings}, nil
}
