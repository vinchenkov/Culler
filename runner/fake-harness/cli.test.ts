// cli.test.ts — end-to-end tests of the fake harness CLI, run as a real
// child process (Bun.spawn) against temp session dirs. Covers the adapter
// contract in README.md: golden byte-deterministic native.jsonl, succeed /
// crash / hang exit semantics, exec tool-use recording with 8 KiB
// truncation, stdin vs --turn-file, and the exit-2 usage/environment
// failures.

import { afterEach, describe, expect, test } from "bun:test";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const CLI = join(import.meta.dir, "cli.ts");

const FIXED_TS = "2026-01-01T00:00:00.000Z";

const madeDirs: string[] = [];
afterEach(() => {
  while (madeDirs.length > 0) rmSync(madeDirs.pop()!, { recursive: true, force: true });
});

function tempDir(): string {
  const d = mkdtempSync(join(tmpdir(), "fake-harness-test-"));
  madeDirs.push(d);
  return d;
}

interface Fixture {
  dir: string;
  sessionDir: string;
  behaviorPath: string;
}

function fixture(behavior: unknown): Fixture {
  const dir = tempDir();
  const sessionDir = join(dir, "session");
  Bun.spawnSync(["mkdir", "-p", sessionDir], { env: process.env });
  const behaviorPath = join(dir, "behavior.json");
  writeFileSync(behaviorPath, typeof behavior === "string" ? behavior : JSON.stringify(behavior));
  return { dir, sessionDir, behaviorPath };
}

interface RunResult {
  exitCode: number;
  stdout: string;
  stderr: string;
  events: Record<string, unknown>[];
  native: string;
  nativeEvents: Record<string, unknown>[];
}

async function runCli(
  fx: Fixture,
  opts: { extraArgs?: string[]; stdin?: string; omitSessionDir?: boolean } = {},
): Promise<RunResult> {
  const argv = [process.execPath, CLI, "--behavior", fx.behaviorPath];
  if (!opts.omitSessionDir) argv.push("--session-dir", fx.sessionDir);
  argv.push(...(opts.extraArgs ?? []));
  const proc = Bun.spawn(argv, {
    env: process.env,
    stdin: Buffer.from(opts.stdin ?? ""),
    stdout: "pipe",
    stderr: "pipe",
  });
  const [exitCode, stdout, stderr] = await Promise.all([
    proc.exited,
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
  ]);
  const parse = (text: string) =>
    text
      .split("\n")
      .filter((l) => l.length > 0)
      .map((l) => JSON.parse(l) as Record<string, unknown>);
  const nativePath = join(fx.sessionDir, "native.jsonl");
  const native = existsSync(nativePath) ? readFileSync(nativePath, "utf-8") : "";
  return { exitCode, stdout, stderr, events: parse(stdout), native, nativeEvents: parse(native) };
}

