import { expect, test } from "bun:test";
import { applyPendingVisions } from "./vision-save";
import { msgLogger } from "./test-helpers";
import type { ExecResult } from "./types";

const ok = (body: unknown): ExecResult => ({ exitCode: 0, stdout: JSON.stringify(body), stderr: "" });

test("the resident applies each authorized submission through the host command", async () => {
  const calls: Array<{ argv: string[]; body: unknown }> = [];
  const logs: string[] = [];
  await applyPendingVisions(async (argv, stdin) => {
    calls.push({ argv, body: stdin ? JSON.parse(new TextDecoder().decode(stdin)) : null });
    if (argv[1] === "vision-pending") return ok({ submissions: [
      { id: "first", submission_id: "save-1", content: "Untrusted list content" },
      { id: "second", submission_id: "save-2" },
    ] });
    if (calls.length === 2) throw new Error("The host is unavailable.");
    return ok({ draft: { setup_status: "configured" } });
  }, msgLogger(logs));
  expect(calls).toEqual([
    { argv: ["project", "vision-pending"], body: null },
    { argv: ["project", "vision-apply"], body: { id: "first", submission_id: "save-1" } },
    { argv: ["project", "vision-apply"], body: { id: "second", submission_id: "save-2" } },
  ]);
  expect(logs).toEqual(["Culler cannot complete the Vision save."]);
});

test("an invalid submission list leaves every workspace untouched", async () => {
  let calls = 0;
  const logs: string[] = [];
  await applyPendingVisions(async () => {
    calls++;
    return ok({ submissions: [{ id: "first" }] });
  }, msgLogger(logs));
  expect(calls).toBe(1);
  expect(logs).toEqual(["Culler cannot inspect the Vision submissions."]);
});
