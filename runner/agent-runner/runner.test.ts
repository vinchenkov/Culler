// runner.test.ts — unit tests for the agent runner's pure helpers. The
// process flow (exit-code passthrough, register-session-once, nonfatal mc
// failures) is covered subprocess-level by runner-process.test.ts; the
// Docker e2e exercises only exit-0 harness runs.
// Docker-free, token-free: part of the fast suite.

import { describe, expect, test } from "bun:test";
import {
  adapterInvocation, harnessEnv, makeLineSplitter, nativeLocator, runStorePreamble,
  type PreambleExec, type RunEnvelope, type RunEnvelopeRuntime,
} from "./main";

/** The resolved capability config.toml carried into the envelope. Tests state
 * only the identity that matters and inherit explicit empties elsewhere, so no
 * fixture can prove that a PARTIAL capability launches. */
const runtime = (
  harness: string,
  id: string,
  over: Partial<RunEnvelopeRuntime> = {},
): RunEnvelopeRuntime => ({
  base_url: "", channel: harness === "codex" ? "codex" : "claude", harness, id, model: "", ...over,
});

/** An envelope on a real route, carrying the capability that describes it. */
const routed = (
  harness: string,
  id: string,
  over: Partial<RunEnvelope> = {},
  capability: Partial<RunEnvelopeRuntime> = {},
): RunEnvelope => envelope({
  harness, model_binding: id, harness_config: undefined,
  runtime: runtime(harness, id, capability), ...over,
});

const envelope = (over: Partial<RunEnvelope>): RunEnvelope => ({
  run_id: "abc123",
  role: "worker",
	harness: "fake",
	model_binding: "fake",
  subject_id: 42,
  pool_ids: [],
  heartbeat_interval_s: 1,
  brief: "b",
  harness_config: { behavior: "/mc/behaviors/worker.json" },
  mounts: { session: "/mc/session" },
  ...over,
});

describe("harnessEnv (the env-interpolation channel)", () => {
  test("exports run id, role, subject", () => {
    const env = harnessEnv(envelope({}), {});
    expect(env["MC_RUN_ID"]).toBe("abc123");
    expect(env["MC_ROLE"]).toBe("worker");
    expect(env["MC_SUBJECT_ID"]).toBe("42");
  });

  test("null subject exports as the empty string", () => {
    const env = harnessEnv(envelope({ subject_id: null, role: "strategist(propose)" }), {});
    expect(env["MC_SUBJECT_ID"]).toBe("");
    expect(env["MC_ROLE"]).toBe("strategist(propose)");
  });

  test("pool ids are comma-joined; absent pool is empty", () => {
    expect(harnessEnv(envelope({ pool_ids: [7, 8, 9] }), {})["MC_POOL_IDS"]).toBe("7,8,9");
    expect(harnessEnv(envelope({ pool_ids: undefined }), {})["MC_POOL_IDS"]).toBe("");
  });

  test("base env (MC_SPINE et al.) passes through; MC_* keys win over base", () => {
    const env = harnessEnv(envelope({}), {
      MC_SPINE: "/mc/spine/spine.db",
      PATH: "/usr/local/bin",
      MC_ROLE: "forged",
    });
    expect(env["MC_SPINE"]).toBe("/mc/spine/spine.db");
    expect(env["PATH"]).toBe("/usr/local/bin");
    expect(env["MC_ROLE"]).toBe("worker");
  });
});

describe("makeLineSplitter", () => {
  test("reassembles lines across arbitrary chunk boundaries", () => {
    const got: string[] = [];
    const feed = makeLineSplitter((l) => got.push(l));
    feed('{"event":"sess');
    feed('ion-start"}\n{"event":"turn-');
    feed('complete"}\n');
    expect(got).toEqual(['{"event":"session-start"}', '{"event":"turn-complete"}']);
  });

  test("a trailing partial line is held, never emitted", () => {
    const got: string[] = [];
    const feed = makeLineSplitter((l) => got.push(l));
    feed("a\nb");
    expect(got).toEqual(["a"]);
  });

  test("several lines in one chunk all emit, in order", () => {
    const got: string[] = [];
    const feed = makeLineSplitter((l) => got.push(l));
    feed("1\n2\n3\n");
    expect(got).toEqual(["1", "2", "3"]);
  });
});

