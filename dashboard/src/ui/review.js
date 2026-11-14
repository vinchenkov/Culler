// review.js — pure view models for the Review page. `mc packet list` states
// the review state of each latest packet; the Live queue and the ledger are
// browser filters over that one read. Decision payloads are shaped here so the
// affordance can follow the verb's reason rules while the verb itself stays
// the authority on them.

import { relativeAge, utcMs, dateTimeLabel } from "./format.js";

/** A packet older than this is queue pressure, not queue flow. */
const STALE_MS = 24 * 60 * 60 * 1000;

const DECISIONS = ["approve", "revise", "cancel"];

/** The review states whose task holds a review slot. */
export const LIVE_STATES = ["awaits_decision", "approved", "returned"];

/** The ledger filters, in control order. The first is the default. */
export const LEDGER_FILTERS = ["approved", "cancelled", "all"];
const LEDGER_STATES = { approved: ["merged"], cancelled: ["cancelled"], all: ["merged", "cancelled"] };

const byTask = (a, b) => (a.task_id ?? 0) - (b.task_id ?? 0);
const byAge = (a, b) => (utcMs(a.created_at) || 0) - (utcMs(b.created_at) || 0) || byTask(a, b);

/** The Live queue: packets that await a decision first, then the approved and
 * returned packets. Each group reads oldest first, because that is the
 * decision owed longest. */
export function livePackets(packets) {
  const rows = (packets ?? []).filter((p) => LIVE_STATES.includes(p?.review_state));
  const awaiting = rows.filter((p) => p.review_state === "awaits_decision").sort(byAge);
  const others = rows.filter((p) => p.review_state !== "awaits_decision").sort(byAge);
  return [...awaiting, ...others];
}

/** The ledger: the packets with a final decision, newest decision first. An
 * unknown filter is the default filter. */
export function ledgerPackets(packets, filter) {
  const states = LEDGER_STATES[filter] ?? LEDGER_STATES[LEDGER_FILTERS[0]];
  return (packets ?? [])
    .filter((p) => states.includes(p?.review_state))
    .sort((a, b) => (utcMs(b.decided_at) || 0) - (utcMs(a.decided_at) || 0) || byTask(b, a));
}

const RETURN_CAUSES = {
  conflict: "A merge conflict returned the task to the Worker for a merge repair.",
};

const STATE_TAGS = {
  approved: ["Approved", "leaf"],
  returned: ["Returned to the pipeline", "sky"],
  merged: ["Approved", "leaf"],
  cancelled: ["Cancelled", "rust"],
};

/** The cause of a return to the pipeline, or "" when the spine records none. */
function causeText(row) {
  if (row.return_cause === "revise") {
    return typeof row.reason === "string" && row.reason !== "" ? `Revise: ${row.reason}` : "Revise.";
  }
  return RETURN_CAUSES[row.return_cause] ?? "";
}

/** The row line under the task title: what happened to the packet after review. */
function detailText(row) {
  const reason = typeof row.reason === "string" ? row.reason : "";
  switch (row.review_state) {
    case "approved":
      return typeof row.merge_state_text === "string" ? row.merge_state_text : "";
    case "returned":
      return causeText(row);
    case "merged":
      return `Merged into ${row.destination_branch ?? "the destination branch"}.`;
    case "cancelled":
      return reason === "" ? "" : `Reason: ${reason}`;
    default:
      return "";
  }
}

/** The reader line that states the decision and its local date and time. */
function decisionText(row, decidedAt) {
  const detail = detailText(row);
  switch (row.review_state) {
    case "approved":
      return detail === "" ? `Approved on ${decidedAt}.` : `Approved on ${decidedAt}. ${detail}`;
    case "returned":
      return detail === "" ? "Returned to the pipeline." : `Returned to the pipeline. ${detail}`;
    case "merged":
      return `Approved on ${decidedAt}. Merged into ${row.destination_branch ?? "the destination branch"} at ${row.merge_commit ?? ""}.`;
    case "cancelled":
      return detail === "" ? `Cancelled on ${decidedAt}.` : `Cancelled on ${decidedAt}. ${detail}`;
    default:
      return "";
  }
}

/** Each packet shows its review state, its age or its decision time, and
 * whether the operator can decide it. */
