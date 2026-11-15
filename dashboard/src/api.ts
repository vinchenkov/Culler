// The dashboard passes requests to mc and returns its recorded results.

import type { McResult, McRunner } from "./mc";
import type { Logger } from "../../runner/shared/log";

export interface ApiDeps {
  mc: McRunner;
  /** Generates a fresh dashboard channel ref (`dash-<8 hex>`). */
  genRef: () => string;
  /** The structured log. Levelled methods with a frozen event name; also
   * callable with a bare string, which the dashboard no longer uses. */
  log: Logger;
  /** Reads a resolved HOST artifact path. Injected so the render endpoint's
   * refusals are testable without a real artifact plane on disk. */
  readRender: (hostPath: string) => Promise<string>;
  /** Reads the bytes of a resolved HOST artifact path. */
  readArtifact: (hostPath: string) => Promise<Uint8Array>;
  /** Opens a resolved HOST attachment path for streaming; null when the open
   * finds nothing servable. The real seam (hostfile.ts) refuses symlinks and
   * anything that is no longer an operator-owned regular file, because the
   * verb's verification must not be widened by this later open. Injected so
   * the byte endpoint is testable without a real attachment tree on disk. */
  openAttachment: (hostPath: string) => Promise<Blob | null>;
  /** Wakes the resident after a Collie write or a packet decision. A rejection
   * is logged and never fails the request; the timer is the fallback. */
  nudgeResident: (sessionId: string) => Promise<void>;
}

/** Answers a Collie verb and, when it succeeded, nudges the resident for its
 * session. The nudge is best effort: the request's outcome is the verb's. */
async function collieResponse(res: McResult, sessionID: string | undefined, deps: ApiDeps): Promise<Response> {
  const id = sessionID ?? res.json?.["session_id"];
  if (res.code === 0 && typeof id === "string") {
    try {
      await deps.nudgeResident(id);
    } catch (err) {
      deps.log.warn("dash.nudge_failed",
        `resident nudge failed; the round runs at the next tick: ${err instanceof Error ? err.message : String(err)}`,
        { session_id: id });
    }
  }
  return verbResponse(res);
}

export function makeGenRef(): () => string {
  return () => {
    const bytes = new Uint8Array(4);
    crypto.getRandomValues(bytes);
    return `dash-${Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("")}`;
  };
}

/** The command error code determines the response status. */
function verbResponse(res: McResult): Response {
  if (res.code === 0) {
    return Response.json(res.json ?? {}, { status: 200 });
  }
  const envelopeCode = (res.json?.["error"] as Record<string, unknown> | undefined)?.["code"];
  const envelope =
    res.json !== null && typeof res.json["error"] === "object"
      ? res.json
      : {
          error: {
            code: res.code === 1 ? "domain-rejection" : "environment",
            message: res.stderr.trim() || "the spine did not answer",
          },
        };
  const callerCaused = res.code === 1 || envelopeCode === "usage";
  const conflict = envelopeCode === "record-conflict" || envelopeCode === "operation-conflict";
  return Response.json(envelope, { status: conflict ? 409 : callerCaused ? 400 : 500 });
}

function usageError(message: string): Response {
  return Response.json({ error: { code: "usage", message } }, { status: 400 });
}

const positiveID = (value: unknown): value is number => Number.isSafeInteger(value) && (value as number) > 0;

/** Appends the two window flags both log lanes share. Only `tail`'s numeric
 * shape is guarded before spawning; `format` values stay the verb's authority.
 * Returns the usage response when the guard refuses, else null. */
function pushLogWindowFlags(argv: string[], url: URL): Response | null {
  const tail = url.searchParams.get("tail");
  if (tail !== null && tail !== "") {
    if (!/^\d+$/.test(tail)) {
      return usageError('logs requires "tail" to be a whole number');
    }
    argv.push("--tail", tail);
  }
  const format = url.searchParams.get("format");
  if (format !== null && format !== "") {
    argv.push("--format", format);
  }
  return null;
}

/** The session's existing dashboard channel ref from `mc collie list`, or a
 * fresh generated one. Returns a Response when the list itself fails. */
async function dashboardRef(sessionID: string, deps: ApiDeps): Promise<string | Response> {
  const list = await deps.mc.run(["collie", "list"]);
  if (list.code !== 0) {
    return verbResponse(list);
  }
  const sessions = (list.json?.["sessions"] as Record<string, unknown>[] | undefined) ?? [];
  const row = sessions.find((s) => s["id"] === sessionID);
  const bindings = [
    ...((row?.["active_bindings"] as Record<string, unknown>[] | undefined) ?? []),
    ...((row?.["known_bindings"] as Record<string, unknown>[] | undefined) ?? []),
  ];
  const dash = bindings.find((b) => b["surface"] === "dashboard");
  const ref = dash?.["channel_ref"];
  return typeof ref === "string" && ref !== "" ? ref : deps.genRef();
}

