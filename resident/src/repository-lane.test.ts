// repository-lane.test.ts — the lane against a scripted mc and a fake
// Docker: owner registration, the start requests and their events, the
// heartbeat, the stop requests, and the install pause. All time is fake.

import { describe, expect, test } from "bun:test";
import {
  createRepositoryLane, EARLIER_OWNER_POLL_MS, HEARTBEAT_INTERVAL_MS, COLLIE_TURN_ENDED, PRE_CONTAINER_ATTEMPTS,
  RETRY_DELAY_MS, type LaneDeps, type RepositoryLane,
} from "./repository-lane";
import type { StartedExecution } from "./repository";
import { startTickLoop } from "./tick-loop";
import {
  deferred, effectJson, fail, FakeFollow, FakeScheduler, makeRig, ok, presentChild, settle, type TestRig,
} from "./test-helpers";
import type { ExecResult } from "./types";

type Scripted = ExecResult | Promise<ExecResult>;
type Body = { owner: number; running: string[]; stopped: string[]; drain?: boolean; containers?: string[] };

function execution(run: string, operation: StartedExecution["operation"] = "observe", fields: Partial<StartedExecution> = {}): StartedExecution {
  return {
    run_id: run, operation, task_id: 7, project: "project", workspace_root: "/operator/project",
    destination_branch: "main", input_commit: "a".repeat(40), deadline_ms: 300_000, ...fields,
  };
}

const WIKI_RUN = "9a0b1c2d3e4f5a6b";

/** One execution of the wiki update of run WIKI_RUN. */
function wikiExecution(run: string, operation: StartedExecution["operation"], fields: Partial<StartedExecution> = {}): StartedExecution {
  return {
    run_id: run, operation, wiki_update: WIKI_RUN, project: "project", workspace_root: "/operator/project",
    destination_branch: "main", deadline_ms: 300_000, ...fields,
  };
}

function startResponse(fields: Record<string, unknown> = {}): ExecResult {
  return ok(JSON.stringify({
    owner: 2, executions: [], stops: [], confirmed: [], started: [], failed: [], published: [],
    due_in_ms: null, drain_pending: false, ...fields,
  }));
}

function heartbeatResponse(fields: Record<string, unknown> = {}): ExecResult {
  return ok(JSON.stringify({
    renewed: [], unstarted: [], stops: [], confirmed: [], ended: [], container_stops: [], ...fields,
  }));
}

const staleOwner = (): ExecResult => ({
  exitCode: 1,
  stdout: JSON.stringify({ error: { code: "stale-owner", message: "A later resident owns the repository operations." } }),
  stderr: "",
});

function absent(argv: string[]): ExecResult {
  return fail(1, `Error: No such container: ${argv.at(-1)}`);
}

/** A log record with no time, so a test can compare each other key. */
function withoutTime(record: Record<string, unknown>): Record<string, unknown> {
  const { ts: _ts, ...rest } = record;
  return rest;
}

/** A lane over one fake rig. The scripts answer mc by verb and Docker by
 * command. Each start and heartbeat request body is recorded in order. */
class LaneRig {
  rig: TestRig = makeRig();
  scheduler = new FakeScheduler();
  followers = new FakeFollow();
  pauseRequested = false;
  /** The `active` argument of each install pause check. */
  actives: number[] = [];
  exits: number[] = [];
  starts: Body[] = [];
  heartbeats: Body[] = [];
  natives: Array<{ envelope: Record<string, unknown>; signal?: AbortSignal }> = [];
  mcCalls: string[][] = [];
  dockerCalls: string[][] = [];
  owner = 2;
  register: () => Scripted = () => ok(JSON.stringify({ owner: this.owner }));
  start: (body: Body) => Scripted = () => startResponse();
  heartbeat: (body: Body) => Scripted = (body) => heartbeatResponse({ renewed: body.running });
  native: (envelope: Record<string, unknown>) => Scripted = (envelope) =>
    ok(JSON.stringify({ task_id: envelope.task_id, input_commit: "a".repeat(40), destination_commit: "b".repeat(40) }));
  record: (args: string[]) => Scripted = () => ok("{}");
  docker: (argv: string[]) => Scripted = (argv) => {
    if (argv[0] === "inspect") return absent(argv);
    if (argv[0] === "run") return ok(JSON.stringify({ task_id: 7, applied: { commit: "c".repeat(40) } }));
    return ok("");
  };
  deps: LaneDeps;
  lane: RepositoryLane;

