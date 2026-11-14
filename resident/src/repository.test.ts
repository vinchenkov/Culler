import { expect, test } from "bun:test";
import {
  prepareRepositoryRun, REPORT_ATTEMPTS, REPORT_RETRY_MS, repositoryRunRoot, runExecution, subjectFields,
  type ExecutionContext, type StartedExecution,
} from "./repository";
import { PRODUCER_ABSENCE_ATTEMPTS, PRODUCER_ABSENCE_POLL_MS } from "./pipeline-liveness";
import { deferred, fail, makeRig, ok, presentChild, type TestRig } from "./test-helpers";
import type { ExecResult } from "./types";

const execution: StartedExecution = {
  run_id: "merge-42", operation: "prepare", task_id: 42, project: "project",
  workspace_root: "/operator/project", destination_branch: "culler",
  submitted_commit: "a".repeat(40), deadline_ms: 600000,
};

function context(overrides: Partial<ExecutionContext> = {}): { controller: AbortController; ctx: ExecutionContext } {
  const controller = new AbortController();
  return {
    controller,
    ctx: {
      signal: controller.signal,
      checkBeforeContainer: async () => true,
      graceRemainingMs: () => 0,
      ...overrides,
    },
  };
}

function absent(argv: string[]): ExecResult {
  return fail(1, `Error: No such container: ${argv.at(-1)}`);
}

/** Answers each docker inspect with the absence of the container, and each
 * other docker call through `other`. */
function dockerRoute(rig: TestRig, other: (argv: string[]) => ExecResult | Promise<ExecResult> = () => ok("")): string[][] {
  const calls: string[][] = [];
  rig.deps.docker = async (argv) => {
    calls.push(argv);
    return argv[0] === "inspect" ? absent(argv) : other(argv);
  };
  return calls;
}

function envelopeOf(rig: TestRig, args: string[]): Record<string, unknown> {
  return JSON.parse(rig.fakeFs.writes.get(args[1]!)!);
}

function failures(rig: TestRig): string[][] {
  return rig.mc.calls.filter((args) => args[0] === "delivery" && args[1] === "fail");
}

test("merge preparation isolates project commands from the accepted repository", async () => {
  const rig = makeRig();
  const calls: string[] = [];
  const candidate = { status: "merged", commit: "c".repeat(40) };
  rig.deps.runMc = async (args, stdin) => {
    if (args[0] === "__repository") {
      const envelope = envelopeOf(rig, args);
      calls.push(envelope.operation as string);
      expect(envelope.run_id).toBe("merge-42");
      expect(envelope.deadline_ms).toBe(600000);
      return ok(JSON.stringify(["allocate", "prepare-repository"].includes(envelope.operation as string)
        ? { task_id: 42, input_commit: "a".repeat(40), destination_commit: "b".repeat(40) }
        : { task_id: 42, candidate, retained_ref: "refs/mc/tasks/42/candidates/merge-42" }));
    }
    calls.push(`record:${args[3]}`);
    expect(args).toEqual(["delivery", "record", "--operation", args[3]!, "--run", "merge-42", "--result", "-"]);
    expect(JSON.parse(new TextDecoder().decode(stdin)).task_id).toBe(42);
    return ok("{}");
  };
  rig.deps.docker = async (args) => {
    calls.push("container");
    expect(args.some((arg) => arg.includes("/operator/project"))).toBe(false);
    expect(args).toContain("/tmp/mc-home/runs/repositories/merge-42:/mc/repositories/merge-42");
    expect(args.slice(-2)).toEqual(["prepare", "/mc/repository.json"]);
    const merge = JSON.parse(rig.fakeFs.writes.get("/tmp/mc-home/runs/merge-42.merge.json")!);
    expect(merge.workspace_root).toBe("/workspace/source");
    expect(merge.deadline_ms).toBeUndefined();
    return ok(JSON.stringify({ task_id: 42, candidate }));
  };
  await runExecution(execution, rig.deps, context().ctx);
  expect(calls).toEqual(["allocate", "record:ready", "prepare-repository", "record:ready", "container", "retain-candidate", "record:prepare"]);
  expect(rig.fakeFs.writes.has("/tmp/mc-home/runs/merge-42.repository.json")).toBe(false);
  expect(rig.fakeFs.writes.has("/tmp/mc-home/runs/merge-42.merge.json")).toBe(false);
});

test("application uses its exact run and grants no preparation command", async () => {
  const rig = makeRig();
  const applied = { task_id: 42, applied: { authorization: "merge-42", commit: "c".repeat(40) } };
  rig.deps.docker = async (args) => {
    expect(args).toContain("/operator/project:/workspace/source");
    expect(args).toContain("mc-project-merge-42");
    expect(args).toContain("no-new-privileges=true");
    expect(args.slice(-2)).toEqual(["apply", "/mc/repository.json"]);
    return ok(JSON.stringify(applied));
  };
  await runExecution({ ...execution, operation: "apply", application: { authorization: "merge-42" } }, rig.deps, context().ctx);
  expect(rig.mc.calls).toEqual([["delivery", "record", "--operation", "apply", "--run", "merge-42", "--result", "-"]]);
  expect(JSON.parse(rig.mc.inputs[0]!)).toEqual(applied);
});

