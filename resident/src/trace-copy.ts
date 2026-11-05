// trace-copy.ts — copies the new bytes of one trace file into its trace
// copy. The copy preserves source offsets and masks recognizable Culler tokens.
// Each copy ends at a complete line. Its size is the copy position, so a restart
// continues with no duplicate and no gap.
//
// An agent can write its session folder while the resident reads it. An
// agent can swap a folder for a symbolic link during a pass, so each opened
// source file and folder must be the entry at its real path inside the real
// session folder.

import { constants, lstat, mkdir, open, realpath, rename, stat, type FileHandle } from "node:fs/promises";
import { basename, dirname, join, relative, sep } from "node:path";
import { maskProviderTokens, PROVIDER_TOKEN_LENGTH } from "../../runner/shared/provider-token";

/** The last bytes of a copy that a restart compares with the source. */
export const TAIL_CHECK_BYTES = 4096;
export const CHUNK_BYTES = 1 << 20;
/** The bytes that one pass reads from one file. */
export const PASS_BYTES = 8 << 20;
export const MAX_LINE_BYTES = 64 << 20;

const NEWLINE = 0x0a;
// O_NONBLOCK keeps a FIFO at a trace path from blocking the open.
const READ_FLAGS = constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK;
const FOLDER_FLAGS = constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_DIRECTORY;
const WRITE_FLAGS = constants.O_WRONLY | constants.O_CREAT | constants.O_NOFOLLOW;
// An append after the first byte never creates the copy, so a copy that
// disappears cannot come back with a gap.
const APPEND_FLAGS = constants.O_WRONLY | constants.O_NOFOLLOW;

export interface CopyState {
  /** The session folder. The source must stay inside its real path. */
  root: string;
  source: string;
  dest: string;
  /** The size of the verified copy. The lane verifies the copy again when
   * this value is undefined. */
  position?: number;
  /** The device and inode of the source at the last pass. */
  sourceId?: string;
  sourceMtimeMs?: number;
  /** The size of the source at the last pass. */
  sourceSize?: number;
  /** The source bytes from `from` to `to` hold no newline. `from` is the
   * start of the unterminated last line. */
  scan?: { from: number; to: number };
  /** A complete new copy that replaces the copy when it reaches the source. */
  recopy?: { temp: string; position: number };
  /** Epoch milliseconds of the last change of the copy. */
  changedAt?: number;
}

export interface CopyResult {
  /** The bytes that this pass wrote. */
  copied: number;
  /** False when the pass stopped at its byte limit. */
  caughtUp: boolean;
  /** True when the source file does not exist. */
  absent: boolean;
  /** True when a complete new copy replaced the copy in this pass. */
  replaced: boolean;
}

export interface CopyLimits {
  chunkBytes?: number;
  passBytes?: number;
  maxLineBytes?: number;
}

function code(error: unknown): string | undefined {
  return error !== null && typeof error === "object" && "code" in error ? String(error.code) : undefined;
}

/** The copy is absent or has another size than the copy position. */
class CopyChanged extends Error {
  constructor() {
    super("the trace copy changed while the resident wrote it");
  }
}

async function openForRead(path: string, what: string): Promise<FileHandle | undefined> {
  let handle: FileHandle;
  try {
    handle = await open(path, READ_FLAGS);
  } catch (error) {
    if (code(error) === "ENOENT") return undefined;
    if (code(error) === "ELOOP") throw new Error(`the ${what} is a symbolic link`);
    throw error;
  }
  try {
    if (!(await handle.stat()).isFile()) throw new Error(`the ${what} is not a regular file`);
  } catch (error) {
    await handle.close();
    throw error;
  }
  return handle;
}

/** Proves that an opened entry below the session folder `root` is the entry
 * at its real path, and that this real path is inside the real path of
 * `root`. The session folder itself passes as a folder. */
export async function checkInside(handle: FileHandle, root: string, path: string, what: string): Promise<void> {
  const below = relative(root, path);
  if (below === ".." || below.startsWith(`..${sep}`)) throw new Error(`the ${what} is outside its session folder`);
  const [rootReal, real, opened] = await Promise.all([realpath(root), realpath(path), handle.stat()]);
  if (!real.startsWith(rootReal + sep) && !(opened.isDirectory() && real === rootReal)) {
    throw new Error(`the ${what} resolves outside its session folder`);
  }
  let entry = root;
  for (const part of ["", ...below.split(sep).filter(Boolean)]) {
    entry = join(entry, part);
    if ((await lstat(entry)).isSymbolicLink()) throw new Error(`the ${what} path contains a symbolic link`);
  }
  const named = await stat(real);
  if (named.dev !== opened.dev || named.ino !== opened.ino) {
    throw new Error(`the ${what} changed while the resident opened it`);
  }
}

/** Opens one folder below the session folder `root` for a walk. The caller
 * closes the handle. */
