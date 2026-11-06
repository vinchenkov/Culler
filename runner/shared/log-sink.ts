// log-sink.ts — the IO half of the logger: an O_APPEND fd, a size-capped ring
// of gzipped generations, and an absolute refusal to throw.
//
// Two properties drive every decision here. First, many short-lived `mc`
// processes append to the same file concurrently, so ownership of the file is
// never exclusive: rotation is lock-guarded and best-effort, and losing the
// race means appending a little past the cap, never stalling. Second, a log
// write is not part of any transaction — `resident/src/effects.ts` `applyEffect`
// has no catch, so an exception escaping `write` kills the tick that was only
// trying to say what it did. Every failure in this file is counted and
// swallowed.

import {
  closeSync,
  constants,
  existsSync,
  fstatSync,
  fsyncSync,
  openSync,
  readFileSync,
  renameSync,
  statSync,
  unlinkSync,
  writeSync,
} from "node:fs";
import { gzipSync } from "node:zlib";
import { join } from "node:path";

import type { Sink } from "./log";

export const ROTATE_MAX_BYTES = 16 * 1024 * 1024;
export const ROTATE_KEEP = 4;
export const RUN_SINK_MAX_BYTES = 4 * 1024 * 1024;

/** How long a rotate lock may sit before the next appender treats its holder as
 * dead. Long enough that a real rotation of a 16 MiB file finishes first. */
const LOCK_STALE_MS = 60_000;

/** The stderr complaint is rate-limited to one per this window per sink. */
const REPORT_EVERY_MS = 60_000;

/** Reopen detection cadence for long-lived writers (see `maybeReopen`). */
const CHECK_EVERY_WRITES = 256;
const CHECK_EVERY_MS = 5_000;

const OPEN_FLAGS = constants.O_WRONLY | constants.O_APPEND | constants.O_CREAT;
const FILE_MODE = 0o600;

function errnoOf(err: unknown): string {
  return typeof err === "object" && err !== null && "code" in err ? String((err as { code: unknown }).code) : "";
}

