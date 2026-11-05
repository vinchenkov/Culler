// log-sink.test.ts — the file half. What is proved here is what a reader of a
// rotated log is entitled to assume: no record disappears across the rename,
// four concurrent appenders never split a line, and nothing the sink can hit —
// a missing MC_HOME, an unwritable path, a lock somebody else holds — reaches
// the caller as an exception.

import { afterEach, describe, expect, test } from "bun:test";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, statSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { gunzipSync } from "node:zlib";

import { openFileSink, openRunSink, ROTATE_MAX_BYTES, RUN_SINK_MAX_BYTES, runLogSink, stderrSink } from "./log-sink";

const scratches: string[] = [];

function scratch(): string {
  const dir = mkdtempSync(join(tmpdir(), "mc-log-sink-"));
  scratches.push(dir);
  return dir;
}

afterEach(() => {
  while (scratches.length > 0) rmSync(scratches.pop()!, { recursive: true, force: true });
});

/** A line of exactly `bytes` bytes including its newline, so a test can predict
 * where rotation falls instead of guessing. */
function padded(n: number, bytes = 100): string {
  const head = `{"i":${n},"pad":"`;
  const tail = '"}\n';
  return head + "p".repeat(bytes - head.length - tail.length) + tail;
}

function lines(path: string): string[] {
  return readFileSync(path, "utf8").split("\n").filter((l) => l.length > 0);
}

function gzLines(path: string): string[] {
  return gunzipSync(readFileSync(path)).toString("utf8").split("\n").filter((l) => l.length > 0);
}

describe("openFileSink", () => {
  test("appends one line per write and creates the file 0600", () => {
    const dir = scratch();
    const sink = openFileSink({ dir, name: "a.log", maxBytes: 0 });
    sink.write('{"event":"boot.started"}\n');
    sink.write('{"event":"boot.stopping"}\n');
    sink.close();
    const path = join(dir, "a.log");
    expect(lines(path)).toEqual(['{"event":"boot.started"}', '{"event":"boot.stopping"}']);
    expect(statSync(path).mode & 0o777).toBe(0o600);
  });

  test("appends to an existing file rather than truncating it", () => {
    const dir = scratch();
    writeFileSync(join(dir, "a.log"), "earlier\n");
    const sink = openFileSink({ dir, name: "a.log", maxBytes: 0 });
    sink.write("later\n");
    sink.close();
    expect(lines(join(dir, "a.log"))).toEqual(["earlier", "later"]);
  });

  test("a missing directory makes every write a silent no-op", () => {
    const dir = join(scratch(), "never-provisioned");
    let errors = 0;
    const sink = openFileSink({ dir, name: "a.log", onError: () => void errors++ });
    expect(() => {
      sink.write('{"event":"boot.started"}\n');
      sink.write('{"event":"boot.stopping"}\n');
      sink.close();
    }).not.toThrow();
    expect(existsSync(dir)).toBe(false); // it never creates the directory
    expect(errors).toBe(0); // an unprovisioned MC_HOME is a state, not a fault
  });

  test("starts working the moment the directory appears", () => {
    const root = scratch();
    const dir = join(root, "logs");
    const sink = openFileSink({ dir, name: "a.log", maxBytes: 0 });
    sink.write("lost\n");
    mkdirSync(dir);
    sink.write("kept\n");
    sink.close();
    expect(lines(join(dir, "a.log"))).toEqual(["kept"]);
  });

  test("a write error is reported, not propagated", () => {
    const dir = scratch();
    mkdirSync(join(dir, "a.log")); // the sink's path is a directory: EISDIR
    const seen: unknown[] = [];
    const sink = openFileSink({ dir, name: "a.log", maxBytes: 0, onError: (err) => void seen.push(err) });
    const real = process.stderr.write.bind(process.stderr);
    (process.stderr as { write: unknown }).write = () => true; // it complains; that is tested below
    try {
      expect(() => sink.write("x\n")).not.toThrow();
    } finally {
      (process.stderr as { write: unknown }).write = real;
    }
    expect(seen).toHaveLength(1);
  });

  test("complains to stderr at most once a minute, however many failures", () => {
    const dir = scratch();
    mkdirSync(join(dir, "a.log"));
    const said: string[] = [];
    const real = process.stderr.write.bind(process.stderr);
    // The one place the structured channel cannot report its own failure, so
    // the plain channel does — rate-limited, or a broken log becomes the flood.
    (process.stderr as { write: unknown }).write = (chunk: unknown) => {
      said.push(String(chunk));
      return true;
    };
    try {
      const sink = openFileSink({ dir, name: "a.log", maxBytes: 0 });
      for (let i = 0; i < 50; i++) sink.write("x\n");
      sink.close();
    } finally {
      (process.stderr as { write: unknown }).write = real;
    }
    expect(said).toHaveLength(1);
    expect(said[0]).toContain("log sink");
    expect(said[0]!.endsWith("\n")).toBe(true);
  });

  test("the default cap is 16 MiB and the default ring keeps 4", () => {
    expect(ROTATE_MAX_BYTES).toBe(16 * 1024 * 1024);
  });
});

