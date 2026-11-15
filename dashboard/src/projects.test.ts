import { expect, test } from "bun:test";
import { handleApi } from "./api";
import { apiReq, deps } from "./test-helpers";
import { projectState } from "./ui/projects.js";

test("Project operations pass their request to the command", async () => {
  for (const action of ["setup-preview", "setup-save", "details", "vision-save", "start"]) {
    const calls: unknown[] = [];
    const request = { id: "project", path: "/a folder", vision: "The goal.\nA constraint." };
    const mc = { async run(argv: string[], input?: string) { calls.push({ argv, input }); return { code: 0, json: { saved: true }, stderr: "" }; } };
    const result = await handleApi(...apiReq("POST", `/api/projects/${action}`, request), deps({ mc }));
    expect(result?.status).toBe(200);
    expect(calls).toEqual([{ argv: ["project", action], input: JSON.stringify(request) }]);
  }
});

test("project guidance follows requirements independently of task count", () => {
  const ready = { id: "a", status: "active", vision_status: { ready: true } };
  const state = { available: true, projects: [ready], selected: "a" };
  expect(projectState(state)).toBe("ready");
  expect(projectState({ ...state, projects: [] })).toBe("empty");
  expect(projectState({ ...state, available: false, projects: [] })).toBe("unavailable");
  expect(projectState({ ...state, projects: [{ ...ready, status: "paused" }] })).toBe("paused");
  expect(projectState({ ...state, projects: [{ ...ready, vision_status: { ready: false } }] })).toBe("vision");
  expect(projectState({ ...state, projects: [{ ...ready, status: "archived" }] })).toBe("archived");
});

test("a successful pipeline start wakes the resident", async () => {
  for (const code of [0, 1]) {
    const nudges: string[] = [];
    const mc = { async run() { return { code, json: code === 0 ? { draft: {} } : { error: { message: "Initialization is incomplete." } }, stderr: "" }; } };
    await handleApi(...apiReq("POST", "/api/projects/start", { id: "project" }), deps({ mc, nudgeResident: async reason => { nudges.push(reason); } }));
    expect(nudges).toEqual(code === 0 ? ["project-start"] : []);
  }
});
