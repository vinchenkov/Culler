import { expect, test } from "bun:test";
import { createRequests } from "./requests.js";

function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

test("Concurrent consumers share a read and reuse it until its freshness expires", async () => {
  let clock = 100;
  let calls = 0;
  const response = deferred();
  const client = createRequests({ send: () => { calls++; return response.promise; }, now: () => clock });
  const first = client.request("/tasks");
  const second = client.request("/tasks");
  expect(calls).toBe(1);
  response.resolve({ tasks: [1] });
  expect(await first).toEqual(await second);
  await client.request("/tasks");
  expect(calls).toBe(1);
  clock += 10000;
  await client.request("/tasks");
  expect(calls).toBe(2);
});

test("A write rejects old freshness and every consumer receives the new result", async () => {
  const old = deferred();
  let reads = 0;
  const client = createRequests({ send: (_path, options) => options.method === "POST" ? Promise.resolve({})
    : ++reads === 1 ? old.promise : Promise.resolve({ version: 2 }) });
  const first = client.request("/tasks");
  await client.request("/tasks", { method: "POST" });
  const second = await client.request("/tasks");
  old.resolve({ version: 1 });
  expect(await first).toEqual(second);
  expect(await client.request("/tasks")).toEqual({ version: 2 });
  expect(reads).toBe(2);
});

test("Cancellation releases one consumer and preserves the shared read", async () => {
  const response = deferred();
  const client = createRequests({ send: () => response.promise });
  const controller = new AbortController();
  const first = client.request("/tasks", { signal: controller.signal });
  const second = client.request("/tasks");
  controller.abort();
  await expect(first).rejects.toThrow();
  response.resolve({ tasks: [1] });
  expect(await second).toEqual({ tasks: [1] });
});

test("A failed read permits a retry and an authorization failure clears protected data", async () => {
  let denied = 0;
  let calls = 0;
  const client = createRequests({ send: async () => {
    calls++;
    if (calls === 1) throw new Error("Unavailable");
    if (calls === 3) throw Object.assign(new Error("Denied"), { status: 401 });
    return { tasks: [1] };
  }, denied: () => denied++ });
  await expect(client.request("/tasks")).rejects.toThrow("Unavailable");
  expect(await client.request("/tasks")).toEqual({ tasks: [1] });
  await expect(client.request("/private")).rejects.toThrow("Denied");
  await expect(client.request("/tasks")).rejects.toThrow("token");
  expect(denied).toBeGreaterThan(0);
  expect(calls).toBe(3);
});

test("Prefetch has one active read, yields to page reads, and stops while hidden", async () => {
  const response = deferred();
  const paths: string[] = [];
  let visible = true;
  const client = createRequests({ send: path => { paths.push(path); return response.promise; } });
  const speculative = client.prefetch(["/one", "/two"], () => visible);
  void client.prefetch(["/one", "/three"]);
  expect(paths).toEqual(["/one"]);
  const foreground = client.request("/one");
  visible = false;
  response.resolve({});
  await speculative;
  await foreground;
  expect(paths).toEqual(["/one"]);
  await client.prefetch(["/two"], () => visible);
  expect(paths).toEqual(["/one"]);
});

test("A record conflict invalidates old details before the conflict form reads them", async () => {
  let version = 1;
  const client = createRequests({ send: async (_path, options) => {
    if (options.method === "POST") { version = 2; throw Object.assign(new Error("Conflict"), { status: 409 }); }
    return { version };
  } });
  expect(await client.request("/api/tasks/1")).toEqual({ version: 1 });
  await expect(client.request("/api/board/write", { method: "POST" })).rejects.toThrow("Conflict");
  expect(await client.request("/api/tasks/1")).toEqual({ version: 2 });
});

test("A read action preserves unrelated cached data", async () => {
  let reads = 0;
  const client = createRequests({ send: async (_path, options) => { if (!options.method) reads++; return {}; } });
  await client.request("/api/tasks");
  await client.request("/api/agent-profiles/operation-status", { method: "POST" });
  await client.request("/api/tasks");
  expect(reads).toBe(1);
});

test("A new consumer can retry after the last prior consumer cancels", async () => {
  const old = deferred();
  let reads = 0;
  const client = createRequests({ send: () => ++reads === 1 ? old.promise : Promise.resolve({ version: 2 }) });
  const controller = new AbortController();
  const first = client.request("/api/agent-profiles", { signal: controller.signal });
  controller.abort();
  await expect(first).rejects.toThrow();
  expect(await client.request("/api/agent-profiles")).toEqual({ version: 2 });
  old.resolve({ version: 1 });
  await Promise.resolve();
  expect(await client.request("/api/agent-profiles")).toEqual({ version: 2 });
});
