// The resident runs one dispatch cycle at a time. The tick selects agent runs
// only. The repository lane runs repository operations outside the tick. The
// mc command owns dispatch decisions and recovery.

import { applyEffect } from "./effects";
import type { Effect, TickDeps } from "./types";

export interface TickHandle {
  /** Runs a round now, or books one more round after the in-flight one. */
  nudge(): void;
  /** Clears the timer and waits for the current cycle. */
  stop(): Promise<void>;
}

export function startTickLoop(deps: TickDeps): TickHandle {
  let inFlight: Promise<void> | null = null;
  let pending = false;
  let stopped = false;

  const timer = deps.setTimer(() => fire(), deps.intervalMs);

  function fire(): Promise<void> | void {
    if (stopped) return;
    if (inFlight) {
      deps.log.warn("tick.skipped", "tick skipped: previous tick still in flight");
      return;
    }
    return run();
  }

  function run(): Promise<void> {
    const p = tick(deps)
      .catch((err) => {
        deps.log.warn("tick.failed", `tick failed: ${err instanceof Error ? err.message : String(err)}`);
      })
      .finally(() => {
        inFlight = null;
        try {
          deps.afterTick?.();
        } catch (err) {
          deps.log.warn("tick.failed", `after-tick callback failed: ${err instanceof Error ? err.message : String(err)}`);
        }
        if (pending && !stopped) {
          pending = false;
          run();
        }
      });
    inFlight = p;
    return p;
  }

  function nudge(): void {
    if (stopped) return;
    if (inFlight) {
      pending = true;
      return;
    }
    deps.log.debug("tick.nudged", "tick: a nudge runs a round now");
    void run();
  }

  return {
    nudge,
    async stop() {
      if (!stopped) {
        stopped = true;
        pending = false;
        deps.clearTimer(timer);
      }
      while (inFlight) await inFlight;
    },
  };
}

type TickEffect = Extract<Effect, { action: "tick" }>;

/** One tick: `mc dispatch`, then effect the tick object it returns. Exported
 * for tests. */
export async function tick(deps: TickDeps): Promise<void> {
  if (deps.maintenance !== undefined && await deps.maintenance()) {
    await deps.tickComplete?.();
    return;
  }
  if (deps.beforeTick !== undefined) await deps.beforeTick();
  const res = await deps.runMc(["dispatch"]);
  if (res.exitCode !== 0) {
    deps.log.warn("tick.dispatch_failed", `mc dispatch failed (exit ${res.exitCode}): ${res.stderr.trim()}`,
      { exit: res.exitCode });
    return;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(res.stdout);
  } catch {
    deps.log.warn("tick.dispatch_failed", `mc dispatch: unparseable effect JSON: ${res.stdout.trim().slice(0, 200)}`);
    return;
  }
  // `mc dispatch` writes exactly one tick object. Anything else is not a
  // dispatch result the resident can effect.
  const object = parsed as { action?: unknown; actions?: unknown } | null;
  if (object === null || typeof object !== "object" || object.action !== "tick" || !Array.isArray(object.actions)) {
    deps.log.warn("tick.dispatch_failed", `mc dispatch: not a tick object: ${res.stdout.trim().slice(0, 200)}`);
    return;
  }
  await applyTick(object as TickEffect, deps);
  await deps.tickComplete?.();
}

/** Effect committed actions in tick order. A failed host action leaves the
 * other committed actions available. */
export async function applyTick(tick: TickEffect, deps: TickDeps): Promise<void> {
  for (const [index, action] of tick.actions.entries()) {
    const envelope = action as unknown as { action?: unknown; error?: { code?: unknown; message?: unknown } } | null;
    if (envelope !== null && typeof envelope === "object" && envelope.action === undefined && envelope.error !== undefined) {
      deps.log.warn("tick.dispatch_refused",
        `tick: round ${index} refused (${String(envelope.error?.code)}): ${String(envelope.error?.message)}`);
      continue;
    }
    try {
      await applyEffect(action, deps);
    } catch (err) {
      const kind = String((action as { action?: unknown } | null)?.action);
      deps.log.warn("tick.failed",
        `tick: action ${index} (${kind}) failed: ${err instanceof Error ? err.message : String(err)}; continuing with the next action`);
    }
  }
  for (const entry of tick.idle ?? []) {
    deps.log.debug("tick.idle", `tick: ${entry.project} idle (${entry.reason})`);
  }
}
