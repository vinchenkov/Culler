// test-helpers.ts — the fake timer, the fake scheduler and follow seams of
// the repository lane, fake mc/docker/fs, and a deps factory.
// Tests using this complete fake rig touch no real filesystem or Docker. The
// split-brain acceptance suite overrides the injectable mc/fs seams with the
// real CLI and temp host state, but keeps this timer and fake Docker.

import { makeLogger, type LogRecord, type Logger } from "../../runner/shared/log";
import type {
  AttachmentCanaryReceipt,
  ExecResult,
  ResidentConfig,
  RuntimeBinding,
  TickDeps,
} from "./types";
import { CollieRuntimeRegistry } from "./collie-runtime";
import { AttachmentConfinementHealth } from "./attachment-health";

export function greenAttachmentCanaryReceipt(
  generation = "1".repeat(32),
): AttachmentCanaryReceipt {
  return {
    version: 1,
    generation,
    inbound_sha256: "2".repeat(64),
    outbound_sha256: "3".repeat(64),
    real_uid: 10002,
    effective_uid: 10001,
    saved_uid: 10001,
    fs_uid: 10001,
    warm_inbound_read: true,
    host_outbound_read: true,
    private_gate_healthy: true,
    denied_outbound: [
      "create:EACCES",
      "open:EACCES",
      "rename:EACCES",
      "traverse:EACCES",
      "unlink:EACCES",
    ],
    restart_inbound_read: true,
    restart_outbound_read: true,
    outside_sessions: true,
  };
}

/** One resolved runtime capability, exactly as the dispatch tick carries it.
 * Tests state only the identity that matters and inherit an explicit empty
 * value for every other declared field, so a fixture can never accidentally
 * prove that a PARTIAL carrier launches. The credential source id defaults to
 * the binding id, the same default onboarding applies; the fake family
 * carries none. */
export function runtimeBinding(
  harness: string,
  id: string,
  overrides: Partial<RuntimeBinding> = {},
): RuntimeBinding {
  return {
    base_url: "",
    channel: harness === "codex" ? "codex" : harness === "claude-sdk" ? "claude" : harness === "opencode" ? "opencode" : "",
    credential: harness === "fake" && id === "fake" ? "" : id,
    harness,
    id,
    model: "",
    ...overrides,
  };
}

/** setInterval-shaped fake: the test fires ticks by hand. */
export class FakeTimer {
  fn: (() => unknown) | null = null;
  intervalMs: number | null = null;
  cleared = false;

  setTimer = (fn: () => unknown, ms: number): unknown => {
    this.fn = fn;
    this.intervalMs = ms;
    return "the-timer";
  };

  clearTimer = (handle: unknown): void => {
    if (handle === "the-timer") this.cleared = true;
  };

  /** Simulate one timer firing (fires even mid-tick, like setInterval). */
  fire(): Promise<void> | void {
    if (this.cleared || !this.fn) return;
    return this.fn() as Promise<void> | void;
  }
}

/** Waits until every pending promise chain of the fakes settles. The fakes
 * answer through promises only, so a few macrotask turns drain them. */
export async function settle(turns = 10): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((resolve) => setImmediate(resolve));
}

/** A one-shot timer fake with several timers and a manual clock. `advance`
 * runs each due timer in time order and lets its work settle. */
export class FakeScheduler {
  now = 0;
  private nextHandle = 1;
  private timers = new Map<number, { at: number; fn: () => void }>();

  schedule = (fn: () => void, ms: number): unknown => {
    const handle = this.nextHandle++;
    this.timers.set(handle, { at: this.now + Math.max(0, ms), fn });
    return handle;
  };

  cancel = (handle: unknown): void => {
    this.timers.delete(handle as number);
  };

  clock = (): number => this.now;

  /** The delay of each pending timer from now, in schedule order. */
  pending(): number[] {
    return [...this.timers.values()].map((timer) => timer.at - this.now);
  }

  async advance(ms: number): Promise<void> {
    const target = this.now + ms;
    await settle();
    for (;;) {
      let due: [number, { at: number; fn: () => void }] | undefined;
      for (const entry of this.timers) {
        if (entry[1].at <= target && (due === undefined || entry[1].at < due[1].at)) due = entry;
      }
      if (due === undefined) break;
      this.timers.delete(due[0]);
      this.now = due[1].at;
      due[1].fn();
      await settle();
    }
    this.now = target;
    await settle();
  }
}

