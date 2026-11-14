import { expect, test } from "bun:test";
import { handleApi } from "./api";
import { FakeMc, apiReq, deps, domain, ok } from "./test-helpers";

test("pipeline controls call dedicated commands", async () => {
  const cases: [string, string, string[]][] = [
    ["GET", "/api/pause", ["pause", "status"]],
    ["POST", "/api/pause", ["pause"]],
    ["POST", "/api/resume", ["resume"]],
    ["POST", "/api/projects/ws-one/pause", ["project", "pause", "ws-one"]],
    ["POST", "/api/projects/ws-one/resume", ["project", "resume", "ws-one"]],
  ];
  for (const [method, path, command] of cases) {
    const body = { projects: [{ id: "ws-one", title: "One", status: "paused", setup_status: "ready" }] };
    const mc = new FakeMc().on(command, ok(body));
    const response = await handleApi(...apiReq(method, path), deps({ mc }));
    expect(response?.status).toBe(200);
    expect(await response?.json()).toEqual(body);
    expect(mc.calls).toEqual([command]);
  }
});

test("a pause refusal reaches the dashboard", async () => {
  const mc = new FakeMc().on(["project", "resume"], domain("The Project is archived."));
  const response = await handleApi(...apiReq("POST", "/api/projects/old/resume"), deps({ mc }));
  expect(response?.status).toBe(400);
  expect((await response?.json() as any).error.message).toBe("The Project is archived.");
});

test("reads cannot change a pause", async () => {
  const mc = new FakeMc();
  for (const path of ["/api/resume", "/api/projects/ws/pause", "/api/projects/ws/resume"]) {
    expect((await handleApi(...apiReq("GET", path), deps({ mc })))?.status).toBe(404);
  }
  expect(mc.calls).toEqual([]);
});
