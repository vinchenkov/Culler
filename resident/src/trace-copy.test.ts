// trace-copy.test.ts — the copy of one trace file in real temporary folders:
// complete lines only, byte-identical prefixes, restart continuation, and a
// complete new copy after a shorter, replaced, or different source.

import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { appendFile, chmod, mkdir, mkdtemp, open, readFile, readdir, rename, rm, stat, symlink, truncate, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { checkInside, copyTrace, openFolderInside, type CopyState } from "./trace-copy";

let root: string;
let source: string;
let dest: string;

beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), "trace-copy-"));
  source = join(root, "session", "native.jsonl");
  dest = join(root, "copies", "culler", "11111111-1111-4111-8111-111111111111.jsonl");
  await Bun.write(join(root, "session", ".keep"), "");
});

afterEach(async () => {
  await rm(root, { recursive: true, force: true });
});

function line(n: number): string {
  return JSON.stringify({ type: "user", uuid: `u-${n}`, message: { role: "user", content: `line ${n}` } }) + "\n";
}

function state(): CopyState {
  return { root: join(root, "session"), source, dest };
}

const read = (path: string) => readFile(path, "utf8");

describe("copyTrace", () => {
  test("masks a deliberate Culler token across read boundaries before export", async () => {
    const token = "culler_proxy_" + "c".repeat(43);
    const body = JSON.stringify({ type: "user", uuid: "same-native-id", message: `tool result: ${token}` }) + "\n";
    await writeFile(source, body);
    expect(await read(source)).toContain(token);
    const copy = state();
    await copyTrace(copy, 1, { chunkBytes: 32 });
    const result = await read(dest);
    expect(result).not.toContain(token);
    expect(Buffer.byteLength(result)).toBe(Buffer.byteLength(body));
    expect(JSON.parse(result)).toEqual({ type: "user", uuid: "same-native-id", message: `tool result: ${"*".repeat(token.length)}` });
    expect(copy.position).toBe(Buffer.byteLength(body));
  });

  test("a restart verifies a masked token across the tail boundary and retains native context", async () => {
    const token = "culler_proxy_" + "d".repeat(43);
    const body = JSON.stringify({ type: "user", body: token + "x".repeat(4080) }) + "\n";
    await writeFile(source, body);
    await copyTrace(state(), 1);
    const inode = (await stat(dest)).ino;
    await appendFile(source, line(2));
    const restarted = state();
    const result = await copyTrace(restarted, 2);
    expect(result.copied).toBe(Buffer.byteLength(line(2)));
    expect((await stat(dest)).ino).toBe(inode);
    const exported = await read(dest);
    expect(exported).not.toContain(token);
    expect(exported).toEndWith(line(2));
    expect(exported.split("\n").filter(Boolean).map(value => JSON.parse(value).type)).toEqual(["user", "user"]);
  });

  test("copies complete lines only and adds the rest of a line when it ends", async () => {
    await writeFile(source, line(1) + line(2) + '{"type":"assis');
    const copy = state();
    const first = await copyTrace(copy, 1_000);
    expect(first).toEqual({ copied: (line(1) + line(2)).length, caughtUp: true, absent: false, replaced: false });
    expect(await read(dest)).toBe(line(1) + line(2));
    expect(copy.position).toBe((line(1) + line(2)).length);
    expect(copy.changedAt).toBe(1_000);

    await appendFile(source, 'tant"}\n');
    await copyTrace(copy, 2_000);
    expect(await read(dest)).toBe(line(1) + line(2) + '{"type":"assistant"}\n');
    expect(copy.changedAt).toBe(2_000);
  });

  test("a pass without new lines leaves the copy and its change time", async () => {
    await writeFile(source, line(1));
    const copy = state();
    await copyTrace(copy, 1_000);
    const result = await copyTrace(copy, 5_000);
    expect(result.copied).toBe(0);
    expect(copy.changedAt).toBe(1_000);
  });

  test("gives the copy mode 0600 and each new folder mode 0700", async () => {
    await writeFile(source, line(1));
    await copyTrace(state(), 1);
    expect((await stat(dest)).mode & 0o777).toBe(0o600);
    expect((await stat(join(root, "copies", "culler"))).mode & 0o777).toBe(0o700);
    expect((await stat(join(root, "copies"))).mode & 0o777).toBe(0o700);
  });

  test("a new copy state continues after the copy with no duplicate and no gap", async () => {
    await writeFile(source, line(1) + line(2) + line(3).slice(0, 10));
    await copyTrace(state(), 1);
    await appendFile(source, line(3).slice(10) + line(4));
    const restarted = state();
    const result = await copyTrace(restarted, 2);
    expect(result.copied).toBe((line(3) + line(4)).length);
    expect(await read(dest)).toBe(line(1) + line(2) + line(3) + line(4));
  });

  test("a restart keeps the copy file when its last bytes equal the source", async () => {
    await writeFile(source, line(1) + line(2));
    await copyTrace(state(), 1);
    const inode = (await stat(dest)).ino;
    await appendFile(source, line(3));
    await copyTrace(state(), 2);
    expect((await stat(dest)).ino).toBe(inode);
    expect(await read(dest)).toBe(line(1) + line(2) + line(3));
  });

  test("a copy whose last bytes differ from the source is made again", async () => {
    await writeFile(source, line(1) + line(2));
    await copyTrace(state(), 1);
    await writeFile(dest, line(1) + line(9));
    await appendFile(source, line(3));
    const restarted = state();
    await copyTrace(restarted, 2);
    expect(await read(dest)).toBe(line(1) + line(2) + line(3));
    expect(restarted.recopy).toBeUndefined();
    expect(await readdir(join(root, "copies", "culler"))).toEqual(["11111111-1111-4111-8111-111111111111.jsonl"]);
  });

  test("a copy that ends inside a line is made again", async () => {
    await writeFile(source, line(1) + line(2));
    await copyTrace(state(), 1);
    await writeFile(dest, line(1) + line(2).slice(0, 5));
    await copyTrace(state(), 2);
    expect(await read(dest)).toBe(line(1) + line(2));
  });

  test("a shorter source makes a complete new copy by rename", async () => {
    await writeFile(source, line(1) + line(2) + line(3));
    const copy = state();
    await copyTrace(copy, 1);
    const inode = (await stat(dest)).ino;
    await truncate(source, line(1).length);
    await appendFile(source, line(5).slice(0, 4));
    await copyTrace(copy, 2);
    expect(await read(dest)).toBe(line(1));
    expect((await stat(dest)).ino).not.toBe(inode);
    expect((await stat(dest)).mode & 0o777).toBe(0o600);
    expect(copy.changedAt).toBe(2);
  });

  test("a replaced source with other bytes makes a complete new copy", async () => {
    await writeFile(source, line(1) + line(2));
    const copy = state();
    await copyTrace(copy, 1);
    const next = join(root, "session", "next.jsonl");
    await writeFile(next, line(7) + line(8) + line(9));
    await rename(next, source);
    await copyTrace(copy, 2);
    expect(await read(dest)).toBe(line(7) + line(8) + line(9));
  });

  test("a replaced source that keeps the same prefix continues the copy", async () => {
    await writeFile(source, line(1) + line(2));
    const copy = state();
    await copyTrace(copy, 1);
    const inode = (await stat(dest)).ino;
    const next = join(root, "session", "next.jsonl");
    await writeFile(next, line(1) + line(2) + line(3));
    await rename(next, source);
    await copyTrace(copy, 2);
    expect(await read(dest)).toBe(line(1) + line(2) + line(3));
    expect((await stat(dest)).ino).toBe(inode);
  });

  test("one pass reads at most its byte limit and each pass ends at a complete line", async () => {
    const lines = Array.from({ length: 12 }, (_, index) => `${String(index).padStart(4, "0")}\n`).join("");
    await writeFile(source, lines);
    const copy = state();
    const limits = { chunkBytes: 8, passBytes: 16 };
    const sizes: number[] = [];
    for (;;) {
      const result = await copyTrace(copy, 1, limits);
      const text = await read(dest);
      expect(text.endsWith("\n")).toBe(true);
      expect(lines.startsWith(text)).toBe(true);
      sizes.push(text.length);
      if (result.caughtUp) break;
      expect(result.copied).toBeLessThanOrEqual(16 + 5);
    }
    expect(sizes.length).toBeGreaterThan(2);
    expect(await read(dest)).toBe(lines);
  });

  test("a line longer than one read is copied whole", async () => {
    const long = JSON.stringify({ type: "tool_result", content: "x".repeat(100) }) + "\n";
    await writeFile(source, long + line(2));
    await copyTrace(state(), 1, { chunkBytes: 8 });
    expect(await read(dest)).toBe(long + line(2));
  });

  test("a line longer than the line limit fails the copy", async () => {
    await writeFile(source, `${"x".repeat(40)}\n`);
    await expect(copyTrace(state(), 1, { chunkBytes: 4, maxLineBytes: 16 }))
      .rejects.toThrow("a trace line is longer than 16 bytes");
  });

  test("a copy that disappears gets a complete new copy", async () => {
    await writeFile(source, line(1) + line(2));
    const copy = state();
    await copyTrace(copy, 1);
    await rm(join(root, "copies"), { recursive: true });
    await appendFile(source, line(3));
    const result = await copyTrace(copy, 2);
    expect(await read(dest)).toBe(line(1) + line(2) + line(3));
    expect(result.replaced).toBe(true);
    expect(copy.position).toBe((line(1) + line(2) + line(3)).length);
  });

  test("a copy with another size than the copy position gets a complete new copy", async () => {
    await writeFile(source, line(1) + line(2));
    const copy = state();
    await copyTrace(copy, 1);
    await truncate(dest, line(1).length);
    await appendFile(source, line(3));
    await copyTrace(copy, 2);
    expect(await read(dest)).toBe(line(1) + line(2) + line(3));
    await appendFile(dest, line(9));
    await copyTrace(copy, 3);
    expect(await read(dest)).toBe(line(1) + line(2) + line(3));
  });

  test("a pass opens no unchanged source, also with an unterminated last line", async () => {
    await writeFile(source, line(1) + line(2).slice(0, 9));
    const copy = state();
    await copyTrace(copy, 1);
    await chmod(source, 0o000);
    try {
      expect(await copyTrace(copy, 2)).toEqual({ copied: 0, caughtUp: true, absent: false, replaced: false });
    } finally {
      await chmod(source, 0o600);
    }
    await appendFile(source, line(2).slice(9));
    await copyTrace(copy, 3);
    expect(await read(dest)).toBe(line(1) + line(2));
  });

  test("the reads of an unterminated line count against the pass limit, and each pass reads only new bytes", async () => {
    const tail = "x".repeat(100);
    await writeFile(source, line(1) + tail);
    const copy = state();
    const limits = { chunkBytes: 8, passBytes: 16 };
    const first = await copyTrace(copy, 1, limits);
    expect(first.caughtUp).toBe(false);
    let passes = 1;
    while (!(await copyTrace(copy, 1, limits)).caughtUp) passes += 1;
    const size = (line(1) + tail).length;
    expect(passes).toBeLessThanOrEqual(Math.ceil(size / 16) + 1);
    expect(copy.scan).toEqual({ from: line(1).length, to: size });

    await appendFile(source, "y".repeat(20));
    expect((await copyTrace(copy, 2, limits)).caughtUp).toBe(false);
    expect((await copyTrace(copy, 2, limits)).caughtUp).toBe(true);
    expect(copy.scan).toEqual({ from: line(1).length, to: size + 20 });

    await appendFile(source, "\n");
    await copyTrace(copy, 3, { chunkBytes: 8 });
    expect(await read(dest)).toBe(`${line(1)}${tail}${"y".repeat(20)}\n`);
    expect(copy.scan).toBeUndefined();
  });

  test("an absent source copies nothing", async () => {
    const result = await copyTrace(state(), 1);
    expect(result).toEqual({ copied: 0, caughtUp: true, absent: true, replaced: false });
    expect(await Bun.file(dest).exists()).toBe(false);
  });

  test("a symbolic link at the trace path fails the copy", async () => {
    const outside = join(root, "outside.jsonl");
    await writeFile(outside, line(1));
    await symlink(outside, source);
    await expect(copyTrace(state(), 1)).rejects.toThrow("the trace file is a symbolic link");
    expect(await Bun.file(dest).exists()).toBe(false);
  });

  test("a folder at the copy path fails the copy", async () => {
    await writeFile(source, line(1));
    await Bun.write(join(dest, "blocker"), "");
    await expect(copyTrace(state(), 1)).rejects.toThrow("the trace copy is not a regular file");
  });
});