/** A fake of the lane follow seam. The test emits lines and ends followers. */
export class FakeFollow {
  followers: Array<{
    argv: string[];
    onLine: (line: string) => void;
    stopped: boolean;
    end(code: number): void;
  }> = [];

  follow = (argv: string[], onLine: (line: string) => void): { stop(): void; done: Promise<number> } => {
    let end!: (code: number) => void;
    const done = new Promise<number>((resolve) => { end = resolve; });
    const follower = { argv, onLine, stopped: false, end };
    this.followers.push(follower);
    return {
      stop: () => {
        follower.stopped = true;
        end(143);
      },
      done,
    };
  };

  /** Each follower whose argv starts with `head`. */
  matching(...head: string[]) {
    return this.followers.filter((follower) => head.every((value, index) => follower.argv[index] === value));
  }
}

/** Scripted fake exec: answers from a queue, records every argv. */
export class FakeExec {
  calls: string[][] = [];
  /** The stdin bytes of each call, as text. */
  inputs: Array<string | undefined> = [];
  /** The abort signal of each call. */
  signals: Array<AbortSignal | undefined> = [];
  private queue: Array<
    ExecResult |
    Promise<ExecResult> |
    ((argv: string[], calls: string[][]) => ExecResult | Promise<ExecResult>)
  > = [];
  /** Fallback when the queue is empty. */
  fallback: ExecResult = ok("{}");

  enqueue(...results: Array<
    ExecResult |
    Promise<ExecResult> |
    ((argv: string[], calls: string[][]) => ExecResult | Promise<ExecResult>)
  >): void {
    this.queue.push(...results);
  }

  exec = async (argv: string[], stdin?: Uint8Array, options?: { signal?: AbortSignal }): Promise<ExecResult> => {
    this.calls.push(argv);
    this.inputs.push(stdin === undefined ? undefined : new TextDecoder().decode(stdin));
    this.signals.push(options?.signal);
    if (this.queue.length === 0) return this.fallback;
    const scripted = this.queue.shift()!;
    return typeof scripted === "function" ? scripted(argv, this.calls) : scripted;
  };
}

/** Build Docker's immutable inspect projection from the exact fake create
 * argv. This keeps Collie tests coupled to the same create plan production
 * validates, without hand-copying credentials or mount lists into fixtures. */