  constructor() {
    const { deps: rigDeps } = this.rig;
    this.deps = {
      runMc: async (args, stdin, options) => {
        this.mcCalls.push(args);
        const body = stdin === undefined ? undefined : JSON.parse(new TextDecoder().decode(stdin));
        if (args[0] === "delivery" && args[1] === "register") return this.register();
        if (args[0] === "delivery" && args[1] === "start") { this.starts.push(body); return this.start(body); }
        if (args[0] === "delivery" && args[1] === "heartbeat") { this.heartbeats.push(body); return this.heartbeat(body); }
        if (args[0] === "__repository") {
          const envelope = JSON.parse(this.rig.fakeFs.writes.get(args[1]!)!);
          this.natives.push({ envelope, signal: options?.signal });
          return this.native(envelope);
        }
        if (args[0] === "delivery" && args[1] === "record") return this.record(args);
        return ok("{}");
      },
      docker: async (argv) => {
        this.dockerCalls.push(argv);
        return this.docker(argv);
      },
      log: rigDeps.log,
      fs: rigDeps.fs,
      config: rigDeps.config,
      sleep: rigDeps.sleep,
      schedule: this.scheduler.schedule,
      cancel: this.scheduler.cancel,
      follow: this.followers.follow,
      now: this.scheduler.clock,
      maintenance: async (active) => { this.actives.push(active); return this.pauseRequested; },
      exit: (code) => { this.exits.push(code); },
    };
    this.lane = createRepositoryLane(this.deps);
  }

  async started(): Promise<void> {
    await this.lane.start();
    await settle();
  }

  events(name: string) {
    return this.rig.logRecords.filter((record) => record.event === name);
  }

  calls(verb: string): string[][] {
    return this.mcCalls.filter((args) => args[0] === "delivery" && args[1] === verb);
  }
}

