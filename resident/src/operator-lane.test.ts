import { expect, test } from "bun:test";
import type { Logger } from "../../runner/shared/log";
import type { Exec } from "./types";
import { createOperatorLane } from "./operator-lane";
import { createInstallMaintenance } from "./install-maintenance";
import { mkdtemp, mkdir, writeFile, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const logger = { warn() {} } as unknown as Logger;
const ok = (value: unknown) => ({ exitCode: 0, stdout: JSON.stringify(value), stderr: "" });

async function until(check: () => boolean) {
  for (let i = 0; i < 100 && !check(); i++) await new Promise((resolve) => setTimeout(resolve, 2));
  expect(check()).toBe(true);
}

test("The operator lane recovers before execution and keeps one execution active", async () => {
  const calls: string[][] = [];
  let finish!: () => void;
  const gate = new Promise<void>((resolve) => { finish = resolve; });
  let completed = false;
  const runMc: Exec = async (args) => {
    calls.push([...args]);
    if (args[0] === "__operator-due") return ok({ operations: completed ? [] : [{ id: "request-1" }] });
    if (args[0] === "__operator-execute") {
      await gate;
      completed = true;
    }
    return ok({});
  };
  const lane = createOperatorLane(runMc, logger, 2);
  await lane.start();
  await until(() => calls.some((call) => call[0] === "__operator-execute"));
  expect(calls[0]).toEqual(["__operator-recover"]);
  await new Promise((resolve) => setTimeout(resolve, 15));
  expect(calls.filter((call) => call[0] === "__operator-execute")).toHaveLength(1);
  const stopped = lane.stop();
  finish();
  await stopped;
});

test("A failed executor retains an uncertain receipt", async () => {
  const calls: Array<{ args: string[]; input?: string }> = [];
  let supplied = false;
  const runMc: Exec = async (args, input) => {
    calls.push({ args: [...args], input: input === undefined ? undefined : new TextDecoder().decode(input) });
    if (args[0] === "__operator-due") {
      const rows = supplied ? [] : [{ id: "request-2" }];
      supplied = true;
      return ok({ operations: rows });
    }
    if (args[0] === "__operator-execute") return { exitCode: 2, stdout: "", stderr: "The process stopped." };
    return ok({});
  };
  const lane = createOperatorLane(runMc, logger, 2);
  await lane.start();
  await until(() => calls.some((call) => call.args[0] === "__operator-recover" && call.input !== undefined));
  await lane.stop();
  expect(calls.find((call) => call.input !== undefined)?.input).toBe('{"id":"request-2"}');
  expect(calls.filter((call) => call.args[0] === "__operator-execute")).toHaveLength(1);
});

test("An invalid request identifier reaches no host executor", async () => {
  const calls: string[][] = [];
  const runMc: Exec = async (args) => {
    calls.push([...args]);
    return ok(args[0] === "__operator-due" ? { operations: [{ id: "../command" }] } : {});
  };
  const lane = createOperatorLane(runMc, logger, 2);
  await lane.start();
  await until(() => calls.some((call) => call[0] === "__operator-due"));
  await lane.stop();
  expect(calls.some((call) => call[0] === "__operator-execute")).toBe(false);
});

test("A thrown executor error retains an uncertain receipt", async () => {
  let supplied = false;
  let recovered = false;
  const runMc: Exec = async (args, input) => {
    if (args[0] === "__operator-due") {
      const operations = supplied ? [] : [{ id: "interrupted" }];
      supplied = true;
      return ok({ operations });
    }
    if (args[0] === "__operator-execute") throw new Error("The result stream closed.");
    if (args[0] === "__operator-recover" && input !== undefined) recovered = true;
    return ok({});
  };
  const lane = createOperatorLane(runMc, logger, 2);
  await lane.start();
  await until(() => recovered);
  await lane.stop();
});

test("Install maintenance waits for an operator effect and prevents the next effect", async () => {
  const home = await mkdtemp(join(tmpdir(), "culler-operator-maintenance-"));
  await mkdir(join(home, "state"));
  let paused = false;
  let finish!: () => void;
  const gate = new Promise<void>((resolve) => { finish = resolve; });
  const executed: string[] = [];
  const runMc: Exec = async (args) => {
    if (args[0] === "__operator-due") return ok({ operations: [{ id: "first" }, { id: "second" }] });
    if (args[0] === "__operator-execute") { executed.push(args[1]!); await gate; }
    return ok({});
  };
  const lane = createOperatorLane(runMc, logger, 2, async () => paused);
  const maintenance = createInstallMaintenance(home, "development");
  try {
    await lane.start();
    await until(() => lane.activeCount() === 1);
    paused = true;
    await writeFile(join(home, "state", "install-maintenance.json"), JSON.stringify({ id: "operator-maintenance-request" }), { mode: 0o600 });
    await maintenance(lane.activeCount());
    expect(JSON.parse(await readFile(join(home, "state", "install-maintenance-ack.json"), "utf8")).ready).toBe(false);
    finish();
    await until(() => lane.activeCount() === 0);
    await maintenance(lane.activeCount());
    expect(JSON.parse(await readFile(join(home, "state", "install-maintenance-ack.json"), "utf8")).ready).toBe(true);
    expect(executed).toEqual(["first"]);
  } finally { finish(); await lane.stop(); await rm(home, { recursive: true, force: true }); }
});

test("A listing that never returns does not stop the lane", async () => {
  const warnings: string[] = [];
  const log = { warn(event: string) { warnings.push(event); } } as unknown as Logger;
  let listings = 0;
  let aborted = false;
  const executed: string[] = [];
  const runMc: Exec = (args, _input, options) => {
    if (args[0] === "__operator-due" && ++listings === 1) {
      options?.signal?.addEventListener("abort", () => { aborted = true; });
      return new Promise(() => {});
    }
    if (args[0] === "__operator-due") return Promise.resolve(ok({ operations: executed.length === 0 ? [{ id: "after-the-hang" }] : [] }));
    if (args[0] === "__operator-execute") executed.push(args[1]!);
    return Promise.resolve(ok({}));
  };
  const lane = createOperatorLane(runMc, log, 2, undefined, { commandTimeoutMs: 20 });
  await lane.start();
  await until(() => executed.length === 1);
  await lane.stop();
  expect(executed).toEqual(["after-the-hang"]);
  expect(aborted).toBe(true);
  expect(warnings).toContain("operator.command_timed_out");
});

test("The lane records its health while listings succeed or a request executes", async () => {
  const records: Date[] = [];
  let failing = false;
  let executing = false;
  let finish!: () => void;
  const gate = new Promise<void>((resolve) => { finish = resolve; });
  const runMc: Exec = async (args) => {
    if (args[0] === "__operator-due") {
      if (failing) return { exitCode: 1, stdout: "", stderr: "" };
      return ok({ operations: executing ? [{ id: "long-request" }] : [] });
    }
    if (args[0] === "__operator-execute") { await gate; executing = false; }
    return ok({});
  };
  const lane = createOperatorLane(runMc, logger, 2, undefined, {
    publish: async (now) => { records.push(now); }, publishIntervalMs: 0,
  });
  await lane.start();
  await until(() => records.length >= 3);

  // A request in execution keeps the record fresh.
  executing = true;
  await until(() => lane.activeCount() === 1);
  const duringExecution = records.length;
  await until(() => records.length >= duringExecution + 3);
  finish();
  await until(() => lane.activeCount() === 0);

  // Failed listings leave the record to become old.
  failing = true;
  await new Promise((resolve) => setTimeout(resolve, 10));
  const afterFailure = records.length;
  await new Promise((resolve) => setTimeout(resolve, 30));
  expect(records.length).toBe(afterFailure);
  await lane.stop();
});
