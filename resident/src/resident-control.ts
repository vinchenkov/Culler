import { createHash } from "node:crypto";
import { constants, readFileSync } from "node:fs";
import { open } from "node:fs/promises";
import { spawn } from "node:child_process";
import type { Readable } from "node:stream";
import { join } from "node:path";
import { dlopen, FFIType, ptr } from "bun:ffi";
import type { Exec, ExecResult } from "./types";
import type { CollieRuntimeRegistry } from "./collie-runtime";
import { canonicalCollieDockerEnvelope } from "./collie-envelope";
import type { PipelineLivenessEntry } from "./pipeline-liveness";

export const GATEWAY_CONTROL_VERSION = 4;
export const SPINE_SCHEMA_ID = createHash("sha256")
  .update(readFileSync(join(import.meta.dir, "../../mc/substrate/schema.sql")))
  .update("\n")
  .update(readFileSync(join(import.meta.dir, "../../mc/substrate/delivery.sql")))
  .update("\n")
  .update(readFileSync(join(import.meta.dir, "../../mc/substrate/environments.sql")))
  .digest("hex");
export const CONFIG_SCHEMA_VERSION = 1;
const MAX_FRAME_BYTES = 64 * 1024;
// Budget for the ENTIRE control exchange with a freshly spawned dispatch
// child — hello through pipeline_liveness_result, including the docker
// inventory calls collectCollieInventory makes after hello_ack — not just the
// first frame, despite the name and the error message. The number is a chosen
// bound, not a measurement: no hello round-trip has been timed under idle or
// loaded conditions, so it means only "far above the idle cost of a local
// socketpair exchange". Missing it is fail-closed and self-recovering: the
// tick fails with "resident control hello timeout", the child is reaped
// (1s exit grace, then kill -9), and the next tick retries — observed live
// 2026-08-08..10 as three isolated single-tick failures in three days, each
// followed by a healthy tick. Do not change the number without measuring the
// exchange first; the timeout test pins the refusal message and the recovery
// shape.
const HELLO_TIMEOUT_MS = 2_000;
const socketLibrary = dlopen(
  process.platform === "darwin" ? "/usr/lib/libSystem.B.dylib" : "libc.so.6",
  {
    close: { args: [FFIType.i32], returns: FFIType.i32 },
    fcntl: { args: [FFIType.i32, FFIType.i32, FFIType.i32], returns: FFIType.i32 },
    socketpair: {
      args: [FFIType.i32, FFIType.i32, FFIType.i32, FFIType.ptr],
      returns: FFIType.i32,
    },
  },
);
// Bun 1.3.9 implements connect({fd}) but its bundled declaration omits the
// FdSocketOptions overload even though it declares that option shape.
const connectFD = Bun.connect as unknown as (
  options: Bun.FdSocketOptions<undefined>
) => Promise<Bun.Socket<undefined>>;

export interface ResidentControlIdentity {
  mcHome: string;
  releaseBuildId: string;
  configSchemaVersion: number;
}

interface HelloFrame {
  op: "hello" | "hello_ack";
  seq: number;
  release_build_id: string;
  gateway_control_version: number;
  spine_schema_id: string;
  config_schema_version: number;
  deployment_uuid: string;
}

interface CollieInventoryRequest {
  op: "collie_inventory";
  seq: 2;
}

export interface CollieContainerInventory {
  container_id: string;
  launch: string;
  name: string;
  runtime_attestation?: {
    envelope_digest: string;
    runtime_generation: string;
  };
  session: string;
  state: "created" | "running" | "paused" | "restarting" | "removing" | "exited" | "dead";
}

interface CollieInventoryResponse {
  op: "collie_inventory_result";
  seq: 2;
  containers: CollieContainerInventory[];
}

interface PipelineLivenessRequest {
  op: "pipeline_liveness";
  seq: 3;
}

interface PipelineLivenessResponse {
  op: "pipeline_liveness_result";
  seq: 3;
  observed: PipelineLivenessEntry[];
}

/**
 * Bind one host mc command prefix. Only exact `mc dispatch` receives the
 * resident-owned one-use fd 3; every other verb keeps ordinary stdio and
 * whole-command byte/exit passthrough.
 */
