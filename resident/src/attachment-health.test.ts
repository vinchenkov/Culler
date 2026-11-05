import { expect, test } from "bun:test";
import { AttachmentConfinementHealth } from "./attachment-health";
import { greenAttachmentCanaryReceipt } from "./test-helpers";

test("attachment confinement health is boot-local and exact", () => {
  const firstBoot = new AttachmentConfinementHealth();
  expect(() => firstBoot.assertHealthy()).toThrow("unhealthy for this resident boot");
  const receipt = greenAttachmentCanaryReceipt();
  expect(firstBoot.markHealthy(receipt)).toEqual(receipt);
  expect(firstBoot.assertHealthy()).toBe(receipt.generation);
  expect(() => firstBoot.markHealthy(receipt)).toThrow("already bound");

  const nextBoot = new AttachmentConfinementHealth();
  expect(() => nextBoot.assertHealthy()).toThrow("unhealthy for this resident boot");
});

test("attachment confinement refuses every red or expanded receipt", () => {
  const green = greenAttachmentCanaryReceipt();
  for (const changed of [
    { ...green, effective_uid: 10002 },
    { ...green, restart_inbound_read: false },
    { ...green, denied_outbound: green.denied_outbound.slice(1) },
    { ...green, extra: true },
  ]) {
    const health = new AttachmentConfinementHealth();
    expect(() => health.markHealthy(changed)).toThrow("malformed or red");
    expect(() => health.assertHealthy()).toThrow("unhealthy");
  }
});
