package verbs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"mc/boundary"
	operatorconfig "mc/config"
	"mc/deployment"
	"mc/routing"
	"mc/substrate"
)

// mc onboard: the sectioned wizard.
// Named sections only, in a fixed order; every section is idempotent —
// re-running skips what is already healthy — and fail-closed: the first
// failing or input-starved section aborts the run with its repairing
// instruction, and completed sections' writes persist (resumable). The
// Docker and host-effect sections (runtime-auth, container, supervision)
// remain deferred doubles; supervision NEVER loads launchd during
// development.

var onboardSections = []string{
	"preflight", "home", "runtime-auth", "routing", "container",
	"project", "tunables", "surfaces", "supervision", "verify",
}

// setupOnboardSections is the agent-facing subset: the
// operator-owned setup questions, plus home's first-setup half. The
// install-time and release-application sections (preflight, container,
// supervision, verify), the whole wizard, and the smoke are reachable only
// through the private `__onboard` spelling the front doors drive; public
// usage text never lists that namespace.
var setupOnboardSections = []string{
	"home", "runtime-auth", "routing", "project", "tunables", "surfaces",
}

// defaultRoutingTable maps every role to the first enabled binding. One
// enabled binding is enough for a complete deployment.
func defaultRoutingTable(first string, registry routing.Registry) ([]byte, error) {
	table, err := routing.UniformTable(first, registry)
	if err != nil {
		return nil, Usagef("default routing: %v", err)
	}
	return []byte(table.Markdown()), nil
}

type OnboardArgs struct {
	Section string
	Spine   string
	Smoke   bool
	// SmokeApproveTask is an explicit operator decision for the real packet
	// emitted by the transient full-pipeline smoke. Zero means no pre-supplied
	// decision; an interactive caller is prompted only after the packet exists.
	SmokeApproveTask int64
	// SmokeReset retires the registered smoke generation instead of resuming
	// it. The smoke is resumable by design, which means a generation that can
	// never finish — one whose Project predates a schema its dispatch now
	// requires — is also resumed forever, and no rerun can escape it. This is
	// the only way out, and it only ever makes the old generation inert.
	SmokeReset bool
	// Dual-input answers: supplied as flags by the shepherding agent.
	Project                string
	WorkspaceRoot          string
	ProjectGitAuthorName   string
	ProjectGitAuthorEmail  string
	AcceptGeneratedSandbox bool
	// ProjectTitle is the display title of a Project the section registers.
	// The id is the title when it is empty.
	ProjectTitle string
	// ProjectPaused registers the Project paused.
	ProjectPaused      bool
	ProjectVisionSetup bool
	// ProjectVisionAnswer is the operator's answer to the VISION.md question
	// (`--project-vision create|later`), consulted only when the workspace
	// root has no VISION.md. Host-only: it never crosses into the helper.
	ProjectVisionAnswer string
	// ProjectVision is the host-resolved projects.vision value
	// carried through the helper boundary. Internal frame
	// data, never a caller-controlled flag.
	ProjectVision string
	// ProjectSandboxProfile is the canonical host-generated v3 receipt
	// carried through the helper boundary. It is internal frame data, never a
	// caller-controlled flag.
	ProjectSandboxProfile string
	// ProjectToolHomeDir is the per-Project synthetic HOME. The
	// project section carries it inside the profile receipt above; the smoke
	// section has no profile JSON, so it rides as its own bounded scalar.
	// Internal frame data on both paths, never caller-controlled.
	ProjectToolHomeDir string
	// RuntimeBindings lists the selected binding ids. Secret values never
	// enter argv.
	RuntimeBindings string

	AcquireRuntimeAuth bool
	// ConfirmLiveSpend confirms the provider spend of the live acceptance for
	// every enabled binding after the final verification.
	ConfirmLiveSpend     bool
	AcceptDefaultRouting bool
	RoutingFile          string
	ActivateSupervision  bool
	// ReconfigureSupervision opts INTO stopping loaded units to apply a changed
	// config.toml. Separate from ActivateSupervision on purpose: a routine
	// activation that finds drift must keep refusing, because restarting the
	// operator's running deployment is never a repair a health check may choose.
	ReconfigureSupervision bool
	RestoreLatest          bool
	AllowDatabaseReset     bool
	// ReleaseSource is the repo's runner/ tree supplied only by install.sh.
	// Home copies its fixed production manifest into MC_HOME; deployments never
	// execute runner source directly from the clone.
	ReleaseSource     string
	HostReleaseSource string
	// Tunables; zero = accept the schema defaults.
	TimeoutMinutes      int
	GraceMinutes        int
	HeartbeatIntervalS  int
	SpawnGraceS         int
	HardDeadlineMinutes int
	// HomieIdleTimeoutS is a lock tunable like the five above rather than a
	// host-only config value: the wake selector reads lock.homie_idle_timeout_s,
	// so config.toml only means something once the section projects it.
	HomieIdleTimeoutS          int
	WikiUpdateIntervalS        int
	ReviewWIPCap               int
	TunablesConfirmed          bool
	AcceptDefaultDashboardBind bool
	DashboardBind              string
	DashboardAuthTokenFile     string
	DiscordMode                string
	DiscordBotTokenFile        string
	DiscordGuildID             string
	DiscordChannelID           string
	DiscordOperatorIDs         string
	DiscordTestConfirmed       bool
	AcceptDefaultGitIdentity   bool
	GitAuthorName              string
	GitAuthorEmail             string
	// AllowReleaseMechanics permits private deployment changes, including a database reset.
	AllowReleaseMechanics bool
}

// GateSetupOnboardArgs is the agent-facing `mc onboard` fence: only the
// operator-owned setup sections, and none of the release/acceptance flags.
// Everything it refuses lives on the front doors — install.sh, redeploy.sh —
// and their private helper namespace.
func GateSetupOnboardArgs(a OnboardArgs) error {
	if a.AllowDatabaseReset {
		return Usagef("database reset authorization belongs to deployment administration")
	}
	if a.Smoke || a.SmokeReset || a.SmokeApproveTask != 0 {
		return Usagef("the full-pipeline smoke is release acceptance, not an agent verb; run install.sh --smoke")
	}
	if a.ReleaseSource != "" || a.HostReleaseSource != "" {
		return Usagef("release installation is not an agent verb; run install.sh or redeploy.sh from the release checkout")
	}
	if a.ActivateSupervision || a.ReconfigureSupervision {
		return Usagef("supervision activation is not an agent verb; run install.sh --activate or redeploy.sh")
	}
	if a.Section == "" {
		return Usagef("usage: mc onboard <section>; sections: %s",
			strings.Join(setupOnboardSections, "|"))
	}
	for _, s := range setupOnboardSections {
		if s == a.Section {
			return nil
		}
	}
	return Usagef("unknown onboarding section %q; sections: %s",
		a.Section, strings.Join(setupOnboardSections, "|"))
}

// OnboardSetup runs one agent-facing setup section (the dual-input questions
// and home's first-setup half). It never installs a release, migrates a
// schema, builds an image, or touches supervision — those are `__onboard`'s,
// reached only by the front doors.
func OnboardSetup(id *RunIdentity, a OnboardArgs) (any, error) {
	if err := RequireHostScope(id, "mc onboard"); err != nil {
		return nil, err
	}
	if err := GateSetupOnboardArgs(a); err != nil {
		return nil, err
	}
	a.AllowReleaseMechanics = false
	if a.Section != "home" {
		if err := requireOnboardIdentity(a.Spine); err != nil {
			return nil, err
		}
	}
	status, detail, err := onboardSection(a.Section, a)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "sections": []map[string]any{
		{"section": a.Section, "status": status, "detail": detail},
	}}, nil
}

func Onboard(id *RunIdentity, a OnboardArgs) (any, error) {
	if err := RequireHostScope(id, "mc __onboard"); err != nil {
		return nil, err
	}
	a.AllowReleaseMechanics = true
	sections := onboardSections
	if a.Section != "" {
		known := false
		for _, s := range onboardSections {
			if s == a.Section {
				known = true
				break
			}
		}
		if !known {
			return nil, Usagef("unknown onboarding section %q; sections: %s",
				a.Section, strings.Join(onboardSections, "|"))
		}
		sections = []string{a.Section}
	}
	if a.Smoke {
		return nil, Domainf("mc __onboard --smoke is the full-pipeline ride-through and needs the container tier")
	}
	results := make([]map[string]any, 0, len(sections))
	for _, section := range sections {
		if section != "preflight" && section != "home" {
			if err := requireOnboardIdentity(a.Spine); err != nil {
				return nil, err
			}
		}
		status, detail, err := onboardSection(section, a)
		if err != nil {
			return nil, err
		}
		results = append(results, map[string]any{
			"section": section, "status": status, "detail": detail,
		})
	}
	return map[string]any{"ok": true, "sections": results}, nil
}

