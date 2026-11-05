// effects.test.ts — the effectors against stubbed mc/docker/fs: spawn's
// folder → repository → run.json → container order, the run.json schema, the
// docker argv, the producer-absence fence, exact-name reap, and idle as a
// no-op.

import { describe, expect, test } from "bun:test";
import { applyEffect, DISCARD_RETRY_MS } from "./effects";
import { fail, homieInspectFromCreate, makeRig, ok, presentChild, runtimeBinding, testConfig } from "./test-helpers";
import { canonicalHomieDockerEnvelope } from "./homie-envelope";
import { AttachmentConfinementHealth } from "./attachment-health";
import { runExecution, type RepositoryPlan } from "./repository";
import type { CredentialProjection, Effect, ExecResult, TickDeps } from "./types";

/** One artifact class row: the only destination family a committed plan
 * may name. */
const artifactEntry = {
  access: "rw" as const,
  destination: "/mc/artifacts/ws-e2e",
  device: "16777231",
  inode: "424242",
  kind: "dir" as const,
  logical_id: "artifact:ws-e2e",
  mode: 448,
  owner_uid: 501,
  source: "/host/artifacts/ws-e2e",
};

function accessProjection(harness = "codex"): CredentialProjection {
  return { env: {}, proxyConfig: JSON.stringify({ version: 1, harness, authMode: "account",
    url: "https://host.docker.internal:7444", token: "culler_proxy_" + "c".repeat(43),
    ca: "-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----" }) };
}

const INPUT_COMMIT = "a".repeat(40);
const DESTINATION_COMMIT = "b".repeat(40);

/** The isolated repository of task 42, as the dispatch effect describes it. */
const repositoryPlan: RepositoryPlan = {
  task_id: 42,
  workspace_root: "/host/workspace",
  input_commit: INPUT_COMMIT,
  destination_branch: "main",
  max_temporary_bytes: 1024,
};

const REPOSITORY_ROOT = "/tmp/mc-home/runs/repositories/run-42-worker/repo";

/** A rig whose `mc` answers the repository preparation of a spawn. Each
 * `__repository` call returns the fixed input and destination commits, and
 * each `delivery record` call succeeds. These calls never reach the scripted
 * queue, so `rig.mc.enqueue` and `rig.mc.calls` address the other verbs only
 * (the mount rechecks first). `repositoryOperations` lists the envelope
 * operations in order. */
function repositoryRig(overrides: Partial<TickDeps> = {}) {
  const rig = makeRig(overrides);
  const scripted = rig.deps.runMc;
  const repositoryOperations: string[] = [];
  rig.deps.runMc = async (argv, stdin) => {
    if (argv[0] === "__repository") {
      const envelope = JSON.parse(rig.fakeFs.writes.get(argv[1]!)!) as { operation: string };
      repositoryOperations.push(envelope.operation);
      return ok(JSON.stringify({ task_id: 42, input_commit: INPUT_COMMIT, destination_commit: DESTINATION_COMMIT }));
    }
    if (argv[0] === "delivery" && argv[1] === "record") return ok("{}");
    return scripted(argv, stdin);
  };
  return { ...rig, repositoryOperations };
}

/** A well-formed inspect record for one run container: the positive shape
 * the producer-absence fence treats as "still present". */
const spawnEffect: Effect = {
  action: "spawn",
  run_id: "run-42-worker",
  role: "worker",
  subject_id: 42,
  project: "ws-e2e",
  skill_store_dir: "skills/projects/ws-e2e/worker",
  pool_ids: [42],
  harness: "fake",
  model_binding: "fake",
  runtime_binding: runtimeBinding("fake", "fake", { channel: "fake" }),
  git_identity_override: null,
	brief: "immutable claimed-state brief",
  skills: [{ name: "mc-worker", body: "---\nname: mc-worker\n---\nthe worker's command surface\n" }],
  mount_plan: { entries: [artifactEntry], repository: repositoryPlan, version: 1 },
  session_path: "sessions/run-42-worker",
  heartbeat_interval_s: 1,
};

/** The same spawn on an explicitly named route, with the capability the
 * dispatch attest would have carried for it. Overriding only harness/binding
 * would leave the effect describing a route its own capability does not, which
 * the resident now refuses — as it should. */
const routedSpawn = (
  harness: string,
  id: string,
  overrides: Partial<Extract<Effect, { action: "spawn" }>> = {},
): Effect => ({
  ...(spawnEffect as Extract<Effect, { action: "spawn" }>),
  harness,
  model_binding: id,
  runtime_binding: runtimeBinding(harness, id),
  ...overrides,
});

const PLAN_PATH = "/tmp/mc-home/runs/run-42-worker.mounts.json";

describe("the Scribe run of a wiki update", () => {
  const WIKI_RUN = "0123456789abcdef";
  const WIKI_RUN_ROOT = `/tmp/mc-home/runs/wiki-repositories/${WIKI_RUN}`;

  /** The spawn of a wiki update run: no task, no pool, no artifact root, no
   * tool home, and the isolated repository of the wiki update. */
  const wikiSpawn: Effect = {
    ...(spawnEffect as Extract<Effect, { action: "spawn" }>),
    run_id: WIKI_RUN,
    role: "scribe",
    subject_id: null,
    pool_ids: [],
    skill_store_dir: "skills/projects/ws-e2e/scribe",
    session_path: `sessions/${WIKI_RUN}`,
    mount_plan: {
      entries: [],
      repository: {
        wiki_update: WIKI_RUN,
        workspace_root: "/operator/project",
        input_commit: INPUT_COMMIT,
        destination_branch: "main",
        max_temporary_bytes: 21474836480,
      },
      version: 1,
    },
  };

  /** A rig whose `mc` answers the repository preparation of the wiki update
   * run. `envelopes` and `records` hold each repository envelope and each
   * recorded result in order. */
  function wikiRig() {
    const rig = makeRig();
    const scripted = rig.deps.runMc;
    const envelopes: Record<string, unknown>[] = [];
    const records: Array<{ argv: string[]; result: unknown }> = [];
    const result = { wiki_update: WIKI_RUN, input_commit: INPUT_COMMIT, destination_commit: DESTINATION_COMMIT };
    rig.deps.runMc = async (argv, stdin) => {
      if (argv[0] === "__repository") {
        envelopes.push(JSON.parse(rig.fakeFs.writes.get(argv[1]!)!));
        return ok(JSON.stringify(result));
      }
      if (argv[0] === "delivery" && argv[1] === "record") {
        records.push({ argv, result: JSON.parse(new TextDecoder().decode(stdin)) });
        return ok("{}");
      }
      return scripted(argv, stdin);
    };
    rig.docker.enqueue(ok("container-id\n"), ok(""));
    return { ...rig, envelopes, records, result };
  }

  test("the spawn prepares the repository of the wiki update below the wiki folder", async () => {
    const rig = wikiRig();
    await applyEffect(wikiSpawn, rig.deps);
    const allocate = {
      operation: "allocate",
      wiki_update: WIKI_RUN,
      run_id: WIKI_RUN,
      wiki_only: true,
      workspace_root: "/operator/project",
      run_root: WIKI_RUN_ROOT,
      input_commit: INPUT_COMMIT,
      destination_branch: "main",
      max_temporary_bytes: 21474836480,
    };
    expect(rig.envelopes).toEqual([allocate, { ...allocate, operation: "ready", destination_commit: DESTINATION_COMMIT }]);
    expect(rig.envelopes.every((envelope) => !("task_id" in envelope))).toBe(true);
    const ready = ["delivery", "record", "--operation", "ready", "--run", WIKI_RUN, "--result", "-"];
    expect(rig.records).toEqual([{ argv: ready, result: rig.result }, { argv: ready, result: rig.result }]);
    // The plan holds no entry and no tool home, so no mount recheck runs.
    expect(rig.mc.calls).toEqual([]);
    expect(rig.logs.some((line) => line.includes("spawn refused"))).toBe(false);
  });

  test("the run mounts Project content read-only and grants wiki writes", async () => {
    const rig = wikiRig();
    await applyEffect(wikiSpawn, rig.deps);
    const root = `${WIKI_RUN_ROOT}/repo`;
    const create = rig.docker.calls.find((args) => args[0] === "create")!;
    expect(create).toContain(`${root}:/workspace/source:ro`);
    expect(create).toContain(`${root}/openwiki:/workspace/source/openwiki`);
    expect(create).toContain(`${root}/.git:/workspace/source/.git`);
    expect(create).not.toContain(`${root}:/workspace/source`);
    expect(create.join("\n")).not.toContain("/tmp/mc-home/runs/repositories/");
    expect(create[create.indexOf("--name") + 1]).toBe(`mc-ws-e2e-${WIKI_RUN}`);
    expect(rig.docker.calls.at(-1)).toEqual(["start", `mc-ws-e2e-${WIKI_RUN}`]);
    const run = JSON.parse(rig.fakeFs.writes.get(`/tmp/mc-home/runs/${WIKI_RUN}.json`)!);
    expect(run.mounts.workspace).toBe("/workspace/source");
    expect(run.mounts.allow_write).toEqual(["/mc/logs", "/workspace/source/openwiki", "/workspace/source/.git"]);
    expect(run.repository).toEqual({ input_commit: INPUT_COMMIT });
  });

  test("the launch envelope holds the Scribe role with no subject and no pool", async () => {
    const rig = wikiRig();
    await applyEffect(wikiSpawn, rig.deps);
    const run = JSON.parse(rig.fakeFs.writes.get(`/tmp/mc-home/runs/${WIKI_RUN}.json`)!);
    expect(run).toMatchObject({
      run_id: WIKI_RUN, tier: "pipeline", role: "scribe", subject_id: null, project: "ws-e2e", pool_ids: [],
      harness_config: { behavior: "/mc/behaviors/scribe.json" },
    });
  });

  test("the run has no tool home and uses the empty home of the image", async () => {
    const rig = wikiRig();
    await applyEffect(wikiSpawn, rig.deps);
    const create = rig.docker.calls.find((args) => args[0] === "create")!;
    const environment = create.flatMap((arg, i) => (create[i - 1] === "-e" ? [arg] : []));
    expect(environment.filter((entry) => entry.startsWith("HOME="))).toEqual(["HOME=/home/mc-model"]);
    expect(create.some((arg) => arg.endsWith(":/home/agent"))).toBe(false);
  });

  for (const [name, subject] of [
    ["two subjects", { task_id: 42, wiki_update: WIKI_RUN }],
    ["no subject", {}],
    ["a wiki update that is no run identifier", { wiki_update: "../escape" }],
    ["a task that is no task identifier", { task_id: 0 }],
  ] as const) {
    test(`a repository plan with ${name} is refused before any launch file or Docker call`, async () => {
      const rig = wikiRig();
      const { wiki_update: _subject, ...plan } = (wikiSpawn as Extract<Effect, { action: "spawn" }>).mount_plan.repository!;
      await applyEffect({
        ...wikiSpawn,
        mount_plan: { entries: [], repository: { ...plan, ...subject } as RepositoryPlan, version: 1 },
      } as Effect, rig.deps);
      expect(rig.docker.calls).toEqual([]);
      expect(rig.fakeFs.events).toEqual([]);
      expect(rig.envelopes).toEqual([]);
      expect(rig.logs).toContain("spawn refused: The repository subject is invalid. (fail-closed)");
      expect(rig.mc.calls).toEqual([[
        "run", "infra-failure", WIKI_RUN, "--condition",
        "spawn refused: The repository subject is invalid. (fail-closed)",
        "--source", "resident", "--cause", "unknown",
      ]]);
    });
  }
});

describe("idle and unknown effects", () => {
  test("a repository effect is not a tick effect", async () => {
    const rig = makeRig();
    await applyEffect({
      action: "repository", operation: "cleanup", task_id: 42, project: "ws-e2e", run_id: "run-42-worker",
    } as unknown as Effect, rig.deps);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.mc.calls).toEqual([]);
    expect(rig.logRecords.map((record) => record.event)).toEqual(["tick.effect_unknown"]);
  });

  test("idle does nothing but log", async () => {
    const rig = makeRig();
    await applyEffect({ action: "idle", reason: "no work" }, rig.deps);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.mc.calls).toEqual([]);
    expect(rig.fakeFs.events).toEqual([]);
    expect(rig.logs.some((l) => l.includes("idle") && l.includes("no work"))).toBe(true);
  });

  test("an unknown action is logged and ignored (fail-closed)", async () => {
    const rig = makeRig();
    await applyEffect({ action: "explode" } as unknown as Effect, rig.deps);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.mc.calls).toEqual([]);
    expect(rig.logs.some((l) => l.includes("unknown action") && l.includes("explode"))).toBe(true);
  });

  // `mc` emits `action: "refused"` for every dispatch refusal and has already
  // written the health row and applied the consequence before the effect gets
  // here. The arm therefore commits nothing (`mc` is the sole spine writer)
  // and exists only so the one line an operator reads is true.
  test("a refusal names its code and consequence instead of being called unknown", async () => {
    const rig = makeRig();
    await applyEffect(
      {
        action: "refused",
        class: "health",
        code: "health.effect_oversize",
        consequence: "task_blocked",
        task_id: 46,
        infrastructure_remaining: 0,
      } as unknown as Effect,
      rig.deps,
    );
    expect(rig.docker.calls).toEqual([]);
    expect(rig.mc.calls).toEqual([]);
    expect(rig.fakeFs.events).toEqual([]);
    const line = rig.logs.find((l) => l.includes("dispatch refused"));
    expect(line).toBeDefined();
    expect(line).toContain("health.effect_oversize");
    expect(line).toContain("task_blocked");
    // The line must NOT claim the resident met something it does not
    // understand: that string is the signature of a resident older than its
    // `mc`, and AGENTS.md routes it to the rung-4 paired rebuild.
    expect(rig.logs.some((l) => l.includes("unknown action"))).toBe(false);
  });

  // The tail is the half a rewording is most likely to drop, and it carries the
  // only field that separates "retries left" from "this was the last one".
  test("a refusal keeps the bounded JSON tail the default arm builds", async () => {
    const rig = makeRig();
    await applyEffect(
      {
        action: "refused",
        class: "health",
        code: "mount.source_missing",
        consequence: "health",
        task_id: 13,
        infrastructure_remaining: 2,
      } as unknown as Effect,
      rig.deps,
    );
    const line = rig.logs.find((l) => l.includes("dispatch refused"))!;
    expect(line).toContain('"infrastructure_remaining":2');
    expect(line).toContain('"class":"health"');
    expect(line).toContain('"task_id":13');
  });

  // Same 512-byte bound as the default arm, and for the same reason: a runaway
  // or hostile effect must not flood the log through this path either.
  test("a refusal's tail is bounded like the default arm's", async () => {
    const rig = makeRig();
    await applyEffect(
      {
        action: "refused",
        class: "candidate",
        code: "candidate.overlong",
        consequence: "none",
        filler: "x".repeat(4000),
      } as unknown as Effect,
      rig.deps,
    );
    const line = rig.logs.find((l) => l.includes("dispatch refused"))!;
    expect(line).toContain("…");
    expect(line.length).toBeLessThan(700);
  });

  // Every class reaches this arm — stale, health and candidate — so the arm
  // must not assume the health shape. `consequence` is interpolated verbatim;
  // there is no mapping table, no severity, no prettifying.
  test("a stale-class refusal reaches the same arm", async () => {
    const rig = makeRig();
    await applyEffect(
      { action: "refused", class: "stale", code: "stale.records", consequence: "none" } as unknown as Effect,
      rig.deps,
    );
    expect(rig.mc.calls).toEqual([]);
    const line = rig.logs.find((l) => l.includes("dispatch refused"))!;
    expect(line).toContain("stale.records");
    expect(line).toContain("none");
  });
});