describe("succeed path", () => {
  test("exit 0, event ladder, turn-complete with output", async () => {
    const fx = fixture({
      steps: [{ do: "succeed", output: "all done" }],
    });
    const r = await runCli(fx, { stdin: "the turn" });
    expect(r.exitCode).toBe(0);
    expect(r.stderr).toBe("");
    expect(r.events.map((e) => e.event)).toEqual(["session-start", "turn-start", "turn-complete"]);
    expect(r.events[0]).toMatchObject({ event: "session-start", session_id: "fake-session", harness: "fake" });
    expect(r.events[1]).toMatchObject({ event: "turn-start", turn: 1, input: "the turn" });
    expect(r.events[2]).toMatchObject({ event: "turn-complete", turn: 1, status: "success", output: "all done" });
  });

  test("native.jsonl mirrors stdout byte-for-byte", async () => {
    const fx = fixture({ fixed_ts: FIXED_TS, steps: [{ do: "succeed", output: "x" }] });
    const r = await runCli(fx, { stdin: "t" });
    expect(r.exitCode).toBe(0);
    expect(r.native).toBe(r.stdout);
  });

  test("golden: byte-deterministic native.jsonl under fixed_ts", async () => {
    const behavior = {
      session_id: "golden-1",
      fixed_ts: FIXED_TS,
      steps: [
        { do: "exec", command: "printf out; printf err >&2; exit 3" },
        { do: "succeed", output: "fin" },
      ],
    };
    const golden =
      `{"event":"session-start","session_id":"golden-1","harness":"fake","ts":"${FIXED_TS}"}\n` +
      `{"event":"turn-start","session_id":"golden-1","turn":1,"input":"do the thing","ts":"${FIXED_TS}"}\n` +
      `{"event":"tool-use","session_id":"golden-1","turn":1,"tool":"shell","command":"printf out; printf err >&2; exit 3","exit_code":3,"stdout":"out","stderr":"err","ts":"${FIXED_TS}"}\n` +
      `{"event":"turn-complete","session_id":"golden-1","turn":1,"status":"success","output":"fin","ts":"${FIXED_TS}"}\n`;

    const r1 = await runCli(fixture(behavior), { stdin: "do the thing" });
    expect(r1.exitCode).toBe(0);
    expect(r1.native).toBe(golden);

    // Determinism: an independent run produces identical bytes.
    const r2 = await runCli(fixture(behavior), { stdin: "do the thing" });
    expect(r2.native).toBe(golden);
  });

  test("--session-id overrides behavior session_id", async () => {
    const fx = fixture({ session_id: "from-file", steps: [{ do: "succeed", output: "" }] });
    const r = await runCli(fx, { extraArgs: ["--session-id", "from-flag"] });
    expect(r.exitCode).toBe(0);
    expect(r.events[0].session_id).toBe("from-flag");
  });

  test("without fixed_ts every event carries a parseable ts", async () => {
    const fx = fixture({ steps: [{ do: "succeed", output: "" }] });
    const r = await runCli(fx);
    expect(r.exitCode).toBe(0);
    for (const e of r.nativeEvents) {
      expect(typeof e.ts).toBe("string");
      expect(Number.isNaN(Date.parse(e.ts as string))).toBe(false);
    }
  });
});

describe("turn input channels", () => {
  test("stdin and --turn-file produce the same turn-start input", async () => {
    const turn = "brief text\nwith a second line";

    const viaStdin = await runCli(fixture({ steps: [{ do: "succeed", output: "" }] }), { stdin: turn });
    expect(viaStdin.exitCode).toBe(0);

    const fx = fixture({ steps: [{ do: "succeed", output: "" }] });
    const turnFile = join(fx.dir, "turn.txt");
    writeFileSync(turnFile, turn);
    const viaFile = await runCli(fx, { extraArgs: ["--turn-file", turnFile] });
    expect(viaFile.exitCode).toBe(0);

    expect(viaStdin.events[1].input).toBe(turn);
    expect(viaFile.events[1].input).toBe(turn);
  });

  test("missing --turn-file path fails exit 2", async () => {
    const fx = fixture({ steps: [{ do: "succeed", output: "" }] });
    const r = await runCli(fx, { extraArgs: ["--turn-file", join(fx.dir, "nope.txt")] });
    expect(r.exitCode).toBe(2);
    expect(r.stderr).toContain("fake-harness:");
    expect(r.native).toBe(""); // nothing written before validation completes
  });
});

describe("crash path", () => {
  test("scripted exit code, NO turn-complete, prior events persist", async () => {
    const fx = fixture({
      fixed_ts: FIXED_TS,
      steps: [
        { do: "exec", command: "echo before-crash" },
        { do: "crash", code: 7 },
      ],
    });
    const r = await runCli(fx, { stdin: "t" });
    expect(r.exitCode).toBe(7);
    const kinds = r.nativeEvents.map((e) => e.event);
    expect(kinds).toEqual(["session-start", "turn-start", "tool-use"]);
    expect(kinds).not.toContain("turn-complete");
    expect(r.stdout).toBe(r.native); // mirrored stream stops at the same point
  });

  test("crash code 255 passes through", async () => {
    const fx = fixture({ steps: [{ do: "crash", code: 255 }] });
    const r = await runCli(fx);
    expect(r.exitCode).toBe(255);
    expect(r.nativeEvents.map((e) => e.event)).toEqual(["session-start", "turn-start"]);
  });
});

