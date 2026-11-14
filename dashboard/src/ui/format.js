// format.js — shared pure helpers for the panes. Spine timestamps are UTC
// "YYYY-MM-DD HH:MM:SS" with no zone marker, so they must be read AS UTC —
// letting the browser parse them locally would shift every age by the
// operator's offset. Every absolute stamp then renders in the browser zone,
// through this module and nowhere else.

/** Milliseconds since epoch for a spine timestamp, or NaN. */
export function utcMs(createdAt) {
  if (typeof createdAt !== "string" || createdAt === "") return NaN;
  const stamp = createdAt.replace(" ", "T");
  return Date.parse(/[zZ]$|[+-]\d{2}:\d{2}$/.test(stamp) ? stamp : stamp + "Z");
}

/** "3m" / "2h" / "5d" age of a spine timestamp against nowMs, or "". */
export function relativeAge(createdAt, nowMs) {
  const ms = utcMs(createdAt);
  if (Number.isNaN(ms)) return "";
  const mins = Math.max(0, Math.floor((nowMs - ms) / 60000));
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

/** Seconds since a spine timestamp, or null. Sub-minute precision is what a
 * heartbeat is read for — "0m" cannot tell a live lease from a stalled one. */
export function secondsSince(createdAt, nowMs) {
  const ms = utcMs(createdAt);
  if (Number.isNaN(ms)) return null;
  return Math.max(0, Math.round((nowMs - ms) / 1000));
}

/** A heartbeat/short age: "12s" under a minute, then the relative age. */
export function shortAge(createdAt, nowMs) {
  const secs = secondsSince(createdAt, nowMs);
  if (secs === null) return "";
  return secs < 60 ? `${secs}s` : relativeAge(createdAt, nowMs);
}

/** "3m ago" / "just now" / "" — the reading form of relativeAge. */
export function ageLabel(createdAt, nowMs) {
  return agoLabel(relativeAge(createdAt, nowMs));
}

/** A short age ("3m") as the dashboard states it: "just now" under one
 * minute, else "3m ago". */
export function agoLabel(age) {
  if (!age) return "";
  return age === "0m" ? "just now" : `${age} ago`;
}

/** The calendar fields of an instant in one zone, plus the zone's offset from
 * UTC in minutes at that instant. `zone` is an IANA name; absent, it is the
 * zone of the browser. */
function zoneFields(ms, zone) {
  const parts = new Intl.DateTimeFormat("en-US", {
    ...(zone === undefined ? {} : { timeZone: zone }),
    hourCycle: "h23",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).formatToParts(new Date(ms));
  const f = {};
  for (const p of parts) if (p.type !== "literal") f[p.type] = Number(p.value);
  const wall = Date.UTC(f.year, f.month - 1, f.day, f.hour, f.minute, f.second);
  f.offsetMinutes = Math.round((wall - Math.floor(ms / 1000) * 1000) / 60000);
  return f;
}

function pad2(n) {
  return String(n).padStart(2, "0");
}

/** The wall clock a header shows, in the operator's own zone. */
export function clockLabel(nowMs, zone) {
  const f = zoneFields(nowMs, zone);
  return `${pad2(f.hour)}:${pad2(f.minute)}`;
}

/** Spine stamp as "Aug 17 14:52" in the browser zone, for dense stream rows.
 * The header clock uses the same zone, so a row and the clock agree on the
 * hour and the date. */
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
export function stampLabel(createdAt, zone) {
  const ms = utcMs(createdAt);
  if (Number.isNaN(ms)) return "";
  const f = zoneFields(ms, zone);
  return `${MONTHS[f.month - 1]} ${pad2(f.day)} ${pad2(f.hour)}:${pad2(f.minute)}`;
}

/** Spine stamp as "Aug 17, 2026 14:52" in the browser zone, for a record
 * that a reader compares across months. */
export function dateTimeLabel(createdAt, zone) {
  const ms = utcMs(createdAt);
  if (Number.isNaN(ms)) return "";
  const f = zoneFields(ms, zone);
  return `${MONTHS[f.month - 1]} ${f.day}, ${f.year} ${pad2(f.hour)}:${pad2(f.minute)}`;
}

/** A log record stamp ("2026-09-08T05:21:46.377Z") in the browser zone with a
 * numeric offset: "2026-09-07T22:21:46.377-07:00". "" for an unreadable one. */
export function localStamp(ts, zone) {
  const ms = typeof ts === "string" && ts !== "" ? Date.parse(ts) : NaN;
  if (Number.isNaN(ms)) return "";
  const f = zoneFields(ms, zone);
  const sign = f.offsetMinutes < 0 ? "-" : "+";
  const abs = Math.abs(f.offsetMinutes);
  const millis = String(new Date(ms).getUTCMilliseconds()).padStart(3, "0");
  return `${f.year}-${pad2(f.month)}-${pad2(f.day)}T${pad2(f.hour)}:${pad2(f.minute)}:${pad2(f.second)}.${millis}` +
    `${sign}${pad2(Math.floor(abs / 60))}:${pad2(abs % 60)}`;
}

/** The pause state of one Project row, in the words the header uses. */
export function projectStateText(status, setupStatus) {
  if (status === "archived") return "Archived";
  if (setupStatus && setupStatus !== "ready") return "Initialization incomplete";
  if (status === "paused") return "Project paused";
  return "Pipeline active";
}

/** The Pause all control over the Project rows. Only a Project with a
 * complete setup can change its pause, so only those rows count. */
export function pauseAllView(projects) {
  const rows = (projects ?? []).filter((row) => row.status !== "archived");
  const ready = rows.filter((row) => !row.setup_status || row.setup_status === "ready");
  const paused = ready.filter((row) => row.status === "paused").length;
  const allPaused = ready.length > 0 && paused === ready.length;
  return {
    allPaused,
    available: ready.length >= 2,
  };
}

/** The lease state of one Project, from its lease badge. */
export function leaseStateText(badge) {
  if (badge.held === 0) return "Lease free";
  return badge.stale ? "Lease stale" : "Lease held";
}

export function projectName(id, projects = []) {
  return projects.find(row => row.id === id)?.title?.trim() || "Project name unavailable";
}

/** The name of a connection from the connection rows of the system status, or
 * null when the rows do not name it. */
export function connectionName(id, connections = []) {
  return (connections ?? []).find(row => row?.connection_id === id)?.name?.trim() || null;
}
