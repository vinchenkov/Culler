package verbs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/substrate"
)

const (
	testOnboardBuild   = "test-build"
	testOnboardControl = 1
	testOnboardConfig  = 1
)

func provisionOnboardStateFixture(t *testing.T) (string, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	spine := filepath.Join(home, "spine.db")
	t.Setenv("MC_HOME", home)
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}
	return home, spine
}

func TestOnboardStateProjectKeepsFilesystemAuthorityOnHost(t *testing.T) {
	_, spine := provisionOnboardStateFixture(t)
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeVisionFixture(t, root)
	link := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}

	req, err := PrepareOnboardState(OnboardArgs{
		Section: "project", Project: "primary", WorkspaceRoot: link,
		AcceptGeneratedSandbox: true,
	}, testOnboardBuild, testOnboardControl, testOnboardConfig)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if req.WorkspaceRoot != canonicalRoot {
		t.Fatalf("prepared workspace root = %q, want canonical %q", req.WorkspaceRoot, canonicalRoot)
	}

	// The helper half must not stat or resolve a host path. Removing it after
	// host attestation therefore does not prevent the spine mutation; the
	// host-side finalizer catches the race and refuses to call it healthy.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := RecheckOnboardStateHost(req); err == nil || !strings.Contains(err.Error(), "recheck workspace root") {
		t.Fatalf("host recheck error = %v", err)
	}
	result, err := OnboardState(spine, req, testOnboardBuild, testOnboardControl, testOnboardConfig)
	if err != nil {
		t.Fatalf("helper state operation consulted host path: %v", err)
	}
	if _, _, err := FinalizeOnboardState(req, result); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("finalize error = %v, want unavailable host root", err)
	}
}

func TestOnboardStateReplaysAllSpineSections(t *testing.T) {
	_, spine := provisionOnboardStateFixture(t)
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeVisionFixture(t, root)
	cases := []OnboardArgs{
		{Section: "project", Project: "primary", WorkspaceRoot: root,
			AcceptGeneratedSandbox: true},
		{Section: "tunables", TimeoutMinutes: 17, GraceMinutes: 15,
			HeartbeatIntervalS: 60, SpawnGraceS: 60,
			HardDeadlineMinutes: 240, HomieIdleTimeoutS: 1800, WikiUpdateIntervalS: 3600, ReviewWIPCap: 5},
		{Section: "surfaces"},
	}
	for _, args := range cases {
		t.Run(args.Section, func(t *testing.T) {
			req, err := PrepareOnboardState(args, testOnboardBuild, testOnboardControl, testOnboardConfig)
			if err != nil {
				t.Fatal(err)
			}
			if args.Section == "surfaces" {
				cfg := operatorconfig.Default()
				cfg.Surfaces.Discord.ChannelID = "123456789"
				cfg.Surfaces.Alerts.Blocked = []string{"dashboard", "discord"}
				if err := ProjectOnboardAlertRoutes(&req, cfg); err != nil {
					t.Fatal(err)
				}
			}
			first, err := OnboardState(spine, req, testOnboardBuild, testOnboardControl, testOnboardConfig)
			if err != nil {
				t.Fatal(err)
			}
			status, _, err := FinalizeOnboardState(req, first)
			if err != nil || status != "done" {
				t.Fatalf("first finalize = %q, %v", status, err)
			}
			second, err := OnboardState(spine, req, testOnboardBuild, testOnboardControl, testOnboardConfig)
			if err != nil {
				t.Fatal(err)
			}
			status, _, err = FinalizeOnboardState(req, second)
			if err != nil || status != "ok" {
				t.Fatalf("replay finalize = %q, %v", status, err)
			}
		})
	}
}

func TestOnboardStateFrameIsClosedAndIdentityBound(t *testing.T) {
	_, spine := provisionOnboardStateFixture(t)
	req, err := PrepareOnboardState(OnboardArgs{Section: "tunables"}, testOnboardBuild, testOnboardControl, testOnboardConfig)
	if err != nil {
		t.Fatal(err)
	}
	bad := req
	bad.ReleaseBuildID = "other-build"
	if _, err := OnboardState(spine, bad, testOnboardBuild, testOnboardControl, testOnboardConfig); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("mismatched build error = %v", err)
	}
	if req.SpineSchemaID != substrate.CurrentSchemaID || req.DeploymentUUID == "" {
		t.Fatalf("request lacks spine/deployment identity: %+v", req)
	}
	routing, err := PrepareOnboardState(OnboardArgs{Section: "routing"}, testOnboardBuild, testOnboardControl, testOnboardConfig)
	if err != nil {
		t.Fatal(err)
	}
	if routing.WorkspaceRoot != "" || routing.Project != "" || len(routing.AlertRoutes) != 0 {
		t.Fatalf("routing identity frame leaked host/config data: %+v", routing)
	}
	if req.ReviewWIPCap != 0 {
		t.Fatalf("empty tunables request invented review cap: %+v", req)
	}
}