export async function openFolderInside(root: string, path: string, what: string): Promise<FileHandle> {
  let handle: FileHandle;
  try {
    handle = await open(path, FOLDER_FLAGS);
  } catch (error) {
    // Linux gives ELOOP for a symbolic link. macOS gives ENOTDIR.
    if (code(error) === "ELOOP" || code(error) === "ENOTDIR") {
      const link = code(error) === "ELOOP" || (await lstat(path).catch(() => undefined))?.isSymbolicLink();
      throw new Error(link ? `the ${what} is a symbolic link` : `the ${what} is not a folder`);
    }
    throw error;
  }
  try {
    await checkInside(handle, root, path, what);
  } catch (error) {
    await handle.close();
    throw error;
  }
  return handle;
}

async function readAt(handle: FileHandle, position: number, length: number): Promise<Buffer> {
  const buffer = Buffer.alloc(length);
  let filled = 0;
  while (filled < length) {
    const { bytesRead } = await handle.read(buffer, filled, length - filled, position + filled);
    if (bytesRead === 0) break;
    filled += bytesRead;
  }
  return buffer.subarray(0, filled);
}

async function writeAt(handle: FileHandle, data: Buffer, position: number): Promise<void> {
  let written = 0;
  while (written < data.length) {
    const { bytesWritten } = await handle.write(data, written, data.length - written, position + written);
    written += bytesWritten;
  }
}

/** The size and time of a copy whose last bytes equal the same bytes of the
 * source, or undefined when the copy must be made again. */
async function verifiedCopy(
  dest: string, source: FileHandle, size: number,
): Promise<{ size: number; mtimeMs?: number } | undefined> {
  const copy = await openForRead(dest, "trace copy");
  if (copy === undefined) return { size: 0 };
  try {
    const info = await copy.stat();
    if (info.size === 0) return { size: 0, mtimeMs: info.mtimeMs };
    if (info.size > size) return undefined;
    const length = Math.min(TAIL_CHECK_BYTES, info.size);
    const start = info.size - length;
    const overlap = Math.min(start, PROVIDER_TOKEN_LENGTH - 1);
    const [mine, raw] = await Promise.all([readAt(copy, start, length), readAt(source, start - overlap, length + overlap)]);
    const theirs = maskProviderTokens(raw).subarray(overlap);
    if (mine.length !== length || mine[length - 1] !== NEWLINE || !mine.equals(theirs)) return undefined;
    return { size: info.size, mtimeMs: info.mtimeMs };
  } finally {
    await copy.close();
  }
}

async function beginRecopy(state: CopyState): Promise<void> {
  const temp = join(dirname(state.dest), `.${basename(state.dest)}.recopy`);
  await mkdir(dirname(temp), { recursive: true, mode: 0o700 });
  const handle = await open(temp, WRITE_FLAGS | constants.O_TRUNC, 0o600);
  await handle.close();
  state.recopy = { temp, position: 0 };
}

/** Opens the target for a write at `start`. The target must be a regular
 * file of exactly `start` bytes. Only a write at the first byte creates it. */
async function openTarget(target: string, start: number): Promise<FileHandle> {
  if (start === 0) await mkdir(dirname(target), { recursive: true, mode: 0o700 });
  let handle: FileHandle;
  try {
    handle = await open(target, start === 0 ? WRITE_FLAGS : APPEND_FLAGS, 0o600);
  } catch (error) {
    if (code(error) === "ENOENT") throw new CopyChanged();
    throw error;
  }
  try {
    const info = await handle.stat();
    if (!info.isFile() || info.size !== start) throw new CopyChanged();
  } catch (error) {
    await handle.close();
    throw error;
  }
  return handle;
}

async function copyHasSize(dest: string, size: number): Promise<boolean> {
  const info = await lstat(dest).catch(() => undefined);
  return info?.isFile() === true && info.size === size;
}

/** True when the source and the copy did not change since the last pass.
 * The pass then has no byte to read. */
async function unchanged(state: CopyState): Promise<boolean> {
  if (state.position === undefined || state.recopy !== undefined || state.sourceId === undefined) return false;
  const source = await lstat(state.source).catch(() => undefined);
  if (source === undefined || !source.isFile() || `${source.dev}:${source.ino}` !== state.sourceId) return false;
  if (source.size !== state.sourceSize || source.mtimeMs !== state.sourceMtimeMs) return false;
  const scanned = state.scan?.from === state.position ? state.scan.to : state.position;
  return source.size === scanned && await copyHasSize(state.dest, state.position);
}

/** Appends the complete lines of the source after the copy position to the
 * target, within the byte limit of one pass. Each read counts against the
 * limit. The pass remembers an unterminated last line, so the next pass
 * reads only the bytes after it. */
