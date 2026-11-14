import { expect, test } from "bun:test";
import { createEnvironmentHealth } from "./environment-health";
import { makeRig, ok, settle } from "./test-helpers";

const failedResult = { operation_id: "setup-one", inputs_digest: "digest", status: "failed", error: "The harness stopped.", metadata: { failure_details: "Harness evidence." } };

test("failed saves survive successful work requests and a resident restart", async () => {
  const { deps, fakeFs } = makeRig();
  let fail = true;
  const submitted: any[] = [];
  deps.runMc = async (args, input) => {
    if (args[1] === "get") return ok(JSON.stringify({ operations: [{ id: failedResult.operation_id, status: "running" }] }));
    submitted.push(JSON.parse(new TextDecoder().decode(input)));
    return fail ? { exitCode: 2, stdout: "", stderr: "The helper is unavailable." } : ok("{}");
  };
  const read = () => JSON.parse(fakeFs.writes.get(`${deps.config.mcHome}/health/resident-setup.json`)!);
  const first = createEnvironmentHealth(deps, "first", () => new Date("2026-10-13T00:00:00Z"));
  await first.recover();
  await first.submit("garden", failedResult);
  await first.recordRequest();
  expect(read().request.status).toBe("succeeded");
  expect(read().submissions).toHaveLength(1);
  expect(read().submissions[0]).toMatchObject({ project_id: "garden", operation_id: "setup-one", result_error: "The harness stopped.", message: "The helper is unavailable." });
  const second = createEnvironmentHealth(deps, "second", () => new Date("2026-10-13T00:00:05Z"));
  await second.recover();
  expect(read().submissions[0].resident_instance_id).toBe("second");
  expect(read().request.resident_instance_id).toBe("first");
  expect(read().request.observed_at).toBe("2026-10-13T00:00:00.000Z");
  fail = false;
  await second.recover();
  expect(read().submissions).toEqual([]);
  expect(submitted).toEqual([failedResult, failedResult, failedResult]);
  expect(fakeFs.writes.has(`${deps.config.mcHome}/health/setup-results/setup-one.json`)).toBe(false);
});

test("one result acknowledgement clears only that operation's observation", async () => {
  const { deps, fakeFs } = makeRig();
  let recovered = "";
  deps.runMc = async (args, input) => args[1] === "get" ? ok('{"operations":[]}')
    : JSON.parse(new TextDecoder().decode(input)).operation_id === recovered ? ok("{}")
      : { exitCode: 2, stdout: "", stderr: "The result could not be saved." };
  const health = createEnvironmentHealth(deps, "resident");
  await health.recover();
  await health.submit("garden", failedResult);
  await health.submit("library", { ...failedResult, operation_id: "setup-two" });
  await health.recordRequest(new Error("The work request failed."));
  recovered = "setup-one";
  await health.recover();
  const snapshot = JSON.parse(fakeFs.writes.get(`${deps.config.mcHome}/health/resident-setup.json`)!);
  expect(snapshot.submissions.map((row: any) => row.operation_id)).toEqual(["setup-two"]);
  expect(snapshot.request.message).toBe("The work request failed.");
});

test("an acknowledgement lost after commit uses the recorded terminal operation", async () => {
  const { deps } = makeRig();
  deps.runMc = async args => args[1] === "get"
    ? ok(JSON.stringify({ operations: [{ id: "setup-one", status: "succeeded" }] }))
    : { exitCode: 1, stdout: "", stderr: "This operation already has a result." };
  const health = createEnvironmentHealth(deps, "resident");
  await health.recover();
  await health.submit("garden", failedResult);
  expect(health.has("setup-one")).toBe(false);
});

test("overlapping requests share one result submission", async () => {
  const { deps } = makeRig();
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let calls = 0;
  deps.runMc = async () => { calls++; await gate; return ok("{}"); };
  const health = createEnvironmentHealth(deps, "resident");
  await health.recover();
  const submitted = health.submit("garden", failedResult);
  await settle();
  const recovered = health.recover();
  release();
  await Promise.all([submitted, recovered]);
  expect(calls).toBe(1);
});
