import { expect, test } from "bun:test";
import { handleApi } from "./api";
import { apiReq, deps, FakeMc, ok } from "./test-helpers";

test("customization routes preserve the command records and exact text", async () => {
  const mc = new FakeMc().on(["project", "role-show"], ok({ current: { instructions: "Text" } }))
    .on(["project", "role-save"], ok({ instructions: "Text" })).on(["project", "role-unblock"], ok({ blocked: false }))
    .on(["run", "customization"], ok({ instructions: "Original" }));
  const d = deps({ mc });
  const show = await handleApi(...apiReq("GET", "/api/projects/role-show?project=alpha&role=strategist%28propose%29"), d);
  expect(await show!.json()).toEqual({ current: { instructions: "Text" } });
  expect(mc.calls[0]).toEqual(["project", "role-show", "--project", "alpha", "--role", "strategist(propose)"]);
  const body = { project: "alpha", role: "worker", instructions: "  Text\n" };
  await handleApi(...apiReq("POST", "/api/projects/role-save", body), d);
  expect(mc.inputs[1]).toBe(JSON.stringify(body));
  await handleApi(...apiReq("POST", "/api/projects/role-unblock", { run: "run-1", reason: "Resolved" }), d);
  expect(mc.calls[2]).toEqual(["project", "role-unblock"]);
  await handleApi(...apiReq("GET", "/api/runs/run-1/customization"), d);
  expect(mc.calls[3]).toEqual(["run", "customization", "run-1"]);
});
