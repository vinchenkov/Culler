//go:build docker_boundary

package boundarydocker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

// A separate image permits checks without replacement of the deployed image.
var prodImage = func() string {
	if image := os.Getenv("MC_BOUNDARY_IMAGE"); image != "" {
		return image
	}
	return "mc-prod"
}()

var fakeImage = func() string {
	if image := os.Getenv("MC_BOUNDARY_FAKE_IMAGE"); image != "" {
		return image
	}
	return "mc-fake-e2e"
}()

func requireProdImage(t *testing.T) {
	t.Helper()
	requireDocker(t)
	out, err := exec.Command("docker", "image", "inspect", prodImage).CombinedOutput()
	if err != nil {
		t.Skipf("production image %q not built — run runner/image/build-prod.sh (%s)",
			prodImage, strings.TrimSpace(string(out)))
	}
}

// ---------------------------------------------------------------------------
// The production image contains no fake route and has its own untagged
// build.
//
// Until this file, the ONLY image in the tree was mc-fake-e2e, built with
// `-tags test_fake_routing`. That tag is what admits the fake harness family
// into the routing registry, so the requirement was not merely untested — the
// artifact it describes did not exist.
//
// The property is proved by BEHAVIOUR rather than by inspecting build metadata:
// a production binary must be unable to resolve a `fake` route at all, because
// an unparsable route is what denies fake spawn and mount authority.
// ---------------------------------------------------------------------------