describe("start requests", () => {
  test("container exit follower starts before the first start request", async () => {
    const lane = new LaneRig();
    let followersAtFirstStart = -1;
    lane.start = () => {
      if (followersAtFirstStart < 0) followersAtFirstStart = lane.followers.matching("events").length;
      return startResponse();
    };
    await lane.started();
    expect(lane.calls("register")).toHaveLength(1);
    expect(followersAtFirstStart).toBe(1);
    expect(lane.followers.matching("events")[0]!.argv).toEqual([
      "events", "--filter", "type=container", "--filter", "event=die",
      "--filter", "label=mc-tier=pipeline", "--format", "{{.Actor.Attributes.name}}",
    ]);
    expect(lane.starts[0]).toEqual({ owner: 2, running: [], stopped: [], drain: false });
    const before = lane.starts.length;
    lane.followers.matching("events")[0]!.onLine("mc-project-worker-3");
    await settle();
    expect(lane.starts.length).toBe(before + 1);
    await lane.lane.stop();
  });

  test("the container exit follower restarts after it ends and causes a start request", async () => {
    const lane = new LaneRig();
    await lane.started();
    const before = lane.starts.length;
    lane.followers.matching("events")[0]!.end(1);
    await settle();
    expect(lane.events("repository.follow_failed")).toHaveLength(1);
    await lane.scheduler.advance(RETRY_DELAY_MS);
    expect(lane.followers.matching("events")).toHaveLength(2);
    expect(lane.starts.length).toBe(before + 1);
    await lane.lane.stop();
    expect(lane.followers.matching("events")[1]!.stopped).toBe(true);
  });

  test("a failed registration registers again after 5 seconds", async () => {
    const lane = new LaneRig();
    let attempts = 0;
    lane.register = () => (attempts += 1) === 1 ? fail(2, "helper unavailable") : ok(JSON.stringify({ owner: 2 }));
    const started = lane.lane.start();
    await settle();
    expect(lane.starts).toHaveLength(0);
    await lane.scheduler.advance(RETRY_DELAY_MS);
    await started;
    await settle();
    expect(attempts).toBe(2);
    expect(lane.starts[0]!.owner).toBe(2);
    await lane.lane.stop();
  });

  test("lane starts a listed execution once", async () => {
    const lane = new LaneRig();
    const native = deferred<ExecResult>();
    lane.native = () => native.promise;
    lane.start = () => startResponse({ executions: [execution("observe-5")] });
    await lane.started();
    lane.lane.request("tick");
    await settle();
    expect(lane.starts.length).toBeGreaterThanOrEqual(2);
    expect(lane.starts.slice(1).every((body) => body.running.join() === "observe-5")).toBe(true);
    expect(lane.natives).toHaveLength(1);
    expect(lane.lane.activeCount()).toBe(1);
    native.resolve(ok(JSON.stringify({ task_id: 7, destination_commit: "b".repeat(40) })));
    await settle();
    expect(lane.calls("record")).toEqual([["delivery", "record", "--operation", "observe", "--run", "observe-5", "--result", "-"]]);
    expect(lane.lane.activeCount()).toBe(0);
    await lane.lane.stop();
  });

  test("lane skips a finished execution that a response still lists", async () => {
    const lane = new LaneRig();
    let listed = true;
    lane.start = () => startResponse({ executions: listed ? [execution("observe-5")] : [] });
    await lane.started();
    expect(lane.natives).toHaveLength(1);
    const afterEnd = lane.starts.at(-1)!;
    expect(afterEnd.running).toEqual([]);
    expect(afterEnd.stopped).toEqual(["observe-5"]);
    lane.lane.request("tick");
    await settle();
    expect(lane.natives).toHaveLength(1);
    listed = false;
    lane.lane.request("tick");
    await settle();
    lane.lane.request("tick");
    await settle();
    expect(lane.starts.at(-1)!.stopped).toEqual([]);
    expect(lane.natives).toHaveLength(1);
    await lane.lane.stop();
  });

  test("lane retries a failed start request", async () => {
    const lane = new LaneRig();
    let failing = true;
    lane.start = () => failing ? fail(2, "helper unavailable")
      : startResponse({ executions: lane.natives.length === 0 ? [execution("observe-5")] : [] });
    await lane.started();
    const failed = lane.starts.length;
    expect(lane.events("repository.start_failed")).toHaveLength(failed);
    expect(lane.natives).toHaveLength(0);
    expect(lane.scheduler.pending()).toContain(RETRY_DELAY_MS);
    failing = false;
    await lane.scheduler.advance(RETRY_DELAY_MS - 1);
    expect(lane.starts).toHaveLength(failed);
    await lane.scheduler.advance(1);
    expect(lane.starts.length).toBeGreaterThan(failed);
    expect(lane.natives).toHaveLength(1);
    await lane.lane.stop();
  });

  test("events during a start request cause exactly one more start request", async () => {
    const lane = new LaneRig();
    const response = deferred<ExecResult>();
    let answers = 0;
    lane.start = () => (answers += 1) === 1 ? response.promise : startResponse();
    await lane.started();
    lane.lane.request("packet-decision");
    lane.lane.request("tick");
    lane.lane.request("collie-turn");
    await settle();
    expect(lane.starts).toHaveLength(1);
    response.resolve(startResponse());
    await settle();
    expect(lane.starts).toHaveLength(2);
    await lane.lane.stop();
  });

  test("a packet decision causes a start request", async () => {
    const lane = new LaneRig();
    await lane.started();
    const before = lane.starts.length;
    lane.lane.request("packet-decision");
    await settle();
    expect(lane.starts.length).toBe(before + 1);
    await lane.lane.stop();
  });

  test("due timer sends a start request", async () => {
    const lane = new LaneRig();
    lane.start = () => startResponse({ due_in_ms: 30_000 });
    await lane.started();
    const before = lane.starts.length;
    await lane.scheduler.advance(29_999);
    expect(lane.starts).toHaveLength(before);
    await lane.scheduler.advance(1);
    expect(lane.starts).toHaveLength(before + 1);
    await lane.lane.stop();
  });

  test("each start response replaces the due timer", async () => {
    const lane = new LaneRig();
    let due = 30_000;
    lane.start = () => startResponse({ due_in_ms: due });
    await lane.started();
    due = 50_000;
    lane.lane.request("tick");
    await settle();
    const before = lane.starts.length;
    await lane.scheduler.advance(30_000);
    expect(lane.starts).toHaveLength(before);
    await lane.scheduler.advance(20_000);
    expect(lane.starts).toHaveLength(before + 1);
    await lane.lane.stop();
  });

  test("a failed start is logged with its task", async () => {
    const lane = new LaneRig();
    lane.start = () => startResponse({
      failed: lane.starts.length > 1 ? [] : [{ project: "project", task_id: 9, operation: "apply", recorded: true, run_id: "apply-9",
        code: "invalid-authorization", message: "The authorization is not valid." }],
    });
    await lane.started();
    expect(lane.events("repository.execution_failed").map(withoutTime)).toEqual([{
      level: "warn", comp: "resident", event: "repository.execution_failed",
      msg: "the start of apply for task 9 failed (invalid-authorization): The authorization is not valid.",
      operation: "apply", project: "project", recorded: true, task_id: 9,
    }]);
    await lane.lane.stop();
  });

  // A context value with no content gives the record a `dropped_fields` key.
  test("a failed start of a wiki update is logged with its wiki update", async () => {
    const lane = new LaneRig();
    lane.start = () => startResponse({
      failed: lane.starts.length > 1 ? [] : [{ project: "project", wiki_update: WIKI_RUN, operation: "capture", recorded: true,
        run_id: "capture-3", code: "domain-rejection", message: "the repository operation waits for the run of its wiki update" }],
    });
    await lane.started();
    expect(lane.events("repository.execution_failed").map(withoutTime)).toEqual([{
      level: "warn", comp: "resident", event: "repository.execution_failed",
      msg: `the start of capture for wiki update ${WIKI_RUN} failed (domain-rejection): ` +
        "the repository operation waits for the run of its wiki update",
      operation: "capture", project: "project", recorded: true, wiki_update: WIKI_RUN,
    }]);
    await lane.lane.stop();
  });

  test("a failed start with no valid subject is logged with no subject field", async () => {
    const lane = new LaneRig();
    lane.start = () => startResponse({
      failed: lane.starts.length > 1 ? [] : [{ project: "project", task_id: 9, wiki_update: WIKI_RUN, operation: "apply", recorded: false,
        code: "usage", message: "the repository operation requires its task or its wiki update" }],
    });
    await lane.started();
    expect(lane.events("repository.execution_failed").map(withoutTime)).toEqual([{
      level: "warn", comp: "resident", event: "repository.execution_failed",
      msg: "the start of apply for an invalid subject failed (usage): the repository operation requires its task or its wiki update",
      operation: "apply", project: "project", recorded: false,
    }]);
    await lane.lane.stop();
  });

  test("a failed execution of a wiki update is logged with its wiki update", async () => {
    const lane = new LaneRig();
    lane.native = () => fail(1, "The wiki commit is not in the repository of the run.");
    lane.start = () => startResponse({
      executions: lane.starts.length > 1 ? [] : [wikiExecution("capture-3", "capture", {
        producer_run: WIKI_RUN, input_commit: "a".repeat(40), result_commit: "b".repeat(40), container_grace_ms: 0,
      })],
    });
    await lane.started();
    expect(lane.natives.map((native) => native.envelope)).toEqual([expect.objectContaining({
      operation: "capture", wiki_update: WIKI_RUN, run_id: WIKI_RUN,
      run_root: `${lane.deps.config.mcHome}/runs/wiki-repositories/${WIKI_RUN}`,
    })]);
    expect(lane.calls("fail")).toEqual([
      ["delivery", "fail", "--run", "capture-3", "--condition", "The wiki commit is not in the repository of the run."],
    ]);
    expect(lane.events("repository.execution_failed").map(withoutTime)).toEqual([{
      level: "warn", comp: "resident", event: "repository.execution_failed",
      msg: "repository execution capture-3 (capture) failed: The wiki commit is not in the repository of the run.",
      operation: "capture", run_id: "capture-3", wiki_update: WIKI_RUN,
    }]);
    await lane.lane.stop();
  });
});

