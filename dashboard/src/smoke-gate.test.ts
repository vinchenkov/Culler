import { expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { assessSmokeReport, parseSmokeReport, suiteFiles } from "../smoke-gate";

const root = join(import.meta.dir, "..", "..");
const gate = join(import.meta.dir, "..", "smoke-gate.ts");

function report(tests: number, failures: number, skipped: number, files: string[]): string {
  const suites = files.map(file => `  <testsuite name="${file}" file="smoke/${file}" tests="1" failures="0" skipped="0">
    <testsuite name="group" file="smoke/${file}" line="3" tests="1">
      <testcase name="case" file="smoke/${file}" />
    </testsuite>
  </testsuite>`).join("\n");
  return `<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="bun test" tests="${tests}" assertions="1" failures="${failures}" skipped="${skipped}" time="1">
${suites}
</testsuites>
`;
}

function runGate(xml: string | null, ...args: string[]) {
  const scratch = mkdtempSync(join(tmpdir(), "smoke-gate-"));
  try {
    const path = join(scratch, "report.xml");
    if (xml !== null) writeFileSync(path, xml);
    const proc = Bun.spawnSync([process.execPath, gate, path, ...args], { stdout: "pipe", stderr: "pipe" });
    return { code: proc.exitCode, stderr: proc.stderr.toString() };
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}

test("the smoke gate reads report totals and each executed file", () => {
  const parsed = parseSmokeReport(report(3, 1, 1, ["b.smoke.test.ts", "a.smoke.test.ts"]));
  expect(parsed).toEqual({ tests: 3, failures: 1, errors: 0, skipped: 1, files: ["a.smoke.test.ts", "b.smoke.test.ts"] });
  expect(() => parseSmokeReport("bun test crashed")).toThrow("no <testsuites>");
});

test("the smoke gate fails zero tests, failures, errors, skips, and missing suite files", () => {
  const passing = { tests: 2, failures: 0, errors: 0, skipped: 0, files: ["a.smoke.test.ts", "b.smoke.test.ts"] };
  expect(assessSmokeReport(passing, ["a.smoke.test.ts", "b.smoke.test.ts"])).toEqual([]);
  expect(assessSmokeReport({ ...passing, tests: 0, files: [] })).toEqual(["The smoke run executed zero tests."]);
  expect(assessSmokeReport({ ...passing, failures: 1 })).toEqual(["The smoke run reported 1 failed tests."]);
  expect(assessSmokeReport({ ...passing, errors: 1 })).toEqual(["The smoke run reported 1 errors."]);
  expect(assessSmokeReport({ ...passing, skipped: 1 })).toEqual(["The smoke run skipped 1 tests."]);
  expect(assessSmokeReport(passing, ["a.smoke.test.ts", "c.smoke.test.ts"]))
    .toEqual(["The smoke run executed no test from c.smoke.test.ts."]);
});

test("the smoke gate command exits nonzero for each failed report", () => {
  const files = suiteFiles(join(import.meta.dir, "..", "smoke"));
  expect(files.length).toBeGreaterThan(0);
  expect(runGate(report(files.length, 0, 0, files), "--suite", join(import.meta.dir, "..", "smoke")).code).toBe(0);
  expect(runGate(report(1, 0, 0, files.slice(1)), "--suite", join(import.meta.dir, "..", "smoke")).code).toBe(1);
  expect(runGate(report(0, 0, 0, [])).stderr).toContain("executed zero tests");
  expect(runGate(report(2, 1, 0, files)).code).toBe(1);
  expect(runGate(null).stderr).toContain("wrote no report");
  expect(runGate(report(1, 0, 0, files), "--suite").code).toBe(2);
});

test("smoke.sh gates every run on its report and selects the complete suite by default", () => {
  const script = readFileSync(join(import.meta.dir, "..", "smoke.sh"), "utf8");
  expect(script).toContain('set -- smoke/\n  suite="--suite smoke"');
  expect(script).toContain('bun test --reporter=junit --reporter-outfile="$report" "$@" || status=$?');
  expect(script).toContain('bun smoke-gate.ts "$report" $suite || status=1');
  expect(script).toContain('exit "$status"');
  const fast = readFileSync(join(import.meta.dir, "..", "check.sh"), "utf8");
  expect(fast).toContain("exec mise exec -- bun test src/");
  expect(fast.split("\n").filter(line => !line.startsWith("#")).join("\n")).not.toContain("smoke");
});

// The workflow uploads MC_SMOKE_EVIDENCE_DIR, so each unconditional screenshot
// path starts at evidencePath(). MC_UX_EVIDENCE paths are opt-in.
test("each smoke screenshot writes under the evidence directory", () => {
  const smoke = join(import.meta.dir, "..", "smoke");
  const outside: string[] = [];
  for (const name of suiteFiles(smoke)) {
    const source = readFileSync(join(smoke, name), "utf8");
    // A named directory counts only when the file derives it from these roots.
    const roots = ["evidencePath("];
    if (/const evidence = evidencePath\(/.test(source)) roots.push("join(evidence,");
    if (source.includes("const root = process.env.MC_UX_EVIDENCE;")) roots.push("join(root,");
    roots.push("`${process.env.MC_UX_EVIDENCE}/");
    for (const match of source.matchAll(/screenshot\(\{\s*path:\s*([^\n]+)/g)) {
      if (!roots.some(root => match[1]!.startsWith(root))) outside.push(`${name}: ${match[1]}`);
    }
  }
  expect(outside).toEqual([]);
});

// The release workflow validation: the browser job runs the complete suite on
// pull requests and pushes to main, and release publication requires it.
test("the release workflow requires the dashboard browser job before publication", () => {
  const workflow = Bun.YAML.parse(readFileSync(join(root, ".github", "workflows", "release.yml"), "utf8")) as any;
  expect(workflow.on.pull_request.branches).toEqual(["main"]);
  expect(workflow.on.push.branches).toEqual(["main"]);
  expect(workflow.permissions).toEqual({ contents: "read" });

  const browser = workflow.jobs["dashboard-browser"];
  expect(browser.if).toBeUndefined();
  expect(browser.needs).toBeUndefined();
  expect(browser.permissions).toEqual({ contents: "read" });
  expect(JSON.stringify(browser)).not.toMatch(/secrets\.|github\.token|login-action|GH_TOKEN|packages:/);
  const steps = browser.steps as any[];
  expect(steps[0].with).toEqual({ ref: "${{ github.sha }}", "persist-credentials": false });
  const runs = steps.map(step => String(step.run ?? ""));
  expect(steps.some(step => String(step.uses).startsWith("jdx/mise-action@"))).toBe(true);
  expect(steps.find(step => String(step.uses).startsWith("actions/setup-node@")).with["node-version"])
    .toBe(readFileSync(join(root, "runner", "image", "Dockerfile"), "utf8").match(/ARG NODE_VERSION=(\S+)/)![1]);
  const install = runs.find(run => run.includes("install --with-deps chromium"))!;
  expect(install).toContain("cp runner/image/project-tools/package.json runner/image/project-tools/package-lock.json");
  expect(install).toContain('npm ci --prefix "$tools" --omit=dev --ignore-scripts');
  expect(install).toContain('"$tools/node_modules/.bin/playwright" install --with-deps chromium');
  expect(install).toContain(`MC_SMOKE_PLAYWRIGHT=%s\\n' "$tools/node_modules/playwright/index.mjs"`);
  const suite = steps.find(step => String(step.run ?? "").includes("./dashboard/smoke.sh"));
  expect(suite.run).toContain("set -o pipefail");
  expect(suite.run).toMatch(/\.\/dashboard\/smoke\.sh 2>&1 \| tee /);
  expect(suite.if).toBeUndefined();
  expect(suite["continue-on-error"]).toBeUndefined();
  expect(browser["continue-on-error"]).toBeUndefined();
  const upload = steps.find(step => String(step.uses).startsWith("actions/upload-artifact@"));
  expect(upload.if).toBe("always()");
  expect(upload.with.path).toBe("${{ runner.temp }}/culler-smoke");
  const selection = runs.find(run => run.includes("MC_SMOKE_REPORT"))!;
  for (const name of ["MC_SMOKE_REPORT", "MC_SMOKE_EVIDENCE_DIR"]) expect(selection).toContain(`${name}=%s\\n' "$RUNNER_TEMP/culler-smoke/`);

  const publication = workflow.jobs["release-downloads"];
  expect(publication.needs).toContain("dashboard-browser");
  expect(publication.if).toBe("github.event_name == 'push' && github.ref == 'refs/heads/main'");
  expect(workflow.jobs["product-image"].needs).toContain("dashboard-browser");
  expect(workflow.jobs["release-images"].needs).toContain("fast-suite-macos");

  const fast = workflow.jobs["fast-suite-macos"].steps.map((step: any) => String(step.run ?? "")).join("\n");
  expect(fast).toContain("dashboard; do");
  expect(fast).not.toContain("smoke");
});