describe("spawn effect", () => {
  // No agent runs as root. A run whose plan carries a repository also binds
  // the isolated repository of its task.
  test("every run container runs as uid 10002 without capabilities", async () => {
    for (const [role, plan, repository] of [
      ["worker", { entries: [], repository: repositoryPlan, version: 1 }, true],
      ["verifier", { entries: [], repository: repositoryPlan, version: 1 }, true],
      ["packager", { entries: [], repository: repositoryPlan, version: 1 }, true],
      ["worker", { entries: [artifactEntry], version: 1 }, false],
      ["strategist(propose)", { entries: [], version: 1 }, false],
    ] as const) {
      const rig = repositoryRig();
      rig.docker.enqueue(ok("container-id\n"), ok(""));
      await applyEffect({
        ...spawnEffect,
        role,
        subject_id: role === "strategist(propose)" ? null : 42,
        mount_plan: { ...plan, entries: [...plan.entries] },
      }, rig.deps);
      const create = rig.docker.calls.find((call) => call[0] === "create")!;
      const flag = (name: string) => create[create.indexOf(name) + 1];
      expect(create).toContain("--init");
      expect([role, flag("--user"), flag("--cap-drop")]).toEqual([role, "10002:10002", "ALL"]);
      expect(create.some((arg) => arg.endsWith(":/workspace/source"))).toBe(repository);
    }
  });

  // The runner reads the absence of `repository` as "no repository preamble".
  test("an envelope without a repository carries no repository object", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect({ ...spawnEffect, mount_plan: { entries: [artifactEntry], version: 1 } }, rig.deps);
    const runJson = JSON.parse(rig.fakeFs.writes.get("/tmp/mc-home/runs/run-42-worker.json")!);
    expect("repository" in runJson).toBe(false);
    expect(runJson.mounts.workspace).toBe("/mc/scratch");
  });

  test("canonical route reaches the real credential gate without a fake stand-in", async () => {
    const rig = makeRig();
    await applyEffect(
      routedSpawn("codex", "chatgpt"),
      rig.deps,
    );
    expect(rig.fakeFs.events).toEqual([]);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.logs.some((l) => l.includes("runtime credential projector unavailable"))).toBe(true);
  });

  // The shipped image carries only the fake agent-runner. `agentRunnerRoutes`
  // authorizes exactly which non-fake `harness/model_binding` routes that one
  // adapter may stand in for, so the real resident can launch a production
  // Worker through it. The default (unset) keeps the fake/fake-only refusal.
  test("a configured non-fake adapter route launches through the shipped agent-runner", async () => {
    const rig = repositoryRig({
      config: { ...testConfig, agentRunnerRoutes: ["codex/chatgpt"] },
      credentials: { project: () => accessProjection() },
    });
    rig.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect(routedSpawn("codex", "chatgpt"), rig.deps);
    const create = rig.docker.calls[0]!;
    expect(create[0]).toBe("create");
    expect(create).toContain("mc-ws-e2e-run-42-worker");
    // The same allowlist is handed to the in-container adapter so it, too, will
    // stand in for the route (its own fail-closed gate is otherwise fake-only).
    expect(create).toEqual(expect.arrayContaining(["-e", "MC_AGENT_RUNNER_ROUTES=codex/chatgpt"]));
    // The run.json records the real committed route, never a fake relabel.
    const runJson = JSON.parse(rig.fakeFs.writes.get("/tmp/mc-home/runs/run-42-worker.json")!);
    expect(runJson.harness).toBe("codex");
    expect(runJson.model_binding).toBe("chatgpt");
    expect(rig.logs.some((l) => l.includes("spawn refused"))).toBe(false);
  });

  test("a production adapter launches without fake behavior fixtures", async () => {
    const rig = repositoryRig({
      config: { ...testConfig, roleBehaviors: {} },
      credentials: { project: () => accessProjection() },
    });
    rig.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect(routedSpawn("codex", "chatgpt"), rig.deps);
    expect(rig.docker.calls[0]![0]).toBe("create");
    const runJson = JSON.parse(rig.fakeFs.writes.get("/tmp/mc-home/runs/run-42-worker.json")!);
    expect(runJson.harness_config).toBeUndefined();
  });

  test("a spawn whose capability disagrees with its route refuses before any effect", async () => {
    const base = routedSpawn("codex", "chatgpt");
    for (const effect of [
      // No carrier at all: config resolved nothing, so nothing is launchable.
      { ...base, runtime_binding: undefined as unknown as never },
      // A partial carrier is not a capability; every missing field is an input
      // nothing resolved.
      { ...base, runtime_binding: { harness: "codex", id: "chatgpt" } as unknown as never },
      // A real family that names no credential source.
      { ...base, runtime_binding: runtimeBinding("codex", "chatgpt", { credential: "" }) },
      // A capability for a different route than the effect claims.
      { ...base, runtime_binding: runtimeBinding("codex", "other") },
      { ...base, runtime_binding: runtimeBinding("claude-sdk", "chatgpt") },
      { ...base, runtime_binding: runtimeBinding("codex", "chatgpt", { channel: "static" }) },
      { ...base, runtime_binding: runtimeBinding("codex", "chatgpt", { channel: "none" }) },
      { ...base, runtime_binding: runtimeBinding("codex", "chatgpt", { base_url: "https://unapproved.invalid" }) },
    ]) {
      const rig = makeRig({
        config: { ...testConfig, agentRunnerRoutes: ["codex/chatgpt"] },
        credentials: { project: () => accessProjection() },
      });
      await applyEffect(effect as Effect, rig.deps);
      expect(rig.fakeFs.events).toEqual([]);
      expect(rig.docker.calls).toEqual([]);
      expect(rig.logs.some((l) => l.includes("spawn refused"))).toBe(true);
    }
  });

  test("the configured runtime reaches the run envelope", async () => {
    const rig = repositoryRig({
      config: { ...testConfig, agentRunnerRoutes: ["codex/chatgpt"] },
      credentials: { project: () => accessProjection() },
    });
    rig.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect(routedSpawn("codex", "chatgpt", {
      runtime_binding: {
        ...runtimeBinding("codex", "chatgpt", {
          channel: "codex",
          credential: "openai-login",
          model: "operator-chosen-model",
          effort: "high",
        }),
        // Keys the resident does not consume ride along and stay on the host.
        client_id: "app_secret_looking_but_public",
        token_url: "https://auth.example.test/oauth/token",
      } as unknown as never,
    }), rig.deps);

    const runJson = JSON.parse(rig.fakeFs.writes.get("/tmp/mc-home/runs/run-42-worker.json")!);
    expect(runJson.runtime).toEqual({
      base_url: "",
      channel: "codex",
      harness: "codex",
      id: "chatgpt",
      model: "operator-chosen-model",
      effort: "high",
    });
    // The credential source id and the OAuth authority strings are the
    // resident's business, never a container's.
    expect(runJson.runtime.credential).toBeUndefined();
    expect(runJson.runtime.token_url).toBeUndefined();
    expect(runJson.runtime.client_id).toBeUndefined();
  });

  test("an unknown route absent from the test stand-in list stays refused", async () => {
    const rig = makeRig({ config: { ...testConfig, agentRunnerRoutes: ["codex/chatgpt"] } });
    await applyEffect(routedSpawn("unknown", "unknown"), rig.deps);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.logs.some((l) => l.includes("spawn refused"))).toBe(true);
  });

  test("an authorized non-fake repository Worker drops to model uid without NNP", async () => {
    const rig = repositoryRig({
      config: { ...testConfig, agentRunnerRoutes: ["codex/chatgpt"] },
      credentials: { project: () => accessProjection() },
    });
    await applyEffect(routedSpawn("codex", "chatgpt", {
      mount_plan: { version: 1, entries: [], repository: repositoryPlan },
    }), rig.deps);
    const create = rig.docker.calls[0]!;
    expect(create).toEqual(expect.arrayContaining(["--user", "10002:10002", "--cap-drop", "ALL"]));
    // Agent containers never take NNP: the repository class is the one that does.
    expect(create).not.toContain("no-new-privileges=true");
  });

  test("effects in order: folder → repository → run.json → container", async () => {
    const rig = repositoryRig();
    rig.docker.enqueue(ok("container-id\n"));
    // interleave docker into the same event stream
    const realDocker = rig.deps.docker;
    rig.deps.docker = (argv) => {
      rig.fakeFs.events.push("docker:" + argv[0]);
      return realDocker(argv);
    };
    await applyEffect(spawnEffect, rig.deps);
    // The repository preparation writes its envelope under runs/ twice: once
    // to allocate the run's repository, once to mark it ready.
    expect(rig.fakeFs.events).toEqual([
      "mkdir:/tmp/mc-home/sessions/run-42-worker",
      "mkdir:/tmp/mc-home/runs",
      "mkdir:/tmp/mc-home/runs",
      "write:/tmp/mc-home/runs/run-42-worker.repository.json",
      "mkdir:/tmp/mc-home/runs",
      "write:/tmp/mc-home/runs/run-42-worker.repository.json",
      "mkdir:/tmp/mc-home/logs/runs/run-42-worker",
      "write:/tmp/mc-home/runs/run-42-worker.json",
      "write:" + PLAN_PATH,
      "docker:create",
      "docker:start",
    ]);
    expect(rig.mc.calls).toEqual([
      ["__mount-recheck", PLAN_PATH],
      ["__mount-recheck", PLAN_PATH],
    ]);
  });

  test("run.json is materialized outside the session folder", async () => {
    const rig = repositoryRig();
    await applyEffect(spawnEffect, rig.deps);
    for (const path of rig.fakeFs.writes.keys()) {
      expect(path.startsWith("/tmp/mc-home/sessions/")).toBe(false);
    }
  });

  test("run.json matches the fixed schema", async () => {
    const rig = repositoryRig();
    await applyEffect(spawnEffect, rig.deps);
    const written = rig.fakeFs.writes.get("/tmp/mc-home/runs/run-42-worker.json");
    expect(written).toBeDefined();
    expect(written!.endsWith("\n")).toBe(true);
    const runJson = JSON.parse(written!);
    expect(runJson).toEqual({
      run_id: "run-42-worker",
      tier: "pipeline",
      role: "worker",
      subject_id: 42,
      project: "ws-e2e",
      harness: "fake",
      model_binding: "fake",
      runtime: {
        base_url: "",
        channel: "fake",
        harness: "fake",
        id: "fake",
        model: "",
      },
      mode: "fresh",
	      brief: "immutable claimed-state brief",
      skills: [{
        name: "mc-worker",
        body: "---\nname: mc-worker\n---\nthe worker's command surface\n",
      }],
      pool_ids: [42],
      heartbeat_interval_s: 1,
      harness_config: { behavior: "/mc/behaviors/worker.json" },
      mounts: {
        session: "/mc/session",
        workspace: "/workspace/source",
        allow_write: ["/mc/artifacts/ws-e2e", "/mc/logs", "/workspace/source"],
      },
      // The runner pins the isolated repository to `input_commit` before the
      // model sees the tree. The host plan stays out of the envelope.
      repository: { input_commit: INPUT_COMMIT },
    });
	    expect(runJson.brief).toBe(spawnEffect.brief);
    // The envelope carries THIS run's role and no other. The container-side
    // half of that claim — that the installed skill root holds one directory —
    // is judged from inside the container in mc/e2e/role_skill_isolation_test.go.
    expect(runJson.skills).toEqual(spawnEffect.skills);
    expect(written!.includes("mc-verifier")).toBe(false);
    // The agent-visible envelope never carries the host source of a plan
    // entry; that lives only in the host-side plan sibling.
    expect(written!.includes("/host/artifacts")).toBe(false);
    const plan = JSON.parse(rig.fakeFs.writes.get(PLAN_PATH)!);
    expect(plan).toEqual({ entries: [artifactEntry], repository: repositoryPlan, version: 1 });
  });

  // The operator's skills for this role in this Project ride run.json
  // behind the shipped set, read from the host store at spawn; what the store
  // cannot deliver becomes a problem the runner logs, and the rest of the set
  // still goes.
  describe("operator skills from the host store", () => {
    const storeDir = `${testConfig.mcHome}/skills/projects/ws-e2e/worker`;
    const good = (name: string) => `---\nname: ${name}\ndescription: ${name} help\n---\nUse ${name}.\n`;
    const storedSpawn = { ...spawnEffect, skill_store_dir: "skills/projects/ws-e2e/worker" } as Effect;
    const runJsonOf = (rig: ReturnType<typeof repositoryRig>) =>
      JSON.parse(rig.fakeFs.writes.get(`${testConfig.mcHome}/runs/run-42-worker.json`)!);

    test("the set is the command skill first, then every deliverable operator skill by name", async () => {
      const rig = repositoryRig();
      rig.fakeFs.dir(`${testConfig.mcHome}/skills/homie`);
      rig.fakeFs.writes.set(`${storeDir}/zeta/SKILL.md`, good("zeta"));
      rig.fakeFs.writes.set(`${storeDir}/alpha/SKILL.md`, good("alpha"));
      await applyEffect(storedSpawn, rig.deps);
      const runJson = runJsonOf(rig);
      expect(runJson.skills.map((s: { name: string }) => s.name)).toEqual(["mc-worker", "alpha", "zeta"]);
      expect(runJson.skills[1].body).toBe(good("alpha"));
      expect(runJson.skill_problems).toBeUndefined();
    });

    test("a skill the store cannot deliver is a logged problem, not a refused run", async () => {
      const rig = repositoryRig();
      rig.fakeFs.dir(`${testConfig.mcHome}/skills/homie`);
      rig.fakeFs.writes.set(`${storeDir}/fine/SKILL.md`, good("fine"));
      rig.fakeFs.dir(`${storeDir}/Bad Name`);
      rig.fakeFs.dir(`${storeDir}/empty`);
      rig.fakeFs.writes.set(`${storeDir}/renamed/SKILL.md`, good("other"));
      rig.fakeFs.writes.set(`${storeDir}/mc-worker/SKILL.md`, good("mc-worker"));
      rig.docker.enqueue(ok("container-id\n"));
      await applyEffect(storedSpawn, rig.deps);
      const runJson = runJsonOf(rig);
      expect(runJson.skills.map((s: { name: string }) => s.name)).toEqual(["mc-worker", "fine"]);
      expect(runJson.skills[0].body).toBe(spawnEffect.skills[0].body);
      expect(runJson.skill_problems).toHaveLength(4);
      expect(runJson.skill_problems.join("\n")).toContain('refused skill name "Bad Name"');
      expect(runJson.skill_problems.join("\n")).toContain("skill empty: cannot read SKILL.md");
      expect(runJson.skill_problems.join("\n")).toContain('skill renamed: frontmatter name "other"');
      expect(runJson.skill_problems.join("\n")).toContain("refused skill mc-worker: the name is one of the role's shipped skills");
      // The run still launched.
      expect(rig.fakeFs.events.filter((e) => e.startsWith("write:"))).toContain(`write:${testConfig.mcHome}/runs/run-42-worker.json`);
    });

    test("an absent store directory is no skills and no problem", async () => {
      const rig = repositoryRig();
      await applyEffect(storedSpawn, rig.deps);
      const runJson = runJsonOf(rig);
      expect(runJson.skills.map((s: { name: string }) => s.name)).toEqual(["mc-worker"]);
      expect(runJson.skill_problems).toBeUndefined();
    });

    test("a Project whose store directories are missing gets them at first spawn, when the store exists", async () => {
      const rig = repositoryRig();
      rig.fakeFs.dir(`${testConfig.mcHome}/skills/homie`);
      await applyEffect(storedSpawn, rig.deps);
      const made = rig.fakeFs.events.filter((e) => e.startsWith(`mkdir:${testConfig.mcHome}/skills/`));
      expect(made).toEqual([
        `mkdir:${testConfig.mcHome}/skills/projects/ws-e2e`,
        ...["arbiter", "packager", "scribe", "setup",
          "strategist-propose", "verifier", "worker"]
          .map((role) => `mkdir:${testConfig.mcHome}/skills/projects/ws-e2e/${role}`),
      ]);
    });

    test("a home without a store is left alone", async () => {
      const rig = repositoryRig();
      await applyEffect(storedSpawn, rig.deps);
      expect(rig.fakeFs.events.some((e) => e.startsWith(`mkdir:${testConfig.mcHome}/skills`))).toBe(false);
    });

    test("a store directory the effect names outside skills/ is refused, not read", async () => {
      const rig = repositoryRig();
      rig.fakeFs.writes.set(`${testConfig.mcHome}/runs/secret/SKILL.md`, good("secret"));
      await applyEffect({ ...spawnEffect, skill_store_dir: "../runs" } as Effect, rig.deps);
      const runJson = runJsonOf(rig);
      expect(runJson.skills.map((s: { name: string }) => s.name)).toEqual(["mc-worker"]);
      expect(runJson.skill_problems.join("\n")).toContain('refused skill store directory "../runs"');
    });
  });

  // A plan-less run declared /workspace/source as its cwd while nothing bound
  // it and the image ships no such directory, so the harness binary could not
  // be spawned at all: each tick died in under a second and the `--rm`
  // container erased the evidence. Nothing in the suite covered a spawn whose
  // mount plan was empty, which is the only shape that reaches the bug.
  test("a plan-less run's cwd is a bound directory, not a path nothing mounts", async () => {
    const rig = makeRig();
    await applyEffect(
      {
        action: "spawn", run_id: "run-9-propose", role: "strategist(propose)",
        subject_id: null, project: "ws-e2e", skill_store_dir: "skills/projects/ws-e2e/strategist-propose", pool_ids: [],
        harness: "fake", model_binding: "fake",
        runtime_binding: runtimeBinding("fake", "fake", { channel: "fake" }),
        git_identity_override: null,
        brief: "propose immutable brief",
        mount_plan: { entries: [], version: 1 },
        heartbeat_interval_s: 1,
      },
      rig.deps,
    );
    const scratch = "/tmp/mc-home/runs/scratch/run-9-propose";
    const runJson = JSON.parse(
      rig.fakeFs.writes.get("/tmp/mc-home/runs/run-9-propose.json")!,
    );
    expect(runJson.mounts.workspace).toBe("/mc/scratch");
    expect(rig.fakeFs.events).toContain(`mkdir:${scratch}`);
    // Declaring the cwd is not enough — the container must actually carry it.
    expect(rig.docker.calls[0]).toEqual(
      expect.arrayContaining(["-v", `${scratch}:/mc/scratch`]),
    );
  });

  // The discriminator is the plan's repository, never the role: a run whose
  // plan carries one binds the isolated repository and keeps pointing at it,
  // so a lost bind still fails closed instead of quietly running the agent in
  // an empty scratch directory.
  test("a repository run binds the isolated repository at /workspace/source and gets no scratch bind", async () => {
    const rig = repositoryRig();
    await applyEffect(spawnEffect, rig.deps);
    // The repository is allocated and made ready before the container exists.
    expect(rig.repositoryOperations).toEqual(["allocate", "ready"]);
    const runJson = JSON.parse(
      rig.fakeFs.writes.get("/tmp/mc-home/runs/run-42-worker.json")!,
    );
    expect(runJson.mounts.workspace).toBe("/workspace/source");
    expect(runJson.repository).toEqual({ input_commit: INPUT_COMMIT });
    expect(JSON.stringify(runJson)).not.toContain(repositoryPlan.workspace_root);
    const create = rig.docker.calls[0]!;
    expect(create).toContain(`${REPOSITORY_ROOT}:/workspace/source`);
    expect(create.join(" ")).not.toContain("/mc/scratch");
  });

  test("subjectless spawn (strategist) writes subject_id null", async () => {
    const rig = makeRig();
    await applyEffect(
      {
        action: "spawn", run_id: "run-9-strategist", role: "strategist",
        subject_id: null, project: "ws-e2e", skill_store_dir: "skills/projects/ws-e2e/strategist-propose", pool_ids: [],
        harness: "fake", model_binding: "fake",
        runtime_binding: runtimeBinding("fake", "fake", { channel: "fake" }),
        git_identity_override: null,
	        brief: "subjectless immutable brief",
        mount_plan: { entries: [], version: 1 },
        heartbeat_interval_s: 1,
      },
      rig.deps,
    );
    const runJson = JSON.parse(
      rig.fakeFs.writes.get("/tmp/mc-home/runs/run-9-strategist.json")!,
    );
    expect(runJson.subject_id).toBeNull();
    expect(runJson.pool_ids).toEqual([]);
    expect(runJson.harness_config.behavior).toBe("/mc/behaviors/strategist.json");
    expect(rig.docker.calls[0]).toEqual(expect.arrayContaining([
      "-e", "GIT_AUTHOR_NAME=Culler",
      "-e", "GIT_AUTHOR_EMAIL=mc@localhost",
      "-e", "GIT_COMMITTER_NAME=Culler",
      "-e", "GIT_COMMITTER_EMAIL=mc@localhost",
    ]));
  });

  // The envelope must CARRY the writable set rather than let an adapter guess
  // it: whatever the resident binds rw is what the sandbox may write, and a
  // later mount-plan row (an artifact root) becomes writable with no adapter
  // edit at all.
  test("run.json carries every writable destination the container actually binds", async () => {
    const planless = makeRig();
    await applyEffect(
      {
        ...spawnEffect,
        run_id: "run-9-propose",
        role: "strategist(propose)",
        subject_id: null,
        pool_ids: [],
        mount_plan: { entries: [], version: 1 },
        brief: "propose brief",
      },
      planless.deps,
    );
    const planlessJson = JSON.parse(
      planless.fakeFs.writes.get("/tmp/mc-home/runs/run-9-propose.json")!,
    );
    // Its scratch cwd, and the log root every run binds rw.
    expect(planlessJson.mounts.allow_write).toEqual(["/mc/scratch", "/mc/logs"]);

    const worker = repositoryRig();
    await applyEffect(spawnEffect, worker.deps);
    const workerJson = JSON.parse(
      worker.fakeFs.writes.get("/tmp/mc-home/runs/run-42-worker.json")!,
    );
    // The plan's rw rows, the log root, and the isolated repository.
    expect(workerJson.mounts.allow_write).toEqual(["/mc/artifacts/ws-e2e", "/mc/logs", "/workspace/source"]);
  });

  // A plan can carry mounts and still bind no repository, and the two are not
  // the same question. A role that legitimately holds no checkout (an Arbiter
  // with an artifact root) must run in scratch; asking how many entries the
  // plan has would hand it a cwd nothing binds.
  test("a plan with mounts but no repository still runs in scratch", async () => {
    const rig = makeRig();
    rig.mc.enqueue(ok(JSON.stringify({ project: "ws-e2e", accepted_commit: "a".repeat(40), status: "absent", graph: {} })));
    await applyEffect(
      {
        ...spawnEffect,
        run_id: "run-7-arbiter",
        role: "arbiter",
        subject_id: 22,
        pool_ids: [22],
        mount_plan: { entries: [artifactEntry], version: 1 },
        brief: "arbiter brief",
      },
      rig.deps,
    );
    const json = JSON.parse(rig.fakeFs.writes.get("/tmp/mc-home/runs/run-7-arbiter.json")!);
    expect(json.mounts.workspace).toBe("/mc/scratch");
    // The artifact root is writable too — it is a genuine rw row in the plan.
    expect(json.mounts.allow_write).toEqual(["/mc/artifacts/ws-e2e", "/mc/scratch", "/mc/logs"]);
    // The cwd must actually be created, not merely named.
    expect(rig.fakeFs.events).toContain("mkdir:/tmp/mc-home/runs/scratch/run-7-arbiter");
  });

  test("read-only plan rows never reach the writable set", async () => {
    const rig = repositoryRig();
    await applyEffect(
      {
        ...spawnEffect,
        mount_plan: {
          entries: [
            artifactEntry,
            {
              ...artifactEntry, access: "ro" as const, destination: "/mc/artifacts/reference",
              logical_id: "artifact:reference", source: "/host/artifacts/reference",
            },
          ],
          repository: repositoryPlan,
          version: 1,
        },
      },
      rig.deps,
    );
    const runJson = JSON.parse(
      rig.fakeFs.writes.get("/tmp/mc-home/runs/run-42-worker.json")!,
    );
    expect(runJson.mounts.allow_write).toEqual(["/mc/artifacts/ws-e2e", "/mc/logs", "/workspace/source"]);
  });

  test("an exact wire role identifier selects the fake behavior before the base role", async () => {
    const effect: Effect = {
      ...spawnEffect,
      run_id: "run-9-strategist",
      role: "strategist(propose)",
      subject_id: null,
      pool_ids: [],
      mount_plan: { entries: [], version: 1 },
      brief: "propose brief",
    };
    const exact = makeRig({
      config: {
        ...testConfig,
        roleBehaviors: {
          ...testConfig.roleBehaviors,
          "strategist(propose)": "/mc/behaviors/strategist-exact.json",
        },
      },
    });
    await applyEffect(effect, exact.deps);
    expect(JSON.parse(exact.fakeFs.writes.get(
      "/tmp/mc-home/runs/run-9-strategist.json",
    )!).harness_config.behavior).toBe("/mc/behaviors/strategist-exact.json");

    const fallback = makeRig();
    await applyEffect({ ...effect, run_id: "run-9-propose-fallback" }, fallback.deps);
    expect(JSON.parse(fallback.fakeFs.writes.get(
      "/tmp/mc-home/runs/run-9-propose-fallback.json",
    )!).harness_config.behavior).toBe("/mc/behaviors/strategist.json");
  });

  // A real (non-fake) agent route projects credentials through the injected
  // CredentialProjector and gets an open network. The fake family stays
  // --network none as spawn-side hygiene: it is deterministic and token-free.
  describe("credentialed agent spawn", () => {
    const claudeEffect: Effect = routedSpawn("claude-sdk", "claude-max", {
      run_id: "run-77-worker",
    });
    const routedConfig = { ...testConfig, agentRunnerRoutes: ["claude-sdk/claude-max", "codex/codex-sub"] };

    test("a Claude launch mounts only private Culler access and retains ordinary network access", async () => {
      const rig = repositoryRig({
        config: routedConfig,
        credentials: {
          project: () => accessProjection("claude-sdk"),
        },
      });
      rig.docker.enqueue(ok("container-id\n"), ok(""));
      await applyEffect(claudeEffect, rig.deps);
      const create = rig.docker.calls[0]!;
      expect(create).not.toContain("--network");
      expect(create).not.toContain("--security-opt");
      for (const [flag, value] of [["--memory", "4g"], ["--cpus", "2"], ["--pids-limit", "512"], ["--workdir", "/workspace/source"]]) {
        expect(create[create.indexOf(flag!) + 1]).toBe(value!);
      }
      expect(create.join(" ")).not.toContain("CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST");
      expect(create.join(" ")).not.toContain("host-minted-access");
      expect(create).toContain("MC_PROVIDER_ACCESS_FILE=/mc/provider-access.json");
      expect(create.join(" ")).not.toContain("ANTHROPIC_API_KEY");
      expect(rig.docker.calls[1]).toEqual(["start", "mc-ws-e2e-run-77-worker"]);
    });

    test("a Codex launch mounts private Culler access without provider credentials", async () => {
      const rig = repositoryRig({
        config: routedConfig,
        credentials: {
          project: () => accessProjection(),
        },
      });
      rig.docker.enqueue(ok("container-id\n"), ok(""));
      await applyEffect(
        routedSpawn("codex", "codex-sub", { run_id: "run-78-worker" }),
        rig.deps,
      );
      expect(rig.fakeFs.writes.get("/tmp/mc-home/runs/provider-access/run-78-worker-run-78-worker.json")).toBe(accessProjection().proxyConfig);
      const create = rig.docker.calls[0]!;
      expect(create).toContain("/tmp/mc-home/runs/provider-access/run-78-worker-run-78-worker.json:/mc/provider-access.json:ro");
	  expect(create).toContain("/tmp/mc-home/sessions/run-78-worker:/mc/session");
      expect(create.join(" ")).not.toContain("CODEX_HOME");
      expect(create.join(" ")).not.toContain("REFRESH_TOKEN");
      expect(create).not.toContain("--network");
      expect(create).not.toContain("--security-opt");
      for (const [flag, value] of [["--memory", "4g"], ["--cpus", "2"], ["--pids-limit", "512"], ["--workdir", "/workspace/source"]]) {
        expect(create[create.indexOf(flag!) + 1]).toBe(value!);
      }
    });

    test("a refused projection spawns nothing and writes nothing", async () => {
      const rig = makeRig({
        config: routedConfig,
        credentials: { project: () => ({ refused: "claude credential lapsed" }) },
      });
      await applyEffect(claudeEffect, rig.deps);
      expect(rig.docker.calls).toEqual([]);
      expect(rig.fakeFs.writes.size).toBe(0);
      expect(rig.logs.some((line) => line.includes("claude credential lapsed"))).toBe(true);
    });

    test("without a projector a routed non-fake spawn refuses before writing or launching", async () => {
      const rig = makeRig({ config: routedConfig });
      await applyEffect(claudeEffect, rig.deps);
      expect(rig.docker.calls).toEqual([]);
      expect(rig.fakeFs.writes.size).toBe(0);
      expect(rig.logs.some((line) => line.includes("runtime credential projector unavailable"))).toBe(true);
    });

    test("the fake family keeps --network none as hygiene", async () => {
      const rig = repositoryRig({
        credentials: { project: () => ({ env: { CLAUDE_CODE_OAUTH_TOKEN: "never" } }) },
      });
      rig.docker.enqueue(ok("container-id\n"), ok(""));
      await applyEffect(spawnEffect, rig.deps);
      const create = rig.docker.calls[0]!;
      expect(create).toContain("--network");
      expect(create).not.toContain("--security-opt");
      expect(create.join(" ")).not.toContain("CLAUDE_CODE");
    });
  });

  test("docker create+start argv: --rm, --network none, exact name, labels, hardening, plan binds, the repository, MC_SPINE", async () => {
    const rig = repositoryRig();
    rig.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect(spawnEffect, rig.deps);
    expect(rig.docker.calls).toEqual([
      [
        "create", "--rm", "--init",
        "--user", "10002:10002", "--cap-drop", "ALL",
        "--pids-limit", "512", "--memory", "4g", "--cpus", "2",
        "--workdir", "/workspace/source",
        "--network", "none",
        "--name", "mc-ws-e2e-run-42-worker",
        "--label", "mc-managed=true",
        "--label", "mc-tier=pipeline",
        "--label", "mc-run-id=run-42-worker",
        "--label", "mc-project=ws-e2e",
        "-v", "/tmp/mc-home/sessions/run-42-worker:/mc/session",
        "-v", "/tmp/mc-home/runs/run-42-worker.json:/mc/run.json:ro",
        "-v", "/tmp/mc-home/logs/runs/run-42-worker:/mc/logs",
        "-v", "/host/behaviors:/mc/behaviors:ro",
        "-v", "/host/runner:/app/src:ro",
        "-e", "HOME=/home/mc-model",
        "-v", "/host/artifacts/ws-e2e:/mc/artifacts/ws-e2e",
        "-v", `${REPOSITORY_ROOT}:/workspace/source`,
        "-v", "mc-spine-test:/mc/spine",
        "-e", "MC_SPINE=/mc/spine/spine.db",
        "-e", "GIT_AUTHOR_NAME=Culler",
        "-e", "GIT_AUTHOR_EMAIL=mc@localhost",
        "-e", "GIT_COMMITTER_NAME=Culler",
        "-e", "GIT_COMMITTER_EMAIL=mc@localhost",
        "mc-fake-e2e:test",
        "bun", "/app/src/agent-runner/main.ts",
      ],
      ["start", "mc-ws-e2e-run-42-worker"],
    ]);
  });

  // The container identity is per Project. The id is a segment of the
  // container name and of the skill store path, so a spawn whose Project is
  // missing or outside `mc project add`'s id pattern is refused before any
  // launch artifact exists.
  describe("the spawn's Project", () => {
    for (const project of [null, undefined, "", "-leading-dash", "has/slash", "has space", "x".repeat(65)]) {
      test(`project ${JSON.stringify(project)} refuses the spawn before any launch file or Docker call`, async () => {
        const rig = makeRig();
        await applyEffect({ ...spawnEffect, project } as unknown as Effect, rig.deps);
        expect(rig.docker.calls).toEqual([]);
        expect(rig.fakeFs.events).toEqual([]);
        expect(rig.logs.some((l) => l.includes("spawn refused") && l.includes("fails the Project id pattern"))).toBe(true);
      });
    }

    test("a spawn without an operator skill store directory is refused (every role has one)", async () => {
      const rig = makeRig();
      const { skill_store_dir: _omitted, ...stripped } = spawnEffect as Extract<Effect, { action: "spawn" }>;
      await applyEffect(stripped as unknown as Effect, rig.deps);
      expect(rig.docker.calls).toEqual([]);
      expect(rig.fakeFs.events).toEqual([]);
      expect(rig.logs.some((l) => l.includes("spawn refused") && l.includes("no operator skill store directory"))).toBe(true);
    });

    test("the agent container is mc-<project>-<run_id>, labelled with the Project beside tier and run id", async () => {
      const rig = repositoryRig();
      rig.docker.enqueue(ok("container-id\n"), ok(""));
      await applyEffect({ ...spawnEffect, project: "repo.v2", skill_store_dir: "skills/projects/repo.v2/worker" }, rig.deps);
      const create = rig.docker.calls[0]!;
      expect(create.slice(0, 2)).toEqual(["create", "--rm"]);
      expect(create[create.indexOf("--name") + 1]).toBe("mc-repo.v2-run-42-worker");
      const labels = create.flatMap((arg, i) => (create[i - 1] === "--label" ? [arg] : []));
      expect(labels).toEqual(["mc-managed=true", "mc-tier=pipeline", "mc-run-id=run-42-worker", "mc-project=repo.v2"]);
      expect(rig.docker.calls[1]).toEqual(["start", "mc-repo.v2-run-42-worker"]);
      const runJson = JSON.parse(rig.fakeFs.writes.get("/tmp/mc-home/runs/run-42-worker.json")!);
      expect(runJson.project).toBe("repo.v2");
    });
  });

  test("an unsafe configured Git identity refuses before launch artifacts or Docker", async () => {
    const rig = makeRig({
      config: { ...testConfig, gitAuthorEmail: "mc@example.test\nforged" },
    });
    await applyEffect(spawnEffect, rig.deps);
    expect(rig.fakeFs.events).toEqual([]);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.logs).toContain(
      "spawn refused: configured Git author name/email are missing or unsafe (fail-closed)",
    );
  });

  test("a selected Project Git identity atomically overrides the global identity", async () => {
    const rig = repositoryRig();
    rig.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect({
      ...spawnEffect,
      git_identity_override: {
        author_name: "Project Bot",
        author_email: "bot@example.test",
      },
    }, rig.deps);
    const create = rig.docker.calls[0]!;
    expect(create).toEqual(expect.arrayContaining([
      "-e", "GIT_AUTHOR_NAME=Project Bot",
      "-e", "GIT_AUTHOR_EMAIL=bot@example.test",
      "-e", "GIT_COMMITTER_NAME=Project Bot",
      "-e", "GIT_COMMITTER_EMAIL=bot@example.test",
    ]));
    expect(create.join("\n")).not.toContain("Culler");
    expect(create.join("\n")).not.toContain("mc@localhost");
  });

  test("a credential environment cannot replace the Project identity", async () => {
    const rig = repositoryRig({
      config: { ...testConfig, agentRunnerRoutes: ["codex/chatgpt"] },
      credentials: { project: () => ({ ...accessProjection(), env: { GIT_AUTHOR_NAME: "credential-forgery" } }) },
    });
    await expect(applyEffect(routedSpawn("codex", "chatgpt"), rig.deps)).rejects.toThrow("provider access configuration");
    expect(rig.docker.calls).toEqual([]);
  });

  test("a stripped or malformed Project Git identity decision never falls back", async () => {
    for (const effect of [
      (() => {
        const { git_identity_override: _stripped, ...stripped } = spawnEffect;
        return stripped as Effect;
      })(),
      {
        ...spawnEffect,
        git_identity_override: { author_name: "Project Bot", author_email: "" },
      } as Effect,
    ]) {
      const rig = makeRig();
      await applyEffect(effect, rig.deps);
      expect(rig.fakeFs.events).toEqual([]);
      expect(rig.docker.calls).toEqual([]);
    }
  });

  test("the attested profile home mounts at /home/agent and is rechecked on both launch legs", async () => {
    const rig = repositoryRig();
    rig.mc.enqueue(ok("{}\n"), ok("{}\n"));
    rig.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect({
      ...spawnEffect,
      mount_plan: {
        ...spawnEffect.mount_plan,
        tool_home: {
          canonical: "/tmp/mc-home/state/projects/primary/home",
          device: "8",
          inode: "9",
          owner_uid: 501,
        },
      },
    }, rig.deps);
    const create = rig.docker.calls[0]!;
    expect(create).toEqual(expect.arrayContaining([
      "--mount", "type=bind,src=/tmp/mc-home/state/projects/primary/home,dst=/home/agent",
      "-e", "HOME=/home/agent",
    ]));
    expect(create).not.toContain("HOME=/home/mc-model");
    expect(rig.mc.calls).toEqual([
      ["__mount-recheck", "/tmp/mc-home/runs/run-42-worker.mounts.json"],
      ["__mount-recheck", "/tmp/mc-home/runs/run-42-worker.mounts.json"],
    ]);
  });

  test("a malformed synthetic home refuses before launch artifacts or Docker", async () => {
    const rig = makeRig();
    await applyEffect({
      ...spawnEffect,
      mount_plan: {
        ...spawnEffect.mount_plan,
        tool_home: {
          canonical: "/tmp/mc-home/../operator",
          device: "8",
          inode: "9",
          owner_uid: 501,
        },
      },
    }, rig.deps);
    expect(rig.fakeFs.events).toEqual([]);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.logs).toContain("spawn refused: synthetic home descriptor is malformed (fail-closed)");
  });

  test("the private runtime preserves the Project home and separates role state", async () => {
    const image = `sha256:${"c".repeat(64)}`;
    const rig = repositoryRig({ config: { ...testConfig, runtimeVolume: "runtime-test", projectImage: "project-test" } });
    const runMc = rig.deps.runMc;
    rig.deps.runMc = async (args, input) => args[0] === "environment" && args[1] === "resolve"
      ? ok(JSON.stringify({ image_digest: image, configuration: { secrets: [] } })) : runMc(args, input);
    rig.docker.enqueue(ok(JSON.stringify([{ Id: image }])), ok("container-id\n"), ok(""));
    await applyEffect({
      ...spawnEffect,
      mount_plan: { ...spawnEffect.mount_plan, tool_home: {
        canonical: "/tmp/mc-home/state/projects/primary/home", device: "8", inode: "9", owner_uid: 501,
      } },
    }, rig.deps);
    const create = rig.docker.calls.find(args => args[0] === "create")!;
    expect(create).toContain("HOME=/home/agent");
    expect(create).toContain("type=bind,src=/tmp/mc-home/state/projects/primary/home,dst=/home/agent");
    for (const directory of [".agents", ".claude", ".codex"]) {
      expect(create).toContain(`/home/agent/${directory}:rw,uid=10002,gid=10002,mode=0700`);
    }
    expect(create).toContain("runtime-test:/opt/culler-runtime:ro");
    expect(create).toContain("/opt/culler-runtime/bin/bun");
    expect(create).toContain(image);
  });

  test("an RO plan entry binds with the :ro flag; an empty plan skips the rechecks", async () => {
    const rig = makeRig();
    await applyEffect(
      {
        ...spawnEffect,
        mount_plan: {
          entries: [{ ...artifactEntry, access: "ro", destination: "/mc/artifacts/reference", logical_id: "artifact:reference", source: "/host/artifacts/reference" }],
          version: 1,
        },
      },
      rig.deps,
    );
    const createArgv = rig.docker.calls[0]!;
    expect(createArgv).toContain("/host/artifacts/reference:/mc/artifacts/reference:ro");
    expect(rig.mc.calls).toHaveLength(2);

    const emptyRig = makeRig();
    await applyEffect(
      { ...spawnEffect, mount_plan: { entries: [], version: 1 } },
      emptyRig.deps,
    );
    expect(emptyRig.mc.calls).toEqual([]);
    expect(emptyRig.docker.calls.map((argv) => argv[0])).toEqual(["create", "start"]);
    expect(emptyRig.docker.calls[0]!.join(" ")).not.toContain("/workspace/source");
  });

	test("a Git seeding source is materialized before create and only the committed projection is bound", async () => {
		const rig = makeRig();
		const effect: Effect = {
			...spawnEffect,
			run_id: "run-10-proposer",
			role: "strategist(propose)",
			subject_id: null,
			project: "ws-e2e",
			pool_ids: [],
			brief: "Git-backed proposer brief",
			mount_plan: {
				version: 1,
				entries: [],
				seeding_projections: [{
					git_cover_kind: "seeding-source-git-cover",
					object_format: "sha1",
					projection_kind: "seeding-committed-projection",
					target_ref: "HEAD",
					workspace_root: { canonical: "/host/repo", device: "7", inode: "11", owner_uid: 501 },
					project_id: "repo",
				}],
			},
		};
		await applyEffect(effect, rig.deps);
		expect(rig.mc.calls).toEqual([
			["__mount-recheck", "/tmp/mc-home/runs/run-10-proposer.mounts.json"],
			["__mount-recheck", "/tmp/mc-home/runs/run-10-proposer.mounts.json"],
		]);
		const setup = rig.docker.calls[0]!;
		expect(setup).toEqual(expect.arrayContaining([
			"run", "--rm", "-v", "/host/repo:/repo/source:ro",
			"-v", "/tmp/mc-home/runs/seeding/run-10-proposer/repo:/repo/projection",
			"__setup-seeding-projection", "/mc/setup.json",
		]));
		const create = rig.docker.calls[1]!;
		expect(create).toContain("/tmp/mc-home/runs/seeding/run-10-proposer/repo:/workspace/seeding/repo/source:ro");
		expect(create).toContain("/tmp/mc-home/runs/seeding/run-10-proposer/repo/.git:/workspace/seeding/repo/source/.git:ro");
		expect(create.join(" ")).not.toContain("/host/repo:/workspace/seeding");
		expect(rig.fakeFs.events).toContain("write:/tmp/mc-home/runs/run-10-proposer.seeding-0.json");
		expect(rig.fakeFs.events).toContain("rm:/tmp/mc-home/runs/run-10-proposer.seeding-0.json");
		// The projections are read-only inputs; the proposer still runs from scratch.
		const runJson = JSON.parse(rig.fakeFs.writes.get("/tmp/mc-home/runs/run-10-proposer.json")!);
		expect(runJson.mounts.workspace).toBe("/mc/scratch");
		expect(runJson.mounts.allow_write).toEqual(["/mc/scratch", "/mc/logs"]);
	});

	test("only a role that proposes work or selects work receives a seeding projection", async () => {
		const projection = {
			git_cover_kind: "seeding-source-git-cover",
			object_format: "sha1",
			projection_kind: "seeding-committed-projection",
			target_ref: "HEAD",
			workspace_root: { canonical: "/host/repo", device: "7", inode: "11", owner_uid: 501 },
			project_id: "repo",
		};
		const cases: { role: string; subject_id: number | null; bound: boolean }[] = [
			{ role: "arbiter", subject_id: null, bound: true },
			{ role: "arbiter", subject_id: 7, bound: false },
			{ role: "strategist(propose)", subject_id: null, bound: true },
			{ role: "strategist(propose)", subject_id: 7, bound: false },
			{ role: "worker", subject_id: 7, bound: false },
		];
		for (const c of cases) {
			const rig = makeRig();
			const effect: Effect = {
				...spawnEffect,
				run_id: "run-11-reader",
				role: c.role,
				subject_id: c.subject_id,
				project: "ws-e2e",
				pool_ids: c.role === "arbiter" ? [7, 8] : [],
				brief: "committed tree reader brief",
				mount_plan: { version: 1, entries: [], seeding_projections: [projection] },
			};
			await applyEffect(effect, rig.deps);
			const create = rig.docker.calls.find((call) => call[0] === "create");
			if (!c.bound) {
				expect(create).toBeUndefined();
				expect(rig.mc.calls.some((call) => call[0] === "run" && call[1] === "infra-failure")).toBe(true);
				continue;
			}
			expect(create).toContain("/tmp/mc-home/runs/seeding/run-11-reader/repo:/workspace/seeding/repo/source:ro");
			expect(create!.some((arg) => arg.includes("/mc/wiki.json"))).toBe(false);
			expect(rig.mc.calls).toEqual([
				["__mount-recheck", "/tmp/mc-home/runs/run-11-reader.mounts.json"],
				["__mount-recheck", "/tmp/mc-home/runs/run-11-reader.mounts.json"],
			]);
			expect([...rig.fakeFs.writes.keys()].some((path) => path.endsWith(".wiki.json"))).toBe(false);
			expect(rig.docker.calls.some((call) => call[0] === "start")).toBe(true);
			expect(rig.mc.calls.some((call) => call[0] === "run" && call[1] === "infra-failure")).toBe(false);
			const envelope = JSON.parse(rig.fakeFs.writes.get("/tmp/mc-home/runs/run-11-reader.json")!);
			expect(envelope.subject_id).toBe(c.subject_id);
			expect(envelope.pool_ids).toEqual(effect.pool_ids);
			expect(envelope.mounts.wiki).toBeUndefined();
		}
	});

  // The plan namespace and the namespace the resident composes for itself
  // share the /mc root, and this predicate is the only thing keeping them
  // disjoint: the mount planner's overlap scan never sees the resident's own
  // binds. So every neighbour of the admitted prefix is pinned here, in both
  // directions.
  test("the /mc class namespace is admitted by exact prefix and nothing else on /mc is", async () => {
    for (const destination of [
      "/mc/artifacts/art",
      "/mc/artifacts/art/nested/suffix",
    ]) {
      const rig = makeRig();
      await applyEffect(
        {
          ...spawnEffect,
          mount_plan: { entries: [{ ...artifactEntry, destination }], version: 1 },
        },
        rig.deps,
      );
      expect(rig.docker.calls.length).toBeGreaterThan(0);
    }

    for (const destination of [
      // Sibling-prefix.
      "/mc/artifactsx/art",
      // The workspace is the resident's own bind from the plan's repository.
      "/workspace/source",
      "/workspace",
      // The retired reference plane: its prefix is now an ordinary refusal.
      "/mc/references/ref",
      // The bare root: a class namespace is a prefix, never a bindable cell.
      "/mc/artifacts",
      // Everything the RESIDENT composes for itself, which a plan must never
      // reach now that it shares the root. /mc/run.json is the sharpest: it is
      // the run's own read-only identity envelope, and scope follows from it.
      "/mc/run.json",
      "/mc/session",
      "/mc/spine",
      "/mc/outputs",
      "/mc/scratch",
      "/mc/behaviors",
      "/mc/private/attachments/out",
      "/mc/codex/auth.json",
      // Traversal: `startsWith` alone would admit this, and Docker cleans it
      // straight onto the identity envelope above.
      "/mc/artifacts/../run.json",
      "/mc/artifacts/./../session",
    ]) {
      const rig = makeRig();
      await applyEffect(
        {
          ...spawnEffect,
          mount_plan: { entries: [{ ...artifactEntry, destination }], version: 1 },
        },
        rig.deps,
      );
      expect(rig.docker.calls, `destination ${destination} must be refused`).toEqual([]);
      expect(rig.logs.some((l) => l.includes("spawn refused"))).toBe(true);
    }
  });

  test("a spawn without a mount plan is refused before any effect (fail-closed)", async () => {
    const rig = makeRig();
    const { mount_plan: _dropped, ...withoutPlan } = spawnEffect as Effect & { mount_plan: unknown };
    await applyEffect(withoutPlan as Effect, rig.deps);
    expect(rig.fakeFs.events).toEqual([]);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.logs.some((l) => l.includes("spawn refused") && l.includes("no explicit mount plan"))).toBe(true);
  });

  test("a plan entry unsafe for -v grammar is refused before any effect", async () => {
    const rig = makeRig();
    await applyEffect(
      {
        ...spawnEffect,
        mount_plan: { entries: [{ ...artifactEntry, source: "/host/evil:path" }], version: 1 },
      },
      rig.deps,
    );
    expect(rig.docker.calls).toEqual([]);
    expect(rig.logs.some((l) => l.includes("spawn refused") && l.includes("colon-free"))).toBe(true);
  });

  test("recheck drift before create: no container, launch files removed", async () => {
    const rig = repositoryRig();
    rig.mc.enqueue(fail(1, "mount recheck: entry 0 (artifact:ws-e2e): source mode grant changed"));
    await applyEffect(spawnEffect, rig.deps);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.logs.some((l) => l.includes("mount recheck refused before create (exit 1)"))).toBe(true);
    expect(rig.fakeFs.events.slice(-2)).toEqual([
      "rm:/tmp/mc-home/runs/run-42-worker.json",
      "rm:" + PLAN_PATH,
    ]);
  });

	// Same hazard as the reap arm, one step sharper: this is an ABORT path, so
	// a removal that throws would abort the abort — leaving the spawn half
	// unwound and taking the tick with it.
	test("an unremovable launch file does not abort the abort", async () => {
		const rig = repositoryRig();
		rig.mc.enqueue(fail(1, "mount recheck: entry 0 (artifact:ws-e2e): source mode grant changed"));
		rig.fakeFs.failRm("/tmp/mc-home/runs/run-42-worker.json", "EACCES: permission denied");
		await applyEffect(spawnEffect, rig.deps);
		expect(rig.docker.calls).toEqual([]);
		// The plan sibling still went, even though the envelope removal refused.
		expect(rig.fakeFs.events.slice(-2)).toEqual([
			"rm:/tmp/mc-home/runs/run-42-worker.json",
			"rm:" + PLAN_PATH,
		]);
		expect(rig.logs.some((l) =>
			l.includes("spawn run-42-worker") && l.includes("could not remove") && l.includes("EACCES")
		)).toBe(true);
	});

  test("recheck drift after create removes the unstarted container", async () => {
    const rig = repositoryRig();
    rig.mc.enqueue(ok("{}"), fail(1, "source is a different filesystem object"));
    rig.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect(spawnEffect, rig.deps);
    expect(rig.docker.calls.map((argv) => argv[0])).toEqual(["create", "rm"]);
    expect(rig.docker.calls[1]).toEqual(["rm", "mc-ws-e2e-run-42-worker"]);
    expect(rig.logs.some((l) => l.includes("mount recheck refused after create (exit 1)"))).toBe(true);
    expect(rig.fakeFs.events.slice(-2)).toEqual([
      "rm:/tmp/mc-home/runs/run-42-worker.json",
      "rm:" + PLAN_PATH,
    ]);
  });

  test("docker create failure removes the launch files and never starts", async () => {
    const rig = repositoryRig();
    rig.docker.enqueue(fail(125, "docker daemon down"));
    await applyEffect(spawnEffect, rig.deps);
    expect(rig.docker.calls.map((argv) => argv[0])).toEqual(["create"]);
    expect(rig.logs.some((l) => l.includes("docker create failed (exit 125)"))).toBe(true);
    expect(rig.fakeFs.events.slice(-2)).toEqual([
      "rm:/tmp/mc-home/runs/run-42-worker.json",
      "rm:" + PLAN_PATH,
    ]);
    expect(rig.mc.calls.filter((call) => call[1] === "infra-failure")).toEqual([[
      "run", "infra-failure", "run-42-worker", "--condition",
      "spawn run-42-worker: docker create failed (exit 125): docker daemon down",
      "--source", "resident", "--cause", "unknown",
    ]]);
  });

  test("a failed start removes the container before the infrastructure report", async () => {
    const rig = repositoryRig();
    rig.docker.enqueue(ok("container-id"), fail(125, "runtime unavailable"), ok(""));
    const calls: string[] = [];
    const docker = rig.deps.docker;
    const runMc = rig.deps.runMc;
    rig.deps.docker = async (argv) => { calls.push(argv[0]!); return docker(argv); };
    rig.deps.runMc = async (argv) => { calls.push(argv[1]!); return runMc(argv); };
    await applyEffect(spawnEffect, rig.deps);
    expect(rig.docker.calls.map((argv) => argv[0])).toEqual(["create", "start", "rm"]);
    expect(rig.docker.calls[2]).toEqual(["rm", "-f", "mc-ws-e2e-run-42-worker"]);
    expect(calls.slice(-2)).toEqual(["rm", "infra-failure"]);
    const report = rig.mc.calls.find((argv) => argv[1] === "infra-failure")!;
    expect(report[4]).toContain("docker start failed (exit 125): runtime unavailable");
    expect(report.slice(-4)).toEqual(["--source", "resident", "--cause", "unknown"]);
  });

  test("a generic projection refusal retains an unknown cause", async () => {
    const rig = makeRig({ credentials: { project: () => ({ refused: "projector unavailable" }) } });
    await applyEffect(routedSpawn("claude-sdk", "claude"), rig.deps);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.mc.calls).toHaveLength(2);
    expect(rig.mc.calls[1]![4]).toContain("projector unavailable");
    expect(rig.mc.calls[1]!.slice(-4)).toEqual(["--source", "resident", "--cause", "unknown"]);
  });

  test("a refused launch check reports a connection cause with the refusal as evidence", async () => {
    const rig = makeRig();
    rig.mc.enqueue(fail(1, "The connection changed after dispatch.\n"));
    await applyEffect(routedSpawn("claude-sdk", "claude"), rig.deps);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.mc.calls.map((call) => call[1])).toEqual(["launch-check", "infra-failure"]);
    expect(rig.mc.calls[1]!.slice(-8)).toEqual([
      "--source", "resident", "--cause", "connection",
      "--evidence", "The connection changed after dispatch.", "--operator-action", "",
    ]);
  });

  test("a credential refusal forwards explicit evidence and the required operator action", async () => {
    const rig = makeRig({ credentials: { project: () => ({
      refused: "credential absent",
      cause: "credentials",
      evidence: "The credential store contains no grant.",
      operator_action: "Restore the credentials.",
    }) } });
    await applyEffect(routedSpawn("claude-sdk", "claude"), rig.deps);
    expect(rig.mc.calls).toHaveLength(2);
    expect(rig.mc.calls[1]!.slice(5)).toEqual([
      "--source", "resident", "--cause", "credentials",
      "--evidence", "The credential store contains no grant.",
      "--operator-action", "Restore the credentials.",
    ]);
  });

  test("a long execution error fits the infrastructure report limit", async () => {
    const rig = repositoryRig();
    rig.docker.enqueue(fail(125, "x".repeat(9000)));
    await applyEffect(spawnEffect, rig.deps);
    const reports = rig.mc.calls.filter((call) => call[1] === "infra-failure");
    expect(reports).toHaveLength(1);
    expect(reports[0]![4]).toHaveLength(2048);
  });

  test("an unassigned proposer refusal reports its infrastructure failure", async () => {
    const rig = makeRig();
    await applyEffect({ ...spawnEffect, subject_id: null, role: "strategist(propose)", brief: "" } as Effect, rig.deps);
    expect(rig.mc.calls.filter(call => call[1] === "infra-failure")).toHaveLength(1);
  });

  test("a role with no mapped behavior is refused before any effect", async () => {
    const rig = makeRig();
    await applyEffect({ ...spawnEffect, role: "gardener" } as Effect, rig.deps);
    expect(rig.fakeFs.events).toEqual([]);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.logs.some((l) => l.includes("spawn refused") && l.includes("gardener"))).toBe(true);
  });

  test("a run_id outside the container-name charset is refused", async () => {
    const rig = makeRig();
    await applyEffect({ ...spawnEffect, run_id: "../etc; rm -rf" } as Effect, rig.deps);
    expect(rig.fakeFs.events).toEqual([]);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.logs.some((l) => l.includes("spawn refused") && l.includes("charset"))).toBe(true);
  });
});

