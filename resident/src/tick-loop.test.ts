// tick-loop.test.ts — the loop's contract:
// timer-driven, one tick at a time, `mc dispatch` and nothing else, the tick's
// actions effected in order, failures logged and survived, clean shutdown. All
// time is fake.

import { describe, expect, test } from "bun:test";
import { startTickLoop } from "./tick-loop";
import { deferred, effectJson, fail, makeRig, ok, runtimeBinding } from "./test-helpers";

/** One tick object: the committed actions in tick order, then the Projects
 * whose round committed nothing. */
const tickOf = (actions: unknown[], idle: { project: string; reason: string }[] = []) =>
  effectJson({ action: "tick", actions, idle });
const idle = () => tickOf([], [{ project: "ws-e2e", reason: "board empty" }]);

/** A launchable fake-route spawn of `run_id` in `project`. */
const spawn = (run_id: string, project: string) => ({
  action: "spawn", run_id, role: "worker", subject_id: 1,
  project, skill_store_dir: `skills/projects/${project}/worker`,
  pool_ids: [1], harness: "fake", model_binding: "fake",
  runtime_binding: runtimeBinding("fake", "fake", { channel: "fake" }),
  git_identity_override: null, brief: "immutable brief",
  mount_plan: { entries: [], version: 1 }, heartbeat_interval_s: 1,
});

describe("timer wiring", () => {
  test("an installer pause skips dispatch and the chores until the pause request ends", async () => {
    let requested = true;
    let chores = 0;
    let checks = 0;
    let receipts = 0;
    const rig = makeRig({
      maintenance: async () => { checks += 1; return requested; },
      beforeTick: async () => { chores += 1; },
      tickComplete: async () => { receipts += 1; },
    });
    rig.mc.enqueue(idle());
    const loop = startTickLoop(rig.deps);
    await rig.timer.fire();
    await rig.timer.fire();
    expect(checks).toBe(2);
    expect(rig.mc.calls).toEqual([]);
    expect(chores).toBe(0);
    expect(receipts).toBe(2);
    requested = false;
    await rig.timer.fire();
    expect(rig.mc.calls).toEqual([["dispatch"]]);
    expect(chores).toBe(1);
    await loop.stop();
  });

  test("each tick attempt ends with the after-tick callback, also a failed one", async () => {
    const events: string[] = [];
    const rig = makeRig({
      afterTick: () => { events.push("after"); },
      tickComplete: async () => { events.push("receipt"); },
    });
    rig.mc.enqueue(idle(), fail(2, "helper unavailable"));
    const loop = startTickLoop(rig.deps);
    await rig.timer.fire();
    await rig.timer.fire();
    expect(events).toEqual(["receipt", "after", "after"]);
    await loop.stop();
  });

  test("a failed installer pause check leaves dispatch stopped", async () => {
    const rig = makeRig({ maintenance: async () => { throw new Error("invalid pause request"); } });
    const loop = startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(rig.mc.calls).toEqual([]);
    expect(rig.logs.join(" ")).toContain("invalid pause request");
    await loop.stop();
  });

  test("registers a repeating timer at intervalMs, no tick before it fires", () => {
    const rig = makeRig({ intervalMs: 1234 });
    startTickLoop(rig.deps);
    expect(rig.timer.intervalMs).toBe(1234);
    expect(rig.timer.fn).not.toBeNull();
    expect(rig.mc.calls).toEqual([]); // ticks come from the timer only
  });

  test("each firing runs exactly one `mc dispatch` with argv ['dispatch']", async () => {
    let receipts = 0;
    const rig = makeRig({ tickComplete: async () => { receipts += 1; } });
    rig.mc.enqueue(idle(), idle(), idle());
    startTickLoop(rig.deps);
    await rig.timer.fire();
    await rig.timer.fire();
    await rig.timer.fire();
    expect(rig.mc.calls).toEqual([["dispatch"], ["dispatch"], ["dispatch"]]);
    expect(receipts).toBe(3);
  });
});

describe("one tick at a time", () => {
  test("a firing that lands mid-tick is skipped, never queued", async () => {
    const rig = makeRig();
    const gate = deferred<ReturnType<typeof ok>>();
    rig.mc.enqueue(gate.promise, idle());
    startTickLoop(rig.deps);

    const first = rig.timer.fire(); // tick 1 blocks on mc dispatch
    rig.timer.fire(); // fires mid-tick → skipped
    rig.timer.fire(); // and again → skipped, not queued
    expect(rig.mc.calls.length).toBe(1);
    expect(rig.logs.filter((l) => l.includes("tick skipped"))).toHaveLength(2);

    gate.resolve(idle());
    await first;

    await rig.timer.fire(); // next firing ticks normally
    expect(rig.mc.calls.length).toBe(2);
  });
});