describe("Collie turns", () => {
  test("a Collie turn line causes a start request", async () => {
    const lane = new LaneRig();
    const id = "d".repeat(64);
    const docker = lane.docker;
    lane.docker = (argv) => argv[0] === "ps" && argv.includes("label=mc-tier=collie") ? ok(`${id.slice(0, 12)}\n`) : docker(argv);
    await lane.started();
    const [follower] = lane.followers.matching("logs");
    expect(follower!.argv).toEqual(["logs", "--follow", "--tail", "0", id.slice(0, 12)]);
    lane.lane.followCollie(id);
    expect(lane.followers.matching("logs")).toHaveLength(1);
    const before = lane.starts.length;
    follower!.onLine("the harness wrote this line");
    await settle();
    expect(lane.starts.length).toBe(before);
    follower!.onLine(COLLIE_TURN_ENDED);
    await settle();
    expect(lane.starts.length).toBe(before + 1);
    await lane.lane.stop();
    expect(follower!.stopped).toBe(true);
  });

  test("a new Collie container gets its own follower", async () => {
    const lane = new LaneRig();
    await lane.started();
    lane.lane.followCollie("e".repeat(64));
    lane.lane.followCollie("not a container id");
    expect(lane.followers.matching("logs").map((follower) => follower.argv.at(-1))).toEqual(["e".repeat(64)]);
    expect(lane.events("repository.follow_failed")).toHaveLength(1);
    await lane.lane.stop();
  });
});