func onboardSection(section string, a OnboardArgs) (string, string, error) {
	switch section {
	case "preflight":
		return onboardPreflight()
	case "home":
		status, detail, err := onboardHome(a.Spine, a.AllowReleaseMechanics)
		if err != nil {
			return status, detail, err
		}
		if a.ReleaseSource != "" {
			releaseStatus, err := installOnboardRunnerRelease(a.ReleaseSource)
			if err != nil {
				return "", "", err
			}
			if releaseStatus == "done" {
				status = "done"
			}
			detail += "; runner release " + releaseStatus
		}
		if a.HostReleaseSource != "" {
			hostStatus, err := installOnboardHostRelease(a.HostReleaseSource)
			if err != nil {
				return "", "", err
			}
			if hostStatus == "done" {
				status = "done"
			}
			detail += "; host release " + hostStatus
		}
		return status, detail, nil
	case "runtime-auth":
		return "deferred", "agent credentials are checked in the live acceptance, which sends a maximum of two provider requests for each enabled binding", nil
	case "routing":
		return onboardRouting(a)
	case "container":
		// This section covers the image build and warm-helper provisioning.
		return "deferred", "image build and warm helper provisioning run at install/onboarding", nil
	case "project":
		return onboardProject(a)
	case "tunables":
		return onboardTunables(a)
	case "surfaces":
		return onboardSurfaces(a)
	case "supervision":
		return "deferred", "the background services start once, and only while you are at the computer", nil
	case "verify":
		return onboardVerify(a.Spine)
	}
	return "", "", Usagef("unknown onboarding section %q", section)
}

func installOnboardRunnerRelease(source string) (string, error) {
	home, err := mcHomeDir()
	if err != nil {
		return "", err
	}
	status, err := deployment.InstallRunnerRelease(home, source)
	if err != nil {
		return "", Domainf("install production runner release: %v", err)
	}
	return status, nil
}

func installOnboardHostRelease(source string) (string, error) {
	home, err := mcHomeDir()
	if err != nil {
		return "", err
	}
	status, err := deployment.InstallHostRelease(home, source)
	if err != nil {
		return "", Domainf("install native host release: %v", err)
	}
	return status, nil
}

const deploymentUUIDFilename = "deployment.uuid"

type spineInspection struct {
	exists     bool
	bytes      int64
	tables     int
	hasMeta    bool
	uuid       string
	schemaID   string
	compatible bool
}

// inspectSpineReadOnly is the meta-first gate. In particular, it does
// not use substrate.Open: that connection deliberately applies WAL pragmas,
// which would mutate a foreign database before onboarding had accepted its
// identity. It reads through the shared read-only path
// (substrate.OpenReadOnly), which never changes journal mode.
func inspectSpineReadOnly(spine string) (spineInspection, error) {
	if spine == "" {
		return spineInspection{}, Usagef("mc onboard home requires MC_SPINE")
	}
	// substrate.OpenReadOnly carries the lock-domain guard, but the "no spine
	// yet" answer below must not vouch for a path the lock domain refuses, so
	// the guard also runs before the stat. The lock domain binds every open,
	// including read-only ones.
	if err := substrate.GuardLockDomain(spine); err != nil {
		return spineInspection{}, Domainf("%v", err)
	}
	st, err := os.Stat(spine)
	if os.IsNotExist(err) {
		return spineInspection{}, nil
	}
	if err != nil {
		return spineInspection{}, Domainf("inspect spine %q: %v", spine, err)
	}
	if st.IsDir() {
		return spineInspection{}, Domainf("spine path %q is a directory", spine)
	}
	inspection := spineInspection{exists: true, bytes: st.Size()}
	if st.Size() == 0 {
		return inspection, nil
	}
	db, err := substrate.OpenReadOnly(spine)
	if err != nil {
		return spineInspection{}, Domainf("inspect spine %q read-only: %v", spine, err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&inspection.tables); err != nil {
		return spineInspection{}, Domainf("inspect spine %q tables: %v", spine, err)
	}
	if inspection.tables == 0 {
		return inspection, nil
	}
	var metaTables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'`).Scan(&metaTables); err != nil {
		return spineInspection{}, Domainf("inspect spine %q meta table: %v", spine, err)
	}
	if metaTables == 0 {
		return inspection, nil
	}
	if err := db.QueryRow(`SELECT deployment_uuid FROM meta WHERE id = 1`).Scan(&inspection.uuid); err != nil {
		return spineInspection{}, Domainf("spine at %q has no singleton identity: %v", spine, err)
	}
	inspection.hasMeta = true
	schemaErr := substrate.CheckSchema(db)
	if schemaErr != nil && !errors.Is(schemaErr, substrate.ErrSchemaMismatch) {
		return spineInspection{}, schemaErr
	}
	inspection.compatible = schemaErr == nil
	if inspection.compatible {
		inspection.schemaID = substrate.CurrentSchemaID
	}

	return inspection, nil
}

func readDeploymentMirror(home string) (string, bool, error) {
	path := filepath.Join(home, deploymentUUIDFilename)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, Domainf("read deployment identity mirror %q: %v", path, err)
	}
	uuid := strings.TrimSpace(string(b))
	if uuid == "" {
		return "", false, Domainf("deployment identity mirror %q is empty; verify the deployment identity", path)
	}
	return uuid, true, nil
}

func writeDeploymentMirror(home, uuid string) error {
	path := filepath.Join(home, deploymentUUIDFilename)
	tmp, err := os.CreateTemp(home, ".deployment.uuid.tmp-*")
	if err != nil {
		return Domainf("create deployment identity mirror: %v", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(uuid + "\n"); err != nil {
		tmp.Close()
		return Domainf("write deployment identity mirror: %v", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Domainf("sync deployment identity mirror: %v", err)
	}
	if err := tmp.Close(); err != nil {
		return Domainf("close deployment identity mirror: %v", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return Domainf("publish deployment identity mirror: %v", err)
	}
	return nil
}

// validateInspection requires a recognized deployment identity before any database reset.
func validateInspection(spine string, inspection spineInspection) error {
	if inspection.tables == 0 || !inspection.hasMeta {
		return Domainf("spine at %q has no meta identity; verify the deployment database", spine)
	}
	if inspection.uuid == "" {
		return Domainf("spine at %q has an empty deployment identity", spine)
	}
	return nil
}

// resetDevelopmentSpine removes only the verified database and its SQLite files.
// The private deployment caller must stop every database user first.
func resetDevelopmentSpine(spine string) error {
	for _, path := range []string{spine, spine + "-wal", spine + "-shm"} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return Domainf("inspect database reset target: %v", err)
		}
		if !info.Mode().IsRegular() {
			return Domainf("database reset target %q must be a regular file", path)
		}
	}
	for _, path := range []string{spine + "-wal", spine + "-shm", spine} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return Domainf("remove database reset target: %v", err)
		}
	}
	return nil
}

func requireOnboardIdentity(spine string) error {
	home, err := mcHomeDir()
	if err != nil {
		return err
	}
	mirrored, mirrorExists, err := readDeploymentMirror(home)
	if err != nil {
		return err
	}
	if !mirrorExists {
		return Domainf("deployment identity is not provisioned; run mc onboard home first")
	}
	inspection, err := inspectSpineReadOnly(spine)
	if err != nil {
		return err
	}
	if !inspection.exists || inspection.tables == 0 {
		return Domainf("spine lost for deployment %s at %q; apply the release through redeploy.sh", mirrored, spine)
	}
	if err := validateInspection(spine, inspection); err != nil {
		return err
	}
	if mirrored != inspection.uuid {
		return Domainf("deployment identity mismatch: MC_HOME has %s but spine has %s — restore the matching backup", mirrored, inspection.uuid)
	}
	return nil
}

func scaffoldOnboardHome(home string) (bool, error) {
	changed := false
	// No `workflows` directory: nothing writes one.
	for _, dir := range []string{"", "backups", "sessions", "outputs", "attachments"} {
		if st, err := os.Stat(filepath.Join(home, dir)); os.IsNotExist(err) {
			changed = true
		} else if err != nil {
			return false, Domainf("inspect MC_HOME scaffold: %v", err)
		} else if !st.IsDir() {
			return false, Domainf("MC_HOME scaffold path %q is not a directory", filepath.Join(home, dir))
		}
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			return false, Domainf("scaffold MC_HOME: %v", err)
		}
	}
	// The operator skill store: the scaffold owns `skills/` and
	// `skills/homie/`; Project registration owns the rest.
	storeChanged, err := EnsureSkillStore(home)
	if err != nil {
		return false, err
	}
	changed = changed || storeChanged
	configChanged, err := operatorconfig.Ensure(filepath.Join(home, "config.toml"))
	if err != nil {
		return false, Domainf("scaffold config.toml: %v", err)
	}
	changed = changed || configChanged
	allowlistChanged, err := EnsureMountAllowlist(home)
	if err != nil {
		return false, err
	}
	changed = changed || allowlistChanged
	return changed, nil
}

