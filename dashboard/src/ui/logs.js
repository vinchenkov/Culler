import { localStamp, projectName } from "./format.js";

export const LOG_UNITS = ["resident", "dashboard", "agentsview", "mc"];

/** The run-diagnostics lane: one per-run file a container's runner and adapters
 * wrote. Not a unit in the verb's table — it has its own verb
 * lane and its own picker, fed by the server-produced run list. */
export const RUN_LANE = "run";

/** The unit picker's full option list, run lane last. */
export const UNIT_OPTIONS = [...LOG_UNITS, RUN_LANE];

// Per unit, because the units are not alike: `mc` is not supervised, has no
// launchd redirect, and therefore has no stdout or stderr to offer. A flat list
// would let the picker compose a pair the verb refuses.
//
// `events` leads because it is the structured file the component opens and owns
// — the one with levels, fields, and rotation. `stderr` is the crash channel
// and matters exactly when `events` is what broke; `stdout` is structurally
// empty, and is offered so an operator who looks finds an empty file rather
// than wondering where it went.
//
// AgentsView writes plain text only: the two launchd files and its own debug
// log. The debug log holds its operational log, so it leads. The launchd
// files hold only startup progress.
export const LOG_STREAMS_BY_UNIT = {
  resident: ["events", "stderr", "stdout"],
  dashboard: ["events", "stderr", "stdout"],
  agentsview: ["debug", "stderr", "stdout"],
  mc: ["events"],
};

/** The union, in picker order — the full option set the pane can ever show. */
export const LOG_STREAMS = ["events", "stderr", "stdout", "debug"];

/** The record levels the shared registry admits, loudest first. */
export const LOG_LEVELS = ["error", "warn", "info", "debug"];

/** The streams one unit actually has. The run lane carries only its one
 * structured file; an unknown unit answers for the default unit rather than
 * for nothing, so a stale deep-link still renders a pane. */
export function streamsFor(unit) {
  if (unit === RUN_LANE) return ["events"];
  return LOG_STREAMS_BY_UNIT[unit] ?? LOG_STREAMS_BY_UNIT[LOG_UNITS[0]];
}

// Mirrored from the verb's own clamp so the picker cannot ask for a window the
// server will refuse.
export const TAIL_DEFAULT = 500;
export const TAIL_MAX = 2000;

// The resident's SAFE_ID shape, mirrored because a hash is operator-supplied
// input: a token that fails it is dropped before any fetch. The verb's own
// fences stay the authority for anything that passes.
const RUN_ID = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/;

export function validRunID(run) {
  return typeof run === "string" && RUN_ID.test(run);
}

/** The /api/logs query for a picker state; invalid picks fall back to the
 * defaults rather than reaching the server. The run lane with no run picked
 * has nothing to ask for, so it returns null and the pane says so instead.
 * Structured streams ask for `format=json`: the verb parses AND renders, and
 * the browser only selects. */
export function logsQuery({ unit, stream, tail, run } = {}) {
  let n = Number.isInteger(tail) && tail > 0 ? tail : TAIL_DEFAULT;
  if (n > TAIL_MAX) n = TAIL_MAX;
  if (unit === RUN_LANE) {
    if (!validRunID(run)) return null;
    return `/api/logs/run/${encodeURIComponent(run)}?tail=${n}&format=json`;
  }
  const u = LOG_UNITS.includes(unit) ? unit : LOG_UNITS[0];
  const streams = streamsFor(u);
  const s = streams.includes(stream) ? stream : streams[0];
  const format = s === "events" ? "&format=json" : "";
  return `/api/logs/${u}?stream=${s}&tail=${n}${format}`;
}

// Each request adds 500 lines, up to the server limit.
export function nextTail(current) {
  const base = Number.isInteger(current) && current > 0 ? current : TAIL_DEFAULT;
  return Math.min(base + TAIL_DEFAULT, TAIL_MAX);
}

/** Whether a wider window is still available — the affordance disables itself
 * at the clamp instead of asking for a tail the verb will trim back. */
export function canLoadOlder(current) {
  const base = Number.isInteger(current) && current > 0 ? current : TAIL_DEFAULT;
  return base < TAIL_MAX;
}

