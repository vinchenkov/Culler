#!/usr/bin/env bun
// cli.ts — the fake harness: a CLI/SDK-shaped stub that implements the
// Runtime Adapter contract.
//
// Shape of the contract it fakes, faithfully:
//   - the runner starts the harness and supplies ONE top-level turn
//     (stdin or --turn-file);
//   - the harness appends its native session record (native.jsonl) into
//     the designated session folder AS IT RUNS (append per event, no
//     buffered write-at-exit — nothing is lost if it dies mid-turn);
//   - the run signal is the structured stream on stdout (each event
//     mirrored as one JSON line), read only to detect completion and
//     exit status;
//   - it exits with the scripted status.
//
// Deterministic, token-free, no network: everything it does comes from
// the behavior file. See README.md for the schema (the fake family's
// adapter contract doc).

import { appendFileSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { BehaviorError, parseBehavior, type Behavior } from "./behavior";

const USAGE = `usage: fake-harness --behavior <file> --session-dir <dir> [--turn-file <file>] [--session-id <id>]

Reads the top-level turn from --turn-file, or from stdin when omitted.
Appends JSONL events to <session-dir>/native.jsonl and mirrors each event
to stdout. Exits with the scripted status (0 for succeed, K for crash).`;

const CAPTURE_LIMIT = 8 * 1024; // bytes of tool stdout/stderr kept per event

interface Args {
  behavior: string;
  sessionDir: string;
  turnFile?: string;
  sessionId?: string;
}

class UsageError extends Error {}

function parseArgs(argv: string[]): Args {
  const out: Partial<Args> = {};
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i];
    const need = (name: string): string => {
      const v = argv[++i];
      if (v === undefined) throw new UsageError(`${name} requires a value`);
      return v;
    };
    switch (flag) {
      case "--behavior":
        out.behavior = need(flag);
        break;
      case "--session-dir":
        out.sessionDir = need(flag);
        break;
      case "--turn-file":
        out.turnFile = need(flag);
        break;
      case "--session-id":
        out.sessionId = need(flag);
        break;
      case "--help":
      case "-h":
        console.log(USAGE);
        process.exit(0);
      default:
        throw new UsageError(`unknown argument "${flag}"`);
    }
  }
  if (!out.behavior) throw new UsageError("--behavior is required");
  if (!out.sessionDir) throw new UsageError("--session-dir is required");
  return out as Args;
}

function truncate(s: string): string {
  const buf = Buffer.from(s, "utf-8");
  if (buf.byteLength <= CAPTURE_LIMIT) return s;
  // The cap is bytes (8 KiB), not UTF-16 code units. Back off to a UTF-8
  // character boundary so the kept prefix is always valid UTF-8.
  let end = CAPTURE_LIMIT;
  while (end > 0 && (buf[end]! & 0xc0) === 0x80) end--;
  return buf.toString("utf-8", 0, end);
}

/** One emitter per session: appends each event to native.jsonl in the
 * session dir and mirrors it to stdout, in that order (durable first). */
function makeEmit(sessionDir: string, fixedTs: string | undefined) {
  const path = join(sessionDir, "native.jsonl");
  return (event: Record<string, unknown>): void => {
    const line = JSON.stringify({
      ...event,
      ts: fixedTs ?? new Date().toISOString(),
    });
    appendFileSync(path, line + "\n");
    process.stdout.write(line + "\n");
  };
}

async function readTurn(args: Args): Promise<string> {
  if (args.turnFile !== undefined) {
    return readFileSync(args.turnFile, "utf-8");
  }
  return await Bun.stdin.text();
}

export async function main(argv: string[]): Promise<number> {
  let args: Args;
  let behavior: Behavior;
  let turn: string;
  try {
    args = parseArgs(argv);
    behavior = parseBehavior(readFileSync(args.behavior, "utf-8"));
    // The host creates the session folder before launch. The harness only
    // ever writes into it. Fail closed if absent.
    if (!statSync(args.sessionDir).isDirectory()) {
      throw new UsageError(`--session-dir ${args.sessionDir} is not a directory`);
    }
    turn = await readTurn(args);
  } catch (e) {
    if (e instanceof UsageError || e instanceof BehaviorError) {
      console.error(`fake-harness: ${e.message}`);
      if (e instanceof UsageError) console.error(USAGE);
      return 2;
    }
    console.error(`fake-harness: ${(e as Error).message}`);
    return 2;
  }

  const sessionId = args.sessionId ?? behavior.session_id ?? "fake-session";
  const emit = makeEmit(args.sessionDir, behavior.fixed_ts);

  emit({ event: "session-start", session_id: sessionId, harness: "fake" });
  emit({ event: "turn-start", session_id: sessionId, turn: 1, input: turn });

  for (const step of behavior.steps) {
    switch (step.do) {
      case "exec": {
        const proc = Bun.spawnSync(["/bin/sh", "-c", step.command], {
          stdout: "pipe",
          stderr: "pipe",
        });
        emit({
          event: "tool-use",
          session_id: sessionId,
          turn: 1,
          tool: "shell",
          command: step.command,
          exit_code: proc.exitCode,
          stdout: truncate(proc.stdout.toString()),
          stderr: truncate(proc.stderr.toString()),
        });
        break;
      }
      case "sleep":
        await Bun.sleep(step.seconds * 1000);
        break;
      case "succeed":
        emit({
          event: "turn-complete",
          session_id: sessionId,
          turn: 1,
          status: "success",
          output: step.output,
        });
        return 0;
      case "crash":
        // A real harness dying mid-turn writes no completion event.
        return step.code;
      case "hang":
        // Keep the event loop alive forever; only a signal ends us.
        setInterval(() => {}, 1 << 30);
        return await new Promise<number>(() => {});
    }
  }
  // Unreachable: the validator guarantees a terminal last step.
  console.error("fake-harness: internal error: no terminal step executed");
  return 70;
}

if (import.meta.main) {
  process.exit(await main(process.argv.slice(2)));
}
