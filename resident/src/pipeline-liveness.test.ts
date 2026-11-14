import { describe, expect, test } from "bun:test";
import {
  collectPipelineLiveness, confirmRunContainersAbsent, PRODUCER_ABSENCE_ATTEMPTS, PRODUCER_ABSENCE_POLL_MS,
  type PipelineLivenessDeps,
} from "./pipeline-liveness";
import { fail, FakeExec, ok, presentChild } from "./test-helpers";
import type { ExecResult } from "./types";

const MC_HOME = "/tmp/mc-home";
const NOW = 1_800_000_000_000;
const PROJECT = "ws-e2e";

// Captured from Docker 29.4.0 against a name that does not exist. The exact
// bytes matter: this stderr is the ONLY thing that distinguishes "the daemon
// answered, and the container is gone" from "the daemon did not answer".
const NOT_FOUND: ExecResult = {
  exitCode: 1,
  stdout: "[]\n",
  stderr: "Error response from daemon: No such container: mc-ws-e2e-x\n",
};
const DAEMON_DOWN: ExecResult = {
  exitCode: 1,
  stdout: "",
  stderr: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?\n",
};
function running(name: string): ExecResult {
  return {
    exitCode: 0,
    stdout: JSON.stringify([{ Id: "c".repeat(64), Name: `/${name}`, State: { Status: "running" } }]),
    stderr: "",
  };
}

interface Harness {
  deps: PipelineLivenessDeps;
  inspected: string[];
  mcCalls: string[][];
  logs: string[];
}

/** `files` maps a runs/ entry name to its mtime; every launch record among
 * them reads as a run of PROJECT unless `bodies` names its bytes. Each launch
 * record is the run of one lease, and no wiki update is active, unless
 * `overrides.runMc` answers the two reads. */
function harness(
  files: Record<string, number>,
  answer: (name: string) => ExecResult,
  overrides: Partial<PipelineLivenessDeps> = {},
  bodies: Record<string, string> = {},
): Harness {
  const inspected: string[] = [];
  const mcCalls: string[][] = [];
  const logs: string[] = [];
  const runMc = overrides.runMc ?? reads(leases(Object.keys(files).filter((name) => /^[0-9a-f]{16}\.json$/.test(name))
    .map((name) => ({ project: PROJECT, run_id: name.slice(0, 16) }))));
  const deps: PipelineLivenessDeps = {
    docker: async (argv) => {
      expect(argv.slice(0, 3)).toEqual(["inspect", "--type", "container"]);
      inspected.push(argv[3]!);
      return answer(argv[3]!);
    },
    mcHome: MC_HOME,
    now: () => NOW,
    stat: async (path) => {
      const name = path.slice(`${MC_HOME}/runs/`.length);
      const mtimeMs = files[name];
      if (mtimeMs === undefined) throw new Error(`ENOENT ${path}`);
      return { mtimeMs };
    },
    readFile: async (path) => {
      const name = path.slice(`${MC_HOME}/runs/`.length);
      if (files[name] === undefined) throw new Error(`ENOENT ${path}`);
      return bodies[name] ?? JSON.stringify({ run_id: name.slice(0, -".json".length), project: PROJECT });
    },
    log: (msg) => { logs.push(msg); },
    ...overrides,
    runMc: async (argv) => {
      mcCalls.push(argv);
      return runMc(argv);
    },
  };
  return { deps, inspected, mcCalls, logs };
}

/** Answers the lease read and the wiki state read. Each other command fails the read. */
function reads(leaseList: ExecResult, wikiStateList: ExecResult = wikiStates([])): PipelineLivenessDeps["runMc"] {
  return async (argv) => {
    const command = argv.join(" ");
    if (command === "lease list") return leaseList;
    if (command === "wiki-state list") return wikiStateList;
    throw new Error(`unexpected mc command: ${command}`);
  };
}

function leases(rows: { project: string; run_id: string | null }[]): ExecResult {
  return { exitCode: 0, stdout: JSON.stringify({ leases: rows }), stderr: "" };
}

function wikiStates(rows: unknown[]): ExecResult {
  return { exitCode: 0, stdout: JSON.stringify({ wiki_states: rows }), stderr: "" };
}

