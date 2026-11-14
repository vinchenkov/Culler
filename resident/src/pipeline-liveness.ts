import type { Exec } from "./types";

// Docker container-name charset (also excludes path separators, so the id
// is safe to embed in mount paths).
export const SAFE_ID = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/;

// The Project id pattern `mc project add` admits (Docker container-name
// safe, at most 64 characters). The resident refuses
// an effect whose Project fails it rather than derive a container name from
// it: the id is a segment of `mc-<project>-<run_id>` and of the operator
// skill store path.
export const PROJECT_ID = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/;

/** The one agent container of pipeline run `runId` in Project `project`. */
export function pipelineContainerName(project: string, runId: string): string {
  return `mc-${project}-${runId}`;
}

/** The field order matches the canonical Go frame. */
export interface PipelineLivenessEntry {
  launch_age_s: number;
  present: boolean;
  run_id: string;
}

export interface PipelineLivenessDeps {
  docker: Exec;
  runMc: Exec;
  mcHome: string;
  now(): number;
  stat(path: string): Promise<{ mtimeMs: number }>;
  readFile(path: string): Promise<string>;
  log?(msg: string): void;
}

interface ActiveRun {
  project: string;
  run_id: string;
}

const RUN_ID = /^[0-9a-f]{16}$/;
const NOT_FOUND = /No such (?:container|object)/i;

/** The spine identifies active runs. Completed launch records cannot displace them.
 * An agent run holds the Project lease, or it is the Scribe run of a wiki update. */
export async function collectPipelineLiveness(deps: PipelineLivenessDeps): Promise<PipelineLivenessEntry[]> {
  const active: ActiveRun[] = [];
  const ids = new Set<string>();
  try {
    const response = await deps.runMc(["lease", "list"]);
    if (response.exitCode !== 0) throw new Error(response.stderr.trim() || "The lease read failed.");
    const body = JSON.parse(response.stdout) as { leases?: unknown };
    if (!Array.isArray(body?.leases)) throw new Error("The lease read returned no lease list.");
    for (const row of body.leases) {
      if (row?.run_id === null) continue;
      if (typeof row?.run_id !== "string" || !RUN_ID.test(row.run_id) ||
          typeof row.project !== "string" || !PROJECT_ID.test(row.project) || ids.has(row.run_id)) {
        throw new Error("The lease read returned an invalid run identity.");
      }
      ids.add(row.run_id);
      active.push({ project: row.project, run_id: row.run_id });
    }
  } catch (error) {
    deps.log?.(`Pipeline observation failed: ${String(error)}`);
    return [];
  }
  // A failed read of the wiki states keeps the observation of each lease holder.
  try {
    active.push(...await wikiUpdateRuns(deps, ids));
  } catch (error) {
    deps.log?.(`Pipeline observation omits the wiki update runs: ${String(error)}`);
  }

  const now = deps.now();
  const observed: PipelineLivenessEntry[] = [];
  for (const run of active) {
    const launchedAt = await launchTime(run, deps);
    if (launchedAt === undefined) continue;
    const present = await observeContainer(run.project, run.run_id, deps);
    if (present === undefined) continue;
    observed.push({
      launch_age_s: Math.max(0, Math.floor((now - launchedAt) / 1000)),
      present,
      run_id: run.run_id,
    });
  }
  observed.sort((a, b) => a.run_id.localeCompare(b.run_id));
  return observed;
}

/** The run of each wiki update whose run has no end. */
async function wikiUpdateRuns(deps: PipelineLivenessDeps, leaseHolders: Set<string>): Promise<ActiveRun[]> {
  const response = await deps.runMc(["wiki-state", "list"]);
  if (response.exitCode !== 0) throw new Error(response.stderr.trim() || "The wiki state read failed.");
  const body = JSON.parse(response.stdout) as { wiki_states?: unknown };
  if (!Array.isArray(body?.wiki_states)) throw new Error("The wiki state read returned no list.");
  const runs: ActiveRun[] = [];
  const ids = new Set(leaseHolders);
  for (const row of body.wiki_states) {
    const run = row?.active;
    if (run === null || typeof run?.run_ended_at === "string") continue;
    if (run?.run_ended_at !== null || typeof run.run_id !== "string" || !RUN_ID.test(run.run_id) ||
        typeof row.project !== "string" || !PROJECT_ID.test(row.project) || ids.has(run.run_id)) {
      throw new Error("The wiki state read returned an invalid run identity.");
    }
    ids.add(run.run_id);
    runs.push({ project: row.project, run_id: run.run_id });
  }
  return runs;
}

/** Both the launch timestamp and the current time come from the host clock. */
async function launchTime(run: ActiveRun, deps: PipelineLivenessDeps): Promise<number | undefined> {
  const path = `${deps.mcHome}/runs/${run.run_id}.json`;
  try {
    const record = JSON.parse(await deps.readFile(path));
    if (record?.run_id !== run.run_id || record.project !== run.project) return undefined;
    const { mtimeMs } = await deps.stat(path);
    if (Number.isFinite(mtimeMs)) return mtimeMs;
  } catch {
    // An absent or unreadable launch record supplies no age evidence.
  }
  return undefined;
}