async function readBody(req: Request): Promise<Record<string, unknown> | Response> {
  const raw = await req.text();
  if (raw.trim() === "") {
    return {};
  }
  // Declared JSON only: keeps a cross-site text/plain "simple request" from
  // smuggling a body past the browser's preflight (defense in depth behind
  // the server's Origin check).
  if (!(req.headers.get("content-type") ?? "").toLowerCase().includes("application/json")) {
    return usageError("request body must be application/json");
  }
  try {
    const parsed: unknown = JSON.parse(raw);
    if (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
  } catch {
    // fall through to the usage error
  }
  return usageError("request body must be a JSON object");
}

/** The kinds the dashboard acks on sight, and the reason it acks them: for
 * these two the history pane is the render source, so the operator has
 * already seen the row's content by the time the drain runs. Every other kind
 * reaches the operator only through a surface of its own, and an ack cannot be
 * repaid — so a row this set does not name stays undelivered until something
 * renders it. Fail closed on an unknown kind for the same reason. */
const TRIVIALLY_ACKED_KINDS = new Set(["collie_reply", "collie_echo"]);

/** The kinds a dashboard pane renders in full from the spine, so that pane's
 * render is confirmed delivery. Not on sight and not on poll: the
 * browser asks for the ack after it has painted, and only for the kind it
 * painted. */
const PANE_RENDERED_KINDS = new Set(["blocked_alert", "health"]);

/** Every surface the outbox fans out to (`mc outbox poll` refuses any other). */
const OUTBOX_SURFACES = ["discord", "dashboard", "cli"] as const;
const LAG_POLL_LIMIT = 100;

/** Drains the dashboard's delivery cursor: poll once, ack the rows history
 * has already rendered. An at-least-once replay is idempotent. */
async function drainOutbox(deps: ApiDeps): Promise<Response> {
  const poll = await deps.mc.run(["outbox", "poll", "--surface", "dashboard", "--limit", "100"]);
  if (poll.code !== 0) {
    return verbResponse(poll);
  }
  const rows = (poll.json?.["rows"] as Record<string, unknown>[] | undefined) ?? [];
  let drained = 0;
  for (const row of rows) {
    const id = row["id"];
    if (typeof id !== "number") {
      continue;
    }
    const kind = row["kind"];
    if (typeof kind !== "string" || !TRIVIALLY_ACKED_KINDS.has(kind)) {
      continue;
    }
    const ack = await deps.mc.run(["outbox", "ack", String(id), "--surface", "dashboard"]);
    if (ack.code !== 0) {
      deps.log.warn("outbox.ack_failed", "outbox ack refused during the drain", {
        row: id,
        kind,
        stderr: ack.stderr.trim(),
      });
      continue;
    }
    drained++;
  }
  return Response.json({ polled: rows.length, drained }, { status: 200 });
}

/** The media type a packet render's own recorded filename declares. The spine
 * stores a bare path and sniffing bytes would be interpretation, so the
 * extension is the whole signal; an extensionless render stays plain text. */
function renderContentType(renderPath: string): string {
  const lower = renderPath.toLowerCase();
  if (lower.endsWith(".html") || lower.endsWith(".htm")) {
    return "text/html; charset=utf-8";
  }
  if (lower.endsWith(".md") || lower.endsWith(".markdown")) {
    return "text/markdown; charset=utf-8";
  }
  return "text/plain; charset=utf-8";
}

interface ChangeRecord {
  from_commit: string;
  to_commit: string;
  inventory_path: string;
  inventory_sha256: string;
  patch_path: string;
  patch_sha256: string;
}

interface ChangeFile {
  status: string;
  path: string;
  previous_path?: string;
  binary?: boolean;
  text_diff?: boolean;
  patch_offset: number;
  patch_bytes: number;
}

interface ChangeInventory {
  from_commit: string;
  to_commit: string;
  patch_bytes: number;
  files: ChangeFile[];
}

function changeDataMismatch(): Response {
  return Response.json({ error: { code: "domain-rejection", message: "The change data differs from its record." } }, { status: 409 });
}

/** Reads the verified file inventory of the change data of the open packet. */
async function readChangeInventory(taskID: string, url: URL, deps: ApiDeps):
  Promise<Response | { project: unknown; record: ChangeRecord; body: ChangeInventory }> {
  const list = await deps.mc.run(["packet", "list"]);
  if (list.code !== 0) return verbResponse(list);
  const packets = (list.json?.["packets"] as Record<string, unknown>[] | undefined) ?? [];
  const row = packets.find((p) => String(p["task_id"]) === taskID);
  if (!row || url.searchParams.get("packet") !== String(row["packet_id"]) || url.searchParams.get("version") !== String(row["version_id"])) {
    return Response.json({ error: { code: "domain-rejection", message: "The review packet changed. Open the current packet." } }, { status: 409 });
  }
  const retention = row["artifact_retention"] as { status?: string; expired_at?: string } | undefined;
  if (retention?.status === "expired") {
    return Response.json({ error: { code: "artifact-expired", message: `Task artifacts expired on ${retention.expired_at}. The repository preserves the deliverable.` } }, { status: 410 });
  }
  const record = row["candidate_changes"] as ChangeRecord | null | undefined;
  if (!record || typeof record.inventory_path !== "string" || typeof record.patch_path !== "string") {
    return Response.json({ error: { code: "change-data-absent", message: "Culler recorded no change data for this packet." } }, { status: 404 });
  }
  const bytes = await readChangeFile(record, "inventory", deps);
  if (bytes instanceof Response) return bytes;
  let body: ChangeInventory;
  try {
    body = JSON.parse(new TextDecoder().decode(bytes)) as ChangeInventory;
  } catch {
    return changeDataMismatch();
  }
  if (body.from_commit !== record.from_commit || body.to_commit !== record.to_commit || !Array.isArray(body.files)) {
    return changeDataMismatch();
  }
  return { project: row["project"], record, body };
}

/** Reads the version of one changed text file at each commit of the
 * comparison from the Project repository. */
async function readChangeFiles(project: unknown, body: ChangeInventory, file: ChangeFile, deps: ApiDeps): Promise<Response> {
  if (file.binary || !file.text_diff || !["modified", "renamed", "copied"].includes(file.status)) {
    return Response.json({ error: { code: "domain-rejection", message: "The file has no unmodified lines to show." } }, { status: 409 });
  }
  const list = await deps.mc.run(["project", "list"]);
  if (list.code !== 0) return verbResponse(list);
  const projects = (list.json?.["projects"] as Record<string, unknown>[] | undefined) ?? [];
  const root = projects.find((row) => row["id"] === project)?.["workspace_root"];
  if (typeof root !== "string" || root === "") {
    return Response.json({ error: { code: "domain-rejection", message: "The Project of the packet has no repository." } }, { status: 409 });
  }
  const sides = await Promise.all([
    deps.mc.run(["__repository-file", root, body.from_commit, file.previous_path ?? file.path]),
    deps.mc.run(["__repository-file", root, body.to_commit, file.path]),
  ]);
  const failed = sides.find((side) => side.code !== 0);
  if (failed) return verbResponse(failed);
  const [before, after] = sides.map((side) => ({ path: side.json?.["path"], content: side.json?.["content"] }));
  if (typeof before!.content !== "string" || typeof after!.content !== "string") {
    return Response.json({ error: { code: "environment", message: "the repository read returned no content" } }, { status: 500 });
  }
  return Response.json({ old_file: before, new_file: after });
}

/** Reads one recorded change data file and checks its recorded digest. */
async function readChangeFile(record: ChangeRecord, kind: "inventory" | "patch", deps: ApiDeps): Promise<Response | Uint8Array> {
  const resolved = await deps.mc.run(["__artifact-host-path", record[`${kind}_path`]]);
  if (resolved.code !== 0) return verbResponse(resolved);
  const hostPath = resolved.json?.["host_path"];
  if (typeof hostPath !== "string" || hostPath === "") {
    return Response.json({ error: { code: "environment", message: "the artifact resolver returned no host path" } }, { status: 500 });
  }
  let bytes: Uint8Array;
  try {
    bytes = await deps.readArtifact(hostPath);
  } catch (err) {
    return Response.json({ error: { code: "environment", message: `the change data could not be read: ${String(err)}` } }, { status: 500 });
  }
  if (new Bun.CryptoHasher("sha256").update(bytes).digest("hex") !== record[`${kind}_sha256`]) return changeDataMismatch();
  return bytes;
}

/** Maps `mc __attachment-bytes`'s refusals onto HTTP statuses, by the verb's
 * own distinguishable messages. A claim the spine does not confirm, or one
 * whose bytes are gone, is 404; an over-cap claim is 413; every remaining
 * domain rejection (a digest or size disagreement, a wrong-scope caller) means
 * this deployment's state is broken, which no browser caused — 500. Malformed
 * addresses (usage) and environment failures stay with `verbResponse`. */
function attachmentBytesStatus(res: McResult): number | null {
  if (res.code !== 1) {
    return null;
  }
  const envelope = (res.json?.["error"] as Record<string, unknown> | undefined)?.["message"];
  const message = typeof envelope === "string" ? envelope : res.stderr;
  if (message.includes("serve cap")) {
    return 413;
  }
  if (
    message.includes("no conversation attachment claim") ||
    message.includes("not materialized") ||
    message.includes("no longer live")
  ) {
    return 404;
  }
  return 500;
}

/** The download disposition. `display_name` is verb-guaranteed a control-free
 * basename (1..255 bytes, no slash or backslash), so only what an HTTP header
 * cannot carry is replaced in the quoted fallback; `filename*` keeps the exact
 * name for browsers that read it. */
function attachmentDisposition(displayName: string): string {
  const ascii = displayName.replace(/[^\x20-\x7e]/g, "_").replaceAll('"', "'");
  return `attachment; filename="${ascii}"; filename*=UTF-8''${encodeURIComponent(displayName)}`;
}

/** Routes one request. Returns null for paths outside /api/ (static UI). */
export async function handleApi(req: Request, url: URL, deps: ApiDeps): Promise<Response | null> {
  const path = url.pathname;
  if (!path.startsWith("/api/")) {
    return null;
  }

  if (path === "/api/sessions" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["collie", "list"]));
  }

  if (path === "/api/sessions" && req.method === "POST") {
    const body = req.headers.get("content-length") === "0" || req.body === null ? {} : await readBody(req);
    if (body instanceof Response) return body;
    const project = body["vision_project"];
    const packet = body["packet_id"];
    const version = body["version_id"];
    const ensure = body["ensure"];
    const packetConversation = packet !== undefined || version !== undefined;
    if (packetConversation && !(positiveID(packet) && positiveID(version))) return usageError("A packet conversation requires a packet and a version.");
    if (packetConversation && (project !== undefined || ensure !== undefined)) return usageError("A packet conversation accepts only a packet, a version, and clear.");
    if (project !== undefined && (typeof project !== "string" || !project)) return usageError("A Vision conversation requires a Project.");
    const clear = body["clear"];
    if (clear !== undefined && (clear !== true || (!project && !packetConversation))) return usageError("Only a Vision or packet conversation can be cleared.");
    if (ensure !== undefined && (ensure !== true || project || clear)) return usageError("Only an ordinary conversation supports automatic creation.");
    const ref = project ? `vision-${project}` : deps.genRef();
    return collieResponse(await deps.mc.run(["collie", "start", "--from", `dashboard:${ref}`,
      ...(project ? ["--vision-project", project as string] : []),
      ...(packetConversation ? ["--packet", String(packet), "--version", String(version)] : []),
      ...(clear ? ["--clear"] : []), ...(ensure ? ["--ensure"] : [])]), undefined, deps);
  }

  const model = path.match(/^\/api\/sessions\/([^/]+)\/model$/);
  if (model && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) return body;
    if (typeof body.binding !== "string" || !body.binding || typeof body.request_id !== "string" || !body.request_id) {
      return usageError("A model change requires an agent profile and a request identifier.");
    }
    return collieResponse(await deps.mc.run(["collie", "start", "--from", `dashboard:${deps.genRef()}`,
      "--previous", model[1], "--binding", body.binding, "--request", body.request_id]), undefined, deps);
  }

  const rename = path.match(/^\/api\/sessions\/([^/]+)\/title$/);
  if (rename && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) return body;
    if (typeof body["title"] !== "string") return usageError("A conversation name is required.");
    return verbResponse(await deps.mc.run(["collie", "title", rename[1], "--title", body["title"]]));
  }

  const removal = path.match(/^\/api\/sessions\/([^/]+)$/);
  if (removal && req.method === "DELETE") {
    return collieResponse(await deps.mc.run(["collie", "delete", removal[1]]), removal[1], deps);
  }

  const upload = path.match(/^\/api\/sessions\/([^/]+)\/upload$/);
  if (upload && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) return body;
    return collieResponse(await deps.mc.run(["__dashboard-upload"], JSON.stringify({ ...body, session_id: upload[1] })), upload[1], deps);
  }

  const session = path.match(/^\/api\/sessions\/([^/]+)\/(history|send|resume)$/);
  if (session) {
    const [, sessionID, verb] = session;
    if (verb === "history" && req.method === "GET") {
      return verbResponse(await deps.mc.run(["collie", "history", sessionID]));
    }
    if (verb === "send" && req.method === "POST") {
      const body = await readBody(req);
      if (body instanceof Response) {
        return body;
      }
      const text = body["body"];
      if (typeof text !== "string" || text.trim() === "") {
        return usageError("send requires a non-empty string field \"body\"");
      }
      const ref = await dashboardRef(sessionID, deps);
      if (ref instanceof Response) {
        return ref;
      }
      return collieResponse(
        await deps.mc.run(["collie", "send", sessionID, "--from", `dashboard:${ref}`, "--body", text]),
        sessionID, deps,
      );
    }
    if (verb === "resume" && req.method === "POST") {
      const ref = await dashboardRef(sessionID, deps);
      if (ref instanceof Response) {
        return ref;
      }
      return collieResponse(
        await deps.mc.run(["collie", "resume", sessionID, "--from", `dashboard:${ref}`]),
        sessionID, deps,
      );
    }
  }

  // One conversation attachment's bytes. The BROWSER
  // names a claim identity — session, direction, sha256, size, verbatim the
  // fields a typed claim in `mc collie history` carries — never a path:
  // `mc __attachment-bytes` confirms that identity against the spine row,
  // verifies the published file byte-for-byte, and answers with the host path
  // this mirror only streams. Its metadata is translated to headers unchanged:
  // an `inline` claim (the verb's raster-image allowlist) keeps its declared
  // media type; everything else is a named download, because serving a
  // surface-chosen type would let agent-authored bytes render in the
  // dashboard's origin. The bytes are content-addressed and immutable, which
  // is what makes the long-lived cache header true.
  const attachment = path.match(/^\/api\/sessions\/([^/]+)\/attachments\/([^/]+)\/([^/]+)\/([^/]+)$/);
  if (attachment && req.method === "GET") {
    const [, sessionID, direction, sha256, size] = attachment;
    // The closed address grammar, guarded before spawning: direction, digest,
    // and size are this route's own path shape. The session id stays an opaque
    // token the verb's fences judge, exactly as the history route passes it.
    if (
      (direction !== "in" && direction !== "out") ||
      !/^[0-9a-f]{64}$/.test(sha256!) ||
      !/^(0|[1-9][0-9]*)$/.test(size!)
    ) {
      return usageError(
        "attachment bytes are addressed by direction in|out, a 64-lowercase-hex sha256, and an exact decimal byte size",
      );
    }
    const served = await deps.mc.run(["__attachment-bytes", sessionID!, direction, sha256!, size!]);
    if (served.code !== 0) {
      const status = attachmentBytesStatus(served);
      if (status === null) {
        return verbResponse(served);
      }
      const envelope =
        served.json !== null && typeof served.json["error"] === "object"
          ? served.json
          : { error: { code: "domain-rejection", message: served.stderr.trim() } };
      return Response.json(envelope, { status });
    }
    const hostPath = served.json?.["host_path"];
    const mediaType = served.json?.["media_type"];
    const displayName = served.json?.["display_name"];
    const inline = served.json?.["inline"] === true;
    if (typeof hostPath !== "string" || hostPath === "" || typeof mediaType !== "string" || typeof displayName !== "string") {
      return Response.json(
        { error: { code: "environment", message: "the attachment resolver returned an incomplete claim" } },
        { status: 500 },
      );
    }
    let blob: Blob | null;
    try {
      blob = await deps.openAttachment(hostPath);
    } catch {
      blob = null;
    }
    if (blob === null) {
      // The verb verified this file an instant ago, so a failed open now is
      // the GC race on a content-addressed immutable object: gone, not broken.
      return Response.json(
        { error: { code: "domain-rejection", message: "the attachment bytes were collected before they could be served" } },
        { status: 404 },
      );
    }
    if (blob.size !== Number(size)) {
      // The verb verified the exact recorded length an instant ago; a
      // different length now means the file changed under the read. Broken
      // state, never served — the same do-not-serve verdict the verb gives a
      // spine disagreement.
      return Response.json(
        { error: { code: "domain-rejection", message: "the attachment bytes changed between verification and serving" } },
        { status: 500 },
      );
    }
    return new Response(blob, {
      status: 200,
      headers: {
        "content-type": inline ? mediaType : "application/octet-stream",
        "content-length": size!,
        "content-disposition": inline ? "inline" : attachmentDisposition(displayName),
        "content-security-policy": "sandbox",
        "x-content-type-options": "nosniff",
        "cache-control": "private, max-age=31536000, immutable",
      },
    });
  }

  if (path === "/api/outbox/drain" && req.method === "POST") {
    return drainOutbox(deps);
  }

  if (path === "/api/board/write" && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) return body;
    return verbResponse(await deps.mc.run(["board", "write"], JSON.stringify(body)));
  }

  if (path === "/api/tasks" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["task", "list"]));
  }

  const taskGet = path.match(/^\/api\/tasks\/(\d+)$/);
  if (taskGet && req.method === "GET") {
    return verbResponse(await deps.mc.run(["task", "get", taskGet[1]!]));
  }

  // Board's active cards — one per Project:
  // the lease rows themselves, which are the only thing that makes a card
  // active. One row per Project, held or free.
  if (path === "/api/leases" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["lease", "list"]));
  }

  // The one settings row: the lease tunables
  // the badge judges staleness by and the review WIP cap every pane renders
  // occupancy against. Deployment-wide; the cap applies per Project.
  if (path === "/api/settings" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["settings", "get"]));
  }

  // Pipeline's run history. The verb orders
  // by creation; picking the recent slice to show is the browser's job.
  if (path === "/api/runs" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["run", "list"]));
  }

  // The wiki state rows: one row for each Project that is not archived.
  if (path === "/api/wiki-states" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["wiki-state", "list"]));
  }

  if (path === "/api/projects/role-show" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["project", "role-show", "--project", url.searchParams.get("project") ?? "", "--role", url.searchParams.get("role") ?? ""]));
  }
  if (["/api/projects/role-save", "/api/projects/role-unblock"].includes(path) && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) return body;
    return verbResponse(await deps.mc.run(["project", path.split("/").pop()!], JSON.stringify(body)));
  }
  const runCustomization = path.match(/^\/api\/runs\/([^/]+)\/customization$/);
  if (runCustomization && req.method === "GET") {
    return verbResponse(await deps.mc.run(["run", "customization", decodeURIComponent(runCustomization[1]!)]));
  }

  // Configs: the project registry rows, mirrored verbatim.
  const environment = path.match(/^\/api\/environment\/(get|save|approve|deny|assist|retry|cache-clear)$/);
  if (environment && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) return body;
    const action = environment[1]!;
    const result = await deps.mc.run(["environment", action], JSON.stringify(body));
    if (result.code === 0 && ["approve", "deny", "assist", "retry", "cache-clear"].includes(action)) {
      try { await deps.nudgeResident("project-environment"); } catch { /* The resident timer also processes work. */ }
    }
    return verbResponse(result);
  }

  if (path === "/api/projects/readiness" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["project", "readiness"]));
  }
  const projectSetup = path.match(/^\/api\/projects\/(setup-preview|setup-save|details|vision-save|start|vision-open|vision-draft|vision-edit|vision-submit|vision-rebase|vision-workspace)$/);
  if (projectSetup && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) return body;
    const result = await deps.mc.run(["project", projectSetup[1]!], JSON.stringify(body));
    if ((projectSetup[1] === "vision-submit" || projectSetup[1] === "start") && result.code === 0) {
      try { await deps.nudgeResident(projectSetup[1] === "start" ? "project-start" : "vision-save"); } catch { /* The resident timer also processes work. */ }
    }
    return verbResponse(result);
  }

  if (path === "/api/projects" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["project", "list"]));
  }

  const projectDeletion = path.match(/^\/api\/projects\/([^/]+)\/delete$/);
  if (projectDeletion && req.method === "GET") {
    return verbResponse(await deps.mc.run(["project", "delete-preview", decodeURIComponent(projectDeletion[1]!)]));
  }
  if (projectDeletion && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) return body;
    if (body.confirm !== true) return usageError("Confirm Project deletion in the Projects page.");
    return verbResponse(await deps.mc.run([
      "project", "delete", decodeURIComponent(projectDeletion[1]!), "--confirm",
    ]));
  }

  if (path === "/api/pause" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["pause", "status"]));
  }
  if (path === "/api/pause" && req.method === "POST") {
    return verbResponse(await deps.mc.run(["pause"]));
  }
  if (path === "/api/resume" && req.method === "POST") {
    return verbResponse(await deps.mc.run(["resume"]));
  }
  const projectPause = path.match(/^\/api\/projects\/([^/]+)\/(pause|resume)$/);
  if (projectPause && req.method === "POST") {
    return verbResponse(await deps.mc.run([
      "project", projectPause[2]!, decodeURIComponent(projectPause[1]!),
    ]));
  }

  if (path === "/api/config" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["config", "show"]));
  }
  if (path === "/api/discord" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["discord", "show"]));
  }
  if (path.startsWith("/api/discord/") && req.method === "POST") {
    const action = path.slice("/api/discord/".length);
    if (!["check-bot", "servers", "channels", "test", "reply", "renew", "cancel", "draft", "test-current", "check", "connect", "alerts", "disconnect"].includes(action)) {
      return usageError("The Discord action is unsupported.");
    }
    const body = await readBody(req);
    if (body instanceof Response) return body;
    return verbResponse(await deps.mc.run(["discord", action], JSON.stringify(body)));
  }
  if (path === "/api/config" && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) return body;
    return verbResponse(await deps.mc.run(["config", "save"], JSON.stringify(body)));
  }

  if (path === "/api/agent-profiles" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["agent-profile", "list"]));
  }
  const profileAction = path.match(/^\/api\/agent-profiles\/(models|save|connection-save|connection-delete|operation-start|operation-status|operation-cancel)$/);
  if (profileAction && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) return body;
    if (profileAction[1] !== "connection-save" && Object.keys(body).some(key => key.toLowerCase() === "api_key")) {
      return Response.json({ error: "API keys require the connection form." }, { status: 400 });
    }
    return verbResponse(await deps.mc.run(["agent-profile", profileAction[1]!], JSON.stringify(body)));
  }

  // The shared assignments include Collie.
  if (path === "/api/routing" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["routing", "show"]));
  }

  // The Pipeline page's role route change: one selection maps to one
  // `mc routing set` invocation. The verb owns the role list and the
  // enabled-binding rule, so a wrong role or a disabled binding comes back
  // as the verb's own rejection.
  if (path === "/api/routing" && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) {
      return body;
    }
    const role = body["role"];
    const binding = body["binding"];
    const expected = body["expected"];
    if (typeof role !== "string" || role === "" || typeof binding !== "string" || binding === "" || typeof expected !== "string" || expected === "") {
      return usageError('routing requires non-empty string fields "role", "binding", and "expected"');
    }
    return verbResponse(await deps.mc.run(["routing", "set", "--role", role, "--binding", binding, "--expected", expected]));
  }

  // System Logs: the run lane's own list — which runs left diagnostics.
  // The list is server-produced so the operator picks from it rather than
  // supplying an id. Matched before the unit route: "runs"
  // would otherwise read as a unit named runs and earn a usage refusal.
  if (path === "/api/logs/runs" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["logs", "runs", "--list"]));
  }

  // One run's captured diagnostics. The id is passed through
  // as an opaque token: the verb's own fences (safe-id charset, Clean, Lstat
  // walk) are the authority on what addresses a run directory, and restating
  // them here would be a second, drifting copy.
  const runLog = path.match(/^\/api\/logs\/run\/([^/]+)$/);
  if (runLog && req.method === "GET") {
    const argv = ["logs", "run", decodeURIComponent(runLog[1]!)];
    const guarded = pushLogWindowFlags(argv, url);
    if (guarded !== null) {
      return guarded;
    }
    return verbResponse(await deps.mc.run(argv));
  }

  // System Logs: the supervision-plane tail. The BROWSER names a unit
  // and a stream, never a path — the verb resolves both against its fixed
  // table, so there is no read-anything oracle here. Only the numeric shape
  // of `tail` is guarded before spawning; unit/stream/format validity stays
  // the verb's authority (its usage envelope maps to 400).
  const logs = path.match(/^\/api\/logs\/([a-z]+)$/);
  if (logs && req.method === "GET") {
    const argv = ["logs", logs[1]!];
    const stream = url.searchParams.get("stream");
    if (stream !== null && stream !== "") {
      argv.push("--stream", stream);
    }
    const guarded = pushLogWindowFlags(argv, url);
    if (guarded !== null) {
      return guarded;
    }
    return verbResponse(await deps.mc.run(argv));
  }

  // Activity, one stream. The verb clamps its own limit; the
  // "separate error stream" is the dispatch.health kind, and splitting the
  // rows by kind is interpretation, so the browser does it.
  //
  // `?kind=` is passed through because it is the VERB's own filter, not a
  // classification invented here: System Health asks for one kind directly
  // rather than fetching every row and discarding most of them.
  // `?limit=` is passed through for the same reason as `?kind=` — it is the
  // verb's own parameter, clamped by the verb at 1000. Without it the browser
  // got the verb's 100-row default and split THAT, so on a busy deployment
  // every one of the hundred newest rows was a dispatch.spawn and the error
  // pane rendered its empty state while dispatch.health rows sat unread in the
  // spine. An error stream that says "nothing has refused" when something has
  // is worse than no error stream. Guarded the same way `tail` is: only the
  // numeric shape is checked here, the clamp stays the verb's authority.
  if (path === "/api/activity" && req.method === "GET") {
    const argv = ["activity", "list"];
    const kind = url.searchParams.get("kind");
    if (kind !== null && kind !== "") {
      argv.push("--kind", kind);
    }
    const limit = url.searchParams.get("limit");
    if (limit !== null && limit !== "") {
      if (!/^\d+$/.test(limit)) {
        return usageError('activity requires "limit" to be a whole number');
      }
      argv.push("--limit", limit);
    }
    const project = url.searchParams.get("project");
    if (project) argv.push("--project", project);
    return verbResponse(await deps.mc.run(argv));
  }

  if (path === "/api/system/status" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["system", "status"]));
  }
  if (path === "/api/releases/status" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["__release", "status"]));
  }
  if (path === "/api/releases/open" && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) return body;
    if (body.confirm !== true || Object.keys(body).some(key => key !== "confirm")) {
      return usageError("Confirm the request to open the installer.");
    }
    return verbResponse(await deps.mc.run(["__release", "open"]));
  }
  if (path === "/api/system/diagnostics" && req.method === "GET") {
    const argv = ["system", "diagnostics"];
    for (const field of ["connection", "run", "attempt", "after", "project"]) {
      const value = url.searchParams.get(field);
      if (value) argv.push(`--${field}`, value);
    }
    return verbResponse(await deps.mc.run(argv));
  }
  if (path === "/api/connection/retry" && req.method === "POST") {
    const body = await readBody(req);
    return verbResponse(await deps.mc.run(["connection", "retry"], JSON.stringify(body)));
  }
  if (path === "/api/system/resolve-proposals" && req.method === "POST") {
    const body = await req.json() as { project_id?: string; decision_id?: number; reason?: string };
    if (!body.project_id || !Number.isInteger(body.decision_id) || !body.reason?.trim()) return usageError("The proposal decision requires its Project, identifier, and resolution.");
    return verbResponse(await deps.mc.run(["system", "resolve-proposals", body.project_id, "--decision", String(body.decision_id), "--reason", body.reason]));
  }

  if (path === "/api/system/resume-infrastructure" && req.method === "POST") {
    const body = await readBody(req);
    if (typeof body.project_id !== "string") return usageError("A Project is required.");
    return verbResponse(await deps.mc.run(["system", "resume-infrastructure", body.project_id, "--request", String(body.request_id ?? "")]));
  }
  if (path === "/api/collie/reply-retry" && req.method === "POST") {
    const body = await readBody(req);
    if (typeof body.session_id !== "string" || !Number.isSafeInteger(body.message_id) || typeof body.request_id !== "string") return usageError("The reply retry identity is invalid.");
    return verbResponse(await deps.mc.run(["collie", "reply-retry", body.session_id, "--message", String(body.message_id), "--request", body.request_id]));
  }

  if (path === "/api/doctor" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["doctor"]));
  }

  // Host-plane health. The verb opens no spine, spawns no container, and reads
  // only host files, so it keeps answering when the spine is unreachable and
  // the doctor beside it cannot. `tail` is the record window each component is
  // judged over; only its numeric shape is guarded before spawning, the clamp
  // is the verb's.
  if (path === "/api/health" && req.method === "GET") {
    const argv = ["health"];
    const tail = url.searchParams.get("tail");
    if (tail !== null && tail !== "") {
      if (!/^\d+$/.test(tail)) {
        return usageError('health requires "tail" to be a whole number');
      }
      argv.push("--tail", tail);
    }
    return verbResponse(await deps.mc.run(argv));
  }

  // Review Queue. The verb returns EVERY packet, archived ones
  // included, and carries no task title — both are deliberate here: filtering
  // the live queue and computing age-in-queue are interpretation, and the API
  // holds no interpretation. The browser does both.
  if (path === "/api/evaluation/cases" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["evaluation", "list"], "{}"));
  }
  const evaluation = path.match(/^\/api\/evaluation\/(status|submit|download|inspect)$/);
  if (evaluation && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) return body;
    return verbResponse(await deps.mc.run(["evaluation", evaluation[1]!], JSON.stringify(body)));
  }

  if (path === "/api/packets" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["packet", "list"]));
  }

  // The Review Queue's three structured verbs. This is form
  // submission, not interpretation: one decision maps to one `mc packet
  // decide` invocation, and the reason the operator typed rides along
  // untouched. The reason rules are the VERB's — forbidden on approve,
  // required on revise and cancel — so they are not restated here; a wrong
  // one comes back as the verb's own domain rejection rather than a second,
  // drifting copy of the rule.
  // The artifact-target → host-root map, mirrored verbatim. The BROWSER uses it
  // to translate the container paths a packet render cites (the Packager
  // authors inside a container and can name no other path), because rewriting
  // the role's own prose here would be interpretation, which belongs in the
  // browser, next to the other display decisions.
  if (path === "/api/artifact-roots" && req.method === "GET") {
    return verbResponse(await deps.mc.run(["__artifact-roots"]));
  }

  if (path === "/api/outbox/lag" && req.method === "GET") {
    const polls = await Promise.all(OUTBOX_SURFACES.map(surface =>
      deps.mc.run(["outbox", "poll", "--surface", surface, "--limit", String(LAG_POLL_LIMIT)])));
    const failure = polls.find(poll => poll.code !== 0);
    if (failure) return verbResponse(failure);
    const surfaces = OUTBOX_SURFACES.map((surface, index) => {
      const poll = polls[index]!;
      const rows = (poll.json?.["rows"] as Record<string, unknown>[] | undefined) ?? [];
      const stamps = rows.map((r) => r["created_at"]).filter((c): c is string => typeof c === "string");
      stamps.sort();
      return {
        surface,
        undelivered: rows.length,
        oldest_created_at: stamps[0] ?? null,
        // The count is a floor once the poll fills, and a floor presented as a
        // total hides a growing backlog behind a steady number.
        capped: rows.length >= LAG_POLL_LIMIT,
      };
    });
    return Response.json({ surfaces }, { status: 200 });
  }

  // A pane identifies the task or activity records it displayed.
  const paneAck = path.match(/^\/api\/outbox\/([a-z_]+)\/ack$/);
  if (paneAck && req.method === "POST") {
    const kind = paneAck[1]!;
    if (!PANE_RENDERED_KINDS.has(kind)) {
      return usageError(`no pane renders outbox kind ${kind}, so this surface cannot confirm its delivery`);
    }
    const body = await readBody(req);
    if (body instanceof Response) return body;
    const ids = body["ids"];
    if (!Array.isArray(ids) || !ids.every((id) => Number.isSafeInteger(id) && id > 0)) {
      return usageError("A pane must identify the records it displayed.");
    }
    return verbResponse(await deps.mc.run([
      "outbox", "ack-rendered", "--surface", "dashboard", "--kind", kind, "--ids", JSON.stringify(ids),
    ]));
  }

  // The command supplies the recorded report path and checks its host access.
  const taskReport = path.match(/^\/api\/tasks\/(\d+)\/(report|correction-report)$/);
  const historyReport = path.match(/^\/api\/tasks\/(\d+)\/history\/(packets|evidence)\/(\d+)\/render$/);
  const render = path.match(/^\/api\/packets\/(\d+)\/render$/) ?? taskReport ?? historyReport;
  if (render && req.method === "GET") {
    const taskID = render[1]!;
    const list = await deps.mc.run(historyReport
      ? ["board", "history-report", "--task", taskID, "--record", historyReport[3]!, "--kind", historyReport[2] === "packets" ? "packet" : "evidence"]
      : taskReport ? ["task", "get", taskID] : ["packet", "list"]);
    if (list.code !== 0) {
      return verbResponse(list);
    }
    const packets = (list.json?.["packets"] as Record<string, unknown>[] | undefined) ?? [];
    const delivery = list.json?.["delivery"] as Record<string, unknown> | undefined;
    const row = historyReport ? list.json
      : taskReport ? { ...delivery, render_path: taskReport[2] === "correction-report" ? (delivery?.["verifier_report"] as Record<string, unknown> | undefined)?.["report_path"] : delivery?.["report_path"] } : packets.find((p) => String(p["task_id"]) === taskID);
    if (row?.["packet_id"] && (url.searchParams.get("packet") !== String(row["packet_id"]) || url.searchParams.get("version") !== String(row["version_id"]))) {
      return Response.json({ error: { code: "domain-rejection", message: "The review packet changed. Open the current packet." } }, { status: 409 });
    }
    const retention = row?.["artifact_retention"] as { status?: string; expired_at?: string } | undefined;
    if (retention?.status === "expired") {
      return Response.json({ error: { code: "artifact-expired", message: `Task artifacts expired on ${retention.expired_at}. The repository preserves the deliverable.` } }, { status: 410 });
    }
    const containerPath = row?.["render_path"];
    if (typeof containerPath !== "string" || containerPath === "") {
      return Response.json(
        { error: { code: "domain-rejection", message: `no review packet render recorded for task ${taskID}` } },
        { status: 404 },
      );
    }
    const resolved = await deps.mc.run(["__artifact-host-path", containerPath]);
    if (resolved.code !== 0) {
      return verbResponse(resolved);
    }
    const hostPath = resolved.json?.["host_path"];
    if (typeof hostPath !== "string" || hostPath === "") {
      return Response.json(
        { error: { code: "environment", message: "the artifact resolver returned no host path" } },
        { status: 500 },
      );
    }
    let body: string;
    try {
      body = await deps.readRender(hostPath);
    } catch (err) {
      return Response.json(
        { error: { code: "environment", message: `the packet render could not be read: ${String(err)}` } },
        { status: 500 },
      );
    }
    if (typeof row?.["report_sha256"] === "string") {
      const digest = new Bun.CryptoHasher("sha256").update(body).digest("hex");
      if (digest !== row["report_sha256"]) return Response.json({ error: { code: "domain-rejection", message: "The report differs from its recorded version." } }, { status: 409 });
    }
    return new Response(body, {
      status: 200,
      headers: {
        // The content type names what the recorded path says the Packager
        // wrote. A packet is HTML, and an older packet is markdown. Naming the
        // type is content negotiation, not interpretation — the browser picks
        // its reader from it. `CSP: sandbox` + nosniff are for the operator
        // who opens this endpoint DIRECTLY in a tab: agent-authored bytes must
        // never run in the dashboard's own origin. The Review pane renders
        // them inside its own sandboxed frame instead.
        "content-type": renderContentType(containerPath),
        "content-security-policy": "sandbox",
        "x-content-type-options": "nosniff",
      },
    });
  }

  // The command supplies the change data record of the packet, and the
  // dashboard returns the recorded bytes. The file versions come from the
  // Project repository at the two commits of the record.
  const changes = path.match(/^\/api\/packets\/(\d+)\/changes(\/patch|\/files)?$/);
  if (changes && req.method === "GET") {
    const inventory = await readChangeInventory(changes[1]!, url, deps);
    if (inventory instanceof Response) return inventory;
    if (!changes[2]) {
      const { files, from_commit, to_commit, patch_bytes } = inventory.body;
      return Response.json({ from_commit, to_commit, patch_bytes, files });
    }
    const index = Number(url.searchParams.get("file"));
    const file = Number.isSafeInteger(index) && index >= 0 ? inventory.body.files[index] : undefined;
    if (!file) return usageError("The file must identify one entry of the file inventory.");
    if (changes[2] === "/files") return readChangeFiles(inventory.project, inventory.body, file, deps);
    const patch = await readChangeFile(inventory.record, "patch", deps);
    if (patch instanceof Response) return patch;
    const start = file.patch_offset, end = file.patch_offset + file.patch_bytes;
    if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start < 0 || end > patch.length || end < start) {
      return changeDataMismatch();
    }
    return new Response(patch.subarray(start, end), {
      status: 200,
      headers: {
        "content-type": "text/plain; charset=utf-8",
        "content-security-policy": "sandbox",
        "x-content-type-options": "nosniff",
      },
    });
  }

  const packet = path.match(/^\/api\/packets\/(\d+)\/decide$/);
  if (packet && req.method === "POST") {
    const body = await readBody(req);
    if (body instanceof Response) {
      return body;
    }
    const decision = body["decision"];
    if (decision !== "approve" && decision !== "revise" && decision !== "cancel") {
      return usageError('decide requires "decision" to be one of approve, revise, cancel');
    }
    const argv = ["packet", "decide", packet[1]!, `--${decision}`];
    for (const field of ["packet_id", "version_id"] as const) {
      if (body[field] !== undefined) {
        if (!Number.isSafeInteger(body[field]) || (body[field] as number) < 1) return usageError(`${field} must identify the reviewed version`);
        argv.push(field === "packet_id" ? "--packet" : "--version", String(body[field]));
      }
    }
    const reason = body["reason"];
    if (typeof reason === "string" && reason !== "") {
      argv.push("--reason", reason);
    }
    const result = await deps.mc.run(argv);
    if (result.code === 0) {
      try {
        await deps.nudgeResident("packet-decide");
      } catch (err) {
        deps.log.warn("dash.nudge_failed",
          `resident nudge failed; the next start request runs at the end of the next tick: ${err instanceof Error ? err.message : String(err)}`,
          { task_id: Number(packet[1]) });
      }
    }
    return verbResponse(result);
  }

  return Response.json(
    { error: { code: "usage", message: `no such endpoint: ${req.method} ${path}` } },
    { status: 404 },
  );
}
