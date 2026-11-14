import { afterEach, expect, test } from "bun:test";
import { chmod, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInstallMaintenance } from "./install-maintenance";

const homes: string[] = [];
afterEach(async () => { await Promise.all(homes.splice(0).map(home => rm(home, { recursive: true, force: true }))); });
async function fixture() {
  const home = await mkdtemp(join(tmpdir(), "culler-maintenance-"));
  homes.push(home);
  await mkdir(join(home, "state"), { mode: 0o700 });
  const request = join(home, "state", "install-maintenance.json");
  const ack = join(home, "state", "install-maintenance-ack.json");
  return { request, ack, maintenance: createInstallMaintenance(home, "release-a") };
}

test("the installer pause waits for repository operations and preserves its request", async () => {
  const { request, ack, maintenance } = await fixture();
  expect(await maintenance(0)).toBe(false);
  await writeFile(request, JSON.stringify({ id: "request-1234567890" }), { mode: 0o600 });
  expect(await maintenance(1)).toBe(true);
  expect(JSON.parse(await readFile(ack, "utf8")).ready).toBe(false);
  expect(await maintenance(0)).toBe(true);
  expect(JSON.parse(await readFile(ack, "utf8"))).toMatchObject({ id: "request-1234567890", ready: true, release: "release-a", pid: process.pid });
  await rm(request);
  expect(await maintenance(0)).toBe(false);
  expect(await Bun.file(ack).exists()).toBe(false);
});

test("an unsafe or invalid pause request prevents dispatch", async () => {
  const { request, maintenance } = await fixture();
  await writeFile(request, "invalid", { mode: 0o600 });
  await expect(maintenance(0)).rejects.toThrow();
  await writeFile(request, JSON.stringify({ id: "request-1234567890" }));
  await chmod(request, 0o644);
  await expect(maintenance(0)).rejects.toThrow("owner-only");
  await rm(request);
  await symlink("/dev/null", request);
  await expect(maintenance(0)).rejects.toThrow();
});

test("an interrupted acknowledgement permits the next request", async () => {
  const { request, ack, maintenance } = await fixture();
  await writeFile(request, JSON.stringify({ id: "request-1234567890" }), { mode: 0o600 });
  await writeFile(`${ack}.${process.pid}.next`, "partial", { mode: 0o600 });
  expect(await maintenance(0)).toBe(true);
  expect(JSON.parse(await readFile(ack, "utf8")).id).toBe("request-1234567890");
});

test("update verification publishes receipts before normal work starts", async () => {
  const { createHash } = await import("node:crypto");
  const { waitForUpdateActivation, readInstallMaintenance } = await import("./install-maintenance");
  const home = await mkdtemp(join(tmpdir(), "culler-update-start-"));
  homes.push(home);
  await mkdir(join(home, "updates"), { mode: 0o700 });
  const path = join(home, "updates", "transaction.json");
  const record = { version: 1, id: "a".repeat(32), phase: "verify", candidate: { commit: "b".repeat(40) },
    previous: { commit: "c".repeat(40) }, deployment: createHash("sha256").update("mc-deployment-v1\0" + home).digest("hex").slice(0, 12) };
  const save = () => writeFile(path, JSON.stringify(record), { mode: 0o600 });
  await save();
  let receipts = 0;
  await expect(waitForUpdateActivation(home, record.candidate.commit, async () => { receipts++; }, async () => {}, async () => { throw new Error("preflight failed"); })).rejects.toThrow("preflight failed");
  expect(receipts).toBe(0);
  expect(await readInstallMaintenance(home)).toBe(record.id);
  await waitForUpdateActivation(home, record.candidate.commit, async () => { receipts++; }, async () => { record.phase = "committed"; await save(); });
  expect(receipts).toBe(1);
  expect(await readInstallMaintenance(home)).toBeNull();
  for (const phase of ["stop", "prepare", "checkpoint", "replace", "restore"]) {
    record.phase = phase; await save();
    await expect(waitForUpdateActivation(home, record.candidate.commit, async () => { receipts++; })).rejects.toThrow("keeps this release stopped");
  }
  expect(receipts).toBe(1);
  record.phase = "restore"; await save();
  await waitForUpdateActivation(home, record.previous.commit, async () => { receipts++; }, async () => { record.phase = "restored"; await save(); });
  expect(receipts).toBe(2);
  record.phase = "unknown"; await save();
  await expect(readInstallMaintenance(home)).rejects.toThrow("invalid identity or phase");
});

test("a visible commit keeps work stopped until its maintenance request disappears", async () => {
  const { createHash } = await import("node:crypto");
  const { waitForUpdateActivation } = await import("./install-maintenance");
  const home = await mkdtemp(join(tmpdir(), "culler-update-commit-"));
  homes.push(home);
  await mkdir(join(home, "updates"), { mode: 0o700 });
  await mkdir(join(home, "state"), { mode: 0o700 });
  const id = "a".repeat(32), commit = "b".repeat(40);
  const record = { version: 1, id, phase: "committed", candidate: { commit },
    deployment: createHash("sha256").update("mc-deployment-v1\0" + home).digest("hex").slice(0, 12) };
  await writeFile(join(home, "updates", "transaction.json"), JSON.stringify(record), { mode: 0o600 });
  const request = join(home, "state", "install-maintenance.json");
  await writeFile(request, JSON.stringify({ id }), { mode: 0o600 });
  let receipts = 0;
  await waitForUpdateActivation(home, commit, async () => { receipts++; }, async () => { await rm(request); });
  expect(receipts).toBe(1);
  await writeFile(request, JSON.stringify({ id: "another-operation-1234" }), { mode: 0o600 });
  await waitForUpdateActivation(home, commit, async () => { receipts++; });
  expect(receipts).toBe(1);
});