describe("heartbeat", () => {
  test("heartbeat runs during a long tick", async () => {
    const lane = new LaneRig();
    const native = deferred<ExecResult>();
    lane.native = () => native.promise;
    let answers = 0;
    lane.start = () => startResponse({ executions: (answers += 1) === 1 ? [execution("observe-5")] : [] });
    lane.docker = (argv) => argv[0] === "ps" && argv.includes("label=mc-tier=pipeline") ? ok("worker-3\nworker-3\nobserve-5\n") : ok("");
    await lane.started();
    const dispatch = deferred<ExecResult>();
    const tickRig = makeRig({
      maintenance: () => lane.lane.pauseCheck(),
      afterTick: () => lane.lane.request("tick"),
      runMc: async (args) => {
        expect(args).toEqual(["dispatch"]);
        return dispatch.promise;
      },
    });
    const loop = startTickLoop(tickRig.deps);
    const tick = tickRig.timer.fire();
    await lane.scheduler.advance(120_000);
    expect(lane.heartbeats).toHaveLength(6);
    for (const body of lane.heartbeats) {
      expect(body).toEqual({ owner: 2, running: ["observe-5"], stopped: [], containers: ["worker-3", "observe-5"] });
    }
    dispatch.resolve(effectJson({ action: "tick", actions: [], idle: [] }));
    await tick;
    await settle();
    expect(lane.starts.length).toBeGreaterThanOrEqual(2);
    native.resolve(ok(JSON.stringify({ task_id: 7 })));
    await loop.stop();
    await lane.lane.stop();
  });

  test("a failed container list sends an empty list", async () => {
    const lane = new LaneRig();
    lane.docker = (argv) => argv[0] === "ps" ? fail(1, "Cannot connect to the Docker daemon") : ok("");
    await lane.started();
    await lane.scheduler.advance(HEARTBEAT_INTERVAL_MS);
    expect(lane.heartbeats[0]!.containers).toEqual([]);
    expect(lane.events("repository.heartbeat_failed")).toHaveLength(1);
    await lane.lane.stop();
  });

  test("heartbeat unstarted entry causes a start request", async () => {
    const lane = new LaneRig();
    let listed = false;
    const native = deferred<ExecResult>();
    lane.native = () => native.promise;
    lane.start = () => startResponse({ executions: listed ? [execution("observe-5")] : [] });
    lane.heartbeat = () => heartbeatResponse({ unstarted: lane.natives.length === 0 ? ["observe-5"] : [] });
    await lane.started();
    const before = lane.starts.length;
    listed = true;
    await lane.scheduler.advance(HEARTBEAT_INTERVAL_MS);
    expect(lane.starts.length).toBe(before + 1);
    expect(lane.natives.map((native) => native.envelope.operation)).toEqual(["observe"]);
    native.resolve(ok(JSON.stringify({ task_id: 7 })));
    await lane.lane.stop();
  });

  test("heartbeat confirmation and end entries cause a start request", async () => {
    const lane = new LaneRig();
    let answers = 0;
    lane.heartbeat = () => {
      answers += 1;
      return heartbeatResponse(answers === 1 ? { confirmed: ["apply-1"] } : answers === 2 ? { ended: ["capture-2"] } : {});
    };
    await lane.started();
    const before = lane.starts.length;
    await lane.scheduler.advance(HEARTBEAT_INTERVAL_MS);
    expect(lane.starts.length).toBe(before + 1);
    await lane.scheduler.advance(HEARTBEAT_INTERVAL_MS);
    expect(lane.starts.length).toBe(before + 2);
    await lane.scheduler.advance(HEARTBEAT_INTERVAL_MS);
    expect(lane.starts.length).toBe(before + 2);
    await lane.lane.stop();
  });

  test("heartbeat container_stops stops an ended Strategist container", async () => {
    const lane = new LaneRig();
    let answers = 0;
    lane.heartbeat = () => heartbeatResponse((answers += 1) === 1
      ? { container_stops: [{ run_id: "strategist-9", project: "project" }, { run_id: "../host", project: "project" }] } : {});
    lane.docker = (argv) => argv[0] === "rm" && argv[2] === "mc-setup-strategist-9"
      ? fail(1, "Error: No such container: mc-setup-strategist-9") : ok("");
    await lane.started();
    await lane.scheduler.advance(HEARTBEAT_INTERVAL_MS);
    expect(lane.dockerCalls.filter((argv) => argv[0] === "stop" || argv[0] === "rm")).toEqual([
      ["stop", "mc-project-strategist-9"],
      ["rm", "-f", "mc-project-strategist-9"],
      ["stop", "mc-setup-strategist-9"],
      ["rm", "-f", "mc-setup-strategist-9"],
    ]);
    expect(lane.events("repository.stop_failed")).toHaveLength(1);
    await lane.lane.stop();
  });
});

