// repository-lane.ts — the repository lane of the resident. The lane
// registers the owner number of this resident instance, sends each start
// request, renews the active executions, applies the stop requests of mc,
// and runs the host work of each execution. The tick has no part in this
// work, and a repository operation holds no Project lease.

import type { ExecResult, TickDeps } from "./types";
import {
  removeRunContainers, runExecution, subjectFields, type ExecutionContext, type StartedExecution,
} from "./repository";
import { confirmRunContainersAbsent, pipelineContainerName, PROJECT_ID, SAFE_ID } from "./pipeline-liveness";

export type StartEvent = "resident-start" | "container-exit" | "execution-end" | "tick" | "packet-decision"
  | "collie-turn" | "due-time" | "heartbeat" | "expiry-end" | "pause-drain" | "pause-end" | "retry";

export interface LaneDeps extends Pick<TickDeps, "runMc" | "docker" | "log" | "fs" | "config" | "sleep"> {
  /** One-shot timer. */
  schedule(fn: () => void, ms: number): unknown;
  cancel(handle: unknown): void;
  /** Runs one docker command (without the docker binary) and delivers each
   * line of its standard output. `done` resolves with the exit code. */
  follow(argv: string[], onLine: (line: string) => void): { stop(): void; done: Promise<number> };
  /** Host monotonic milliseconds. */
  now(): number;
  /** The install pause check. The acknowledgement is ready only when
   * `active` is 0. */
  maintenance(active: number): Promise<boolean>;
  /** The shutdown chain, then the process exit. */
  exit(code: number): void;
}

export interface RepositoryLane {
  /** Registers the owner, starts the followers and the heartbeat timer, and
   * sends the first start request. */
  start(): Promise<void>;
  /** One start request. Events during a start request cause one more. */
  request(event: StartEvent): void;
  /** The install pause check of the tick. It takes the lane lock. */
  pauseCheck(): Promise<boolean>;
  followCollie(containerId: string): void;
  activeCount(): number;
  stop(): Promise<void>;
}

export const HEARTBEAT_INTERVAL_MS = 20_000;
export const RETRY_DELAY_MS = 5_000;
export const EARLIER_OWNER_POLL_MS = 5_000;
/** The check before a merge container sends at most this number of
 * heartbeats. The attempts and their waits stay inside the 90-second
 * heartbeat limit of an execution. */
export const PRE_CONTAINER_ATTEMPTS = 3;
/** The line that the Collie runner writes after each harness turn. */
export const COLLIE_TURN_ENDED = "collie-turn-ended";

interface StopEntry {
  run_id: string;
  project: string;
  operation: string;
  owner: number;
  wait_ms: number;
}

interface FailedStart {
  project?: string;
  task_id?: number;
  wiki_update?: string;
  operation?: string;
  recorded?: boolean;
  run_id?: string;
  code?: string;
  message?: string;
}

interface StartResponse {
  executions: StartedExecution[];
  stops: StopEntry[];
  failed: FailedStart[];
  due_in_ms: number | null;
  drain_pending: boolean;
}

interface HeartbeatResponse {
  renewed: string[];
  unstarted: string[];
  stops: StopEntry[];
  confirmed: string[];
  ended: string[];
  container_stops: Array<{ run_id: string; project: string }>;
}

interface Flow {
  execution: StartedExecution;
  controller: AbortController;
  done: Promise<void>;
}

type Follower = { stop(): void; done: Promise<number> };

const MERGE_CONTAINER_OPERATIONS = new Set(["prepare", "apply"]);

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function detail(result: ExecResult): string {
  return (result.stderr.trim() || result.stdout.trim()).slice(0, 500);
}

function refusalCode(result: ExecResult): string | undefined {
  try {
    const body = JSON.parse(result.stdout) as { error?: { code?: unknown } } | null;
    return typeof body?.error?.code === "string" ? body.error.code : undefined;
  } catch {
    return undefined;
  }
}

/** The subject of an execution or of a failed start, for a log record. An
 * invalid subject gives no field, so no context value is undefined. */
function subjectOf(x: { task_id?: unknown; wiki_update?: unknown }): { task_id?: number; wiki_update?: string } {
  try {
    return subjectFields(x);
  } catch {
    return {};
  }
}

