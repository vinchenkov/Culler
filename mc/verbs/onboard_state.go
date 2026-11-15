package verbs

import (
	"os"
	"path/filepath"
	"strings"

	"mc/substrate"
)

// OnboardStateRequest is the closed host→helper frame for the spine-backed
// onboarding sections. WorkspaceRoot is schema data attested by Darwin; the
// helper stores or compares it but never treats it as a path. AlertRoutes is
// the validated secret-free projection of config.toml; no token or author
// identity crosses.
type OnboardStateRequest struct {
	ProtocolVersion     int    `json:"protocol_version"`
	ReleaseBuildID      string `json:"release_build_id"`
	ControlVersion      int    `json:"control_version"`
	SpineSchemaID       string `json:"spine_schema_id"`
	ConfigSchemaVersion int    `json:"config_schema_version"`
	DeploymentUUID      string `json:"deployment_uuid"`
	Section             string `json:"section"`

	Project                string `json:"project,omitempty"`
	WorkspaceRoot          string `json:"workspace_root,omitempty"`
	ProjectGitAuthorName   string `json:"project_git_author_name,omitempty"`
	ProjectGitAuthorEmail  string `json:"project_git_author_email,omitempty"`
	AcceptGeneratedSandbox bool   `json:"accept_generated_sandbox,omitempty"`
	ProjectTitle           string `json:"project_title,omitempty"`
	ProjectPaused          bool   `json:"project_paused,omitempty"`
	ProjectVisionSetup     bool   `json:"project_vision_setup,omitempty"`
	ProjectSandboxProfile  string `json:"project_sandbox_profile,omitempty"`
	ProjectToolHomeDir     string `json:"project_tool_home_dir,omitempty"`
	ProjectVision          string `json:"project_vision,omitempty"`

	TimeoutMinutes      int  `json:"timeout_minutes,omitempty"`
	GraceMinutes        int  `json:"grace_minutes,omitempty"`
	HeartbeatIntervalS  int  `json:"heartbeat_interval_s,omitempty"`
	SpawnGraceS         int  `json:"spawn_grace_s,omitempty"`
	HardDeadlineMinutes int  `json:"hard_deadline_minutes,omitempty"`
	CollieIdleTimeoutS  int  `json:"collie_idle_timeout_s,omitempty"`
	WikiUpdateIntervalS int  `json:"wiki_update_interval_s,omitempty"`
	ReviewWIPCap        int  `json:"review_wip_cap,omitempty"`
	TunablesConfirmed   bool `json:"tunables_confirmed,omitempty"`

	AlertRoutes []OnboardAlertRoute `json:"alert_routes,omitempty"`
}

type OnboardAlertRoute struct {
	Class      string `json:"class"`
	Ordinal    int    `json:"ordinal"`
	Surface    string `json:"surface"`
	ChannelRef string `json:"channel_ref,omitempty"`
}

func validateOnboardAlertRoutes(routes []OnboardAlertRoute) error {
	position := 0
	for _, class := range []string{"blocked", "health"} {
		seen := map[string]bool{}
		ordinal := 1
		for position < len(routes) && routes[position].Class == class {
			route := routes[position]
			if route.Ordinal != ordinal || (route.Surface != "dashboard" && route.Surface != "discord") ||
				seen[route.Surface] ||
				(route.Surface == "dashboard" && route.ChannelRef != "") ||
				(route.Surface == "discord" && !decimalAlertChannel(route.ChannelRef)) {
				return Domainf("configured alert route projection is malformed")
			}
			seen[route.Surface] = true
			ordinal++
			position++
		}
		if ordinal == 1 {
			return Domainf("configured alert class %q has no destination", class)
		}
	}
	if position != len(routes) {
		return Domainf("configured alert route projection is malformed")
	}
	return nil
}

