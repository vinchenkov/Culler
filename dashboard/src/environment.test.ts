import { expect, test } from "bun:test";
import { handleApi } from "./api";
import { apiReq, deps } from "./test-helpers";

test("environment actions preserve the exact request and response", async () => {
  for (const action of ["get", "save", "approve", "assist", "retry", "deny", "cache-clear"]) {
    const calls: unknown[] = [];
    const nudges: string[] = [];
    const body = { project_id: "garden", configuration: { dockerfile: { path: ".culler/Dockerfile", content: "FROM sha256:abc\nRUN echo '$PATH'\n" } }, revision_id: "revision-1" };
    const result = { environment: { project_id: "garden", phase: "approval" } };
    const mc = { async run(argv: string[], input?: string) { calls.push({ argv, input }); return { code: 0, json: result, stderr: "" }; } };
    const response = await handleApi(...apiReq("POST", `/api/environment/${action}`, body), deps({ mc, nudgeResident: async reason => { nudges.push(reason); } }));
    expect(await response?.json()).toEqual(result);
    expect(calls).toEqual([{ argv: ["environment", action], input: JSON.stringify(body) }]);
    expect(nudges).toEqual(["approve", "assist", "retry", "deny", "cache-clear"].includes(action) ? ["project-environment"] : []);
  }
});

test("an environment refusal reaches the browser without an execution request", async () => {
  let nudges = 0;
  const result = { error: { code: "record-conflict", message: "The approved revision changed." } };
  const mc = { async run() { return { code: 1, json: result, stderr: "" }; } };
  const response = await handleApi(...apiReq("POST", "/api/environment/approve", { project_id: "garden", revision_id: "old" }),
    deps({ mc, nudgeResident: async () => { nudges++; } }));
  expect(response?.status).toBe(409);
  expect(await response?.json()).toEqual(result);
  expect(nudges).toBe(0);
});

test("GET requests and malformed input cannot approve an environment", async () => {
  let calls = 0;
  const mc = { async run() { calls++; return { code: 0, json: {}, stderr: "" }; } };
  await handleApi(...apiReq("GET", "/api/environment/approve"), deps({ mc }));
  const url = new URL("http://localhost/api/environment/approve");
  const response = await handleApi(new Request(url, { method: "POST", body: "{" }), url, deps({ mc }));
  expect(response?.status).toBe(400);
  expect(calls).toBe(0);
});