// EnsureMountAllowlist creates the deny-all mount policy the first time
// and never touches it again. The file is operator policy the moment it
// exists, so re-running a routine section must not revert a
// grant; O_EXCL is what makes "create if absent" a single syscall rather than
// a stat/write race two concurrent onboarding runs could lose.
//
// Zero `[[allow]]` entries grants nothing, which is why onboarding can author
// it before any Project exists: a Project's own roots reach a container
// as typed roots and are authorized against its jurisdiction, never against an
// allowlist entry. Entries appear only when the operator mounts something
// outside a Project.
//
// It is exported so that test fixtures which stand up an MC_HOME without
// running the wizard call THIS, not a hand-copied `version = 1`. That is the
// property the gap needed: a fixture that writes the file itself is a fixture
// agreeing with its own declaration, and it is precisely why a deployment
// shipped without the file while every suite stayed green.
func EnsureMountAllowlist(home string) (bool, error) {
	path := MountAllowlistPath(home)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, Domainf("scaffold mount-allowlist: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString("version = 1\n"); err != nil {
		return false, Domainf("write mount-allowlist: %v", err)
	}
	return true, nil
}

// EnsureProjectArtifactGrant authorizes one Project's `.mc-artifacts`
// root in the mount allowlist, idempotently, and reports whether it appended.
//
// This is the one place onboarding EXTENDS the allowlist rather than merely
// scaffolding it, and the narrowness is the justification. An artifact source
// reaches a container as an ALLOWLISTED mount, not as a typed root, so a profile that names an artifact root the
// allowlist does not carry refuses at dispatch. Section 6 is the operator
// registering the Project and accepting its generated deny-by-default
// profile, which is exactly the authorization this entry records.
//
// Append-only and match-by-path: an operator who has widened, retargeted, or
// deliberately removed the entry keeps their edit. `EnsureMountAllowlist`'s
// rule — the file is operator policy the moment it exists — still holds; this
// adds a grant the operator just asked for and never revokes or rewrites one.
func EnsureProjectArtifactGrant(home, project, root string) (bool, error) {
	return ensureProjectMountGrant(home, project, root)
}

// RemoveProjectMountGrant retracts one grant this package added, by exact
// (path, target), and reports whether it removed anything. Absence is success.
//
// It exists because a grant OUTLIVES the directory it authorizes, and that is
// not a cosmetic leftover: `boundary.ResolveAllowlist` resolves every allow
// root, since its uniqueness law compares filesystem identity rather than
// strings. ONE root that no longer exists therefore refuses every mounted
// dispatch in the deployment — every Project, every role, permanently, with
// a refusal naming a path nobody is dispatching. A deleted workspace that
// left its grant behind stopped a live board this way.
//
// So whoever deletes a workspace must retract its grant in the same breath.
// This is the retraction half of ensureProjectMountGrant and is deliberately
// as narrow: it matches BOTH path and target, so it can only ever remove the
// exact row that function wrote, never an operator's own edit of it.
//
// The file is rewritten from its parsed entries, which drops comments — an
// accepted cost, taken only on the removal path, because the alternative
// (line-surgery on operator-owned TOML) fails in more ways than it preserves.
// The result is proved to parse and to have lost exactly the one entry before
// it replaces the file.
func RemoveProjectMountGrant(home, target, root string) (bool, error) {
	path := MountAllowlistPath(home)
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, Domainf("read mount-allowlist: %v", err)
	}
	allowlist, err := boundary.ParseMountAllowlist(body)
	if err != nil {
		return false, Domainf("mount-allowlist must parse before a grant can be retracted: %v", err)
	}
	kept := make([]boundary.AllowEntry, 0, len(allowlist.Entries()))
	removed := 0
	for _, entry := range allowlist.Entries() {
		if entry.Path == root && entry.Target == target {
			removed++
			continue
		}
		kept = append(kept, entry)
	}
	if removed == 0 {
		return false, nil
	}
	var out strings.Builder
	out.WriteString("version = 1\n")
	for _, entry := range kept {
		fmt.Fprintf(&out, "\n[[allow]]\npath = %q\ntarget = %q\naccess = %q\n",
			entry.Path, entry.Target, string(entry.Maximum))
	}
	rewritten, err := boundary.ParseMountAllowlist([]byte(out.String()))
	if err != nil {
		return false, Domainf("retracting the grant would break the mount-allowlist: %v", err)
	}
	if len(rewritten.Entries()) != len(kept) {
		return false, Domainf("retracting the grant would change %d other entries; refusing",
			len(kept)-len(rewritten.Entries()))
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o600); err != nil {
		return false, Domainf("rewrite mount-allowlist: %v", err)
	}
	return true, nil
}