describe("stop requests", () => {
  test("stop list aborts the flow and stops the container", async () => {
    const lane = new LaneRig();
    const run = deferred<ExecResult>();
    lane.start = () => startResponse({ executions: lane.starts.length === 1 ? [execution("apply-4", "apply")] : [] });
    let heartbeats = 0;
    lane.heartbeat = (body) => (heartbeats += 1) === 1 ? heartbeatResponse({ renewed: body.running })
      : heartbeatResponse({ stops: [{ run_id: "apply-4", project: "project", operation: "apply", owner: 2, wait_ms: 0 }] });
    lane.docker = (argv) => {
      if (argv[0] === "run") return run.promise;
      if (argv[0] === "stop") run.resolve({ exitCode: 137, stdout: "", stderr: "" });
      return ok("");
    };
    await lane.started();
    expect(lane.heartbeats[0]!.containers).toEqual([]);
    expect(lane.dockerCalls.filter((argv) => argv[0] === "run")).toHaveLength(1);
    await lane.scheduler.advance(HEARTBEAT_INTERVAL_MS);
    expect(lane.dockerCalls.filter((argv) => argv[0] === "stop")[0]).toEqual(["stop", "mc-project-apply-4"]);
    expect(lane.lane.activeCount()).toBe(0);
    expect(lane.calls("fail")).toEqual([]);
    expect(lane.calls("record")).toEqual([]);
    expect(lane.starts.at(-1)!.stopped).toEqual(["apply-4"]);
    await lane.lane.stop();
  });

  test("the deadline aborts the native mc process", async () => {
    const lane = new LaneRig();
    const native = deferred<ExecResult>();
    lane.native = () => native.promise;
    lane.start = () => startResponse({ executions: lane.starts.length === 1 ? [execution("observe-5", "observe", { deadline_ms: 1_000 })] : [] });
    await lane.started();
    expect(lane.natives[0]!.signal!.aborted).toBe(false);
    await lane.scheduler.advance(1_000);
    expect(lane.natives[0]!.signal!.aborted).toBe(true);
    native.resolve({ exitCode: 143, stdout: "", stderr: "" });
    await settle();
    expect(lane.lane.activeCount()).toBe(0);
    expect(lane.calls("fail")).toEqual([]);
    await lane.lane.stop();
  });

  test("a stop of this owner for a run that the lane does not hold is confirmed at once", async () => {
    const lane = new LaneRig();
    lane.start = () => startResponse({ stops: [{ run_id: "capture-3", project: "project", operation: "capture", owner: 2, wait_ms: 0 }] });
    await lane.started();
    lane.lane.request("tick");
    await settle();
    expect(lane.starts.at(-1)!.stopped).toEqual(["capture-3"]);
    await lane.lane.stop();
  });

  test("earlier-owner apply container wait confirms the stop after exit", async () => {
    const lane = new LaneRig();
    let present = true;
    lane.start = () => startResponse({ stops: [{ run_id: "apply-1", project: "project", operation: "apply", owner: 1, wait_ms: 60_000 }] });
    lane.docker = (argv) => argv[0] === "inspect" && present && argv[3] === "mc-project-apply-1"
      ? ok(presentChild("apply-1", "mc-project", "running")) : argv[0] === "inspect" ? absent(argv) : ok("");
    await lane.started();
    expect(lane.starts.at(-1)!.stopped).toEqual([]);
    await lane.scheduler.advance(EARLIER_OWNER_POLL_MS);
    expect(lane.starts.at(-1)!.stopped).toEqual([]);
    present = false;
    const before = lane.starts.length;
    await lane.scheduler.advance(EARLIER_OWNER_POLL_MS);
    expect(lane.starts.length).toBe(before + 1);
    expect(lane.starts.at(-1)!.stopped).toEqual(["apply-1"]);
    expect(lane.dockerCalls.some((argv) => argv[0] === "stop" || argv[0] === "rm")).toBe(false);
    await lane.lane.stop();
  });

  test("an earlier-owner merge container is stopped after its wait", async () => {
    const lane = new LaneRig();
    let present = true;
    lane.start = () => startResponse({ stops: [{ run_id: "prepare-1", project: "project", operation: "prepare", owner: 1, wait_ms: 10_000 }] });
    lane.docker = (argv) => {
      if (argv[0] === "rm") present = false;
      if (argv[0] !== "inspect") return ok("");
      return present && argv[3] === "mc-project-prepare-1" ? ok(presentChild("prepare-1", "mc-project", "running")) : absent(argv);
    };
    await lane.started();
    await lane.scheduler.advance(EARLIER_OWNER_POLL_MS);
    expect(lane.dockerCalls.some((argv) => argv[0] === "stop")).toBe(false);
    await lane.scheduler.advance(EARLIER_OWNER_POLL_MS);
    expect(lane.dockerCalls.filter((argv) => argv[0] === "stop" || argv[0] === "rm")).toEqual([
      ["stop", "mc-project-prepare-1"],
      ["rm", "-f", "mc-project-prepare-1"],
    ]);
    await lane.scheduler.advance(EARLIER_OWNER_POLL_MS);
    expect(lane.starts.at(-1)!.stopped).toEqual(["prepare-1"]);
    await lane.lane.stop();
  });

  test("an earlier-owner capture is confirmed at once", async () => {
    const lane = new LaneRig();
    lane.start = () => startResponse({ stops: [{ run_id: "capture-1", project: "project", operation: "capture", owner: 1, wait_ms: 0 }] });
    await lane.started();
    lane.lane.request("tick");
    await settle();
    expect(lane.starts.at(-1)!.stopped).toEqual(["capture-1"]);
    expect(lane.dockerCalls.some((argv) => argv[0] === "inspect")).toBe(false);
    await lane.lane.stop();
  });

  test("the pre-container check refuses a run that the heartbeat does not renew", async () => {
    const lane = new LaneRig();
    lane.start = () => startResponse({ executions: lane.starts.length === 1 ? [execution("apply-4", "apply")] : [] });
    lane.heartbeat = () => heartbeatResponse();
    await lane.started();
    expect(lane.heartbeats).toEqual([{ owner: 2, running: ["apply-4"], stopped: [], containers: [] }]);
    expect(lane.dockerCalls.some((argv) => argv[0] === "run")).toBe(false);
    expect(lane.calls("fail")).toEqual([]);
    expect(lane.lane.activeCount()).toBe(0);
    await lane.lane.stop();
  });

  test("a failed pre-container heartbeat repeats and the merge container starts", async () => {
    const lane = new LaneRig();
    lane.start = () => startResponse({ executions: lane.starts.length === 1 ? [execution("apply-4", "apply")] : [] });
    let heartbeats = 0;
    lane.heartbeat = (body) => (heartbeats += 1) === 1
      ? fail(2, "The helper container is unavailable.") : heartbeatResponse({ renewed: body.running });
    await lane.started();
    await settle();
    expect(lane.heartbeats).toHaveLength(2);
    expect(lane.rig.sleeps).toEqual([RETRY_DELAY_MS]);
    expect(lane.dockerCalls.filter((argv) => argv[0] === "run")).toHaveLength(1);
    expect(lane.calls("fail")).toEqual([]);
    expect(lane.calls("record")).toEqual([["delivery", "record", "--operation", "apply", "--run", "apply-4", "--result", "-"]]);
    await lane.lane.stop();
  });

  test("a pre-container check whose heartbeats all fail reports the failure", async () => {
    const lane = new LaneRig();
    lane.start = () => startResponse({ executions: lane.starts.length === 1 ? [execution("apply-4", "apply")] : [] });
    lane.heartbeat = () => fail(2, "The helper container is unavailable.");
    await lane.started();
    await settle();
    expect(lane.heartbeats).toHaveLength(PRE_CONTAINER_ATTEMPTS);
    expect(lane.rig.sleeps).toEqual(Array.from({ length: PRE_CONTAINER_ATTEMPTS - 1 }, () => RETRY_DELAY_MS));
    expect(lane.dockerCalls.some((argv) => argv[0] === "run")).toBe(false);
    expect(lane.calls("fail")).toEqual([["delivery", "fail", "--run", "apply-4", "--condition",
      "The heartbeat before the merge container failed: mc delivery heartbeat exited 2: The helper container is unavailable."]]);
    expect(lane.lane.activeCount()).toBe(0);
    await lane.lane.stop();
  });
});

