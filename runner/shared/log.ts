// log.ts — the structured-logging core: a record in, one JSON line out. Pure by
// construction (no fs, no Bun API, no DOM) so every rule below is a unit test
// with no scratch directory; the IO half lives in `log-sink.ts`.
//
// This module lives under `runner/` because `runner/` is the only subtree BOTH
// release manifests can carry: mc/deployment/host_release.go addresses files
// from the repo root, mc/deployment/runner_release.go addresses them from
// `runner/` and cannot express `../`. Moving it makes the runners import a file
// that is not in their image.
//
// The level is a literal chosen by the author at the call site and frozen in
// source. This module contains NO mapping from a refusal code, exit status, or
// harness output to a level, and exposes no helper that could compute one.
// The resident passes every code through verbatim and derives no meaning from
// it (resident/src/effects.ts). That stays intact: a refusal's `code` and
// `consequence` are carried verbatim as context fields, and the site's level
// is chosen by the author of that site. A levelFor(code) function, a SEVERITY
// map, and an event name minted per refusal code are the same violation
// arriving by three doors.

export type Level = "error" | "warn" | "info" | "debug";

/** Ordered error < warn < info < debug. The order of this array IS the ordering
 * relation — `levelEnabled` compares indices, so nothing may be reordered
 * without changing what a configured level admits. */
export const LEVELS = ["error", "warn", "info", "debug"] as const;

// Event grammar: ^[a-z][a-z0-9]*\.[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)?$
//
// Rules for changing this list:
//   - A new name lands in the same changeset as its first call site. A name
//     with no caller is a label nobody has agreed to yet.
//   - Reuse a name when the *situation* matches and only the wording differs.
//     Variation belongs in `msg`, never in a second name: the registry is what
//     a query groups by, and two names for one situation split every count.
//   - No identifiers in a name. Run ids, container names, task ids and paths
//     are context fields; a name that carries one has a cardinality of
//     "however many runs there have ever been".
//   - NEVER one name per refusal code. The code is carried verbatim as a
//     context field on `tick.dispatch_refused` (and its peers). Minting a name
//     per code is the interpretation this module's header forbids, arriving
//     through the registry instead of through a mapping table.
export const EVENTS = [
  "boot.started", "boot.stopping", "boot.stop_failed", "boot.aborted", "boot.config_invalid", "boot.canary_passed",
  "boot.canary_failed", "boot.guard_started", "boot.guard_unavailable",
  "boot.snapshot_committed", "boot.projector_live", "boot.grant_store_invalid",
  "boot.catalog_invalid",
  "tick.started", "tick.completed", "tick.failed", "tick.idle", "tick.dispatch_refused",
  "tick.effect_unknown", "tick.receipt_failed", "tick.skipped",
  "tick.dispatch_failed", "tick.nudged", "tick.nudge_refused",
  "spawn.refused", "spawn.envelope_written", "spawn.recheck_refused",
  "spawn.projection_failed", "spawn.create_failed", "spawn.started", "spawn.start_failed",
  "spawn.unwound", "spawn.gate_failed",
  "reap.started", "reap.stop_failed", "reap.discard_failed", "reap.completed",
  "reap.skipped", "reap.refused",
  "sweep.reclaimed", "sweep.skipped", "sweep.failed",
  "artifacts.expiry_failed",
  "evaluation.capture_failed",
  "repository.start_failed", "repository.owner_refused", "repository.heartbeat_failed",
  "repository.stop_failed", "repository.follow_failed", "repository.execution_failed",
  "operator.request_failed", "operator.unavailable",
  "runner.skill_unavailable",
  "trace.copy_failed", "trace.sessions_failed",
  "agentsview.available", "agentsview.unavailable", "agentsview.request_failed", "agentsview.health_failed",
  "wiki.exported", "wiki.export_failed",
  "vision.save_failed", "vision.queue_failed",
  "collie.wake_refused", "collie.created", "collie.create_failed", "collie.envelope_refused",
  "collie.bind_refused", "collie.adopt_refused", "collie.started", "collie.start_failed",
  "collie.idle_exit", "collie.stop_skipped", "collie.stop_failed", "collie.remove_refused",
  "collie.remove_failed", "collie.draft_stopped",
  // Best-effort removal of launch residue: the work the run asked for is done
  // or refused either way, and the metronome's sweep is the backstop. Never an
  // error, because nothing the operator asked for failed to happen.
  "cleanup.failed",
  "credential.projected", "credential.projection_failed", "credential.refresh_failed",
  "credential.acceptance_armed", "credential.acceptance_refused", "credential.grant_expired",
  "discord.connected", "discord.disconnected", "discord.reconnect_scheduled",
  "discord.inbound_ignored", "discord.delivery_failed", "discord.typing_failed",
  "liveness.probe_failed", "liveness.degraded", "liveness.recovered",
  "outbox.drain_failed", "outbox.ack_failed",
  "attachment.tree_prepared", "attachment.tree_refused", "attachment.terminal_swept",
  "runner.started", "runner.turn_started", "runner.turn_completed", "runner.harness_failed",
  "runner.skill_skipped", "runner.skill_installed", "runner.skill_refused", "runner.session_lost", "runner.recovery_unconfirmed",
  "runner.exited",
  "runner.mc_failed", "runner.store_preamble_refused",
  "adapter.launch_failed", "adapter.stream_error", "adapter.session_captured",
  "cli.invoked", "cli.verb_refused",
  "dash.request_failed", "dash.render_refused", "dash.nudge_failed", "dash.wiki_failed", "dash.wiki_library_remote", "dash.mc_completed",
  "log.sink_failed",
] as const;