export function collieInspectFromCreate(
  create: string[],
  containerId: string,
  image: string,
  cmd: string[],
  state: string = "created",
): Record<string, unknown> {
  const values = (flag: string): string[] => create.flatMap((arg, index) =>
    arg === flag && create[index + 1] !== undefined ? [create[index + 1]!] : []);
  const one = (flag: string): string => {
    const found = values(flag);
    if (found.length !== 1) throw new Error(`fake Collie create needs exactly one ${flag}`);
    return found[0]!;
  };
  const binds = values("-v");
  const mounts = values("--mount").map((value) => {
    const fields = value.split(",");
    return {
      Type: "volume",
      Source: fields.find((field) => field.startsWith("src="))!.slice(4),
      Target: fields.find((field) => field.startsWith("dst="))!.slice(4),
      ReadOnly: fields.includes("readonly"),
      VolumeOptions: { NoCopy: fields.includes("volume-nocopy") },
    };
  });
  const tmpfs = Object.fromEntries(values("--tmpfs").map((value) => {
    const separator = value.indexOf(":");
    return [value.slice(0, separator), value.slice(separator + 1)];
  }));
  const labels = Object.fromEntries(values("--label").map((entry) => {
    const split = entry.indexOf("=");
    if (split < 1) throw new Error("fake Collie label is malformed");
    return [entry.slice(0, split), entry.slice(split + 1)];
  }));
  const network = values("--network")[0] ?? "default";
  const security = values("--security-opt");
  // Docker reports a byte count for a `<n>m` flag and null for an absent
  // pids limit.
  const megabytes = (flag: string): number => {
    const value = values(flag)[0];
    if (value === undefined) return 0;
    if (!/^[0-9]+m$/.test(value)) throw new Error(`fake Collie ${flag} needs a <n>m value`);
    return Number(value.slice(0, -1)) * 1024 * 1024;
  };
  const pidsLimit = values("--pids-limit")[0];
  const entrypoint = values("--entrypoint")[0];
  const actualCmd = entrypoint ? cmd.slice(1) : cmd;
  return {
    Args: entrypoint ? actualCmd : cmd.slice(1),
    Config: {
      Cmd: actualCmd,
      Entrypoint: entrypoint ? [entrypoint] : null,
      Env: ["PATH=/usr/bin", ...values("-e")],
      Image: image,
      Labels: labels,
      OpenStdin: false,
      Tty: false,
      User: values("--user")[0] ?? "",
      WorkingDir: "",
    },
    HostConfig: {
      AutoRemove: create.includes("--rm"),
      Binds: binds,
      Mounts: mounts,
      Tmpfs: tmpfs,
      CapAdd: null,
      CapDrop: values("--cap-drop").length === 0 ? null : values("--cap-drop"),
      CgroupnsMode: "private",
      Dns: [],
      DnsOptions: [],
      DnsSearch: [],
      IpcMode: "private",
      Memory: megabytes("--memory"),
      MemorySwap: megabytes("--memory-swap"),
      NanoCpus: 0,
      NetworkMode: network,
      PidMode: "",
      PidsLimit: pidsLimit === undefined ? null : Number(pidsLimit),
      Privileged: false,
      ReadonlyRootfs: false,
      RestartPolicy: { MaximumRetryCount: 0, Name: "no" },
      SecurityOpt: security,
      UTSMode: "",
    },
    Id: containerId,
    Image: `sha256:${"1".repeat(64)}`,
    Mounts: [...binds.map((bind) => {
      const [source, destination, access] = bind.split(":");
      const volume = !source!.startsWith("/");
      return {
        Destination: destination,
        Driver: volume ? "local" : "",
        Mode: "",
        Name: volume ? source : "",
        Propagation: "rprivate",
        RW: access !== "ro",
        Source: volume ? `/var/lib/docker/volumes/${source}/_data` : source,
        Type: volume ? "volume" : "bind",
      };
    }), ...mounts.map((mount) => ({
      Destination: mount.Target,
      Driver: "local",
      Mode: "z",
      Name: mount.Source,
      Propagation: "",
      RW: !mount.ReadOnly,
      Source: `/var/lib/docker/volumes/${mount.Source}/_data`,
      Type: "volume",
    }))],
    Name: `/${one("--name")}`,
    Path: entrypoint ?? cmd[0],
    State: { Status: state },
  };
}

/** In-memory fs recorder; `events` interleaves mkdir/write for order asserts. */
export class FakeFs {
  events: string[] = [];
  writes = new Map<string, string>();
  /** Paths whose `rm` rejects, and the error each rejects with. Lets a test
   * drive the filesystem refusals the host actually produces (EACCES on a
   * container-owned mountpoint) rather than only the happy path. */
  rmFailures = new Map<string, Error>();

  /** How many rejections are left for a path whose failure is transient.
   * Absent means the entry in `rmFailures` fails forever. */
  rmFailuresLeft = new Map<string, number>();

  /** Counts every `rm` attempt per path, so a test can assert a retry budget
   * was actually spent rather than inferring it from the outcome. */
  rmAttempts = new Map<string, number>();

  /** Makes `rm` on `path` reject, e.g. `failRm(p, "EACCES: permission denied")`. */
  failRm(path: string, message: string): this {
    this.rmFailures.set(path, new Error(message));
    return this;
  }

  /** Makes `rm` on `path` reject `times` times and then succeed — the live
   * shape of the projection EACCES, which was confirmed transient by rerunning
   * the identical `fs.rm` call by hand and watching it succeed. */
  failRmTimes(path: string, message: string, times: number): this {
    this.failRm(path, message);
    this.rmFailuresLeft.set(path, times);
    return this;
  }

  /** The mode each mkdir asked for, when it asked for one. Kept out of
   * `events` so the order assertions stay readable, and recorded at all
   * because exactly one directory's mode is load-bearing: the per-run log leaf
   * a sealed run's non-root container has to write into. */
  mkdirModes = new Map<string, number>();

  /** Directories `readdir` knows, beyond those implied by `writes`. A test
   * seeds the operator skill store with `dir()`. */
  dirs = new Set<string>();

  /** Declares `path` an existing directory (and every ancestor of it). */
  dir(path: string): this {
    const parts = path.split("/");
    for (let i = 2; i <= parts.length; i++) this.dirs.add(parts.slice(0, i).join("/"));
    return this;
  }