test("merge preparation records the Git candidate without Project setup", async () => {
  const rig = makeRig();
  rig.deps.config = { ...rig.deps.config, runtimeVolume: "runtime-test" };
  const candidate = { status: "merged", commit: "c".repeat(40), submitted_commit: execution.submitted_commit };
  const events: string[] = [];
  rig.deps.runMc = async (args, input) => {
    if (args[0] === "environment") {
      events.push(`environment:${args[1]}`);
      return fail(2, "Project setup is unavailable.");
    }
    if (args[0] === "__repository") {
      const envelope = envelopeOf(rig, args);
      if (envelope.operation === "retain-candidate") {
        events.push("retain-candidate");
        expect(envelope.candidate).toEqual(candidate);
        return ok(JSON.stringify({ task_id: 42, candidate }));
      }
      return ok(JSON.stringify({ task_id: 42, input_commit: "a".repeat(40), destination_commit: "b".repeat(40) }));
    }
    if (args[3] === "prepare") {
      events.push("record:prepare");
      expect(JSON.parse(new TextDecoder().decode(input))).toEqual({ task_id: 42, candidate });
    }
    return ok("{}");
  };
  rig.deps.docker = async args => {
    events.push("merge");
    expect(args.slice(-2)).toEqual(["prepare", "/mc/repository.json"]);
    expect(args[args.indexOf("--network") + 1]).toBe("none");
    expect(args).toContain("runtime-test:/opt/culler-runtime:ro");
    expect(args.join(" ")).not.toContain(rig.deps.config.spineVolume);
    expect(args.join(" ")).not.toContain("docker.sock");
    return ok(JSON.stringify({ task_id: 42, candidate }));
  };
  await runExecution(execution, rig.deps, context().ctx);
  expect(events).toEqual(["merge", "retain-candidate", "record:prepare"]);
});

test("source acceptance records its environment hold before the merge and resolves it before dispatch", async () => {
  const rig = makeRig();
  rig.deps.config = { ...rig.deps.config, runtimeVolume: "runtime-test" };
  const events: string[] = [];
  const commit = "c".repeat(40);
  rig.deps.runMc = async (args, input) => {
    if (args[0] === "environment") {
      events.push(args[1]!);
      expect(JSON.parse(new TextDecoder().decode(input))).toMatchObject({
        project_id: "project", candidate_source: execution.submitted_commit, source_commit: commit,
      });
    } else events.push(`record:${args[3]}`);
    return ok("{}");
  };
  rig.deps.docker = async args => {
    events.push("merge");
    expect(args).toContain("runtime-test:/opt/culler-runtime:ro");
    return ok(JSON.stringify({ task_id: 42, applied: { commit, authorization: execution.run_id } }));
  };
  await runExecution({ ...execution, operation: "apply", application: {
    authorization: execution.run_id, candidate: { commit, submitted_commit: execution.submitted_commit },
  } }, rig.deps, context().ctx);
  expect(events).toEqual(["source-accepting", "merge", "source-accepted", "record:apply"]);
});

test("an uncertain source apply retains its environment hold for receipt recovery", async () => {
  const rig = makeRig();
  rig.deps.config = { ...rig.deps.config, runtimeVolume: "runtime-test" };
  const actions: string[] = [];
  rig.deps.runMc = async args => { actions.push(args.slice(0, 2).join(" ")); return ok("{}"); };
  rig.deps.docker = async () => fail(1, "Docker lost the apply result.");
  await expect(runExecution({ ...execution, operation: "apply" }, rig.deps, context().ctx)).rejects.toThrow("lost the apply result");
  expect(actions).toEqual(["environment source-accepting", "delivery fail"]);
});

test("source association holds follow proven merge outcomes", async () => {
  for (const [operation, status, cleared] of [
    ["reconcile", "operator-attention", false], ["reconcile", "not-applied", true],
    ["apply", "destination-changed", true], ["apply", "operator-attention", false],
  ] as const) {
    const rig = makeRig();
    rig.deps.config = { ...rig.deps.config, runtimeVolume: "runtime-test" };
    const actions: string[] = [];
    rig.deps.runMc = async args => {
      if (args[0] === "environment") actions.push(args[1]!);
      return ok(JSON.stringify({ task_id: 42, status }));
    };
    rig.deps.docker = async args => args[0] === "inspect" ? absent(args) : ok(JSON.stringify({ task_id: 42, status }));
    await runExecution({ ...execution, operation, ...(operation === "reconcile" ? { previous_run: "apply-42" } : {}) }, rig.deps, context().ctx);
    expect(actions).toEqual(cleared ? ["source-accepting", "source-aborted"] : ["source-accepting"]);
  }
});