describe("exec step", () => {
  test("records tool-use with exit code and both output channels", async () => {
    const fx = fixture({
      steps: [
        { do: "exec", command: "echo hello-out; echo hello-err >&2; exit 42" },
        { do: "succeed", output: "" },
      ],
    });
    const r = await runCli(fx);
    expect(r.exitCode).toBe(0); // child exit never changes harness flow
    const tool = r.events.find((e) => e.event === "tool-use")!;
    expect(tool).toMatchObject({
      tool: "shell",
      command: "echo hello-out; echo hello-err >&2; exit 42",
      exit_code: 42,
      stdout: "hello-out\n",
      stderr: "hello-err\n",
    });
  });

  test("exec sees the harness environment (brief-comprehension channel)", async () => {
    const fx = fixture({
      steps: [
        { do: "exec", command: 'printf "%s" "$MC_FAKE_TEST_VAR"' },
        { do: "succeed", output: "" },
      ],
    });
    const proc = Bun.spawn(
      [process.execPath, CLI, "--behavior", fx.behaviorPath, "--session-dir", fx.sessionDir],
      { stdin: Buffer.from(""), stdout: "pipe", stderr: "pipe", env: { ...process.env, MC_FAKE_TEST_VAR: "propagated" } },
    );
    await proc.exited;
    const out = await new Response(proc.stdout).text();
    const tool = out
      .split("\n")
      .filter((l) => l.length > 0)
      .map((l) => JSON.parse(l))
      .find((e) => e.event === "tool-use");
    expect(tool.stdout).toBe("propagated");
  });

  test("stdout truncated at exactly 8 KiB (ASCII)", async () => {
    const fx = fixture({
      steps: [
        // 10000 'a' bytes on stdout, nothing on stderr.
        { do: "exec", command: "head -c 10000 /dev/zero | tr '\\0' a" },
        { do: "succeed", output: "" },
      ],
    });
    const r = await runCli(fx);
    expect(r.exitCode).toBe(0);
    const tool = r.events.find((e) => e.event === "tool-use")!;
    expect((tool.stdout as string).length).toBe(8192);
    expect(tool.stdout as string).toBe("a".repeat(8192));
  });

  test("stderr truncated at 8 KiB independently of stdout", async () => {
    const fx = fixture({
      steps: [
        { do: "exec", command: "head -c 9000 /dev/zero | tr '\\0' b >&2; printf small" },
        { do: "succeed", output: "" },
      ],
    });
    const r = await runCli(fx);
    const tool = r.events.find((e) => e.event === "tool-use")!;
    expect((tool.stderr as string).length).toBe(8192);
    expect(tool.stdout).toBe("small");
  });

  test("8 KiB cap is measured in BYTES, not UTF-16 code units", async () => {
    // 4000 U+20AC (3 UTF-8 bytes each) = 12000 bytes but only 4000 code
    // units — a length-based cap would keep all 12000 bytes.
    const fx = fixture({
      steps: [
        { do: "exec", command: `printf '\\342\\202\\254%.0s' $(seq 1 4000)` },
        { do: "succeed", output: "" },
      ],
    });
    const r = await runCli(fx);
    expect(r.exitCode).toBe(0);
    const tool = r.events.find((e) => e.event === "tool-use")!;
    expect(Buffer.byteLength(tool.stdout as string, "utf-8")).toBeLessThanOrEqual(8192);
    // Boundary-safe: 8192 falls mid-character (8190 = 2730 * 3), so the
    // cut backs off to 2730 complete characters — valid UTF-8, no U+FFFD.
    expect(tool.stdout as string).toBe("€".repeat(2730));
  });

  test("output at exactly the cap is not truncated", async () => {
    const fx = fixture({
      steps: [
        { do: "exec", command: "head -c 8192 /dev/zero | tr '\\0' c" },
        { do: "succeed", output: "" },
      ],
    });
    const r = await runCli(fx);
    const tool = r.events.find((e) => e.event === "tool-use")!;
    expect(tool.stdout as string).toBe("c".repeat(8192));
  });
});

