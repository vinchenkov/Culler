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