test("a preparation retry confirms the previous container absence before repository access", async () => {
  const rig = makeRig();
  const docker = dockerRoute(rig);
  rig.mc.enqueue(fail(1, "The allocation stopped after the absence check."), ok("{}"));
  await expect(runExecution({ ...execution, previous_run: "previous-prepare" }, rig.deps, context().ctx))
    .rejects.toThrow("The allocation stopped after the absence check.");
  expect(docker).toEqual([
    ["inspect", "--type", "container", "mc-project-previous-prepare"],
    ["inspect", "--type", "container", "mc-setup-previous-prepare"],
  ]);
  expect(rig.mc.calls[0]![0]).toBe("__repository");
  expect(failures(rig)).toEqual([
    ["delivery", "fail", "--run", "merge-42", "--condition", "The allocation stopped after the absence check."],
  ]);
});

test("a preparation retry with a present previous container reports the failure without repository access", async () => {
  const rig = makeRig();
  rig.deps.docker = async () => ok(presentChild("previous-prepare", "mc-project", "running"));
  await expect(runExecution({ ...execution, previous_run: "previous-prepare" }, rig.deps, context().ctx))
    .rejects.toThrow("The container of the interrupted merge is present.");
  expect(rig.sleeps).toHaveLength(PRODUCER_ABSENCE_ATTEMPTS - 1);
  expect(rig.mc.calls).toEqual([
    ["delivery", "fail", "--run", "merge-42", "--condition", "The container of the interrupted merge is present."],
  ]);
});

test("reconcile confirms the merge container is absent", async () => {
  const rig = makeRig();
  const present = presentChild("apply-7", "mc-project", "exited");
  const docker: string[][] = [];
  rig.deps.docker = async (argv) => { docker.push(argv); return ok(present); };
  const reconcile = { ...execution, run_id: "reconcile-8", operation: "reconcile" as const, previous_run: "apply-7" };
  await expect(runExecution(reconcile, rig.deps, context().ctx))
    .rejects.toThrow("The container of the interrupted merge is present.");
  expect(docker.every((argv) => argv.join(" ") === "inspect --type container mc-project-apply-7")).toBe(true);
  expect(rig.mc.calls.some((args) => args[0] === "__repository")).toBe(false);
  expect(rig.mc.calls).toEqual([
    ["delivery", "fail", "--run", "reconcile-8", "--condition", "The container of the interrupted merge is present."],
  ]);
});

test("reconciliation records the old authorization after container absence", async () => {
  const rig = makeRig();
  const docker = dockerRoute(rig);
  const result = { task_id: 42, status: "not-applied" };
  rig.deps.runMc = async (args, stdin) => {
    rig.mc.calls.push(args);
    expect(docker).toHaveLength(2);
    if (args[0] === "__repository") {
      expect(args[1]).toBe("/tmp/mc-home/runs/reconcile-8.repository.json");
      const envelope = envelopeOf(rig, args);
      expect(envelope.operation).toBe("reconcile");
      expect(envelope.run_id).toBe("apply-7");
      expect(envelope.run_root).toBe("/tmp/mc-home/runs/repositories/apply-7");
      expect(envelope.application).toEqual({ authorization: "original" });
      return ok(JSON.stringify(result));
    }
    expect(args).toEqual(["delivery", "record", "--operation", "reconcile", "--run", "reconcile-8", "--result", "-"]);
    expect(JSON.parse(new TextDecoder().decode(stdin))).toEqual(result);
    return ok("{}");
  };
  await runExecution({
    ...execution, run_id: "reconcile-8", operation: "reconcile", previous_run: "apply-7",
    application: { authorization: "original" },
  }, rig.deps, context().ctx);
  expect(rig.mc.calls).toHaveLength(2);
  expect(docker.map((argv) => argv[3])).toEqual(["mc-project-apply-7", "mc-setup-apply-7"]);
});

test("a failed durable capture reports its failure and preserves the producer folder", async () => {
  const rig = makeRig();
  dockerRoute(rig);
  rig.deps.runMc = async (args) => {
    rig.mc.calls.push(args);
    if (args[0] === "__repository") {
      expect(envelopeOf(rig, args).run_id).toBe("worker-3");
      return ok(JSON.stringify({ task_id: 42, result_commit: "a".repeat(40) }));
    }
    if (args[1] === "record") return fail(1, "The result transaction failed.");
    return ok("{}");
  };
  const capture = { ...execution, run_id: "capture-4", operation: "capture" as const, producer_run: "worker-3" };
  await expect(runExecution(capture, rig.deps, context().ctx)).rejects.toThrow("The result transaction failed.");
  expect(failures(rig)).toEqual([
    ["delivery", "fail", "--run", "capture-4", "--condition", "The result transaction failed."],
  ]);
  expect(rig.fakeFs.events.filter((event) => event.startsWith("rm:"))).toEqual([
    "rm:/tmp/mc-home/runs/capture-4.repository.json",
    "rm:/tmp/mc-home/runs/capture-4.merge.json",
  ]);
});

