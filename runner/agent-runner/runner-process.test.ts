// runner-process.test.ts — subprocess-level tests for the agent runner's
// pinned PROCESS behaviors, which
// no other suite reaches (the Docker e2e only ever sees exit-0 harness runs):
//
//   - exit-code passthrough: the runner "waits for harness exit, exits with
//     it" and never converts an ordinary harness exit into success;
//   - `mc run register-session` fires exactly once, with the registered
//     (never assumed) locators;
//   - failing `mc` invocations are logged and never fatal (the spine is
//     the authority on staleness).
//
// main() runs as a real `bun` subprocess against a temp run.json and the
// real fake harness (via the test-tier MC_RUN_JSON / MC_HARNESS_CLI
// overrides), with a stub `mc` on PATH that records its argv. Docker-free,
// token-free: part of the fast suite.

import { afterAll, describe, expect, test } from "bun:test";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const MAIN = join(import.meta.dir, "main.ts");
const HARNESS_CLI = join(import.meta.dir, "..", "fake-harness", "cli.ts");

const roots: string[] = [];
afterAll(() => {
  for (const r of roots) rmSync(r, { recursive: true, force: true });
});

interface RunResult {
  exitCode: number;
  stderr: string;
  mcCalls: string[];
}

/** Materialize a temp envelope + behavior + stub mc, run main() for real. */
async function runRunner(opts: {
  behavior: unknown;
  mcExit?: number;
  startExit?: number;
  recoveryExit?: number;
  role?: string;
  subjectID?: number | null;
  delayRegistration?: boolean;
  heartbeatIntervalS?: number;
  stopAfterHeartbeat?: boolean;
  setupCommand?: string;
	  harness?: string;
	  modelBinding?: string;
	  agentRunnerRoutes?: string;
	  claudeAdapterBody?: string;
	  /** Repository preamble inputs: the envelope's expected SHA and a real
	   * host directory standing in for the mounted store. Absent means the
	   * envelope carries no store, exactly like every plan-less production
	   * lane. */
	  inputCommit?: string;
	  workspaceDir?: string;
	  /** Omit to derive the coherent capability from harness/model_binding; pass
	   * null to write an envelope that carries none at all. */
	  runtime?: Record<string, unknown> | null;
}): Promise<RunResult> {
  const root = mkdtempSync(join(tmpdir(), "mc-runner-test-"));
  roots.push(root);
  const sessionDir = join(root, "session");
  const binDir = join(root, "bin");
  // The runner DELETES $HOME/.agents/skills before it installs the run's set
  // (runner/agent-runner/skills.ts), and it deletes even when the envelope
  // carries no skill. This runner is a host process, so an inherited HOME would
  // make that delete land on the developer's own skills. Give it a home here.
  const agentHome = join(root, "home");
  mkdirSync(sessionDir);
  mkdirSync(binDir);
  mkdirSync(agentHome);

  const behaviorPath = join(root, "behavior.json");
  writeFileSync(behaviorPath, JSON.stringify(opts.behavior));

  const runJsonPath = join(root, "run.json");
  writeFileSync(
    runJsonPath,
    JSON.stringify({
      run_id: "proc-test-run",
      tier: "pipeline",
      role: opts.role ?? "worker",
      subject_id: opts.subjectID === undefined ? 7 : opts.subjectID,
      project: "ws-test",
	      harness: opts.harness ?? "fake",
	      model_binding: opts.modelBinding ?? "fake",
	      ...(opts.runtime === null ? {} : {
	        runtime: opts.runtime ?? {
	          base_url: "", channel: "claude",
	          harness: opts.harness ?? "fake", id: opts.modelBinding ?? "fake", model: "",
	        },
	      }),
      mode: "fresh",
      brief: "process-level runner test",
      pool_ids: [],
      heartbeat_interval_s: opts.heartbeatIntervalS ?? 3600,
      harness_config: { behavior: behaviorPath },
      ...(opts.inputCommit === undefined ? {} : { repository: { input_commit: opts.inputCommit } }),
      ...(opts.setupCommand === undefined ? {} : { environment: { image_digest: `sha256:${"a".repeat(64)}`,
        configuration_revision: "test", configuration: { base_digest: `sha256:${"a".repeat(64)}`,
          build_files: [], build_args: {}, setup: { command: opts.setupCommand, directory: "." },
          services: [], variables: [], secrets: [], declarations: [] } } }),
      mounts: {
        session: sessionDir,
        workspace: opts.workspaceDir ?? "/workspace/source",
      },
    }),
  );

  // Stub mc: append argv to a log, exit as scripted (0 or nonzero).
  const mcLog = join(root, "mc-calls.log");
  const mcStub = join(binDir, "mc");
  writeFileSync(
    mcStub,
    `#!/bin/sh\n` +
      (opts.delayRegistration
        ? `if [ "$1 $2" = "run register-session" ]; then sleep 0.2; fi\n`
        : "") +
      `echo "$@" >> "${mcLog}"\n` +
      `if [ "$1 $2" = "run process-started" ]; then exit ${opts.startExit ?? 0}; fi\n` +
      `if [ "$1 $2" = "run model-response" ]; then exit ${opts.recoveryExit ?? opts.mcExit ?? 0}; fi\n` +
      (opts.mcExit ? `echo "stub mc: scripted failure" >&2\nexit ${opts.mcExit}\n` : "exit 0\n"),
  );
  chmodSync(mcStub, 0o755);

  const claudeAdapter = join(root, "claude-adapter.ts");
  if (opts.claudeAdapterBody !== undefined) writeFileSync(claudeAdapter, opts.claudeAdapterBody);

  const proc = Bun.spawn(["bun", MAIN], {
    stdin: opts.setupCommand === undefined ? "ignore" : "pipe",
    stdout: "ignore",
    stderr: "pipe",
    env: {
      ...process.env,
      HOME: agentHome,
      PATH: `${binDir}:${process.env["PATH"] ?? ""}`,
      MC_RUN_JSON: runJsonPath,
      MC_HARNESS_CLI: HARNESS_CLI,
	  ...(opts.claudeAdapterBody !== undefined ? { MC_CLAUDE_ADAPTER: claudeAdapter } : {}),
      ...(opts.agentRunnerRoutes !== undefined ? { MC_AGENT_RUNNER_ROUTES: opts.agentRunnerRoutes } : {}),
    },
  });
  if (opts.setupCommand !== undefined && proc.stdin) { proc.stdin.write("{}\n"); proc.stdin.end(); }
  if (opts.stopAfterHeartbeat) {
    const deadline = Date.now() + 5000;
    while (!existsSync(mcLog) || !readFileSync(mcLog, "utf8").includes("heartbeat")) {
      if (Date.now() >= deadline) { proc.kill(); throw new Error("The runner omitted its heartbeat"); }
      await Bun.sleep(5);
    }
    proc.kill("SIGTERM");
  }
  const [exitCode, stderr] = await Promise.all([
    proc.exited,
    new Response(proc.stderr).text(),
  ]);

  let mcCalls: string[] = [];
  try {
    mcCalls = readFileSync(mcLog, "utf-8").split("\n").filter((l) => l !== "");
  } catch {
    // no mc call ever landed
  }
  return { exitCode, stderr, mcCalls };
}

