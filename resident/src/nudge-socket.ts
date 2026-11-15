// nudge-socket.ts — the resident's wake listener.
//
// A Collie message, Vision submission, Project start, or packet decision can
// wake the resident. Each request contains one recognized value followed by a
// newline.

import { chmod, lstat, rm } from "node:fs/promises";
import { join } from "node:path";
import type { Logger } from "../../runner/shared/log";

export const NUDGE_SOCKET_NAME = "resident.socket";

/** Each wake request fits within this byte limit. */
export const MAX_NUDGE_LINE_BYTES = 64;

const collieSessionID = /^h-[0-9a-f]{16}$/;

export function nudgeSocketPath(mcHome: string): string {
  return join(mcHome, NUDGE_SOCKET_NAME);
}

/** Returns the recognized wake request. */
export function parseNudgeLine(line: string): string | null {
  return collieSessionID.test(line) || line === "vision-save" || line === "project-start" ||
    line === "packet-decide" ? line : null;
}

export interface NudgeSocketHandle {
  path: string;
  stop(): Promise<void>;
}

function bounded(input: string): string {
  return JSON.stringify(input.slice(0, 32)) + (input.length > 32 ? "…" : "");
}

export async function startNudgeSocket(
  mcHome: string,
  nudge: (sessionId: string) => void,
  log: Logger,
): Promise<NudgeSocketHandle> {
  const path = nudgeSocketPath(mcHome);
  try {
    const stale = await lstat(path);
    if (!stale.isSocket() || stale.uid !== process.getuid?.()) {
      throw new Error("nudge socket path is occupied by an unsafe inode");
    }
    await rm(path);
  } catch (err) {
    if (!(err !== null && typeof err === "object" && "code" in err && err.code === "ENOENT")) {
      throw err;
    }
  }
  const states = new WeakMap<object, { body: string; done: boolean }>();
  const refuse = (socket: { end(): void }, state: { done: boolean }, reason: string, input: string) => {
    state.done = true;
    log.warn("tick.nudge_refused", `nudge refused: ${reason}: ${bounded(input)}`);
    socket.end();
  };
  const server = Bun.listen({
    unix: path,
    socket: {
      open(socket) {
        states.set(socket, { body: "", done: false });
      },
      data(socket, data) {
        const state = states.get(socket);
        if (state === undefined || state.done) return;
        state.body += Buffer.from(data).toString("utf8");
        if (Buffer.byteLength(state.body, "utf8") > MAX_NUDGE_LINE_BYTES) {
          refuse(socket, state, "input exceeds one line", state.body);
          return;
        }
        const newline = state.body.indexOf("\n");
        if (newline < 0) return;
        const line = state.body.slice(0, newline);
        const sessionId = parseNudgeLine(line);
        if (sessionId === null) {
          refuse(socket, state, "line is not a recognized wake request", line);
          return;
        }
        state.done = true;
        socket.end();
        nudge(sessionId);
      },
      error(_socket, err) {
        log.warn("tick.nudge_refused", `nudge socket error: ${String(err)}`);
      },
    },
  });
  try {
    await chmod(path, 0o600);
  } catch (err) {
    server.stop(true);
    await rm(path, { force: true });
    throw err;
  }
  return {
    path,
    async stop() {
      server.stop(true);
      await rm(path, { force: true });
    },
  };
}