/** The grammar the registry is checked against. Exported for the test and for
 * any tool that has to validate a name outside this module. */
export const EVENT_GRAMMAR = /^[a-z][a-z0-9]*\.[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)?$/;

export type Event = (typeof EVENTS)[number];

/** Scalars only. A nested object in a log line is a schema nobody declared:
 * it defeats the flat `key=value` grep that is the whole point of the format,
 * and its depth is unbounded where the line length is not. */
export type Ctx = Readonly<Record<string, string | number | boolean | null>>;

export interface LogRecord {
  ts: string;
  level: Level;
  comp: string;
  event: string;
  msg: string;
  [k: string]: unknown;
}

export interface Sink {
  write(line: string): void;
}

/** Every record names an event from the registry above. There is no free-text
 * form: a line with no event is a line no reader can select on, which is the
 * state this module exists to end. */
export interface Logger {
  error(event: Event, msg: string, ctx?: Ctx): void;
  warn(event: Event, msg: string, ctx?: Ctx): void;
  info(event: Event, msg: string, ctx?: Ctx): void;
  debug(event: Event, msg: string, ctx?: Ctx): void;
  child(ctx: Ctx): Logger;
}

/** Keys the record owns. A context field may not take one of these names: the
 * five head keys are the only thing a reader can rely on being present and
 * meaning what they say. */
const RESERVED: readonly string[] = ["ts", "level", "comp", "event", "msg"];

const MSG_MAX_BYTES = 2048;
const LINE_MAX_BYTES = 8192;

const ENC = new TextEncoder();
const DEC = new TextDecoder();

type Scalar = string | number | boolean | null;

function byteLength(s: string): number {
  return ENC.encode(s).length;
}

function isScalar(v: unknown): v is Scalar {
  if (v === null) return true;
  const t = typeof v;
  if (t === "string" || t === "boolean") return true;
  // NaN and the infinities are `typeof "number"` but JSON-stringify to `null`,
  // which reads downstream as a deliberately-null field rather than as the
  // arithmetic accident it actually was. Drop them like any other non-scalar.
  return t === "number" && Number.isFinite(v);
}

/** Cuts `s` to at most `max` BYTES, appending "…" when anything was removed.
 * Bytes, not code units: the caps exist to bound what reaches a file and a
 * three-byte character has to count as three. A cut that lands inside a
 * multi-byte sequence loses that one character rather than emitting the
 * replacement char, and the result is re-measured because a lone continuation
 * byte decodes WIDER than it was. */
function capBytes(s: string, max: number): string {
  const bytes = ENC.encode(s);
  if (bytes.length <= max) return s;
  if (max <= 3) return "…".slice(0, max > 0 ? 1 : 0);
  for (let room = max - 3; room > 0; room--) {
    const out = DEC.decode(bytes.subarray(0, room)).replace(/\uFFFD+$/, "") + "…";
    if (byteLength(out) <= max) return out;
  }
  return "…";
}

/** Resolves a configured level from an environment string. NEVER throws:
 * a process that will not start because MC_LOG_LEVEL says "verbose" has traded
 * a wrong log level for an outage. */
export function parseLevel(raw: string | undefined, fallback: Level = "info"): Level {
  if (typeof raw !== "string") return fallback;
  const want = raw.trim().toLowerCase();
  return (LEVELS as readonly string[]).includes(want) ? (want as Level) : fallback;
}

export function levelEnabled(configured: Level, at: Level): boolean {
  return LEVELS.indexOf(at) <= LEVELS.indexOf(configured);
}

/** One record, one line, byte-stable: fixed head order then context keys sorted
 * alphabetically, so two lines describing the same thing differ only where the
 * facts differ and a diff of two runs is readable. */