// ensureProjectMountGrant is the general form: a Project may need more
// than one grant, and they differ in target, not in mechanism. Every grant this
// package writes is `rw` — the artifact plane is the sole remaining family.
//
// The target is a parameter rather than the Project id because the shadow
// check below refuses a second entry under a target already bound, which is
// correct (two sources answering to one target is exactly the ambiguity the
// check exists to stop) and is why a second grant for the same Project
// would need a distinct, non-overlapping target of its own.
func ensureProjectMountGrant(home, target, root string) (bool, error) {
	if err := boundary.ValidateTarget(target); err != nil {
		return false, Domainf("mount target %q is not usable: %v", target, err)
	}
	project := target
	path := MountAllowlistPath(home)
	body, err := os.ReadFile(path)
	if err != nil {
		return false, Domainf("read mount-allowlist: %v", err)
	}
	// Parse before appending: an unparseable file must not be grown, and the
	// parsed entries are the only trustworthy answer to "is it already there".
	allowlist, err := boundary.ParseMountAllowlist(body)
	if err != nil {
		return false, Domainf("mount-allowlist must parse before it can be extended: %v", err)
	}
	for _, entry := range allowlist.Entries() {
		if entry.Path == root {
			return false, nil
		}
		if entry.Target == project {
			return false, Domainf("mount-allowlist already binds target %q to %q; refusing to shadow it with %q",
				project, entry.Path, root)
		}
	}
	var out strings.Builder
	if len(body) > 0 && !strings.HasSuffix(string(body), "\n") {
		out.WriteString("\n")
	}
	fmt.Fprintf(&out, "\n[[allow]]\npath = %q\ntarget = %q\naccess = %q\n", root, project, string(boundary.AccessRW))
	// Prove the result parses before it is the file every dispatch reads. A
	// mount-allowlist that stops parsing refuses every mount in the deployment,
	// so writing one and discovering it at the next tick is the wrong order.
	if _, err := boundary.ParseMountAllowlist([]byte(string(body) + out.String())); err != nil {
		return false, Domainf("appending the Project artifact grant would break the mount-allowlist: %v", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return false, Domainf("open mount-allowlist for append: %v", err)
	}
	if _, err := file.WriteString(out.String()); err != nil {
		file.Close()
		return false, Domainf("append mount-allowlist: %v", err)
	}
	if err := file.Close(); err != nil {
		return false, Domainf("append mount-allowlist: %v", err)
	}
	return true, nil
}

// onboardPreflight writes nothing (nothing is written until preflight
// passes): MC_HOME resolves outside every git working tree unless the
// concrete root is ignored there, and git is present. Preflight does not
// probe container-runtime reachability.
func onboardPreflight() (string, string, error) {
	home, err := mcHomeDir()
	if err != nil {
		return "", "", err
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", "", Domainf("git is required and not on PATH")
	}
	resolved, err := deployment.CanonicalHome(home)
	if err != nil {
		return "", "", Domainf("%v", err)
	}
	for dir := resolved; ; {
		_, gitErr := os.Stat(filepath.Join(dir, ".git"))
		if gitErr == nil {
			rel, relErr := filepath.Rel(dir, resolved)
			if relErr != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return "", "", Domainf("MC_HOME %q is inside a git working tree (%q) and is not an ignored child path", home, dir)
			}
			ignored := exec.Command("git", "-C", dir, "check-ignore", "-q", "--", rel).Run() == nil
			if !ignored {
				// A directory-only pattern (for example `.mc/`) needs the
				// trailing slash while the not-yet-created root has no stat
				// information for git to infer that it will be a directory.
				ignored = exec.Command("git", "-C", dir, "check-ignore", "-q", "--", filepath.ToSlash(rel)+"/").Run() == nil
			}
			if !ignored {
				return "", "", Domainf("MC_HOME %q resolves inside git working tree %q but is not covered by its .gitignore", home, dir)
			}
		} else if !os.IsNotExist(gitErr) {
			return "", "", Domainf("inspect git working-tree marker under %q: %v", dir, gitErr)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "ok", fmt.Sprintf("%s/%s; MC_HOME %s (resolved %s); the agent container check runs later in this setup", runtime.GOOS, runtime.GOARCH, home, resolved), nil
}

// onboardHome preserves a compatible database and resets incompatible development state through the private deployment path.
func onboardHome(spine string, allowReset bool) (string, string, error) {
	req, err := PrepareOnboardHome(allowReset)
	if err != nil {
		return "", "", err
	}
	result, err := OnboardSpine(spine, req)
	if err != nil {
		return "", "", err
	}
	return FinalizeOnboardHome(result)
}

// onboardRouting publishes the explicitly accepted default or an explicitly
// supplied remap. The candidate is validated before writing, so an invalid
// table can never come out of onboarding; only --routing-file may replace an
// existing table.
func onboardRouting(a OnboardArgs) (string, string, error) {
	home, err := mcHomeDir()
	if err != nil {
		return "", "", err
	}
	if a.AcceptDefaultRouting == (a.RoutingFile != "") {
		return "", "", Usagef("mc onboard routing requires exactly one of --accept-default-routing or --routing-file <path>")
	}
	registry, first, err := onboardRoutingRegistry(a)
	if err != nil {
		return "", "", err
	}
	var candidate []byte
	if a.AcceptDefaultRouting {
		candidate, err = defaultRoutingTable(first, registry)
	} else {
		candidate, err = readOnboardRoutingFile(a.RoutingFile)
	}
	if err != nil {
		return "", "", err
	}
	if _, err := routing.Parse(candidate, registry); err != nil {
		return "", "", Domainf("invalid requested routing: %v", err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", "", Domainf("scaffold MC_HOME: %v", err)
	}
	path := filepath.Join(home, "routing.md")
	if _, err := os.Lstat(path); err == nil {
		existing, err := readRoutingPath(path)
		if err != nil {
			return "", "", err
		}
		if a.AcceptDefaultRouting {
			if _, err := routing.Parse(existing, registry); err != nil {
				return "", "", Domainf("invalid routing.md at %q: %v — pass the intended table with --routing-file to repair it", path, err)
			}
		}
		if string(existing) == string(candidate) {
			return finishOnboardRouting("ok", path, a)
		}
		if a.AcceptDefaultRouting {
			return "", "", Domainf("routing.md at %q differs from the accepted default; pass the intended table with --routing-file to remap explicitly", path)
		}
	} else if !os.IsNotExist(err) {
		return "", "", Domainf("inspect routing.md at %q: %v", path, err)
	}
	tmp, err := os.CreateTemp(home, ".routing.md.tmp-*")
	if err != nil {
		return "", "", Domainf("create routing.md temp file: %v", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", "", Domainf("protect routing.md temp file: %v", err)
	}
	if _, err := tmp.Write(candidate); err != nil {
		tmp.Close()
		return "", "", Domainf("write routing.md temp file: %v", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", "", Domainf("sync routing.md temp file: %v", err)
	}
	if err := tmp.Close(); err != nil {
		return "", "", Domainf("close routing.md temp file: %v", err)
	}
	// Hard-link publication is atomic and refuses to replace a concurrent
	// creator or a symlink; unlike a direct write, a crash cannot publish a
	// partial routing table.
	if _, err := os.Lstat(path); err == nil {
		existing, readErr := readRoutingPath(path)
		if readErr != nil {
			return "", "", readErr
		}
		if string(existing) == string(candidate) {
			return finishOnboardRouting("ok", path, a)
		}
		if a.AcceptDefaultRouting {
			return "", "", Domainf("routing.md at %q changed before the accepted default could be published; retry with the intended table", path)
		}
		if err := os.Rename(tmpPath, path); err != nil {
			return "", "", Domainf("replace routing.md: %v", err)
		}
		if err := validateRoutingPath(path, registry); err != nil {
			return "", "", err
		}
		return finishOnboardRouting("done", path, a)
	} else if !os.IsNotExist(err) {
		return "", "", Domainf("reinspect routing.md at %q: %v", path, err)
	}
	if err := os.Link(tmpPath, path); err != nil {
		if os.IsExist(err) {
			existing, readErr := readRoutingPath(path)
			if readErr != nil {
				return "", "", readErr
			}
			if string(existing) != string(candidate) {
				return "", "", Domainf("routing.md at %q was concurrently published with different bytes; retry the explicit routing choice", path)
			}
			return finishOnboardRouting("ok", path, a)
		}
		return "", "", Domainf("publish routing.md: %v", err)
	}
	return finishOnboardRouting("done", path, a)
}

func finishOnboardRouting(status, detail string, a OnboardArgs) (string, string, error) {
	changed, err := RecordOnboardRuntimeBindings(a.RuntimeBindings)
	if err != nil {
		return "", "", err
	}
	if changed {
		status = "done"
	}
	return status, detail + "; config.toml runtime enablement verified", nil
}

// onboardRoutingRegistry resolves the operator's binding selection against
// the configured catalog. It returns the registry of the selected bindings
// and the first selected binding, which the default routing maps every role
// to.
func onboardRoutingRegistry(a OnboardArgs) (routing.Registry, string, error) {
	if a.RuntimeBindings == "" {
		return nil, "", Usagef("mc onboard routing requires --runtime-bindings <csv> so disabled subscriptions cannot enter routing.md")
	}
	cfg, err := loadOnboardSelectionConfig()
	if err != nil {
		return nil, "", err
	}
	configured := onboardBindingCatalog(cfg)
	bindings, err := selectedOnboardBindingIDs(a.RuntimeBindings, configured)
	if err != nil {
		return nil, "", err
	}
	selected := routing.Registry{}
	prospectiveBindings := []string{}
	for _, binding := range bindings {
		selected[binding] = configured[binding].Harness
		if binding != "fake" {
			prospectiveBindings = append(prospectiveBindings, binding)
		}
	}
	first := bindings[0]
	if len(prospectiveBindings) != 0 {
		prospective := cfg
		prospective.Runtimes.EnabledBindings = prospectiveBindings
		if err := prospective.Validate(); err != nil {
			return nil, "", Usagef("selected runtime bindings are invalid: %v", err)
		}
	}
	return selected, first, nil
}

func readOnboardRoutingFile(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, Usagef("mc onboard routing --routing-file must be absolute, got %q", path)
	}
	st, err := os.Lstat(path)
	if err != nil {
		return nil, Domainf("inspect routing input %q: %v", path, err)
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
		return nil, Domainf("routing input %q must be a regular non-symlink file", path)
	}
	if st.Size() > 1<<20 {
		return nil, Domainf("routing input %q exceeds the 1 MiB bound", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, Domainf("read routing input %q: %v", path, err)
	}
	return data, nil
}

func validateRoutingPath(path string, registry routing.Registry) error {
	data, err := readRoutingPath(path)
	if err != nil {
		return err
	}
	if _, err := routing.Parse(data, registry); err != nil {
		return Domainf("invalid routing.md at %q: %v — repair it or remove it to accept the default", path, err)
	}
	return nil
}

func readRoutingPath(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, Domainf("inspect routing.md at %q: %v", path, err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, Domainf("routing.md at %q is a symlink; operator config must be a regular file under MC_HOME", path)
	}
	if !before.Mode().IsRegular() {
		return nil, Domainf("routing.md at %q is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, Domainf("read routing.md at %q: %v", path, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, Domainf("stat open routing.md at %q: %v", path, err)
	}
	if !os.SameFile(before, opened) {
		return nil, Domainf("routing.md at %q changed during validation; retry", path)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, Domainf("read routing.md at %q: %v", path, err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, after) {
		return nil, Domainf("routing.md at %q changed during validation; retry", path)
	}
	return data, nil
}

// prepareOnboardProject checks the workspace and resolves its vision on the host.
func prepareOnboardProject(a OnboardArgs) (OnboardArgs, error) {
	supplied := a.Project != "" || a.WorkspaceRoot != "" ||
		a.ProjectGitAuthorName != "" ||
		a.ProjectGitAuthorEmail != "" || a.AcceptGeneratedSandbox ||
		a.ProjectVisionAnswer != "" || a.ProjectTitle != "" || a.ProjectPaused || a.ProjectVisionSetup
	if supplied && (a.Project == "" || a.WorkspaceRoot == "") {
		return OnboardArgs{}, Usagef("mc onboard project requires --project and --workspace-root together")
	}
	if supplied && !a.AcceptGeneratedSandbox {
		return OnboardArgs{}, Usagef("mc onboard project requires --accept-generated-sandbox after reviewing the generated deny-by-default profile")
	}
	if supplied && !validProfileGitIdentity(onboardGitIdentityOverride(a)) {
		return OnboardArgs{}, Usagef("mc onboard project Git author name/email must be supplied together and be safe")
	}
	if supplied && (!validStructuralText(a.Project, maxPrivateScalarBytes) ||
		!validStructuralText(a.WorkspaceRoot, maxPrivateScalarBytes)) {
		return OnboardArgs{}, Usagef("mc onboard Project inputs must be valid UTF-8 without controls and at most 4096 bytes")
	}
	if supplied && (a.Project == "." || a.Project == ".." ||
		strings.ContainsAny(a.Project, `/\`)) {
		return OnboardArgs{}, Usagef("mc onboard Project id must be one path-safe segment")
	}
	if supplied {
		a.ProjectTitle = strings.TrimSpace(a.ProjectTitle)
		if a.ProjectTitle == "" {
			a.ProjectTitle = a.Project
		}
		if !validStructuralText(a.ProjectTitle, MaxProjectTitleBytes) {
			return OnboardArgs{}, Usagef("mc onboard project --title must be one line of at most %d bytes", MaxProjectTitleBytes)
		}
	}
	if supplied {
		if !filepath.IsAbs(a.WorkspaceRoot) {
			return OnboardArgs{}, Usagef("mc onboard project --workspace-root must be absolute, got %q", a.WorkspaceRoot)
		}
		identity, err := boundary.ResolveSource(a.WorkspaceRoot)
		if err != nil {
			return OnboardArgs{}, Domainf("inspect workspace root %q: %v", a.WorkspaceRoot, err)
		}
		if !identity.IsDir {
			return OnboardArgs{}, Usagef("mc onboard project --workspace-root must name a directory, got %q", a.WorkspaceRoot)
		}
		if (boundary.BlockPolicy{}).Rejects(identity.RawClean, identity.Canonical) {
			return OnboardArgs{}, Domainf("workspace root %q has a blocked address component", a.WorkspaceRoot)
		}
		a.WorkspaceRoot = filepath.Clean(identity.Canonical)
		_, body, err := canonicalOnboardSandboxProfile(a)
		if err != nil {
			return OnboardArgs{}, err
		}
		a.ProjectSandboxProfile = body
		// The host checks the workspace file before the helper stores the result.
		a.ProjectVision, err = resolveProjectVision(a.Project, a.WorkspaceRoot, a.ProjectVisionAnswer)
		if err != nil {
			return OnboardArgs{}, err
		}
	}
	return a, nil
}

type OnboardWorkspaceRoot struct {
	ID   string `json:"id"`
	Root string `json:"root"`
}

// Version 3 drops readonly_mounts with the read-only Project.
// canonicalOnboardSandboxProfile decodes with DisallowUnknownFields,
// so a stored v2 body carrying the field refuses cleanly; re-running the
// section regenerates a fresh v3 body from the same inputs.
const onboardSandboxProfileVersion = 3

type onboardSandboxProfile struct {
	Version             int                                    `json:"version"`
	ID                  string                                 `json:"id"`
	WorkspaceRoot       string                                 `json:"workspace_root"`
	ArtifactRoots       []string                               `json:"artifact_roots"`
	DeniedPaths         []string                               `json:"denied_paths"`
	ToolHomeDir         string                                 `json:"tool_home_dir"`
	RuntimeControlDir   string                                 `json:"runtime_control_dir"`
	HarnessEnvPolicy    string                                 `json:"harness_env_policy"`
	ToolEnvPolicy       string                                 `json:"tool_env_policy"`
	ToolAllowlist       []string                               `json:"tool_allowlist"`
	GitIdentityOverride *substrate.DispatchGitIdentityOverride `json:"git_identity_override"`
	RuntimeAuthDelivery string                                 `json:"runtime_auth_delivery"`
}

func onboardGitIdentityOverride(a OnboardArgs) *substrate.DispatchGitIdentityOverride {
	if a.ProjectGitAuthorName == "" && a.ProjectGitAuthorEmail == "" {
		return nil
	}
	return &substrate.DispatchGitIdentityOverride{
		AuthorName: a.ProjectGitAuthorName, AuthorEmail: a.ProjectGitAuthorEmail,
	}
}

func sameGitIdentityOverride(a, b *substrate.DispatchGitIdentityOverride) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.AuthorName == b.AuthorName && a.AuthorEmail == b.AuthorEmail
}

func gitIdentityOverrideFromSQL(name, email sql.NullString) (*substrate.DispatchGitIdentityOverride, error) {
	if name.Valid != email.Valid {
		return nil, Domainf("sandbox profile carries an incomplete Git identity override")
	}
	if !name.Valid {
		return nil, nil
	}
	identity := &substrate.DispatchGitIdentityOverride{
		AuthorName: name.String, AuthorEmail: email.String,
	}
	if !validProfileGitIdentity(identity) {
		return nil, Domainf("sandbox profile carries an invalid Git identity override")
	}
	return identity, nil
}

func gitIdentityOverrideSQL(identity *substrate.DispatchGitIdentityOverride) (any, any) {
	if identity == nil {
		return nil, nil
	}
	return identity.AuthorName, identity.AuthorEmail
}

func generatedOnboardSandboxProfile(a OnboardArgs) (onboardSandboxProfile, error) {
	home, err := mcHomeDir()
	if err != nil {
		return onboardSandboxProfile{}, err
	}
	home, err = deployment.CanonicalHome(home)
	if err != nil {
		return onboardSandboxProfile{}, Domainf("resolve canonical MC_HOME for sandbox profile: %v", err)
	}
	operatorHome, err := realOperatorHome(os.Getuid())
	if err != nil {
		return onboardSandboxProfile{}, Domainf("resolve operator home for sandbox profile: %v", err)
	}
	denied := make([]string, 0, 13)
	for _, rel := range []string{
		".ssh", ".aws", ".azure", ".config", ".docker", ".gnupg", ".kube",
		".codex", ".claude", "Library/Keychains", ".netrc", ".npmrc",
		".pypirc", ".git-credentials",
	} {
		denied = append(denied, filepath.Join(operatorHome, filepath.FromSlash(rel)))
	}
	artifacts := []string{ProjectArtifactRoot(a.WorkspaceRoot)}
	return onboardSandboxProfile{
		Version: onboardSandboxProfileVersion,
		ID:      "project-" + a.Project, WorkspaceRoot: a.WorkspaceRoot,
		ArtifactRoots: artifacts, DeniedPaths: denied,
		ToolHomeDir:       ProjectToolHome(home, a.Project),
		RuntimeControlDir: "", HarnessEnvPolicy: "{}", ToolEnvPolicy: "{}",
		ToolAllowlist: []string{}, GitIdentityOverride: onboardGitIdentityOverride(a),
		RuntimeAuthDelivery: "projection",
	}, nil
}

func canonicalOnboardSandboxProfile(a OnboardArgs) (onboardSandboxProfile, string, error) {
	var profile onboardSandboxProfile
	if a.ProjectSandboxProfile == "" {
		generated, err := generatedOnboardSandboxProfile(a)
		if err != nil {
			return profile, "", err
		}
		body, err := json.Marshal(generated)
		return generated, string(body), err
	}
	dec := json.NewDecoder(strings.NewReader(a.ProjectSandboxProfile))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&profile); err != nil {
		return profile, "", Domainf("private onboard sandbox profile is invalid")
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return profile, "", Domainf("private onboard sandbox profile carries trailing data")
	}
	body, err := json.Marshal(profile)
	if err != nil || string(body) != a.ProjectSandboxProfile {
		return profile, "", Domainf("private onboard sandbox profile is not canonical")
	}
	if profile.Version != onboardSandboxProfileVersion ||
		profile.ID != "project-"+a.Project ||
		profile.WorkspaceRoot != a.WorkspaceRoot ||
		profile.ArtifactRoots == nil ||
		profile.DeniedPaths == nil || profile.ToolAllowlist == nil ||
		profile.HarnessEnvPolicy != "{}" || profile.ToolEnvPolicy != "{}" ||
		profile.RuntimeControlDir != "" || profile.RuntimeAuthDelivery != "projection" ||
		!sameGitIdentityOverride(profile.GitIdentityOverride, onboardGitIdentityOverride(a)) ||
		!validProfileGitIdentity(profile.GitIdentityOverride) ||
		!filepathIsCleanAbsolute(profile.ToolHomeDir) || len(profile.DeniedPaths) == 0 {
		return profile, "", Domainf("private onboard sandbox profile does not match the generated v3 contract")
	}
	// The artifact plane is exactly the one derived root — a helper-supplied
	// profile may not add, drop, or respell it, because the allowlist grant is
	// written against the derivation and a mismatch would authorize a root no
	// profile names or vice versa.
	if len(profile.ArtifactRoots) != 1 ||
		profile.ArtifactRoots[0] != ProjectArtifactRoot(a.WorkspaceRoot) ||
		len(profile.ToolAllowlist) != 0 {
		return profile, "", Domainf("private onboard sandbox profile changed the requested Project mounts")
	}
	for _, denied := range profile.DeniedPaths {
		if !filepathIsCleanAbsolute(denied) {
			return profile, "", Domainf("private onboard sandbox profile carries an invalid denied path")
		}
	}
	return profile, string(body), nil
}

func onboardProjectProfile(a OnboardArgs) (onboardSandboxProfile, error) {
	profile, _, err := canonicalOnboardSandboxProfile(a)
	return profile, err
}

func onboardProfilePathJSON(profile onboardSandboxProfile) (string, string, string, error) {
	artifactBody, err := json.Marshal(profile.ArtifactRoots)
	if err != nil {
		return "", "", "", err
	}
	deniedBody, err := json.Marshal(profile.DeniedPaths)
	if err != nil {
		return "", "", "", err
	}
	allowlistBody, err := json.Marshal(profile.ToolAllowlist)
	if err != nil {
		return "", "", "", err
	}
	return string(artifactBody), string(deniedBody), string(allowlistBody), nil
}

// registerOnboardProject writes the reviewed generated profile and the
// Project row bound to it. A Project is a Git repository: the row is born
// with its profile, and the host prepared the repository before this write.
func registerOnboardProject(ctx context.Context, q Q, a OnboardArgs) (string, error) {
	profile, err := onboardProjectProfile(a)
	if err != nil {
		return "", err
	}
	if err := insertAndVerifyOnboardSandboxProfile(ctx, q, a, profile); err != nil {
		return "", err
	}
	vision := a.ProjectVision
	if vision == "" {
		vision = ProjectVisionUnknown
	}
	status := "active"
	setupStatus := "ready"
	if a.ProjectPaused || a.ProjectVisionSetup {
		status = "paused"
	}
	if a.ProjectVisionSetup {
		setupStatus = "vision"
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO projects (id, title, sandbox_profile, vision, status, setup_status)
		VALUES (?, ?, ?, ?, ?, ?)`,
		a.Project, a.ProjectTitle, profile.ID, vision, status, setupStatus); err != nil {
		return "", err
	}
	if err := substrate.ValidateDispatchMountProjection(ctx, q); err != nil {
		return "", err
	}
	profileDigest := sha256.Sum256([]byte(a.ProjectSandboxProfile))
	detail := fmt.Sprintf("Project %s at %s; sandbox=v%d:%x; %s",
		a.Project, a.WorkspaceRoot, onboardSandboxProfileVersion, profileDigest,
		projectSecretsAdvisory)
	return appendProjectVisionDetail(detail, a.WorkspaceRoot, a.ProjectVision), nil
}

func appendProjectVisionDetail(detail, root, vision string) string {
	sentence := ProjectVisionDetail(root, vision)
	if sentence == "" {
		return detail
	}
	if detail != "" {
		detail += "; "
	}
	return detail + sentence
}

// insertAndVerifyOnboardSandboxProfile writes the reviewed generated profile and
// then reads it back and compares every field, so a pre-existing row under the
// same id can never be silently adopted.
func insertAndVerifyOnboardSandboxProfile(ctx context.Context, q Q, a OnboardArgs, profile onboardSandboxProfile) error {
	artifacts, denied, allowlist, err := onboardProfilePathJSON(profile)
	if err != nil {
		return err
	}
	gitAuthorName, gitAuthorEmail := gitIdentityOverrideSQL(profile.GitIdentityOverride)
	if _, err := q.ExecContext(ctx, `
		INSERT INTO sandbox_profiles
			(id, workspace_root, artifact_roots, denied_paths,
			 tool_home_dir, runtime_control_dir, harness_env_policy, tool_env_policy,
			 tool_allowlist, runtime_auth_delivery, git_author_name, git_author_email)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO NOTHING`,
		profile.ID, a.WorkspaceRoot, artifacts, denied,
		profile.ToolHomeDir, profile.RuntimeControlDir, profile.HarnessEnvPolicy,
		profile.ToolEnvPolicy, allowlist, profile.RuntimeAuthDelivery,
		gitAuthorName, gitAuthorEmail); err != nil {
		return err
	}
	var storedRoot, storedArtifacts, storedDenied, storedToolHome string
	var storedRuntimeControl, storedHarnessEnv, storedToolEnv, storedAllowlist, storedAuthDelivery string
	var storedGitAuthorName, storedGitAuthorEmail sql.NullString
	if err := q.QueryRowContext(ctx, `
		SELECT workspace_root, COALESCE(artifact_roots, '[]'),
		       COALESCE(denied_paths, '[]'),
		       COALESCE(tool_home_dir, ''), COALESCE(runtime_control_dir, ''),
		       COALESCE(harness_env_policy, ''), COALESCE(tool_env_policy, ''),
		       COALESCE(tool_allowlist, '[]'), runtime_auth_delivery,
		       git_author_name, git_author_email
		FROM sandbox_profiles WHERE id = ?`, profile.ID,
	).Scan(&storedRoot, &storedArtifacts, &storedDenied,
		&storedToolHome, &storedRuntimeControl, &storedHarnessEnv, &storedToolEnv,
		&storedAllowlist, &storedAuthDelivery, &storedGitAuthorName, &storedGitAuthorEmail); err != nil {
		return err
	}
	storedGitIdentity, err := gitIdentityOverrideFromSQL(storedGitAuthorName, storedGitAuthorEmail)
	if err != nil {
		return err
	}
	if storedRoot != a.WorkspaceRoot || storedArtifacts != artifacts ||
		storedDenied != denied ||
		storedToolHome != profile.ToolHomeDir || storedRuntimeControl != profile.RuntimeControlDir ||
		storedHarnessEnv != profile.HarnessEnvPolicy || storedToolEnv != profile.ToolEnvPolicy ||
		storedAllowlist != allowlist || storedAuthDelivery != profile.RuntimeAuthDelivery ||
		!sameGitIdentityOverride(storedGitIdentity, profile.GitIdentityOverride) {
		return Domainf("sandbox profile %s conflicts with the reviewed generated profile; refuse implicit rewrite", profile.ID)
	}
	return nil
}

// onboardProjectDB is the helper-authoritative half. Workspace roots are
// durable schema data here, never filesystem authority: this function does
// not stat, resolve, or open them.
func onboardProjectDB(a OnboardArgs) (string, string, []OnboardWorkspaceRoot, error) {
	if err := requireSpineFile(a.Spine); err != nil {
		return "", "", nil, err
	}
	supplied := a.Project != "" || a.WorkspaceRoot != ""
	db, err := substrate.Open(a.Spine)
	if err != nil {
		return "", "", nil, Usagef("%v", err)
	}
	defer db.Close()
	status, detail := "", ""
	var roots []OnboardWorkspaceRoot
	err = inTx(db, func(ctx context.Context, q Q) error {
		var existing int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&existing); err != nil {
			return err
		}
		visionMoved, visionDetail := false, ""
		if existing > 0 {
			if supplied {
				var storedRoot, storedProfile string
				var storedArtifacts, storedDenied, storedToolHome string
				var storedRuntimeControl, storedHarnessEnv, storedToolEnv, storedAllowlist, storedAuthDelivery string
				var storedGitAuthorName, storedGitAuthorEmail sql.NullString
				err := q.QueryRowContext(ctx, `
					SELECT p.workspace_root,
					       w.sandbox_profile, COALESCE(p.artifact_roots, '[]'),
					       COALESCE(p.denied_paths, '[]'),
					       COALESCE(p.tool_home_dir, ''), COALESCE(p.runtime_control_dir, ''),
					       COALESCE(p.harness_env_policy, ''), COALESCE(p.tool_env_policy, ''),
					       COALESCE(p.tool_allowlist, '[]'), p.runtime_auth_delivery,
					       p.git_author_name, p.git_author_email
					FROM projects w JOIN sandbox_profiles p ON p.id = w.sandbox_profile
					WHERE w.id = ?`, a.Project).Scan(
					&storedRoot,
					&storedProfile, &storedArtifacts, &storedDenied,
					&storedToolHome, &storedRuntimeControl, &storedHarnessEnv, &storedToolEnv,
					&storedAllowlist, &storedAuthDelivery, &storedGitAuthorName, &storedGitAuthorEmail)
				if err == sql.ErrNoRows {
					var registered int
					if err := q.QueryRowContext(ctx,
						`SELECT COUNT(*) FROM projects WHERE id = ?`, a.Project).Scan(&registered); err != nil {
						return err
					}
					if registered > 0 {
						return Domainf("Project %s has no sandbox profile; a Project must be a Git repository", a.Project)
					}
					registeredDetail, err := registerOnboardProject(ctx, q, a)
					if err != nil {
						return err
					}
					roots = append(roots, OnboardWorkspaceRoot{
						ID: a.Project, Root: a.WorkspaceRoot,
					})
					status, detail = "done", registeredDetail
					return nil
				}
				if err != nil {
					return err
				}
				if storedRoot != a.WorkspaceRoot {
					return Domainf("Project %s already points at %q, not requested workspace %q; refuse implicit rebinding", a.Project, storedRoot, a.WorkspaceRoot)
				}
				profile, profileErr := onboardProjectProfile(a)
				if profileErr != nil {
					return profileErr
				}
				requestedArtifacts, requestedDenied, requestedAllowlist, profileErr :=
					onboardProfilePathJSON(profile)
				if profileErr != nil {
					return profileErr
				}
				storedGitIdentity, profileErr := gitIdentityOverrideFromSQL(storedGitAuthorName, storedGitAuthorEmail)
				if profileErr != nil {
					return profileErr
				}
				if storedProfile != profile.ID ||
					storedArtifacts != requestedArtifacts ||
					storedDenied != requestedDenied || storedToolHome != profile.ToolHomeDir ||
					storedRuntimeControl != profile.RuntimeControlDir ||
					storedHarnessEnv != profile.HarnessEnvPolicy || storedToolEnv != profile.ToolEnvPolicy ||
					storedAllowlist != requestedAllowlist || storedAuthDelivery != profile.RuntimeAuthDelivery ||
					!sameGitIdentityOverride(storedGitIdentity, profile.GitIdentityOverride) {
					return Domainf("Project %s already has a different Git/sandbox posture; refuse implicit rewrite", a.Project)
				}
				// The host supplies the current file state.
				visionMoved, err = recordProjectVision(ctx, q, a.Project, a.ProjectVision)
				if err != nil {
					return err
				}
				if visionMoved {
					visionDetail = appendProjectVisionDetail("", storedRoot, a.ProjectVision)
				}
				roots = append(roots, OnboardWorkspaceRoot{
					ID: a.Project, Root: storedRoot,
				})
			} else {
				// Archived Projects are excluded: an archived workspace may be
				// deleted, and every root returned here is host-rechecked by
				// ValidateOnboardWorkspaceRoots before any layout effect, so an
				// archived row would make the section refuse on a path the
				// operator removed on purpose. Paused Projects are kept — paused
				// is a dispatch posture, not a removal, and their workspaces
				// exist.
				rows, err := q.QueryContext(ctx, `
					SELECT w.id, p.workspace_root
					FROM projects w LEFT JOIN sandbox_profiles p ON p.id = w.sandbox_profile
					WHERE w.status <> 'archived'
					ORDER BY w.id`)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var id string
					var storedRoot sql.NullString
					if err := rows.Scan(&id, &storedRoot); err != nil {
						return err
					}
					if !storedRoot.Valid {
						return Domainf("Project %s has no sandbox profile; repair it before onboarding can call the section healthy", id)
					}
					roots = append(roots, OnboardWorkspaceRoot{
						ID: id, Root: storedRoot.String,
					})
				}
				if err := rows.Err(); err != nil {
					return err
				}
			}
			status = "ok"
			detail = fmt.Sprintf("%d Projects registered, %d live and reachable", existing, len(roots))
			if visionMoved {
				status = "done"
				detail += "; " + visionDetail
			}
			return nil
		}
		if !supplied {
			status, detail = "ok", "Add a Project in the dashboard to start project work."
			return nil
		}
		registeredDetail, err := registerOnboardProject(ctx, q, a)
		if err != nil {
			return err
		}
		status, detail = "done", registeredDetail
		roots = append(roots, OnboardWorkspaceRoot{
			ID: a.Project, Root: a.WorkspaceRoot,
		})
		return nil
	})
	if err != nil {
		return "", "", nil, err
	}
	return status, detail, roots, nil
}

// ValidateOnboardWorkspaceRoots re-attests helper-returned schema paths before
// any host Git or layout effect. A helper response is never filesystem
// authority: each spelling must still name its canonical, unblocked directory.
func ValidateOnboardWorkspaceRoots(roots []OnboardWorkspaceRoot) error {
	for _, root := range roots {
		identity, err := boundary.ResolveSource(root.Root)
		if err != nil {
			return Domainf("Project %s workspace root %q is unavailable: %v", root.ID, root.Root, err)
		}
		if !identity.IsDir {
			return Domainf("Project %s workspace root %q is not a directory", root.ID, root.Root)
		}
		if filepath.Clean(identity.Canonical) != root.Root {
			return Domainf("Project %s workspace root %q is not its canonical identity", root.ID, root.Root)
		}
		if (boundary.BlockPolicy{}).Rejects(identity.RawClean, identity.Canonical) {
			return Domainf("Project %s workspace root %q has a blocked address component", root.ID, root.Root)
		}
	}
	return nil
}

func onboardProject(a OnboardArgs) (string, string, error) {
	prepared, err := prepareOnboardProject(a)
	if err != nil {
		return "", "", err
	}
	gitChanged, gitInitialized := false, false
	if prepared.Project != "" {
		home, err := mcHomeDir()
		if err != nil {
			return "", "", err
		}
		uuid, exists, err := readDeploymentMirror(home)
		if err != nil {
			return "", "", err
		}
		if !exists {
			return "", "", Domainf("deployment identity is not provisioned; run mc onboard home first")
		}
		gitChanged, gitInitialized, err = PrepareOnboardProjectGit(
			prepared.WorkspaceRoot,
			OnboardGitPostureIntent{
				Home: home, DeploymentUUID: uuid, Project: prepared.Project,
			}, hostOnboardGitMutation)
		if err != nil {
			return "", "", err
		}
		homeChanged, err := PrepareOnboardProjectToolHome(prepared, os.Getuid())
		if err != nil {
			return "", "", err
		}
		gitChanged = gitChanged || homeChanged
	}
	status, detail, roots, err := onboardProjectDB(prepared)
	if err != nil {
		return "", "", err
	}
	if prepared.Project != "" {
		detail = AppendProjectGitDetail(detail, prepared.WorkspaceRoot, gitInitialized)
	}
	if prepared.Project == "" {
		for _, root := range roots {
			changed, err := RepairOnboardProjectGit(
				root.Root, hostOnboardGitMutation)
			if err != nil {
				return "", "", err
			}
			gitChanged = gitChanged || changed
		}
	}
	if err := ValidateOnboardWorkspaceRoots(roots); err != nil {
		return "", "", err
	}
	status, detail, err = FinishOnboardProjectLayout(status, detail, roots)
	if err == nil && gitChanged {
		status = "done"
	}
	return status, detail, err
}

// FinishOnboardProjectLayout is the tail both lanes share: the local
// in-process one above, and the production one where the helper authorizes the
// roots and the HOST half builds them (onboard_state_prod.go). Keeping it in
// one function is what stops the two lanes from disagreeing about what
// onboarding leaves on disk.
//
// It must never run inside the helper. The roots it creates are proved against
// the OPERATOR's uid by every later consumer, and a helper builds them as root
// against container paths that do not include the workspace at all.
func FinishOnboardProjectLayout(status, detail string, roots []OnboardWorkspaceRoot) (string, string, error) {
	changed, layout, err := EnsureOnboardWorkspaceLayout(roots, os.Getuid())
	if err != nil {
		return "", "", err
	}
	if layout != "" {
		if detail != "" {
			detail += "; "
		}
		detail += layout
	}
	// The artifact plane needs BOTH halves or it is not usable: the directory
	// (created above) and the allowlist grant that authorizes binding it. A
	// profile naming an artifact root the allowlist does not carry refuses at
	// dispatch, so splitting these across sections would ship the same
	// half-configured state that left `artifact_roots` empty in the first place.
	home, err := mcHomeDir()
	if err != nil {
		return "", "", err
	}
	for _, root := range roots {
		granted, err := ensureProjectMountGrant(home, root.ID, ProjectArtifactRoot(root.Root))
		if err != nil {
			return "", "", err
		}
		if granted {
			if detail != "" {
				detail += "; "
			}
			detail += root.ID + ": authorized .mc-artifacts in the mount allowlist"
			changed = true
		}
		// The Project's operator skill store directories: one per role,
		// created here so both lanes leave the same empty shape for the
		// operator to fill.
		extended, err := ExtendSkillStore(home, root.ID)
		if err != nil {
			return "", "", err
		}
		if extended {
			if detail != "" {
				detail += "; "
			}
			detail += root.ID + ": created the operator skill store directories"
			changed = true
		}
	}
	// `ok` means "already healthy" and `done` means "this run changed
	// something"; the section receipt admits no other value.
	if changed {
		status = "done"
	}
	return status, detail, nil
}

func onboardTunables(a OnboardArgs) (string, string, error) {
	plan, err := PrepareOnboardTunablesHost(a)
	if err != nil {
		return "", "", err
	}
	projected := plan.Project(a)
	return onboardTunablesProjection(projected, func() (bool, error) {
		return operatorconfig.UpdateAtomic(plan.Path, func(cfg *operatorconfig.Config) error {
			applyOnboardTunableAnswers(cfg, a)
			return cfg.Validate()
		})
	})
}

type onboardTunable struct {
	column string
	value  int
}

func onboardTunablesDB(a OnboardArgs) (string, string, error) {
	return onboardTunablesProjection(a, nil)
}

func onboardTunablesProjection(a OnboardArgs, publishConfig func() (bool, error)) (string, string, error) {
	requested := []onboardTunable{
		{"review_wip_cap", a.ReviewWIPCap}, {"timeout_minutes", a.TimeoutMinutes},
		{"grace_minutes", a.GraceMinutes}, {"heartbeat_interval_s", a.HeartbeatIntervalS},
		{"spawn_grace_s", a.SpawnGraceS}, {"hard_deadline_minutes", a.HardDeadlineMinutes},
		{"homie_idle_timeout_s", a.HomieIdleTimeoutS},
		{"wiki_update_interval_s", a.WikiUpdateIntervalS},
	}
	for _, item := range requested {
		if item.value <= 0 {
			return "", "", Usagef("mc onboard tunables projection requires positive %s", item.column)
		}
	}
	if err := requireSpineFile(a.Spine); err != nil {
		return "", "", err
	}
	db, err := substrate.Open(a.Spine)
	if err != nil {
		return "", "", Usagef("%v", err)
	}
	defer db.Close()
	status, detail := "", ""
	configChanged := false
	err = inTx(db, func(ctx context.Context, q Q) error {
		var reviewCap, timeout, grace, heartbeat, spawn, deadline, homieIdle, wikiInterval int
		if err := q.QueryRowContext(ctx, `
			SELECT review_wip_cap, timeout_minutes, grace_minutes, heartbeat_interval_s,
			       spawn_grace_s, hard_deadline_minutes, homie_idle_timeout_s, wiki_update_interval_s
			FROM settings WHERE id = 1`,
		).Scan(&reviewCap, &timeout, &grace, &heartbeat, &spawn, &deadline, &homieIdle, &wikiInterval); err != nil {
			return err
		}
		current := map[string]int{
			"review_wip_cap":  reviewCap,
			"timeout_minutes": timeout, "grace_minutes": grace,
			"heartbeat_interval_s": heartbeat, "spawn_grace_s": spawn,
			"hard_deadline_minutes": deadline, "homie_idle_timeout_s": homieIdle,
			"wiki_update_interval_s": wikiInterval,
		}
		mismatched := []onboardTunable{}
		for _, item := range requested {
			if current[item.column] != item.value {
				mismatched = append(mismatched, item)
			}
		}
		for _, item := range mismatched {
			if _, err := q.ExecContext(ctx,
				fmt.Sprintf(`UPDATE settings SET %s = ? WHERE id = 1`, item.column), item.value); err != nil {
				return err
			}
		}
		if publishConfig != nil {
			configChanged, err = publishConfig()
			if err != nil {
				return Domainf("publish authoritative tunables: %v", err)
			}
		}
		if len(mismatched) == 0 && !configChanged {
			status = "ok"
			detail = "config.toml tunables and spine projection already agree"
			return nil
		}
		names := make([]string, 0, len(mismatched))
		for _, item := range mismatched {
			names = append(names, item.column)
		}
		status = "done"
		detail = "spine tunable projection updated"
		if publishConfig != nil {
			detail = "config.toml authority updated"
		}
		if len(names) != 0 {
			detail += "; projected: " + strings.Join(names, ", ")
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return status, detail, nil
}

// onboardSurfaces projects the validated alert routing; the
// dashboard is core with loopback defaults, and Discord is skippable
// entirely — both land with their own processes.
func onboardSurfacesDB(a OnboardArgs, routes []OnboardAlertRoute) (string, string, error) {
	if err := validateOnboardAlertRoutes(routes); err != nil {
		return "", "", err
	}
	if err := requireSpineFile(a.Spine); err != nil {
		return "", "", err
	}
	db, err := substrate.Open(a.Spine)
	if err != nil {
		return "", "", Usagef("%v", err)
	}
	defer db.Close()
	status, detail := "", ""
	err = inTx(db, func(ctx context.Context, q Q) error {
		rows, err := q.QueryContext(ctx, `
			SELECT class,ordinal,surface,COALESCE(channel_ref,'')
			FROM alert_routes
			ORDER BY CASE class WHEN 'blocked' THEN 1 ELSE 2 END, ordinal`)
		if err != nil {
			return err
		}
		var currentRoutes []OnboardAlertRoute
		for rows.Next() {
			var route OnboardAlertRoute
			if err := rows.Scan(&route.Class, &route.Ordinal, &route.Surface, &route.ChannelRef); err != nil {
				rows.Close()
				return err
			}
			currentRoutes = append(currentRoutes, route)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		routesChanged := !slices.Equal(currentRoutes, routes)

		if routesChanged {
			if _, err := q.ExecContext(ctx, `DELETE FROM alert_routes`); err != nil {
				return err
			}
			for _, route := range routes {
				var channel any
				if route.ChannelRef != "" {
					channel = route.ChannelRef
				}
				if _, err := q.ExecContext(ctx, `
					INSERT INTO alert_routes (class,ordinal,surface,channel_ref)
					VALUES (?,?,?,?)`,
					route.Class, route.Ordinal, route.Surface, channel); err != nil {
					return err
				}
			}
			status = "done"
			detail = "alert routing projected"
		} else {
			status = "ok"
			detail = "alert routing verified"
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return status, detail, nil
}

func onboardSurfaces(a OnboardArgs) (string, string, error) {
	plan, err := PrepareOnboardSurfacesHost(a)
	if err != nil {
		return "", "", err
	}
	hostStatus, hostDetail, err := ApplyOnboardSurfacesHost(plan)
	if err != nil {
		return "", "", err
	}
	req := OnboardStateRequest{Section: "surfaces"}
	if err := ProjectCurrentOnboardAlertRoutes(&req); err != nil {
		return "", "", err
	}
	status, detail, err := onboardSurfacesDB(a, req.AlertRoutes)
	if err != nil {
		return "", "", err
	}
	if hostStatus == "done" {
		status = "done"
	}
	return status, detail + "; " + hostDetail, nil
}

// onboardVerify checks setup while a missing vision can still block work.
func onboardVerify(spine string) (string, string, error) {
	res, err := Doctor(nil, spine)
	if err != nil {
		return "", "", err
	}
	report := res.(map[string]any)
	findings := report["findings"].([]doctorFinding)
	detail, err := OnboardVerification(findings)
	if err != nil {
		return "", "", err
	}
	return "ok", detail, nil
}

// OnboardVerification separates setup failures from a required vision.
func OnboardVerification(findings []DoctorFinding) (string, error) {
	okCount, deferredCount := 0, 0
	failing := []string{}
	visionBlocks := []string{}
	for _, finding := range findings {
		switch finding.Status {
		case "ok":
			okCount++
		case "deferred":
			deferredCount++
		case "fail":
			if finding.Check == "projects" && finding.SetupComplete {
				visionBlocks = append(visionBlocks, finding.Detail)
				continue
			}
			failing = append(failing, fmt.Sprintf("%s: %s", finding.Check, finding.Detail))
		}
	}
	if len(failing) != 0 {
		return "", Domainf("Setup verification failed: %s. Ask your agent to repair these checks.", strings.Join(failing, "; "))
	}
	detail := fmt.Sprintf("Setup verification passed: %d checks passed, %d checks deferred.", okCount, deferredCount)
	if len(visionBlocks) != 0 {
		detail += " Setup is complete. " + strings.Join(visionBlocks, "; ")
	}
	return detail, nil
}
