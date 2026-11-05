import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import {
  collectHomieInventory,
  execMcVia,
  GATEWAY_CONTROL_VERSION,
  SPINE_SCHEMA_ID,
  type ResidentControlIdentity,
} from "./resident-control";
import { HomieRuntimeRegistry } from "./homie-runtime";
import { canonicalHomieDockerEnvelope } from "./homie-envelope";
import type { PipelineLivenessEntry } from "./pipeline-liveness";
import { fail, FakeExec, homieInspectFromCreate, ok } from "./test-helpers";

const homes: string[] = [];

/** The default control-test child speaks the protocol but has nothing to
 * observe; tests that care about the observation supply their own. */
const noLiveness = async (): Promise<PipelineLivenessEntry[]> => [];

function runtime(fill = 3): HomieRuntimeRegistry {
  return new HomieRuntimeRegistry(Buffer.alloc(32, fill));
}

afterEach(async () => {
  await Promise.all(homes.splice(0).map((path) => rm(path, { recursive: true, force: true })));
});

async function identity(): Promise<ResidentControlIdentity> {
  const mcHome = await mkdtemp(join(tmpdir(), "mc-resident-control-"));
  homes.push(mcHome);
  await writeFile(join(mcHome, "deployment.uuid"), "deployment-test-1\n", { mode: 0o600 });
  return { mcHome, releaseBuildId: "development", configSchemaVersion: 1 };
}

function helloChild(overrides: Record<string, unknown> = {}, waitForAck = true): string {
  const hello = {
    op: "hello",
    seq: 1,
    release_build_id: "development",
    gateway_control_version: GATEWAY_CONTROL_VERSION,
    spine_schema_id: SPINE_SCHEMA_ID,
    config_schema_version: 1,
    deployment_uuid: "deployment-test-1",
    ...overrides,
  };
  const encoded = Buffer.from(JSON.stringify(hello)).toString("base64");
  return `
import base64
import json
import os
import stat
import struct
import sys

sockets = []
for fd in range(4, 65):
    try:
        if stat.S_ISSOCK(os.fstat(fd).st_mode):
            sockets.append(fd)
    except OSError:
        pass
if sockets:
    raise RuntimeError("Extra control sockets: " + str(sockets))

def read_bytes(length):
    data = bytearray()
    while len(data) < length:
        part = os.read(3, length - len(data))
        if not part:
            raise RuntimeError("The control socket closed.")
        data.extend(part)
    return bytes(data)

def read_frame():
    return read_bytes(struct.unpack("!I", read_bytes(4))[0]).decode()

def write_frame(body):
    frame = struct.pack("!I", len(body)) + body
    while frame:
        frame = frame[os.write(3, frame):]

def write_request(value):
    write_frame(json.dumps(value, separators=(",", ":")).encode())

write_frame(base64.b64decode(${JSON.stringify(encoded)}))
if ${waitForAck ? "False" : "True"}:
    sys.exit(0)
ack = json.loads(read_frame())
write_request({"op": "homie_inventory", "seq": 2})
inventory = json.loads(read_frame())
write_request({"op": "pipeline_liveness", "seq": 3})
liveness_body = read_frame()
liveness = json.loads(liveness_body)
if liveness["op"] != "pipeline_liveness_result":
    raise RuntimeError("The liveness operation differs.")
if liveness["seq"] != 3:
    raise RuntimeError("The liveness sequence differs.")
print(json.dumps({
    "action": "idle", "ack": ack["op"],
    "inventory_count": len(inventory["containers"]), "liveness": liveness_body
}))
  `;
}