// The sleep before the terminal step gives the void-fired register-session /
// heartbeat subprocesses time to land in the log before the runner exits.
const crashBehavior = {
  steps: [{ do: "sleep", seconds: 0.5 }, { do: "crash", code: 17 }],
};

describe("agent runner process behaviors", () => {
	  test("refuses an unknown route before invoking any adapter", async () => {
	    const res = await runRunner({
	      behavior: { steps: [{ do: "succeed", output: "must not run" }] },
	      harness: "unknown",
	      modelBinding: "unknown",
	    });
	    expect(res.exitCode).toBe(2);
	    expect(res.stderr).toContain("ships no");
	    expect(res.mcCalls).toHaveLength(1);
    expect(res.mcCalls[0]).toContain("run infra-failure proc-test-run --condition runner execution failed:");
    expect(res.mcCalls[0]).toContain("--source runner --cause unknown");
	  }, 20_000);

	  test("a real route whose envelope carries no capability refuses", async () => {
	    const res = await runRunner({
	      behavior: { steps: [{ do: "succeed", output: "must not run" }] },
	      harness: "codex",
	      modelBinding: "openai-sub",
	      runtime: null,
	    });
	    expect(res.exitCode).toBe(2);
	    expect(res.stderr).toContain("no resolved runtime capability");
	    expect(res.mcCalls).toHaveLength(1);
    expect(res.mcCalls[0]).toContain("run infra-failure proc-test-run --condition runner execution failed:");
    expect(res.mcCalls[0]).toContain("--source runner --cause unknown");
	  }, 20_000);

	  // Design B: the deployment authorizes its one fake adapter to stand in for
	  // named non-fake routes (MC_AGENT_RUNNER_ROUTES), so a production
	  // completion-seal Worker can run through it. Unset stays fake-only above.
	  test("an authorized non-fake route runs through the fake adapter", async () => {
	    const res = await runRunner({
	      behavior: { steps: [{ do: "sleep", seconds: 0.5 }, { do: "succeed", output: "ok" }] },
	      harness: "codex",
	      modelBinding: "openai-sub",
	      agentRunnerRoutes: "codex/openai-sub",
	    });
	    expect(res.exitCode).toBe(0);
	    expect(res.stderr).not.toContain("ships no");
	    expect(res.mcCalls.some((c) => c.startsWith("run register-session"))).toBe(true);
	  }, 20_000);

	  test("a canonical route absent from the fake stand-in list uses its real adapter", async () => {
	    const res = await runRunner({
	      behavior: { steps: [{ do: "succeed", output: "unused" }] },
	      harness: "claude-sdk",
	      modelBinding: "anthropic-sub",
	      agentRunnerRoutes: "codex/openai-sub",
	      claudeAdapterBody: `console.log(JSON.stringify({event:"session-start",session_id:"claude-native",native_file:"native.jsonl"}));`,
	    });
	    expect(res.exitCode).toBe(0);
	    expect(res.stderr).not.toContain("ships no");
	    expect(res.mcCalls).toContain(
	      "run register-session proc-test-run --native-ref claude-native --file native.jsonl",
	    );
	  }, 20_000);

  test("a crash report preserves the harness exit code", async () => {
    const res = await runRunner({ behavior: crashBehavior });
    expect(res.exitCode).toBe(17);
    expect(res.mcCalls.filter((call) => call.startsWith("run infra-failure"))).toEqual([
      "run infra-failure proc-test-run --condition harness exited 17 --source runner --cause unknown",
    ]);
  }, 20_000);

  test("register-session fires exactly once, with the registered locators", async () => {
    const res = await runRunner({ behavior: crashBehavior });
    const regs = res.mcCalls.filter((c) => c.startsWith("run register-session"));
    expect(regs).toEqual([
      "run register-session proc-test-run --native-ref fake-session --file native.jsonl",
    ]);
    // Heartbeating begins only after session-start, immediately.
    expect(res.mcCalls).toContain("heartbeat proc-test-run");
  }, 20_000);

  test("awaits locator registration when the harness exits immediately", async () => {
    const res = await runRunner({
      behavior: { steps: [{ do: "succeed", output: "ok" }] },
      delayRegistration: true,
    });
    expect(res.exitCode).toBe(0);
    expect(res.mcCalls.filter((c) => c.startsWith("run register-session"))).toEqual([
      "run register-session proc-test-run --native-ref fake-session --file native.jsonl",
    ]);
  }, 20_000);

  test("failing mc calls are logged and never fatal; harness exit still passes through", async () => {
    const res = await runRunner({ behavior: crashBehavior, mcExit: 1 });
    expect(res.exitCode).toBe(17);
    expect(res.mcCalls.some((c) => c.startsWith("run register-session"))).toBe(true);
    expect(res.stderr).toContain("exited 1");
  }, 20_000);

  test("a refused process start stops the adapter before it receives the brief", async () => {
    const res = await runRunner({ behavior: crashBehavior, startExit: 1 });
    expect(res.exitCode).toBe(2);
    expect(res.mcCalls).toContain("run process-started proc-test-run");
    expect(res.mcCalls.filter((call) => call.startsWith("run infra-failure"))).toHaveLength(1);
    expect(res.mcCalls.some((call) => call.startsWith("run register-session"))).toBe(false);
  });

  test("the runner reports a zero exit for mc to compare with the run outcome", async () => {
    const res = await runRunner({
      behavior: { steps: [{ do: "sleep", seconds: 0.5 }, { do: "succeed", output: "ok" }] },
    });
    expect(res.exitCode).toBe(0);
    expect(res.mcCalls.filter((call) => call.startsWith("run infra-failure"))).toEqual([
      "run infra-failure proc-test-run --condition harness exited 0 --source runner --cause unknown",
    ]);
  }, 20_000);
  test("a proposal run reports its failure immediately", async () => {
    const res = await runRunner({ behavior: crashBehavior, subjectID: null });
    expect(res.exitCode).toBe(17);
    expect(res.mcCalls.some((call) => call.startsWith("run infra-failure"))).toBe(true);
  }, 20_000);

  test("an adapter protocol failure supplies its observation without a cause diagnosis", async () => {
    const res = await runRunner({
      behavior: { steps: [] },
      harness: "claude-sdk",
      modelBinding: "claude",
      claudeAdapterBody: `console.log(JSON.stringify({event:"session-start",session_id:"session",native_file:"../unsafe"}));`,
    });
    expect(res.exitCode).toBe(2);
    expect(res.mcCalls).toEqual([
      "run process-started proc-test-run",
      "run infra-failure proc-test-run --condition adapter protocol failed; harness exited 0 --source runner --cause unknown",
    ]);
  }, 20_000);

});