func decimalAlertChannel(value string) bool {
	if len(value) < 1 || len(value) > 20 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

type OnboardStateResult struct {
	ProtocolVersion     int                    `json:"protocol_version"`
	ReleaseBuildID      string                 `json:"release_build_id"`
	ControlVersion      int                    `json:"control_version"`
	SpineSchemaID       string                 `json:"spine_schema_id"`
	ConfigSchemaVersion int                    `json:"config_schema_version"`
	DeploymentUUID      string                 `json:"deployment_uuid"`
	Section             string                 `json:"section"`
	Status              string                 `json:"status"`
	Detail              string                 `json:"detail"`
	WorkspaceRoots      []OnboardWorkspaceRoot `json:"workspace_roots,omitempty"`
}

func onboardStateBase(a OnboardArgs, releaseBuildID string, controlVersion, configSchemaVersion int) OnboardStateRequest {
	return OnboardStateRequest{
		ProtocolVersion: 1, ReleaseBuildID: releaseBuildID,
		ControlVersion: controlVersion, SpineSchemaID: substrate.CurrentSchemaID,
		ConfigSchemaVersion: configSchemaVersion, Section: a.Section,
		Project: a.Project, WorkspaceRoot: a.WorkspaceRoot,
		ProjectGitAuthorName:   a.ProjectGitAuthorName,
		ProjectGitAuthorEmail:  a.ProjectGitAuthorEmail,
		AcceptGeneratedSandbox: a.AcceptGeneratedSandbox,
		ProjectTitle:           a.ProjectTitle,
		ProjectPaused:          a.ProjectPaused,
		ProjectVisionSetup:     a.ProjectVisionSetup,
		ProjectSandboxProfile:  a.ProjectSandboxProfile,
		ProjectToolHomeDir:     a.ProjectToolHomeDir,
		ProjectVision:          a.ProjectVision,
		TimeoutMinutes:         a.TimeoutMinutes, GraceMinutes: a.GraceMinutes,
		HeartbeatIntervalS: a.HeartbeatIntervalS, SpawnGraceS: a.SpawnGraceS,
		HardDeadlineMinutes: a.HardDeadlineMinutes, CollieIdleTimeoutS: a.CollieIdleTimeoutS,
		WikiUpdateIntervalS: a.WikiUpdateIntervalS,
		ReviewWIPCap:        a.ReviewWIPCap, TunablesConfirmed: a.TunablesConfirmed,
	}
}

// PrepareOnboardState performs every host-authoritative check and binds the
// request to the deployment mirror. It deliberately refuses host-only
// sections; surfaces is the one narrow exception and receives only the closed,
// secret-free alert-route projection after this base request is prepared.
func PrepareOnboardState(a OnboardArgs, releaseBuildID string, controlVersion, configSchemaVersion int) (OnboardStateRequest, error) {
	if !validStructuralText(releaseBuildID, maxPrivateScalarBytes) || releaseBuildID == "" {
		return OnboardStateRequest{}, Usagef("private onboard-state release identity is invalid")
	}
	var err error
	switch a.Section {
	case "routing":
	case "supervision":
	case "project":
		a, err = prepareOnboardProject(a)
		if err != nil {
			return OnboardStateRequest{}, err
		}
	case "smoke":
		a, err = prepareOnboardSmoke(a)
		if err != nil {
			return OnboardStateRequest{}, err
		}
	case "tunables":
		for _, value := range []int{a.TimeoutMinutes, a.GraceMinutes, a.HeartbeatIntervalS, a.SpawnGraceS, a.HardDeadlineMinutes, a.CollieIdleTimeoutS, a.WikiUpdateIntervalS, a.ReviewWIPCap} {
			if value < 0 {
				return OnboardStateRequest{}, Usagef("mc onboard tunables values must be non-negative")
			}
		}
	case "surfaces":
	default:
		return OnboardStateRequest{}, Usagef("section %q is not a private onboarding state section", a.Section)
	}
	home, err := mcHomeDir()
	if err != nil {
		return OnboardStateRequest{}, err
	}
	uuid, exists, err := readDeploymentMirror(home)
	if err != nil {
		return OnboardStateRequest{}, err
	}
	if !exists {
		return OnboardStateRequest{}, Domainf("deployment identity is not provisioned; run mc onboard home first")
	}
	req := onboardStateBase(a, releaseBuildID, controlVersion, configSchemaVersion)
	req.DeploymentUUID = uuid
	return req, nil
}

// RecheckOnboardStateHost narrows the unavoidable pathname race immediately
// before the helper call. The finalizer checks the returned roots again after
// the transaction; at no point does the helper receive filesystem authority.
func RecheckOnboardStateHost(req OnboardStateRequest) error {
	if req.Section != "project" || req.WorkspaceRoot == "" {
		return nil
	}
	st, err := os.Stat(req.WorkspaceRoot)
	if err != nil {
		return Domainf("recheck workspace root %q: %v", req.WorkspaceRoot, err)
	}
	if !st.IsDir() {
		return Domainf("recheck workspace root %q: not a directory", req.WorkspaceRoot)
	}
	resolved, err := filepath.EvalSymlinks(req.WorkspaceRoot)
	if err != nil {
		return Domainf("recheck workspace root %q: %v", req.WorkspaceRoot, err)
	}
	if filepath.Clean(resolved) != req.WorkspaceRoot {
		return Domainf("workspace root %q changed after host attestation; retry onboarding", req.WorkspaceRoot)
	}
	return nil
}

func validateOnboardStateIdentity(req OnboardStateRequest, releaseBuildID string, controlVersion, configSchemaVersion int) error {
	if req.ProtocolVersion != 1 || req.ReleaseBuildID != releaseBuildID ||
		req.ControlVersion != controlVersion || req.SpineSchemaID != substrate.CurrentSchemaID ||
		req.ConfigSchemaVersion != configSchemaVersion || req.DeploymentUUID == "" {
		return Domainf("private onboard-state build/schema identity mismatch")
	}
	projectFields := req.Project != "" || req.WorkspaceRoot != "" ||
		req.ProjectGitAuthorName != "" ||
		req.ProjectGitAuthorEmail != "" || req.AcceptGeneratedSandbox ||
		req.ProjectSandboxProfile != "" || req.ProjectToolHomeDir != "" ||
		req.ProjectVision != ""
	tunableFields := req.TimeoutMinutes != 0 || req.GraceMinutes != 0 || req.HeartbeatIntervalS != 0 ||
		req.SpawnGraceS != 0 || req.HardDeadlineMinutes != 0 || req.CollieIdleTimeoutS != 0 ||
		req.WikiUpdateIntervalS != 0 || req.ReviewWIPCap != 0 || req.TunablesConfirmed
	surfaceFields := len(req.AlertRoutes) != 0
	switch req.Section {
	case "routing":
		if projectFields || tunableFields || surfaceFields {
			return Domainf("private onboard-state routing frame carries another section's fields")
		}
	case "supervision":
		if projectFields || tunableFields || surfaceFields {
			return Domainf("private onboard-state supervision frame carries another section's fields")
		}
	case "project":
		if tunableFields || surfaceFields {
			return Domainf("private onboard-state project frame carries another section's fields")
		}
	case "smoke":
		if !projectFields || tunableFields || surfaceFields {
			return Domainf("private onboard-state smoke frame carries an invalid field set")
		}
	case "tunables":
		if projectFields || surfaceFields {
			return Domainf("private onboard-state tunables frame carries another section's fields")
		}
	case "surfaces":
		if projectFields || tunableFields || validateOnboardAlertRoutes(req.AlertRoutes) != nil {
			return Domainf("private onboard-state surfaces frame carries another section's fields")
		}
	default:
		return Usagef("unknown private onboard-state section %q", req.Section)
	}
	return nil
}

func onboardArgsFromState(spine string, req OnboardStateRequest) OnboardArgs {
	return OnboardArgs{
		Section: req.Section, Spine: spine, Smoke: req.Section == "smoke",
		Project: req.Project, WorkspaceRoot: req.WorkspaceRoot,
		ProjectGitAuthorName:   req.ProjectGitAuthorName,
		ProjectGitAuthorEmail:  req.ProjectGitAuthorEmail,
		AcceptGeneratedSandbox: req.AcceptGeneratedSandbox,
		ProjectTitle:           req.ProjectTitle,
		ProjectPaused:          req.ProjectPaused,
		ProjectVisionSetup:     req.ProjectVisionSetup,
		ProjectSandboxProfile:  req.ProjectSandboxProfile,
		ProjectToolHomeDir:     req.ProjectToolHomeDir,
		ProjectVision:          req.ProjectVision,
		TimeoutMinutes:         req.TimeoutMinutes, GraceMinutes: req.GraceMinutes,
		HeartbeatIntervalS: req.HeartbeatIntervalS, SpawnGraceS: req.SpawnGraceS,
		HardDeadlineMinutes: req.HardDeadlineMinutes, CollieIdleTimeoutS: req.CollieIdleTimeoutS,
		WikiUpdateIntervalS: req.WikiUpdateIntervalS,
		ReviewWIPCap:        req.ReviewWIPCap, TunablesConfirmed: req.TunablesConfirmed,
	}
}

// OnboardState runs only inside the fixed helper scope. It checks the mirror
// identity against the spine before any section mutation.
func OnboardState(spine string, req OnboardStateRequest, releaseBuildID string, controlVersion, configSchemaVersion int) (OnboardStateResult, error) {
	if err := validateOnboardStateIdentity(req, releaseBuildID, controlVersion, configSchemaVersion); err != nil {
		return OnboardStateResult{}, err
	}
	inspection, err := inspectSpineReadOnly(spine)
	if err != nil {
		return OnboardStateResult{}, err
	}
	if !inspection.exists {
		return OnboardStateResult{}, Domainf("private onboard-state deployment identity mismatch")
	}
	if err := validateInspection(spine, inspection); err != nil {
		return OnboardStateResult{}, err
	}
	if inspection.schemaID != substrate.CurrentSchemaID || inspection.uuid != req.DeploymentUUID {
		return OnboardStateResult{}, Domainf("private onboard-state deployment identity mismatch")
	}
	a := onboardArgsFromState(spine, req)
	var status, detail string
	var roots []OnboardWorkspaceRoot
	switch req.Section {
	case "routing":
		status, detail = "ok", "deployment identity verified"
	case "project":
		if _, err := prepareOnboardProjectFrame(a); err != nil {
			return OnboardStateResult{}, err
		}
		status, detail, roots, err = onboardProjectDB(a)
	case "smoke":
		if _, err := prepareOnboardProjectFrame(a); err != nil {
			return OnboardStateResult{}, err
		}
		status, detail, roots, err = onboardSmokeDB(a)
	case "tunables":
		status, detail, err = onboardTunablesDB(a)
	case "surfaces":
		status, detail, err = onboardSurfacesDB(a, req.AlertRoutes)
	case "supervision":
		status, detail, roots, err = onboardProjectDB(OnboardArgs{Section: "project", Spine: spine})
	}
	if err != nil {
		return OnboardStateResult{}, err
	}
	return OnboardStateResult{
		ProtocolVersion: req.ProtocolVersion, ReleaseBuildID: req.ReleaseBuildID,
		ControlVersion: req.ControlVersion, SpineSchemaID: req.SpineSchemaID,
		ConfigSchemaVersion: req.ConfigSchemaVersion, DeploymentUUID: req.DeploymentUUID,
		Section: req.Section, Status: status, Detail: detail, WorkspaceRoots: roots,
	}, nil
}

func prepareOnboardProjectFrame(a OnboardArgs) (OnboardArgs, error) {
	supplied := a.Project != "" || a.WorkspaceRoot != "" ||
		a.ProjectGitAuthorName != "" ||
		a.ProjectGitAuthorEmail != "" || a.AcceptGeneratedSandbox ||
		a.ProjectSandboxProfile != "" || a.ProjectToolHomeDir != "" ||
		a.ProjectVision != ""
	if supplied && (a.Project == "" || a.WorkspaceRoot == "") {
		return OnboardArgs{}, Usagef("mc onboard project requires --project and --workspace-root together")
	}
	if a.Section == "smoke" {
		// The synthetic home is REQUIRED here, not optional: this section
		// carries no profile JSON to hold it, and a profile without one
		// registers a Project that dispatch refuses at commit.
		if a.ProjectGitAuthorName != "" ||
			a.ProjectGitAuthorEmail != "" || a.AcceptGeneratedSandbox ||
			a.ProjectSandboxProfile != "" || a.ProjectVision != "" ||
			!validStructuralText(a.Project, maxPrivateScalarBytes) ||
			!filepathIsCleanAbsolute(a.WorkspaceRoot) ||
			!validStructuralText(a.ProjectToolHomeDir, maxPrivateScalarBytes) ||
			!filepathIsCleanAbsolute(a.ProjectToolHomeDir) {
			return OnboardArgs{}, Usagef("private onboard smoke Project frame is invalid")
		}
		return a, nil
	}
	// Outside the smoke section the synthetic home rides inside the canonical
	// profile receipt, so a second copy in the frame is a second authority.
	if a.ProjectToolHomeDir != "" {
		return OnboardArgs{}, Usagef("private onboard project frame carries its synthetic home in the sandbox profile, not as a scalar")
	}
	if supplied && (!validStructuralText(a.Project, maxPrivateScalarBytes) ||
		a.Project == "." || a.Project == ".." || strings.ContainsAny(a.Project, `/\`) ||
		!validStructuralText(a.WorkspaceRoot, maxPrivateScalarBytes) ||
		!validProfileGitIdentity(onboardGitIdentityOverride(a)) ||
		!a.AcceptGeneratedSandbox ||
		a.ProjectSandboxProfile == "" ||
		!ValidProjectVisionState(a.ProjectVision) ||
		!filepathIsCleanAbsolute(a.WorkspaceRoot)) {
		return OnboardArgs{}, Usagef("private onboard project frame is invalid")
	}
	if _, _, err := canonicalOnboardSandboxProfile(a); err != nil {
		return OnboardArgs{}, err
	}
	return a, nil
}

func filepathIsCleanAbsolute(path string) bool {
	return path != "" && filepath.IsAbs(path) && path == filepath.Clean(path)
}

func FinalizeOnboardState(req OnboardStateRequest, result OnboardStateResult) (string, string, error) {
	if result.ProtocolVersion != req.ProtocolVersion || result.ReleaseBuildID != req.ReleaseBuildID ||
		result.ControlVersion != req.ControlVersion || result.SpineSchemaID != req.SpineSchemaID ||
		result.ConfigSchemaVersion != req.ConfigSchemaVersion || result.DeploymentUUID != req.DeploymentUUID ||
		result.Section != req.Section || (result.Status != "ok" && result.Status != "done") ||
		!validStructuralText(result.Detail, maxPrivateScalarBytes) {
		return "", "", Domainf("private onboard-state response identity mismatch")
	}
	if req.Section == "project" || req.Section == "supervision" || req.Section == "smoke" {
		if len(result.WorkspaceRoots) == 0 && (req.Project != "" || req.Section == "smoke") {
			return "", "", Domainf("private onboard-state response omitted workspace roots")
		}
		for _, root := range result.WorkspaceRoots {
			if !validStructuralText(root.ID, maxPrivateScalarBytes) || !validStructuralText(root.Root, maxPrivateScalarBytes) ||
				!filepathIsCleanAbsolute(root.Root) {
				return "", "", Domainf("private onboard-state response carries an invalid workspace root")
			}
		}
		if (req.Section == "project" || req.Section == "smoke") && req.Project != "" && (len(result.WorkspaceRoots) != 1 ||
			result.WorkspaceRoots[0].ID != req.Project || result.WorkspaceRoots[0].Root != req.WorkspaceRoot) {
			return "", "", Domainf("private onboard-state response changed the requested Project")
		}
		if err := ValidateOnboardWorkspaceRoots(result.WorkspaceRoots); err != nil {
			return "", "", err
		}
	} else if len(result.WorkspaceRoots) != 0 {
		return "", "", Domainf("private onboard-state response carries unexpected workspace roots")
	}
	return result.Status, result.Detail, nil
}

func OnboardRoutingHost(a OnboardArgs) (string, string, error) { return onboardRouting(a) }
