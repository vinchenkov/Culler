import { ageLabel, connectionName, stampLabel, utcMs } from "./format.js";

/** The dispatch errors: refusals, and the start and end of each connection wait. */
const ERROR_KINDS = new Set(["dispatch.health", "dispatch.wait_started", "dispatch.wait_ended"]);

/** Splits one activity read into {events, errors}. */
export function splitStreams(rows) {
  const all = rows ?? [];
  return {
    errors: all.filter((r) => ERROR_KINDS.has(r.kind)),
    events: all.filter((r) => !ERROR_KINDS.has(r.kind)),
  };
}

/** Display grouping only — a log line carries no severity, so these tones say
 * which part of the system spoke, not how bad it is. */
function kindTone(kind) {
  const k = typeof kind === "string" ? kind : "";
  if (k === "dispatch.health" || k === "dispatch.wait_started") return "rust";
  if (k.startsWith("collie.")) return "sky";
  if (k.startsWith("task.") || k.startsWith("packet.")) return "leaf";
  return "stone";
}

/** The subject of one row in words. `subject_kind` says whether the subject
 * identifies a run, a task, a connection, or a Project. */
export function subjectLabel(row, { connections = [], projects = [] } = {}) {
  const subject = row?.subject;
  if (subject === null || subject === undefined || subject === "") return null;
  if (row.subject_kind === "run") return `run ${subject}`;
  if (row.subject_kind === "task") return `task #${subject}`;
  if (row.subject_kind === "connection") {
    const name = connectionName(subject, connections);
    return name ? `connection ${name}` : "Connection name unavailable";
  }
  if (row.subject_kind === "project") {
    const title = (projects ?? []).find((project) => project.id === subject)?.title?.trim();
    return title ? `Project ${title}` : "Project name unavailable";
  }
  return String(subject);
}

/** One stream as row facts, in the verb's own order (newest first). `names`
 * holds the connection rows and the Project rows for the subject labels. */
export function activityRows(rows, nowMs, names = {}) {
  return (rows ?? []).map((r) => ({
    id: r.id ?? null,
    kind: r.kind ?? "",
    actor: r.actor ?? "system",
    subject: subjectLabel(r, names),
    detail: r.detail ?? "",
    age: ageLabel(r.created_at, nowMs),
    stamp: stampLabel(r.created_at),
    tone: kindTone(r.kind),
  }));
}

/** The kinds the filter box suggests. The filter itself is the verb's own
 * `?kind=` parameter, so this can only ever offer what this read contained. */
export function kindOptions(rows) {
  const seen = new Set();
  for (const r of rows ?? []) {
    if (typeof r.kind === "string" && r.kind !== "") seen.add(r.kind);
  }
  return [...seen].sort();
}

/** Sort key for an id: ids ascend with time, so they break stamp ties. */
function idKey(row) {
  const id = row.id;
  if (id === null || id === undefined) return -Infinity;
  const n = Number(id);
  return Number.isNaN(n) ? -Infinity : n;
}

/** Newest first: by timestamp, then by id. An unparsable stamp sorts last —
 * a row with no readable time cannot claim to be the newest thing. */
function byNewest(a, b) {
  const ta = utcMs(a.created_at);
  const tb = utcMs(b.created_at);
  const na = Number.isNaN(ta);
  const nb = Number.isNaN(tb);
  if (na !== nb) return na ? 1 : -1;
  if (!na && ta !== tb) return tb - ta;
  const ia = idKey(a);
  const ib = idKey(b);
  if (ia === ib) return 0;
  return ib > ia ? 1 : -1;
}

/** The live tail: the union of what is on screen and what the poll returned,
 * deduped, newest first. A poll returns a window that overlaps the rows the
 * operator is reading, so prepending it blind would show them twice. */
export function mergeRows(existing, incoming) {
  const out = [];
  const byID = new Set();
  const byShape = new Set();
  for (const r of [...(existing ?? []), ...(incoming ?? [])]) {
    if (r === null || r === undefined) continue;
    const id = r.id;
    if (id !== null && id !== undefined) {
      const key = String(id);
      if (byID.has(key)) continue;
      byID.add(key);
    } else {
      // An id-less row (an older spine) can only be identified by its content.
      const key = JSON.stringify(r);
      if (byShape.has(key)) continue;
      byShape.add(key);
    }
    out.push(r);
  }
  return out.sort(byNewest);
}

/** The ids the poll actually added — app.js flashes exactly these rows. */
export function newRowIDs(existing, incoming) {
  const known = new Set();
  for (const r of existing ?? []) {
    if (r && r.id !== null && r.id !== undefined) known.add(String(r.id));
  }
  const out = [];
  for (const r of incoming ?? []) {
    if (!r || r.id === null || r.id === undefined) continue;
    const key = String(r.id);
    if (known.has(key)) continue;
    known.add(key);
    out.push(r.id);
  }
  return out;
}

/** The header facts for one stream card: how much history is on screen, and
 * how far back it reaches. */
export function streamStats(rows, nowMs) {
  const all = rows ?? [];
  const actors = new Set();
  let newest = null;
  let oldest = null;
  for (const r of all) {
    actors.add(r.actor ?? "system");
    const ms = utcMs(r.created_at);
    if (Number.isNaN(ms)) continue;
    if (newest === null || ms > utcMs(newest)) newest = r.created_at;
    if (oldest === null || ms < utcMs(oldest)) oldest = r.created_at;
  }
  return {
    count: all.length,
    newest: newest === null ? "" : ageLabel(newest, nowMs),
    oldest: oldest === null ? "" : ageLabel(oldest, nowMs),
    actors: actors.size,
  };
}