// The tick returns the ordered list of the actions it committed, and the
// resident effects them in that order. A Project has at most two actions: one
// of its agent run with the Project lease and one of its wiki update. Each
// action is a committed decision, so a failed host action never holds back
// another action.
describe("the tick's action list", () => {
  const createdNames = (rig: ReturnType<typeof makeRig>) =>
    rig.docker.calls.filter((c) => c[0] === "create").map((c) => c[c.indexOf("--name") + 1]);

  test("two spawns of two Projects are effected in tick order", async () => {
    let receipts = 0;
    const rig = makeRig({ tickComplete: async () => { receipts += 1; } });
    rig.mc.enqueue(tickOf([spawn("r1", "ws-a"), spawn("r2", "ws-b")]));
    rig.docker.enqueue(ok("id-a\n"), ok(""), ok("id-b\n"), ok(""));
    startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(createdNames(rig)).toEqual(["mc-ws-a-r1", "mc-ws-b-r2"]);
    expect(rig.docker.calls.filter((c) => c[0] === "start")).toEqual([["start", "mc-ws-a-r1"], ["start", "mc-ws-b-r2"]]);
    // Effected in order: a's start precedes b's create.
    const order = rig.docker.calls.map((c) => `${c[0]} ${c[0] === "create" ? c[c.indexOf("--name") + 1] : c[1]}`);
    expect(order).toEqual(["create mc-ws-a-r1", "start mc-ws-a-r1", "create mc-ws-b-r2", "start mc-ws-b-r2"]);
    expect(receipts).toBe(1);
  });

  test("the spawn of a Worker run and the spawn of a wiki update run of one Project are both effected", async () => {
    const rig = makeRig();
    const commit = "a".repeat(40);
    // The run of a wiki update: the Scribe role, no subject, no pool, and the
    // repository of the wiki update.
    const wikiSpawn = {
      ...spawn("r2", "ws-a"),
      role: "scribe", subject_id: null, pool_ids: [],
      skill_store_dir: "skills/projects/ws-a/scribe",
      mount_plan: {
        entries: [],
        repository: {
          wiki_update: "r2", workspace_root: "/operator/ws-a", input_commit: commit,
          destination_branch: "main", max_temporary_bytes: 21474836480,
        },
        version: 1,
      },
    };
    const scripted = rig.deps.runMc;
    rig.deps.runMc = async (argv, stdin) => argv[0] === "__repository"
      ? ok(JSON.stringify({ wiki_update: "r2", input_commit: commit, destination_commit: commit }))
      : scripted(argv, stdin);
    rig.mc.enqueue(tickOf([spawn("r1", "ws-a"), wikiSpawn]));
    rig.docker.enqueue(ok("id-1\n"), ok(""), ok("id-2\n"), ok(""));
    startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(rig.logs.filter((line) => line.includes("spawn refused") || line.includes("failed"))).toEqual([]);
    const order = rig.docker.calls.map((c) => `${c[0]} ${c[0] === "create" ? c[c.indexOf("--name") + 1] : c[1]}`);
    expect(order).toEqual(["create mc-ws-a-r1", "start mc-ws-a-r1", "create mc-ws-a-r2", "start mc-ws-a-r2"]);
    const repository = "/tmp/mc-home/runs/wiki-repositories/r2/repo:/workspace/source:ro";
    expect(rig.docker.calls[0]).not.toContain(repository);
    expect(rig.docker.calls[2]).toContain(repository);
    const launch = (run: string) => JSON.parse(rig.fakeFs.writes.get(`/tmp/mc-home/runs/${run}.json`)!);
    expect(launch("r1")).toMatchObject({ run_id: "r1", role: "worker", subject_id: 1, project: "ws-a", pool_ids: [1] });
    expect(launch("r2")).toMatchObject({ run_id: "r2", role: "scribe", subject_id: null, project: "ws-a", pool_ids: [] });
  });

  test("a failing action is logged and the next action still runs", async () => {
    let receipts = 0;
    const rig = makeRig({ tickComplete: async () => { receipts += 1; } });
    rig.mc.enqueue(tickOf([spawn("r1", "ws-a"), spawn("r2", "ws-b")]));
    rig.docker.enqueue(fail(125, "docker daemon down"), ok("id-b\n"), ok(""));
    startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(rig.logs.some((l) => l.includes("docker create failed (exit 125)"))).toBe(true);
    expect(createdNames(rig)).toEqual(["mc-ws-a-r1", "mc-ws-b-r2"]);
    expect(rig.docker.calls.at(-1)).toEqual(["start", "mc-ws-b-r2"]);
    expect(receipts).toBe(1);
  });

  test("an action that throws is logged and the next action still runs", async () => {
    const rig = makeRig();
    rig.deps.homieRuntime.revokeContainer = () => { throw new Error("the runtime registry refused"); };
    rig.docker.enqueue(ok(""), ok("id-b\n"), ok(""));
    rig.mc.enqueue(tickOf([
      { action: "homie-stop", session: "h-test", container_id: "c".repeat(64) },
      spawn("r2", "ws-b"),
    ]));
    startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(rig.logs.some((line) => line.includes("action 0 (homie-stop) failed: the runtime registry refused"))).toBe(true);
    expect(createdNames(rig)).toEqual(["mc-ws-b-r2"]);
  });

  test("a refused round arrives as an error envelope: logged as a refusal, the other actions still effected", async () => {
    const rig = makeRig();
    rig.mc.enqueue(tickOf([
      spawn("r1", "ws-a"),
      { error: { code: "health.helper_unavailable", message: "private helper did not answer" } },
    ]));
    rig.docker.enqueue(ok("id-a\n"), ok(""));
    startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(createdNames(rig)).toEqual(["mc-ws-a-r1"]);
    expect(rig.logs.some((l) => l.includes("tick: round 1 refused (health.helper_unavailable): private helper did not answer"))).toBe(true);
  });

  test("the Projects whose round committed nothing are logged with their reason", async () => {
    const rig = makeRig();
    rig.mc.enqueue(tickOf([], [{ project: "ws-a", reason: "lease-held" }, { project: "ws-b", reason: "queue-full" }]));
    startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(rig.logs).toContain("tick: ws-a idle (lease-held)");
    expect(rig.logs).toContain("tick: ws-b idle (queue-full)");
    expect(rig.docker.calls).toEqual([]);
  });

  test("a result that is not a tick object is logged and not effected, and earns no receipt", async () => {
    let receipts = 0;
    const rig = makeRig({ tickComplete: async () => { receipts += 1; } });
    rig.mc.enqueue(effectJson(spawn("r1", "ws-a")), idle());
    startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(rig.logs.some((l) => l.includes("mc dispatch: not a tick object"))).toBe(true);
    expect(rig.docker.calls).toEqual([]);
    expect(receipts).toBe(0);
    await rig.timer.fire();
    expect(receipts).toBe(1);
  });
});

