// smoke-gate.ts — checks the JUnit report of one dashboard/smoke.sh run.
// A Bun test run that selects zero tests exits zero, so the exit status alone
// cannot prove that the browser suite ran. The gate fails a report with zero
// tests, a failure, an error, or a skip. With --suite it also requires one
// executed test file for each smoke test file in that directory.
//
// Usage: bun smoke-gate.ts <junit report> [--suite <directory>]

import { existsSync, readdirSync, readFileSync } from "node:fs";
import { basename } from "node:path";

export interface SmokeReport {
  tests: number;
  failures: number;
  errors: number;
  skipped: number;
  files: string[];
}

function attribute(tag: string, name: string): number {
  const match = tag.match(new RegExp(`\\s${name}="(\\d+)"`));
  return match ? Number(match[1]) : 0;
}

/** Reads the totals of a Bun JUnit report and each test file it names. */
export function parseSmokeReport(xml: string): SmokeReport {
  const root = xml.match(/<testsuites\b[^>]*>/);
  if (root === null) throw new Error("The smoke report has no <testsuites> element.");
  const files = new Set<string>();
  for (const suite of xml.matchAll(/<testsuite\b[^>]*\sfile="([^"]*)"/g)) files.add(basename(suite[1]!));
  return {
    tests: attribute(root[0], "tests"),
    failures: attribute(root[0], "failures"),
    errors: attribute(root[0], "errors"),
    skipped: attribute(root[0], "skipped"),
    files: [...files].sort(),
  };
}

/** Returns each reason that the report fails the gate. An empty list passes. */
export function assessSmokeReport(report: SmokeReport, expectedFiles: string[] = []): string[] {
  const problems: string[] = [];
  if (report.tests === 0) problems.push("The smoke run executed zero tests.");
  if (report.failures > 0) problems.push(`The smoke run reported ${report.failures} failed tests.`);
  if (report.errors > 0) problems.push(`The smoke run reported ${report.errors} errors.`);
  if (report.skipped > 0) problems.push(`The smoke run skipped ${report.skipped} tests.`);
  const executed = new Set(report.files);
  for (const file of expectedFiles) {
    if (!executed.has(file)) problems.push(`The smoke run executed no test from ${file}.`);
  }
  return problems;
}

/** Lists the smoke test files of a suite directory. */
export function suiteFiles(directory: string): string[] {
  return readdirSync(directory).filter(name => name.endsWith(".test.ts")).sort();
}

if (import.meta.main) {
  const [path, flag, directory, ...rest] = process.argv.slice(2);
  if (path === undefined || (flag !== undefined && (flag !== "--suite" || directory === undefined)) || rest.length > 0) {
    console.error("usage: bun smoke-gate.ts <junit report> [--suite <directory>]");
    process.exit(2);
  }
  if (!existsSync(path)) {
    console.error(`smoke gate: the smoke run wrote no report at ${path}.`);
    process.exit(1);
  }
  const report = parseSmokeReport(readFileSync(path, "utf8"));
  const expected = directory === undefined ? [] : suiteFiles(directory);
  if (directory !== undefined && expected.length === 0) {
    console.error(`smoke gate: ${directory} holds no smoke test files.`);
    process.exit(1);
  }
  const problems = assessSmokeReport(report, expected);
  for (const problem of problems) console.error(`smoke gate: ${problem}`);
  if (problems.length > 0) process.exit(1);
  console.log(`smoke gate: ${report.tests} tests passed across ${report.files.length} files.`);
}