test("capture in the grace reports container-present", async () => {
  const rig = makeRig();
  const docker: string[][] = [];
  rig.deps.docker = async (argv) => { docker.push(argv); return ok(presentChild("worker-3", "mc-project", "running")); };
  const capture = { ...execution, run_id: "capture-4", operation: "capture" as const, producer_run: "worker-3" };
  await runExecution(capture, rig.deps, context({ graceRemainingMs: () => 20000 }).ctx);
  expect(docker).toEqual([["inspect", "--type", "container", "mc-project-worker-3"]]);
  expect(rig.mc.calls).toEqual([
    ["delivery", "fail", "--run", "capture-4", "--condition", "A container of the ended run is still present.", "--container-present"],
  ]);
});

test("capture stops the container after the grace", async () => {
  const rig = makeRig();
  let stopped = false;
  const docker: string[][] = [];
  rig.deps.docker = async (argv) => {
    docker.push(argv);
    if (argv[0] === "inspect") {
      return stopped || argv[3] === "mc-setup-worker-3" ? absent(argv) : ok(presentChild("worker-3", "mc-project", "running"));
    }
    if (argv[0] === "rm") stopped = true;
    return argv[0] === "rm" && argv[2] === "mc-setup-worker-3" ? fail(1, "Error: No such container: mc-setup-worker-3") : ok("");
  };
  rig.mc.enqueue(ok(JSON.stringify({ task_id: 42, result_commit: "a".repeat(40) })), ok("{}"));
  const capture = { ...execution, run_id: "capture-4", operation: "capture" as const, producer_run: "worker-3" };
  await runExecution(capture, rig.deps, context({ graceRemainingMs: () => 0 }).ctx);
  expect(docker.map((argv) => argv.join(" "))).toEqual([
    "inspect --type container mc-project-worker-3",
    "stop mc-project-worker-3",
    "rm -f mc-project-worker-3",
    "stop mc-setup-worker-3",
    "rm -f mc-setup-worker-3",
    "inspect --type container mc-project-worker-3",
    "inspect --type container mc-setup-worker-3",
  ]);
  expect(rig.mc.calls.map((args) => args[0] === "__repository" ? "native" : args.slice(0, 4).join(" "))).toEqual([
    "native", "delivery record --operation capture",
  ]);
  expect(failures(rig)).toEqual([]);
});

test("capture after the grace waits for a removal that Docker already started", async () => {
  const rig = makeRig();
  const agent = "mc-project-worker-3";
  const states = ["running", "removing"];
  rig.deps.docker = async (argv) => {
    if (argv[0] === "inspect") {
      const state = argv[3] === agent ? states.shift() : undefined;
      return state === undefined ? absent(argv) : ok(presentChild("worker-3", "mc-project", state));
    }
    if (argv[0] === "rm") {
      return argv[2] === agent
        ? fail(1, `Error response from daemon: removal of container ${agent} is already in progress`)
        : fail(1, "Error: No such container: mc-setup-worker-3");
    }
    return ok("");
  };
  rig.mc.enqueue(ok(JSON.stringify({ task_id: 42, result_commit: "a".repeat(40) })), ok("{}"));
  const capture = { ...execution, run_id: "capture-4", operation: "capture" as const, producer_run: "worker-3" };
  await runExecution(capture, rig.deps, context({ graceRemainingMs: () => 0 }).ctx);
  expect(states).toEqual([]);
  expect(rig.sleeps).toEqual([PRODUCER_ABSENCE_POLL_MS]);
  expect(rig.mc.calls.map((args) => args[0] === "__repository" ? "native" : args.slice(0, 4).join(" "))).toEqual([
    "native", "delivery record --operation capture",
  ]);
  expect(failures(rig)).toEqual([]);
});

test("a failed container-present report repeats and keeps its flag", async () => {
  const rig = makeRig();
  rig.deps.docker = async () => ok(presentChild("worker-3", "mc-project", "running"));
  rig.mc.enqueue(fail(2, "The helper container is unavailable."), ok("{}"));
  const capture = { ...execution, run_id: "capture-4", operation: "capture" as const, producer_run: "worker-3" };
  await runExecution(capture, rig.deps, context({ graceRemainingMs: () => 20000 }).ctx);
  const report = ["delivery", "fail", "--run", "capture-4", "--condition", "A container of the ended run is still present.", "--container-present"];
  expect(rig.mc.calls).toEqual([report, report]);
  expect(rig.sleeps).toEqual([REPORT_RETRY_MS]);
});

test("a failure report that fails at each attempt sends no second report", async () => {
  const rig = makeRig();
  rig.deps.docker = async () => ok(presentChild("worker-3", "mc-project", "running"));
  rig.mc.fallback = fail(2, "The helper container is unavailable.");
  const capture = { ...execution, run_id: "capture-4", operation: "capture" as const, producer_run: "worker-3" };
  await expect(runExecution(capture, rig.deps, context({ graceRemainingMs: () => 20000 }).ctx))
    .rejects.toThrow("The failure report of repository execution capture-4 failed: The helper container is unavailable.");
  const report = ["delivery", "fail", "--run", "capture-4", "--condition", "A container of the ended run is still present.", "--container-present"];
  expect(rig.mc.calls).toEqual(Array.from({ length: REPORT_ATTEMPTS }, () => report));
  expect(rig.sleeps).toEqual(Array.from({ length: REPORT_ATTEMPTS - 1 }, () => REPORT_RETRY_MS));
});

