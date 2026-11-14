// behavior.ts — the fake harness's scripted-behavior schema and its
// fail-closed validator. See README.md for the documented contract.
//
// Design posture — conservative is defined: the validator
// rejects anything it does not recognize — unknown step kinds, missing
// terminal steps, steps after a terminal — rather than defaulting. A
// behavior file is trusted test input, but a typo in one must fail the
// test loudly, never silently succeed as an empty turn.

export type Step =
  | SucceedStep
  | ExecStep
  | SleepStep
  | CrashStep
  | HangStep;

/** Terminal: emit `turn-complete` (status "success") and exit 0. */
export interface SucceedStep {
  do: "succeed";
  /** The final assistant output for the turn. */
  output: string;
}

/** Non-terminal: run a shell command (how the fake "agent" calls `mc` in
 * later e2e), record a `tool-use` event with its exit code. The command's
 * exit code never changes the harness's own flow — the scripted terminal
 * step alone decides the exit status. */
export interface ExecStep {
  do: "exec";
  command: string;
}

/** Non-terminal: sleep N seconds (fractional allowed). */
export interface SleepStep {
  do: "sleep";
  seconds: number;
}

/** Terminal: exit immediately with code `code` — no `turn-complete` event,
 * like a real harness dying mid-turn. */
export interface CrashStep {
  do: "crash";
  code: number;
}

/** Terminal: never complete the turn and never exit — no `turn-complete`
 * event; the process stays alive until killed (how reap/timeout paths are
 * exercised). */
export interface HangStep {
  do: "hang";
}

export interface Behavior {
  /** Optional fixed native session handle; overridable by --session-id.
   * Defaults to "fake-session" (deterministic). */
  session_id?: string;
  /** Optional fixed ISO timestamp stamped on every event, for
   * byte-deterministic native.jsonl output in golden tests. */
  fixed_ts?: string;
  /** Executed in order; exactly one terminal step, and it must be last. */
  steps: Step[];
}

const TERMINAL = new Set(["succeed", "crash", "hang"]);
const KINDS = new Set(["succeed", "exec", "sleep", "crash", "hang"]);

export class BehaviorError extends Error {}

function fail(msg: string): never {
  throw new BehaviorError(`invalid behavior file: ${msg}`);
}

/** Parse and validate a behavior document. Throws BehaviorError on any
 * deviation from the schema — fail-closed, no defaults for the flow. */
export function parseBehavior(text: string): Behavior {
  let doc: unknown;
  try {
    doc = JSON.parse(text);
  } catch (e) {
    fail(`not valid JSON: ${(e as Error).message}`);
  }
  if (typeof doc !== "object" || doc === null || Array.isArray(doc)) {
    fail("top level must be a JSON object");
  }
  const obj = doc as Record<string, unknown>;

  for (const key of Object.keys(obj)) {
    if (!["session_id", "fixed_ts", "steps"].includes(key)) {
      fail(`unknown top-level key "${key}"`);
    }
  }
  if (obj.session_id !== undefined && typeof obj.session_id !== "string") {
    fail("session_id must be a string");
  }
  if (obj.fixed_ts !== undefined) {
    if (typeof obj.fixed_ts !== "string" || Number.isNaN(Date.parse(obj.fixed_ts))) {
      fail("fixed_ts must be an ISO-8601 timestamp string");
    }
  }
  if (!Array.isArray(obj.steps) || obj.steps.length === 0) {
    fail("steps must be a non-empty array");
  }

  const steps: Step[] = [];
  for (let i = 0; i < obj.steps.length; i++) {
    const raw = obj.steps[i];
    if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
      fail(`steps[${i}] must be an object`);
    }
    const s = raw as Record<string, unknown>;
    if (typeof s.do !== "string" || !KINDS.has(s.do)) {
      fail(`steps[${i}].do must be one of succeed|exec|sleep|crash|hang`);
    }
    const kind = s.do as Step["do"];

    const allowedKeys: Record<Step["do"], string[]> = {
      succeed: ["do", "output"],
      exec: ["do", "command"],
      sleep: ["do", "seconds"],
      crash: ["do", "code"],
      hang: ["do"],
    };
    for (const key of Object.keys(s)) {
      if (!allowedKeys[kind].includes(key)) {
        fail(`steps[${i}] ("${kind}") has unknown key "${key}"`);
      }
    }

    switch (kind) {
      case "succeed":
        if (typeof s.output !== "string") {
          fail(`steps[${i}] (succeed) requires string "output"`);
        }
        break;
      case "exec":
        if (typeof s.command !== "string" || s.command.length === 0) {
          fail(`steps[${i}] (exec) requires non-empty string "command"`);
        }
        break;
      case "sleep":
        if (typeof s.seconds !== "number" || !(s.seconds >= 0)) {
          fail(`steps[${i}] (sleep) requires number "seconds" >= 0`);
        }
        break;
      case "crash":
        if (
          typeof s.code !== "number" ||
          !Number.isInteger(s.code) ||
          s.code < 1 ||
          s.code > 255
        ) {
          fail(`steps[${i}] (crash) requires integer "code" in 1..255`);
        }
        break;
      case "hang":
        break;
    }

    const isTerminal = TERMINAL.has(kind);
    const isLast = i === obj.steps.length - 1;
    if (isTerminal && !isLast) {
      fail(`steps[${i}] ("${kind}") is terminal but not the last step`);
    }
    if (isLast && !isTerminal) {
      fail(
        `last step must be terminal (succeed|crash|hang), got "${kind}"`,
      );
    }

    steps.push(raw as Step);
  }

  return {
    session_id: obj.session_id as string | undefined,
    fixed_ts: obj.fixed_ts as string | undefined,
    steps,
  };
}