/** One row of the wiki state read. `runEndedAt` is absent for a Project with no active wiki update. */
function wikiState(project: string, runId?: string, runEndedAt: string | null = null): unknown {
  const active = runId === undefined ? null : {
    run_id: runId,
    started_at: "2026-10-09 18:00:00",
    agent_started_at: "2026-10-09 18:00:05",
    last_heartbeat_at: "2026-10-09 18:00:35",
    run_ended_at: runEndedAt,
    step: runEndedAt === null ? "run" : "merge",
    step_text: runEndedAt === null ? "The Scribe updates the Project wiki." : "Culler merges the wiki commit.",
    repair_text: null,
  };
  return {
    project,
    state: active === null ? "current" : "update-active",
    state_text: active === null ? "The Project wiki is current." : "A wiki update is active.",
    accepted_commit: "4f1c".repeat(10),
    next_start_at: null,
    next_start_text: null,
    active,
    last: null,
  };
}

function id(char: string): string {
  return char.repeat(16);
}

/** The container of run `runId` in PROJECT. */
function container(runId: string, project = PROJECT): string {
  return `mc-${project}-${runId}`;
}

describe("pipeline liveness gathering", () => {
  test("retained envelopes cannot hide an older active run", async () => {
    const files: Record<string, number> = { [`${id("a")}.json`]: NOW - 900_000 };
    for (let i = 0; i < 40; i++) files[`${i.toString(16).padStart(16, "0")}.json`] = NOW - i * 1000;
    const { deps, inspected } = harness(files, () => NOT_FOUND, {
      runMc: reads(leases([{ project: PROJECT, run_id: id("a") }, { project: "free", run_id: null }])),
    });
    expect(await collectPipelineLiveness(deps)).toEqual([
      { launch_age_s: 900, present: false, run_id: id("a") },
    ]);
    expect(inspected).toEqual([container(id("a"))]);
  });

  test("every active Project receives an observation beyond sixteen runs", async () => {
    const files: Record<string, number> = {};
    const bodies: Record<string, string> = {};
    const active = Array.from({ length: 32 }, (_, index) => {
      const run_id = index.toString(16).padStart(16, "0");
      const project = `project-${index}`;
      files[`${run_id}.json`] = NOW - 5000;
      bodies[`${run_id}.json`] = JSON.stringify({ run_id, project });
      return { run_id, project };
    });
    const { deps, inspected } = harness(files, running, { runMc: reads(leases(active)) }, bodies);
    const observed = await collectPipelineLiveness(deps);
    expect(observed).toHaveLength(32);
    expect(inspected).toHaveLength(32);
    expect(observed.map((entry) => entry.run_id)).toEqual(active.map((run) => run.run_id));
  });

  test("inspects the Project-named container the launch record names", async () => {
    const { deps, inspected } = harness(
      { [`${id("a")}.json`]: NOW - 1_000, [`${id("b")}.json`]: NOW - 2_000 },
      () => NOT_FOUND,
      { runMc: reads(leases([{ project: PROJECT, run_id: id("a") }, { project: "repo.v2", run_id: id("b") }])) },
      { [`${id("b")}.json`]: JSON.stringify({ run_id: id("b"), project: "repo.v2" }) },
    );
    expect(await collectPipelineLiveness(deps)).toEqual([
      { launch_age_s: 1, present: false, run_id: id("a") },
      { launch_age_s: 2, present: false, run_id: id("b") },
    ]);
    expect(inspected).toEqual([container(id("a")), container(id("b"), "repo.v2")]);
  });

  test("a launch record that names no well-formed Project is silence, not an observation", async () => {
    const { deps, inspected } = harness(
      {
        [`${id("a")}.json`]: NOW - 1_000,
        [`${id("b")}.json`]: NOW - 2_000,
        [`${id("c")}.json`]: NOW - 3_000,
        [`${id("d")}.json`]: NOW - 4_000,
      },
      () => NOT_FOUND,
      {},
      {
        [`${id("b")}.json`]: JSON.stringify({ run_id: id("b") }),
        [`${id("c")}.json`]: JSON.stringify({ run_id: id("c"), project: "has/slash" }),
        [`${id("d")}.json`]: "not json",
      },
    );
    expect(await collectPipelineLiveness(deps)).toEqual([
      { launch_age_s: 1, present: false, run_id: id("a") },
    ]);
    expect(inspected).toEqual([container(id("a"))]);
  });

  test("emits its keys in the byte-compared wire order", async () => {
    const { deps } = harness({ [`${id("a")}.json`]: NOW - 1_000 }, () => NOT_FOUND);
    // Both hops of the carrier re-marshal and require bytes.Equal against the
    // Go struct's field-declaration order, so key order is a wire fact.
    expect(JSON.stringify(await collectPipelineLiveness(deps)))
      .toBe(`[{"launch_age_s":1,"present":false,"run_id":"${id("a")}"}]`);
  });

  test("only Docker's own not-found answer is absence; every other unknown is silence", async () => {
    const files = {
      [`${id("a")}.json`]: NOW - 10_000,
      [`${id("b")}.json`]: NOW - 20_000,
      [`${id("c")}.json`]: NOW - 30_000,
      [`${id("d")}.json`]: NOW - 40_000,
    };
    const ambiguous: Record<string, ExecResult> = {
      [container(id("a"))]: NOT_FOUND,
      // A stopped daemon returns exit 1 too. Reading that as absence would
      // reap every healthy run on every tick of an outage.
      [container(id("b"))]: DAEMON_DOWN,
      // Exit 0 describing some other container: not an observation of this one.
      [container(id("c"))]: running("mc-ws-e2e-somebody-else"),
      [container(id("d"))]: { exitCode: 0, stdout: "not json", stderr: "" },
    };
    const { deps } = harness(files, (name) => ambiguous[name]!);

    expect(await collectPipelineLiveness(deps)).toEqual([
      { launch_age_s: 10, present: false, run_id: id("a") },
    ]);
  });

  test("a docker invocation that throws is silence, not absence", async () => {
    const { deps } = harness({ [`${id("a")}.json`]: NOW - 10_000 }, () => NOT_FOUND, {
      docker: async () => {
        throw new Error("spawn docker ENOENT");
      },
    });
    expect(await collectPipelineLiveness(deps)).toEqual([]);
  });

  test("a record stamped in the future clamps to age zero rather than going negative", async () => {
    // The VM/host clock pair steps across sleep; a negative age must never
    // arithmetically pass a spawn-grace comparison.
    const { deps } = harness({ [`${id("a")}.json`]: NOW + 120_000 }, () => NOT_FOUND);
    expect(await collectPipelineLiveness(deps)).toEqual([
      { launch_age_s: 0, present: false, run_id: id("a") },
    ]);
  });

  test("a failed lease read reports no observation and reads no wiki state", async () => {
    const wikiRun = wikiStates([wikiState(PROJECT, id("a"))]);
    const failures: [PipelineLivenessDeps["runMc"], string][] = [
      [async () => { throw new Error("The helper is unavailable."); }, "The helper is unavailable."],
      [reads(fail(1), wikiRun), "The lease read failed."],
      [reads(ok("{}"), wikiRun), "The lease read returned no lease list."],
      [reads(leases([{ project: PROJECT, run_id: "not-a-run" }]), wikiRun), "The lease read returned an invalid run identity."],
    ];
    for (const [runMc, cause] of failures) {
      const { deps, inspected, mcCalls, logs } = harness({ [`${id("a")}.json`]: NOW - 5_000 }, () => NOT_FOUND, { runMc });
      expect(await collectPipelineLiveness(deps)).toEqual([]);
      expect(inspected).toEqual([]);
      expect(mcCalls).toEqual([["lease", "list"]]);
      expect(logs).toEqual([`Pipeline observation failed: Error: ${cause}`]);
    }
  });

  test("an active run without a launch record remains unknown", async () => {
    const { deps, inspected } = harness({ [`${id("a")}.json`]: NOW - 5_000 }, () => NOT_FOUND, {
      runMc: reads(leases([{ project: PROJECT, run_id: id("a") }, { project: PROJECT, run_id: id("b") }])),
    });
    expect(await collectPipelineLiveness(deps)).toEqual([
      { launch_age_s: 5, present: false, run_id: id("a") },
    ]);
    expect(inspected).toEqual([container(id("a"))]);
  });

  test("the run of a wiki update joins the lease holders in run identifier order", async () => {
    const { deps, inspected, mcCalls, logs } = harness(
      { [`${id("a")}.json`]: NOW - 30_000, [`${id("b")}.json`]: NOW - 20_000, [`${id("c")}.json`]: NOW - 10_000 },
      (name) => name === container(id("a")) ? running(name) : NOT_FOUND,
      {
        runMc: reads(
          leases([{ project: PROJECT, run_id: id("b") }, { project: "repo.v2", run_id: null }]),
          wikiStates([wikiState("repo.v2", id("c")), wikiState(PROJECT, id("a"))]),
        ),
      },
      { [`${id("c")}.json`]: JSON.stringify({ run_id: id("c"), project: "repo.v2" }) },
    );
    // One Project holds its lease holder and its wiki update run at the same time.
    expect(await collectPipelineLiveness(deps)).toEqual([
      { launch_age_s: 30, present: true, run_id: id("a") },
      { launch_age_s: 20, present: false, run_id: id("b") },
      { launch_age_s: 10, present: false, run_id: id("c") },
    ]);
    expect(inspected).toEqual([container(id("b")), container(id("c"), "repo.v2"), container(id("a"))]);
    expect(mcCalls).toEqual([["lease", "list"], ["wiki-state", "list"]]);
    expect(logs).toEqual([]);
  });

  test("a wiki update adds an entry only while its run has no end", async () => {
    const { deps, inspected, logs } = harness(
      { [`${id("a")}.json`]: NOW - 5_000, [`${id("b")}.json`]: NOW - 5_000 },
      () => NOT_FOUND,
      {
        runMc: reads(
          leases([{ project: PROJECT, run_id: id("a") }]),
          // The merge of a wiki commit follows the end of the run and has no agent container.
          wikiStates([wikiState("current"), wikiState(PROJECT, id("b"), "2026-10-09 18:04:10")]),
        ),
      },
    );
    expect(await collectPipelineLiveness(deps)).toEqual([
      { launch_age_s: 5, present: false, run_id: id("a") },
    ]);
    expect(inspected).toEqual([container(id("a"))]);
    expect(logs).toEqual([]);
  });

  test("a failed wiki state read keeps each lease entry and adds no wiki entry", async () => {
    const lease = leases([{ project: PROJECT, run_id: id("a") }]);
    const invalid = "The wiki state read returned an invalid run identity.";
    const active = wikiState(PROJECT, id("b")) as { active: Record<string, unknown> };
    const failures: [ExecResult | Error, string][] = [
      [new Error("The helper is unavailable."), "The helper is unavailable."],
      [fail(2), "The wiki state read failed."],
      [fail(1, "The spine is unavailable.\n"), "The spine is unavailable."],
      [ok("{}"), "The wiki state read returned no list."],
      [ok(JSON.stringify({ wiki_states: null })), "The wiki state read returned no list."],
      [wikiStates([wikiState(PROJECT, id("b")), wikiState("repo.v2", "9A0B1C2D3E4F5A6B")]), invalid],
      [wikiStates([wikiState(PROJECT, id("b")), wikiState("has/slash", id("c"))]), invalid],
      [wikiStates([wikiState(PROJECT, id("a"))]), invalid],
      [wikiStates([wikiState(PROJECT, id("b")), wikiState("repo.v2", id("b"))]), invalid],
      [wikiStates([{ ...active, active: { ...active.active, run_ended_at: undefined } }]), invalid],
      [wikiStates([{ project: PROJECT }]), invalid],
      [wikiStates([null]), invalid],
    ];
    for (const [answer, cause] of failures) {
      const { deps, inspected, mcCalls, logs } = harness(
        { [`${id("a")}.json`]: NOW - 5_000, [`${id("b")}.json`]: NOW - 5_000, [`${id("c")}.json`]: NOW - 5_000 },
        () => NOT_FOUND,
        {
          runMc: async (argv) => {
            if (argv.join(" ") === "lease list") return lease;
            if (answer instanceof Error) throw answer;
            return answer;
          },
        },
      );
      expect(await collectPipelineLiveness(deps)).toEqual([
        { launch_age_s: 5, present: false, run_id: id("a") },
      ]);
      expect(inspected).toEqual([container(id("a"))]);
      expect(mcCalls).toEqual([["lease", "list"], ["wiki-state", "list"]]);
      expect(logs).toEqual([`Pipeline observation omits the wiki update runs: Error: ${cause}`]);
    }
  });
});

