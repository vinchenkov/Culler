package main_test

// The agent-facing `mc` surface carries no development or release mechanics.
//
// Release application, install-time checks, acceptance walks, and destructive
// provisioning live behind the front doors (`install.sh`, `redeploy.sh`,
// `uninstall.sh`) and the private `__` namespace they drive. An agent reading
// the public surface must find only operational verbs and the operator-owned
// setup sections — and every moved `__` verb must refuse any run.json caller.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func agentSurfaceHome(t *testing.T) (string, string, []string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "mc-home")
	spine := filepath.Join(home, "spine.db")
	return home, spine, []string{"MC_HOME=" + home, "MC_SPINE=" + spine}
}

// The four dev-release top-level verbs are gone from the public dispatcher:
// an agent that names them meets "unknown verb", exactly like any other verb
// the surface never had.
func TestAgentSurfaceDropsReleaseVerbs(t *testing.T) {
	_, spine, env := agentSurfaceHome(t)
	for _, argv := range [][]string{
		{"init", "--spine", spine, "--project", "ws-x", "--workspace-root", "/tmp/ws-x"},
		{"reset", "--confirm"},
		{"uninstall", "--dry-run"},
		{"acceptance", "s7", "prepare", "--resource-saver-disabled"},
	} {
		res := runMC(t, env, "", argv...)
		if res.code != 2 || !strings.Contains(res.stderr, "unknown verb") {
			t.Fatalf("mc %v = code %d stderr %q, want unknown-verb usage error", argv, res.code, res.stderr)
		}
	}
	if _, err := os.Stat(spine); !os.IsNotExist(err) {
		t.Fatalf("a refused dev-release verb created the spine (err %v)", err)
	}
}

// Public `mc onboard` narrows to the operator-owned setup sections. The
// install-time and release-application sections, the smoke, and the release/
// activation flags are refused with a usage error that never advertises the
// private namespace.
func TestAgentOnboardListsOnlySetupSections(t *testing.T) {
	home, _, env := agentSurfaceHome(t)

	bare := runMC(t, env, "", "onboard")
	if bare.code != 2 || !strings.Contains(bare.stderr, "home|runtime-auth|routing|project|tunables|surfaces") {
		t.Fatalf("bare onboard = code %d stderr %q, want the setup-section usage", bare.code, bare.stderr)
	}
	for _, banned := range []string{"preflight", "container", "supervision", "verify", "__"} {
		if strings.Contains(bare.stderr, banned) {
			t.Fatalf("public onboard usage advertises %q: %q", banned, bare.stderr)
		}
	}

	for _, section := range []string{"preflight", "container", "supervision", "verify", "bogus-section"} {
		res := runMC(t, env, "", "onboard", section)
		if res.code != 2 || !strings.Contains(res.stderr, "unknown onboarding section") {
			t.Fatalf("onboard %s = code %d stderr %q, want unknown-section usage error", section, res.code, res.stderr)
		}
		if strings.Contains(res.stderr, "supervision|verify") {
			t.Fatalf("onboard %s usage lists removed sections: %q", section, res.stderr)
		}
	}

	for name, argv := range map[string][]string{
		"smoke":          {"onboard", "--smoke"},
		"smoke-approve":  {"onboard", "--smoke", "--smoke-approve-task", "3"},
		"release-source": {"onboard", "home", "--release-source", "/tmp/r", "--host-release-source", "/tmp/h"},
		"activate":       {"onboard", "--activate"},
		"reconfigure":    {"onboard", "--reconfigure"},
	} {
		res := runMC(t, env, "", argv...)
		if res.code != 2 {
			t.Fatalf("%s: mc %v = code %d stderr %q, want usage refusal", name, argv, res.code, res.stderr)
		}
	}

	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("a refused public onboard created MC_HOME (err %v)", err)
	}
}

// The moved mechanics still work, under their private spellings, for the
// front doors: `__onboard home` provisions, `__init` provisions a skeleton
// spine, `__reset` keeps its confirmation gate and snapshot-first rule.
func TestFrontDoorNamespaceCarriesTheMechanics(t *testing.T) {
	_, _, env := agentSurfaceHome(t)
	res := runMC(t, env, "", "__onboard", "home")
	if res.code != 0 {
		t.Fatalf("__onboard home = code %d stderr %q", res.code, res.stderr)
	}

	bareHome := t.TempDir()
	if err := os.Chmod(bareHome, 0o700); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(bareHome, "spine.db")
	workspace := gitFixtureWorkspace(t)
	provisioned := runMC(t, []string{"MC_HOME=" + bareHome}, "", "__init", "--spine", bare,
		"--project", "ws-front", "--workspace-root", workspace)
	if provisioned.code != 0 || provisioned.json["deployment_uuid"] == "" {
		t.Fatalf("__init = code %d stderr %q", provisioned.code, provisioned.stderr)
	}

	spine := initSpine(t)
	unconfirmed := runMC(t, spineEnv(spine), "", "__reset")
	if unconfirmed.code != 1 || !strings.Contains(unconfirmed.stderr, "--confirm") {
		t.Fatalf("unconfirmed __reset = code %d stderr %q", unconfirmed.code, unconfirmed.stderr)
	}
	confirmed := runMC(t, spineEnv(spine), "", "__reset", "--confirm")
	if confirmed.code != 0 || confirmed.json["reset"] != true || confirmed.json["snapshot"] == "" {
		t.Fatalf("__reset --confirm = code %d json %v stderr %q", confirmed.code, confirmed.json, confirmed.stderr)
	}
}

// Every moved verb is refused outside host scope: a pipeline or Collie
// container that somehow names a `__` mechanic is turned away on identity,
// before any state is touched.
func TestFrontDoorNamespaceRefusesContainerCallers(t *testing.T) {
	for _, argv := range [][]string{
		{"__onboard", "home"},
		{"__init", "--spine", "/tmp/forged.db", "--project", "f", "--workspace-root", "/tmp/f"},
		{"__reset", "--confirm"},
		{"__uninstall", "--confirm"},
	} {
		home, spine, _ := agentSurfaceHome(t)
		envDenied := append(runJSONEnv(t, spine, "r-x", "pipeline", "worker"), "MC_HOME="+home)
		res := runMC(t, envDenied, "", argv...)
		if res.code != 1 || !strings.Contains(res.stderr, "host scope only") {
			t.Fatalf("mc %v under run.json = code %d stderr %q, want host-scope refusal", argv, res.code, res.stderr)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatalf("denied %v created MC_HOME (err %v)", argv, err)
		}
	}
}

func TestPublicOnboardHomeNeverResets(t *testing.T) {
	_, spine, env := agentSurfaceHome(t)
	if res := runMC(t, env, "", "__onboard", "home"); res.code != 0 {
		t.Fatalf("provision failed: %s", res.stderr)
	}
	db := openDB(t, spine)
	if _, err := db.Exec(`UPDATE meta SET schema_id = 'incompatible' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	res := runMC(t, env, "", "onboard", "home")
	if res.code != 1 || !strings.Contains(res.stderr, "redeploy.sh") {
		t.Fatalf("public home on a incompatible spine = code %d stderr %q, want a redeploy.sh refusal", res.code, res.stderr)
	}
}
