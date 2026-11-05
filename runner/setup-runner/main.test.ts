import { expect, test } from "bun:test";
import { prepareSetupWorkflow } from "./main";
import type { ProjectEnvironment, WorkflowDependencies } from "../shared/project-environment";

function configuration(): ProjectEnvironment {
  return {
    base_digest: `sha256:${"a".repeat(64)}`, build_files: [], build_args: {},
    setup: { command: "install", directory: "." }, services: [], declarations: [],
    variables: [{ name: "BUILD_MODE", value: "install", scope: "setup" }, { name: "APP_MODE", value: "test", scope: "agent" }],
    secrets: [{ name: "SETUP_TOKEN", reference: "registry", scope: "setup" }, { name: "APP_TOKEN", reference: "test", scope: "agent" }],
  };
}

test("initial setup prepares recorded dependencies with separate command and agent scopes", async () => {
  const calls: { command: string; env: Record<string, string | undefined> }[] = [];
  const deps: WorkflowDependencies = {
    directory: async () => "/project", sleep: async () => {},
    execute: async (command, _, env) => { calls.push({ command, env }); return { exitCode: 0, output: "" }; },
    start: () => ({ stop: async () => {}, exited: () => false }),
  };
  const result = await prepareSetupWorkflow(configuration(), "setup", "/project", {}, { SETUP_TOKEN: "registry-secret", APP_TOKEN: "app-secret" }, deps);
  expect(calls).toHaveLength(1);
  expect(calls[0]).toEqual({ command: "install", env: { BUILD_MODE: "install", SETUP_TOKEN: "registry-secret" } });
  expect(result.agentEnvironment).toEqual({ APP_MODE: "test", APP_TOKEN: "app-secret" });
  expect(result.evidence).toBe("");
  await result.workflow?.stop();
});

test("a missing prerequisite leaves repair available and keeps secrets outside the brief", async () => {
  const deps: WorkflowDependencies = {
    directory: async () => "/project", sleep: async () => {},
    execute: async () => ({ exitCode: 1, output: "registry-secret: compiler unavailable" }),
    start: () => ({ stop: async () => {}, exited: () => false }),
  };
  const result = await prepareSetupWorkflow(configuration(), "setup", "/project", {}, { SETUP_TOKEN: "registry-secret", APP_TOKEN: "app-secret" }, deps);
  expect(result.workflow).toBeUndefined();
  expect(result.evidence).toContain("compiler unavailable");
  expect(result.evidence).toContain("unverified");
  expect(result.evidence).not.toContain("registry-secret");
  expect(result.agentEnvironment.SETUP_TOKEN).toBeUndefined();
});

test("the continuation prepares dependencies without automatic repository commands", async () => {
  const calls: string[] = [];
  const deps: WorkflowDependencies = {
    directory: async () => "/project", sleep: async () => {},
    execute: async command => { calls.push(command); return { exitCode: 0, output: "" }; },
    start: () => ({ stop: async () => {}, exited: () => false }),
  };
  const result = await prepareSetupWorkflow(configuration(), "validate", "/project", {},
    { SETUP_TOKEN: "registry-secret", APP_TOKEN: "app-secret" }, deps);
  expect(calls).toEqual(["install"]);
  expect(result.evidence).toBe("");
  await result.workflow?.stop();
});

import { forwardSetupEvents } from "./main";

test("setup registers one native session after split event lines and preserves output", async () => {
  const line = JSON.stringify({ event: "session-start", session_id: "setup-native", native_file: "2026/10/12/rollout-setup-native.jsonl" }) + "\n";
  const stream = new ReadableStream<Uint8Array>({ start(controller) {
    for (const chunk of [line.slice(0, 17), line.slice(17), line, "ordinary output\n"]) controller.enqueue(new TextEncoder().encode(chunk));
    controller.close();
  } });
  const calls: string[][] = [], output: string[] = [];
  await forwardSetupEvents(stream, undefined, async (id, file) => { calls.push([id, file]); return true; }, line => output.push(line), () => { throw new Error("unexpected failure"); });
  expect(calls).toEqual([["setup-native", "2026/10/12/rollout-setup-native.jsonl"]]);
  expect(output).toHaveLength(3);
});

test("a refused trace registration preserves the setup event stream", async () => {
  const body = '{"event":"session-start","session_id":"native","native_file":"native.jsonl"}\n{"event":"turn-complete"}\n';
  const output: string[] = [], failures: string[] = [];
  await forwardSetupEvents(new Response(body).body!, undefined, async () => false, line => output.push(line), cause => failures.push(cause));
  expect(output).toHaveLength(2);
  expect(failures).toHaveLength(1);
  expect(failures[0]).toContain("recovery");
});

test("an unsafe setup trace event creates no registration", async () => {
  const body = '{"event":"session-start","session_id":"native","native_file":"../native.jsonl"}\n';
  let calls = 0;
  const failures: string[] = [];
  await forwardSetupEvents(new Response(body).body!, undefined, async () => { calls++; return true; }, () => {}, cause => failures.push(cause));
  expect(calls).toBe(0);
  expect(failures).toHaveLength(1);
});

test("setup retains terminal adapter failures separately from session registration", async () => {
  const messages = [
    { event: "session-start", session_id: "session-one", native_file: "/mc/session/native.jsonl" },
    { type: "system", subtype: "api_error", error: "An intermediate request failed." },
    { event: "infrastructure-failure", condition: "The harness request failed.", evidence: "The final request failed." },
  ];
  const lines: string[] = [];
  const stream = new ReadableStream<Uint8Array>({ start(controller) {
    controller.enqueue(new TextEncoder().encode(messages.map(value => JSON.stringify(value)).join("\n"))); controller.close();
  } });
  const failure = await forwardSetupEvents(stream, undefined, async () => true, line => lines.push(line), () => {});
  expect(failure?.condition).toBe("The harness request failed.");
  expect(failure?.evidence).toBe("The final request failed.");
  expect(lines).toHaveLength(3);
});

test("an intermediate adapter error alone supplies no terminal failure", async () => {
  const stream = new ReadableStream<Uint8Array>({ start(controller) {
    controller.enqueue(new TextEncoder().encode('{"type":"system","subtype":"api_error","error":"Temporary failure"}\n{"type":"result","is_error":false}\n')); controller.close();
  } });
  expect(await forwardSetupEvents(stream, undefined, async () => true, () => {}, () => {})).toBeUndefined();
});

test("setup redacts escaped secrets without corrupting the adapter event", async () => {
  const { diagnosticRedactor } = await import("../agent-runner/diagnostics");
  const { forwardSetupDiagnostics } = await import("./main");
  const secret = 'private"value\nsecond-line';
  const event = { event: "infrastructure-failure", condition: `The request failed: ${secret}`, evidence: "api_key=private-key" };
  const stream = (text: string) => new ReadableStream<Uint8Array>({ start(controller) {
    controller.enqueue(new TextEncoder().encode(text)); controller.close();
  } });
  const lines: string[] = [];
  const redact = diagnosticRedactor({}, [secret]);
  await forwardSetupEvents(stream(JSON.stringify(event) + "\n"), undefined, async () => true, line => lines.push(line), () => {}, redact);
  expect(JSON.parse(lines[0]!).condition).toBe("The request failed: [redacted]");
  expect(lines[0]).not.toContain("private-key");
  const diagnostic: string[] = [];
  await forwardSetupDiagnostics(stream("Bearer private-token"), line => diagnostic.push(line), redact);
  expect(diagnostic).toEqual(["Bearer [redacted]"]);
});
