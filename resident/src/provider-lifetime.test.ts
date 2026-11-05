import { expect, test } from "bun:test";
import { createProviderLifetimeCheck, providerLaunchIsCurrent } from "./provider-lifetime";
import type { CredentialProjectorHandle } from "./credential-projector";
import type { Exec } from "./types";

function fixture(count = 1) {
  const launches = Array.from({ length: count }, (_, index) => ({ runId: `run-${index}`, launchId: `launch-${index}` }));
  const extended: unknown[][] = [], revoked: unknown[][] = [];
  const handle = { launches: () => launches, projector: {
    extend: (...args: unknown[]) => extended.push(args), revoke: (...args: unknown[]) => revoked.push(args),
  } } as unknown as CredentialProjectorHandle;
  return { launches, extended, revoked, handle };
}
const result = (launches: unknown[], exitCode = 0) => ({ exitCode, stdout: JSON.stringify({ launches }), stderr: "" });

test("trusted control state extends only the exact current launch", async () => {
  const f = fixture(2);
  const expires = Date.now() + 60_000;
  const runMc: Exec = async (argv, stdin) => {
    expect(argv).toEqual(["connection", "proxy-lifetimes"]);
    expect(JSON.parse(new TextDecoder().decode(stdin))).toEqual({ launches: [
      { run_id: "run-0", launch_id: "launch-0" }, { run_id: "run-1", launch_id: "launch-1" },
    ] });
    return result([{ run_id: "run-0", launch_id: "launch-0", allowed: true, expires_at: new Date(expires).toISOString() },
      { run_id: "run-1", launch_id: "old-launch", allowed: true, expires_at: new Date(expires).toISOString() }]);
  };
  await createProviderLifetimeCheck(f.handle, runMc)();
  expect(f.extended).toEqual([["run-0", "launch-0", expires]]);
  expect(f.revoked).toEqual([["run-1", "launch-1"]]);
});

test("absent, duplicate, malformed, refused, or expired evidence revokes authority", async () => {
  const valid = { run_id: "run-0", launch_id: "launch-0", allowed: true, expires_at: new Date(Date.now() + 60_000).toISOString() };
  for (const rows of [[], [valid, valid], [null], [{ ...valid, allowed: false }], [{ ...valid, allowed: "true" }], [{ ...valid, expires_at: "invalid" }], [{ ...valid, expires_at: "2000-01-01" }]]) {
    const f = fixture();
    await createProviderLifetimeCheck(f.handle, async () => result(rows))();
    expect(f.extended).toEqual([]);
    expect(f.revoked).toEqual([["run-0", "launch-0"]]);
  }
  for (const runMc of [async () => result([valid], 2), async () => { throw new Error("unavailable"); }, async () => ({ exitCode: 0, stdout: "invalid", stderr: "" })]) {
    const f = fixture();
    await createProviderLifetimeCheck(f.handle, runMc)();
    expect(f.revoked).toEqual([["run-0", "launch-0"]]);
  }
});

test("one active check serves concurrent ticks and bounds each batch", async () => {
  const f = fixture(201);
  let resolve!: () => void;
  const wait = new Promise<void>(yes => { resolve = yes; });
  const sizes: number[] = [];
  const check = createProviderLifetimeCheck(f.handle, async (_args, stdin) => {
    const batch = JSON.parse(new TextDecoder().decode(stdin)).launches;
    sizes.push(batch.length);
    await wait;
    return result([]);
  });
  const first = check();
  const second = check();
  expect(sizes).toEqual([100]);
  resolve();
  await Promise.all([first, second]);
  expect(sizes).toEqual([100, 100, 1]);
  expect(f.revoked).toHaveLength(201);
});


test("each provider request checks its exact current launch and propagates cancellation", async () => {
  const signal = new AbortController().signal;
  const live = { run_id: "run", launch_id: "launch", allowed: true, expires_at: new Date(Date.now() + 60_000).toISOString() };
  const check = async (rows: unknown[]) => providerLaunchIsCurrent(async (args, input, options) => {
    expect(args).toEqual(["connection", "proxy-lifetimes"]);
    expect(JSON.parse(new TextDecoder().decode(input))).toEqual({ launches: [{ run_id: "run", launch_id: "launch" }] });
    expect(options?.signal).toBe(signal);
    return result(rows);
  }, "run", "launch", signal);
  expect(await check([live])).toBe(true);
  for (const row of [null, { ...live, launch_id: "old" }, { ...live, allowed: false }, { ...live, expires_at: "2000-01-01" }]) {
    expect(await check([row])).toBe(false);
  }
  await expect(check([live, live])).rejects.toThrow("invalid");
  await expect(providerLaunchIsCurrent(async () => result([live], 1), "run", "launch", signal)).rejects.toThrow("unavailable");
  const cancelled = new AbortController(); cancelled.abort();
  let called = false;
  await expect(providerLaunchIsCurrent(async () => { called = true; return result([live]); }, "run", "launch", cancelled.signal)).rejects.toThrow();
  expect(called).toBe(false);
});