async function appendLines(
  source: FileHandle, size: number, state: CopyState, limits: Required<CopyLimits>,
): Promise<{ start: number; end: number; caughtUp: boolean }> {
  const start = state.recopy?.position ?? state.position!;
  const target = state.recopy?.temp ?? state.dest;
  let position = start;
  // The source bytes from position to scanned hold no newline.
  let scanned = state.scan !== undefined && state.scan.from === start && state.scan.to <= size ? state.scan.to : start;
  let budget = limits.passBytes;
  let output: FileHandle | undefined;
  const write = async (data: Buffer) => {
    output ??= await openTarget(target, position);
    await writeAt(output, maskProviderTokens(data), position);
    position += data.length;
    scanned = position;
  };
  try {
    while (position < size && budget > 0) {
      if (scanned === position) {
        const data = await readAt(source, position, Math.min(size - position, limits.chunkBytes));
        budget -= data.length;
        const newline = data.lastIndexOf(NEWLINE);
        if (newline >= 0) {
          await write(data.subarray(0, newline + 1));
          continue;
        }
        if (data.length === 0) break;
        scanned = position + data.length;
      }
      // The line at the copy position is longer than one read. The pass
      // looks for its end in the bytes after the scanned bytes.
      let end = -1;
      while (end < 0 && scanned < size && budget > 0) {
        if (scanned - position >= limits.maxLineBytes) {
          state.scan = { from: position, to: scanned };
          throw new Error(`a trace line is longer than ${limits.maxLineBytes} bytes`);
        }
        const data = await readAt(source, scanned, Math.min(size - scanned, limits.chunkBytes));
        if (data.length === 0) break;
        budget -= data.length;
        const newline = data.indexOf(NEWLINE);
        if (newline >= 0) end = scanned + newline + 1;
        else scanned += data.length;
      }
      if (end < 0) break;
      if (end - position > limits.maxLineBytes) {
        state.scan = { from: position, to: end - 1 };
        throw new Error(`a trace line is longer than ${limits.maxLineBytes} bytes`);
      }
      const line = await readAt(source, position, end - position);
      budget -= line.length;
      if (line.length !== end - position || line[line.length - 1] !== NEWLINE) break;
      await write(line);
    }
    state.scan = scanned > position ? { from: position, to: scanned } : undefined;
    return { start, end: position, caughtUp: scanned >= size };
  } finally {
    await output?.close();
  }
}

/** One pass for one trace file. A shorter source, a replaced source with
 * other bytes, a copy that is not a prefix of the source, or a copy that is
 * absent or has another size than the copy position starts a complete new
 * copy in a temporary file. A rename replaces the copy when the new copy
 * reaches the source. */
export async function copyTrace(state: CopyState, now: number, limits: CopyLimits = {}): Promise<CopyResult> {
  const bounds: Required<CopyLimits> = {
    chunkBytes: limits.chunkBytes ?? CHUNK_BYTES,
    passBytes: limits.passBytes ?? PASS_BYTES,
    maxLineBytes: limits.maxLineBytes ?? MAX_LINE_BYTES,
  };
  if (await unchanged(state)) return { copied: 0, caughtUp: true, absent: false, replaced: false };
  const source = await openForRead(state.source, "trace file");
  if (source === undefined) return { copied: 0, caughtUp: true, absent: true, replaced: false };
  try {
    await checkInside(source, state.root, state.source, "trace file");
    const info = await source.stat();
    const id = `${info.dev}:${info.ino}`;
    if (state.sourceId !== id) {
      state.sourceId = id;
      state.position = undefined;
      state.recopy = undefined;
      state.scan = undefined;
    }
    state.sourceMtimeMs = info.mtimeMs;
    state.sourceSize = info.size;
    const size = info.size;
    if (state.recopy === undefined && state.position === undefined) {
      const copy = await verifiedCopy(state.dest, source, size);
      if (copy === undefined) {
        await beginRecopy(state);
      } else {
        state.position = copy.size;
        state.changedAt ??= copy.mtimeMs;
      }
    } else if (state.recopy === undefined
      ? size < state.position! || !(await copyHasSize(state.dest, state.position!))
      : size < state.recopy.position) {
      await beginRecopy(state);
    }
    let appended;
    try {
      appended = await appendLines(source, size, state, bounds);
    } catch (error) {
      if (!(error instanceof CopyChanged)) throw error;
      await beginRecopy(state);
      appended = await appendLines(source, size, state, bounds);
    }
    const { start, end, caughtUp } = appended;
    let replaced = false;
    if (state.recopy !== undefined) {
      state.recopy.position = end;
      if (caughtUp) {
        await rename(state.recopy.temp, state.dest);
        state.position = end;
        state.recopy = undefined;
        state.changedAt = now;
        replaced = true;
      }
    } else if (end > start) {
      state.position = end;
      state.changedAt = now;
    }
    return { copied: end - start, caughtUp, absent: false, replaced };
  } finally {
    await source.close();
  }
}
