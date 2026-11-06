// The resident reaches the spine only by executing `mc`, so a verb the
// resident names and the binary does not dispatch is a defect no unit test
// sees: every resident-side test mocks the exec seam, and mc's own tests never
// read the resident's argv. The gap is only visible at runtime, one failed
// tick at a time.
//
// This fixture closes it from the outside. It extracts every statically
// written `mc` argv head from the resident's own source, builds the real
// binary, and asks the binary to dispatch each one. Any exit is acceptable —
// a missing flag, an absent spine, a refusal — except the dispatcher reporting
// that it does not know the verb.

import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const repoRoot = resolve(import.meta.dir, "..", "..");
const residentSourceDir = resolve(import.meta.dir);
const mcSourceDir = join(repoRoot, "mc", "cmd", "mc");
const suiteRoot = realpathSync(mkdtempSync(join(tmpdir(), "mc-verb-surface-")));
const mcBin = join(suiteRoot, "mc");
const probeHome = join(suiteRoot, "home");

// The resident uses these call forms for the binary. Each match yields the verb and its optional subverb.
const CALL_SHAPES = [
  /\b(?:runMc|runTraceCommand)\(\s*\[\s*"([^"]+)"(?:\s*,\s*"([^"]+)")?/g,
  /\brequireMc\(\s*[A-Za-z_.]+\s*,\s*\[\s*"([^"]+)"(?:\s*,\s*"([^"]+)")?/g,
];

// A second literal is a subverb only under a group verb; elsewhere it is the
// first argument of a flat verb and must not be dispatched as one.
const GROUP_VERBS = new Set([
  "attachment",
  "config",
  "credential",
  "delivery",
  "environment",
  "collie",
  "outbox",
  "run",
  "task",
  "project",
  "wiki",
  "wiki-state",
]);

function residentSources(): string[] {
  return readdirSync(residentSourceDir)
    .filter((name) => name.endsWith(".ts") && !name.endsWith(".test.ts"))
    .map((name) => join(residentSourceDir, name));
}

function invocations(): string[][] {
  const found = new Map<string, string[]>();
  for (const file of residentSources()) {
    const source = readFileSync(file, "utf8");
    for (const shape of CALL_SHAPES) {
      shape.lastIndex = 0;
      for (const match of source.matchAll(shape)) {
        const verb = match[1];
        const argv = GROUP_VERBS.has(verb) && match[2] !== undefined ? [verb, match[2]] : [verb];
        found.set(argv.join(" "), argv);
      }
    }
  }
  return [...found.values()].sort((left, right) => left.join(" ").localeCompare(right.join(" ")));
}

async function dispatches(argv: string[]): Promise<string> {
  const env: Record<string, string> = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (value === undefined) continue;
    if (["MC_HOME", "MC_SPINE", "MC_HELPER", "MC_RUN_JSON"].includes(key)) continue;
    env[key] = value;
  }
  env.MC_HOME = probeHome;
  env.MC_RUN_JSON = join(probeHome, "absent-run.json");
  const proc = Bun.spawn([mcBin, ...argv], {
    env,
    stdin: new Blob([""]),
    stdout: "pipe",
    stderr: "pipe",
    signal: AbortSignal.timeout(30_000),
  });
  const [stdout, stderr] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
  ]);
  await proc.exited;
  return `${stdout}${stderr}`;
}

beforeAll(async () => {
  const tags = process.platform === "linux" ? "test_fake_routing,test_scratch_spine" : "test_fake_routing";
  const built = Bun.spawnSync(["mise", "exec", "--", "go", "build", "-tags", tags, "-o", mcBin, "."], {
    cwd: mcSourceDir,
    env: process.env,
  });
  if (built.exitCode !== 0) {
    throw new Error(`build real mc test binary: ${built.stderr.toString() || built.stdout.toString()}`);
  }
}, 300_000);

afterAll(() => {
  rmSync(suiteRoot, { recursive: true, force: true });
});

test("the resident names no verb the real mc binary cannot dispatch", async () => {
  const argvs = invocations();
  expect(argvs.length).toBeGreaterThan(10);

  const unknown: string[] = [];
  for (const argv of argvs) {
    const output = await dispatches(argv);
    if (/unknown (sub)?verb/.test(output)) {
      unknown.push(`mc ${argv.join(" ")}: ${output.trim().split("\n")[0]}`);
    }
  }
  expect(unknown).toEqual([]);
}, 180_000);

test("the extractor still sees the resident's grouped and flat calls", () => {
  const argvs = invocations().map((argv) => argv.join(" "));
  expect(argvs).toContain("dispatch");
  expect(argvs).toContain("run stopped");
  expect(argvs).toContain("collie launch-bind");
  expect(argvs).toContain("wiki-state list");
  expect(argvs).toContain("__trace-sessions");
  expect(argvs).toContain("__trace-register-setup");
  expect(argvs).toContain("__wiki-export");
  for (const subverb of ["register", "start", "heartbeat", "fail", "record"]) {
    expect(argvs).toContain(`delivery ${subverb}`);
  }
});