export function serialize(rec: LogRecord, maxBytes: number = LINE_MAX_BYTES): string {
  let msg = typeof rec.msg === "string" ? rec.msg : String(rec.msg);
  msg = capBytes(msg, MSG_MAX_BYTES);

  // Defence in depth: makeLogger already sanitized, but `serialize` is exported
  // and the type says scalars only to a checker JS callers do not run.
  const extras = new Map<string, Scalar>();
  let dropped = 0;
  for (const k of Object.keys(rec)) {
    if (RESERVED.includes(k)) continue;
    const v = rec[k];
    if (isScalar(v)) extras.set(k, v);
    else dropped++;
  }
  if (dropped > 0) {
    const already = extras.get("dropped_fields");
    extras.set("dropped_fields", (typeof already === "number" ? already : 0) + dropped);
  }

  const build = (): string => {
    const fields = [
      `"ts":${JSON.stringify(rec.ts)}`,
      `"level":${JSON.stringify(rec.level)}`,
      `"comp":${JSON.stringify(rec.comp)}`,
      `"event":${JSON.stringify(rec.event)}`,
      `"msg":${JSON.stringify(msg)}`,
    ];
    for (const k of [...extras.keys()].sort()) {
      fields.push(`${JSON.stringify(k)}:${JSON.stringify(extras.get(k))}`);
    }
    return `{${fields.join(",")}}\n`;
  };

  let line = build();
  if (byteLength(line) <= maxBytes) return line;

  // Over the line cap. Shed the largest context values first: one oversized
  // field (a pasted diff, a stack) is the usual cause, and dropping it saves
  // every other field. `ts/level/comp/event` are never candidates — a line that
  // cannot say when, how loud, from where and about what is not worth keeping.
  extras.set("truncated", true);
  const candidates = [...extras.keys()]
    .filter((k) => k !== "truncated")
    .sort((a, b) => {
      const d = byteLength(JSON.stringify(extras.get(b))) - byteLength(JSON.stringify(extras.get(a)));
      return d !== 0 ? d : a < b ? -1 : 1;
    });
  for (const k of candidates) {
    extras.delete(k);
    line = build();
    if (byteLength(line) <= maxBytes) return line;
  }

  // Nothing but the head is left and it is still too long, so `comp`/`event`
  // are pathological or `msg` alone exceeds the cap. `msg` is the only field
  // still allowed to give.
  while (byteLength(line) > maxBytes) {
    const have = byteLength(msg);
    if (have <= 1) break;
    const room = Math.max(1, have - (byteLength(line) - maxBytes));
    const shorter = capBytes(msg, room);
    if (shorter === msg) break; // nothing left to give; the head is not negotiable
    msg = shorter;
    line = build();
  }
  return line;
}

/** The human-facing rendering of the same record: one line, same field order,
 * for a terminal that is being read rather than queried. */
export function renderText(rec: LogRecord): string {
  const level = String(rec.level).toUpperCase().padEnd(5, " ");
  const pairs = Object.keys(rec)
    .filter((k) => !RESERVED.includes(k) && isScalar(rec[k]))
    .sort()
    .map((k) => `${k}=${String(rec[k])}`);
  const ctx = pairs.length > 0 ? ` ${pairs.join(" ")}` : "";
  return `${rec.ts} ${level} ${rec.comp} ${rec.event}${ctx} — ${rec.msg}`;
}

export function makeLogger(o: { comp: string; level: Level; sink: Sink; now?: () => number }): Logger {
  const now = o.now ?? Date.now;

  const emit = (base: Ctx, level: Level, event: string, msg: string, ctx?: Ctx): void => {
    // Below the configured level nothing is built and the sink is never
    // touched: a `debug` call site in a hot loop must cost a comparison.
    if (!levelEnabled(o.level, level)) return;
    const rec: LogRecord = {
      ts: new Date(now()).toISOString(),
      level,
      comp: o.comp,
      event,
      msg,
    };
    let dropped = 0;
    // Child context first, per-call context second: the nearer call wins.
    for (const source of [base, ctx]) {
      if (!source) continue;
      for (const k of Object.keys(source)) {
        const v = (source as Record<string, unknown>)[k];
        if (RESERVED.includes(k) || !isScalar(v)) {
          dropped++;
          continue;
        }
        rec[k] = v;
      }
    }
    if (dropped > 0) rec.dropped_fields = dropped;
    // Exactly one write per record, newline included: downstream this is one
    // write(2) on an O_APPEND fd, which is what stops concurrent appenders from
    // interleaving halves of two lines.
    o.sink.write(serialize(rec));
  };

  const make = (base: Ctx): Logger => {
    const self = {} as Logger;
    self.error = (event, msg, ctx) => emit(base, "error", event, msg, ctx);
    self.warn = (event, msg, ctx) => emit(base, "warn", event, msg, ctx);
    self.info = (event, msg, ctx) => emit(base, "info", event, msg, ctx);
    self.debug = (event, msg, ctx) => emit(base, "debug", event, msg, ctx);
    self.child = (ctx) => make({ ...base, ...ctx });
    return self;
  };

  return make({});
}

/** A logger that keeps what it wrote. Records are parsed back out of the
 * serialized line rather than captured before it, so a test asserting on
 * `records` is asserting on what the file would actually hold. */
export function makeTestLogger(comp = "test"): Logger & { records: LogRecord[]; lines: string[] } {
  const lines: string[] = [];
  const records: LogRecord[] = [];
  const logger = makeLogger({
    comp,
    level: "debug",
    sink: {
      write(line) {
        lines.push(line);
        records.push(JSON.parse(line) as LogRecord);
      },
    },
  });
  return Object.assign(logger, { records, lines });
}