export function packetView(p, nowMs, zone) {
  const row = p ?? {};
  const ms = utcMs(row.created_at);
  const ageMs = Number.isNaN(ms) ? 0 : nowMs - ms;
  const state = typeof row.review_state === "string" ? row.review_state : "";
  const retention = row.artifact_retention ?? {};
  const ready = row.ready === undefined || row.ready === 1;
  const decidedAt = dateTimeLabel(row.decided_at, zone);
  const [label, tone] = STATE_TAGS[state] ?? ["", ""];
  return {
    taskID: row.task_id ?? null,
    title: typeof row.title === "string" ? row.title : "",
    state,
    live: LIVE_STATES.includes(state),
    label,
    tone,
    detail: detailText(row),
    // `mc packet list` states why the merge of an approved packet is not complete.
    mergeState: state === "approved" ? detailText(row) : "",
    decision: decisionText(row, decidedAt),
    decidedAt,
    age: relativeAge(row.created_at, nowMs),
    ageMs,
    // Age-in-queue is the queue's own pressure signal: a day-old packet must
    // read as loud without the operator doing arithmetic.
    ageTone: ageMs >= STALE_MS ? "rust" : "leaf",
    blocked: row.blocked === 1,
    blockedReason: typeof row.blocked_reason === "string" ? row.blocked_reason : "",
    ready,
    decidable: state === "awaits_decision" && ready,
    expired: retention.status === "expired",
    expiredAt: dateTimeLabel(retention.expired_at, zone),
    thesis: typeof row.thesis === "string" && row.thesis !== "" ? row.thesis : "(no thesis rendered)",
    // The opening text of a task that the Strategist proposed holds the Packager's statement
    // and then the Strategic rationale.
    strategist: row.origin === "autonomous",
    openingLabel: openingLabel(row),
    hasRender: typeof row.render_path === "string" && row.render_path !== "",
  };
}

/** The heading of the opening text of a packet. The origin of the task
 * selects it. */
export function openingLabel(row) {
  return row?.origin === "autonomous" ? "Strategic rationale" : "Thesis";
}

/** The rows one view shows. An unknown view is the Live queue, never an
 * empty pane. */
export function packetsFor(packets, view, filter, nowMs, zone) {
  const rows = view === "ledger" ? ledgerPackets(packets, filter) : livePackets(packets);
  return rows.map((p) => packetView(p, nowMs, zone));
}

/** The place of one packet in the current packet list: the position text and
 * the task of each neighbour. A packet absent from the list has neither. */
export function readerPosition(packets, view, filter, taskID) {
  const order = (view === "ledger" ? ledgerPackets(packets, filter) : livePackets(packets)).map((p) => p.task_id);
  const index = order.indexOf(taskID);
  if (index === -1) return { position: "", previous: null, next: null };
  return {
    position: `${index + 1} of ${order.length}`,
    previous: index > 0 ? order[index - 1] : null,
    next: index + 1 < order.length ? order[index + 1] : null,
  };
}

/** The task of the first packet of the current packet list, or null for an
 * empty list. */
export function firstPacket(packets, view, filter) {
  const rows = view === "ledger" ? ledgerPackets(packets, filter) : livePackets(packets);
  return rows[0]?.task_id ?? null;
}

/** The first 7 characters of a commit identifier, or "" without one. */
export function shortCommit(commit) {
  return typeof commit === "string" ? commit.slice(0, 7) : "";
}

/** What the report area shows for one packet. `read` is the state of the
 * report read: "loading", "report", or "failed". The report area shows the
 * opening text only when it shows no report, and it shows the criteria only
 * for an expired report. */
export function reportArea(view, read) {
  const kind = view?.expired ? "expired"
    : !view?.hasRender ? "missing"
      : read === "report" || read === "failed" ? read : "loading";
  return { kind, thesis: kind !== "report" && kind !== "loading", records: kind === "expired" };
}

/** The counts of the Review page for the packets of one Project. */
export function queueStats(packets, settings) {
  const rows = packets ?? [];
  const awaiting = livePackets(rows).filter((p) => p.review_state === "awaits_decision");
  const cap = typeof settings?.review_wip_cap === "number" ? settings.review_wip_cap : null;
  const slots = rows.filter((p) => LIVE_STATES.includes(p?.review_state)).length;
  return {
    awaiting: awaiting.length,
    slots,
    cap,
    capKnown: cap !== null,
    over: cap !== null && slots > cap,
    approved: rows.filter((p) => p?.review_state === "merged").length,
    cancelled: rows.filter((p) => p?.review_state === "cancelled").length,
  };
}

/** The empty state of one ledger filter. */
export function ledgerEmptyText(filter) {
  if (filter === "cancelled") return "No cancelled packets yet.";
  if (filter === "all") return "No decided packets yet.";
  return "No approved packets yet.";
}

/** Whether the reason field is offered. This drives the affordance only —
 * The verb stays the authority when the operator is creative. */