test("cleanup with an unknown container state reports a charged failure", async () => {
  const rig = makeRig();
  rig.deps.docker = async () => fail(2, "Cannot connect to the Docker daemon");
  const cleanup = { ...execution, run_id: "cleanup-5", operation: "cleanup" as const, producer_run: "worker-3" };
  await expect(runExecution(cleanup, rig.deps, context({ graceRemainingMs: () => 20000 }).ctx))
    .rejects.toThrow("Culler cannot confirm that the containers of the ended run are absent.");
  expect(rig.mc.calls).toEqual([
    ["delivery", "fail", "--run", "cleanup-5", "--condition", "Culler cannot confirm that the containers of the ended run are absent."],
  ]);
});

test("allocation lock runs one allocate at a time", async () => {
  const rig = makeRig();
  let active = 0;
  let highest = 0;
  const gates = [deferred<void>(), deferred<void>()];
  const order: string[] = [];
  rig.deps.runMc = async (args) => {
    if (args[0] !== "__repository") return ok("{}");
    const envelope = envelopeOf(rig, args);
    if (envelope.operation !== "allocate") return fail(1, "The test stops after the allocation.");
    const index = order.length;
    order.push(envelope.run_id as string);
    active += 1;
    highest = Math.max(highest, active);
    await gates[index]!.promise;
    active -= 1;
    return ok(JSON.stringify({ task_id: 42, input_commit: "a".repeat(40), destination_commit: "b".repeat(40) }));
  };
  const first = runExecution({ ...execution, run_id: "merge-1" }, rig.deps, context().ctx).catch(() => undefined);
  const second = prepareRepositoryRun({
    task_id: 43, workspace_root: "/operator/project", input_commit: "a".repeat(40),
    destination_branch: "culler", max_temporary_bytes: 1024,
  }, "worker-2", rig.deps).catch(() => undefined);
  await new Promise((resolve) => setTimeout(resolve, 5));
  expect(order).toEqual(["merge-1"]);
  gates[0]!.resolve();
  await new Promise((resolve) => setTimeout(resolve, 5));
  expect(order).toEqual(["merge-1", "worker-2"]);
  gates[1]!.resolve();
  await Promise.all([first, second]);
  expect(highest).toBe(1);
});

test("a refused pre-container check starts no container and reports nothing", async () => {
  const rig = makeRig();
  let checks = 0;
  await runExecution({ ...execution, operation: "apply" }, rig.deps, context({
    checkBeforeContainer: async () => { checks += 1; return false; },
  }).ctx);
  expect(checks).toBe(1);
  expect(rig.docker.calls).toEqual([]);
  expect(rig.mc.calls).toEqual([]);
  expect(rig.fakeFs.writes.has("/tmp/mc-home/runs/merge-42.merge.json")).toBe(false);
});

test("a stop request ends the native call and reports nothing", async () => {
  const rig = makeRig();
  const { controller, ctx } = context();
  const native = deferred<ExecResult>();
  rig.mc.enqueue(() => native.promise);
  const flow = runExecution({ ...execution, operation: "observe" }, rig.deps, ctx);
  await new Promise((resolve) => setTimeout(resolve, 1));
  expect(rig.mc.signals[0]).toBe(ctx.signal);
  controller.abort();
  native.resolve({ exitCode: 143, stdout: "", stderr: "" });
  await flow;
  expect(rig.mc.calls).toHaveLength(1);
  expect(rig.mc.calls[0]![0]).toBe("__repository");
});

test("a stop request during the merge stops its container and reports nothing", async () => {
  const rig = makeRig();
  const { controller, ctx } = context();
  const run = deferred<ExecResult>();
  const docker: string[][] = [];
  rig.deps.docker = async (argv) => {
    docker.push(argv);
    if (argv[0] === "run") return run.promise;
    run.resolve({ exitCode: 137, stdout: "", stderr: "" });
    return ok("");
  };
  const flow = runExecution({ ...execution, operation: "apply" }, rig.deps, ctx);
  await new Promise((resolve) => setTimeout(resolve, 1));
  controller.abort();
  await flow;
  expect(docker.map((argv) => argv[0])).toEqual(["run", "stop"]);
  expect(docker[1]).toEqual(["stop", "mc-project-merge-42"]);
  expect(rig.mc.calls).toEqual([]);
});

test("an exact apply receipt after a stop request is recorded", async () => {
  const rig = makeRig();
  const { controller, ctx } = context();
  const run = deferred<ExecResult>();
  const applied = { task_id: 42, applied: { authorization: "merge-42", commit: "c".repeat(40) } };
  rig.deps.docker = async (argv) => argv[0] === "run" ? run.promise : ok("");
  const flow = runExecution({ ...execution, operation: "apply" }, rig.deps, ctx);
  await new Promise((resolve) => setTimeout(resolve, 1));
  controller.abort();
  run.resolve(ok(JSON.stringify(applied)));
  await flow;
  expect(rig.mc.calls).toEqual([["delivery", "record", "--operation", "apply", "--run", "merge-42", "--result", "-"]]);
  expect(JSON.parse(rig.mc.inputs[0]!)).toEqual(applied);
});