export function execMcVia(
  command: string[],
  identity: ResidentControlIdentity,
  docker: Exec,
  collieRuntime: CollieRuntimeRegistry,
  /** The resident observes each active agent run before the dispatch child
   * starts. An agent run holds the Project lease, or it is the Scribe run of a
   * wiki update. */
  collectLiveness: () => Promise<PipelineLivenessEntry[]>,
): Exec {
  if (command.length === 0) throw new Error("resident: empty mc command");
  return async (argv, stdin, options) => {
    if (argv.length === 1 && argv[0] === "dispatch") {
      if (stdin !== undefined) {
        throw new Error("resident: dispatch does not accept an stdin frame");
      }
      return execControlledDispatch(
        [...command, ...argv], identity, docker, collieRuntime, collectLiveness,
      );
    }
    return execPlain([...command, ...argv], identity, stdin, options?.signal);
  };
}

async function execPlain(
  command: string[],
  identity: ResidentControlIdentity,
  stdin?: Uint8Array,
  signal?: AbortSignal,
): Promise<ExecResult> {
  const child = spawn(command[0]!, command.slice(1), {
    // Explicitly occupy fd 3 with /dev/null: Bun otherwise preserves an
    // unrelated parent descriptor there. Ordinary verbs must never receive
    // a resident control socket.
    stdio: [stdin === undefined ? "ignore" : "pipe", "pipe", "pipe", "ignore"],
    env: mcEnvironment(identity.mcHome),
  });
  const terminate = () => { child.kill("SIGTERM"); };
  signal?.addEventListener("abort", terminate, { once: true });
  try {
    if (signal?.aborted) terminate();
    if (stdin !== undefined) {
      // A child that a stop request ends can close its input first.
      child.stdin!.on("error", () => {});
      child.stdin!.end(stdin);
    }
    const stdout = collect(child.stdout);
    const stderr = collect(child.stderr);
    const exitCode = await childExit(child);
    return { exitCode, stdout: await stdout, stderr: await stderr };
  } finally {
    signal?.removeEventListener("abort", terminate);
  }
}

async function execControlledDispatch(
  command: string[],
  identity: ResidentControlIdentity,
  docker: Exec,
  collieRuntime: CollieRuntimeRegistry,
  collectLiveness: () => Promise<PipelineLivenessEntry[]>,
): Promise<ExecResult> {
  const deploymentUUID = await readDeploymentUUID(identity.mcHome);
  // Gathered HERE — before the child exists, outside the control exchange's
  // 2s budget on both sides. Serial `docker inspect` calls inside that budget
  // would turn every tick into `preflight.frame_invalid`: a total wedge, worse
  // than the lease wedge this closes. A gatherer that throws costs the
  // observation, never the tick.
  let liveness: PipelineLivenessEntry[];
  try {
    liveness = await collectLiveness();
  } catch {
    liveness = [];
  }
  const [parentFD, childFD] = createControlSocketPair();
  let control: BunControlChannel;
  try {
    // Adopt the parent endpoint before the child exists. The endpoint cannot
    // disappear underneath Bun.connect, unlike Bun.spawn's lazy "pipe"
    // descriptor, which was the source of the production ENOENT/EBADF race.
    control = await BunControlChannel.fromFD(parentFD);
  } catch (err) {
    closeRawFDBestEffort(parentFD);
    closeRawFDBestEffort(childFD);
    throw err;
  }
  const child = await (async () => {
    try {
      return Bun.spawn(command, {
        stdio: ["ignore", "pipe", "pipe", childFD] as const,
        env: mcEnvironment(identity.mcHome),
      });
    } catch (err) {
      closeRawFDBestEffort(childFD);
      await closeControl(control);
      throw err;
    }
  })();
  if (socketLibrary.symbols.close(childFD) !== 0) {
    child.kill(9);
    await closeControl(control);
    await child.exited.catch(() => undefined);
    throw new Error("resident: could not close the parent copy of dispatch control fd 3");
  }
  const stdout = new Response(child.stdout).text();
  const stderr = new Response(child.stderr).text();
  const exited = child.exited;
  try {
    await withTimeout(
      serveControl(control, docker, collieRuntime, liveness, {
        op: "hello",
        seq: 1,
        release_build_id: identity.releaseBuildId,
        gateway_control_version: GATEWAY_CONTROL_VERSION,
        spine_schema_id: SPINE_SCHEMA_ID,
        config_schema_version: identity.configSchemaVersion,
        deployment_uuid: deploymentUUID,
      }),
      HELLO_TIMEOUT_MS,
      "resident control hello timeout",
    );
    const exitCode = await exited;
    await closeControl(control);
    return { exitCode, stdout: await stdout, stderr: await stderr };
  } catch (err) {
    await closeControl(control);
    try {
      await withTimeout(exited, 1_000, "dispatch did not exit after control refusal");
    } catch {
      child.kill(9);
      await exited.catch(() => undefined);
    }
    await Promise.allSettled([stdout, stderr]);
    throw err;
  }
}