// Capture, cleanup, and receipt recovery start only after both containers of
// the target run are positively absent. Docker's explicit not-found answer is
// the only absence. Presence is waited out on one shared attempt budget, and
// an ambiguous or unavailable inspect is unknown on first sight.
describe("run container absence", () => {
  const run = "run-42-worker";
  const present = ok(presentChild(run, "mc-ws-e2e", "exited"));
  const absence = (docker: FakeExec, attempts = PRODUCER_ABSENCE_ATTEMPTS) => {
    const sleeps: number[] = [];
    return {
      sleeps,
      result: confirmRunContainersAbsent("ws-e2e", run, { docker: docker.exec, sleep: async (ms) => { sleeps.push(ms); } }, attempts),
    };
  };

  test("only the not-found answer for both names is absent", async () => {
    const docker = new FakeExec();
    docker.enqueue(fail(1, "No such container"), fail(1, `Error: No such container: mc-setup-${run}`));
    expect(await absence(docker).result).toBe("absent");
    expect(docker.calls).toEqual([
      ["inspect", "--type", "container", `mc-ws-e2e-${run}`],
      ["inspect", "--type", "container", `mc-setup-${run}`],
    ]);
  });

  test("a container that outlives the attempt budget is present", async () => {
    const docker = new FakeExec();
    docker.fallback = present;
    const check = absence(docker);
    expect(await check.result).toBe("present");
    expect(docker.calls).toHaveLength(PRODUCER_ABSENCE_ATTEMPTS);
    expect(check.sleeps).toEqual(Array(PRODUCER_ABSENCE_ATTEMPTS - 1).fill(PRODUCER_ABSENCE_POLL_MS));
  });

  test("one attempt reports a present container without a wait", async () => {
    const docker = new FakeExec();
    docker.fallback = present;
    const check = absence(docker, 1);
    expect(await check.result).toBe("present");
    expect(docker.calls).toHaveLength(1);
    expect(check.sleeps).toEqual([]);
  });

  test("an ambiguous identity or an unavailable Docker is unknown on first sight", async () => {
    for (const answer of [
      ok(JSON.stringify([{ Id: "a".repeat(64), Name: `/mc-ws-e2e-${run}`,
        Config: { Labels: { "mc-managed": "true", "mc-tier": "pipeline" } }, State: { Status: "exited" } }])),
      fail(1, "Cannot connect to the Docker daemon"),
      ok("not JSON"),
    ]) {
      const docker = new FakeExec();
      docker.fallback = answer;
      const check = absence(docker);
      expect(await check.result).toBe("unknown");
      expect(docker.calls).toHaveLength(1);
      expect(check.sleeps).toEqual([]);
    }
  });

  test("a container that Docker still tears down is waited out", async () => {
    const docker = new FakeExec();
    docker.enqueue(present, present, fail(1, "No such container"), fail(1, "No such container"));
    const check = absence(docker);
    expect(await check.result).toBe("absent");
    expect(docker.calls.map((call) => call[3])).toEqual([
      `mc-ws-e2e-${run}`, `mc-ws-e2e-${run}`, `mc-ws-e2e-${run}`, `mc-setup-${run}`,
    ]);
    expect(check.sleeps).toEqual([PRODUCER_ABSENCE_POLL_MS, PRODUCER_ABSENCE_POLL_MS]);
  });

  test("both names share one attempt budget", async () => {
    const docker = new FakeExec();
    docker.enqueue(present, fail(1, "No such container"));
    docker.fallback = ok(presentChild(run, "mc-setup", "removing"));
    expect(await absence(docker).result).toBe("present");
    expect(docker.calls).toHaveLength(PRODUCER_ABSENCE_ATTEMPTS + 1);
  });

  test("a malformed run or Project identity is unknown without a Docker call", async () => {
    const docker = new FakeExec();
    for (const [project, runId] of [["ws-e2e", "../escape"], ["bad/project", run]] as const) {
      expect(await confirmRunContainersAbsent(project, runId, { docker: docker.exec, sleep: async () => {} }, 1)).toBe("unknown");
    }
    expect(docker.calls).toEqual([]);
  });
});