test("an execution with an invalid identity is refused before any work", async () => {
  const rig = makeRig();
  await expect(runExecution({ ...execution, project: "../host" }, rig.deps, context().ctx))
    .rejects.toThrow("has an invalid identity");
  expect(rig.mc.calls).toEqual([]);
  expect(rig.docker.calls).toEqual([]);
});

test("the agent repository receives its database input before launch", async () => {
  const rig = makeRig();
  rig.mc.enqueue(ok(JSON.stringify({ task_id: 42, input_commit: "a".repeat(40), destination_commit: "b".repeat(40) })), fail(1, "The input record failed."));
  await expect(prepareRepositoryRun({ task_id: 42, workspace_root: "/operator/project", input_commit: "a".repeat(40),
    destination_branch: "culler", max_temporary_bytes: 1024, }, "worker-42", rig.deps)).rejects.toThrow("The input record failed.");
  expect(rig.docker.calls).toEqual([]);
});

test("a failed evaluation capture retains its source through an uncharged cleanup result", async () => {
 const rig=makeRig();dockerRoute(rig);
 const calls:string[][]=[];let recorded:Record<string,unknown>={};
 rig.deps.runMc=async(args,input)=>{
  calls.push(args);
  if(args[0]==="evaluation")return fail(1,"The source bundle storage operation failed.");
  if(args[0]==="delivery"&&args[1]==="record"){recorded=JSON.parse(new TextDecoder().decode(input));return ok("{}");}
  throw new Error("The cleanup removed the source before capture.");
 };
 await runExecution({...execution,run_id:"cleanup-capture",operation:"cleanup",producer_run:"packager-original"},rig.deps,context().ctx);
 expect(calls.map(args=>args.slice(0,2))).toEqual([["evaluation","finish"],["delivery","record"]]);
 expect(recorded.cleanup_error).toBe("The source bundle storage operation failed.");
 expect(recorded.evaluation_pending).toBe(true);
});

// A wiki update. Its subject is the run identifier of the wiki update run.
const WIKI_RUN = "0123456789abcdef";
const WIKI_STEP = "fedcba9876543210";
const OTHER_STEP = "0f1e2d3c4b5a6978";
const START_COMMIT = "5".repeat(40);
const WIKI_COMMIT = "6".repeat(40);
const WIKI_DESTINATION = "7".repeat(40);
const wikiCandidate = {
  submitted_commit: WIKI_COMMIT, destination_branch: "culler", destination_commit: WIKI_DESTINATION,
  commit: "8".repeat(40), status: "merged",
};

function wikiExecution(fields: Partial<StartedExecution> & Pick<StartedExecution, "operation">): StartedExecution {
  return {
    run_id: WIKI_STEP, wiki_update: WIKI_RUN, project: "project",
    workspace_root: "/operator/project", destination_branch: "culler", deadline_ms: 300000, ...fields,
  };
}

function wikiReport(operation: string): string[] {
  return ["delivery", "record", "--operation", operation, "--run", WIKI_STEP, "--result", "-"];
}

/** A rig whose `mc __repository` returns `results` in order. `envelopes` and
 * `records` hold each native envelope and each recorded result. `other`
 * holds each other `mc` call. */
function wikiRig(results: unknown[]) {
  const rig = makeRig();
  const envelopes: Record<string, unknown>[] = [];
  const records: Array<{ argv: string[]; result: unknown }> = [];
  const other: string[][] = [];
  rig.deps.runMc = async (args, stdin) => {
    if (args[0] === "__repository") {
      envelopes.push(envelopeOf(rig, args));
      return ok(JSON.stringify(results.shift()));
    }
    if (args[0] === "delivery" && args[1] === "record") {
      records.push({ argv: args, result: JSON.parse(new TextDecoder().decode(stdin)) });
      return ok("{}");
    }
    other.push(args);
    return ok("{}");
  };
  return { rig, envelopes, records, other };
}

test("the subject fields hold one task or one wiki update", () => {
  expect(subjectFields(execution)).toEqual({ task_id: 42 });
  expect(subjectFields(wikiExecution({ operation: "cleanup" }))).toEqual({ wiki_update: WIKI_RUN });
  for (const invalid of [
    {}, { task_id: 42, wiki_update: WIKI_RUN }, { task_id: 0 }, { task_id: 1.5 }, { task_id: "42" },
    { wiki_update: "" }, { wiki_update: "../escape" }, { wiki_update: 7 }, null,
  ]) {
    expect(() => subjectFields(invalid as never), JSON.stringify(invalid)).toThrow("The repository subject is invalid.");
  }
});