/** The run picker's option rows: the verb's own list of runs with captured
 * diagnostics, newest first by joining the spine's run rows (which arrive
 * oldest first). A run the spine does not know keeps its place with no label
 * — the file exists, whatever put it there. */
export function runOptions(logRuns, spineRuns) {
  const withLogs = Array.isArray(logRuns) ? logRuns.filter((r) => typeof r === "string" && r !== "") : [];
  const rows = new Map(withLogs.map((id) => [id, null]));
  for (const run of Array.isArray(spineRuns) ? spineRuns : []) {
    if (typeof run?.id === "string" && rows.has(run.id)) rows.set(run.id, run);
  }
  const order = [];
  for (const run of Array.isArray(spineRuns) ? spineRuns : []) {
    if (typeof run?.id === "string" && rows.has(run.id)) order.unshift(run.id);
  }
  for (const id of withLogs) {
    if (!order.includes(id)) order.push(id);
  }
  return order.map((id) => {
    const run = rows.get(id);
    const role = typeof run?.role === "string" ? run.role : null;
    const subject = typeof run?.subject === "number" ? `#${run.subject}` : null;
    const label = [id, role, subject].filter(Boolean).join(" · ");
    return { id, label };
  });
}

/** Marks each display line for the grep and the level facet. The display text
 * is the verb's rendering when the window carries one, else the raw line; the
 * level and event come from the ALIGNED record, never from reading the text.
 * Numbering is 1-based within the returned window and assigned before any
 * filtering, so a filtered view still says where in the tail a hit sits. */
function markLines(res, query) {
  const src = Array.isArray(res?.lines) ? res.lines : [];
  const rendered = Array.isArray(res?.rendered) ? res.rendered : null;
  const records = Array.isArray(res?.records) ? res.records : null;
  const q = typeof query === "string" ? query.trim().toLowerCase() : "";
  let matches = 0;
  const out = src.map((line, i) => {
    const raw = typeof line === "string" ? line : String(line ?? "");
    const record = records ? records[i] ?? null : null;
    const shown = localizedText(rendered && typeof rendered[i] === "string" ? rendered[i] : raw, record);
    const level = LOG_LEVELS.includes(record?.level) ? record.level : null;
    const match = q !== "" && shown.toLowerCase().includes(q);
    if (match) matches += 1;
    return { n: i + 1, text: shown, match, level, event: typeof record?.event === "string" ? record.event : null };
  });
  return { lines: out, matches, hasRecords: records !== null };
}

/** The verb's rendering with its leading UTC stamp shown in the browser zone.
 * The instant comes from the ALIGNED record, never from reading the text. A
 * line whose text does not open with that record's stamp shows verbatim. */
function localizedText(text, record) {
  const ts = typeof record?.ts === "string" ? record.ts : "";
  if (ts === "" || !text.startsWith(ts)) return text;
  const local = localStamp(ts);
  return local === "" ? text : local + text.slice(ts.length);
}

/** Per-level counts over the whole window, for the facet chips. Null when the
 * window carries no records (prose streams) — the facet has nothing to offer. */
export function levelCounts(view) {
  if (!view?.hasRecords) return null;
  const counts = Object.fromEntries(LOG_LEVELS.map((l) => [l, 0]));
  for (const line of view.allLines ?? []) {
    if (line.level !== null) counts[line.level] += 1;
  }
  return counts;
}

/** The view model for the pane: the window, the grep and level facet applied
 * over it, and the one sentence a non-ok state owes the operator. A log not
 * yet written is a STATE, not an error — the unit simply has not run under
 * supervision (or the run captured nothing). `levels` narrows to a subset of
 * LOG_LEVELS; a line with no record (a crash line, prose) always survives it —
 * those are exactly the interesting lines. */