// An agent writes its session folder while the resident reads it. These
// tests swap a folder for a symbolic link to a folder outside the session
// folder, at the moments of a pass that a real agent can reach.
describe("a folder swapped for a symbolic link", () => {
  let session: string;
  let outside: string;
  let child: string;

  beforeEach(async () => {
    session = join(root, "session");
    outside = join(root, "outside");
    child = join(session, "subagents", "agent-a1.jsonl");
    await mkdir(join(session, "subagents"), { recursive: true });
    await mkdir(outside);
    await writeFile(join(outside, "agent-a1.jsonl"), line(1) + '{"secret":"outside"}\n');
  });

  async function swap(): Promise<void> {
    await rename(join(session, "subagents"), join(root, "subagents-real"));
    await symlink(outside, join(session, "subagents"));
  }

  async function restore(): Promise<void> {
    await rm(join(session, "subagents"));
    await rename(join(root, "subagents-real"), join(session, "subagents"));
  }

  test("a source below the swapped folder fails the copy and leaves the copy", async () => {
    await writeFile(child, line(1));
    const copy: CopyState = { root: session, source: child, dest };
    await copyTrace(copy, 1);
    await swap();
    await expect(copyTrace(copy, 2)).rejects.toThrow("the trace file resolves outside its session folder");
    expect(await read(dest)).toBe(line(1));
    await restore();
    await appendFile(child, line(2));
    await copyTrace(copy, 3);
    expect(await read(dest)).toBe(line(1) + line(2));
  });

  test("a file opened through the swapped folder fails after the agent restores the folder", async () => {
    await writeFile(child, line(1));
    await swap();
    const handle = await open(child, "r");
    try {
      await restore();
      await expect(checkInside(handle, session, child, "trace file"))
        .rejects.toThrow("the trace file changed while the resident opened it");
    } finally {
      await handle.close();
    }
  });

  test("the walk opens no swapped folder and no folder below it", async () => {
    await mkdir(join(session, "subagents", "workflows"));
    const handle = await openFolderInside(session, join(session, "subagents"), "trace folder");
    await handle.close();
    await swap();
    await mkdir(join(outside, "workflows"));
    await expect(openFolderInside(session, join(session, "subagents"), "trace folder"))
      .rejects.toThrow("the trace folder is a symbolic link");
    await expect(openFolderInside(session, join(session, "subagents", "workflows"), "trace folder"))
      .rejects.toThrow("the trace folder resolves outside its session folder");
  });

  test("a folder opened through the swapped folder fails after the agent restores the folder", async () => {
    await mkdir(join(session, "subagents", "workflows"));
    await mkdir(join(outside, "workflows"));
    await swap();
    const handle = await open(join(session, "subagents", "workflows"), "r");
    try {
      await restore();
      await expect(checkInside(handle, session, join(session, "subagents", "workflows"), "trace folder"))
        .rejects.toThrow("the trace folder changed while the resident opened it");
    } finally {
      await handle.close();
    }
  });

  test("the session folder passes as a folder and no file passes as the session folder", async () => {
    const handle = await openFolderInside(session, session, "trace folder");
    await handle.close();
    await writeFile(child, line(1));
    const file = await open(child, "r");
    try {
      await expect(checkInside(file, child, child, "trace file")).rejects.toThrow("resolves outside its session folder");
    } finally {
      await file.close();
    }
  });
});