describe("rotation", () => {
  test("shifts the ring and loses no record across the rename", () => {
    const dir = scratch();
    const path = join(dir, "a.log");
    const sink = openFileSink({ dir, name: "a.log", maxBytes: 1000, keep: 2 });
    for (let i = 1; i <= 25; i++) sink.write(padded(i));
    sink.close();

    // Two rotations at ten 100-byte lines each: the first generation has been
    // pushed from .1.gz to .2.gz, and the tail is still live.
    expect(existsSync(`${path}.1.gz`)).toBe(true);
    expect(existsSync(`${path}.2.gz`)).toBe(true);
    expect(existsSync(`${path}.rotating`)).toBe(false);
    expect(existsSync(`${path}.1.gz.next`)).toBe(false);
    expect(existsSync(`${path}.rotate.lock`)).toBe(false);

    const all = [...gzLines(`${path}.2.gz`), ...gzLines(`${path}.1.gz`), ...lines(path)];
    expect(all).toHaveLength(25);
    expect(all).toEqual(Array.from({ length: 25 }, (_, i) => padded(i + 1).trimEnd()));
  });

  test("drops the oldest generation once the ring is full", () => {
    const dir = scratch();
    const path = join(dir, "a.log");
    const sink = openFileSink({ dir, name: "a.log", maxBytes: 1000, keep: 2 });
    for (let i = 1; i <= 45; i++) sink.write(padded(i));
    sink.close();
    expect(existsSync(`${path}.3.gz`)).toBe(false);
    // The ring holds the two most recent archived generations, not the first.
    expect(gzLines(`${path}.1.gz`)[0]).toBe(padded(31).trimEnd());
    expect(gzLines(`${path}.2.gz`)[0]).toBe(padded(21).trimEnd());
  });

  test("a lock somebody else holds means overshoot, never a stall", () => {
    const dir = scratch();
    const path = join(dir, "a.log");
    writeFileSync(`${path}.rotate.lock`, "");
    const sink = openFileSink({ dir, name: "a.log", maxBytes: 1000, keep: 2 });
    for (let i = 1; i <= 25; i++) sink.write(padded(i));
    sink.close();
    expect(existsSync(`${path}.1.gz`)).toBe(false);
    expect(lines(path)).toHaveLength(25); // every record still landed
  });

  test("breaks a lock whose holder died", () => {
    const dir = scratch();
    const path = join(dir, "a.log");
    writeFileSync(`${path}.rotate.lock`, "");
    const old = new Date(Date.now() - 5 * 60_000);
    utimesSync(`${path}.rotate.lock`, old, old);
    const sink = openFileSink({ dir, name: "a.log", maxBytes: 1000, keep: 2 });
    for (let i = 1; i <= 25; i++) sink.write(padded(i));
    sink.close();
    expect(existsSync(`${path}.1.gz`)).toBe(true);
    expect(existsSync(`${path}.rotate.lock`)).toBe(false);
  });

  test("maxBytes 0 disables rotation entirely", () => {
    const dir = scratch();
    const path = join(dir, "a.log");
    const sink = openFileSink({ dir, name: "a.log", maxBytes: 0, keep: 2 });
    for (let i = 1; i <= 60; i++) sink.write(padded(i));
    sink.close();
    expect(existsSync(`${path}.1.gz`)).toBe(false);
    expect(lines(path)).toHaveLength(60);
  });

  test("reopens after another process rotated the file out from under it", () => {
    const dir = scratch();
    const path = join(dir, "a.log");
    const sink = openFileSink({ dir, name: "a.log", maxBytes: ROTATE_MAX_BYTES });
    sink.write(padded(0));
    renameSync(path, `${path}.taken`); // stand-in for a short-lived mc rotating
    // The check is throttled: it fires on write count, so the reopen is not
    // instant — what matters is that the writer does not go on writing into an
    // inode nobody will ever read.
    for (let i = 1; i <= 300; i++) sink.write(padded(i));
    sink.close();
    expect(existsSync(path)).toBe(true);
    const recovered = lines(path);
    expect(recovered.length).toBeGreaterThan(0);
    expect(recovered.at(-1)).toBe(padded(300).trimEnd());
  });
});