describe("closed adapter selection", () => {
  test("both Scribe routes receive the OpenWiki server switch", () => {
    for (const harness of ["codex", "claude-sdk"]) {
      const run = routed(harness, "configured-binding", { role: "scribe" });
      expect(adapterInvocation(run).argv).toContain("--openwiki");
      expect(adapterInvocation({ ...run, role: "homie" }).argv).not.toContain("--openwiki");
      expect(adapterInvocation({ ...run, role: "worker" }).argv).not.toContain("--openwiki");
    }
  });

  test("the Scribe server receives the immutable repository input commit", () => {
    const commit = "a".repeat(40);
    const run = routed("codex", "configured-binding", { role: "scribe", repository: { input_commit: commit } });
    const argv = adapterInvocation(run).argv;
    expect(argv[argv.indexOf("--openwiki-input-commit") + 1]).toBe(commit);
    const without = adapterInvocation({ ...run, repository: undefined }).argv;
    expect(without).not.toContain("--openwiki-input-commit");
  });
  test("the shipped harness families select their real adapter", () => {
    expect(adapterInvocation(routed("codex", "openai-sub")).argv)
      .toContain("/app/src/agent-runner/adapters/codex.ts");
    expect(adapterInvocation(routed("claude-sdk", "anthropic-sub")).argv)
      .toContain("/app/src/agent-runner/adapters/claude.ts");
  });

  test("an operator-defined binding on a shipped family needs no code change", () => {
    const argv = adapterInvocation(routed("claude-sdk", "operator-invented", {}, {
      channel: "claude", model: "operator-chosen", base_url: "",
    })).argv;
    expect(argv).toContain("/app/src/agent-runner/adapters/claude.ts");
    expect(argv).toEqual(expect.arrayContaining(["--channel", "claude"]));
    expect(argv).toEqual(expect.arrayContaining(["--model", "operator-chosen"]));
    expect(argv).not.toContain("--base-url");
  });

  test("the configured model reaches the codex adapter", () => {
    expect(adapterInvocation(routed("codex", "openai-sub", {}, { model: "operator-chosen" })).argv)
      .toEqual(expect.arrayContaining(["--model", "operator-chosen"]));
    // An empty configured model means the provider default, not an empty flag.
    expect(adapterInvocation(routed("codex", "openai-sub")).argv).not.toContain("--model");
  });

  test("a real route without a coherent capability refuses", () => {
    expect(() => adapterInvocation(envelope({
      harness: "codex", model_binding: "openai-sub", harness_config: undefined,
    }))).toThrow("no resolved runtime capability");
    expect(() => adapterInvocation(envelope({
      harness: "codex", model_binding: "openai-sub", harness_config: undefined,
      runtime: { harness: "codex", id: "openai-sub" } as unknown as RunEnvelopeRuntime,
    }))).toThrow("incomplete runtime capability");
    expect(() => adapterInvocation(routed("codex", "openai-sub", {
      model_binding: "other",
    }))).toThrow("does not describe route");
  });

  test("an explicit test route selects the fake stand-in and unshipped families refuse", () => {
    const run = envelope({ harness: "codex", model_binding: "openai-sub" });
    expect(adapterInvocation(run, "codex/openai-sub").defaultNativeFile).toBe("native.jsonl");
    expect(() => adapterInvocation(routed("unknown", "unknown"))).toThrow("ships no");
  });

  test("native resume and trace locators are fail-closed", () => {
    expect(() => adapterInvocation(routed("codex", "openai-sub", { mode: "native" })))
      .toThrow("native_session_ref");
    expect(nativeLocator("2026/07/native.jsonl")).toBe("2026/07/native.jsonl");
    expect(() => nativeLocator("../escape.jsonl")).toThrow("safe native trace locator");
  });
});