func TestProductionImageHasNoFakeRouteDockerBoundary(t *testing.T) {
	requireProdImage(t)

	home := sharedTempDir(t)
	if _, err := operatorconfig.Ensure(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	// Every role bound to the fake family. A production binary must not be able
	// to resolve ANY of them.
	routing := "# routing\n| role | harness | binding |\n| --- | --- | --- |\n" +
		"| worker | fake | fake |\n| strategist | fake | fake |\n| arbiter | fake | fake |\n" +
		"| verifier | fake | fake |\n| scribe | fake | fake |\n| packager | fake | fake |\n| collie | fake | fake |\n"
	if err := os.WriteFile(filepath.Join(home, "routing.md"), []byte(routing), 0o644); err != nil {
		t.Fatal(err)
	}

	probe := func(image string) string {
		t.Helper()
		out, _ := dockerRun(t,
			"-v", home+":/fixture:ro", "-e", "MC_HOME=/mc/home",
			image, "sh", "-ec", `mkdir -p /mc/home
cp /fixture/config.toml /fixture/routing.md /mc/home/
chown -R 10001:10001 /mc/home
chmod 0700 /mc/home
chmod 0600 /mc/home/config.toml
exec node -e 'process.setgid(10001); process.setuid(10001); const result = require("node:child_process").spawnSync("mc", ["doctor"], {stdio: "inherit"}); process.exit(result.status ?? 2)'`)
		return out
	}

	// The production binary must refuse the fake BINDING itself. This is the
	// property that denies fake spawn and mount authority: an unresolvable
	// binding cannot be routed to, so no fake harness can ever be selected.
	prod := findingByName(t, doctorFindings(t, probe(prodImage)), "routing")
	detail, _ := prod["detail"].(string)
	if prod["status"] != "fail" || !strings.Contains(detail, `unresolved binding "fake"`) {
		t.Fatalf("the production image did not reject the fake binding: %v", prod)
	}

	// The control, and it is doing real work rather than decorating: the
	// fake-route image must get PAST the binding and fail on something else
	// entirely. Without this arm, a fixture that was malformed for some
	// unrelated reason would make the assertion above pass while proving
	// nothing about fake routes at all.
	fake := findingByName(t, doctorFindings(t, probe(fakeImage)), "routing")
	if fake["status"] != "ok" {
		t.Fatalf("the fake image did not accept the complete route configuration: %v", fake)
	}
}

func TestProductionImageCarriesPinnedRealAdapterRuntime(t *testing.T) {
	requireProdImage(t)
	out, err := dockerRun(t, prodImage, "sh", "-c",
		"/app/node_modules/@openai/codex-linux-arm64/vendor/aarch64-unknown-linux-musl/bin/codex --version; "+
			"test -f /app/node_modules/@anthropic-ai/claude-agent-sdk/package.json; "+
			"bun -e 'const p=require(\"/app/node_modules/@anthropic-ai/claude-agent-sdk/package.json\"); console.log(p.version)'")
	if err != nil {
		t.Fatalf("inspect production adapter runtime: %v\n%s", err, out)
	}
	if !strings.Contains(out, "0.156.1") || !strings.Contains(out, "0.3.280") {
		t.Fatalf("production adapter runtime is not pinned:\n%s", out)
	}
}

// The pinned binary validates the shipped arguments and rejects an invalid control.
func TestProductionCodexShippedArgvIsAcceptedByThePinnedBinary(t *testing.T) {
	requireProdImage(t)

	runnerSrc := stageRunnerSource(t)

	// probe runs the shipped argv — optionally with one extra config key spliced
	// in — against the real pinned codex, and returns what codex said about it.
	probe := func(extra string) string {
		t.Helper()
		script := `import { codexArgv } from "/app/src/agent-runner/adapters/codex.ts";
const argv = codexArgv("fresh");
const extra = ` + strconv.Quote(extra) + `;
// Splice before the trailing "-" prompt sentinel, which must stay last.
if (extra !== "") argv.splice(argv.length - 1, 0, "--config", extra);
const p = Bun.spawnSync(argv, {
  stdin: new TextEncoder().encode("noop"), stdout: "pipe", stderr: "pipe", timeout: 20_000,
});
console.log(new TextDecoder().decode(p.stdout) + new TextDecoder().decode(p.stderr));`
		out, _ := dockerRun(t, "--user", "10002:10002",
			"-v", runnerSrc+":/app/src:ro", prodImage, "bun", "-e", script)
		return out
	}

	// The shipped argv must survive config parsing. It cannot reach a model here
	// (dockerRun forces --network none and no auth is mounted) and does not need
	// to: --strict-config rejects long before any of that. Codex retries the
	// network until the probe time limit, so a started thread proves acceptance.
	if out := probe(""); !strings.Contains(out, `"type":"thread.started"`) ||
		strings.Contains(out, "unknown configuration field") ||
		strings.Contains(out, "Error loading config") {
		t.Fatalf("the pinned codex rejected the shipped adapter argv:\n%s", out)
	}

	// The control, load-bearing rather than decorative: if codex ever stopped
	// phrasing config errors the way the arm above matches on, that arm would
	// pass while proving nothing at all. A key we KNOW is bogus must still fail.
	if out := probe(`shell_environment_policy.exclude_patterns=["*TOKEN*"]`); !strings.Contains(
		out, "unknown configuration field") {
		t.Fatalf("a known-bogus config key was NOT refused, so the acceptance arm "+
			"above proves nothing about --strict-config:\n%s", out)
	}
}

// TestProductionCodexNoInnerSandboxLeavesTheSetuidGateIntact pins the reason the
// inner sandbox came off. Under no_new_privs the kernel ignores the setuid bit
// and `mc` cannot open the spine; with the sandbox gone the container's own
// boundary is what confines the agent. Assert the kernel precondition directly
// rather than trusting the flag that is supposed to preserve it.
func TestProductionCodexNoInnerSandboxLeavesTheSetuidGateIntact(t *testing.T) {
	requireProdImage(t)
	out, err := dockerRun(t, "--user", "10002:10002", prodImage,
		"sh", "-c", "grep NoNewPrivs /proc/self/status")
	if err != nil {
		t.Fatalf("read no_new_privs: %v\n%s", err, out)
	}
	if !strings.Contains(out, "NoNewPrivs:\t0") {
		t.Fatalf("no_new_privs is already set for an unsandboxed agent shell, so the "+
			"setuid gate is void and `mc` cannot reach the spine:\n%s", out)
	}
}

func TestProductionImageGeneralMCIsSetuidDockerBoundary(t *testing.T) {
	requireProdImage(t)
	out, err := dockerRun(t, "--user", "10002:10002", prodImage,
		"sh", "-c", "stat -c '%u:%g %a' /opt/culler-runtime/libexec/mc-real")
	if err != nil {
		t.Fatalf("inspect privileged mc: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "10001:10001 6755" {
		t.Fatalf("privileged mc ownership/mode = %q, want 10001:10001 6755", strings.TrimSpace(out))
	}
}

// ---------------------------------------------------------------------------
// The baked merge program.
//
// The merge container never touches the spine: it runs the fixed `mc-land`
// shell program over the mounted envelope, and the resident records the
// result from the host. What the image must therefore guarantee is that the
// program is BAKED — root-owned, not agent-writable, executable by the merge
// uid — and that it fails closed with a diagnostic rather than doing partial
// work when its inputs are missing.
// ---------------------------------------------------------------------------

func TestMergeProgramIsBakedAndFailsClosedDockerBoundary(t *testing.T) {
	requireProdImage(t)

	t.Run("mc-land is baked root-owned and executable by the merge uid", func(t *testing.T) {
		out, err := dockerRun(t, "--user", "10002:10002", prodImage,
			"sh", "-c", "ls -l /usr/local/bin/mc-land && test -x /usr/local/bin/mc-land && echo EXECUTABLE")
		if err != nil {
			t.Fatalf("probe: %v\n%s", err, out)
		}
		if !strings.Contains(out, "EXECUTABLE") {
			t.Fatalf("mc-land is not executable by uid 10002 inside the image:\n%s", out)
		}
		if !strings.Contains(out, "root") {
			t.Fatalf("mc-land is not root-owned; an agent-writable merge program is a merge-time code injection:\n%s", out)
		}
	})

	t.Run("a bare invocation refuses at usage before touching anything", func(t *testing.T) {
		out, err := dockerRun(t, "--user", "10002:10002", prodImage, "mc-land")
		if err == nil {
			t.Fatalf("mc-land with no operation exited zero:\n%s", out)
		}
		if !strings.Contains(out, "usage: mc-land") {
			t.Fatalf("bare mc-land refused without its usage diagnostic:\n%s", out)
		}
	})

	t.Run("an unknown operation refuses at usage", func(t *testing.T) {
		out, err := dockerRun(t, "--user", "10002:10002", prodImage,
			"mc-land", "land", "/mc/repository.json")
		if err == nil {
			t.Fatalf("mc-land ran an unknown operation:\n%s", out)
		}
		if !strings.Contains(out, "prepare") {
			t.Fatalf("the refusal does not name the accepted operations:\n%s", out)
		}
	})

	t.Run("an absent envelope refuses with a diagnostic naming it", func(t *testing.T) {
		// No /mc/repository.json mount at all: the operation must fail closed
		// and name the missing envelope, not proceed into git discovery noise.
		out, err := dockerRun(t, "--user", "10002:10002", prodImage,
			"mc-land", "apply", "/mc/repository.json")
		if err == nil {
			t.Fatalf("mc-land applied with no mounted envelope:\n%s", out)
		}
		if !strings.Contains(out, "/mc/repository.json") {
			t.Fatalf("the refusal does not name the missing envelope:\n%s", out)
		}
	})
}