describe("fail-closed tick failures", () => {
  test("a failed required chore skips dispatch and retries before the next tick", async () => {
    const rig = makeRig();
    let attempts = 0;
    rig.deps.beforeTick = async () => {
      attempts += 1;
      if (attempts === 1) throw new Error("backup unavailable");
    };
    rig.mc.enqueue(idle());
    startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(rig.mc.calls).toEqual([]);
    expect(rig.logs.some((line) => line.includes("backup unavailable"))).toBe(true);
    await rig.timer.fire();
    expect(rig.mc.calls).toEqual([["dispatch"]]);
    expect(attempts).toBe(2);
  });

  test("mc dispatch nonzero exit is logged and the loop continues", async () => {
    const rig = makeRig();
    rig.mc.enqueue(fail(2, "spine unreachable"), idle());
    startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(rig.logs.some((l) => l.includes("mc dispatch failed (exit 2)") && l.includes("spine unreachable"))).toBe(true);
    await rig.timer.fire();
    expect(rig.mc.calls.length).toBe(2); // still alive
  });

  test("unparseable effect JSON is logged and the loop continues", async () => {
    let receipts = 0;
    const rig = makeRig({ tickComplete: async () => { receipts += 1; } });
    rig.mc.enqueue(ok("not json at all\n"), idle());
    startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(rig.logs.some((l) => l.includes("unparseable effect JSON"))).toBe(true);
    expect(receipts).toBe(0);
    await rig.timer.fire();
    expect(receipts).toBe(1);
    expect(rig.mc.calls.length).toBe(2);
  });

  test("a rejected exec (mc binary unspawnable) is logged, loop survives", async () => {
    const rig = makeRig();
    let first = true;
    rig.deps.runMc = async (argv) => {
      rig.mc.calls.push(argv);
      if (first) {
        first = false;
        throw new Error("ENOENT: mc not found");
      }
      return idle();
    };
    startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(rig.logs.some((l) => l.includes("tick failed") && l.includes("ENOENT"))).toBe(true);
    await rig.timer.fire();
    expect(rig.mc.calls.length).toBe(2);
  });

  test("a failed effect never crashes the loop (docker create fails on spawn)", async () => {
    const rig = makeRig();
    rig.mc.enqueue(tickOf([spawn("r1", "ws-e2e")]), ok("{}"), idle());
    rig.docker.enqueue(fail(125, "docker daemon down"));
    startTickLoop(rig.deps);
    await rig.timer.fire();
    expect(rig.logs.some((l) => l.includes("docker create failed (exit 125)"))).toBe(true);
    await rig.timer.fire();
    expect(rig.mc.calls.map((argv) => argv.slice(0, 2))).toEqual([
      ["dispatch"], ["run", "infra-failure"], ["dispatch"],
    ]);
  });
});