describe("repository preamble", () => {
  const SHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";

  /** Records every git argv; each call answers from the scripted results in
   * order, and anything past the script succeeds. */
  const gitRig = (...results: { exitCode: number; stderr: string }[]) => {
    const calls: string[][] = [];
    const exec: PreambleExec = async (argv) => {
      calls.push(argv);
      return results[calls.length - 1] ?? { exitCode: 0, stderr: "" };
    };
    return { calls, exec };
  };

  test("an envelope without a repository skips the preamble entirely", async () => {
    const rig = gitRig();
    await expect(runStorePreamble(envelope({}), rig.exec)).resolves.toBeNull();
    expect(rig.calls).toEqual([]);
  });

  test("the happy path is exactly rev-parse, cat-file, reset --hard, clean -fdx in the repository", async () => {
    const rig = gitRig();
    const run = envelope({
      repository: { input_commit: SHA },
      mounts: { session: "/mc/session", workspace: "/workspace/source" },
    });
    await expect(runStorePreamble(run, rig.exec)).resolves.toBeNull();
    expect(rig.calls).toEqual([
      ["git", "-C", "/workspace/source", "rev-parse", "--is-inside-work-tree"],
      ["git", "-C", "/workspace/source", "cat-file", "-e", `${SHA}^{commit}`],
      ["git", "-C", "/workspace/source", "reset", "--hard", SHA],
      ["git", "-C", "/workspace/source", "clean", "-fdx"],
    ]);
  });

  test("the Scribe checks its read-only source without a reset", async () => {
    const run = envelope({ role: "scribe", repository: { input_commit: SHA } });
    const rig = gitRig();
    await expect(runStorePreamble(run, rig.exec)).resolves.toBeNull();
    expect(rig.calls.at(-1)).toEqual(["git", "-C", "/workspace/source", "diff", "--quiet", SHA, "--"]);
    expect(rig.calls.flat()).not.toContain("reset");
    expect(rig.calls.flat()).not.toContain("clean");
    const changed = gitRig({ exitCode: 0, stderr: "" }, { exitCode: 0, stderr: "" }, { exitCode: 1, stderr: "" });
    expect(await runStorePreamble(run, changed.exec)).toBe("The Scribe repository differs from its start commit.");
  });

  test("a malformed input commit refuses before any git process", async () => {
    const rig = gitRig();
    const refusal = await runStorePreamble(envelope({ repository: { input_commit: "HEAD" } }), rig.exec);
    expect(refusal).toContain("not a canonical git object name");
    expect(rig.calls).toEqual([]);
  });

  test("a missing repository refuses on the first probe and runs nothing destructive", async () => {
    const rig = gitRig({ exitCode: 128, stderr: "fatal: not a git repository" });
    const refusal = await runStorePreamble(envelope({ repository: { input_commit: SHA } }), rig.exec);
    expect(refusal).toContain("missing or is not a git work tree");
    expect(refusal).toContain("not a git repository");
    expect(rig.calls).toHaveLength(1);
  });

  test("an unresolvable input commit refuses by name before reset", async () => {
    const rig = gitRig({ exitCode: 0, stderr: "" }, { exitCode: 1, stderr: "fatal: Not a valid object name" });
    const refusal = await runStorePreamble(envelope({ repository: { input_commit: SHA } }), rig.exec);
    expect(refusal).toContain(SHA);
    expect(refusal).toContain("does not resolve to a commit");
    expect(rig.calls).toHaveLength(2);
  });

  test("a failed reset or clean surfaces the git stderr as the refusal", async () => {
    const resetFailed = gitRig({ exitCode: 0, stderr: "" }, { exitCode: 0, stderr: "" }, { exitCode: 1, stderr: "fatal: index locked" });
    await expect(runStorePreamble(envelope({ repository: { input_commit: SHA } }), resetFailed.exec))
      .resolves.toContain("reset --hard");
    const cleanFailed = gitRig({ exitCode: 0, stderr: "" }, { exitCode: 0, stderr: "" }, { exitCode: 0, stderr: "" }, { exitCode: 1, stderr: "warning: could not remove" });
    await expect(runStorePreamble(envelope({ repository: { input_commit: SHA } }), cleanFailed.exec))
      .resolves.toContain("clean -fdx");
  });
});

for (const role of ["homie", "setup", "worker", "verifier", "packager", "scribe"]) {
  test(`${role} rejects unsupported runtime channels and custom endpoints`, () => {
    for (const harness of ["codex", "claude-sdk"]) {
      for (const patch of [{ channel: "static" }, { channel: "none" }, { channel: "" },
        { channel: harness === "codex" ? "claude" : "codex" }, { base_url: "https://wrong.invalid" }]) {
        expect(() => adapterInvocation(routed(harness, "configured", { role }, patch)))
          .toThrow("supported sign-in connection");
      }
    }
  });
}

test("the adapter receives the profile effort on fresh and resumed runs", () => {
  for (const harness of ["codex", "claude-sdk"]) {
    for (const mode of ["fresh", "native"] as const) {
      const run = routed(harness, "selected", { mode, native_session_ref: "thread" }, { effort: "high" });
      expect(adapterInvocation(run).argv).toEqual(expect.arrayContaining(["--effort", "high"]));
    }
    expect(adapterInvocation(routed(harness, "selected")).argv).not.toContain("--effort");
  }
});