function mcEnvironment(mcHome: string): Record<string, string | undefined> {
  const env: Record<string, string | undefined> = { ...process.env, MC_HOME: mcHome };
  // A resident invocation is always the Darwin host scope. Ambient developer
  // variables must not turn it into a direct-spine or agent-scoped call and
  // bypass the resident-only dispatch broker.
  delete env.MC_SPINE;
  delete env.MC_RUN_JSON;
  return env;
}

// Report each incompatible field without the deployment identifier.
function helloMismatchDetail(got: HelloFrame, expected: HelloFrame): string {
  const differs: string[] = [];
  if (got.release_build_id !== expected.release_build_id) {
    differs.push(`release_build_id ${got.release_build_id} (expected ${expected.release_build_id})`);
  }
  if (got.gateway_control_version !== expected.gateway_control_version) {
    differs.push(`gateway_control_version ${got.gateway_control_version} (expected ${expected.gateway_control_version})`);
  }
  if (got.spine_schema_id !== expected.spine_schema_id) {
    differs.push(`spine_schema_id ${got.spine_schema_id} (expected ${expected.spine_schema_id})`);
  }
  if (got.config_schema_version !== expected.config_schema_version) {
    differs.push(`config_schema_version ${got.config_schema_version} (expected ${expected.config_schema_version})`);
  }
  if (got.deployment_uuid !== expected.deployment_uuid) {
    differs.push("deployment_uuid differs");
  }
  if (got.op !== expected.op || got.seq !== expected.seq) {
    differs.push(`frame ${got.op}/${got.seq} (expected ${expected.op}/${expected.seq})`);
  }
  // The encoding fence above already proved `got` is the closed canonical shape,
  // so every field is accounted for. An empty list would mean the two frames
  // agree field by field and still stringify differently, which is a fence bug
  // rather than an identity mismatch — say so instead of an empty message.
  return differs.length === 0 ? "the frames agree field by field; the canonical encoding fence disagrees" : differs.join("; ");
}

async function serveControl(
  control: BunControlChannel,
  docker: Exec,
  collieRuntime: CollieRuntimeRegistry,
  liveness: PipelineLivenessEntry[],
  expected: HelloFrame,
): Promise<void> {
  const prefix = await control.readExactly(4);
  const length = prefix.readUInt32BE(0);
  if (length < 1 || length > MAX_FRAME_BYTES) {
    throw new Error(`resident control hello length ${length} is outside 1..65536`);
  }
  const body = await control.readExactly(length);
  let parsed: unknown;
  try {
    parsed = JSON.parse(body.toString("utf8"));
  } catch (err) {
    throw new Error(`resident control hello is not JSON: ${String(err)}`);
  }
  const got = canonicalHello(parsed, "hello");
  if (body.toString("utf8") !== JSON.stringify(got)) {
    throw new Error("resident control hello is not the closed canonical encoding");
  }
  if (JSON.stringify(got) !== JSON.stringify(expected)) {
    throw new Error(`resident control hello identity mismatch: ${helloMismatchDetail(got, expected)}`);
  }
  await control.writeFrame({ ...expected, op: "hello_ack" });

  const requestBody = await readControlBody(control, "Collie inventory request");
  let request: unknown;
  try {
    request = JSON.parse(requestBody.toString("utf8"));
  } catch (err) {
    throw new Error(`resident control Collie inventory request is not JSON: ${String(err)}`);
  }
  const expectedRequest: CollieInventoryRequest = { op: "collie_inventory", seq: 2 };
  if (requestBody.toString("utf8") !== JSON.stringify(expectedRequest) ||
      JSON.stringify(request) !== JSON.stringify(expectedRequest)) {
    throw new Error("resident control Collie inventory request is not the closed canonical frame");
  }
  await control.writeFrame({
    op: "collie_inventory_result",
    seq: 2,
    containers: await collectCollieInventory(docker, collieRuntime),
  } satisfies CollieInventoryResponse);

  const livenessBody = await readControlBody(control, "pipeline liveness request");
  let livenessRequest: unknown;
  try {
    livenessRequest = JSON.parse(livenessBody.toString("utf8"));
  } catch (err) {
    throw new Error(`resident control pipeline liveness request is not JSON: ${String(err)}`);
  }
  const expectedLiveness: PipelineLivenessRequest = { op: "pipeline_liveness", seq: 3 };
  if (livenessBody.toString("utf8") !== JSON.stringify(expectedLiveness) ||
      JSON.stringify(livenessRequest) !== JSON.stringify(expectedLiveness)) {
    throw new Error("resident control pipeline liveness request is not the closed canonical frame");
  }
  // Already measured before the child started; this exchange only hands it
  // over. Key order is the wire order — both hops re-marshal and byte-compare
  // against the Go struct's field-declaration order.
  await control.writeFrame({
    op: "pipeline_liveness_result",
    seq: 3,
    observed: liveness,
  } satisfies PipelineLivenessResponse);
}