export function logsView(res, { grep = "", levels = null } = {}) {
  const isRun = typeof res?.run_id === "string" && res.run_id !== "";
  const unit = isRun ? RUN_LANE : typeof res?.unit === "string" && res.unit !== "" ? res.unit : "?";
  const stream = isRun ? res.run_id : typeof res?.stream === "string" && res.stream !== "" ? res.stream : "?";
  const truncated = res?.truncated === true;

  if (!res || res.exists !== true) {
    return {
      state: "missing",
      unit,
      stream,
      truncated,
      total: 0,
      shown: 0,
      lines: [],
      allLines: [],
      hasRecords: false,
      note: isRun
        ? `no diagnostics were captured for run ${stream} — nothing wrote to its log bind`
        : `no ${unit} ${stream} log yet — the unit has not written under supervision`,
    };
  }

  const q = typeof grep === "string" ? grep.trim() : "";
  const { lines: marked, matches, hasRecords } = markLines(res, q);
  const total = marked.length;

  if (total === 0) {
    return { state: "empty", unit, stream, truncated, total: 0, shown: 0, lines: [], allLines: [], hasRecords, note: "the log is empty" };
  }

  const levelSet = hasRecords && Array.isArray(levels) && levels.length < LOG_LEVELS.length
    ? new Set(levels)
    : null;
  const afterLevels = levelSet === null ? marked : marked.filter((l) => l.level === null || levelSet.has(l.level));

  if (q === "" && levelSet === null) {
    return { state: "ok", unit, stream, truncated, total, shown: total, lines: marked, allLines: marked, hasRecords, note: "" };
  }

  const lines = q === "" ? afterLevels : afterLevels.filter((l) => l.match);
  let note = "";
  if (q !== "") {
    note = matches === 0 ? `no line matches "${q}"` : `${matches}/${total} lines match "${q}"`;
  } else if (lines.length === 0) {
    note = "no line at the picked levels";
  }
  return { state: "filtered", unit, stream, truncated, total, shown: lines.length, lines, allLines: marked, hasRecords, note };
}

/** `#logs/<unit>`, `#logs/<unit>/<stream>`, or `#logs/run/<run-id>` —
 * Pipeline's deep-link into a specific tail. Validated against the fixed
 * tables (and the run-id shape), because a hash is operator-supplied input and
 * the verb accepts no client-supplied paths. */
export function parseLogsHash(hash) {
  if (typeof hash !== "string") return null;
  const parts = hash.replace(/^#/, "").split("/");
  if (parts[0] !== "logs") return null;
  if (parts.length < 2 || parts.length > 3) return null;
  const unit = parts[1];
  if (unit === RUN_LANE) {
    if (parts.length !== 3 || !validRunID(parts[2])) return null;
    return { unit: RUN_LANE, stream: "events", run: parts[2] };
  }
  if (!LOG_UNITS.includes(unit)) return null;
  const streams = streamsFor(unit);
  const stream = parts.length === 3 ? parts[2] : streams[0];
  if (!streams.includes(stream)) return null;
  return { unit, stream, run: null };
}

/** The canonical hash for a picker state, so the address bar stays honest and
 * a copied URL lands on the same tail. */
export function logsHash({ unit, stream, run } = {}) {
  if (unit === RUN_LANE) {
    return validRunID(run) ? `#logs/run/${run}` : "#logs/run";
  }
  const u = LOG_UNITS.includes(unit) ? unit : LOG_UNITS[0];
  const streams = streamsFor(u);
  const s = streams.includes(stream) ? stream : streams[0];
  return `#logs/${u}/${s}`;
}

/** What the copy button puts on the clipboard: exactly the shown lines, with
 * no numbers and no markers — what is copied must be what the unit (or the
 * verb's renderer) wrote. */
export function copyText(view) {
  const lines = Array.isArray(view?.lines) ? view.lines : [];
  return lines.map((l) => (typeof l?.text === "string" ? l.text : "")).join("\n");
}

// Only structured records establish a run's relation to a process.
export function processRuns(records, runs = [], logRuns = [], projects = []) {
  const known = new Map(runs.map((run) => [run.id, run]));
  const captured = new Set(logRuns);
  const ids = [...new Set((records ?? []).slice().reverse()
    .map((record) => record?.run_id).filter(validRunID))];
  return ids.map((id) => {
    const run = known.get(id);
    return {
      id,
      label: [id, run?.project ? projectName(run.project, projects) : null].filter(Boolean).join(" · "),
      status: run?.outcome ?? (run && !run.ended_at ? "running" : captured.has(id) ? "recorded" : "no output"),
    };
  });
}