describe("reap effect", () => {
	// An operator interrupt reaches the resident as an ordinary reap carrying
	// reason "interrupted" — the shape `mc dispatch` actually emits. This test
	// used to drive an `interrupt` ACTION, which nothing in production has ever
	// produced, so the stop it proved was a stop no operator ever got.
	test("an interrupt reap stops the exact run container and removes its envelope", async () => {
		const rig = makeRig();
		rig.docker.enqueue(ok(""));
		await applyEffect(
			{ action: "reap", project: "ws-e2e", run_id: "run-42-worker", reason: "interrupted", stop_container: true },
			rig.deps,
		);
		expect(rig.docker.calls).toEqual([["stop", "mc-setup-run-42-worker"], ["stop", "mc-ws-e2e-run-42-worker"]]);
		expect(rig.fakeFs.events).toEqual([
			"rm:/tmp/mc-home/runs/run-42-worker.json",
			"rm:/tmp/mc-home/runs/run-42-worker.mounts.json",
			"rm:/tmp/mc-home/runs/seeding/run-42-worker",
			"rm:/tmp/mc-home/runs/scratch/run-42-worker",
		]);
	});

  // The agent container is `mc-<project>-<run_id>`, so the stop is addressed
  // by the reap effect's own Project, never by a Project-free name.
  test("stops the Project-named container of the reap effect's Project", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok(""), ok(""));
    await applyEffect({ action: "reap", project: "repo.v2", run_id: "run-42-worker", stop_container: true }, rig.deps);
    expect(rig.docker.calls).toEqual([["stop", "mc-setup-run-42-worker"], ["stop", "mc-repo.v2-run-42-worker"]]);
  });

  test("a reap whose Project fails the id pattern stops nothing and removes nothing", async () => {
    for (const project of [undefined, "", "has/slash", "x".repeat(65)]) {
      const rig = makeRig();
      await applyEffect({ action: "reap", project, run_id: "run-42-worker", stop_container: true } as unknown as Effect, rig.deps);
      expect(rig.docker.calls).toEqual([]);
      expect(rig.fakeFs.events).toEqual([]);
      expect(rig.logs.some((l) => l.includes("reap refused") && l.includes("fails the Project id pattern"))).toBe(true);
    }
  });

  test("stops the exact-named container when stop_container is set", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok(""));
    await applyEffect({ action: "reap", project: "ws-e2e", run_id: "run-42-worker", stop_container: true }, rig.deps);
		expect(rig.docker.calls).toEqual([["stop", "mc-setup-run-42-worker"], ["stop", "mc-ws-e2e-run-42-worker"]]);
  });

  test("removes the launch envelope and plan sibling with the container", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok(""));
    await applyEffect({ action: "reap", project: "ws-e2e", run_id: "run-42-worker", stop_container: true }, rig.deps);
    expect(rig.fakeFs.events).toEqual([
      "rm:/tmp/mc-home/runs/run-42-worker.json",
      "rm:/tmp/mc-home/runs/run-42-worker.mounts.json",
			"rm:/tmp/mc-home/runs/seeding/run-42-worker",
			"rm:/tmp/mc-home/runs/scratch/run-42-worker",
    ]);
  });

  test("no docker call and no fs effect when stop_container is absent", async () => {
    const rig = makeRig();
    await applyEffect({ action: "reap", project: "ws-e2e", run_id: "run-42-worker" }, rig.deps);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.fakeFs.events).toEqual([]);
  });

  test("a run_id outside the charset is refused (never spliced into argv)", async () => {
    const rig = makeRig();
    await applyEffect({ action: "reap", run_id: "x $(reboot)", stop_container: true }, rig.deps);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.fakeFs.events).toEqual([]);
    expect(rig.logs.some((l) => l.includes("reap refused"))).toBe(true);
  });

	// A removal that throws would reach the tick loop's outermost catch, so the
	// reap's remaining cleanups would never run and `tickComplete` would never
	// fire. Disposable execution residue must not be able to cancel a committed
	// decision's effect arm.
	test("an unremovable residue directory is logged and the reap continues", async () => {
		const rig = makeRig();
		rig.docker.enqueue(ok(""));
		rig.fakeFs.failRm(
			"/tmp/mc-home/runs/seeding/run-42-worker",
			"EACCES: permission denied, rm '/tmp/mc-home/runs/seeding/run-42-worker'",
		);
		await applyEffect({ action: "reap", project: "ws-e2e", run_id: "run-42-worker", stop_container: true }, rig.deps);
		// Every later removal still ran — the failure did not truncate the arm.
		// Retries of one path collapse: what this test pins is the order and
		// completeness of the paths, and the attempt budget is pinned by the two
		// tests above.
		expect(distinctInOrder(rig.fakeFs.events)).toEqual([
			"rm:/tmp/mc-home/runs/run-42-worker.json",
			"rm:/tmp/mc-home/runs/run-42-worker.mounts.json",
			"rm:/tmp/mc-home/runs/seeding/run-42-worker",
			"rm:/tmp/mc-home/runs/scratch/run-42-worker",
		]);
		// And the operator can still find out, from the resident log alone,
		// which run leaked what and why.
		expect(rig.logs.some((l) =>
			l.includes("run-42-worker") && l.includes("seeding") && l.includes("EACCES")
		)).toBe(true);
	});

	/** Collapses consecutive repeats of the same event, so an assertion about
	 * WHICH paths a failing arm still reached is not also an assertion about how
	 * many times each was attempted. */
	const distinctInOrder = (events: string[]): string[] =>
		events.filter((e, i) => e !== events[i - 1]);

	test("residue the VM has not finished releasing is removed on a retry, not leaked", async () => {
		// The reap issues `docker stop` and removes within milliseconds, so it
		// can ask to unlink a tree the VM is still unmounting. That lands as a
		// transient EACCES, which Node's own fs.rm retry never covers, so the
		// retry has to be the resident's.
		const rig = makeRig();
		rig.docker.enqueue(ok(""));
		rig.fakeFs.failRmTimes(
			"/tmp/mc-home/runs/seeding/run-42-worker",
			"EACCES: permission denied, rm '/tmp/mc-home/runs/seeding/run-42-worker'",
			1,
		);
		await applyEffect({ action: "reap", project: "ws-e2e", run_id: "run-42-worker", stop_container: true }, rig.deps);

		// The real consumer: nothing is left behind for a sweeper to find.
		expect(rig.fakeFs.rmAttempts.get("/tmp/mc-home/runs/seeding/run-42-worker")).toBe(2);
		// A transient failure that cleared is not the operator's problem, so it
		// must not be reported as a leak.
		expect(rig.logs.some((l) => l.includes("could not remove"))).toBe(false);
		// Bounded, and it waited before retrying rather than spinning.
		expect(rig.sleeps).toEqual([DISCARD_RETRY_MS[0]]);
	});

	test("a permanently unremovable path gives up after a bounded budget and logs once", async () => {
		// The guard on the retry: it must not turn one logged leak into an
		// unbounded in-tick loop, and it must not log per attempt.
		const rig = makeRig();
		rig.docker.enqueue(ok(""));
		rig.fakeFs.failRm(
			"/tmp/mc-home/runs/seeding/run-42-worker",
			"EACCES: permission denied",
		);
		await applyEffect({ action: "reap", project: "ws-e2e", run_id: "run-42-worker", stop_container: true }, rig.deps);

		expect(rig.fakeFs.rmAttempts.get("/tmp/mc-home/runs/seeding/run-42-worker"))
			.toBe(DISCARD_RETRY_MS.length + 1);
		expect(rig.logs.filter((l) => l.includes("could not remove")).length).toBe(1);
		// And the arm still ran to the end.
		expect(rig.fakeFs.events.filter((e) => e.startsWith("rm:")).at(-1))
			.toBe("rm:/tmp/mc-home/runs/scratch/run-42-worker");
	});

	test("a failed envelope removal does not strand the residue removals either", async () => {
		const rig = makeRig();
		rig.docker.enqueue(ok(""));
		rig.fakeFs.failRm("/tmp/mc-home/runs/run-42-worker.json", "EACCES: permission denied");
		await applyEffect({ action: "reap", project: "ws-e2e", run_id: "run-42-worker", stop_container: true }, rig.deps);
		expect(distinctInOrder(rig.fakeFs.events)).toEqual([
			"rm:/tmp/mc-home/runs/run-42-worker.json",
			"rm:/tmp/mc-home/runs/run-42-worker.mounts.json",
			"rm:/tmp/mc-home/runs/seeding/run-42-worker",
			"rm:/tmp/mc-home/runs/scratch/run-42-worker",
		]);
	});

  test("a failed stop preserves the lease and launch files until a successful retry", async () => {
    const rig = makeRig();
    const effect: Effect = {
      action: "reap", project: "ws-e2e", run_id: "run-42-worker", stop_container: true,
    };
    rig.docker.enqueue(ok(""), fail(1, "daemon unavailable"),
      ok(presentChild("run-42-worker", "mc-ws-e2e", "running")));
    await applyEffect(effect, rig.deps);
    expect(rig.mc.calls).toEqual([]);
    expect(rig.fakeFs.events).toEqual([]);
    expect(rig.logs.some((line) => line.includes("remains running"))).toBe(true);

    rig.docker.enqueue(ok(""), ok(""));
    await applyEffect(effect, rig.deps);
    expect(rig.mc.calls).toEqual([["run", "stopped", "run-42-worker"]]);
    expect(rig.fakeFs.events).toContain("rm:/tmp/mc-home/runs/run-42-worker.json");
  });

  test("an unavailable inspect cannot confirm container absence", async () => {
    for (const result of [fail(1, "Cannot connect to the Docker daemon"), fail(2, "unavailable"), ok("not JSON")]) {
      const rig = makeRig();
      rig.docker.enqueue(fail(1, "stop failed"), result, ok(""));
      await applyEffect({ action: "reap", project: "ws-e2e", run_id: "run-42-worker", stop_container: true }, rig.deps);
      expect(rig.mc.calls).toEqual([]);
      expect(rig.fakeFs.events).toEqual([]);
    }
  });

  test("a confirmed inactive container permits the stop acknowledgement", async () => {
    for (const status of ["created", "exited", "dead"]) {
      const rig = makeRig();
      rig.docker.enqueue(fail(1, "stop refused"), ok(presentChild("run-42-worker", "mc-setup", status)), ok(""));
      await applyEffect({ action: "reap", project: "ws-e2e", run_id: "run-42-worker", stop_container: true }, rig.deps);
      expect(rig.mc.calls).toEqual([["run", "stopped", "run-42-worker"]]);
    }
  });

  test("a failed stop acknowledgement retains files and permits another acknowledgement", async () => {
    const rig = makeRig();
    const effect: Effect = {
      action: "reap", project: "ws-e2e", run_id: "run-42-worker", stop_container: true,
    };
    rig.mc.enqueue(fail(2, "helper unavailable"), ok("{}"));
    await applyEffect(effect, rig.deps);
    expect(rig.fakeFs.events).toEqual([]);
    await applyEffect(effect, rig.deps);
    expect(rig.mc.calls).toEqual([
      ["run", "stopped", "run-42-worker"], ["run", "stopped", "run-42-worker"],
    ]);
    expect(rig.fakeFs.events).toContain("rm:/tmp/mc-home/runs/run-42-worker.json");
  });

  test("docker stop failure (already gone) is logged, not thrown — envelope still removed", async () => {
    const rig = makeRig();
    rig.docker.enqueue(fail(1, "No such container"), fail(1, "Error: No such container: mc-setup-run-42-worker"));
    await applyEffect({ action: "reap", project: "ws-e2e", run_id: "run-42-worker", stop_container: true }, rig.deps);
	expect(rig.logs.some((l) => l.includes("docker stop mc-setup-run-42-worker exited 1"))).toBe(true);
    expect(rig.fakeFs.events).toEqual([
      "rm:/tmp/mc-home/runs/run-42-worker.json",
      "rm:/tmp/mc-home/runs/run-42-worker.mounts.json",
			"rm:/tmp/mc-home/runs/seeding/run-42-worker",
			"rm:/tmp/mc-home/runs/scratch/run-42-worker",
    ]);
    expect(rig.mc.calls).toEqual([["run", "stopped", "run-42-worker"]]);
  });
});