test("the temporary repositories of wiki updates have their own folder", () => {
  const rig = makeRig();
  expect(repositoryRunRoot(rig.deps, "worker-3", { task_id: 42 })).toBe("/tmp/mc-home/runs/repositories/worker-3");
  expect(repositoryRunRoot(rig.deps, WIKI_STEP, { wiki_update: WIKI_RUN }))
    .toBe(`/tmp/mc-home/runs/wiki-repositories/${WIKI_STEP}`);
  expect(() => repositoryRunRoot(rig.deps, "../escape", { wiki_update: WIKI_RUN }))
    .toThrow("The repository run identifier is invalid.");
});

test("the capture of a wiki commit reads the repository of the wiki update run", async () => {
  const result = {
    wiki_update: WIKI_RUN, input_commit: START_COMMIT, destination_commit: "", result_commit: WIKI_COMMIT,
    retained_ref: `refs/mc/wiki/commits/${WIKI_RUN}`, temporary_bytes: 0, status: "",
  };
  const { rig, envelopes, records, other } = wikiRig([result]);
  const docker = dockerRoute(rig);
  await runExecution(wikiExecution({
    operation: "capture", producer_run: WIKI_RUN, input_commit: START_COMMIT, result_commit: WIKI_COMMIT,
    container_grace_ms: 41000,
  }), rig.deps, context().ctx);
  expect(docker.map((argv) => argv[3])).toEqual([`mc-project-${WIKI_RUN}`, `mc-setup-${WIKI_RUN}`]);
  expect(envelopes).toEqual([{
    operation: "capture", wiki_update: WIKI_RUN, run_id: WIKI_RUN, workspace_root: "/operator/project",
    run_root: `/tmp/mc-home/runs/wiki-repositories/${WIKI_RUN}`, input_commit: START_COMMIT,
    destination_branch: "culler", result_commit: WIKI_COMMIT, author_name: "Culler", author_email: "mc@localhost",
    deadline_ms: 300000,
  }]);
  expect(records).toEqual([{ argv: wikiReport("capture"), result }]);
  expect(other).toEqual([]);
});

test("the merge candidate step of a wiki commit uses the wiki folder and the start commit", async () => {
  const ready = { wiki_update: WIKI_RUN, input_commit: WIKI_COMMIT, destination_commit: WIKI_DESTINATION };
  const retained = {
    ...ready, retained_ref: `refs/mc/wiki/candidates/${WIKI_STEP}`, candidate: wikiCandidate, status: "",
  };
  const { rig, envelopes, records, other } = wikiRig([ready, ready, retained]);
  let merge: Record<string, unknown> = {};
  rig.deps.docker = async (args) => {
    expect(args).toContain(`/tmp/mc-home/runs/wiki-repositories/${WIKI_STEP}:/mc/repositories/${WIKI_STEP}`);
    expect(args.some((arg) => arg.includes("/operator/project"))).toBe(false);
    expect(args).toContain(`mc-project-${WIKI_STEP}`);
    expect(args.slice(-2)).toEqual(["prepare", "/mc/repository.json"]);
    merge = JSON.parse(rig.fakeFs.writes.get(`/tmp/mc-home/runs/${WIKI_STEP}.merge.json`)!);
    return ok(JSON.stringify({ wiki_update: WIKI_RUN, candidate: wikiCandidate }));
  };
  await runExecution(wikiExecution({
    operation: "prepare", deadline_ms: 600000, max_temporary_bytes: 21474836480,
    start_commit: START_COMMIT, submitted_commit: WIKI_COMMIT,
  }), rig.deps, context().ctx);
  const envelope = {
    wiki_update: WIKI_RUN, run_id: WIKI_STEP, workspace_root: "/operator/project",
    run_root: `/tmp/mc-home/runs/wiki-repositories/${WIKI_STEP}`, input_commit: WIKI_COMMIT,
    start_commit: START_COMMIT, destination_branch: "culler", max_temporary_bytes: 21474836480,
    author_name: "Culler", author_email: "mc@localhost",
  };
  expect(envelopes).toEqual([
    { ...envelope, operation: "allocate", deadline_ms: 600000 },
    { ...envelope, operation: "prepare-repository", destination_commit: WIKI_DESTINATION, deadline_ms: 600000 },
    {
      ...envelope, operation: "retain-candidate", destination_commit: WIKI_DESTINATION,
      candidate: wikiCandidate, deadline_ms: 600000,
    },
  ]);
  expect(merge).toEqual({
    ...envelope, operation: "prepare", destination_commit: WIKI_DESTINATION,
    workspace_root: "/workspace/source", run_root: `/mc/repositories/${WIKI_STEP}`,
  });
  expect(records).toEqual([
    { argv: wikiReport("ready"), result: ready },
    { argv: wikiReport("ready"), result: ready },
    { argv: wikiReport("prepare"), result: retained },
  ]);
  expect(other).toEqual([]);
});

