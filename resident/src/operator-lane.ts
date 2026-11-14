import type { Exec, ExecResult } from "./types";
import type { Logger } from "../../runner/shared/log";

export interface OperatorLaneOptions {
  /** The longest wait for one listing or recovery command. */
  commandTimeoutMs?: number;
  /** Records the time at which the lane last worked. */
  publish?: (now: Date) => Promise<void>;
  /** The least time between two records. */
  publishIntervalMs?: number;
}

export function createOperatorLane(
  runMc: Exec, log: Logger, intervalMs = 1000, maintenance: () => Promise<boolean> = async () => false,
  { commandTimeoutMs = 30_000, publish, publishIntervalMs = 5_000 }: OperatorLaneOptions = {},
) {
  let timer: ReturnType<typeof setInterval> | undefined;
  let stopped = false;
  let round: Promise<void> | undefined;
  let execution: AbortController | undefined;
  let published = Number.NEGATIVE_INFINITY;

  // The record must never delay or fail a round.
  function alive(): void {
    if (publish === undefined || Date.now() - published < publishIntervalMs) return;
    published = Date.now();
    void publish(new Date()).catch(() => {
      log.warn("operator.health_unpublished", "The host could not record the operator lane health.");
    });
  }

  // A command that never returns must not stop the lane. The limit stops the
  // command and fails the round, and the next round starts on the next tick.
  async function bounded(args: string[], input?: Uint8Array): Promise<ExecResult> {
    const control = new AbortController();
    let limit: ReturnType<typeof setTimeout> | undefined;
    const expired = new Promise<never>((_, reject) => {
      limit = setTimeout(() => {
        control.abort();
        log.warn("operator.command_timed_out", "An operator transport command exceeded its time limit.",
          { command: args[0], limit_ms: commandTimeoutMs });
        reject(new Error("The operator transport command exceeded its time limit."));
      }, commandTimeoutMs);
    });
    const command = runMc(args, input, { signal: control.signal });
    command.catch(() => {});
    try { return await Promise.race([command, expired]); }
    finally { clearTimeout(limit); }
  }

  async function inspect(): Promise<void> {
    if (await maintenance()) { alive(); return; }
    const listed = await bounded(["__operator-due"]);
    if (listed.exitCode !== 0) throw new Error("The operator requests are unavailable.");
    const value = JSON.parse(listed.stdout);
    if (!Array.isArray(value?.operations)) throw new Error("The operator request list is invalid.");
    alive();
    for (const request of value.operations) {
      if (stopped || await maintenance()) return;
      if (typeof request?.id !== "string" || !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/.test(request.id)) {
        throw new Error("An operator request identifier is invalid.");
      }
      execution = new AbortController();
      const deadline = setTimeout(() => execution?.abort(), 5 * 60_000);
      let failed = false;
      try {
        const result = await runMc(["__operator-execute", request.id], undefined, { signal: execution.signal });
        failed = result.exitCode !== 0;
      }
      catch { failed = true; }
      finally { clearTimeout(deadline); execution = undefined; }
      if (failed) {
        await bounded(["__operator-recover"], new TextEncoder().encode(JSON.stringify({ id: request.id })));
        log.warn("operator.request_failed", "The host could not retain a complete operator result.", { operation: request.id });
      }
    }
  }

  function tick(): void {
    if (stopped) return;
    if (round !== undefined) {
      // A request in execution is work, not a stalled lane.
      if (execution !== undefined) alive();
      return;
    }
    round = inspect().catch(() => {
      log.warn("operator.unavailable", "The host could not inspect operator requests.");
    }).finally(() => { round = undefined; });
  }

  return {
    activeCount: () => execution === undefined ? 0 : 1,
    async start() {
      const recovered = await bounded(["__operator-recover"]);
      if (recovered.exitCode !== 0) throw new Error("The operator request recovery failed.");
      stopped = false;
      alive();
      timer = setInterval(tick, intervalMs);
      timer.unref?.();
      tick();
    },
    async stop() {
      stopped = true;
      if (timer !== undefined) clearInterval(timer);
      execution?.abort();
      await round;
    },
  };
}