describe("resident one-use dispatch control", () => {
  test("keeps the gateway control version in lockstep with the Go private crossing", async () => {
    const crossing = await readFile(
      join(import.meta.dir, "../../mc/verbs/dispatchprivate.go"),
      "utf8",
    );
    const declared = crossing.match(/const GatewayControlVersion = (\d+)/);
    expect(declared?.[1]).toBe(String(GATEWAY_CONTROL_VERSION));
  });

  test("passes only dispatch an inherited full-duplex fd 3 and completes hello/hello_ack", async () => {
    const docker = new FakeExec();
    docker.enqueue(ok(""), ok(""));
    const exec = execMcVia(["python3", "-I", "-c", helloChild()], await identity(), docker.exec, runtime(), noLiveness);
    for (let tick = 0; tick < 2; tick++) {
      const result = await exec(["dispatch"]);
      expect(result.exitCode).toBe(0);
      expect(JSON.parse(result.stdout)).toEqual({
        action: "idle", ack: "hello_ack", inventory_count: 0,
        liveness: '{"op":"pipeline_liveness_result","seq":3,"observed":[]}',
      });
    }
    expect(docker.calls).toEqual([
      ["ps", "-aq", "--no-trunc", "--filter", "label=mc-managed=true", "--filter", "label=mc-tier=homie"],
      ["ps", "-aq", "--no-trunc", "--filter", "label=mc-managed=true", "--filter", "label=mc-tier=homie"],
    ]);
  });

  test("a child that never sends hello fails the tick closed and the next dispatch recovers", async () => {
    // Reconstruction of the live failure `tick failed: resident control hello
    // timeout` (resident.stderr.log, 2026-08-08..10): the dispatch child comes
    // up too slowly to answer its first control frame inside HELLO_TIMEOUT_MS.
    // The pinned contract is the recovery shape the live log showed — the tick
    // fails closed with exactly this message, the stalled child does not
    // outlive the refusal, and the very next dispatch is ordinary.
    const docker = new FakeExec();
    docker.enqueue(ok(""));
    const id = await identity();
    const stalled = execMcVia(
      ["python3", "-I", "-c", "import time; time.sleep(30)"],
      id,
      docker.exec,
      runtime(),
      noLiveness,
    );
    const started = Date.now();
    await expect(stalled(["dispatch"])).rejects.toThrow("resident control hello timeout");
    const elapsed = Date.now() - started;
    // 2s hello budget, then up to 1s of exit grace before the kill -9. A
    // refusal much past that means the reap path wedged behind the timeout.
    expect(elapsed).toBeGreaterThanOrEqual(1_900);
    expect(elapsed).toBeLessThan(8_000);
    // No hello means the exchange never reached the Homie inventory: the
    // budgeted docker calls must not have happened.
    expect(docker.calls).toEqual([]);
    // Fail-closed and self-recovering, not a wedge: the same identity's next
    // tick dispatches a well-behaved child cleanly.
    const healthy = execMcVia(
      ["python3", "-I", "-c", helloChild()],
      id,
      docker.exec,
      runtime(),
      noLiveness,
    );
    const result = await healthy(["dispatch"]);
    expect(result.exitCode).toBe(0);
    expect(JSON.parse(result.stdout)).toEqual({
      action: "idle", ack: "hello_ack", inventory_count: 0,
      liveness: '{"op":"pipeline_liveness_result","seq":3,"observed":[]}',
    });
  }, 15_000);

  test("owns every fd 3 before concurrent successful children can race descriptor adoption", async () => {
    await Promise.all(Array.from({ length: 32 }, async () => {
      const docker = new FakeExec();
      docker.enqueue(ok(""));
      const exec = execMcVia(
        ["python3", "-I", "-c", helloChild()],
        await identity(),
        docker.exec,
        runtime(),
        noLiveness,
      );
      const result = await exec(["dispatch"]);
      expect(result.exitCode).toBe(0);
      expect(JSON.parse(result.stdout)).toEqual({
        action: "idle", ack: "hello_ack", inventory_count: 0,
        liveness: '{"op":"pipeline_liveness_result","seq":3,"observed":[]}',
      });
    }));
  });

  test("returns only a normalized exact Homie container identity", async () => {
    const docker = new FakeExec();
    const id = "c".repeat(64);
    docker.enqueue(
      ok(id + "\n"),
      ok(JSON.stringify([{
        Id: id,
        Name: "/mc-homie-h-abc",
        Config: { Labels: {
          "mc-managed": "true",
          "mc-tier": "homie",
          "mc-session-id": "h-abc",
          "mc-launch-id": "a".repeat(16),
        } },
        State: { Status: "running" },
      }])),
    );
    const exec = execMcVia(["python3", "-I", "-c", helloChild()], await identity(), docker.exec, runtime(), noLiveness);
    const result = await exec(["dispatch"]);
    expect(result.exitCode).toBe(0);
    expect(JSON.parse(result.stdout).inventory_count).toBe(1);
    expect(docker.calls[1]).toEqual(["inspect", "--type", "container", id]);
  });

  test("hands the pre-gathered host observation across in exact wire order", async () => {
    const docker = new FakeExec();
    docker.enqueue(ok(""));
    let gathered = 0;
    const exec = execMcVia(
      ["python3", "-I", "-c", helloChild()],
      await identity(),
      docker.exec,
      runtime(),
      async () => {
        gathered++;
        return [
          { launch_age_s: 3600, present: false, run_id: "a".repeat(16) },
          { launch_age_s: 12, present: true, run_id: "b".repeat(16) },
        ];
      },
    );
    const result = await exec(["dispatch"]);
    expect(result.exitCode).toBe(0);
    // Byte-exact, because the Go broker re-marshals this frame and requires
    // bytes.Equal against its own struct: any key reordering — of the frame or
    // of an entry — refuses the whole tick.
    expect(JSON.parse(result.stdout).liveness).toBe(
      '{"op":"pipeline_liveness_result","seq":3,"observed":' +
      `[{"launch_age_s":3600,"present":false,"run_id":"${"a".repeat(16)}"},` +
      `{"launch_age_s":12,"present":true,"run_id":"${"b".repeat(16)}"}]}`,
    );
    // Gathered once per dispatch, before the child ran — never per frame.
    expect(gathered).toBe(1);
  });

  test("a gatherer that throws costs the observation, never the tick", async () => {
    const docker = new FakeExec();
    docker.enqueue(ok(""));
    const exec = execMcVia(
      ["python3", "-I", "-c", helloChild()],
      await identity(),
      docker.exec,
      runtime(),
      async () => {
        throw new Error("docker unreachable");
      },
    );
    const result = await exec(["dispatch"]);
    expect(result.exitCode).toBe(0);
    expect(JSON.parse(result.stdout).liveness).toBe(
      '{"op":"pipeline_liveness_result","seq":3,"observed":[]}',
    );
  });

  test("refuses an ambiguous Homie inventory before accepting child output", async () => {
    const docker = new FakeExec();
    docker.enqueue(ok("short-id\n"));
    const exec = execMcVia(["python3", "-I", "-c", helloChild()], await identity(), docker.exec, runtime(), noLiveness);
    await expect(exec(["dispatch"])).rejects.toThrow("non-canonical container id");
  });

  test("rejects every identity mismatch and names the field that moved", async () => {
    const cases: [string, Record<string, unknown>, string][] = [
      ["release build", { release_build_id: "other" }, "release_build_id other (expected "],
      ["control protocol", { gateway_control_version: GATEWAY_CONTROL_VERSION + 1 },
        `gateway_control_version ${GATEWAY_CONTROL_VERSION + 1} (expected ${GATEWAY_CONTROL_VERSION})`],
      ["spine schema", { spine_schema_id: "incompatible" }, "spine_schema_id incompatible (expected "],
      ["config schema", { config_schema_version: 2 }, "config_schema_version 2 (expected 1)"],
      // The one field reported without its values: which field is wrong IS the
      // diagnosis, and a deployment identifier is not a diagnostic's to print.
      ["deployment", { deployment_uuid: "foreign" }, "deployment_uuid differs"],
    ];
    await Promise.all(cases.map(async ([name, changed, wanted]) => {
      const exec = execMcVia(
        ["python3", "-I", "-c", helloChild(changed, false)],
        await identity(),
        new FakeExec().exec,
        runtime(),
        noLiveness,
      );
      try {
        await exec(["dispatch"]);
        throw new Error(`${name}: mismatch was accepted`);
      } catch (err) {
        const message = String(err);
        expect(`${name}: ${message}`).toContain(`${name}: Error: resident control hello identity mismatch: `);
        expect([name, message.includes(wanted)]).toEqual([name, true]);
        // The uuid case names the field and never the value it was comparing.
        expect([name, message.includes("foreign")]).toEqual([name, false]);
      }
    }));
  });

  test("ordinary mc verbs retain plain stdio and receive no fd 3", async () => {
    const child = `
      const fs = require("node:fs");
      let fd3 = false; try { fd3 = fs.fstatSync(3).isSocket(); } catch {}
      process.stdout.write(JSON.stringify({plain:true, fd3}) + "\\n");
      process.stderr.write("ordinary-stderr\\n");
      process.exitCode = 7;
    `;
    const exec = execMcVia(
      [process.execPath, "-e", child],
      await identity(),
      async () => fail(2),
      runtime(),
      noLiveness,
    );
    const result = await exec(["land", "report"]);
    expect(result.exitCode).toBe(7);
    expect(JSON.parse(result.stdout)).toEqual({ plain: true, fd3: false });
    expect(result.stderr).toBe("ordinary-stderr\n");
  });

  test("abort sends SIGTERM to the native mc process", async () => {
    const home = await identity();
    const ready = join(home.mcHome, "ready");
    const child = `
      const fs = require("node:fs");
      process.on("SIGTERM", () => {
        process.stdout.write(JSON.stringify({ terminated: true }) + "\\n");
        process.exit(143);
      });
      fs.writeFileSync(${JSON.stringify(ready)}, "");
      setInterval(() => {}, 1000);
    `;
    const exec = execMcVia([process.execPath, "-e", child], home, async () => fail(2), runtime(), noLiveness);
    const controller = new AbortController();
    const pending = exec(["__repository", "/tmp/envelope.json"], undefined, { signal: controller.signal });
    for (let attempt = 0; attempt < 200; attempt++) {
      if (await readFile(ready).then(() => true, () => false)) break;
      await Bun.sleep(10);
    }
    controller.abort();
    const result = await pending;
    expect(result.exitCode).toBe(143);
    expect(JSON.parse(result.stdout)).toEqual({ terminated: true });
  });

  test("ordinary private verbs receive exact stdin bytes without exposing them in argv", async () => {
    const child = `
      const fs = require("node:fs");
      const chunks = [];
      process.stdin.on("data", (chunk) => chunks.push(chunk));
      process.stdin.on("end", () => process.stdout.write(JSON.stringify({
        argv: process.argv.slice(1),
        stdin: Buffer.concat(chunks).toString("hex"),
      }) + "\\n"));
    `;
    const exec = execMcVia(
      [process.execPath, "-e", child],
      await identity(),
      async () => fail(2),
      runtime(),
      noLiveness,
    );
    const secretFrame = Uint8Array.from([0, 0, 0, 3, 0x61, 0x62, 0x63]);
    const result = await exec(["__attachment-publication-renew"], secretFrame);
    expect(result.exitCode).toBe(0);
    expect(JSON.parse(result.stdout)).toEqual({
      argv: ["__attachment-publication-renew"],
      stdin: Buffer.from(secretFrame).toString("hex"),
    });
  });
});