export function reasonRequired(decision) {
  return decision === "revise" || decision === "cancel";
}

/** The POST body for `mc packet decide`, or null when there is nothing to
 * send. A blank reason on revise/cancel is an abandoned decision, not an empty
 * reason. The reason is trimmed and otherwise rides untouched — they are the
 * operator's words, quoted back to the agent that must act on them. */
export function decisionPayload(decision, reason) {
  if (!DECISIONS.includes(decision)) return null;
  // approve takes no reason: the verb forbids one, so never smuggle it along.
  if (!reasonRequired(decision)) return { decision };
  const text = typeof reason === "string" ? reason.trim() : "";
  if (text === "") return null;
  return { decision, reason: text };
}

/** How the reader renders one packet's bytes. The server's content type (from
 * the recorded filename) is the primary signal; a sniff catches the packet
 * whose author wrote HTML under a bare name. A packet is HTML. Markdown is
 * the legacy shape and plain text the honest floor. */
export function renderMode(contentType, body) {
  const type = typeof contentType === "string" ? contentType.toLowerCase() : "";
  if (type.includes("text/html")) return "html";
  if (type.includes("text/markdown")) return "markdown";
  const head = (typeof body === "string" ? body : "").trimStart().slice(0, 256).toLowerCase();
  if (head.startsWith("<!doctype html") || head.startsWith("<html")) return "html";
  return "text";
}

// The packet frame's own CSP, injected as the wrapper's first head element so
// it governs everything after it. `default-src 'none'` means an agent-authored
// page can load NOTHING over the network — no exfiltration channel, no
// operator-IP leak, no external dependency that rots — which is also the
// inline-media packaging rule enforced at read time. Scripts stay
// off twice over: nothing here allows them, and the host iframe's sandbox
// attribute (no allow-scripts, no allow-same-origin) is the second lock.
const FRAME_CSP =
  "default-src 'none'; img-src data: blob:; media-src data: blob:; style-src 'unsafe-inline'; font-src data:;";

// The frame policy permits embedded fonts. The dashboard supplies its typeface
// after it reads the font file.
let frameFontFace = "";
const FONT_DATA = /^data:font\/woff2;base64,[A-Za-z0-9+/]+=*$/;
export function setFrameFont(dataURL) {
  frameFontFace = FONT_DATA.test(dataURL)
    ? `@font-face { font-family: "Figtree"; src: url(${dataURL}) format("woff2"); font-weight: 300 900; }`
    : "";
}

// Inline priority preserves the page style when a packet supplies document styles.
// The parser preserves these attributes when it merges a full packet document.
const FRAME_PAGE_STYLE = "background: #fdfdfe !important; color: #172f47 !important; color-scheme: light !important;";
const FRAME_BODY_STYLE = `${FRAME_PAGE_STYLE} margin: 0 !important; padding: 20px 24px !important; ` +
  "font: 14px/1.62 Figtree, 'Avenir Next', Avenir, 'Segoe UI', system-ui, sans-serif !important;";

const FRAME_BASE_STYLE = `
  body { overflow-wrap: anywhere; }
  main, section, nav { min-width: 0; }
  h1, h2, h3 { font-weight: 700; line-height: 1.3; margin: 20px 0 8px; letter-spacing: -0.005em; }
  h1 { font-size: 21px; } h2 { font-size: 16px; } h3 { font-size: 14px; }
  th { font-size: 11px; text-transform: uppercase; letter-spacing: 0.07em; color: #53667a; }
  h1:first-child, h2:first-child { margin-top: 0; }
  [id] { scroll-margin-top: 12px; }
  p, ul, ol, table { margin: 0 0 10px; }
  code, pre { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; }
  code { background: rgba(23, 47, 71, 0.07); border-radius: 4px; padding: 1px 5px; }
  pre { background: #eef1f4; border: 1px solid rgba(23, 47, 71, 0.12); border-radius: 9px; padding: 10px 12px; overflow-x: auto; }
  pre code { background: none; padding: 0; }
  img, video { max-width: 100%; height: auto; border-radius: 8px; }
  svg { max-width: 100%; height: auto; }
  table { border-collapse: collapse; width: 100%; font-size: 12.5px; }
  th, td { text-align: left; padding: 6px 10px; border-bottom: 1px solid rgba(23, 47, 71, 0.12); }
  a[href] { color: #1f5078; }
  hr { border: 0; border-top: 1px solid rgba(23, 47, 71, 0.12); margin: 14px 0; }
  blockquote { margin: 0 0 10px; padding-left: 12px; border-left: 3px solid rgba(23, 47, 71, 0.2); }
`;

