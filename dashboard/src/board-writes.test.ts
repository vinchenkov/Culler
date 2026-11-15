import { describe, expect, test } from "bun:test";

import { handleApi } from "./api";
import type { McResult } from "./mc";
import { apiReq, deps, FakeMc, ok } from "./test-helpers";

function command(result: McResult) {
  const calls: { argv: string[]; input?: string }[] = [];
  return {
    calls,
    async run(argv: string[], input?: string) {
      calls.push({ argv, input });
      return result;
    },
  };
}

describe("board requests", () => {
  test("the command receives the full request through standard input", async () => {
    const request = {
      operation_id: "create-from-board",
      action: "create",
      scope: "task",
      project: "project-a",
      title: "Keep $(the title) and `these characters`",
      description: "The first requirement.\nThe second requirement.",
      priority: -1,
    };
    const accepted = { task_id: 42, scope: "task", record_version: 1 };
    const mc = command({ code: 0, json: accepted, stderr: "" });
    const response = await handleApi(...apiReq("POST", "/api/board/write", request), deps({ mc }));

    expect(response?.status).toBe(200);
    expect(await response?.json()).toEqual(accepted);
    expect(mc.calls).toEqual([{ argv: ["board", "write"], input: JSON.stringify(request) }]);
  });

  test("a requirements revision reaches the command unchanged", async () => {
    const request = {
      operation_id: "resolve-42",
      action: "revise",
      task_id: 42,
      expected_version: 5,
      description: "Export CSV only.\nThe export omits the JSON file.",
      reason: "The operator chose the CSV criterion.",
    };
    const accepted = { operation_id: "resolve-42", action: "revise", task_id: 42, scope: "task", record_version: 6, revision_id: 3, revision_state: "applied" };
    const mc = command({ code: 0, json: accepted, stderr: "" });
    const response = await handleApi(...apiReq("POST", "/api/board/write", request), deps({ mc }));

    expect(response?.status).toBe(200);
    expect(await response?.json()).toEqual(accepted);
    expect(mc.calls).toEqual([{ argv: ["board", "write"], input: JSON.stringify(request) }]);
  });

  test("a conflict preserves the command response and current record", async () => {
    const conflict = {
      error: { code: "record-conflict", message: "The task changed. Read the current record." },
      current: { id: 42, record_version: 3, title: "The current title" },
    };
    const mc = command({ code: 1, json: conflict, stderr: "" });
    const request = { operation_id: "edit-42", action: "edit", task_id: 42, expected_version: 2, title: "My title" };
    const response = await handleApi(...apiReq("POST", "/api/board/write", request), deps({ mc }));

    expect(response?.status).toBe(409);
    expect(await response?.json()).toEqual(conflict);
    expect(JSON.parse(mc.calls[0]!.input!)).toEqual(request);
  });

  test("the command validates protected fields and returns its rejection", async () => {
    const request = { operation_id: "protected-field", action: "edit", task_id: 42, expected_version: 1, status: "packaged" };
    const rejection = { error: { code: "usage", message: "The request contains a protected field." } };
    const mc = command({ code: 2, json: rejection, stderr: "" });
    const response = await handleApi(...apiReq("POST", "/api/board/write", request), deps({ mc }));

    expect(response?.status).toBe(400);
    expect(await response?.json()).toEqual(rejection);
    expect(JSON.parse(mc.calls[0]!.input!)).toEqual(request);
  });

  test("a malformed request reaches no command", async () => {
    const mc = command({ code: 0, json: {}, stderr: "" });
    for (const body of ["{", "[]", "null"]) {
      const url = new URL("http://127.0.0.1/api/board/write");
      const request = new Request(url, { method: "POST", headers: { "content-type": "application/json" }, body });
      const response = await handleApi(request, url, deps({ mc }));
      expect(response?.status).toBe(400);
    }
    expect(mc.calls).toEqual([]);
  });

  test("a read request cannot submit a board write", async () => {
    const mc = command({ code: 0, json: {}, stderr: "" });
    const response = await handleApi(...apiReq("GET", "/api/board/write"), deps({ mc }));
    expect(response?.status).toBe(404);
    expect(mc.calls).toEqual([]);
  });
});

describe("historical reports", () => {
  test("the recorded report controls the host path", async () => {
    const lookup = ["board", "history-report", "--task", "42", "--record", "8", "--kind", "packet"];
    const mc = new FakeMc()
      .on(lookup, ok({ task_id: 42, record_id: 8, render_path: "/mc/artifacts/reports/original.md" }))
      .on(["__artifact-host-path", "/mc/artifacts/reports/original.md"], ok({ host_path: "/scratch/reports/original.md" }));
    const reads: string[] = [];
    const response = await handleApi(
      ...apiReq("GET", "/api/tasks/42/history/packets/8/render?path=/another/report"),
      deps({ mc, readRender: async (path) => { reads.push(path); return "# Original report"; } }),
    );
    expect(response?.status).toBe(200);
    expect(await response?.text()).toBe("# Original report");
    expect(response?.headers.get("content-security-policy")).toBe("sandbox");
    expect(reads).toEqual(["/scratch/reports/original.md"]);
    expect(mc.calls[0]).toEqual(lookup);
  });

  test("a history row without a report causes no file read", async () => {
    const mc = new FakeMc().on(["board", "history-report"], ok({ task_id: 42, render_path: null }));
    const response = await handleApi(...apiReq("GET", "/api/tasks/42/history/evidence/8/render"), deps({ mc }));
    expect(response?.status).toBe(404);
    expect(mc.calls).toHaveLength(1);
  });

  test("a historical delivery report must match its recorded content", async () => {
    const mc = new FakeMc()
      .on(["board", "history-report"], ok({ render_path: "/mc/artifacts/report.html", report_sha256: "0".repeat(64) }))
      .on(["__artifact-host-path"], ok({ host_path: "/scratch/report.html" }));
    const response = await handleApi(
      ...apiReq("GET", "/api/tasks/42/history/evidence/8/render"),
      deps({ mc, readRender: async () => "The report changed." }),
    );
    expect(response?.status).toBe(409);
  });
});