describe("owner refusal", () => {
  test("owner refusal exits the resident", async () => {
    const lane = new LaneRig();
    const native = deferred<ExecResult>();
    lane.native = () => native.promise;
    lane.start = () => lane.starts.length === 1 ? startResponse({ executions: [execution("observe-5")] }) : staleOwner();
    await lane.started();
    const signal = lane.natives[0]!.signal!;
    lane.lane.request("tick");
    await settle();
    expect(lane.exits).toEqual([1]);
    expect(lane.events("repository.owner_refused")).toHaveLength(1);
    expect(signal.aborted).toBe(true);
    expect(lane.followers.followers.every((follower) => follower.stopped)).toBe(true);
    const starts = lane.starts.length;
    lane.lane.request("packet-decision");
    await lane.scheduler.advance(HEARTBEAT_INTERVAL_MS * 3);
    expect(lane.starts.length).toBe(starts);
    expect(lane.heartbeats).toHaveLength(0);
    native.resolve({ exitCode: 143, stdout: "", stderr: "" });
    await settle();
    expect(lane.calls("fail")).toEqual([]);
  });

  test("a heartbeat owner refusal exits the resident", async () => {
    const lane = new LaneRig();
    lane.heartbeat = () => staleOwner();
    await lane.started();
    await lane.scheduler.advance(HEARTBEAT_INTERVAL_MS);
    expect(lane.exits).toEqual([1]);
    await lane.scheduler.advance(HEARTBEAT_INTERVAL_MS * 2);
    expect(lane.heartbeats).toHaveLength(1);
  });
});