export function openFileSink(o: {
  dir: string;
  name: string;
  maxBytes?: number;
  keep?: number;
  onError?: (err: unknown) => void;
}): Sink & { close(): void } {
  const path = join(o.dir, o.name);
  const maxBytes = o.maxBytes ?? ROTATE_MAX_BYTES;
  const keep = o.keep ?? ROTATE_KEEP;

  let fd: number | null = null;
  let closed = false;
  let errors = 0;
  let lastReport = 0;
  let writes = 0;
  let lastCheck = 0;
  // Running size, so the common write does no syscall of its own: the cap is
  // checked against arithmetic and only a suspected overflow pays for a stat.
  let knownSize = 0;

  const fail = (err: unknown): void => {
    errors++;
    o.onError?.(err);
    // The one intentional meeting of the structured channel and the crash
    // channel: if logging itself is broken, the structured channel is exactly
    // the place that cannot report it. One plain sentence per minute, so a
    // permanently unwritable log cannot itself become the flood.
    const now = Date.now();
    if (now - lastReport < REPORT_EVERY_MS) return;
    lastReport = now;
    try {
      process.stderr.write(`log sink ${path} failed ${errors} time(s); most recent: ${String(err)}\n`);
    } catch {
      // stderr is gone too. There is nowhere left to say so.
    }
  };

  const closeFd = (): void => {
    if (fd === null) return;
    try {
      closeSync(fd);
    } catch {
      // A close that fails leaves nothing to retry; the fd is gone either way.
    }
    fd = null;
  };

  const ensureOpen = (): boolean => {
    if (closed) return false;
    if (fd !== null) return true;
    try {
      fd = openSync(path, OPEN_FLAGS, FILE_MODE);
      knownSize = fstatSync(fd).size;
      lastCheck = Date.now();
      writes = 0;
      return true;
    } catch (err) {
      // ENOENT with O_CREAT means the DIRECTORY is missing: this component is
      // running without a provisioned MC_HOME. That is a state, not a fault —
      // silent, uncounted, and it will start working the moment the directory
      // appears.
      if (errnoOf(err) !== "ENOENT") fail(err);
      return false;
    }
  };

  /** Rotation, under a lock that no one is obliged to win. Steps are ordered so
   * that a crash at any point leaves either the old file or the new generation
   * readable, never a half-written `.gz` claiming to be one. */
  const rotate = (): void => {
    const lock = `${path}.rotate.lock`;
    let stat;
    try {
      stat = statSync(path);
    } catch {
      // Renamed out from under us; the next write's O_CREAT makes a new one.
      knownSize = 0;
      return;
    }
    knownSize = stat.size;
    if (stat.size < maxBytes) return;

    let lockFd: number;
    try {
      lockFd = openSync(lock, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, FILE_MODE);
    } catch (err) {
      if (errnoOf(err) !== "EEXIST") {
        fail(err);
        return;
      }
      try {
        // A holder that died mid-rotation must not wedge the ring forever, and
        // a live holder must not make us wait: overshoot is bounded, a stall is
        // not.
        if (Date.now() - statSync(lock).mtimeMs < LOCK_STALE_MS) return;
        unlinkSync(lock);
        lockFd = openSync(lock, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, FILE_MODE);
      } catch {
        return;
      }
    }
    try {
      closeSync(lockFd);
    } catch {
      // The lock's value is its existence, not its fd.
    }

    try {
      // Re-check under the lock: between our stat and our lock someone else may
      // have rotated, and rotating again would archive a nearly empty file and
      // push a real generation out of the ring.
      if (statSync(path).size < maxBytes) {
        closeFd();
        return;
      }

      renameSync(path, `${path}.rotating`);
      // From here the live file does not exist; the next appender's O_CREAT
      // recreates it. Our own fd still points at the renamed inode and this
      // function is synchronous, so this writer loses nothing: it reopens in
      // the `finally` below, after the archive has been read. Another process
      // still holding a stale fd may land a record in the archived inode after
      // it was read — best-effort is the price of not locking every append.

      const drop = (p: string): void => {
        try {
          unlinkSync(p);
        } catch {
          // Absent is the desired state.
        }
      };
      drop(`${path}.${keep}.gz`);
      for (let i = keep - 1; i >= 1; i--) {
        try {
          renameSync(`${path}.${i}.gz`, `${path}.${i + 1}.gz`);
        } catch {
          // A hole in the ring is normal before `keep` rotations have happened.
        }
      }

      // Stage, fsync, rename, fsync the directory — the shape of
      // `persistGrant` in resident/src/main.ts: a `.gz` that exists is a `.gz`
      // whose bytes are on the platter, because a torn archive is indis-
      // tinguishable from a good one until someone needs it.
      const staged = `${path}.1.gz.next`;
      drop(staged); // leftover from a crash: O_EXCL is the guard, not the goal
      const gz = gzipSync(readFileSync(`${path}.rotating`));
      const stagedFd = openSync(staged, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL, FILE_MODE);
      try {
        writeSync(stagedFd, gz);
        fsyncSync(stagedFd);
      } finally {
        closeSync(stagedFd);
      }
      renameSync(staged, `${path}.1.gz`);
      drop(`${path}.rotating`);
      fsyncDir(o.dir);
    } catch (err) {
      fail(err);
    } finally {
      try {
        unlinkSync(lock);
      } catch {
        // Left behind, it goes stale in a minute and the next appender clears it.
      }
      // Our fd still points at the archived inode. Reopen onto the live path.
      closeFd();
      ensureOpen();
    }
  };

  /** Long-lived writers (the resident, a collie runner) outlive rotations
   * performed by short-lived `mc` processes. Without this an fd keeps writing
   * into an inode that has already been gzipped and unlinked, and the records
   * go nowhere at full speed. */
  const maybeReopen = (): void => {
    if (fd === null) return;
    const now = Date.now();
    if (writes < CHECK_EVERY_WRITES && now - lastCheck <= CHECK_EVERY_MS) return;
    writes = 0;
    lastCheck = now;
    try {
      const mine = fstatSync(fd);
      const live = statSync(path);
      if (mine.ino === live.ino && mine.dev === live.dev) {
        knownSize = live.size;
        return;
      }
    } catch {
      // The path is gone (rotation in flight). Reopening recreates it.
    }
    closeFd();
    ensureOpen();
  };

  return {
    write(line: string): void {
      if (closed) return;
      if (!ensureOpen()) return;
      if (maxBytes > 0) {
        // maxBytes === 0 is the container case: one short-lived process, one
        // file, nobody else appending. Neither rotation nor reopen detection
        // can pay for itself there.
        maybeReopen();
        if (knownSize >= maxBytes) rotate();
      }
      if (fd === null) return;
      try {
        const bytes = Buffer.from(line, "utf8");
        // One writeSync per record. Not a loop over a partial write: an
        // O_APPEND write of a few KB to a regular file is atomic, and a
        // continuation write would be the interleaving this exists to prevent.
        writeSync(fd, bytes);
        knownSize += bytes.length;
        writes++;
      } catch (err) {
        fail(err);
        closeFd(); // a bad fd stays bad; the next write reopens
      }
    },
    close(): void {
      closed = true;
      closeFd();
    },
  };
}