func TestOnboardStateProjectsConfiguredAlertRoutesWithoutHostSecrets(t *testing.T) {
	_, spine := provisionOnboardStateFixture(t)
	req, err := PrepareOnboardState(OnboardArgs{
		Section: "surfaces",
	}, testOnboardBuild, testOnboardControl, testOnboardConfig)
	if err != nil {
		t.Fatal(err)
	}
	cfg := operatorconfig.Default()
	cfg.Surfaces.Discord = operatorconfig.Discord{
		Enabled: true, BotToken: "host-secret", GuildID: "11",
		ChannelID: "22", OperatorIDs: []string{"33"},
	}
	cfg.Surfaces.Alerts.Blocked = []string{"dashboard", "discord"}
	cfg.Surfaces.Alerts.Health = []string{"dashboard", "discord"}
	if err := ProjectOnboardAlertRoutes(&req, cfg); err != nil {
		t.Fatal(err)
	}
	encoded := strings.Join([]string{
		req.AlertRoutes[0].Class, req.AlertRoutes[0].Surface,
		req.AlertRoutes[1].Class, req.AlertRoutes[1].Surface, req.AlertRoutes[1].ChannelRef,
	}, "/")
	if encoded != "blocked/dashboard/blocked/discord/22" {
		t.Fatalf("projected routes = %+v", req.AlertRoutes)
	}
	for _, route := range req.AlertRoutes {
		if route.ChannelRef == "host-secret" || route.ChannelRef == "33" {
			t.Fatalf("host credential or operator identity crossed in route: %+v", route)
		}
	}
	if _, err := OnboardState(
		spine, req, testOnboardBuild, testOnboardControl, testOnboardConfig,
	); err != nil {
		t.Fatal(err)
	}
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got string
	if err := db.QueryRow(`
		SELECT group_concat(class || ':' || surface || ':' || COALESCE(channel_ref,'<null>'), ',')
		FROM (
			SELECT * FROM alert_routes
			ORDER BY CASE class WHEN 'blocked' THEN 1 ELSE 2 END, ordinal
		)`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "blocked:dashboard:<null>,blocked:discord:22,"+
		"health:dashboard:<null>,health:discord:22" {
		t.Fatalf("spine alert route projection = %q", got)
	}

	bad := req
	bad.AlertRoutes = append([]OnboardAlertRoute(nil), req.AlertRoutes...)
	bad.AlertRoutes[1].ChannelRef = "not-a-channel"
	if _, err := OnboardState(
		spine, bad, testOnboardBuild, testOnboardControl, testOnboardConfig,
	); err == nil || !strings.Contains(err.Error(), "surfaces frame") {
		t.Fatalf("malformed alert projection error = %v", err)
	}
}

func TestOnboardStateSupervisionReturnsHostRecheckedWorkspaceCatalog(t *testing.T) {
	_, spine := provisionOnboardStateFixture(t)
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeVisionFixture(t, root)
	project, err := PrepareOnboardState(OnboardArgs{
		Section: "project", Project: "primary", WorkspaceRoot: root,
		AcceptGeneratedSandbox: true,
	}, testOnboardBuild, testOnboardControl, testOnboardConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OnboardState(spine, project, testOnboardBuild, testOnboardControl, testOnboardConfig); err != nil {
		t.Fatal(err)
	}

	req, err := PrepareOnboardState(OnboardArgs{Section: "supervision"}, testOnboardBuild, testOnboardControl, testOnboardConfig)
	if err != nil {
		t.Fatal(err)
	}
	result, err := OnboardState(spine, req, testOnboardBuild, testOnboardControl, testOnboardConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.WorkspaceRoots) != 1 || result.WorkspaceRoots[0].ID != "primary" {
		t.Fatalf("supervision roots=%+v", result.WorkspaceRoots)
	}
	if status, _, err := FinalizeOnboardState(req, result); err != nil || status != "ok" {
		t.Fatalf("supervision finalize status=%q err=%v", status, err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, _, err := FinalizeOnboardState(req, result); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("supervision accepted vanished workspace: %v", err)
	}
}

func TestFinalizeOnboardStateRejectsBlockedHelperReplayRoot(t *testing.T) {
	_, spine := provisionOnboardStateFixture(t)
	req, err := PrepareOnboardState(OnboardArgs{Section: "supervision"},
		testOnboardBuild, testOnboardControl, testOnboardConfig)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), ".ssh", "workspace")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	blocked, err = filepath.EvalSymlinks(blocked)
	if err != nil {
		t.Fatal(err)
	}
	result := OnboardStateResult{
		ProtocolVersion: req.ProtocolVersion, ReleaseBuildID: req.ReleaseBuildID,
		ControlVersion: req.ControlVersion, SpineSchemaID: req.SpineSchemaID,
		ConfigSchemaVersion: req.ConfigSchemaVersion, DeploymentUUID: req.DeploymentUUID,
		Section: req.Section, Status: "ok", Detail: "helper replay",
		WorkspaceRoots: []OnboardWorkspaceRoot{{ID: "corrupt", Root: blocked}},
	}
	if _, _, err := FinalizeOnboardState(req, result); err == nil ||
		!strings.Contains(err.Error(), "blocked address") {
		t.Fatalf("blocked helper replay error = %v", err)
	}
	if _, err := os.Stat(spine); err != nil {
		t.Fatal(err)
	}
}

// TestOnboardStateSupervisionSkipsArchivedProjects pins the archived-Project
// case: `mc __onboard supervision --activate --reconfigure` must not refuse
// the whole deployment because the onboarding smoke's ARCHIVED transient
// Project still carries a workspace_root the smoke teardown deleted. The
// supervision section must resolve only live Project rows.
//
// The fixture is the smoke's real end state, not a hand-shaped one: a live
// Project beside an archived transient whose root is gone. The consumer is
// the real supervision section plus the real host recheck.
func TestOnboardStateSupervisionSkipsArchivedProjects(t *testing.T) {
	_, spine := provisionOnboardStateFixture(t)
	live := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(live, 0o700); err != nil {
		t.Fatal(err)
	}
	writeVisionFixture(t, live)
	project, err := PrepareOnboardState(OnboardArgs{
		Section: "project", Project: "primary", WorkspaceRoot: live,
		AcceptGeneratedSandbox: true,
	}, testOnboardBuild, testOnboardControl, testOnboardConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OnboardState(spine, project, testOnboardBuild, testOnboardControl, testOnboardConfig); err != nil {
		t.Fatal(err)
	}

	// The archived transient, workspace already deleted — exactly what the
	// smoke leaves behind once it self-archives.
	gone := filepath.Join(t.TempDir(), "smoke-workspace")
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dvExec(t, db, `INSERT INTO sandbox_profiles
		(id, workspace_root, artifact_roots, denied_paths)
		VALUES ('smoke-dead', ?, '[]', '[]')`, gone)
	dvExec(t, db, `INSERT INTO projects
		(id, title, sandbox_profile, status)
		VALUES ('smoke-dead', 'Dead smoke', 'smoke-dead', 'archived')`)

	req, err := PrepareOnboardState(OnboardArgs{Section: "supervision"},
		testOnboardBuild, testOnboardControl, testOnboardConfig)
	if err != nil {
		t.Fatal(err)
	}
	result, err := OnboardState(spine, req, testOnboardBuild, testOnboardControl, testOnboardConfig)
	if err != nil {
		t.Fatalf("supervision refused over an archived Project: %v", err)
	}
	if len(result.WorkspaceRoots) != 1 || result.WorkspaceRoots[0].ID != "primary" {
		t.Fatalf("supervision roots=%+v, want only the live Project", result.WorkspaceRoots)
	}
	if status, _, err := FinalizeOnboardState(req, result); err != nil || status != "ok" {
		t.Fatalf("supervision finalize status=%q err=%v", status, err)
	}

	// The live Project is still host-rechecked: archiving is the only thing
	// that excuses a missing root, so removing the LIVE one must still refuse.
	if err := os.RemoveAll(live); err != nil {
		t.Fatal(err)
	}
	if _, _, err := FinalizeOnboardState(req, result); err == nil ||
		!strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("supervision accepted a vanished LIVE workspace: %v", err)
	}
}