async function observeContainer(
  project: string,
  runId: string,
  deps: PipelineLivenessDeps,
): Promise<boolean | undefined> {
  const name = pipelineContainerName(project, runId);
  let inspected: Awaited<ReturnType<Exec>>;
  try {
    inspected = await deps.docker(["inspect", "--type", "container", name]);
  } catch (err) {
    deps.log?.(`pipeline liveness: docker inspect ${name} failed: ${(err as Error).message}`);
    return undefined;
  }
  if (inspected.exitCode !== 0) {
    return NOT_FOUND.test(inspected.stderr) ? false : undefined;
  }
  // A successful inspect must describe exactly this container, or the answer
  // is ambiguous. Presence is the conservative reading anyway — it never
  // reaps — but an ambiguous record is not an observation of anything.
  let records: unknown;
  try {
    records = JSON.parse(inspected.stdout);
  } catch {
    return undefined;
  }
  if (!Array.isArray(records) || records.length !== 1) return undefined;
  const record = records[0] as { Name?: unknown } | null;
  if (record === null || typeof record !== "object" || record.Name !== `/${name}`) {
    return undefined;
  }
  return true;
}

/** How long an absence check waits for a container that Docker still tears
 * down, and how often it looks again. Attempts rather than a wall clock keep
 * the wait deterministic under test. */
export const PRODUCER_ABSENCE_ATTEMPTS = 30;
export const PRODUCER_ABSENCE_POLL_MS = 500;

/** One `docker inspect` against one exact container name. Returns the status
 * string when the object is present and unambiguous, `null` when Docker
 * positively reports not-found, and throws for every other shape. Only
 * presence is worth a retry: an ambiguous identity or an unavailable Docker
 * does not fix itself. */
export async function inspectPipelineRunContainer(
  name: string, runId: string, deps: { docker: Exec }, describe: string,
): Promise<string | null> {
  const inspected = await deps.docker(["inspect", "--type", "container", name]);
  const absentMessages = [
    "No such container",
    `Error: No such container: ${name}`,
    `Error: No such object: ${name}`,
    `Error response from daemon: No such container: ${name}`,
  ];
  if (inspected.exitCode === 1 && absentMessages.includes(inspected.stderr.trim()) &&
      (inspected.stdout.trim() === "" || inspected.stdout.trim() === "[]")) return null;
  if (inspected.exitCode !== 0) {
    throw new Error(`cannot confirm ${describe} absence for ${name}: docker inspect exited ${inspected.exitCode}`);
  }
  let records: unknown;
  try {
    records = JSON.parse(inspected.stdout);
  } catch {
    throw new Error(`${describe} identity is ambiguous for ${name}`);
  }
  if (!Array.isArray(records) || records.length !== 1 || records[0] === null || typeof records[0] !== "object") {
    throw new Error(`${describe} identity is ambiguous for ${name}`);
  }
  const record = records[0] as {
    Id?: unknown; Name?: unknown; Config?: { Labels?: Record<string, unknown> }; State?: { Status?: unknown };
  };
  const labels = record.Config?.Labels;
  if (typeof record.Id !== "string" || !/^[0-9a-f]{64}$/.test(record.Id) ||
      record.Name !== `/${name}` || labels === undefined ||
      labels["mc-managed"] !== "true" || labels["mc-tier"] !== "pipeline" || labels["mc-run-id"] !== runId ||
      typeof record.State?.Status !== "string" ||
      !(["created", "running", "paused", "restarting", "removing", "exited", "dead"] as string[]).includes(record.State.Status)) {
    throw new Error(`${describe} identity is ambiguous for ${name}`);
  }
  return record.State.Status;
}

export type ContainerPresence = "absent" | "present" | "unknown";

/** Checks `mc-<project>-<run>` and `mc-setup-<run>`. Only Docker's not-found
 * answer for both names is "absent". A container that remains after the last
 * attempt is "present". A failed or ambiguous inspect is "unknown". The two
 * names share one attempt budget, because one Docker tears down both. */
export async function confirmRunContainersAbsent(
  project: string, runId: string, deps: { docker: Exec; sleep(ms: number): Promise<void> }, attempts: number,
): Promise<ContainerPresence> {
  if (!SAFE_ID.test(runId) || !PROJECT_ID.test(project)) return "unknown";
  let attemptsLeft = attempts;
  for (const name of [pipelineContainerName(project, runId), `mc-setup-${runId}`]) {
    for (;;) {
      let status: string | null;
      try {
        status = await inspectPipelineRunContainer(name, runId, deps, "run container");
      } catch {
        return "unknown";
      }
      if (status === null) break;
      attemptsLeft -= 1;
      if (attemptsLeft <= 0) return "present";
      await deps.sleep(PRODUCER_ABSENCE_POLL_MS);
    }
  }
  return "absent";
}