  fs = {
    rename: async (source: string, destination: string): Promise<void> => {
      this.writes.set(destination, this.writes.get(source)!);
      this.writes.delete(source);
    },
    mkdir: async (path: string, opts?: { exclusive?: boolean; mode?: number }): Promise<void> => {
      this.events.push(`mkdir:${path}`);
      if (opts?.mode !== undefined) this.mkdirModes.set(path, opts.mode);
      this.dir(path);
    },
    /** Lists the immediate children implied by `dirs` and `writes`; an absent
     * directory rejects with an ENOENT-coded error, as node does. */
    readdir: async (path: string): Promise<string[]> => {
      const known = this.dirs.has(path) ||
        [...this.writes.keys()].some((p) => p.startsWith(`${path}/`));
      if (!known) {
        throw Object.assign(new Error(`ENOENT: no such file or directory, scandir '${path}'`), { code: "ENOENT" });
      }
      const names = new Set<string>();
      for (const p of [...this.dirs, ...this.writes.keys()]) {
        if (!p.startsWith(`${path}/`)) continue;
        const first = p.slice(path.length + 1).split("/")[0];
        if (first !== undefined) names.add(first);
      }
      return [...names].sort();
    },
    writeFile: async (path: string, data: string, _opts?: { mode?: number }): Promise<void> => {
      this.events.push(`write:${path}`);
      this.writes.set(path, data);
    },
    readFile: async (path: string): Promise<string> => {
      const value = this.writes.get(path);
      if (value === undefined) throw Object.assign(new Error(`fake file absent: ${path}`), { code: "ENOENT" });
      return value;
    },
    rm: async (path: string): Promise<void> => {
      this.events.push(`rm:${path}`);
      this.rmAttempts.set(path, (this.rmAttempts.get(path) ?? 0) + 1);
      const failure = this.rmFailures.get(path);
      if (failure !== undefined) {
        const left = this.rmFailuresLeft.get(path);
        if (left === undefined) {
          throw failure; // permanent
        }
        if (left > 0) {
          this.rmFailuresLeft.set(path, left - 1);
          throw failure;
        }
      }
      this.writes.delete(path);
    },
  };
}

/** A logger for a fixture that asserts on PROSE. Every record's `msg` — and
 * nothing else — lands in the caller's array, so an assertion written against
 * the free-text seam keeps meaning what it meant while the module it exercises
 * moves to typed events. A fixture that wants the structure builds its own
 * sink, or uses `makeTestLogger` from the shared module. */
export function msgLogger(msgs: string[], level: "error" | "warn" | "info" | "debug" = "debug"): Logger {
  return makeLogger({
    comp: "resident",
    level,
    sink: {
      write(line) {
        msgs.push((JSON.parse(line) as LogRecord).msg);
      },
    },
  });
}

/** The inspect output of one present pipeline container of run `runId`. */
export function presentChild(runId: string, component: string, status = "exited"): string {
  return JSON.stringify([{
    Id: "a".repeat(64),
    Name: `/${component}-${runId}`,
    Config: { Labels: { "mc-managed": "true", "mc-run-id": runId, "mc-tier": "pipeline" } },
    State: { Status: status },
  }]);
}

export function ok(stdout: string): ExecResult {
  return { exitCode: 0, stdout, stderr: "" };
}

export function fail(exitCode: number, stderr = ""): ExecResult {
  return { exitCode, stdout: "", stderr };
}

export function effectJson(effect: unknown): ExecResult {
  return ok(JSON.stringify(effect) + "\n");
}

export const testConfig: ResidentConfig = {
  mcHome: "/tmp/mc-home",
  agentsViewBind: "127.0.0.1:7334",
  image: "mc-fake-e2e:test",
  agentCmd: ["bun", "/app/src/agent-runner/main.ts"],
  landCmd: ["mc-land"],
  behaviorsDir: "/host/behaviors",
  runnerSrcDir: "/host/runner",
  workspaceRoot: "/host/workspace",
  spineVolume: "mc-spine-test",
  spineDbPath: "/mc/spine/spine.db",
  gitAuthorName: "Culler",
  gitAuthorEmail: "mc@localhost",
  roleBehaviors: {
    arbiter: "/mc/behaviors/arbiter.json",
    scribe: "/mc/behaviors/scribe.json",
    worker: "/mc/behaviors/worker.json",
    verifier: "/mc/behaviors/verifier.json",
    packager: "/mc/behaviors/packager.json",
    strategist: "/mc/behaviors/strategist.json",
    collie: "/mc/behaviors/collie.json",
  },
};

