import { expect, test } from "bun:test";
import { modelResponseReporter } from "./model-response";

test("reports ignore partial events, retry failure, and stop after success", async () => {
  let calls = 0;
  let failures = 0;
  const reporter = modelResponseReporter(async () => ++calls > 1, () => { failures++; });
  for (const event of ["session-start", "turn-text", "heartbeat", "turn-complete"]) reporter.observe({ event });
  await reporter.flush();
  expect(calls).toBe(0);
  reporter.observe({ event: "model-response" });
  reporter.observe({ event: "model-response" });
  await reporter.flush();
  expect(calls).toBe(1);
  expect(failures).toBe(1);
  reporter.observe({ event: "model-response" });
  await reporter.flush();
  reporter.observe({ event: "model-response" });
  await reporter.flush();
  expect(calls).toBe(2);
});

test("a report exception preserves the active attempt", async () => {
  let failed = false;
  const reporter = modelResponseReporter(async () => { throw new Error("Unavailable"); }, () => { failed = true; });
  reporter.observe({ event: "model-response" });
  await reporter.flush();
  expect(failed).toBe(true);
});