describe("config sanity", () => {
  test("test config maps every skeleton role", () => {
    for (const role of ["arbiter", "worker", "verifier", "scribe", "packager", "strategist"]) {
      expect(testConfig.roleBehaviors[role]).toBeDefined();
    }
  });
});

// ---------------------------------------------------------------------------
// The setuid gate — the class divergence guard.
//
// The setuid gate depends on `no-new-privileges` being ABSENT from the AGENT
// container, so the setuid `mc` binary can raise privilege to reach the spine.
// The repository class is the opposite: it never touches the spine, so it sets
// the flag and is hardened by it.
//
// Nothing else asserts the two classes disagree. A refactor that hoists "the
// hardening flags" into one shared envelope builder would silently add
// `no-new-privileges` to the agent container and kill the setuid gate, with
// every existing test still green. This is the tripwire for that refactor.
// ---------------------------------------------------------------------------

describe("container class hardening divergence", () => {
	const NNP = "no-new-privileges=true";

	test("the agent container omits no-new-privileges so the setuid gate survives", async () => {
		const rig = repositoryRig();
		rig.docker.enqueue(ok("container-id"));
		rig.docker.enqueue(ok(""));
		await applyEffect(spawnEffect, rig.deps);

		const create = rig.docker.calls.find((c) => c[0] === "create");
		expect(create).toBeDefined();
		expect(create).not.toContain(NNP);
	});

	test("the repository container sets no-new-privileges", async () => {
		const rig = makeRig();
		await runExecution({
			run_id: "0123456789abcdef", operation: "apply", task_id: 42, project: "ws-e2e",
			workspace_root: "/host/workspace", destination_branch: "main", deadline_ms: 600_000,
		}, rig.deps, {
			signal: new AbortController().signal, checkBeforeContainer: async () => true, graceRemainingMs: () => 0,
		});

		const run = rig.docker.calls.find((c) => c[0] === "run");
		expect(run).toBeDefined();
		expect(run).toContain(NNP);
	});
});

