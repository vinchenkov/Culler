import { createHash } from "node:crypto";
import { resolve } from "node:path";
import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";
import type { McRunner } from "../dashboard/src/mc";

interface Request {
  id: string;
  argv: string[];
}

interface Batch {
  id: string;
  requests: Request[];
  concurrency: number;
}

function argument(name: string, fallback?: string): string {
  const index = process.argv.indexOf(name);
  if (index < 0 && fallback !== undefined) return fallback;
  const value = process.argv[index + 1];
  if (index < 0 || value === undefined || value.startsWith("--")) {
    throw new Error(`The ${name} argument needs a value.`);
  }
  return value;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function request(value: unknown): Request {
  if (!isRecord(value) || typeof value.id !== "string" || value.id.length === 0 || value.id.length > 200 ||
      !Array.isArray(value.argv) || !value.argv.every((part) => typeof part === "string")) {
    throw new Error("Each request needs an identifier and a command argument list.");
  }
  const argv = value.argv as string[];
  const listCommand = argv.length === 2 && ["project", "task"].includes(argv[0]!) && argv[1] === "list";
  const projectTasks = argv.length === 4 && argv[0] === "task" && argv[1] === "list" &&
    argv[2] === "--project" && /^[A-Za-z0-9][A-Za-z0-9._-]{0,159}$/.test(argv[3]!);
  if (!listCommand && !projectTasks) {
    throw new Error("The worker accepts only project list and task list reads.");
  }
  return { id: value.id, argv };
}

function batch(value: unknown, maximum: number): Batch {
  if (!isRecord(value) || typeof value.id !== "string" || value.id.length === 0 || value.id.length > 200 ||
      !Array.isArray(value.requests) || value.requests.length === 0 || value.requests.length > 10_000) {
    throw new Error("Each batch needs an identifier and one to 10000 requests.");
  }
  const concurrency = value.concurrency ?? 1;
  if (typeof concurrency !== "number" || !Number.isInteger(concurrency) || concurrency < 1 || concurrency > maximum) {
    throw new Error("The batch concurrency exceeds the worker limit or is invalid.");
  }
  const requests = value.requests.map(request);
  if (new Set(requests.map((item) => item.id)).size !== requests.length) {
    throw new Error("Each request in a batch needs a different identifier.");
  }
  return { id: value.id, requests, concurrency };
}

function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (isRecord(value)) {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonical(value[key])}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

function digest(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}

let output = Promise.resolve();
function emit(value: unknown): Promise<void> {
  const line = `${JSON.stringify(value)}\n`;
  output = output.then(() => new Promise<void>((done, fail) => {
    process.stdout.write(line, (error) => error === null || error === undefined ? done() : fail(error));
  }));
  return output;
}

async function measure(runner: McRunner, item: Request, batchId: string | null): Promise<void> {
  const workerMemoryBefore = process.memoryUsage();
  const startedUnixMs = Date.now();
  const startedAt = new Date(startedUnixMs).toISOString();
  const started = performance.now();
  try {
    const result = await runner.run(item.argv);
    const elapsedMs = performance.now() - started;
    const completedUnixMs = Date.now();
    const workerMemoryAfter = process.memoryUsage();
    const validCode = result.code === 0;
    const validJson = isRecord(result.json);
    await emit({
      type: "result", id: item.id, batchId, argv: item.argv, startedAt, elapsedMs,
      completedAt: new Date(completedUnixMs).toISOString(), wallElapsedMs: completedUnixMs - startedUnixMs,
      code: result.code, validCode, validJson, valid: validCode && validJson,
      outputDigest: validJson ? digest(canonical(result.json)) : null,
      stderrDigest: digest(result.stderr), stderrBytes: Buffer.byteLength(result.stderr),
      workerMemoryBefore, workerMemoryAfter,
    });
  } catch {
    const elapsedMs = performance.now() - started;
    const completedUnixMs = Date.now();
    await emit({
      type: "result", id: item.id, batchId, argv: item.argv, startedAt, elapsedMs,
      completedAt: new Date(completedUnixMs).toISOString(), wallElapsedMs: completedUnixMs - startedUnixMs,
      code: null, validCode: false, validJson: false, valid: false,
      outputDigest: null, error: "The runner rejected the request.",
      workerMemoryBefore, workerMemoryAfter: process.memoryUsage(),
    });
  }
}

async function main(): Promise<void> {
  const mcBin = resolve(argument("--mc-bin"));
  const runnerModule = resolve(argument("--runner-module", resolve(import.meta.dir, "../dashboard/src/mc.ts")));
  const maximum = Number(argument("--max-concurrency", "8"));
  if (!Number.isInteger(maximum) || maximum < 1 || maximum > 64) {
    throw new Error("The maximum concurrency must be an integer from one to 64.");
  }
  const module = await import(pathToFileURL(runnerModule).href);
  if (typeof module.makeMcRunner !== "function") throw new Error("The runner module must export makeMcRunner.");
  const runner: McRunner = module.makeMcRunner(mcBin);
  await emit({ type: "ready", pid: process.pid, bunVersion: Bun.version, mcBin, runnerModule, maxConcurrency: maximum,
    boundary: "before runner.run through process exit, both output streams, and JSON parse" });
  for await (const line of createInterface({ input: process.stdin, crlfDelay: Infinity })) {
    if (line.trim() === "") continue;
    let value: unknown;
    try {
      value = JSON.parse(line);
      if (isRecord(value) && "requests" in value) {
        const group = batch(value, maximum);
        let next = 0;
        await Promise.all(Array.from({ length: Math.min(group.concurrency, group.requests.length) }, async () => {
          while (next < group.requests.length) {
            const item = group.requests[next++]!;
            await measure(runner, item, group.id);
          }
        }));
        await emit({ type: "batch-complete", id: group.id, count: group.requests.length });
      } else {
        await measure(runner, request(value), null);
      }
    } catch (error) {
      await emit({ type: "request-error", id: isRecord(value) && typeof value.id === "string" ? value.id : null,
        error: error instanceof SyntaxError ? "The request must contain valid JSON." :
          error instanceof Error ? error.message : "The request is invalid." });
    }
  }
  await output;
}

await main();