/** The srcdoc for the sandboxed packet frame: our own skeleton whose head
 * carries the CSP and the base styles, with the packet's bytes as the body.
 * A packet that arrives as a full document still renders — the parser folds
 * nested html/head/body tags away — and its head content lands after the CSP,
 * so the policy already governs it. This string is the ONE deliberate seam
 * where agent bytes become markup, and it exists only because the frame is a
 * separate, sandboxed, network-dead document — never this page's DOM.
 *
 * A srcdoc document resolves "#id" against the dashboard address, so an
 * in-page link would load the dashboard inside the frame. The base element
 * makes "#id" resolve to about:srcdoc#id, which scrolls inside the frame. */
export function packetDocument(body) {
  const bytes = typeof body === "string" ? body : "";
  return (
    `<!doctype html><html style="${FRAME_PAGE_STYLE} margin: 0 !important; padding: 0 !important;"><head><meta charset="utf-8">` +
    `<meta http-equiv="Content-Security-Policy" content="${FRAME_CSP}">` +
    `<base href="about:srcdoc">` +
    `<style>${frameFontFace}${FRAME_BASE_STYLE}</style></head><body style="${FRAME_BODY_STYLE}">` +
    bytes +
    `</body></html>`
  );
}

/** How a report frame treats one link address: "top" for an empty address,
 * "page" for a link inside the report, "tab" for an absolute address, and
 * "none" for a script address or a relative address. A relative address
 * resolves against about:srcdoc, so it leads nowhere. */
export function linkRole(href) {
  const value = String(href ?? "").replace(/[\u0000- ]/g, "");
  if (value === "") return "top";
  if (value.startsWith("#")) return "page";
  if (/^(javascript|vbscript):/i.test(value)) return "none";
  if (/^[a-z][a-z0-9+.-]*:/i.test(value)) return "tab";
  return "none";
}

const XLINK = "http://www.w3.org/1999/xlink";

/** Applies the link rules to a parsed report document. Each link names its
 * own target, so a base target in the report changes nothing. An SVG link can
 * name its address in the XLink namespace; the rules move that address to
 * href. A template loses its shadow root mode, so the frame attaches no
 * shadow root that hides a link from these rules. An SVG animation of a link
 * address or target is removed, because it changes the link after the rules. */
export function applyLinkRules(doc) {
  for (const template of doc.querySelectorAll("template")) {
    template.removeAttribute("shadowrootmode");
    template.removeAttribute("shadowroot");
  }
  for (const animation of doc.querySelectorAll("set, animate")) {
    const name = String(animation.getAttribute("attributeName") ?? "").trim().toLowerCase();
    if (name === "target" || name.split(":").pop() === "href") animation.remove();
  }
  for (const link of doc.querySelectorAll("a, area")) {
    const href = link.getAttribute("href") ?? link.getAttributeNS(XLINK, "href");
    if (href === null) continue;
    link.removeAttributeNS(XLINK, "href");
    const role = linkRole(href);
    if (role === "none") {
      link.removeAttribute("href");
      link.removeAttribute("target");
    } else if (role === "tab") {
      link.setAttribute("href", href);
      link.setAttribute("target", "_blank");
      link.setAttribute("rel", "noopener noreferrer");
    } else {
      link.setAttribute("href", role === "top" ? "#" : href);
      link.setAttribute("target", "_self");
    }
  }
  return doc;
}

const parseHTML = (html) => new DOMParser().parseFromString(html, "text/html");

// A serialization can parse into another tree. Each round parses the last
// output, so the frame parses exactly the tree that the rules checked.
const STABLE_ROUNDS = 4;

/** The srcdoc of every dashboard report frame: the packet document with the
 * link rules applied until its serialization is stable. A report without a
 * stable serialization shows as plain text. The parse is inert: no report
 * script runs and no report resource loads. */
export function reportDocument(body, parse = parseHTML) {
  let html = packetDocument(body);
  for (let round = 0; round < STABLE_ROUNDS; round += 1) {
    const next = `<!doctype html>${applyLinkRules(parse(html)).documentElement.outerHTML}`;
    if (next === html) return html;
    html = next;
  }
  const text = (typeof body === "string" ? body : "").replace(/&/g, "&amp;").replace(/</g, "&lt;");
  return packetDocument(`<pre style="white-space: pre-wrap">\n${text}</pre>`);
}

/** The sandbox the packet frame runs under: no scripts, no same-origin, no
 * forms — links may open a real tab, nothing else escapes. Exported so the
 * tests and the DOM agree on the exact grant. */
export const FRAME_SANDBOX = "allow-popups allow-popups-to-escape-sandbox";
