// test-helpers.ts — scripted fakes for the dashboard unit lane.

import type { McResult, McRunner } from "./mc";
import type { ApiDeps } from "./api";
import { makeTestLogger } from "../../runner/shared/log";

export const ok = (json: Record<string, unknown>): McResult => ({ code: 0, json, stderr: "" });
export const domain = (message: string): McResult => ({
  code: 1,
  json: { error: { code: "domain-rejection", message } },
  stderr: `mc: ${message}`,
});
export const environment = (stderr: string): McResult => ({ code: 2, json: null, stderr });

/** Answers are matched by argv prefix, first match wins, and every call is
 * recorded — the unit tests assert the exact verb traffic. */
export class FakeMc implements McRunner {
  calls: string[][] = [];
  inputs: (string | undefined)[] = [];
  private answers: { prefix: string[]; result: McResult }[] = [];

  on(prefix: string[], result: McResult): this {
    this.answers.push({ prefix, result });
    return this;
  }

  async run(argv: string[], input?: string): Promise<McResult> {
    this.calls.push(argv);
    this.inputs.push(input);
    for (const a of this.answers) {
      if (a.prefix.every((p, i) => argv[i] === p)) {
        return a.result;
      }
    }
    throw new Error(`FakeMc: unscripted invocation: mc ${argv.join(" ")}`);
  }
}

export function deps(over: Partial<ApiDeps> = {}): ApiDeps {
  return {
    mc: new FakeMc(),
    genRef: () => "dash-feedf00d",
    log: makeTestLogger("dashboard"),
    readRender: () => Promise.reject(new Error("FakeMc: unscripted readRender")),
    readArtifact: () => Promise.reject(new Error("FakeMc: unscripted readArtifact")),
    openAttachment: () => Promise.reject(new Error("FakeMc: unscripted openAttachment")),
    nudgeResident: async () => {},
    ...over,
  };
}

export function apiReq(method: string, path: string, body?: unknown): [Request, URL] {
  const url = new URL(`http://127.0.0.1${path}`);
  const req = new Request(url, {
    method,
    headers: body === undefined ? undefined : { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return [req, url];
}