/** stderr, for a process that has no writable log directory: every container
 * before the `/mc/logs` bind exists, and any component whose MC_HOME is absent.
 *
 * Deliberately the same NDJSON the file sinks write, not prose. Whoever reads
 * it — `docker logs`, a terminal, a launchd redirect — reads one format, and a
 * process that later gains a real directory changes its sink and nothing else.
 * `writeSync` because the process may exit before an async flush lands. */
export function stderrSink(): Sink {
  return {
    write(line: string): void {
      try {
        writeSync(2, Buffer.from(line, "utf8"));
      } catch {
        // stderr is closed or full. There is nowhere left to complain to.
      }
    },
  };
}

/** The sink of a runner or an adapter. The resident binds the per-run log
 * directory at /mc/logs. A test names another directory in MC_RUN_LOG_DIR, so
 * the code under test leaves the log of the surrounding run unchanged. Without
 * the directory, the sink is stderr. */
export function runLogSink(env: Record<string, string | undefined> = process.env): Sink {
  const dir = env["MC_RUN_LOG_DIR"] || "/mc/logs";
  return existsSync(dir) ? openRunSink(dir) : stderrSink();
}

/** The per-run log: one file, one short-lived writer, no rotation — and a hard
 * total cap, because a harness in a retry loop can produce megabytes a minute
 * and the run directory is not the place to find that out. */
export function openRunSink(dir: string): Sink & { close(): void } {
  const inner = openFileSink({ dir, name: "runner.ndjson", maxBytes: 0 });
  let total = 0;
  let capped = false;
  return {
    write(line: string): void {
      if (capped) return;
      const size = Buffer.byteLength(line, "utf8");
      if (total + size > RUN_SINK_MAX_BYTES) {
        capped = true;
        // Say why the file stops, in the file's own format, once. A log that
        // simply ends reads as a crash.
        inner.write(
          `{"ts":${JSON.stringify(new Date().toISOString())},"level":"warn","comp":"log-sink",` +
            `"event":"log.sink_failed","msg":"run log cap reached","cap_bytes":${RUN_SINK_MAX_BYTES}}\n`,
        );
        return;
      }
      total += size;
      inner.write(line);
    },
    close(): void {
      inner.close();
    },
  };
}

function fsyncDir(dir: string): void {
  // A rename is only durable once the DIRECTORY entry is. Without this the
  // `.1.gz` can be absent after a power loss while its bytes sit on disk under
  // no name at all.
  let dirFd: number | null = null;
  try {
    dirFd = openSync(dir, constants.O_RDONLY);
    fsyncSync(dirFd);
  } catch {
    // Not every filesystem lets a directory be fsynced; durability of the
    // rename is best-effort, its ordering is not.
  } finally {
    if (dirFd !== null) {
      try {
        closeSync(dirFd);
      } catch {
        // nothing left to do
      }
    }
  }
}
