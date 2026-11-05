// nudge-socket.test.ts — the wake listener's contract: an owner-only socket
// file at MC_HOME/resident.socket, one line per nudge, bad input dropped and
// logged once, a stale file replaced at boot, the file removed at stop.

import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdtemp, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { LogRecord } from "../../runner/shared/log";
import { makeLogger } from "../../runner/shared/log";
import { MAX_NUDGE_LINE_BYTES, parseNudgeLine, startNudgeSocket, type NudgeSocketHandle } from "./nudge-socket";

const sessionId = "h-0123456789abcdef";

let home: string;
let records: LogRecord[];
let nudges: string[];
let handle: NudgeSocketHandle | undefined;

beforeEach(async () => {
  home = await mkdtemp(join(tmpdir(), "mc-nudge-"));
  records = [];
  nudges = [];
});

afterEach(async () => {
  await handle?.stop();
  handle = undefined;
  await rm(home, { recursive: true, force: true });
});

function log() {
  return makeLogger({
    comp: "resident",
    level: "debug",
    sink: { write(line) { records.push(JSON.parse(line) as LogRecord); } },
  });
}

async function start(): Promise<NudgeSocketHandle> {
  handle = await startNudgeSocket(home, (id) => { nudges.push(id); }, log());
  return handle;
}

/** Writes bytes to the socket and resolves once the resident closed it. */
function send(path: string, bytes: string): Promise<void> {
  return new Promise((resolve, reject) => {
    Bun.connect({
      unix: path,
      socket: {
        open(socket) {
          socket.write(bytes);
          socket.flush();
        },
        data() {},
        close() { resolve(); },
        error(_socket, err) { reject(err); },
        connectError(_socket, err) { reject(err); },
      },
    }).catch(reject);
  });
}

describe("parseNudgeLine", () => {
  test("accepts exactly a Homie session id", () => {
    expect(parseNudgeLine(sessionId)).toBe(sessionId);
    for (const bad of ["", "h-", "h-0123456789ABCDEF", "h-0123456789abcde", "h-0123456789abcdef0",
      "r-0123456789abcdef", ` ${sessionId}`, `${sessionId} `, "dispatch"]) {
      expect(parseNudgeLine(bad)).toBeNull();
    }
  });
});

describe("the nudge socket", () => {
  test("listens on MC_HOME/resident.socket as an owner-only socket", async () => {
    const socket = await start();
    expect(socket.path).toBe(join(home, "resident.socket"));
    const info = await stat(socket.path);
    expect(info.isSocket()).toBe(true);
    expect(info.mode & 0o777).toBe(0o600);
    expect(info.uid).toBe(process.getuid!());
  });

  test("one valid line nudges once with the session id", async () => {
    const socket = await start();
    await send(socket.path, `${sessionId}\n`);
    expect(nudges).toEqual([sessionId]);
    expect(records.filter((r) => r.event === "tick.nudge_refused")).toEqual([]);
  });

  test("a Vision submission wakes the resident once", async () => {
    const socket = await start();
    await send(socket.path, "vision-save\n");
    expect(nudges).toEqual(["vision-save"]);
    expect(records.filter((r) => r.event === "tick.nudge_refused")).toEqual([]);
  });

  test("a Project start wakes the resident once", async () => {
    const socket = await start();
    await send(socket.path, "project-start\n");
    expect(nudges).toEqual(["project-start"]);
    expect(records.filter((r) => r.event === "tick.nudge_refused")).toEqual([]);
  });

  test("a packet decision wakes the resident once", async () => {
    const socket = await start();
    await send(socket.path, "packet-decide\n");
    expect(nudges).toEqual(["packet-decide"]);
    expect(records.filter((r) => r.event === "tick.nudge_refused")).toEqual([]);
  });

  test("a line that is not a session id is dropped and logged once", async () => {
    const socket = await start();
    await send(socket.path, "dispatch now\nh-0123456789abcdef\n");
    expect(nudges).toEqual([]);
    const refused = records.filter((r) => r.event === "tick.nudge_refused");
    expect(refused).toHaveLength(1);
    expect(refused[0]!.msg).toContain("not a recognized wake request");
  });

  test("input over the line bound is dropped with a bounded log line", async () => {
    const socket = await start();
    await send(socket.path, "x".repeat(MAX_NUDGE_LINE_BYTES + 200));
    expect(nudges).toEqual([]);
    const refused = records.filter((r) => r.event === "tick.nudge_refused");
    expect(refused).toHaveLength(1);
    expect(refused[0]!.msg.length).toBeLessThan(120);
  });

  test("bytes after the first line are ignored", async () => {
    const socket = await start();
    await send(socket.path, `${sessionId}\n${sessionId}\n`);
    expect(nudges).toEqual([sessionId]);
  });

  test("a stale socket file is replaced at boot; a foreign inode refuses", async () => {
    const path = join(home, "resident.socket");
    // A forced process exit leaves the socket file behind.
    const dead = Bun.spawn(["mise", "exec", "--", process.execPath, "-e", `
      Bun.listen({ unix: process.env.CULLER_TEST_SOCKET, socket: { data() {} } });
      console.log("ready");
    `], { env: { ...process.env, CULLER_TEST_SOCKET: path }, stdout: "pipe", stderr: "pipe" });
    try {
      const { value } = await dead.stdout.getReader().read();
      expect(new TextDecoder().decode(value)).toBe("ready\n");
    } finally {
      dead.kill("SIGKILL");
      await dead.exited;
    }
    expect((await stat(path)).isSocket()).toBe(true);
    const socket = await start();
    await send(socket.path, `${sessionId}\n`);
    expect(nudges).toEqual([sessionId]);
    await handle!.stop();
    handle = undefined;
    await writeFile(path, "not a socket");
    await expect(startNudgeSocket(home, () => {}, log())).rejects.toThrow("unsafe inode");
    expect(await Bun.file(path).text()).toBe("not a socket");
  });

  test("stop() removes the socket file", async () => {
    const socket = await start();
    await handle!.stop();
    handle = undefined;
    await expect(stat(socket.path)).rejects.toMatchObject({ code: "ENOENT" });
  });
});