async function readControlBody(control: BunControlChannel, describe: string): Promise<Buffer> {
  const prefix = await control.readExactly(4);
  const length = prefix.readUInt32BE(0);
  if (length < 1 || length > MAX_FRAME_BYTES) {
    throw new Error(`resident control ${describe} length ${length} is outside 1..65536`);
  }
  return control.readExactly(length);
}

/** Read only the label-scoped Collie set and collapse Docker's wide inspect
 * record into the exact identity/state carrier the helper is allowed to see. */
export async function collectCollieInventory(
  docker: Exec,
  collieRuntime: CollieRuntimeRegistry,
): Promise<CollieContainerInventory[]> {
  const listed = await docker([
    "ps", "-aq", "--no-trunc",
    "--filter", "label=mc-managed=true",
    "--filter", "label=mc-tier=collie",
  ]);
  if (listed.exitCode !== 0) {
    throw new Error(`Collie inventory list failed (exit ${listed.exitCode}): ${listed.stderr.trim()}`);
  }
  const ids = listed.stdout.split("\n").filter((id) => id !== "");
  if (ids.length > 128 || ids.some((id) => !/^[0-9a-f]{64}$/.test(id)) ||
      new Set(ids).size !== ids.length) {
    throw new Error("Collie inventory returned a duplicate, oversized, or non-canonical container id set");
  }
  if (ids.length === 0) return [];
  ids.sort();
  const inspected = await docker(["inspect", "--type", "container", ...ids]);
  if (inspected.exitCode !== 0) {
    throw new Error(`Collie inventory inspect failed (exit ${inspected.exitCode}): ${inspected.stderr.trim()}`);
  }
  let records: unknown;
  try {
    records = JSON.parse(inspected.stdout);
  } catch {
    throw new Error("Collie inventory inspect returned invalid JSON");
  }
  if (!Array.isArray(records) || records.length !== ids.length) {
    throw new Error("Collie inventory inspect disagrees with the listed identity set");
  }
  const allowedStates = new Set<CollieContainerInventory["state"]>([
    "created", "running", "paused", "restarting", "removing", "exited", "dead",
  ]);
  const out = records.map((value): CollieContainerInventory => {
    if (value === null || typeof value !== "object" || Array.isArray(value)) {
      throw new Error("Collie inventory inspect returned a non-object record");
    }
    const record = value as {
      Id?: unknown;
      Name?: unknown;
      Config?: { Labels?: unknown };
      State?: { Status?: unknown };
    };
    const labels = record.Config?.Labels;
    if (labels === null || typeof labels !== "object" || Array.isArray(labels)) {
      throw new Error("Collie inventory record carries no exact label set");
    }
    const labelMap = labels as Record<string, unknown>;
    const mcLabels = Object.keys(labelMap).filter((key) => key.startsWith("mc-")).sort();
    const legacyLabels = [
      "mc-launch-id", "mc-managed", "mc-session-id", "mc-tier",
    ];
    const attestedLabels = [
      "mc-envelope-digest", "mc-launch-id", "mc-managed",
      "mc-runtime-generation", "mc-session-id", "mc-tier",
    ];
    if (JSON.stringify(mcLabels) !== JSON.stringify(legacyLabels) &&
        JSON.stringify(mcLabels) !== JSON.stringify(attestedLabels)) {
      throw new Error("Collie inventory record carries an unexpected Culler label set");
    }
    const id = record.Id;
    const session = labelMap["mc-session-id"];
    const launch = labelMap["mc-launch-id"];
    const state = record.State?.Status;
    if (typeof id !== "string" || !ids.includes(id) ||
        typeof session !== "string" || !/^h-[a-z0-9-]+$/.test(session) ||
        typeof launch !== "string" || !/^[0-9a-f]{16}$/.test(launch) ||
        labelMap["mc-managed"] !== "true" || labelMap["mc-tier"] !== "collie" ||
        record.Name !== `/mc-collie-${session}` ||
        typeof state !== "string" || !allowedStates.has(state as CollieContainerInventory["state"])) {
      throw new Error("Collie inventory record identity is malformed or ambiguous");
    }
    const envelopeDigest = labelMap["mc-envelope-digest"];
    const runtimeGeneration = labelMap["mc-runtime-generation"];
    let dockerEnvelope: string | undefined;
    try {
      dockerEnvelope = canonicalCollieDockerEnvelope(value);
    } catch {
      // Physical identity must still cross for exact-id cleanup. An incomplete
      // or unknown inspect shape simply cannot carry adoption proof.
    }
    const runtimeAttestation = (
      typeof envelopeDigest === "string" &&
      typeof runtimeGeneration === "string" &&
      dockerEnvelope !== undefined
        ? collieRuntime.attestEnvelope({
        container_id: id,
        docker_envelope: dockerEnvelope,
        envelope_digest: envelopeDigest,
        launch,
        runtime_generation: runtimeGeneration,
          session,
        })
        : undefined
    ) ?? undefined;
    return {
      container_id: id,
      launch,
      name: record.Name.slice(1),
      ...(runtimeAttestation === undefined ? {} : { runtime_attestation: runtimeAttestation }),
      session,
      state: state as CollieContainerInventory["state"],
    };
  });
  out.sort((a, b) => a.container_id.localeCompare(b.container_id));
  if (out.some((record, index) => record.container_id !== ids[index])) {
    throw new Error("Collie inventory inspect substituted a listed container identity");
  }
  return out;
}

