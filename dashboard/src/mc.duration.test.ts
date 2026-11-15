import { describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { makeLogger } from "../../runner/shared/log";
import { observeCommand } from "./main";
import { makeMcRunner, type McCompletionRecord } from "./mc";

async function fixture(script: string, body: (bin: string) => Promise<void>): Promise<void> {
  const root = mkdtempSync(join(tmpdir(), "mc-duration-"));
  const bin = join(root, "mc-stub");
  writeFileSync(bin, `#!/bin/sh\n${script}\n`, { mode: 0o755 });
  try {
    await body(bin);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

describe("Command duration records", () => {
  test("The record identifies the host child and preserves arguments and input", async () => {
    await fixture(`
if [ "$#" -ne 3 ] || [ "$1" != task ] || [ "$2" != list ] || [ "$3" != private-argument ]; then exit 9; fi
input=$(cat)
if [ "$input" != private-input ]; then exit 8; fi
printf '{"hostPid":%s,"value":"private-output"}' "$$"
`, async (bin) => {
      const records: McCompletionRecord[] = [];
      const before = Date.now();
      const result = await makeMcRunner(bin, { onComplete: (record) => records.push(record) })
        .run(["task", "list", "private-argument"], "private-input");
      expect(result.code).toBe(0);
      expect(result.json?.value).toBe("private-output");
      expect(records).toHaveLength(1);
      const record = records[0]!;
      expect(record.hostPid).toBe(result.json?.hostPid);
      expect(record.hostPid).toBeGreaterThan(0);
      expect(record.code).toBe(0);
      expect(record.outcome).toBe("completed");
      expect(record.startedAtUnixMs).toBeGreaterThanOrEqual(before);
      expect(record.spawnedAtUnixMs!).toBeGreaterThanOrEqual(record.startedAtUnixMs);
      expect(record.exitObservedAtUnixMs!).toBeGreaterThanOrEqual(record.spawnedAtUnixMs!);
      expect(record.completedAtUnixMs).toBeGreaterThanOrEqual(record.exitObservedAtUnixMs!);
      expect(record.completedAtUnixMs).toBeLessThanOrEqual(Date.now());
      expect(record.elapsedMs).toBeGreaterThanOrEqual(0);
      expect(JSON.stringify(record)).not.toContain("private-");
    });
  });

  test("The duration includes output that closes after the host child exits", async () => {
    await fixture(`(sleep 0.05; printf '{"complete":true}') &\nexit 0`, async (bin) => {
      const records: McCompletionRecord[] = [];
      const result = await makeMcRunner(bin, { onComplete: (record) => records.push(record) }).run([]);
      expect(result).toEqual({ code: 0, json: { complete: true }, stderr: "" });
      expect(records).toHaveLength(1);
      expect(records[0]!.elapsedMs).toBeGreaterThanOrEqual(40);
      expect(records[0]!.outcome).toBe("completed");
    });
  });

  test("The observer preserves exit codes and error output", async () => {
    for (const code of [1, 2, 124, 125]) {
      await fixture(`printf '{"error":{"message":"private-output"}}'\nprintf 'private-error' >&2\nexit ${code}`, async (bin) => {
        const records: McCompletionRecord[] = [];
        const result = await makeMcRunner(bin, { onComplete: (record) => records.push(record) }).run(["system", "status"]);
        expect(result).toEqual({ code, json: { error: { message: "private-output" } }, stderr: "private-error" });
        expect(records).toHaveLength(1);
        expect(records[0]!.code).toBe(code);
        expect(records[0]!.outcome).toBe("completed");
        expect(JSON.stringify(records)).not.toContain("private-");
      });
    }
  });

  test("Invalid output still returns a null JSON result", async () => {
    await fixture(`printf 'not JSON'`, async (bin) => {
      const records: McCompletionRecord[] = [];
      const result = await makeMcRunner(bin, { onComplete: (record) => records.push(record) }).run([]);
      expect(result).toEqual({ code: 0, json: null, stderr: "" });
      expect(records).toHaveLength(1);
      expect(records[0]!.outcome).toBe("completed");
    });
  });

  test("A spawn failure records no child and preserves the command error", async () => {
    await fixture("exit 0", async (bin) => {
      const records: McCompletionRecord[] = [];
      let commandError: unknown;
      try {
        await makeMcRunner(`${bin}-absent`, { onComplete: (record) => {
          records.push(record);
          throw new Error("The observer failed.");
        } }).run([]);
      } catch (error) {
        commandError = error;
      }
      expect((commandError as NodeJS.ErrnoException).code).toBe("ENOENT");
      expect(records).toHaveLength(1);
      expect(records[0]!.hostPid).toBeNull();
      expect(records[0]!.spawnedAtUnixMs).toBeNull();
      expect(records[0]!.exitObservedAtUnixMs).toBeNull();
      expect(records[0]!.code).toBeNull();
      expect(records[0]!.outcome).toBe("error");
      expect(records[0]!.elapsedMs).toBeGreaterThanOrEqual(0);
    });
  });

  test("An observer failure preserves a successful command result", async () => {
    await fixture(`printf '{"complete":true}'`, async (bin) => {
      let calls = 0;
      const result = await makeMcRunner(bin, { onComplete: () => {
        calls++;
        throw new Error("The observer failed.");
      } }).run([]);
      expect(result).toEqual({ code: 0, json: { complete: true }, stderr: "" });
      expect(calls).toBe(1);
    });
  });

  test("The dashboard log reports success, command refusal, and spawn failure", () => {
    const lines: string[] = [];
    const logger = makeLogger({ comp: "dashboard", level: "info", sink: { write: (line) => { lines.push(line); } } });
    const record: McCompletionRecord = {
      startedAtUnixMs: 1000, spawnedAtUnixMs: 1001, exitObservedAtUnixMs: 1010,
      completedAtUnixMs: 1011, elapsedMs: 11, hostPid: 123, code: 0, outcome: "completed",
    };
    observeCommand(logger, record);
    observeCommand(logger, { ...record, code: 1 });
    observeCommand(logger, { ...record, hostPid: null, spawnedAtUnixMs: null, exitObservedAtUnixMs: null, code: null, outcome: "error" });
    const entries = lines.map((line) => JSON.parse(line));
    expect(entries.map((entry) => entry.level)).toEqual(["info", "warn", "warn"]);
    expect(entries.map((entry) => entry.event)).toEqual(Array(3).fill("dash.mc_completed"));
    expect(entries[0].hostPid).toBe(123);
    expect(entries[0].elapsedMs).toBe(11);
    expect(entries[1].code).toBe(1);
    expect(entries[2].hostPid).toBeNull();
    expect(entries[2].outcome).toBe("error");
  });
});