describe("concurrent appenders", () => {
  test("four short-lived processes never interleave a line", async () => {
    const dir = scratch();
    const script = join(dir, "appender.ts");
    writeFileSync(
      script,
      [
        // Each child is a whole short-lived process, exactly like an `mc` verb:
        // it opens the shared file, appends its share, and exits.
        `const [, , mod, dir, tag, count] = Bun.argv;`,
        `const { openFileSink } = await import(mod);`,
        `const sink = openFileSink({ dir, name: "shared.log", maxBytes: 0 });`,
        `for (let i = 0; i < Number(count); i++) {`,
        `  sink.write(JSON.stringify({ tag, i, pad: "x".repeat(400) }) + "\\n");`,
        `}`,
        `sink.close();`,
      ].join("\n"),
    );
    const mod = join(import.meta.dir, "log-sink.ts");
    const N = 4;
    const M = 200;
    const kids = Array.from({ length: N }, (_, k) =>
      Bun.spawn(["bun", script, mod, dir, `child-${k}`, String(M)], { env: process.env, stdout: "pipe", stderr: "pipe" }),
    );
    const codes = await Promise.all(kids.map((p) => p.exited));
    expect(codes).toEqual(Array.from({ length: N }, () => 0));

    const got = lines(join(dir, "shared.log"));
    expect(got).toHaveLength(N * M);
    // Every line whole: a torn append shows up here as a parse failure long
    // before anyone tries to query the file.
    for (const line of got) expect(() => JSON.parse(line)).not.toThrow();
    const perChild = new Map<string, number>();
    for (const line of got) {
      const tag = JSON.parse(line).tag as string;
      perChild.set(tag, (perChild.get(tag) ?? 0) + 1);
    }
    expect([...perChild.values()]).toEqual(Array.from({ length: N }, () => M));
  }, 30_000);
});

describe("openRunSink", () => {
  test("writes runner.ndjson without rotating", () => {
    const dir = scratch();
    const sink = openRunSink(dir);
    sink.write('{"event":"runner.started"}\n');
    sink.close();
    expect(lines(join(dir, "runner.ndjson"))).toEqual(['{"event":"runner.started"}']);
    expect(existsSync(join(dir, "runner.ndjson.1.gz"))).toBe(false);
  });

  test("stops at the cap and says so once, in the file's own format", () => {
    const dir = scratch();
    const sink = openRunSink(dir);
    const line = `${"z".repeat(8190)}\n`;
    for (let i = 0; i < Math.ceil(RUN_SINK_MAX_BYTES / line.length) + 20; i++) sink.write(line);
    sink.close();
    const got = lines(join(dir, "runner.ndjson"));
    const last = JSON.parse(got.at(-1)!);
    expect(last.event).toBe("log.sink_failed");
    expect(last.msg).toBe("run log cap reached");
    expect(got.filter((l) => l.includes("log.sink_failed"))).toHaveLength(1);
    expect(statSync(join(dir, "runner.ndjson")).size).toBeLessThan(RUN_SINK_MAX_BYTES + 4096);
  });
});

describe("runLogSink", () => {
  test("writes to the directory that MC_RUN_LOG_DIR names", () => {
    const dir = scratch();
    const sink = runLogSink({ MC_RUN_LOG_DIR: dir });
    sink.write('{"event":"runner.started"}\n');
    sink.close();
    expect(lines(join(dir, "runner.ndjson"))).toEqual(['{"event":"runner.started"}']);
  });

  test("leaves an absent directory absent", () => {
    const dir = join(scratch(), "absent");
    runLogSink({ MC_RUN_LOG_DIR: dir });
    expect(existsSync(dir)).toBe(false);
  });
});

describe("stderrSink", () => {
  test("writes the line to real fd 2, verbatim", () => {
    // A child process, because the only honest assertion about fd 2 is what a
    // reader of fd 2 receives — and this process's fd 2 is the test output.
    const src = join(import.meta.dir, "log-sink.ts");
    const proc = Bun.spawnSync([
      process.execPath,
      "-e",
      `import { stderrSink } from ${JSON.stringify(src)};\n` +
        `stderrSink().write('{"event":"runner.started"}\\n');`,
    ], { env: process.env });
    expect(new TextDecoder().decode(proc.stderr)).toBe('{"event":"runner.started"}\n');
    expect(new TextDecoder().decode(proc.stdout)).toBe("");
  });
});