function canonicalHello(value: unknown, op: HelloFrame["op"]): HelloFrame {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("resident control hello must be an object");
  }
  const v = value as Record<string, unknown>;
  const frame: HelloFrame = {
    op: stringField(v, "op") as HelloFrame["op"],
    seq: integerField(v, "seq"),
    release_build_id: stringField(v, "release_build_id"),
    gateway_control_version: integerField(v, "gateway_control_version"),
    spine_schema_id: stringField(v, "spine_schema_id"),
    config_schema_version: integerField(v, "config_schema_version"),
    deployment_uuid: stringField(v, "deployment_uuid"),
  };
  if (frame.op !== op || frame.seq !== 1) {
    throw new Error(`resident control expected ${op} sequence 1`);
  }
  return frame;
}

function stringField(v: Record<string, unknown>, key: string): string {
  const value = v[key];
  if (typeof value !== "string" || value.length === 0 || Buffer.byteLength(value) > 4096) {
    throw new Error(`resident control ${key} must be a bounded non-empty string`);
  }
  return value;
}

function integerField(v: Record<string, unknown>, key: string): number {
  const value = v[key];
  if (!Number.isSafeInteger(value)) {
    throw new Error(`resident control ${key} must be an integer`);
  }
  return value as number;
}

async function readDeploymentUUID(mcHome: string): Promise<string> {
  const path = join(mcHome, "deployment.uuid");
  const handle = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const stat = await handle.stat();
    if (!stat.isFile() || stat.size < 1 || stat.size > 4096) {
      throw new Error(`resident: deployment identity mirror ${JSON.stringify(path)} must be a bounded regular file`);
    }
    const deploymentUUID = (await handle.readFile("utf8")).trim();
    if (deploymentUUID.length === 0) {
      throw new Error(`resident: deployment identity mirror ${JSON.stringify(path)} is empty`);
    }
    return deploymentUUID;
  } finally {
    await handle.close();
  }
}