describe("install pause", () => {
  test("lane sends no start request after the pause confirmation", async () => {
    const lane = new LaneRig();
    await lane.started();
    lane.pauseRequested = true;
    lane.lane.request("tick");
    await settle();
    const drains = lane.starts.filter((body) => body.drain);
    expect(drains).toHaveLength(1);
    expect(lane.actives.slice(-3)).toEqual([1, 0, 0]);
    const starts = lane.starts.length;
    lane.lane.request("packet-decision");
    lane.lane.request("tick");
    await settle();
    expect(await lane.lane.pauseCheck()).toBe(true);
    expect(lane.starts.length).toBe(starts);
    expect(lane.actives.at(-1)).toBe(0);
    await lane.lane.stop();
  });

  test("pause check waits for the registration of a start response", async () => {
    const lane = new LaneRig();
    const response = deferred<ExecResult>();
    const native = deferred<ExecResult>();
    lane.native = () => native.promise;
    let answers = 0;
    lane.start = () => (answers += 1) === 1 ? response.promise : startResponse({ drain_pending: false });
    const started = lane.started();
    await settle();
    lane.pauseRequested = true;
    let checked: boolean | undefined;
    const check = lane.lane.pauseCheck().then((value) => { checked = value; });
    await settle();
    expect(checked).toBeUndefined();
    response.resolve(startResponse({ executions: [execution("observe-5")] }));
    await started;
    await check;
    expect(checked).toBe(true);
    expect(lane.actives.slice(1, 3)).toEqual([2, 2]);
    native.resolve(ok(JSON.stringify({ task_id: 7 })));
    await settle();
    await lane.lane.stop();
  });

  test("drain start request starts only receipt recovery", async () => {
    const lane = new LaneRig();
    const reconcile = deferred<ExecResult>();
    lane.native = () => reconcile.promise;
    lane.start = (body) => {
      if (!body.drain) return startResponse();
      return lane.natives.length === 0
        ? startResponse({ executions: [execution("reconcile-8", "reconcile", { previous_run: "apply-7" })], drain_pending: true })
        : startResponse({ drain_pending: false });
    };
    await lane.started();
    lane.pauseRequested = true;
    expect(await lane.lane.pauseCheck()).toBe(true);
    await settle();
    expect(lane.starts.at(-1)!.drain).toBe(true);
    expect(lane.natives.map((native) => native.envelope.operation)).toEqual(["reconcile"]);
    expect(await lane.lane.pauseCheck()).toBe(true);
    expect(lane.actives.at(-1)).toBe(2);
    reconcile.resolve(ok(JSON.stringify({ task_id: 7, status: "not-applied" })));
    await settle();
    expect(lane.starts.at(-1)).toEqual({ owner: 2, running: [], stopped: ["reconcile-8"], drain: true });
    expect(lane.actives.at(-1)).toBe(0);
    const starts = lane.starts.length;
    lane.lane.request("tick");
    await settle();
    expect(lane.starts.length).toBe(starts);
    await lane.lane.stop();
  });

  test("a failed pause check retries the start request after 5 seconds", async () => {
    const lane = new LaneRig();
    let failing = true;
    lane.deps.maintenance = async (active) => {
      lane.actives.push(active);
      if (failing) throw new Error("The pause request file is unreadable.");
      return false;
    };
    lane.lane = createRepositoryLane(lane.deps);
    await lane.started();
    expect(lane.starts).toHaveLength(0);
    expect(lane.events("repository.start_failed").length).toBeGreaterThan(0);
    failing = false;
    await lane.scheduler.advance(RETRY_DELAY_MS);
    expect(lane.starts).toHaveLength(1);
    await lane.lane.stop();
  });

  test("heartbeat runs with an empty registry during a pause", async () => {
    const lane = new LaneRig();
    await lane.started();
    lane.pauseRequested = true;
    lane.lane.request("tick");
    await settle();
    const starts = lane.starts.length;
    await lane.scheduler.advance(HEARTBEAT_INTERVAL_MS * 2);
    expect(lane.heartbeats).toEqual([
      { owner: 2, running: [], stopped: [], containers: [] },
      { owner: 2, running: [], stopped: [], containers: [] },
    ]);
    expect(lane.starts.length).toBe(starts);
    await lane.lane.stop();
  });

  test("lane resumes after the pause ends", async () => {
    const lane = new LaneRig();
    await lane.started();
    lane.pauseRequested = true;
    lane.lane.request("tick");
    await settle();
    const starts = lane.starts.length;
    lane.pauseRequested = false;
    expect(await lane.lane.pauseCheck()).toBe(false);
    await settle();
    expect(lane.starts.length).toBe(starts + 1);
    expect(lane.starts.at(-1)!.drain).toBe(false);
    lane.pauseRequested = true;
    lane.lane.request("tick");
    await settle();
    expect(lane.starts.filter((body) => body.drain)).toHaveLength(2);
    await lane.lane.stop();
  });
});

describe("stop", () => {
  test("lane stop waits for each flow and keeps the heartbeat until then", async () => {
    const lane = new LaneRig();
    const native = deferred<ExecResult>();
    lane.native = () => native.promise;
    lane.start = () => startResponse({ executions: lane.starts.length === 1 ? [execution("observe-5")] : [], due_in_ms: 1_000 });
    await lane.started();
    let stopped = false;
    const stopping = lane.lane.stop().then(() => { stopped = true; });
    await lane.scheduler.advance(HEARTBEAT_INTERVAL_MS);
    expect(stopped).toBe(false);
    expect(lane.heartbeats).toEqual([{ owner: 2, running: ["observe-5"], stopped: [], containers: [] }]);
    const starts = lane.starts.length;
    lane.lane.request("tick");
    native.resolve(ok(JSON.stringify({ task_id: 7 })));
    await stopping;
    expect(lane.starts.length).toBe(starts);
    expect(lane.calls("record")).toHaveLength(1);
    expect(lane.scheduler.pending()).toEqual([]);
  });
});