describe("homie tier (lease-free)", () => {
  const cid = "c".repeat(64);
  const inspectedCreated = (rig: ReturnType<typeof makeRig>) =>
    (_argv: string[], calls: string[][]) => {
      const create = calls.find((call) => call[0] === "create");
      if (create === undefined) throw new Error("missing Homie create call");
      return ok(JSON.stringify([homieInspectFromCreate(
        create,
        cid,
        rig.deps.config.image,
        rig.deps.config.homieCmd ?? rig.deps.config.agentCmd,
      )]));
    };
  const wake: Effect = {
    action: "homie-wake",
    session: "h-abc",
    directive: "# Homie\n\nYou are the operator's assistant.\n",
    launch: "aaaaaaaaaaaaaaaa",
    mode: "fresh",
    binding: "fake/fake",
    runtime_binding: runtimeBinding("fake", "fake", { channel: "fake" }),
    session_path: "sessions/h-abc",
    container_name: "mc-homie-h-abc",
    skills: [{ name: "mc-homie", body: "---\nname: mc-homie\n---\nthe operator conversation surface\n" }],
    // The frozen operator-verb allowlist. A fixture that omits it proves a
    // Homie tier that can only read: the envelope writer needs a test that
    // looks for it.
    verb_allowlist: ["task add", "packet decide", "homie end"],
    idle_timeout_s: 1800,
  };
  const leaveExactCreated = async (rig: ReturnType<typeof makeRig>) => {
    rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig));
    rig.mc.enqueue(ok(JSON.stringify({
      session: "h-abc", launch: wake.launch, container_id: cid,
    })));
    rig.docker.enqueue(fail(1, "start gap"));
    await applyEffect(wake, rig.deps);
    const create = rig.docker.calls.find((call) => call[0] === "create")!;
    const record = homieInspectFromCreate(
      create, cid, rig.deps.config.image,
      rig.deps.config.homieCmd ?? rig.deps.config.agentCmd,
    ) as any;
    const planDigest = record.Config.Labels["mc-envelope-digest"] as string;
    const runtimeGeneration = record.Config.Labels["mc-runtime-generation"] as string;
    const proof = rig.deps.homieRuntime.attestEnvelope({
      container_id: cid,
      docker_envelope: canonicalHomieDockerEnvelope(record),
      envelope_digest: planDigest,
      launch: wake.launch,
      runtime_generation: runtimeGeneration,
      session: wake.session,
    });
    if (proof === null) throw new Error("test setup produced no runtime proof");
    return {
      effect: {
        action: "homie-adopt" as const,
        container_id: cid,
        container_name: "mc-homie-h-abc",
        envelope_digest: proof.envelope_digest,
        launch: wake.launch,
        runtime_generation: runtimeGeneration,
        session: wake.session,
      },
      record,
    };
  };

  test("wake uses current Projects and permits an empty catalog", async () => {
    for (const workspaces of [[], [{ id: "new-project", root: "/host/new-project" }]]) {
      const rig = makeRig({ readWorkspaces: async () => workspaces });
      rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig), ok(""));
      rig.mc.enqueue(ok(JSON.stringify({ session: "h-abc", launch: wake.launch, container_id: cid })));
      await applyEffect(wake, rig.deps);
      const create = rig.docker.calls.find((call) => call[0] === "create");
      expect(create).toBeDefined();
      expect(create).not.toContain(`${testConfig.workspaceRoot}:/workspace/source:ro`);
      const envelope = JSON.parse(rig.fakeFs.writes.get(`${testConfig.mcHome}/runs/h-abc.json`)!);
      expect(envelope.mounts.workspaces).toEqual(workspaces.map((row) => ({ id: row.id, path: `/workspaces/${row.id}` })));
      if (workspaces.length) expect(create).toContain("/host/new-project:/workspaces/new-project:ro");
      else expect(create!.some((arg) => arg.includes(":/workspace/source"))).toBe(false);
    }
  });

  test("Homie executes the private runtime from its protected volume", async () => {
    const rig = makeRig({ config: { ...testConfig, runtimeVolume: "runtime-test",
      homieCmd: ["/opt/culler-runtime/bin/bun", "/opt/culler-runtime/app/src/homie-runner/main.ts"] } });
    rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig), ok(""));
    rig.mc.enqueue(ok(JSON.stringify({ session: "h-abc", launch: wake.launch, container_id: cid })));
    await applyEffect(wake, rig.deps);
    const create = rig.docker.calls.find(call => call[0] === "create")!;
    expect(create).toContain("runtime-test:/opt/culler-runtime:ro");
    expect(create[create.indexOf("--entrypoint") + 1]).toBe("/opt/culler-runtime/bin/bun");
    expect(create).not.toContain(`${testConfig.runnerSrcDir}:/app/src:ro`);
    expect(rig.docker.calls).toContainEqual(["start", cid]);
    expect(rig.logs.some(line => line.includes("refused"))).toBe(false);
  });

  test("a Vision session mounts only its Project with read-only access", async () => {
    const rig = makeRig({ readWorkspaces: async () => [
      { id: "alpha", root: "/host/alpha" },
      { id: "beta", root: "/host/beta" },
    ] });
    rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig), ok(""));
    rig.mc.enqueue(ok(JSON.stringify({ session: "h-abc", launch: wake.launch, container_id: cid })));
    await applyEffect({ ...wake, vision_project_id: "beta" }, rig.deps);
    const create = rig.docker.calls.find((call) => call[0] === "create")!;
    expect(create).toContain("/host/beta:/workspaces/beta:ro");
    expect(create).toContain("/host/beta:/workspace/source:ro");
    expect(create.join(" ")).not.toContain("/host/alpha");
    const envelope = JSON.parse(rig.fakeFs.writes.get(`${testConfig.mcHome}/runs/h-abc.json`)!);
    expect(envelope.vision_project_id).toBe("beta");
  });

  test("an unavailable Vision Project prevents a container launch", async () => {
    const rig = makeRig({ readWorkspaces: async () => [] });
    await expect(applyEffect({ ...wake, vision_project_id: "beta" }, rig.deps)).rejects.toThrow("folder is unavailable");
    expect(rig.docker.calls).toEqual([]);
  });

  test("wake: folder → run.json → create → launch-bind(id) → start", async () => {
    const workspaces = [
      { id: "alpha", root: "/host/workspace" },
      { id: "beta", root: "/host/second-workspace" },
    ];
    const prepared: string[] = [];
    const rig = makeRig({
      config: { ...testConfig, workspaceRoots: workspaces },
      prepareAttachmentTree: async (session) => {
        prepared.push(session);
        return {
          inbound_published: `${testConfig.mcHome}/attachments/${session}/in/published`,
          outbound: `${testConfig.mcHome}/attachments/${session}/out`,
          session,
        };
      },
    });
    rig.docker.enqueue(ok("")); // rm -f any stale same-named container
    rig.docker.enqueue(ok(cid + "\n")); // create prints the 64-hex id
    rig.docker.enqueue(inspectedCreated(rig)); // immutable post-create envelope
    rig.mc.enqueue(ok(JSON.stringify({ session: "h-abc", launch: wake.launch, container_id: cid, bound_at: "t" })));
    rig.docker.enqueue(ok("")); // start
    await applyEffect(wake, rig.deps);

    // run.json is heartbeat-free and tier "homie".
    const runJson = rig.fakeFs.writes.get(`${testConfig.mcHome}/runs/h-abc.json`);
    expect(runJson).toBeDefined();
    const parsed = JSON.parse(runJson!);
    expect(parsed.tier).toBe("homie");
    expect(parsed.run_id).toBe("h-abc");
    expect(parsed.session_id).toBe("h-abc");
    expect(parsed.directive).toBe(wake.directive);
    expect(parsed.launch).toBe(wake.launch);
    expect(parsed.heartbeat_interval_s).toBeUndefined();
    expect(parsed.session_path).toBe("sessions/h-abc");
    expect(parsed.native_session_ref).toBeUndefined();
    expect(parsed.trace_filename).toBeUndefined();
    // Republished with the bound container id before start (the runner reads it).
    expect(parsed.container_id).toBe(cid);
    // The Homie tier carries its command contract the same way a pipeline
    // spawn does — the runner installs it before the first turn. It survives
    // the republish that stamps container_id, which rewrites the whole file.
    expect(parsed.skills).toEqual(wake.skills);
    // The frozen operator-verb allowlist, and the republish must not drop it.
    // This is the Homie's ENTIRE write capability: `mc` inside the container
    // admits an operator verb only by scanning this key (RequireOperatorVerb),
    // then matching it against the stored registry value
    // (requireActiveHomieVerb). Production omitted it from this literal, so
    // every live Homie could read and nothing else, while the CLI suite proved
    // both gates against a run.json its own helper fabricated. Assert the exact
    // value, not merely its presence — a wrong set refuses just as hard.
    expect(parsed.verb_allowlist).toEqual(wake.verb_allowlist);
    // The runner's own exit deadline rides with the envelope.
    expect(parsed.idle_timeout_s).toBe(1800);
    // And HOME is pinned rather than inherited: the runner derives the skill
    // root from it, and this tier binds no synthetic home to supply one, so an
    // unset HOME would make the install a silent no-op.
    const homieCreate = rig.docker.calls.find((call) => call[0] === "create")!;
    expect(homieCreate).toContain("HOME=/home/mc-model");
    // The route key rides as `binding`; the behavior comes from config.
    expect(parsed.binding).toBe("fake/fake");
    expect(parsed.harness_config).toEqual({ behavior: "/mc/behaviors/homie.json" });
    expect(parsed.mounts.workspaces).toEqual([
      { id: "alpha", path: "/workspaces/alpha" },
      { id: "beta", path: "/workspaces/beta" },
    ]);
    expect(parsed.mounts.attachments).toEqual({
      inbound: "/mc/attachments/in",
      outbound: "/mc/private/attachments/out",
    });
    // The cwd is the writable per-launch scratch.
    expect(parsed.mounts.workspace).toBe("/mc/scratch");

    // docker: rm -f (clear stale name), create, then start; launch-bind between.
    expect(rig.docker.calls[0]).toEqual(["rm", "-f", "mc-homie-h-abc"]);
    const create = rig.docker.calls[1];
    expect(create[0]).toBe("create");
    expect(create).toContain("mc-tier=homie");
    expect(create).toContain(`mc-launch-id=${wake.launch}`);
    expect(create).toContain("--rm");
    // A memory cap with no swap headroom and a pids cap; no CPU cap.
    const flag = (name: string) => create[create.indexOf(name) + 1];
    expect(flag("--memory")).toBe("1024m");
    expect(flag("--memory-swap")).toBe("1024m");
    expect(flag("--pids-limit")).toBe("128");
    expect(create).not.toContain("--cpus");
    // No Homie runs as root: uid 10002 without capabilities reaches the spine
    // only through the setuid mc.
    expect(flag("--user")).toBe("10002:10002");
    expect(flag("--cap-drop")).toBe("ALL");
    expect(create).toContain(`${testConfig.mcHome}/runs/scratch/h-abc:/mc/scratch`);
    expect(create).toContain(`${testConfig.mcHome}/sessions/h-abc:/mc/session`);
    // Each session folder is readable below the trace mount, never writable.
    expect(create).toContain(`${testConfig.mcHome}/sessions:/mc/traces:ro`);
    expect(create.filter((arg) => arg.includes(":/mc/traces"))).toHaveLength(1);
    expect(create).toContain(`${testConfig.mcHome}/logs/runs/h-abc:/mc/logs`);
    // Operator read-scope: the workspace is bound RO, never RW.
    expect(create).toContain(`${testConfig.workspaceRoot}:/workspace/source:ro`);
    expect(create).toContain("/host/workspace:/workspaces/alpha:ro");
    expect(create).toContain("/host/second-workspace:/workspaces/beta:ro");
    expect(create).toContain(
      `${testConfig.mcHome}/attachments/h-abc/in/published:/mc/attachments/in:ro`,
    );
    expect(create).toContain(
      `${testConfig.mcHome}/attachments/h-abc/out:/mc/private/attachments/out`,
    );
    expect(rig.docker.calls[2]).toEqual(["inspect", "--type", "container", cid]);
    expect(rig.docker.calls[3]).toEqual(["start", cid]);
    expect(prepared).toEqual(["h-abc"]);

    const envelopeDigest = create.find((arg) => arg.startsWith("mc-envelope-digest="))?.split("=")[1];
    const runtimeGeneration = create.find((arg) => arg.startsWith("mc-runtime-generation="))?.split("=")[1];
    expect(envelopeDigest).toMatch(/^[0-9a-f]{64}$/);
    expect(runtimeGeneration).toMatch(/^[0-9a-f]{64}$/);
    expect(rig.deps.homieRuntime.attest({
      container_id: cid,
      envelope_digest: envelopeDigest!,
      launch: wake.launch,
      runtime_generation: runtimeGeneration!,
      session: wake.session,
    })).toBe(true);

    // launch-bind CASes the exact docker id onto the launch, before start.
    expect(rig.mc.calls).toEqual([
      ["homie", "launch-bind", "h-abc", "--launch", wake.launch, "--container-id", cid],
    ]);
  });

  test("wake: a missing or invalid directive prevents container creation", async () => {
    for (const directive of [undefined, null, 7, "", " \n"]) {
      const rig = makeRig();
      await expect(applyEffect({ ...wake, directive } as Effect, rig.deps))
        .rejects.toThrow("the Homie directive is required");
      expect(rig.fakeFs.writes.size).toBe(0);
      expect(rig.docker.calls).toEqual([]);
      expect(rig.mc.calls.filter(call => !["launch-check", "launch-failure"].includes(call[1]!))).toEqual([]);
      expect(rig.mc.calls.some(call => call[1] === "launch-failure")).toBe(true);
    }
  });

  test("wake: a wake without an idle timeout writes no idle_timeout_s", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig));
    rig.mc.enqueue(ok(JSON.stringify({ session: "h-abc", launch: wake.launch, container_id: cid })));
    rig.docker.enqueue(ok(""));
    const { idle_timeout_s: _omitted, ...older } = wake as Extract<Effect, { action: "homie-wake" }>;
    await applyEffect(older as Effect, rig.deps);
    const parsed = JSON.parse(rig.fakeFs.writes.get(`${testConfig.mcHome}/runs/h-abc.json`)!);
    expect(parsed.container_id).toBe(cid);
    expect("idle_timeout_s" in parsed).toBe(false);
  });

  test("wake: a container whose memory cap drifted from the plan is refused", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok(""), ok(cid + "\n"));
    rig.docker.enqueue((_argv: string[], calls: string[][]) => {
      const create = calls.find((call) => call[0] === "create")!;
      const record = homieInspectFromCreate(
        create, cid, rig.deps.config.image, rig.deps.config.homieCmd ?? rig.deps.config.agentCmd,
      ) as any;
      record.HostConfig.Memory = 0;
      return ok(JSON.stringify([record]));
    });
    rig.docker.enqueue(ok("")); // rm of the refused container
    await applyEffect(wake, rig.deps);
    expect(rig.docker.calls.some((call) => call[0] === "start")).toBe(false);
    expect(rig.docker.calls.some((call) => call[0] === "rm" && call[1] === cid)).toBe(true);
    expect(rig.mc.calls.filter(call => !["launch-check", "launch-failure"].includes(call[1]!))).toEqual([]);
      expect(rig.mc.calls.some(call => call[1] === "launch-failure")).toBe(true);
  });

  test("attachment tree refusal leaves no Homie launch artifact or container", async () => {
    const rig = makeRig({
      prepareAttachmentTree: async () => {
        throw new Error("attachment tree is widened");
      },
    });
    await expect(applyEffect(wake, rig.deps)).rejects.toThrow("attachment tree is widened");
    expect(rig.fakeFs.events).toEqual([]);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.mc.calls.filter(call => !["launch-check", "launch-failure"].includes(call[1]!))).toEqual([]);
      expect(rig.mc.calls.some(call => call[1] === "launch-failure")).toBe(true);
  });

  test("red boot-local attachment confinement leaves no Homie launch effect", async () => {
    const rig = makeRig({
      attachmentConfinement: new AttachmentConfinementHealth(),
    });
    await expect(applyEffect(wake, rig.deps)).rejects.toThrow(
      "attachment confinement is unhealthy for this resident boot",
    );
    expect(rig.fakeFs.events).toEqual([]);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.mc.calls.filter(call => !["launch-check", "launch-failure"].includes(call[1]!))).toEqual([]);
      expect(rig.mc.calls.some(call => call[1] === "launch-failure")).toBe(true);
  });

  test("native wake materializes the exact frozen resume locators", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok("")); // rm -f stale name
    rig.docker.enqueue(ok(cid + "\n")); // create
    rig.docker.enqueue(inspectedCreated(rig));
    rig.mc.enqueue(ok(JSON.stringify({ session: "h-abc", launch: wake.launch, container_id: cid })));
    rig.docker.enqueue(ok("")); // start
    await applyEffect({
      ...wake,
      mode: "native",
      native_session_ref: "native-xyz",
      trace_filename: "trace/native.jsonl",
    }, rig.deps);

    const runJson = JSON.parse(rig.fakeFs.writes.get(`${testConfig.mcHome}/runs/h-abc.json`)!);
    expect(runJson.mode).toBe("native");
    expect(runJson.session_path).toBe("sessions/h-abc");
    expect(runJson.native_session_ref).toBe("native-xyz");
    expect(runJson.trace_filename).toBe("trace/native.jsonl");
    expect(rig.docker.calls[1]).toContain(`${testConfig.mcHome}/sessions/h-abc:/mc/session`);
  });

  test("native wake refuses a drifted session path or incomplete locator pair before effects", async () => {
    for (const effect of [
      {
        ...wake,
        mode: "native" as const,
        session_path: "sessions/other",
        native_session_ref: "native-xyz",
        trace_filename: "native.jsonl",
      },
      { ...wake, mode: "native" as const, native_session_ref: "native-xyz" },
    ]) {
      const rig = makeRig();
      await expect(applyEffect(effect, rig.deps)).rejects.toThrow();
      expect(rig.fakeFs.events).toEqual([]);
      expect(rig.docker.calls).toEqual([]);
      expect(rig.mc.calls.filter(call => !["launch-check", "launch-failure"].includes(call[1]!))).toEqual([]);
      expect(rig.mc.calls.some(call => call[1] === "launch-failure")).toBe(true);
    }
  });

  describe("credentialed Homie wake", () => {
    const nativeWake: Effect = {
      ...wake,
      mode: "native",
      binding: "claude-sdk/claude",
      runtime_binding: runtimeBinding("claude-sdk", "claude", {
        channel: "claude",
        credential: "anthropic-login",
        model: "claude-opus-4-8",
      }),
      native_session_ref: "native-xyz",
      trace_filename: "native.jsonl",
    };
    const codexWake: Effect = {
      ...nativeWake,
      binding: "codex/chatgpt",
      runtime_binding: runtimeBinding("codex", "chatgpt", {
        channel: "codex",
        credential: "openai-login",
        model: "codex-model",
      }),
    };

    test("a Claude resume projects its historical binding and opens the real adapter sandbox", async () => {
      const projected: unknown[] = [];
      const rig = makeRig({
        config: { ...testConfig, roleBehaviors: {} },
        credentials: {
          project: (runtime) => {
            projected.push(runtime);
            return accessProjection("claude-sdk");
          },
        },
      });
      rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig));
      rig.mc.enqueue(ok(JSON.stringify({ session: "h-abc", launch: wake.launch, container_id: cid })));
      rig.docker.enqueue(ok(""));
      await applyEffect(nativeWake, rig.deps);

      expect(projected).toEqual([{
        base_url: "", channel: "claude", credential: "anthropic-login",
        harness: "claude-sdk", id: "claude", model: "claude-opus-4-8",
      }]);
      const runJson = JSON.parse(rig.fakeFs.writes.get(`${testConfig.mcHome}/runs/h-abc.json`)!);
      expect(runJson.harness).toBe("claude-sdk");
      expect(runJson.model_binding).toBe("claude");
      expect(runJson.harness_config).toBeUndefined();
      const create = rig.docker.calls[1]!;
      expect(create).not.toContain("--network");
      expect(create).not.toContain("--security-opt");
      expect(create.join(" ")).not.toContain("CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST");
      expect(create.join(" ")).not.toContain("host-minted-access");
      expect(create).toContain("MC_PROVIDER_ACCESS_FILE=/mc/provider-access.json");
      expect(create).toContain(`${testConfig.mcHome}/sessions:/mc/traces:ro`);
    });

    test("a Codex resume preserves its session store and uses private Culler access", async () => {
      const rig = makeRig({
        config: { ...testConfig, roleBehaviors: {} },
        credentials: {
          project: () => accessProjection(),
        },
      });
      rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig));
      rig.mc.enqueue(ok(JSON.stringify({ session: "h-abc", launch: wake.launch, container_id: cid })));
      rig.docker.enqueue(ok(""));
      await applyEffect(codexWake, rig.deps);

      const authPath =
        `${testConfig.mcHome}/runs/provider-access/h-abc-${wake.launch}.json`;
      expect(rig.fakeFs.writes.get(authPath)).toBe(accessProjection().proxyConfig);
      const create = rig.docker.calls[1]!;
      expect(create).toContain(`${authPath}:/mc/provider-access.json:ro`);
      expect(create).not.toContain(`${testConfig.mcHome}/runs/h-abc.codex-auth.json:/mc/codex/auth.json`);
      expect(create).toContain(`${testConfig.mcHome}/sessions/h-abc:/mc/session`);
      expect(create).toContain(`${testConfig.mcHome}/sessions:/mc/traces:ro`);
      expect(create.join(" ")).not.toContain("CODEX_HOME");
      expect(create.join(" ")).not.toContain("REFRESH_TOKEN");
    });

    test("an unavailable or unknown real binding refuses before any effect", async () => {
      for (const [effect, credentials] of [
        [nativeWake, { project: () => ({ refused: "claude credential lapsed" }) }],
        [
          {
            ...nativeWake,
            binding: "unknown/route",
            runtime_binding: runtimeBinding("unknown", "route"),
          },
          { project: () => ({ env: { SECRET: "never" } }) },
        ],
      ] as const) {
        const rig = makeRig({
          config: { ...testConfig, roleBehaviors: {} },
          credentials,
        });
        await applyEffect(effect, rig.deps);
        expect(rig.fakeFs.events).toEqual([]);
        expect(rig.docker.calls).toEqual([]);
        expect(rig.mc.calls.filter(call => !["launch-check", "launch-failure"].includes(call[1]!))).toEqual([]);
      expect(rig.mc.calls.some(call => call[1] === "launch-failure")).toBe(true);
      }
    });

    test("a wake whose capability disagrees with its frozen binding refuses before any effect", async () => {
      for (const effect of [
        // The carrier is absent entirely.
        { ...nativeWake, runtime_binding: undefined as unknown as never },
        // A partial carrier: every missing field is a launch input nothing resolved.
        {
          ...nativeWake,
          runtime_binding: { harness: "claude-sdk", id: "claude" } as unknown as never,
        },
        // A coherent-looking capability for a DIFFERENT route than the session froze.
        { ...codexWake, binding: "claude-sdk/claude" },
        // The same id under a harness family the catalog never paired it with.
        {
          ...nativeWake,
          runtime_binding: { ...nativeWake.runtime_binding, harness: "codex" },
        },
      ]) {
        const rig = makeRig({
          config: { ...testConfig, roleBehaviors: {} },
          credentials: { project: () => ({ env: { SECRET: "never" } }) },
        });
        await applyEffect(effect as Effect, rig.deps);
        expect(rig.fakeFs.events).toEqual([]);
        expect(rig.docker.calls).toEqual([]);
        expect(rig.mc.calls.filter(call => !["launch-check", "launch-failure"].includes(call[1]!))).toEqual([]);
      expect(rig.mc.calls.some(call => call[1] === "launch-failure")).toBe(true);
      }
    });

    test("each real harness requires the private proxy configuration", async () => {
      // A Codex-channel binding whose projection carries no auth.json, and a
      // Claude-channel binding that carries one: both are shape disagreements
      // the resident refuses rather than launching half-credentialed.
      const cases: Array<[Effect, CredentialProjection]> = [
        [codexWake, { env: { CODEX_REFRESH_TOKEN_URL_OVERRIDE: "http://x/oauth/token" } }],
        [nativeWake, { env: {}, proxyConfig: accessProjection().proxyConfig }],
      ];
      for (const [effect, projection] of cases) {
        const rig = makeRig({
          config: { ...testConfig, roleBehaviors: {} },
          credentials: { project: () => projection },
        });
        await expect(applyEffect(effect, rig.deps)).rejects.toThrow("provider access configuration");
        expect(rig.fakeFs.events).toEqual([]);
        expect(rig.docker.calls).toEqual([]);
        expect(rig.mc.calls.filter(call => !["launch-check", "launch-failure"].includes(call[1]!))).toEqual([]);
      expect(rig.mc.calls.some(call => call[1] === "launch-failure")).toBe(true);
      }
    });

    test.each(["", "low"])("the conversation keeps its %s effort after a profile change", async (effort) => {
      const rig = makeRig({
        config: { ...testConfig, roleBehaviors: {} },
        credentials: {
          project: () => accessProjection("claude-sdk"),
        },
      });
      rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig));
      rig.mc.enqueue(ok(JSON.stringify({ session: "h-abc", launch: wake.launch, container_id: cid })));
      rig.docker.enqueue(ok(""));
      await applyEffect({ ...nativeWake, effort, runtime_binding: { ...nativeWake.runtime_binding, effort: "high" } }, rig.deps);

      const runJson = JSON.parse(rig.fakeFs.writes.get(`${testConfig.mcHome}/runs/h-abc.json`)!);
      expect(runJson.runtime).toEqual({
        base_url: "",
        channel: "claude",
        harness: "claude-sdk",
        id: "claude",
        model: "claude-opus-4-8",
        ...(effort ? { effort } : {}),
      });
      expect(runJson.runtime_binding.effort).toBe("high");
      expect(runJson.runtime.credential).toBeUndefined();
      // OAuth authority strings are the resident's business, never a container's.
      expect(runJson.runtime.token_url).toBeUndefined();
      expect(runJson.runtime.client_id).toBeUndefined();
    });
  });

  test("wake: a fenced launch abandons the container and never starts", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok("")); // rm -f stale name
    rig.docker.enqueue(ok(cid + "\n")); // create
    rig.docker.enqueue(inspectedCreated(rig));
    rig.mc.enqueue(ok(JSON.stringify({ session: "h-abc", fenced: false })));
    rig.docker.enqueue(ok("")); // rm (abandon)
    await applyEffect(wake, rig.deps);

    expect(rig.docker.calls[1][0]).toBe("create");
    expect(rig.docker.calls[2]).toEqual(["inspect", "--type", "container", cid]);
    expect(rig.docker.calls[3]).toEqual(["rm", cid]);
    expect(rig.docker.calls.some((c) => c[0] === "start")).toBe(false);
    expect(rig.deps.homieRuntime.revokeSession("h-abc")).toBe(0);
  });

  test("wake: a failed create removes only the envelope, no bind or start", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok("")); // rm -f stale name
    rig.docker.enqueue(fail(1, "boom")); // create
    await applyEffect(wake, rig.deps);

    expect(rig.docker.calls.map((c) => c[0])).toEqual(["rm", "create"]);
    expect(rig.mc.calls.filter(call => !["launch-check", "launch-failure"].includes(call[1]!))).toEqual([]);
      expect(rig.mc.calls.some(call => call[1] === "launch-failure")).toBe(true);
    expect(rig.fakeFs.events).toContain(`rm:${testConfig.mcHome}/runs/h-abc.json`);
    expect(rig.deps.homieRuntime.revokeSession("h-abc")).toBe(0);
  });

  test("wake: post-create Docker drift removes the exact new id before binding or start", async () => {
    const rig = makeRig();
    rig.docker.enqueue(
      ok(""),
      ok(cid + "\n"),
      (_argv, calls) => {
        const create = calls.find((call) => call[0] === "create")!;
        const record = homieInspectFromCreate(
          create, cid, testConfig.image, testConfig.homieCmd ?? testConfig.agentCmd,
        ) as any;
        record.HostConfig.NetworkMode = "bridge";
        return ok(JSON.stringify([record]));
      },
      ok(""),
    );

    await applyEffect(wake, rig.deps);

    expect(rig.docker.calls.at(-1)).toEqual(["rm", cid]);
    expect(rig.mc.calls.filter(call => !["launch-check", "launch-failure"].includes(call[1]!))).toEqual([]);
      expect(rig.mc.calls.some(call => call[1] === "launch-failure")).toBe(true);
    expect(rig.docker.calls.some((call) => call[0] === "start")).toBe(false);
    expect(rig.deps.homieRuntime.revokeSession("h-abc")).toBe(0);
  });

  test("wake: attachment source, access, destination, or mount-set drift refuses start", async () => {
    for (const drift of [
      "source", "inbound-rw", "outbound-ro", "private-replacement", "extra",
    ] as const) {
      const rig = makeRig();
      rig.docker.enqueue(
        ok(""),
        ok(cid + "\n"),
        (_argv, calls) => {
          const create = calls.find((call) => call[0] === "create")!;
          const record = homieInspectFromCreate(
            create, cid, testConfig.image, testConfig.homieCmd ?? testConfig.agentCmd,
          ) as any;
          const inbound = record.Mounts.find(
            (mount: any) => mount.Destination === "/mc/attachments/in",
          );
          const outbound = record.Mounts.find(
            (mount: any) => mount.Destination === "/mc/private/attachments/out",
          );
          if (drift === "source") inbound.Source = "/tmp/substituted";
          if (drift === "inbound-rw") inbound.RW = true;
          if (drift === "outbound-ro") outbound.RW = false;
          if (drift === "private-replacement") outbound.Destination = "/mc/private";
          if (drift === "extra") {
            record.Mounts.push({
              ...inbound,
              Destination: "/mc/unplanned",
            });
          }
          return ok(JSON.stringify([record]));
        },
        ok(""),
      );

      await applyEffect(wake, rig.deps);

      expect(rig.docker.calls.at(-1)).toEqual(["rm", cid]);
      expect(rig.mc.calls.filter(call => !["launch-check", "launch-failure"].includes(call[1]!))).toEqual([]);
      expect(rig.mc.calls.some(call => call[1] === "launch-failure")).toBe(true);
      expect(rig.docker.calls.some((call) => call[0] === "start")).toBe(false);
    }
  });

  test("wake: an absent or writable trace mount refuses start", async () => {
    for (const drift of ["absent", "writable"] as const) {
      const rig = makeRig();
      rig.docker.enqueue(
        ok(""),
        ok(cid + "\n"),
        (_argv, calls) => {
          const create = calls.find((call) => call[0] === "create")!;
          const record = homieInspectFromCreate(
            create, cid, testConfig.image, testConfig.homieCmd ?? testConfig.agentCmd,
          ) as any;
          const traces = record.Mounts.findIndex(
            (mount: any) => mount.Destination === "/mc/traces",
          );
          expect(traces).toBeGreaterThanOrEqual(0);
          if (drift === "absent") {
            record.Mounts.splice(traces, 1);
            record.HostConfig.Binds = record.HostConfig.Binds.filter(
              (bind: string) => !bind.includes(":/mc/traces"),
            );
          } else {
            record.Mounts[traces].RW = true;
            record.HostConfig.Binds = record.HostConfig.Binds.map(
              (bind: string) => bind.replace(":/mc/traces:ro", ":/mc/traces"),
            );
          }
          return ok(JSON.stringify([record]));
        },
        ok(""),
      );

      await applyEffect(wake, rig.deps);

      expect(rig.docker.calls.at(-1)).toEqual(["rm", cid]);
      expect(rig.docker.calls.some((call) => call[0] === "start")).toBe(false);
    }
  });

  // A packet conversation of task 21 in Project beta. The Project folder of
  // beta holds an artifact root, and the folder of alpha holds none.
  const packet = { packet_id: 41, version_id: 88, task_id: 21, project_id: "beta" };
  const packetWake: Effect = { ...wake, packet };
  const packetWorkspaces = [
    { id: "alpha", root: "/host/alpha" },
    { id: "beta", root: "/host/beta" },
  ];
  const taskArtifacts = { destination: "/mc/artifacts/beta/21", source: "/host/beta/.mc-artifacts/21" };
  const taskBind = `${taskArtifacts.source}:${taskArtifacts.destination}:ro`;
  const maskDir = `${testConfig.mcHome}/runs/artifact-mask`;
  const runJsonPath = `${testConfig.mcHome}/runs/h-abc.json`;
  const taskArtifactsCall = "mc:__task-artifacts /host/beta 21";

  /** A rig with the Projects alpha and beta. `mc __task-artifacts` answers
   * with `answer`, and its call is recorded in `calls` and among the file
   * events. Each other verb reaches the scripted queue. */
  const packetRig = (answer: ExecResult = ok(JSON.stringify(taskArtifacts)), overrides: Partial<TickDeps> = {}) => {
    const rig = makeRig({ readWorkspaces: async () => packetWorkspaces, ...overrides });
    rig.fakeFs.dir("/host/beta/.mc-artifacts");
    const scripted = rig.deps.runMc;
    rig.deps.runMc = async (argv, stdin, options) => {
      if (argv[0] !== "__task-artifacts") return scripted(argv, stdin, options);
      rig.mc.calls.push(argv);
      rig.fakeFs.events.push(`mc:${argv.join(" ")}`);
      return answer;
    };
    return rig;
  };
  const createOf = (rig: ReturnType<typeof makeRig>) => rig.docker.calls.find((call) => call[0] === "create")!;

  test("wake: a packet conversation mounts the artifact files of its task read-only for each harness family", async () => {
    const credentialed = { config: { ...testConfig, roleBehaviors: {} } };
    const families: Array<[Effect, Partial<TickDeps>]> = [
      [packetWake, {}],
      [{
        ...packetWake,
        binding: "claude-sdk/claude",
        runtime_binding: runtimeBinding("claude-sdk", "claude", { channel: "claude", credential: "anthropic-login" }),
      }, {
        ...credentialed,
        credentials: { project: () => accessProjection("claude-sdk") },
      }],
      [{
        ...packetWake,
        binding: "codex/chatgpt",
        runtime_binding: runtimeBinding("codex", "chatgpt", { channel: "codex", credential: "openai-login" }),
      }, {
        ...credentialed,
        credentials: { project: () => accessProjection() },
      }],
    ];
    for (const [effect, overrides] of families) {
      const rig = packetRig(undefined, overrides);
      rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig), ok(""));
      await applyEffect(effect, rig.deps);

      const create = createOf(rig);
      expect(create.filter((arg) => arg.includes(":/mc/artifacts/"))).toEqual([taskBind]);
      expect(create[create.indexOf(taskBind) - 1]).toBe("-v");
      // The host resolves the bind before the launch envelope exists.
      expect(rig.mc.calls).toContainEqual(["__task-artifacts", "/host/beta", "21"]);
      expect(rig.fakeFs.events.indexOf(taskArtifactsCall)).toBeGreaterThanOrEqual(0);
      expect(rig.fakeFs.events.indexOf(taskArtifactsCall))
        .toBeLessThan(rig.fakeFs.events.indexOf(`write:${runJsonPath}`));
      const runJson = JSON.parse(rig.fakeFs.writes.get(runJsonPath)!);
      expect(runJson.packet).toEqual(packet);
      expect(Object.keys(runJson.mounts)).toEqual(["session", "workspace", "workspaces", "attachments"]);
      expect(runJson.mounts.workspaces).toEqual([
        { id: "alpha", path: "/workspaces/alpha" },
        { id: "beta", path: "/workspaces/beta" },
      ]);
      // The post-create comparison accepted the bind.
      expect(rig.docker.calls).toContainEqual(["start", cid]);
    }
  });

  test("wake: an absent or writable artifact mount refuses start", async () => {
    for (const destination of [taskArtifacts.destination, "/workspaces/beta/.mc-artifacts"]) {
      for (const drift of ["absent", "writable"] as const) {
        const rig = packetRig();
        rig.docker.enqueue(
          ok(""),
          ok(cid + "\n"),
          (_argv, calls) => {
            const create = calls.find((call) => call[0] === "create")!;
            const record = homieInspectFromCreate(
              create, cid, testConfig.image, testConfig.homieCmd ?? testConfig.agentCmd,
            ) as any;
            const index = record.Mounts.findIndex(
              (mount: any) => mount.Destination === destination,
            );
            expect(index).toBeGreaterThanOrEqual(0);
            if (drift === "absent") {
              record.Mounts.splice(index, 1);
              record.HostConfig.Binds = record.HostConfig.Binds.filter(
                (bind: string) => !bind.includes(`:${destination}:`),
              );
            } else {
              record.Mounts[index].RW = true;
              record.HostConfig.Binds = record.HostConfig.Binds.map(
                (bind: string) => bind.replace(`:${destination}:ro`, `:${destination}`),
              );
            }
            return ok(JSON.stringify([record]));
          },
          ok(""),
        );

        await applyEffect(packetWake, rig.deps);

        expect(rig.docker.calls.at(-1)).toEqual(["rm", cid]);
        expect(rig.docker.calls.some((call) => call[0] === "start")).toBe(false);
        const refusal = drift === "absent"
          ? "mount set does not match the closed create plan"
          : `mount access drifted at ${destination}`;
        expect(rig.logs.some((line) => line.includes(refusal))).toBe(true);
      }
    }
  });

  test("wake: every Homie container masks the artifact root of each workspace", async () => {
    const workspaces = [...packetWorkspaces, { id: "gamma", root: "/host/gamma" }];
    const workspaceMasks = [
      `${maskDir}:/workspace/source/.mc-artifacts:ro`,
      `${maskDir}:/workspaces/alpha/.mc-artifacts:ro`,
      `${maskDir}:/workspaces/beta/.mc-artifacts:ro`,
    ];
    const cases: Array<[string, Effect, string[]]> = [
      ["ordinary", wake, workspaceMasks],
      ["Vision", { ...wake, vision_project_id: "beta" }, [
        `${maskDir}:/workspace/source/.mc-artifacts:ro`,
        `${maskDir}:/workspaces/beta/.mc-artifacts:ro`,
      ]],
      ["packet", packetWake, workspaceMasks],
    ];
    for (const [kind, effect, masks] of cases) {
      const rig = packetRig(undefined, { readWorkspaces: async () => workspaces });
      rig.fakeFs.dir("/host/alpha/.mc-artifacts");
      rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig), ok(""));
      await applyEffect(effect, rig.deps);

      const create = createOf(rig);
      // Gamma holds no artifact root, so it gets no mask.
      expect(create.filter((arg) => arg.startsWith(`${maskDir}:`))).toEqual(masks);
      for (const mask of masks) expect(create[create.indexOf(mask) - 1]).toBe("-v");
      expect(rig.fakeFs.events).toContain(`mkdir:${maskDir}`);
      expect(rig.mc.calls.some((call) => call[0] === "__task-artifacts")).toBe(kind === "packet");
      expect(rig.docker.calls).toContainEqual(["start", cid]);
    }

    // The first workspace holds no artifact root, so /workspace/source gets no mask.
    const rig = packetRig(undefined, { readWorkspaces: async () => [workspaces[2]!, ...packetWorkspaces] });
    rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig), ok(""));
    await applyEffect(wake, rig.deps);
    expect(createOf(rig).filter((arg) => arg.startsWith(`${maskDir}:`)))
      .toEqual([`${maskDir}:/workspaces/beta/.mc-artifacts:ro`]);
    expect(rig.docker.calls).toContainEqual(["start", cid]);
  });

  test("wake: an artifact root that the resident cannot list gets a mask", async () => {
    for (const [code, masked] of [["EACCES", true], ["EPERM", true], ["EIO", true], ["ENOTDIR", false]] as const) {
      const rig = packetRig();
      rig.fakeFs.dir("/host/alpha");
      const listed = rig.deps.fs.readdir;
      rig.deps.fs = {
        ...rig.deps.fs,
        readdir: async (path: string) => {
          if (path === "/host/alpha/.mc-artifacts") {
            throw Object.assign(new Error(`${code}: ${path}`), { code });
          }
          return listed(path);
        },
      };
      rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig), ok(""));
      await applyEffect(wake, rig.deps);

      const masks = createOf(rig).filter((arg) => arg.startsWith(`${maskDir}:`));
      const alphaMasks = masked
        ? [`${maskDir}:/workspace/source/.mc-artifacts:ro`, `${maskDir}:/workspaces/alpha/.mc-artifacts:ro`]
        : [];
      expect(masks).toEqual([...alphaMasks, `${maskDir}:/workspaces/beta/.mc-artifacts:ro`]);
      expect(rig.docker.calls).toContainEqual(["start", cid]);
    }
  });

  test("wake: an artifact mask folder with an entry prevents a container launch", async () => {
    const rig = packetRig();
    rig.fakeFs.writes.set(`${maskDir}/report.html`, "artifact text");
    await expect(applyEffect(wake, rig.deps)).rejects.toThrow("The artifact mask folder is not empty.");
    expect(rig.docker.calls).toEqual([]);
    expect(rig.fakeFs.events).not.toContain(`write:${runJsonPath}`);
    expect(rig.mc.calls.some((call) => call[1] === "launch-failure")).toBe(true);
  });

  test("wake: an absent task folder omits the artifact mount", async () => {
    const rig = packetRig(ok(JSON.stringify({ destination: taskArtifacts.destination, source: null })));
    rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig), ok(""));
    await applyEffect(packetWake, rig.deps);

    const create = createOf(rig);
    expect(create.some((arg) => arg.includes(":/mc/artifacts/"))).toBe(false);
    expect(create).toContain(`${maskDir}:/workspaces/beta/.mc-artifacts:ro`);
    expect(JSON.parse(rig.fakeFs.writes.get(runJsonPath)!).packet).toEqual(packet);
    expect(rig.docker.calls).toContainEqual(["start", cid]);
  });

  test("wake: a packet conversation carries the Project label", async () => {
    const projectLabels = async (effect: Effect) => {
      const rig = packetRig();
      rig.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(rig), ok(""));
      await applyEffect(effect, rig.deps);
      const create = createOf(rig);
      return create.filter((arg, index) => create[index - 1] === "--label" && arg.startsWith("com.culler.project="));
    };
    // The label routes the creation through the host command that serializes
    // it with Project deletion.
    expect(await projectLabels(packetWake)).toEqual(["com.culler.project=beta"]);
    expect(await projectLabels(wake)).toEqual([]);
  });

  test("wake: an unavailable packet Project prevents a container launch", async () => {
    for (const workspaces of [[], [{ id: "alpha", root: "/host/alpha" }]]) {
      const rig = packetRig(undefined, { readWorkspaces: async () => workspaces });
      await expect(applyEffect(packetWake, rig.deps)).rejects.toThrow(
        "The packet Project folder is unavailable. Restore its access and retry the message.",
      );
      expect(rig.docker.calls).toEqual([]);
      expect(rig.fakeFs.events).toEqual([]);
      expect(rig.mc.calls.filter((call) => call[1] !== "launch-failure")).toEqual([]);
      expect(rig.mc.calls.some((call) => call[1] === "launch-failure")).toBe(true);
    }
  });

  test("wake: a malformed packet identity prevents a container launch", async () => {
    const malformed: unknown[] = [
      null,
      [],
      "beta",
      {},
      { ...packet, packet_id: 0 },
      { ...packet, version_id: -88 },
      { ...packet, task_id: 21.5 },
      { ...packet, task_id: "21" },
      { ...packet, packet_id: Number.MAX_SAFE_INTEGER + 1 },
      { ...packet, project_id: "../beta" },
      { ...packet, project_id: 7 },
      { packet_id: 41, version_id: 88, task_id: 21 },
      { ...packet, path: "/mc/artifacts/beta/21" },
    ];
    const effects = [
      ...malformed.map((value) => ({ ...wake, packet: value }) as Effect),
      // A conversation is never both a Vision conversation and a packet conversation.
      { ...packetWake, vision_project_id: "beta" },
    ];
    for (const effect of effects) {
      const rig = packetRig();
      await expect(applyEffect(effect, rig.deps)).rejects.toThrow("homie-wake h-abc: the packet identity is malformed");
      expect(rig.docker.calls).toEqual([]);
      expect(rig.fakeFs.events).toEqual([]);
      expect(rig.mc.calls.filter((call) => call[1] !== "launch-failure")).toEqual([]);
    }
  });

  test("wake: unavailable artifact files of the packet prevent a container launch", async () => {
    const rig = packetRig(fail(1, "The artifact files of task 21 resolve outside the artifact root."));
    await expect(applyEffect(packetWake, rig.deps)).rejects.toThrow(
      "The artifact files of the packet are unavailable. Restore the Project access and retry the message.",
    );
    expect(rig.docker.calls).toEqual([]);
    expect(rig.fakeFs.events).toEqual([taskArtifactsCall]);
    expect(rig.logs.some((line) => line.includes("resolve outside the artifact root"))).toBe(true);
    expect(rig.mc.calls.some((call) => call[1] === "launch-failure")).toBe(true);
  });

  test("wake: a malformed artifact mount prevents a container launch", async () => {
    const { destination, source } = taskArtifacts;
    const answers = [
      "not json",
      JSON.stringify(null),
      JSON.stringify([destination, source]),
      JSON.stringify({ source }),
      JSON.stringify({ destination: "/mc/artifacts/beta/22", source }),
      JSON.stringify({ destination: "/mc/artifacts/beta/21/", source }),
      JSON.stringify({ destination: "/mc/artifacts/../run.json/21", source }),
      JSON.stringify({ destination: "/mc/session/21", source }),
      JSON.stringify({ destination: "/mc/artifacts/be:ta/21", source }),
      JSON.stringify({ destination }),
      JSON.stringify({ destination, source: 21 }),
      JSON.stringify({ destination, source: "host/beta/.mc-artifacts/21" }),
      JSON.stringify({ destination, source: "/host/be:ta/.mc-artifacts/21" }),
      // A source must be the folder of the task of the packet.
      JSON.stringify({ destination, source: "/host/beta/.mc-artifacts/22" }),
      JSON.stringify({ destination, source: "/host/beta/.mc-artifacts/22/../21" }),
      JSON.stringify({ destination, source: "/host/beta/.mc-artifacts/21/" }),
      JSON.stringify({ destination, source: "/" }),
    ];
    for (const answer of answers) {
      const rig = packetRig(ok(answer));
      await expect(applyEffect(packetWake, rig.deps)).rejects.toThrow(
        "homie-wake h-abc: the artifact mount of the packet is malformed",
      );
      expect(rig.docker.calls).toEqual([]);
      expect(rig.fakeFs.events).toEqual([taskArtifactsCall]);
    }
  });

  test("adopt: re-inspects and starts only the exact created id", async () => {
    const rig = makeRig();
    const { effect, record } = await leaveExactCreated(rig);
    const createCount = rig.docker.calls.filter((call) => call[0] === "create").length;
    rig.docker.enqueue(ok(JSON.stringify([record])), ok(""));

    await applyEffect(effect, rig.deps);

    expect(rig.docker.calls.slice(-2)).toEqual([
      ["inspect", "--type", "container", cid],
      ["start", cid],
    ]);
    expect(rig.docker.calls.filter((call) => call[0] === "create")).toHaveLength(createCount);
    expect(rig.docker.calls.filter((call) => call[0] === "rm")).toHaveLength(1);
  });

  test("each started Homie container reaches the repository lane follower", async () => {
    const started: string[] = [];
    const woken = makeRig({ homieStarted: (id) => started.push(`wake:${id}`) });
    woken.docker.enqueue(ok(""), ok(cid + "\n"), inspectedCreated(woken), ok(""));
    woken.mc.enqueue(ok(JSON.stringify({ session: "h-abc", launch: wake.launch, container_id: cid })));
    await applyEffect(wake, woken.deps);

    const adopted = makeRig();
    const { effect, record } = await leaveExactCreated(adopted);
    adopted.deps.homieStarted = (id) => started.push(`adopt:${id}`);
    adopted.docker.enqueue(ok(JSON.stringify([record])), ok(""));
    await applyEffect(effect, adopted.deps);

    const refused = makeRig();
    const created = await leaveExactCreated(refused);
    refused.deps.homieStarted = (id) => started.push(`refused:${id}`);
    refused.docker.enqueue(ok(JSON.stringify([created.record])), fail(1, "start failed"));
    await applyEffect(created.effect, refused.deps);

    expect(started).toEqual([`wake:${cid}`, `adopt:${cid}`]);
  });

  test("adopt: TOCTOU envelope, state, or dispatched-proof drift refuses start", async () => {
    for (const drift of ["resource", "state", "artifact", "proof"] as const) {
      const rig = makeRig();
      const { effect, record } = await leaveExactCreated(rig);
      if (drift === "resource") record.HostConfig.Memory = 1024;
      if (drift === "state") record.State.Status = "running";
      if (drift === "artifact") {
        rig.fakeFs.writes.set(`${testConfig.mcHome}/runs/h-abc.json`, '{"drifted":true}\n');
      }
      if (drift === "proof") effect.envelope_digest = "9".repeat(64);
      const startsBefore = rig.docker.calls.filter((call) => call[0] === "start").length;
      rig.docker.enqueue(ok(JSON.stringify([record])));

      await applyEffect(effect, rig.deps);

      expect(rig.docker.calls.filter((call) => call[0] === "start")).toHaveLength(startsBefore);
      expect(rig.logs.some((line) => line.includes("refusing start"))).toBe(true);
    }
  });

  test("stop: a bound container is stopped by its id", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok(""));
    await applyEffect({ action: "homie-stop", session: "h-abc", container_id: cid, reason: "idle" }, rig.deps);
    expect(rig.docker.calls).toEqual([["stop", cid]]);
  });

  test("stop: a pre-bind idle end stops nothing", async () => {
    const rig = makeRig();
    await applyEffect({ action: "homie-stop", session: "h-abc", reason: "idle" }, rig.deps);
    expect(rig.docker.calls).toEqual([]);
  });

  test("remove: an orphan is removed only by its exact observed id", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok(""));
    await applyEffect({
      action: "homie-remove",
      session: "h-abc",
      launch: "a".repeat(16),
      container_id: cid,
      container_name: "mc-homie-h-abc",
      reason: "orphan",
    }, rig.deps);
    expect(rig.docker.calls).toEqual([["rm", "-f", cid]]);
  });

  test("remove: a reconciled transitional generation is removed by exact id", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok(""));
    await applyEffect({
      action: "homie-remove",
      session: "h-abc",
      launch: "a".repeat(16),
      container_id: cid,
      container_name: "mc-homie-h-abc",
      reason: "restarting",
    }, rig.deps);
    expect(rig.docker.calls).toEqual([["rm", "-f", cid]]);
  });

  test("remove: a starting-timeout generation is removed by exact id", async () => {
    const rig = makeRig();
    rig.docker.enqueue(ok(""));
    await applyEffect({
      action: "homie-remove",
      session: "h-abc",
      launch: "a".repeat(16),
      container_id: cid,
      container_name: "mc-homie-h-abc",
      reason: "starting-timeout",
    }, rig.deps);
    expect(rig.docker.calls).toEqual([["rm", "-f", cid]]);
  });

  test("remove: malformed identity is refused before Docker", async () => {
    const rig = makeRig();
    await applyEffect({
      action: "homie-remove",
      session: "h-abc",
      launch: "a".repeat(16),
      container_id: cid,
      container_name: "mc-homie-other",
      reason: "orphan",
    }, rig.deps);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.logs.some((line) => line.includes("malformed identity"))).toBe(true);
  });
});