function subjectText(x: { task_id?: unknown; wiki_update?: unknown }): string {
  const subject = subjectOf(x);
  if (subject.wiki_update !== undefined) return `wiki update ${subject.wiki_update}`;
  if (subject.task_id !== undefined) return `task ${subject.task_id}`;
  return "an invalid subject";
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

function stopEntries(value: unknown): StopEntry[] {
  if (!Array.isArray(value)) return [];
  return value.filter((entry): entry is StopEntry => entry !== null && typeof entry === "object" &&
    typeof entry.run_id === "string" && typeof entry.project === "string" &&
    typeof entry.operation === "string" && typeof entry.owner === "number" &&
    typeof entry.wait_ms === "number");
}

function parseStartResponse(stdout: string): StartResponse {
  const body = JSON.parse(stdout) as Record<string, unknown> | null;
  if (body === null || typeof body !== "object" || !Array.isArray(body.executions) || !Array.isArray(body.stops)) {
    throw new Error("The start response has no execution list or stop list.");
  }
  return {
    executions: body.executions as StartedExecution[],
    stops: stopEntries(body.stops),
    failed: Array.isArray(body.failed) ? body.failed as FailedStart[] : [],
    due_in_ms: typeof body.due_in_ms === "number" ? body.due_in_ms : null,
    drain_pending: body.drain_pending === true,
  };
}

function parseHeartbeatResponse(stdout: string): HeartbeatResponse {
  const body = JSON.parse(stdout) as Record<string, unknown> | null;
  if (body === null || typeof body !== "object" || !Array.isArray(body.renewed) || !Array.isArray(body.stops)) {
    throw new Error("The heartbeat response has no renewed list or stop list.");
  }
  const containerStops = Array.isArray(body.container_stops) ? body.container_stops.filter(
    (entry): entry is { run_id: string; project: string } => entry !== null && typeof entry === "object" &&
      typeof entry.run_id === "string" && typeof entry.project === "string") : [];
  return {
    renewed: strings(body.renewed),
    unstarted: strings(body.unstarted),
    stops: stopEntries(body.stops),
    confirmed: strings(body.confirmed),
    ended: strings(body.ended),
    container_stops: containerStops,
  };
}

export function createRepositoryLane(deps: LaneDeps): RepositoryLane {
  let owner: number | undefined;
  let stopping = false;
  let refused = false;
  let heartbeatStopped = false;
  // Each execution whose flow runs now, by execution run.
  const registry = new Map<string, Flow>();
  // Each run whose flow ended. A run leaves when a response omits it from
  // the executions.
  const finished = new Set<string>();
  // Each run whose host work stopped. A run leaves when a response omits it
  // from the stops.
  const stopped = new Set<string>();
  // The unstarted runs of the latest heartbeat response.
  let unstarted = new Set<string>();
  const waiters = new Set<string>();
  const containerStops = new Set<string>();
  let pause: "none" | "drain" | "confirmed" = "none";
  let drainPending = true;
  let pending = false;
  let looping = false;
  let dueTimer: unknown;
  let retryTimer: unknown;
  let heartbeatTimer: unknown;
  let followerTimer: unknown;
  const delays = new Map<unknown, () => void>();
  let exitFollower: Follower | undefined;
  const collieFollowers = new Map<string, Follower>();
  let lock: Promise<unknown> = Promise.resolve();

  const closed = () => stopping || refused;

  function withLock<T>(fn: () => Promise<T>): Promise<T> {
    const section = lock.then(fn);
    lock = section.then(() => undefined, () => undefined);
    return section;
  }

  function delay(ms: number): Promise<void> {
    return new Promise((resolve) => {
      const handle = deps.schedule(() => {
        delays.delete(handle);
        resolve();
      }, ms);
      delays.set(handle, resolve);
    });
  }

  function cancelTimer(handle: unknown): undefined {
    if (handle !== undefined) deps.cancel(handle);
    return undefined;
  }

  function closeTimersAndFollowers(): void {
    dueTimer = cancelTimer(dueTimer);
    retryTimer = cancelTimer(retryTimer);
    followerTimer = cancelTimer(followerTimer);
    for (const [handle, resolve] of delays) {
      deps.cancel(handle);
      resolve();
    }
    delays.clear();
    exitFollower?.stop();
    exitFollower = undefined;
    for (const follower of collieFollowers.values()) follower.stop();
    collieFollowers.clear();
  }

  function refuse(): void {
    if (refused) return;
    refused = true;
    heartbeatStopped = true;
    heartbeatTimer = cancelTimer(heartbeatTimer);
    for (const flow of registry.values()) flow.controller.abort();
    closeTimersAndFollowers();
    deps.log.error("repository.owner_refused",
      "mc refused the owner number of this resident instance; the resident exits so that supervision starts a new instance",
      { owner: owner ?? 0 });
    deps.exit(1);
  }

  function isStaleOwner(result: ExecResult): boolean {
    return result.exitCode === 1 && refusalCode(result) === "stale-owner";
  }

  // The pause check runs under the lane lock.
  async function pauseCheckLocked(): Promise<boolean> {
    let active = registry.size + unstarted.size;
    if (pause !== "confirmed" && drainPending) active += 1;
    const requested = await deps.maintenance(active);
    if (!requested) {
      if (pause !== "none") {
        pause = "none";
        drainPending = true;
        request("pause-end");
      }
      return false;
    }
    if (active === 0) {
      pause = "confirmed";
    } else if (pause === "none") {
      pause = "drain";
      request("pause-drain");
    }
    return true;
  }

  function scheduleRetry(): void {
    if (retryTimer !== undefined || closed()) return;
    retryTimer = deps.schedule(() => {
      retryTimer = undefined;
      request("retry");
    }, RETRY_DELAY_MS);
  }

  function failStart(text: string): void {
    deps.log.warn("repository.start_failed", `${text}; the lane retries in ${RETRY_DELAY_MS / 1000} seconds`);
    scheduleRetry();
  }

  async function stopMergeContainer(project: string, run: string): Promise<void> {
    const name = pipelineContainerName(project, run);
    try {
      const result = await deps.docker(["stop", name]);
      if (result.exitCode !== 0 && !/No such container/i.test(result.stderr)) {
        deps.log.warn("repository.stop_failed", `docker stop ${name} exited ${result.exitCode}: ${result.stderr.trim()}`,
          { run_id: run });
      }
    } catch (error) {
      deps.log.warn("repository.stop_failed", `docker stop ${name} failed: ${message(error)}`, { run_id: run });
    }
  }

  /** Waits for the merge container of an earlier owner. After `wait_ms`, the
   * lane stops and removes it. An absent container confirms the stop. */
  function waitForEarlierContainer(stop: StopEntry): void {
    if (waiters.has(stop.run_id) || stopped.has(stop.run_id)) return;
    if (!SAFE_ID.test(stop.run_id) || !PROJECT_ID.test(stop.project)) {
      deps.log.warn("repository.stop_failed", `the stop request names an invalid run ${JSON.stringify(stop.run_id)}`);
      return;
    }
    waiters.add(stop.run_id);
    const began = deps.now();
    const name = pipelineContainerName(stop.project, stop.run_id);
    void (async () => {
      while (!closed()) {
        if (await confirmRunContainersAbsent(stop.project, stop.run_id, deps, 1) === "absent") {
          stopped.add(stop.run_id);
          request("execution-end");
          return;
        }
        if (deps.now() - began >= stop.wait_ms) {
          await deps.docker(["stop", name]);
          const removed = await deps.docker(["rm", "-f", name]);
          if (removed.exitCode !== 0 && !/No such container/i.test(removed.stderr)) {
            deps.log.warn("repository.stop_failed", `docker rm ${name} exited ${removed.exitCode}: ${removed.stderr.trim()}`,
              { run_id: stop.run_id });
          }
        }
        await delay(EARLIER_OWNER_POLL_MS);
      }
    })().catch((error) => {
      deps.log.warn("repository.stop_failed", `the wait for ${name} failed: ${message(error)}`, { run_id: stop.run_id });
    }).finally(() => waiters.delete(stop.run_id));
  }

  /** The stops of a response come before its new executions. The lane awaits
   * the docker stop commands, not the end of the flows. */
  async function applyStops(stops: StopEntry[]): Promise<void> {
    const commands: Promise<void>[] = [];
    for (const stop of stops) {
      if (stop.owner === owner) {
        const flow = registry.get(stop.run_id);
        if (flow === undefined) {
          stopped.add(stop.run_id);
          continue;
        }
        if (flow.controller.signal.aborted) continue;
        flow.controller.abort();
        if (MERGE_CONTAINER_OPERATIONS.has(stop.operation)) commands.push(stopMergeContainer(stop.project, stop.run_id));
      } else if (MERGE_CONTAINER_OPERATIONS.has(stop.operation)) {
        waitForEarlierContainer(stop);
      } else {
        stopped.add(stop.run_id);
      }
    }
    await Promise.all(commands);
  }

  function register(execution: StartedExecution): void {
    const run = execution.run_id;
    const controller = new AbortController();
    const registeredAt = deps.now();
    const deadline = deps.schedule(() => controller.abort(), Math.max(0, execution.deadline_ms));
    const ctx: ExecutionContext = {
      signal: controller.signal,
      checkBeforeContainer: () => checkBeforeContainer(run, controller.signal),
      graceRemainingMs: () => Math.max(0, (execution.container_grace_ms ?? 0) - (deps.now() - registeredAt)),
    };
    const flow: Flow = { execution, controller, done: Promise.resolve() };
    registry.set(run, flow);
    flow.done = (async () => {
      try {
        await runExecution(execution, deps, ctx);
      } catch (error) {
        deps.log.warn("repository.execution_failed",
          `repository execution ${run} (${String(execution.operation)}) failed: ${message(error)}`,
          { run_id: run, operation: String(execution.operation), ...subjectOf(execution) });
      } finally {
        deps.cancel(deadline);
        registry.delete(run);
        finished.add(run);
        stopped.add(run);
        request("execution-end");
      }
    })();
  }

  async function startSection(): Promise<void> {
    await withLock(async () => {
      if (closed() || owner === undefined) return;
      try {
        await pauseCheckLocked();
      } catch (error) {
        failStart(`the install pause check failed: ${message(error)}`);
        return;
      }
      if (pause === "confirmed" || closed()) return;
      const body = { owner, running: [...registry.keys()], stopped: [...stopped], drain: pause === "drain" };
      let result: ExecResult;
      try {
        result = await deps.runMc(["delivery", "start", "--request", "-"], new TextEncoder().encode(JSON.stringify(body)));
      } catch (error) {
        failStart(`mc delivery start failed: ${message(error)}`);
        return;
      }
      if (refused) return;
      if (isStaleOwner(result)) {
        refuse();
        return;
      }
      if (result.exitCode !== 0) {
        failStart(`mc delivery start exited ${result.exitCode}: ${detail(result)}`);
        return;
      }
      let response: StartResponse;
      try {
        response = parseStartResponse(result.stdout);
      } catch (error) {
        failStart(`mc delivery start returned an invalid response: ${message(error)}`);
        return;
      }
      const listedStops = new Set(response.stops.map((stop) => stop.run_id));
      for (const run of [...stopped]) if (!listedStops.has(run)) stopped.delete(run);
      const listed = new Set(response.executions.map((execution) => execution?.run_id));
      for (const run of [...finished]) if (!listed.has(run)) finished.delete(run);
      await applyStops(response.stops);
      if (refused) return;
      for (const execution of response.executions) {
        const run = execution?.run_id;
        if (typeof run !== "string" || !SAFE_ID.test(run)) {
          deps.log.warn("repository.execution_failed", `the start response lists an invalid execution: ${JSON.stringify(execution).slice(0, 200)}`);
          continue;
        }
        if (registry.has(run) || finished.has(run) || stopped.has(run)) continue;
        register(execution);
        unstarted.delete(run);
      }
      for (const failure of response.failed) {
        deps.log.warn("repository.execution_failed",
          `the start of ${String(failure.operation)} for ${subjectText(failure)} failed (${String(failure.code)}): ${String(failure.message)}`,
          { project: String(failure.project), operation: String(failure.operation), recorded: failure.recorded === true,
            ...subjectOf(failure) });
      }
      dueTimer = cancelTimer(dueTimer);
      if (response.due_in_ms !== null && !closed()) {
        dueTimer = deps.schedule(() => {
          dueTimer = undefined;
          request("due-time");
        }, Math.max(0, response.due_in_ms));
      }
      if (pause === "drain") drainPending = response.drain_pending;
      try {
        await pauseCheckLocked();
      } catch (error) {
        deps.log.warn("repository.start_failed", `the install pause check failed: ${message(error)}`);
      }
    });
  }

  function request(_event: StartEvent): void {
    if (closed()) return;
    pending = true;
    if (looping) return;
    looping = true;
    void (async () => {
      try {
        while (pending && !closed()) {
          pending = false;
          try {
            await startSection();
          } catch (error) {
            failStart(`the start request failed: ${message(error)}`);
          }
        }
      } finally {
        looping = false;
      }
    })();
  }

  /** Returns the response, or the text of the failure. */
  async function sendHeartbeat(containers: string[]): Promise<HeartbeatResponse | { failure: string }> {
    const failed = (text: string) => {
      deps.log.warn("repository.heartbeat_failed", text);
      return { failure: text };
    };
    const body = { owner, running: [...registry.keys()], stopped: [...stopped], containers };
    let result: ExecResult;
    try {
      result = await deps.runMc(["delivery", "heartbeat", "--request", "-"], new TextEncoder().encode(JSON.stringify(body)));
    } catch (error) {
      return failed(`mc delivery heartbeat failed: ${message(error)}`);
    }
    if (isStaleOwner(result)) {
      refuse();
      return { failure: "mc refused the owner number of this resident instance" };
    }
    if (result.exitCode !== 0) return failed(`mc delivery heartbeat exited ${result.exitCode}: ${detail(result)}`);
    try {
      return parseHeartbeatResponse(result.stdout);
    } catch (error) {
      return failed(`mc delivery heartbeat returned an invalid response: ${message(error)}`);
    }
  }

  /** Stops and removes the containers of an ended agent run after its grace.
   * The lane does not await this work. */
  function stopEndedRunContainers(entry: { run_id: string; project: string }): void {
    if (containerStops.has(entry.run_id)) return;
    if (!SAFE_ID.test(entry.run_id) || !PROJECT_ID.test(entry.project)) {
      deps.log.warn("repository.stop_failed", `the container stop names an invalid run ${JSON.stringify(entry.run_id)}`);
      return;
    }
    containerStops.add(entry.run_id);
    void removeRunContainers(entry.project, entry.run_id, deps).then((failures) => {
      for (const failure of failures) deps.log.warn("repository.stop_failed", failure, { run_id: entry.run_id });
    }).finally(() => containerStops.delete(entry.run_id));
  }

  async function applyHeartbeat(response: HeartbeatResponse): Promise<void> {
    const listedStops = new Set(response.stops.map((stop) => stop.run_id));
    for (const run of [...stopped]) if (!listedStops.has(run)) stopped.delete(run);
    unstarted = new Set(response.unstarted.filter((run) => !finished.has(run) && !registry.has(run)));
    await applyStops(response.stops);
    for (const entry of response.container_stops) stopEndedRunContainers(entry);
    if (response.confirmed.length > 0 || response.ended.length > 0 || unstarted.size > 0) request("heartbeat");
  }

  async function heartbeatSection(): Promise<void> {
    await withLock(async () => {
      if (refused || owner === undefined) return;
      try {
        await pauseCheckLocked();
      } catch (error) {
        deps.log.warn("repository.heartbeat_failed", `the install pause check failed: ${message(error)}`);
      }
      let containers: string[] = [];
      try {
        const listed = await deps.docker(["ps", "-a", "--filter", "label=mc-managed=true",
          "--filter", "label=mc-tier=pipeline", "--format", "{{.Label \"mc-run-id\"}}"]);
        if (listed.exitCode !== 0) throw new Error(`docker ps exited ${listed.exitCode}: ${listed.stderr.trim()}`);
        containers = [...new Set(listed.stdout.split("\n").map((line) => line.trim()).filter((line) => line !== ""))];
      } catch (error) {
        deps.log.warn("repository.heartbeat_failed", `the pipeline container list failed: ${message(error)}`);
        containers = [];
      }
      const response = await sendHeartbeat(containers);
      if (!("failure" in response)) await applyHeartbeat(response);
    });
  }

  function scheduleHeartbeat(): void {
    if (heartbeatStopped) return;
    heartbeatTimer = deps.schedule(() => {
      heartbeatTimer = undefined;
      void heartbeatSection()
        .catch((error) => deps.log.warn("repository.heartbeat_failed", `the heartbeat failed: ${message(error)}`))
        .finally(() => scheduleHeartbeat());
    }, HEARTBEAT_INTERVAL_MS);
  }

  /** The last heartbeat of an execution is also the check directly before
   * its merge container starts. Only an owner refusal, a stop request, or a
   * response without a renewal of the run refuses the start. A failed call
   * repeats outside the lock, and the last failure ends the execution. */
  async function checkBeforeContainer(run: string, signal: AbortSignal): Promise<boolean> {
    let failure = "";
    for (let attempt = 1; attempt <= PRE_CONTAINER_ATTEMPTS; attempt += 1) {
      if (attempt > 1) await deps.sleep(RETRY_DELAY_MS);
      const outcome = await withLock(async (): Promise<boolean | string> => {
        if (signal.aborted || refused) return false;
        const response = await sendHeartbeat([]);
        if ("failure" in response) return response.failure;
        await applyHeartbeat(response);
        return response.renewed.includes(run) && !signal.aborted;
      });
      if (typeof outcome === "boolean") return outcome;
      if (refused || signal.aborted) return false;
      failure = outcome;
    }
    throw new Error(`The heartbeat before the merge container failed: ${failure}`);
  }

  function startExitFollower(): void {
    if (closed()) return;
    let follower: Follower;
    try {
      follower = deps.follow([
        "events", "--filter", "type=container", "--filter", "event=die",
        "--filter", "label=mc-tier=pipeline", "--format", "{{.Actor.Attributes.name}}",
      ], () => request("container-exit"));
    } catch (error) {
      restartExitFollower(`the container exit follower did not start: ${message(error)}`);
      return;
    }
    exitFollower = follower;
    request("container-exit");
    follower.done.then(
      (code) => { if (exitFollower === follower) restartExitFollower(`the container exit follower ended with exit ${code}`); },
      (error) => { if (exitFollower === follower) restartExitFollower(`the container exit follower failed: ${message(error)}`); },
    );
  }

  function restartExitFollower(text: string): void {
    exitFollower = undefined;
    if (closed()) return;
    deps.log.warn("repository.follow_failed", `${text}; it restarts in ${RETRY_DELAY_MS / 1000} seconds`);
    followerTimer = deps.schedule(() => {
      followerTimer = undefined;
      startExitFollower();
    }, RETRY_DELAY_MS);
  }

  function followCollie(containerId: string): void {
    if (closed()) return;
    if (!/^[0-9a-f]{12,64}$/.test(containerId)) {
      deps.log.warn("repository.follow_failed", `the Collie container id ${JSON.stringify(containerId)} is invalid`);
      return;
    }
    const key = containerId.slice(0, 12);
    if (collieFollowers.has(key)) return;
    let follower: Follower;
    try {
      follower = deps.follow(["logs", "--follow", "--tail", "0", containerId], (line) => {
        if (line.trim() === COLLIE_TURN_ENDED) request("collie-turn");
      });
    } catch (error) {
      deps.log.warn("repository.follow_failed", `the Collie follower for ${key} did not start: ${message(error)}`);
      return;
    }
    collieFollowers.set(key, follower);
    const forget = () => { if (collieFollowers.get(key) === follower) collieFollowers.delete(key); };
    follower.done.then(forget, forget);
  }

  async function followRunningCollies(): Promise<void> {
    try {
      const listed = await deps.docker(["ps", "--filter", "label=mc-tier=collie", "--format", "{{.ID}}"]);
      if (listed.exitCode !== 0) throw new Error(`docker ps exited ${listed.exitCode}: ${listed.stderr.trim()}`);
      for (const id of listed.stdout.split("\n").map((line) => line.trim()).filter((line) => line !== "")) {
        followCollie(id);
      }
    } catch (error) {
      deps.log.warn("repository.follow_failed", `the Collie container list failed: ${message(error)}`);
    }
  }

  async function registerOwner(): Promise<boolean> {
    while (!closed()) {
      try {
        const result = await deps.runMc(["delivery", "register"]);
        if (result.exitCode === 0) {
          const body = JSON.parse(result.stdout) as { owner?: unknown };
          if (typeof body.owner === "number" && Number.isSafeInteger(body.owner) && body.owner > 0) {
            owner = body.owner;
            return true;
          }
          throw new Error("mc delivery register returned no owner number");
        }
        throw new Error(`mc delivery register exited ${result.exitCode}: ${detail(result)}`);
      } catch (error) {
        deps.log.warn("repository.start_failed", `${message(error)}; the lane registers again in ${RETRY_DELAY_MS / 1000} seconds`);
      }
      await delay(RETRY_DELAY_MS);
    }
    return false;
  }

  return {
    async start() {
      if (!await registerOwner()) return;
      startExitFollower();
      await followRunningCollies();
      if (closed()) return;
      scheduleHeartbeat();
      request("resident-start");
    },
    request,
    pauseCheck: () => withLock(() => pauseCheckLocked()),
    followCollie,
    activeCount: () => registry.size,
    async stop() {
      if (!stopping) {
        stopping = true;
        closeTimersAndFollowers();
      }
      // A start request in flight can still register an execution, so the
      // lock settles before each check of the registry.
      for (;;) {
        await lock;
        if (registry.size === 0) break;
        await Promise.all([...registry.values()].map((flow) => flow.done));
      }
      heartbeatStopped = true;
      heartbeatTimer = cancelTimer(heartbeatTimer);
      await lock;
    },
  };
}