test("the merge of a wiki commit runs in the operator checkout with its authorization", async () => {
  const application = { authorization: WIKI_STEP, candidate: wikiCandidate };
  const applied = {
    wiki_update: WIKI_RUN, input_commit: WIKI_COMMIT,
    applied: { authorization: WIKI_STEP, commit: wikiCandidate.commit }, status: "",
  };
  const { rig, envelopes, records, other } = wikiRig([]);
  let merge: Record<string, unknown> = {};
  rig.deps.docker = async (args) => {
    expect(args).toContain("/operator/project:/workspace/source");
    expect(args.some((arg) => arg.includes("/tmp/mc-home/runs/wiki-repositories/"))).toBe(false);
    expect(args).toContain(`mc-project-${WIKI_STEP}`);
    expect(args.slice(-2)).toEqual(["apply", "/mc/repository.json"]);
    merge = JSON.parse(rig.fakeFs.writes.get(`/tmp/mc-home/runs/${WIKI_STEP}.merge.json`)!);
    return ok(JSON.stringify(applied));
  };
  await runExecution(wikiExecution({
    operation: "apply", deadline_ms: 600000, max_temporary_bytes: 21474836480,
    submitted_commit: WIKI_COMMIT, application,
  }), rig.deps, context().ctx);
  expect(merge).toEqual({
    operation: "apply", wiki_update: WIKI_RUN, run_id: WIKI_STEP, workspace_root: "/workspace/source",
    run_root: `/mc/repositories/${WIKI_STEP}`, input_commit: WIKI_COMMIT, destination_branch: "culler",
    application, max_temporary_bytes: 21474836480, author_name: "Culler", author_email: "mc@localhost",
  });
  expect(envelopes).toEqual([]);
  expect(records).toEqual([{ argv: wikiReport("apply"), result: applied }]);
  expect(other).toEqual([]);
});

test("receipt recovery of a wiki merge reads the repository of the interrupted merge", async () => {
  const application = { authorization: OTHER_STEP, candidate: wikiCandidate };
  const result = {
    wiki_update: WIKI_RUN, input_commit: WIKI_COMMIT, destination_commit: WIKI_DESTINATION, status: "not-applied",
  };
  const { rig, envelopes, records, other } = wikiRig([result]);
  const docker = dockerRoute(rig);
  await runExecution(wikiExecution({
    operation: "reconcile", previous_run: OTHER_STEP, submitted_commit: WIKI_COMMIT, application,
  }), rig.deps, context().ctx);
  expect(docker.map((argv) => argv[3])).toEqual([`mc-project-${OTHER_STEP}`, `mc-setup-${OTHER_STEP}`]);
  expect(envelopes).toEqual([{
    operation: "reconcile", wiki_update: WIKI_RUN, run_id: OTHER_STEP, workspace_root: "/operator/project",
    run_root: `/tmp/mc-home/runs/wiki-repositories/${OTHER_STEP}`, input_commit: WIKI_COMMIT,
    destination_branch: "culler", application, author_name: "Culler", author_email: "mc@localhost",
    deadline_ms: 300000,
  }]);
  expect(records).toEqual([{ argv: wikiReport("reconcile"), result }]);
  expect(other).toEqual([]);
});

for (const [owner, producer] of [["the wiki update run", WIKI_RUN], ["a merge candidate step", OTHER_STEP]] as const) {
  test(`the cleanup of the repository of ${owner} sends no evaluation call`, async () => {
    const result = { wiki_update: WIKI_RUN, input_commit: "", destination_commit: "", temporary_bytes: 0, status: "" };
    const { rig, envelopes, records, other } = wikiRig([result]);
    const docker = dockerRoute(rig);
    await runExecution(wikiExecution({ operation: "cleanup", producer_run: producer, container_grace_ms: 0 }),
      rig.deps, context().ctx);
    expect(docker.map((argv) => argv[3])).toEqual([`mc-project-${producer}`, `mc-setup-${producer}`]);
    expect(envelopes).toEqual([{
      operation: "cleanup", wiki_update: WIKI_RUN, run_id: producer, workspace_root: "/operator/project",
      run_root: `/tmp/mc-home/runs/wiki-repositories/${producer}`, input_commit: "", destination_branch: "culler",
      author_name: "Culler", author_email: "mc@localhost", deadline_ms: 300000,
    }]);
    expect(records).toEqual([{ argv: wikiReport("cleanup"), result }]);
    expect(other).toEqual([]);
  });
}

for (const [name, subject] of [
  ["two subjects", { wiki_update: WIKI_RUN }],
  ["no subject", { task_id: undefined }],
] as const) {
  test(`an execution with ${name} reports the failure before any repository access`, async () => {
    const rig = makeRig();
    await expect(runExecution({ ...execution, ...subject }, rig.deps, context().ctx))
      .rejects.toThrow("The repository subject is invalid.");
    expect(rig.mc.calls).toEqual([
      ["delivery", "fail", "--run", "merge-42", "--condition", "The repository subject is invalid."],
    ]);
    expect(rig.docker.calls).toEqual([]);
    expect(rig.fakeFs.events.filter((event) => event.startsWith("write:"))).toEqual([]);
  });
}