function createControlSocketPair(): [number, number] {
  const fds = new Int32Array(2);
  // AF_UNIX=1 and SOCK_STREAM=1 on both supported host kernels (Darwin/Linux).
  if (socketLibrary.symbols.socketpair(1, 1, 0, ptr(fds)) !== 0 ||
      fds[0]! < 0 || fds[1]! < 0 || fds[0]! === fds[1]!) {
    throw new Error("resident: kernel socketpair creation failed");
  }
  // F_SETFD=2 and FD_CLOEXEC=1 on Darwin/Linux. Bun's explicit stdio mapping
  // duplicates childFD onto descriptor 3 (clearing the flag on that target);
  // every original endpoint stays closed across exec, so the child receives
  // exactly one control descriptor and cannot keep the resident endpoint live.
  if (socketLibrary.symbols.fcntl(fds[0]!, 2, 1) !== 0 ||
      socketLibrary.symbols.fcntl(fds[1]!, 2, 1) !== 0) {
    closeRawFDBestEffort(fds[0]!);
    closeRawFDBestEffort(fds[1]!);
    throw new Error("resident: could not close-on-exec the dispatch control socketpair");
  }
  return [fds[0]!, fds[1]!];
}

function closeRawFDBestEffort(fd: number): void {
  socketLibrary.symbols.close(fd);
}

class BunControlChannel {
  private socket?: Bun.Socket<undefined>;
  private buffered = Buffer.alloc(0);
  private ended = false;
  private closed = false;
  private failure?: Error;
  private waiters: (() => void)[] = [];
  private closeWaiters: (() => void)[] = [];

  static async fromFD(fd: number): Promise<BunControlChannel> {
    const channel = new BunControlChannel();
    await connectFD({
      fd,
      socket: {
        open(socket) {
          channel.socket = socket;
        },
        data(_socket, data) {
          channel.buffered = Buffer.concat([channel.buffered, Buffer.from(data)]);
          channel.wake();
        },
        end() {
          channel.ended = true;
          channel.wake();
        },
        close(_socket, error) {
          channel.ended = true;
          channel.closed = true;
          if (error) channel.failure = error;
          channel.wake();
          for (const resolve of channel.closeWaiters.splice(0)) resolve();
        },
        error(_socket, error) {
          channel.failure = error;
          channel.wake();
        },
      },
    });
    if (!channel.socket) throw new Error("resident: could not bind dispatch control fd 3");
    return channel;
  }

  async readExactly(length: number): Promise<Buffer> {
    while (this.buffered.length < length) {
      if (this.failure) throw this.failure;
      if (this.ended) throw new Error("resident control early EOF");
      await new Promise<void>((resolve) => this.waiters.push(resolve));
    }
    const result = this.buffered.subarray(0, length);
    this.buffered = this.buffered.subarray(length);
    return result;
  }

  async writeFrame(value: unknown): Promise<void> {
    const body = Buffer.from(JSON.stringify(value));
    if (body.length < 1 || body.length > MAX_FRAME_BYTES) {
      throw new Error(`resident control frame length ${body.length} exceeds the 64-KiB bound`);
    }
    const prefix = Buffer.allocUnsafe(4);
    prefix.writeUInt32BE(body.length);
    const frame = Buffer.concat([prefix, body]);
    if (!this.socket || this.socket.write(frame) !== frame.length) {
      throw new Error("resident control frame write did not make bounded progress");
    }
  }

  async close(): Promise<void> {
    if (this.closed || !this.socket) return;
    const closed = new Promise<void>((resolve) => this.closeWaiters.push(resolve));
    this.socket.close();
    await closed;
  }

  private wake(): void {
    for (const resolve of this.waiters.splice(0)) resolve();
  }
}

async function collect(stream: Readable | null): Promise<string> {
  if (!stream) return "";
  const chunks: Buffer[] = [];
  for await (const chunk of stream) chunks.push(Buffer.from(chunk));
  return Buffer.concat(chunks).toString("utf8");
}

function childExit(child: ReturnType<typeof spawn>): Promise<number> {
  return new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("close", (code, signal) => resolve(code ?? (signal ? 128 : 2)));
  });
}

async function closeControl(control: BunControlChannel): Promise<void> {
  await control.close();
}

async function withTimeout<T>(promise: Promise<T>, ms: number, message: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error(message)), ms);
      }),
    ]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}