describe("hang path", () => {
  test("stays alive and silent; SIGKILL leaves partial native.jsonl intact, no turn-complete", async () => {
    const fx = fixture({
      fixed_ts: FIXED_TS,
      steps: [{ do: "exec", command: "echo pre-hang" }, { do: "hang" }],
    });
    const proc = Bun.spawn(
      [process.execPath, CLI, "--behavior", fx.behaviorPath, "--session-dir", fx.sessionDir],
      { env: process.env, stdin: Buffer.from("t"), stdout: "pipe", stderr: "pipe" },
    );

    // Wait until the pre-hang tool-use has been durably appended.
    const nativePath = join(fx.sessionDir, "native.jsonl");
    const deadline = Date.now() + 5000;
    let native = "";
    while (Date.now() < deadline) {
      native = existsSync(nativePath) ? readFileSync(nativePath, "utf-8") : "";
      if (native.includes('"tool-use"')) break;
      await Bun.sleep(20);
    }
    expect(native).toContain('"tool-use"');

    // Observe no completion within a short window: still running, and the
    // durable record has not grown past the pre-hang events.
    await Bun.sleep(300);
    expect(proc.killed).toBe(false);
    expect(proc.exitCode).toBeNull();
    native = readFileSync(nativePath, "utf-8");
    expect(native).not.toContain('"turn-complete"');

    proc.kill("SIGKILL");
    await proc.exited;
    expect(proc.signalCode).toBe("SIGKILL");

    // Partial native.jsonl intact: full JSON lines, correct ladder, no
    // completion event. A run that mc stops at a liveness limit leaves this
    // record.
    const events = readFileSync(nativePath, "utf-8")
      .split("\n")
      .filter((l) => l.length > 0)
      .map((l) => JSON.parse(l));
    expect(events.map((e) => e.event)).toEqual(["session-start", "turn-start", "tool-use"]);
    const stdout = await new Response(proc.stdout).text();
    expect(stdout).not.toContain('"turn-complete"');
  }, 15000);
});

describe("usage and environment errors (exit 2)", () => {
  test("missing session dir fails exit 2 before any write", async () => {
    const fx = fixture({ steps: [{ do: "succeed", output: "" }] });
    rmSync(fx.sessionDir, { recursive: true });
    const r = await runCli(fx);
    expect(r.exitCode).toBe(2);
    expect(r.stdout).toBe("");
    expect(r.stderr).toContain("fake-harness:");
  });

  test("session dir path that is a file fails exit 2", async () => {
    const fx = fixture({ steps: [{ do: "succeed", output: "" }] });
    rmSync(fx.sessionDir, { recursive: true });
    writeFileSync(fx.sessionDir, "not a dir");
    const r = await runCli(fx);
    expect(r.exitCode).toBe(2);
    expect(r.stderr).toContain("is not a directory");
  });

  test("invalid behavior file fails exit 2 with the validator's message", async () => {
    const fx = fixture({ steps: [{ do: "exec", command: "true" }] }); // non-terminal last
    const r = await runCli(fx);
    expect(r.exitCode).toBe(2);
    expect(r.stderr).toContain("invalid behavior file: last step must be terminal");
    expect(r.stdout).toBe("");
    expect(r.native).toBe("");
  });

  test("unparseable behavior file fails exit 2", async () => {
    const fx = fixture("{broken");
    const r = await runCli(fx);
    expect(r.exitCode).toBe(2);
    expect(r.stderr).toContain("not valid JSON");
  });

  test("missing behavior file path fails exit 2", async () => {
    const fx = fixture({ steps: [{ do: "succeed", output: "" }] });
    rmSync(fx.behaviorPath);
    const r = await runCli(fx);
    expect(r.exitCode).toBe(2);
  });

  test("missing required flags and unknown flags fail exit 2 with usage", async () => {
    const fx = fixture({ steps: [{ do: "succeed", output: "" }] });

    const noSession = await runCli(fx, { omitSessionDir: true });
    expect(noSession.exitCode).toBe(2);
    expect(noSession.stderr).toContain("--session-dir is required");
    expect(noSession.stderr).toContain("usage:");

    const unknown = await runCli(fx, { extraArgs: ["--frobnicate"] });
    expect(unknown.exitCode).toBe(2);
    expect(unknown.stderr).toContain('unknown argument "--frobnicate"');

    const dangling = await runCli(fx, { extraArgs: ["--session-id"] });
    expect(dangling.exitCode).toBe(2);
    expect(dangling.stderr).toContain("--session-id requires a value");
  });
});
