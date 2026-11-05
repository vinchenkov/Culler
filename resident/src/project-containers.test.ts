import { expect, test } from "bun:test";
import { projectContainers } from "./project-containers";
import type { Exec } from "./types";

const ok = (stdout = "") => ({ exitCode: 0, stdout, stderr: "" });

test("Project creates and runs use the host guard with their input", async () => {
  const calls: unknown[] = [];
  const guarded = projectContainers(async () => { throw new Error("The unguarded Docker command ran."); },
    async (...args) => { calls.push(args); return ok(JSON.stringify(ok("container"))); });
  const input = new Uint8Array([1, 2]);
  for (const action of ["create", "run", "build"]) {
    const args = [action, "--label", "mc-project=garden", "image"];
    expect((await guarded(args, input)).stdout).toBe("container");
    expect(calls.at(-1)).toEqual([["__project-docker", "garden", "--", ...args], input, undefined]);
  }
});

test("Project starts recover the Project label after a resident restart", async () => {
  const calls: string[][] = [];
  const raw: Exec = async argv => {
    calls.push(argv);
    return ok(JSON.stringify([{ Config: { Labels: { "com.culler.project": "garden" } } }]));
  };
  const guarded = projectContainers(raw, async argv => { calls.push(argv); return ok(JSON.stringify(ok())); });
  await guarded(["start", "--interactive", "container"]);
  expect(calls).toEqual([
    ["inspect", "--type", "container", "container"],
    ["__project-docker", "garden", "--", "start", "--interactive", "container"],
  ]);
});

test("cleanup and ordinary Homie containers remain available during Project deletion", async () => {
  const calls: string[][] = [];
  const guarded = projectContainers(async argv => { calls.push(argv); return ok(); },
    async () => { throw new Error("An unrelated command used the Project guard."); });
  await guarded(["rm", "--force", "container"]);
  await guarded(["create", "--label", "mc-tier=homie", "image"]);
  expect(calls.length).toBe(2);
});