describe("Homie runtime inventory attestation", () => {
  const id = "c".repeat(64);
  const plan = {
    session: "h-abc",
    launch: "a".repeat(16),
    envelope_digest: "e".repeat(64),
  };

  function recordFor(runtime_generation: string): Record<string, unknown> {
    const create = [
      "create", "--rm", "--network", "none",
      "--name", "mc-homie-h-abc",
      "--label", "mc-managed=true",
      "--label", "mc-tier=homie",
      "--label", `mc-session-id=${plan.session}`,
      "--label", `mc-launch-id=${plan.launch}`,
      "--label", `mc-envelope-digest=${plan.envelope_digest}`,
      "--label", `mc-runtime-generation=${runtime_generation}`,
      "-v", "/host/session:/mc/session",
      "-e", "MC_SPINE=/mc/spine/spine.db",
      "mc-fake-e2e:test", "bun", "/app/src/homie-runner/main.ts",
    ];
    return homieInspectFromCreate(
      create, id, "mc-fake-e2e:test", ["bun", "/app/src/homie-runner/main.ts"],
    );
  }

  function dockerFor(runtime_generation: string): FakeExec {
    const docker = new FakeExec();
    docker.enqueue(
      ok(id + "\n"),
      ok(JSON.stringify([recordFor(runtime_generation)])),
    );
    return docker;
  }

  test("emits proof only when this resident boot owns the exact bound generation", async () => {
    const live = runtime();
    const runtime_generation = live.register(plan);
    const dockerEnvelope = canonicalHomieDockerEnvelope(recordFor(runtime_generation));
    live.bind({ ...plan, container_id: id, docker_envelope: dockerEnvelope, runtime_generation });
    const attestedDigest = live.sealArtifacts({
      ...plan,
      artifact_envelope: '{"run_json":"exact"}',
      container_id: id,
      runtime_generation,
    });

    expect(await collectHomieInventory(dockerFor(runtime_generation).exec, live)).toEqual([{
      container_id: id,
      launch: plan.launch,
      name: "mc-homie-h-abc",
      runtime_attestation: {
        envelope_digest: attestedDigest,
        runtime_generation,
      },
      session: plan.session,
      state: "created",
    }]);

    expect(await collectHomieInventory(dockerFor(runtime_generation).exec, runtime(9))).toEqual([{
      container_id: id,
      launch: plan.launch,
      name: "mc-homie-h-abc",
      session: plan.session,
      state: "created",
    }]);

    const driftedDocker = new FakeExec();
    const drifted = recordFor(runtime_generation) as any;
    drifted.HostConfig.Memory = 1024;
    driftedDocker.enqueue(ok(id + "\n"), ok(JSON.stringify([drifted])));
    expect(await collectHomieInventory(driftedDocker.exec, live)).toEqual([{
      container_id: id,
      launch: plan.launch,
      name: "mc-homie-h-abc",
      session: plan.session,
      state: "created",
    }]);
  });

  test("expired proxy authority keeps the exact container identity for removal", async () => {
    const live = runtime();
    let authorized = true;
    const runtime_generation = live.register(plan, () => authorized);
    const docker_envelope = canonicalHomieDockerEnvelope(recordFor(runtime_generation));
    live.bind({ ...plan, container_id: id, docker_envelope, runtime_generation });
    live.sealArtifacts({ ...plan, artifact_envelope: '{"run_json":"exact"}', container_id: id, runtime_generation });
    for (const state of ["created", "running"]) {
      const inventory = async () => {
        const docker = new FakeExec();
        const record = recordFor(runtime_generation) as any;
        record.State.Status = state;
        docker.enqueue(ok(id + "\n"), ok(JSON.stringify([record])));
        return collectHomieInventory(docker.exec, live);
      };
      authorized = true;
      expect((await inventory())[0]!.runtime_attestation).toBeDefined();
      authorized = false;
      expect(await inventory()).toEqual([{ container_id: id, launch: plan.launch,
        name: "mc-homie-h-abc", session: plan.session, state }]);
    }
  });
});
