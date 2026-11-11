import type { Exec } from "./types";
import type { Logger } from "../../runner/shared/log";

export function createOperatorLane(runMc: Exec, log: Logger, intervalMs = 1000, maintenance: () => Promise<boolean> = async () => false) {
  let timer: ReturnType<typeof setInterval> | undefined;
  let stopped = false;
  let round: Promise<void> | undefined;
  let execution: AbortController | undefined;

  async function inspect(): Promise<void> {
    if (await maintenance()) return;
    const listed = await runMc(["__operator-due"]);
    if (listed.exitCode !== 0) throw new Error("The operator requests are unavailable.");
    const value = JSON.parse(listed.stdout);
    if (!Array.isArray(value?.operations)) throw new Error("The operator request list is invalid.");
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
        await runMc(["__operator-recover"], new TextEncoder().encode(JSON.stringify({ id: request.id })));
        log.warn("operator.request_failed", "The host could not retain a complete operator result.", { operation: request.id });
      }
    }
  }

  function tick(): void {
    if (stopped || round !== undefined) return;
    round = inspect().catch(() => {
      log.warn("operator.unavailable", "The host could not inspect operator requests.");
    }).finally(() => { round = undefined; });
  }

  return {
    activeCount: () => execution === undefined ? 0 : 1,
    async start() {
      const recovered = await runMc(["__operator-recover"]);
      if (recovered.exitCode !== 0) throw new Error("The operator request recovery failed.");
      stopped = false;
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