// The bind is the one thing in this change that can half-land in three
// different ways, each of which produces an empty file that reads as "nothing
// went wrong": a `-v` row the envelope does not declare writable, a Homie list
// that forgot the same row, and a leaf mode a hardened run's uid cannot write.
// Each tier asserts the row and the declaration from the SAME fixture.
describe("/mc/logs — runner diagnostics survive a --rm container", () => {
  test("the pipeline tier binds it, declares it writable, and makes it writable", async () => {
    const rig = repositoryRig();
    rig.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect(spawnEffect, rig.deps);

    const create = rig.docker.calls[0]!;
    expect(create).toContain(`/tmp/mc-home/logs/runs/run-42-worker:/mc/logs`);

    const runJson = JSON.parse(rig.fakeFs.writes.get("/tmp/mc-home/runs/run-42-worker.json")!);
    expect(runJson.mounts.allow_write).toContain("/mc/logs");

    // 0777, because a repository run is hardened with `--user 10002:10002`
    // and a 0700 leaf loses diagnostics on exactly the runs that most need
    // them. Safe because the parent logs/ is 0700 operator-owned.
    expect(rig.fakeFs.mkdirModes.get("/tmp/mc-home/logs/runs/run-42-worker")).toBe(0o777);
  });

  test("the leaf is created before the container that writes into it", async () => {
    const rig = repositoryRig();
    rig.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect(spawnEffect, rig.deps);
    // A bind source Docker has to invent is a root-owned directory the
    // container cannot write — the mkdir is what stops that.
    expect(rig.fakeFs.events).toContain("mkdir:/tmp/mc-home/logs/runs/run-42-worker");
    expect(rig.fakeFs.events.indexOf("mkdir:/tmp/mc-home/logs/runs/run-42-worker"))
      .toBeLessThan(rig.fakeFs.events.length);
  });

  test("the level knob reaches the container when it is set, and only then", async () => {
    const bare = repositoryRig();
    bare.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect(spawnEffect, bare.deps);
    expect(bare.docker.calls[0]!.join(" ")).not.toContain("MC_LOG_LEVEL");

    process.env["MC_LOG_LEVEL"] = "debug";
    try {
      const raised = repositoryRig();
      raised.docker.enqueue(ok("container-id\n"), ok(""));
      await applyEffect(spawnEffect, raised.deps);
      expect(raised.docker.calls[0]!).toContain("MC_LOG_LEVEL=debug");
    } finally {
      delete process.env["MC_LOG_LEVEL"];
    }
  });

  test("the zone of the Mac reaches the container as TZ when the host names one, and only then", async () => {
    const bare = repositoryRig();
    bare.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect(spawnEffect, bare.deps);
    expect(bare.docker.calls[0]!.join(" ")).not.toContain("TZ=");

    const zoned = repositoryRig({ hostZone: () => "Asia/Tokyo" });
    zoned.docker.enqueue(ok("container-id\n"), ok(""));
    await applyEffect(spawnEffect, zoned.deps);
    const create = zoned.docker.calls[0]!;
    expect(create.indexOf("TZ=Asia/Tokyo")).toBeGreaterThan(0);
    expect(create[create.indexOf("TZ=Asia/Tokyo") - 1]).toBe("-e");
    expect(create.indexOf("TZ=Asia/Tokyo")).toBeLessThan(create.indexOf(testConfig.image));
  });
});

// Reap removes launch input and execution residue. It must not remove the one
// thing the /mc/logs bind exists to make readable afterwards, and that list is
// the obvious place for someone to "tidy" it into.
describe("reap and the run's diagnostics", () => {
  test("reap removes the residue and leaves the diagnostics", async () => {
    const rig = makeRig();
    await applyEffect({ action: "reap", project: "ws-e2e", run_id: "run-42-worker", stop_container: true }, rig.deps);
    const removed = rig.fakeFs.events.filter((e) => e.startsWith("rm:"));
    // It really did run: the scratch cwd is residue and goes.
    expect(removed.some((e) => e.includes("/runs/scratch/run-42-worker"))).toBe(true);
    // The diagnostics stay. The run-log prune chore bounds the tree instead.
    expect(removed.some((e) => e.includes("/logs/runs/"))).toBe(false);
  });
});