// The runner restores the recorded commit before the adapter starts.
// A failed recovery reports an infrastructure failure for the assigned run.
describe("repository preamble", () => {
  const run = (argv: string[], cwd: string) => {
    const proc = Bun.spawnSync(argv, { cwd, env: process.env, stdout: "pipe", stderr: "pipe" });
    if (proc.exitCode !== 0) {
      throw new Error(`${argv.join(" ")} exited ${proc.exitCode}: ${proc.stderr.toString()}`);
    }
    return proc.stdout.toString().trim();
  };

  /** A real store: one commit, then debris (an untracked file and a moved
   * tracked file) the preamble must wipe. */
  const gitStore = () => {
    const store = mkdtempSync(join(tmpdir(), "mc-runner-store-"));
    roots.push(store);
    run(["git", "init", "-q", "-b", "main"], store);
    run(["git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "base"], store);
    writeFileSync(join(store, "kept.txt"), "committed\n");
    run(["git", "add", "kept.txt"], store);
    run(["git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "kept"], store);
    const head = run(["git", "rev-parse", "HEAD"], store);
    writeFileSync(join(store, "kept.txt"), "prior-round debris\n");
    writeFileSync(join(store, "debris.txt"), "untracked debris\n");
    return { store, head };
  };

  test("the preamble pins the repository to the input commit before the harness runs", async () => {
    const { store, head } = gitStore();
    const res = await runRunner({
      behavior: { steps: [{ do: "sleep", seconds: 0.5 }, { do: "succeed", output: "ok" }] },
      inputCommit: head,
      workspaceDir: store,
    });
    expect(res.exitCode).toBe(0);
    // The harness ran — the preamble's success never blocks the launch.
    expect(res.mcCalls.some((c) => c.startsWith("run register-session"))).toBe(true);
    expect(res.mcCalls.some((c) => c.startsWith("task block"))).toBe(false);
    // Debris is gone and the tracked tree sits exactly on the input commit.
    expect(existsSync(join(store, "debris.txt"))).toBe(false);
    expect(readFileSync(join(store, "kept.txt"), "utf-8")).toBe("committed\n");
    expect(run(["git", "rev-parse", "HEAD"], store)).toBe(head);
    expect(run(["git", "status", "--porcelain"], store)).toBe("");
  }, 20_000);

  test("dependency preparation keeps its lease and preserves its installed files", async () => {
    const { store, head } = gitStore();
    const result = await runRunner({
      behavior: { steps: [{ do: "succeed", output: "ok" }] },
      inputCommit: head, workspaceDir: store, heartbeatIntervalS: 0.03,
      setupCommand: "test ! -f debris.txt\nsleep 0.2\nmkdir -p node_modules/example\nprintf ready > node_modules/example/index.js",
    });
    expect(result.exitCode).toBe(0);
    const processStart = result.mcCalls.indexOf("run process-started proc-test-run");
    expect(processStart).toBeGreaterThan(1);
    expect(result.mcCalls.slice(0, processStart).filter(call => call === "heartbeat proc-test-run").length).toBeGreaterThan(1);
    expect(readFileSync(join(store, "node_modules/example/index.js"), "utf8")).toBe("ready");
    expect(existsSync(join(store, "debris.txt"))).toBe(false);
  }, 20_000);

  test("a missing repository reports its condition before the runner exits", async () => {
    const empty = mkdtempSync(join(tmpdir(), "mc-runner-nostore-"));
    roots.push(empty);
    const res = await runRunner({
      behavior: { steps: [{ do: "succeed", output: "must not run" }] },
      inputCommit: "a".repeat(40),
      workspaceDir: empty,
    });
    expect(res.exitCode).toBe(2);
    const reports = res.mcCalls.filter((c) => c.startsWith("run infra-failure proc-test-run --condition"));
    expect(reports).toHaveLength(1);
    expect(reports[0]).toContain("repository preamble refused");
    expect(reports[0]).toContain("not a git work tree");
    expect(reports[0]).toContain("--source runner --cause unknown");
    expect(res.mcCalls.some((c) => c.startsWith("task block"))).toBe(false);
    expect(res.mcCalls.some((c) => c.startsWith("run register-session"))).toBe(false);
  }, 20_000);

  test("an unresolvable input commit reports a failure and preserves the repository", async () => {
    const { store } = gitStore();
    const missing = "b".repeat(40);
    const res = await runRunner({
      behavior: { steps: [{ do: "succeed", output: "must not run" }] },
      inputCommit: missing,
      workspaceDir: store,
    });
    expect(res.exitCode).toBe(2);
    const reports = res.mcCalls.filter((c) => c.startsWith("run infra-failure proc-test-run --condition"));
    expect(reports).toHaveLength(1);
    expect(reports[0]).toContain(missing);
    expect(reports[0]).toContain("does not resolve to a commit");
    expect(res.mcCalls.some((c) => c.startsWith("task block"))).toBe(false);
    expect(res.mcCalls.some((c) => c.startsWith("run register-session"))).toBe(false);
    // A refused preamble mutates nothing: the debris is still there for the
    // operator to inspect.
    expect(existsSync(join(store, "debris.txt"))).toBe(true);
  }, 20_000);

  // The run has the role `scribe` and no subject. The runner checks the
  // repository against the start commit and changes no file.
  describe("the Scribe run of a wiki update", () => {
    test("the runner keeps the repository and names the run in each command", async () => {
      const { store, head } = gitStore();
      writeFileSync(join(store, "kept.txt"), "committed\n");
      const adapterDir = mkdtempSync(join(tmpdir(), "mc-runner-adapter-"));
      roots.push(adapterDir);
      const seen = join(adapterDir, "input.json");
      const res = await runRunner({
        behavior: {},
        role: "scribe",
        subjectID: null,
        harness: "claude-sdk",
        modelBinding: "test",
        inputCommit: head,
        workspaceDir: store,
        claudeAdapterBody: `
          await Bun.stdin.text();
          await Bun.write(${JSON.stringify(seen)}, JSON.stringify({
            argv: process.argv.slice(2), role: process.env.MC_ROLE, subject: process.env.MC_SUBJECT_ID,
          }));
          console.log(JSON.stringify({ event: "session-start", session_id: "wiki-session", native_file: "native.jsonl" }));
          console.log(JSON.stringify({ event: "model-response" }));
          await Bun.sleep(500);
        `,
      });
      expect(res.exitCode).toBe(0);
      expect([...res.mcCalls].sort()).toEqual([
        "heartbeat proc-test-run",
        "run infra-failure proc-test-run --condition harness exited 0 --source runner --cause unknown",
        "run model-response proc-test-run",
        "run process-started proc-test-run",
        "run register-session proc-test-run --native-ref wiki-session --file native.jsonl",
      ]);
      expect(JSON.parse(readFileSync(seen, "utf-8"))).toEqual({
        argv: [
          "--session-dir", expect.any(String), "--workspace", store,
          "--openwiki", "--openwiki-input-commit", head, "--channel", "claude",
        ],
        role: "scribe",
        subject: "",
      });
      expect(readFileSync(join(store, "debris.txt"), "utf-8")).toBe("untracked debris\n");
      expect(run(["git", "rev-parse", "HEAD"], store)).toBe(head);
    }, 20_000);

    test("a repository that differs from the start commit stops the run before the adapter", async () => {
      const { store, head } = gitStore();
      const res = await runRunner({
        behavior: { steps: [{ do: "succeed", output: "must not run" }] },
        role: "scribe",
        subjectID: null,
        inputCommit: head,
        workspaceDir: store,
      });
      expect(res.exitCode).toBe(2);
      expect(res.mcCalls).toEqual([
        "run infra-failure proc-test-run --condition repository preamble refused: " +
          "The Scribe repository differs from its start commit. --source runner --cause unknown",
      ]);
      expect(readFileSync(join(store, "kept.txt"), "utf-8")).toBe("prior-round debris\n");
      expect(existsSync(join(store, "debris.txt"))).toBe(true);
    }, 20_000);
  });

  // The skip case needs no dedicated test: every other test in this file runs
  // an envelope without `repository` against a workspace that does not
  // exist on the host, and would fail on the preamble if it ever ran.
});

for (const afterSession of [false, true]) {
  test(`the runner retains an adapter diagnosis ${afterSession ? "after" : "before"} session registration`, async () => {
    const session = 'console.log(JSON.stringify({ event: "session-start", session_id: "native-id", native_file: "native.jsonl" }));';
    const failure = 'console.log(JSON.stringify({ event: "infrastructure-failure", condition: "OpenWiki startup deadline expired", evidence: "Server: openwiki; Last connection status: pending; Harness error: module unavailable; Outcome: startup timeout" }));';
    const result = await runRunner({
      behavior: {}, harness: "claude-sdk", modelBinding: "test", delayRegistration: true,
      claudeAdapterBody: `${afterSession ? session : ""}\n${failure}\n${afterSession ? "" : session}\nprocess.exit(1);`,
    });
    expect(result.exitCode).toBe(1);
    expect(result.mcCalls.filter((call) => call.includes("register-session"))).toHaveLength(1);
    expect(result.mcCalls.filter((call) => call.startsWith("heartbeat"))).toHaveLength(1);
    const reports = result.mcCalls.filter((call) => call.includes("infra-failure"));
    expect(reports).toHaveLength(1);
    expect(reports[0]).toContain("--condition OpenWiki startup deadline expired");
    expect(reports[0]).toContain("--cause unknown");
    expect(result.mcCalls.join("\n")).toContain("Last connection status: pending");
    expect(result.mcCalls.join("\n")).toContain("Harness error: module unavailable");
    expect(result.mcCalls.join("\n")).toContain("Adapter process exit: 1");
  });
}


test("the runner reports a network error without provider facts", async () => {
  const session = 'console.log(JSON.stringify({ event: "session-start", session_id: "native-id", native_file: "native.jsonl" }));';
  const failure = 'console.log(JSON.stringify({ event: "infrastructure-failure", condition: "The harness reported server_error. API Error: Connection refused (ECONNREFUSED)", evidence: "Harness stderr: unknown", network_error: "connect", provider_error_code: "server_error" }));';
  const result = await runRunner({
    behavior: {}, harness: "claude-sdk", modelBinding: "test",
    claudeAdapterBody: `${session}\n${failure}\nprocess.exit(1);`,
  });
  expect(result.mcCalls.filter((call) => call.includes("infra-failure"))).toHaveLength(1);
  expect(result.mcCalls.join("\n")).toContain("--network-error connect");
  expect(result.mcCalls.join("\n")).not.toContain("--provider-error-code");
});

test("the runner supplies heartbeat service throughout the adapter wait", async () => {
  const result = await runRunner({
    behavior: {}, harness: "claude-sdk", modelBinding: "test", heartbeatIntervalS: 0.03,
    claudeAdapterBody: `
      console.log(JSON.stringify({ event: "session-start", session_id: "native-id", native_file: "native.jsonl" }));
      await Bun.sleep(160);
      console.log(JSON.stringify({ event: "infrastructure-failure", condition: "OpenWiki startup deadline expired", evidence: "Server: openwiki" }));
      process.exit(1);
    `,
  });
  expect(result.mcCalls.filter((call) => call.startsWith("heartbeat")).length).toBeGreaterThan(1);
  expect(result.mcCalls.filter((call) => call.includes("register-session"))).toHaveLength(1);
  expect(result.mcCalls.filter((call) => call.includes("infra-failure"))).toHaveLength(1);
});

test("the runner forwards a stop during startup without an infrastructure report", async () => {
  const result = await runRunner({
    behavior: {}, harness: "claude-sdk", modelBinding: "test", stopAfterHeartbeat: true,
    claudeAdapterBody: `
      process.on("SIGTERM", () => process.exit(143));
      console.log(JSON.stringify({ event: "session-start", session_id: "native-id", native_file: "native.jsonl" }));
      await Bun.sleep(60_000);
    `,
  });
  expect(result.exitCode).toBe(143);
  expect(result.mcCalls.filter((call) => call.includes("register-session"))).toHaveLength(1);
  expect(result.mcCalls.filter((call) => call.includes("infra-failure"))).toHaveLength(0);
});

test("the runner reports recovery before the adapter ends and preserves a later failure", async () => {
  const result = await runRunner({
    behavior: {}, harness: "claude-sdk", modelBinding: "fixture",
    claudeAdapterBody: `
      await Bun.stdin.text();
      console.log(JSON.stringify({event:"model-response"}));
      await Bun.sleep(50);
      console.log(JSON.stringify({event:"model-response"}));
      process.exit(1);
    `,
  });
  expect(result.mcCalls.filter(call => call === "run model-response proc-test-run").length).toBe(1);
  expect(result.exitCode).toBe(1);
  expect(result.mcCalls.findIndex(call => call.startsWith("run model-response"))).toBeLessThan(result.mcCalls.findIndex(call => call.startsWith("run infra-failure")));
});

test("a failed recovery report preserves the adapter outcome", async () => {
  const result = await runRunner({
    behavior: {}, harness: "claude-sdk", modelBinding: "fixture", recoveryExit: 2,
    claudeAdapterBody: `await Bun.stdin.text(); console.log(JSON.stringify({event:"session-start",session_id:"fixture",native_file:"native.jsonl"})); console.log(JSON.stringify({event:"model-response"})); await Bun.sleep(30); process.exit(0);`,
  });
  expect(result.mcCalls).toContain("run model-response proc-test-run");
  expect(result.stderr).toContain("The connection recovery report failed.");
  expect(result.exitCode).toBe(0);
});
