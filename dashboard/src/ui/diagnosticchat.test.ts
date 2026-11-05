import { expect, test } from "bun:test";
import { conditionDiagnostic, createDiagnosticChat, diagnosticMessage, setupDiagnostic, taskDiagnostic } from "./diagnosticchat.js";
import { taskDetail } from "./board.js";

test("diagnostic context bounds the evidence and selects only relevant fields", () => {
  const message = diagnosticMessage({ title: "A task needs review", summary: "x".repeat(12000), task_id: 7,
    project_id: "project-a", run_id: "run-4", logs: "private logs", token: "private credential", description: "Full requirements" });
  expect(message.length).toBeLessThan(2500);
  expect(message).toContain("Excerpt; read the referenced record");
  expect(message).toContain('"task_id": 7');
  expect(message).toContain('"project_id": "project-a"');
  expect(message).toContain('"run_id": "run-4"');
  expect(message).not.toContain("private");
  expect(message).not.toContain("Full requirements");
  expect(message).toContain("Propose the next action.");
});

test("task context retains the task and Project when the selected Project differs", () => {
  const row = { id: 3, project: "actual-project", title: "Test container access", blocked: 1,
    blocked_reason: "The container cannot reach Docker.", delivery: { verifier_correction_open: 1,
      verifier_report: { problem: "The boundary test remains unverified.", report_path: "/full/report.html" } } };
  const context = taskDiagnostic(row, taskDetail(row));
  expect(context.project_id).toBe("actual-project");
  expect(context.task_id).toBe(3);
  expect(context.summary).toContain("The container cannot reach Docker.");
  expect(context.summary).not.toContain("The boundary test remains unverified.");
  expect(context.state).toBe("Decision Pending");
  expect(diagnosticMessage(context)).not.toContain("/full/report.html");
});

test("connection evidence identifies an unavailable current status and the diagnostic reference", () => {
  const context = conditionDiagnostic({ id: "connection:a", group: "connections", name: "Work connection",
    state: "stopped", roles: ["worker"], connection_id: "a", diagnostic_id: 23, profile_id: "work",
    http_status: 401, provider_error_code: "invalid_grant", latest_failure_at: "2026-10-20T10:00:00Z" }, true);
  expect(context.state).toContain("Last known:");
  expect(context.state).toContain("Current status is unavailable");
  expect(context.summary).toContain("invalid_grant");
  expect(context.diagnostic_id).toBe(23);
  expect(context.recorded_at).toBe("2026-10-20T10:00:00Z");
  expect(context.task_id).toBeUndefined();
});

test("a stalled task includes its failed run while a resolved failure stays outside the context", () => {
  const row = { id: 4, project: "alpha", stalled: 1, infrastructure_failure_count: 3,
    latest_infrastructure_failure: { run_id: "run-failed", condition: "The container stopped before the first heartbeat.",
      failed_at: "2026-10-20T09:00:00Z" } };
  const context = taskDiagnostic(row, taskDetail(row));
  expect(context.state).toBe("Retries Exhausted");
  expect(context.run_id).toBe("run-failed");
  expect(context.summary).toContain("The container stopped before the first heartbeat.");
  const resolved = { ...row, stalled: 0 };
  expect(taskDiagnostic(resolved, taskDetail(resolved)).summary).toBe("");
});

test("setup evidence names the operation without its full output", () => {
  const context = setupDiagnostic("project-a", { id: "op-9", revision_id: 8, status: "failed", metadata: { output: "full log" } },
    "Prepare the Project image", "The image build failed.");
  expect(context.operation_id).toBe("op-9");
  expect(context.revision_id).toBe(8);
  expect(diagnosticMessage(context)).not.toContain("full log");
});

test("concurrent clicks share one ordinary conversation and one message", async () => {
  const calls: string[] = [], opened: string[] = [];
  const ask = createDiagnosticChat({
    api: async (path, options) => { calls.push(path); if (path === "/api/sessions") {
      expect(JSON.parse(options.body)).toEqual({}); return { session_id: "h-new" };
    } return {}; },
    openConversation: async session => { opened.push(session.session_id); },
  });
  await Promise.all([ask({ task_id: 3 }), ask({ task_id: 3 })]);
  expect(calls).toEqual(["/api/sessions", "/api/sessions/h-new/send"]);
  expect(opened).toEqual(["h-new"]);
});

test("a lost send response checks fresh history and preserves one message", async () => {
  let body = "", starts = 0, sends = 0, opens = 0;
  const ask = createDiagnosticChat({
    api: async (path, options) => {
      if (path === "/api/sessions") { starts++; return { session_id: "h-new" }; }
      if (path.endsWith("/send")) { sends++; body = JSON.parse(options.body).body; throw new Error("The response was lost."); }
      expect(options.maxAge).toBe(0);
      return { session_id: "h-new", messages: [{ direction: "inbound", body }] };
    }, openConversation: async () => { opens++; },
  });
  await expect(ask({ diagnostic_id: 8 })).rejects.toThrow("The response was lost.");
  expect(opens).toBe(0);
  await ask({ diagnostic_id: 8 });
  expect([starts, sends, opens]).toEqual([1, 1, 1]);
});

test("a later request starts a fresh conversation after the first request completes", async () => {
  let starts = 0;
  const opened: string[] = [];
  const ask = createDiagnosticChat({
    api: async path => path === "/api/sessions" ? { session_id: `h-${++starts}` } : {},
    openConversation: async session => { opened.push(session.session_id); },
  });
  await ask({ task_id: 3 });
  await ask({ task_id: 3 });
  expect(opened).toEqual(["h-1", "h-2"]);
});

test("an unavailable history preserves the uncertain message until a reliable retry", async () => {
  let sends = 0, historyAvailable = false;
  const ask = createDiagnosticChat({
    api: async path => {
      if (path === "/api/sessions") return { session_id: "h-new" };
      if (path.endsWith("/send")) { if (++sends === 1) throw new Error("The send failed."); return {}; }
      return historyAvailable ? { session_id: "h-new", messages: [] } : {};
    }, openConversation: async () => {},
  });
  await expect(ask({ task_id: 3 })).rejects.toThrow("The send failed.");
  await expect(ask({ task_id: 3 })).rejects.toThrow("The conversation history is unavailable.");
  expect(sends).toBe(1);
  historyAvailable = true;
  await ask({ task_id: 3 });
  expect(sends).toBe(2);
});