describe("clean shutdown", () => {
  test("stop() clears the timer and later firings do nothing", async () => {
    const rig = makeRig();
    const handle = startTickLoop(rig.deps);
    await handle.stop();
    expect(rig.timer.cleared).toBe(true);
    await rig.timer.fire(); // even if a stale firing arrives: no tick
    expect(rig.mc.calls).toEqual([]);
  });

  test("stop() during an in-flight tick waits for that tick to finish", async () => {
    const rig = makeRig();
    const gate = deferred<ReturnType<typeof ok>>();
    rig.mc.enqueue(gate.promise);
    const handle = startTickLoop(rig.deps);
    const inFlight = rig.timer.fire();

    let stopResolved = false;
    const stopP = handle.stop().then(() => {
      stopResolved = true;
    });
    await Promise.resolve(); // give stop a chance to (wrongly) resolve early
    await Promise.resolve();
    expect(stopResolved).toBe(false);

    gate.resolve(idle());
    await inFlight;
    await stopP;
    expect(stopResolved).toBe(true);
  });

  test("stop() is idempotent", async () => {
    const rig = makeRig();
    const handle = startTickLoop(rig.deps);
    await handle.stop();
    await handle.stop();
    expect(rig.timer.cleared).toBe(true);
  });
});

describe("nudge", () => {
  /** Lets the chained round settle: each round is a few promise hops. */
  const settle = async () => {
    for (let i = 0; i < 20; i++) await Promise.resolve();
  };

  test("a nudge while idle runs a round now, without a timer firing", async () => {
    const rig = makeRig();
    rig.mc.enqueue(idle());
    const handle = startTickLoop(rig.deps);
    expect(rig.mc.calls).toEqual([]);
    handle.nudge();
    await settle();
    expect(rig.mc.calls).toEqual([["dispatch"]]);
    expect(rig.logRecords.some((r) => r.event === "tick.nudged")).toBe(true);
  });

  test("a nudge during a round yields exactly one more round right after it", async () => {
    const rig = makeRig();
    const gate = deferred<ReturnType<typeof ok>>();
    rig.mc.enqueue(gate.promise, idle(), idle());
    const handle = startTickLoop(rig.deps);
    const first = rig.timer.fire();
    handle.nudge();
    expect(rig.mc.calls.length).toBe(1);
    gate.resolve(idle());
    await first;
    await settle();
    expect(rig.mc.calls).toEqual([["dispatch"], ["dispatch"]]);
    await settle();
    expect(rig.mc.calls.length).toBe(2);
  });

  test("three nudges during one round coalesce into one extra round", async () => {
    const rig = makeRig();
    const gate = deferred<ReturnType<typeof ok>>();
    rig.mc.enqueue(gate.promise, idle(), idle(), idle());
    const handle = startTickLoop(rig.deps);
    const first = rig.timer.fire();
    handle.nudge();
    handle.nudge();
    handle.nudge();
    gate.resolve(idle());
    await first;
    await settle();
    expect(rig.mc.calls.length).toBe(2);
  });

  test("the timer keeps its cadence beside nudges", async () => {
    const rig = makeRig({ intervalMs: 60_000 });
    rig.mc.enqueue(idle(), idle());
    const handle = startTickLoop(rig.deps);
    handle.nudge();
    await settle();
    await rig.timer.fire();
    expect(rig.timer.intervalMs).toBe(60_000);
    expect(rig.mc.calls.length).toBe(2);
  });

  test("stop() drops a booked round, and a nudge after stop() does nothing", async () => {
    const rig = makeRig();
    const gate = deferred<ReturnType<typeof ok>>();
    rig.mc.enqueue(gate.promise, idle());
    const handle = startTickLoop(rig.deps);
    const first = rig.timer.fire();
    handle.nudge();
    const stopP = handle.stop();
    gate.resolve(idle());
    await first;
    await stopP;
    expect(rig.mc.calls.length).toBe(1);
    handle.nudge();
    await settle();
    expect(rig.mc.calls.length).toBe(1);
  });
});
