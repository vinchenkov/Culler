import type { ExecResult, TickDeps } from "./types";
import { setupError, setupCommandError } from "../../runner/shared/setup-error";

type Dependencies = Pick<TickDeps, "fs" | "runMc" | "log" | "config">;
type Observation = {
  resident_instance_id: string;
  observed_at: string;
  status: "succeeded" | "failed";
  message?: string;
  details?: string;
  project_id?: string;
  operation_id?: string;
  result_error?: string;
  result_details?: string;
};
type Pending = { project_id: string; operation_id: string; result: Record<string, any>; observation?: Observation };
const id = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/;

export function createEnvironmentHealth(deps: Dependencies, instance: string, now = () => new Date()) {
  const directory = `${deps.config.mcHome}/health`;
  const pendingDirectory = `${directory}/setup-results`;
  const pending = new Map<string, Pending>();
  const deliveries = new Map<string, Promise<ExecResult>>();
  const redactors = new Map<string, (text: string) => string>();
  let request: Observation | undefined;
  let loaded = false;
  let publication = Promise.resolve();

  const atomic = async (path: string, value: unknown) => {
    const stage = `${path}.${instance}.next`;
    await deps.fs.writeFile(stage, JSON.stringify(value), { mode: 0o600 });
    await deps.fs.rename(stage, path);
  };
  const publish = () => {
    const value = { request, submissions: [...pending.values()].flatMap(row => row.observation ? [row.observation] : []) };
    publication = publication.catch(() => {}).then(async () => {
      await deps.fs.mkdir(directory, { mode: 0o700 });
      await atomic(`${directory}/resident-setup.json`, value);
    }).catch(() => deps.log.warn("spawn.gate_failed", "Culler could not publish setup health."));
    return publication;
  };
  const observation = (error?: unknown, redact?: (text: string) => string): Observation => ({
    resident_instance_id: instance, observed_at: now().toISOString(), status: error === undefined ? "succeeded" : "failed",
    ...(error === undefined ? {} : { message: setupError(error, redact).message, details: setupError(error, redact).details }),
  });
  const savePending = async (row: Pending) => {
    await deps.fs.mkdir(pendingDirectory, { mode: 0o700 });
    await atomic(`${pendingDirectory}/${row.operation_id}.json`, row);
  };
  const load = async () => {
    if (loaded) return;
    if (!request) {
      try {
        const prior = JSON.parse(await deps.fs.readFile(`${directory}/resident-setup.json`)).request;
        if (prior?.resident_instance_id && prior.observed_at && ["failed", "succeeded"].includes(prior.status)) request = prior;
      } catch {}
    }
    let names: string[];
    try { names = await deps.fs.readdir(pendingDirectory); }
    catch (error) { if ((error as { code?: string }).code !== "ENOENT") throw error; names = []; }
    for (const name of names.filter(name => name.endsWith(".json"))) {
      const row = JSON.parse(await deps.fs.readFile(`${pendingDirectory}/${name}`)) as Pending;
      if (!id.test(row.operation_id) || name !== `${row.operation_id}.json` || !id.test(row.project_id) ||
          row.result?.operation_id !== row.operation_id || !["failed", "succeeded"].includes(row.result?.status)) {
        throw new Error("A retained setup result has an invalid identity.");
      }
      pending.set(row.operation_id, row);
    }
    loaded = true;
  };
  const attempt = async (row: Pending): Promise<ExecResult> => {
    try {
      await savePending(row);
      const result = await deps.runMc(["environment", "operation-result"], new TextEncoder().encode(JSON.stringify(row.result)));
      if (result.exitCode !== 0) {
        // A completed or canceled operation can supersede a retained result.
        const read = await deps.runMc(["environment", "get"], new TextEncoder().encode(JSON.stringify({ project_id: row.project_id })));
        const recorded = read.exitCode === 0 ? JSON.parse(read.stdout)?.operations?.find((op: any) => op.id === row.operation_id) : undefined;
        if (!recorded || !["succeeded", "failed", "canceled"].includes(recorded.status)) {
          throw setupCommandError("Culler could not save the operation result.", result);
        }
      }
      await deps.fs.rm(`${pendingDirectory}/${row.operation_id}.json`);
      pending.delete(row.operation_id);
      redactors.delete(row.operation_id);
      await publish();
      return { exitCode: 0, stdout: "{}", stderr: "" };
    } catch (error) {
      row.observation = { ...observation(error, redactors.get(row.operation_id)), project_id: row.project_id, operation_id: row.operation_id,
        ...(row.result.status === "failed" ? { result_error: row.result.error, result_details: row.result.metadata?.failure_details } : {}) };
      await savePending(row).catch(() => deps.log.warn("spawn.gate_failed", "Culler could not retain the setup result for a restart."));
      deps.log.warn("spawn.gate_failed", row.observation.message!, { run_id: row.operation_id });
      await publish();
      // The retained result retries independently of operation execution.
      return { exitCode: 0, stdout: "{}", stderr: "" };
    }
  };
  const deliver = (row: Pending): Promise<ExecResult> => {
    const current = deliveries.get(row.operation_id);
    if (current) return current;
    const done = attempt(row).finally(() => deliveries.delete(row.operation_id));
    deliveries.set(row.operation_id, done);
    return done;
  };
  return {
    has: (operation: string) => pending.has(operation),
    async recover() { await load(); for (const row of [...pending.values()]) await deliver(row); },
    async recordRequest(error?: unknown) { request = observation(error); await publish(); },
    async submit(project: string, result: Record<string, any>, redact?: (text: string) => string) {
      if (!id.test(result.operation_id) || !id.test(project)) throw new Error("The setup result has an invalid identity.");
      const row = pending.get(result.operation_id) ?? { project_id: project, operation_id: result.operation_id, result };
      pending.set(row.operation_id, row);
      if (redact) redactors.set(row.operation_id, redact);
      return deliver(row);
    },
  };
}