export interface TestRig {
  deps: TickDeps;
  /** The agent container limits that `mc config show` reports. */
  limits: { cpus: number; memory_mb: number };
  timer: FakeTimer;
  mc: FakeExec;
  docker: FakeExec;
  fakeFs: FakeFs;
  logs: string[];
  logRecords: LogRecord[];
  /** Every `deps.sleep` delay, in order. Lets a test assert that a bounded
   * retry actually waited without the suite paying the wall clock. */
  sleeps: number[];
}

/** The `mc config show` answer with the given agent container limits. */
export function agentLimitsConfig(limits: { cpus: number; memory_mb: number }): ExecResult {
  return ok(JSON.stringify({ families: [{ family: "container", knobs: [
    { key: "container.cpu_limit", value: limits.cpus },
    { key: "container.memory_limit_mb", value: limits.memory_mb },
  ] }] }));
}

export function makeRig(overrides: Partial<TickDeps> = {}): TestRig {
  const timer = new FakeTimer();
  const mc = new FakeExec();
  const docker = new FakeExec();
  const fakeFs = new FakeFs();
  const logs: string[] = [];
  const logRecords: LogRecord[] = [];
  const sleeps: number[] = [];
  const rigLimits = { cpus: 0, memory_mb: 0 };
  const configShow = () => agentLimitsConfig(rigLimits);
  const attachmentConfinement = new AttachmentConfinementHealth();
  attachmentConfinement.markHealthy(greenAttachmentCanaryReceipt());
  const deps: TickDeps = {
    intervalMs: 500,
    setTimer: timer.setTimer,
    clearTimer: timer.clearTimer,
    runMc: async (argv, stdin, options) => {
      // Container starts read the agent container limits. Tests that script
      // mc calls in order do not script this read.
      if (argv[0] === "config" && argv[1] === "show" && argv.length === 2) return configShow();
      const result = await mc.exec(argv, stdin, options);
      if (result.exitCode === 0 && argv[0] === "run" && argv[1] === "launch-check") {
        return { ...result, stdout: JSON.stringify({ ...JSON.parse(result.stdout || "{}"), expires_at: new Date(Date.now() + 3_600_000).toISOString() }) };
      }
      return result;
    },
    readWorkspaces: async () => deps.config.workspaceRoots ?? [{ id: "default", root: deps.config.workspaceRoot }],
    docker: docker.exec,
    // Recorded, never awaited for real: a unit test must be able to spend a
    // 30-attempt retry budget without spending 15 seconds.
    sleep: async (ms) => { sleeps.push(ms); },
    // The rig keeps BOTH shapes, split by what a test is actually asking.
    // `logs` is the prose only — exactly the string the call site passed —
    // so every assertion written against the old free-text seam keeps meaning
    // what it meant, whether it greps with `.includes` or matches an element
    // outright. Carrying the rendered line here instead would break the exact
    // matches for a decoration no test asked about.
    // `logRecords` is where the structure went, and is what a NEW assertion
    // should reach for: it can name the event and the run_id rather than
    // grepping prose that the migration is going to reword anyway.
    log: makeLogger({
      comp: "resident",
      level: "debug",
      sink: {
        write(line) {
          const record = JSON.parse(line) as LogRecord;
          logRecords.push(record);
          logs.push(record.msg);
        },
      },
    }),
    collieRuntime: new CollieRuntimeRegistry(Buffer.alloc(32, 7)),
    fs: fakeFs.fs,
    attachmentConfinement,
    prepareAttachmentTree: async (session) => ({
      inbound_published: `${testConfig.mcHome}/attachments/${session}/in/published`,
      outbound: `${testConfig.mcHome}/attachments/${session}/out`,
      session,
    }),
    // No zone by default, so an argv assertion written before TZ rode along
    // keeps its exact shape; a test that wants the zone names one.
    hostZone: () => undefined,
    config: testConfig,
    ...overrides,
  };
  return { deps, timer, mc, docker, fakeFs, logs, logRecords, sleeps, limits: rigLimits };
}

/** A promise plus its resolver, for holding a tick open mid-test. */
export function deferred<T>(): { promise: Promise<T>; resolve: (v: T) => void } {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}
