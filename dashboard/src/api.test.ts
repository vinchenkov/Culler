// api.test.ts — the API is a thin verb mirror, the channel ref
// is derived not stored, and the outbox drains trivially.

import { describe, expect, test } from "bun:test";

import { handleApi, makeGenRef } from "./api";
import { openAttachmentFile } from "./hostfile";
import { nudgeResident, residentSocketPath } from "./resident-nudge";
import { FakeMc, apiReq, deps, domain, environment, ok } from "./test-helpers";
import { makeTestLogger } from "../../runner/shared/log";

const listWithSession = (id: string, bindings: Record<string, unknown>[]) =>
  ok({
    sessions: [
      { id, status: "active", active_bindings: bindings },
      { id: "h-other", status: "active", active_bindings: [{ surface: "dashboard", channel_ref: "dash-other" }] },
    ],
  });

describe("verb mirror", () => {
  test("Discord exposes only dashboard actions and keeps private activation commands inaccessible", async () => {
    const mc = new FakeMc().on(["discord", "show"], ok({ available: false }))
      .on(["discord", "check-bot"], ok({ bot: { name: "Culler" } }));
    const read = await handleApi(...apiReq("GET", "/api/discord"), deps({ mc }));
    expect(await read?.json()).toEqual({ available: false });
    for (const action of ["candidate", "stage", "apply", "quiesce"]) {
      const refused = await handleApi(...apiReq("POST", `/api/discord/${action}`, {}), deps({ mc }));
      expect(refused?.status).toBe(400);
    }
    expect(mc.calls).toEqual([["discord", "show"]]);
    const check = await handleApi(...apiReq("POST", "/api/discord/check-bot", { token: "private-test-token" }), deps({ mc }));
    expect(await check?.json()).toEqual({ bot: { name: "Culler" } });
  });
  test("Project deletion forwards the preview, explicit confirmation, and rejection", async () => {
    const preview = { project_id: "garden", title: "Garden", tasks: 4, reviews: 2, setup_operations: 3, blockers: [] };
    const mc = new FakeMc()
      .on(["project", "delete-preview", "garden"], ok(preview))
      .on(["project", "delete", "garden", "--confirm"], domain("Wait for this Project's setup operation to finish."));
    const read = await handleApi(...apiReq("GET", "/api/projects/garden/delete"), deps({ mc }));
    expect(await read?.json()).toEqual(preview);
    const absent = await handleApi(...apiReq("POST", "/api/projects/garden/delete", {}), deps({ mc }));
    expect(absent?.status).toBe(400);
    const refused = await handleApi(...apiReq("POST", "/api/projects/garden/delete", { confirm: true }), deps({ mc }));
    expect(refused?.status).toBe(400);
    expect(await refused?.json()).toMatchObject({ error: { message: "Wait for this Project's setup operation to finish." } });
    expect(mc.calls).toEqual([
      ["project", "delete-preview", "garden"], ["project", "delete", "garden", "--confirm"],
    ]);
  });

  test("release status and installer actions use the shared host commands", async () => {
    const mc = new FakeMc()
      .on(["__release", "status"], ok({ state: "unknown", installed: "a", detail: "GitHub refused access." }))
      .on(["__release", "open"], ok({ opened: true }));
    const result = await handleApi(...apiReq("GET", "/api/releases/status"), deps({ mc }));
    expect(await result?.json()).toMatchObject({ state: "unknown" });
    const refused = await handleApi(...apiReq("POST", "/api/releases/open", {}), deps({ mc }));
    expect(refused?.status).toBe(400);
    expect(mc.calls).toEqual([["__release", "status"]]);
    const opened = await handleApi(...apiReq("POST", "/api/releases/open", { confirm: true }), deps({ mc }));
    expect(await opened?.json()).toEqual({ opened: true });
    expect(mc.calls).toEqual([["__release", "status"], ["__release", "open"]]);
  });

  test("GET /api/sessions passes mc collie list through", async () => {
    const mc = new FakeMc().on(["collie", "list"], ok({ sessions: [{ id: "h-1" }] }));
    const res = await handleApi(...apiReq("GET", "/api/sessions"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual({ sessions: [{ id: "h-1" }] });
    expect(mc.calls).toEqual([["collie", "list"]]);
  });

  test("GET /api/sessions/:id/history passes through", async () => {
    const mc = new FakeMc().on(
      ["collie", "history", "h-1"],
      ok({ session_id: "h-1", status: "active", messages: [] }),
    );
    const res = await handleApi(...apiReq("GET", "/api/sessions/h-1/history"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(mc.calls).toEqual([["collie", "history", "h-1"]]);
  });

  test("POST /api/sessions starts with a generated dashboard ref", async () => {
    const mc = new FakeMc().on(["collie", "start"], ok({ session_id: "h-new", status: "active" }));
    const res = await handleApi(...apiReq("POST", "/api/sessions"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(mc.calls).toEqual([["collie", "start", "--from", "dashboard:dash-feedf00d"]]);
  });

  test("a domain rejection surfaces as 400 with the mc envelope", async () => {
    const mc = new FakeMc().on(["collie", "history"], domain('unknown Collie session "h-x"'));
    const res = await handleApi(...apiReq("GET", "/api/sessions/h-x/history"), deps({ mc }));
    expect(res?.status).toBe(400);
    const body = (await res?.json()) as { error: { code: string; message: string } };
    expect(body.error.code).toBe("domain-rejection");
  });

  test("an environment failure surfaces as 500 without leaking argv advice", async () => {
    const mc = new FakeMc().on(["collie", "list"], environment("spine unreachable"));
    const res = await handleApi(...apiReq("GET", "/api/sessions"), deps({ mc }));
    expect(res?.status).toBe(500);
    const body = (await res?.json()) as { error: { code: string; message: string } };
    expect(body.error.code).toBe("environment");
    expect(body.error.message).not.toContain("mc ");
  });

  test("unknown api endpoints are 404, non-api paths fall through as null", async () => {
    const res = await handleApi(...apiReq("GET", "/api/nope"), deps());
    expect(res?.status).toBe(404);
    expect(await handleApi(...apiReq("GET", "/"), deps())).toBeNull();
    expect(await handleApi(...apiReq("GET", "/app.js"), deps())).toBeNull();
  });
});

describe("channel ref derivation", () => {
  test("send reuses the session's existing dashboard binding", async () => {
    const mc = new FakeMc()
      .on(["collie", "list"], listWithSession("h-1", [
        { surface: "cli", channel_ref: "term" },
        { surface: "dashboard", channel_ref: "dash-11223344", bound_at: "t" },
      ]))
      .on(["collie", "send"], ok({ session_id: "h-1", message_id: 7, seq: 3, echoes: 1 }));
    const res = await handleApi(
      ...apiReq("POST", "/api/sessions/h-1/send", { body: "hello" }),
      deps({ mc }),
    );
    expect(res?.status).toBe(200);
    expect(mc.calls[1]).toEqual([
      "collie", "send", "h-1", "--from", "dashboard:dash-11223344", "--body", "hello",
    ]);
  });

  test("send generates a fresh ref when the session has no dashboard binding", async () => {
    const mc = new FakeMc()
      .on(["collie", "list"], listWithSession("h-1", [{ surface: "cli", channel_ref: "term" }]))
      .on(["collie", "send"], ok({ session_id: "h-1", message_id: 7, seq: 1, echoes: 0 }));
    await handleApi(...apiReq("POST", "/api/sessions/h-1/send", { body: "hello" }), deps({ mc }));
    expect(mc.calls[1]?.[4]).toBe("dashboard:dash-feedf00d");
  });

  test("resume derives the ref the same way", async () => {
    const mc = new FakeMc()
      .on(["collie", "list"], ok({ sessions: [] }))
      .on(["collie", "resume"], ok({ session_id: "h-1", status: "active", resumed: true }));
    await handleApi(...apiReq("POST", "/api/sessions/h-1/resume"), deps({ mc }));
    expect(mc.calls[1]).toEqual(["collie", "resume", "h-1", "--from", "dashboard:dash-feedf00d"]);
  });

  test("start, send, and resume nudge the resident with the session id after the verb succeeds", async () => {
    const nudges: string[] = [];
    const nudgeResident = async (id: string) => { nudges.push(id); };
    const mc = new FakeMc()
      .on(["collie", "start"], ok({ session_id: "h-new", status: "active" }))
      .on(["collie", "list"], listWithSession("h-1", [{ surface: "cli", channel_ref: "term" }]))
      .on(["collie", "send"], ok({ session_id: "h-1", message_id: 7, seq: 1, echoes: 0 }))
      .on(["collie", "resume"], ok({ session_id: "h-1", status: "active", resumed: true }));
    expect((await handleApi(...apiReq("POST", "/api/sessions"), deps({ mc, nudgeResident })))?.status).toBe(200);
    expect((await handleApi(...apiReq("POST", "/api/sessions/h-1/send", { body: "hi" }), deps({ mc, nudgeResident })))?.status).toBe(200);
    expect((await handleApi(...apiReq("POST", "/api/sessions/h-1/resume"), deps({ mc, nudgeResident })))?.status).toBe(200);
    expect(nudges).toEqual(["h-new", "h-1", "h-1"]);
  });

  test("a refused verb nudges nothing", async () => {
    const nudges: string[] = [];
    const mc = new FakeMc()
      .on(["collie", "list"], listWithSession("h-1", [{ surface: "cli", channel_ref: "term" }]))
      .on(["collie", "send"], domain("session h-1 has ended"));
    const res = await handleApi(...apiReq("POST", "/api/sessions/h-1/send", { body: "hi" }),
      deps({ mc, nudgeResident: async (id) => { nudges.push(id); } }));
    expect(res?.status).toBe(400);
    expect(nudges).toEqual([]);
  });

  test("a missing resident socket is logged and never fails the request", async () => {
    const log = makeTestLogger("dashboard");
    const mc = new FakeMc()
      .on(["collie", "list"], listWithSession("h-1", [{ surface: "cli", channel_ref: "term" }]))
      .on(["collie", "send"], ok({ session_id: "h-1", message_id: 7, seq: 1, echoes: 0 }));
    const res = await handleApi(...apiReq("POST", "/api/sessions/h-1/send", { body: "hi" }), deps({
      mc, log,
      nudgeResident: (id) => nudgeResident(residentSocketPath("/nonexistent/mc-home"), id),
    }));
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual({ session_id: "h-1", message_id: 7, seq: 1, echoes: 0 });
    const failed = log.records.filter((r) => r.event === "dash.nudge_failed");
    expect(failed).toHaveLength(1);
    expect(failed[0]).toMatchObject({ level: "warn", session_id: "h-1" });
  });

  test("send without a body is a usage 400 and spawns nothing", async () => {
    const mc = new FakeMc();
    const res = await handleApi(...apiReq("POST", "/api/sessions/h-1/send", {}), deps({ mc }));
    expect(res?.status).toBe(400);
    expect(mc.calls).toEqual([]);
  });

  test("a non-JSON content type is refused before parsing (simple-request fence)", async () => {
    const mc = new FakeMc();
    const url = new URL("http://127.0.0.1/api/sessions/h-1/send");
    const req = new Request(url, {
      method: "POST",
      headers: { "content-type": "text/plain" },
      body: '{"body":"smuggled"}',
    });
    const res = await handleApi(req, url, deps({ mc }));
    expect(res?.status).toBe(400);
    expect(mc.calls).toEqual([]);
  });

  test("an mc usage envelope (exit 2) is a caller-caused 400, not a 500", async () => {
    const mc = new FakeMc().on(["collie", "history"], {
      code: 2,
      json: { error: { code: "usage", message: "flag provided but not defined" } },
      stderr: "usage",
    });
    const res = await handleApi(...apiReq("GET", "/api/sessions/--x/history"), deps({ mc }));
    expect(res?.status).toBe(400);
  });

  test("makeGenRef mints dash-<8 hex>", () => {
    const ref = makeGenRef()();
    expect(ref).toMatch(/^dash-[0-9a-f]{8}$/);
  });
});

describe("outbox drain", () => {
  test("drains every polled row and reports the count", async () => {
    const mc = new FakeMc()
      .on(["outbox", "poll"], ok({
        surface: "dashboard",
        rows: [
          { id: 4, kind: "collie_reply", payload: {} },
          { id: 9, kind: "collie_echo", payload: {} },
        ],
      }))
      .on(["outbox", "ack"], ok({ acked: true }));
    const res = await handleApi(...apiReq("POST", "/api/outbox/drain"), deps({ mc }));
    expect(await res?.json()).toEqual({ polled: 2, drained: 2 });
    expect(mc.calls).toEqual([
      ["outbox", "poll", "--surface", "dashboard", "--limit", "100"],
      ["outbox", "ack", "4", "--surface", "dashboard"],
      ["outbox", "ack", "9", "--surface", "dashboard"],
    ]);
  });

  test("a failed ack is logged and skipped, not fatal (at-least-once)", async () => {
    const log = makeTestLogger("dashboard");
    const mc = new FakeMc()
      .on(["outbox", "poll"], ok({
        surface: "dashboard",
        rows: [{ id: 4, kind: "collie_reply" }, { id: 9, kind: "collie_echo" }],
      }))
      .on(["outbox", "ack", "4"], domain("outbox row 4 belongs to surface \"cli\""))
      .on(["outbox", "ack", "9"], ok({ acked: true }));
    const res = await handleApi(
      ...apiReq("POST", "/api/outbox/drain"),
      deps({ mc, log }),
    );
    expect(await res?.json()).toEqual({ polled: 2, drained: 1 });
    // Structured, not prose: the row and the kind are fields, so an operator
    // can find every refused ack across the file without a regex.
    expect(log.records).toMatchObject([
      { level: "warn", comp: "dashboard", event: "outbox.ack_failed", row: 4, kind: "collie_reply" },
    ]);
  });

  test("acks only the kinds history renders; blocked and health rows survive the drain", async () => {
    // The trivial ack covers collie_reply and collie_echo, because for those
    // two the history pane is the render source. Every other kind reaches the operator
    // through a pane of its own, and acking one here converts a missed
    // delivery into an unreplayable one.
    const mc = new FakeMc()
      .on(["outbox", "poll"], ok({
        surface: "dashboard",
        rows: [
          { id: 1, kind: "collie_reply", payload: {} },
          { id: 3, kind: "blocked_alert", payload: { task_id: 8 } },
          { id: 4, kind: "health", payload: {} },
          { id: 5, kind: "collie_echo", payload: {} },
        ],
      }))
      .on(["outbox", "ack"], ok({ acked: true }));
    const res = await handleApi(...apiReq("POST", "/api/outbox/drain"), deps({ mc }));
    expect(await res?.json()).toEqual({ polled: 4, drained: 2 });
    expect(mc.calls).toEqual([
      ["outbox", "poll", "--surface", "dashboard", "--limit", "100"],
      ["outbox", "ack", "1", "--surface", "dashboard"],
      ["outbox", "ack", "5", "--surface", "dashboard"],
    ]);
  });

  test("a row whose kind is absent or unrecognized is left undelivered", async () => {
    // Fail closed: an unknown kind is not proven renderable by history, and an
    // ack cannot be repaid.
    const mc = new FakeMc()
      .on(["outbox", "poll"], ok({
        surface: "dashboard",
        rows: [{ id: 7 }, { id: 8, kind: "some_future_kind" }],
      }))
      .on(["outbox", "ack"], ok({ acked: true }));
    const res = await handleApi(...apiReq("POST", "/api/outbox/drain"), deps({ mc }));
    expect(await res?.json()).toEqual({ polled: 2, drained: 0 });
    expect(mc.calls).toEqual([
      ["outbox", "poll", "--surface", "dashboard", "--limit", "100"],
    ]);
  });
});

describe("review queue", () => {
  test("GET /api/packets passes mc packet list through untouched", async () => {
    // Archived rows and all: filtering the live queue is interpretation, and
    // the API holds no interpretation.
    const rows = {
      packets: [
        { task_id: 4, thesis: "live", archived: 0, created_at: "2026-07-25 09:00:00" },
        { task_id: 5, thesis: "done", archived: 1, created_at: "2026-07-24 09:00:00" },
      ],
    };
    const mc = new FakeMc().on(["packet", "list"], ok(rows));
    const res = await handleApi(...apiReq("GET", "/api/packets"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual(rows);
    expect(mc.calls).toEqual([["packet", "list"]]);
  });

  test("approve fires the verb with no reason", async () => {
    const mc = new FakeMc().on(["packet", "decide"], ok({ task_id: 4, decision: "approved", archived: false }));
    const res = await handleApi(
      ...apiReq("POST", "/api/packets/4/decide", { decision: "approve" }),
      deps({ mc }),
    );
    expect(res?.status).toBe(200);
    expect(mc.calls).toEqual([["packet", "decide", "4", "--approve"]]);
  });

  test("decide nudges the resident with packet-decide", async () => {
    const nudges: string[] = [];
    const mc = new FakeMc().on(["packet", "decide"], ok({ task_id: 4, decision: "approved", archived: false }));
    const res = await handleApi(
      ...apiReq("POST", "/api/packets/4/decide", { decision: "approve" }),
      deps({ mc, nudgeResident: async (line) => { nudges.push(line); } }),
    );
    expect(res?.status).toBe(200);
    expect(nudges).toEqual(["packet-decide"]);
  });

  test("a refused decision nudges nothing", async () => {
    const nudges: string[] = [];
    const mc = new FakeMc().on(["packet", "decide"], domain("the review packet changed"));
    const res = await handleApi(
      ...apiReq("POST", "/api/packets/4/decide", { decision: "approve" }),
      deps({ mc, nudgeResident: async (line) => { nudges.push(line); } }),
    );
    expect(res?.status).toBe(400);
    expect(nudges).toEqual([]);
  });

  test("a failed decision nudge is logged and leaves the response unchanged", async () => {
    const log = makeTestLogger("dashboard");
    const mc = new FakeMc().on(["packet", "decide"], ok({ task_id: 4, decision: "approved", archived: false }));
    const res = await handleApi(...apiReq("POST", "/api/packets/4/decide", { decision: "approve" }), deps({
      mc, log,
      nudgeResident: (line) => nudgeResident(residentSocketPath("/nonexistent/mc-home"), line),
    }));
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual({ task_id: 4, decision: "approved", archived: false });
    const failed = log.records.filter((r) => r.event === "dash.nudge_failed");
    expect(failed).toHaveLength(1);
    expect(failed[0]).toMatchObject({ level: "warn", task_id: 4 });
  });

  test("revise and cancel carry the operator's reason verbatim", async () => {
    for (const decision of ["revise", "cancel"] as const) {
      const mc = new FakeMc().on(["packet", "decide"], ok({ task_id: 7, decision }));
      await handleApi(
        ...apiReq("POST", "/api/packets/7/decide", { decision, reason: "needs a rollback plan" }),
        deps({ mc }),
      );
      expect(mc.calls).toEqual([
        ["packet", "decide", "7", `--${decision}`, "--reason", "needs a rollback plan"],
      ]);
    }
  });

  // The reason rules live in the verb (forbidden on approve, required on
  // revise/cancel). The API must not restate them: a second copy drifts, and
  // the drift is silent. A reason on approve therefore still reaches mc, and
  // mc's own rejection is what the operator sees.
  test("a reason on approve is passed on and refused by the verb, not by us", async () => {
    const mc = new FakeMc().on(
      ["packet", "decide"],
      domain("mc packet decide --approve takes no --reason"),
    );
    const res = await handleApi(
      ...apiReq("POST", "/api/packets/4/decide", { decision: "approve", reason: "why" }),
      deps({ mc }),
    );
    expect(res?.status).toBe(400);
    expect(mc.calls).toEqual([["packet", "decide", "4", "--approve", "--reason", "why"]]);
  });

  test("an unknown decision never reaches mc", async () => {
    const mc = new FakeMc();
    const res = await handleApi(
      ...apiReq("POST", "/api/packets/4/decide", { decision: "delete" }),
      deps({ mc }),
    );
    expect(res?.status).toBe(400);
    expect(mc.calls).toEqual([]);
  });
});

describe("board and activity", () => {
  test("GET /api/tasks mirrors mc task list with its live-work default", async () => {
    const mc = new FakeMc().on(["task", "list"], ok({ tasks: [{ id: 4, status: "seeded" }] }));
    const res = await handleApi(...apiReq("GET", "/api/tasks"), deps({ mc }));
    expect(res?.status).toBe(200);
    // No --all: the Board shows live work, and the verb's default IS that.
    expect(mc.calls).toEqual([["task", "list"]]);
  });

  test("GET /api/tasks/<id> mirrors mc task get, missing task included", async () => {
    const row = { id: 4, status: "seeded", description: "the prose the list omits" };
    const mc = new FakeMc()
      .on(["task", "get", "4"], ok(row))
      .on(["task", "get", "9"], { code: 1, json: null, stderr: "no task 9" });
    const res = await handleApi(...apiReq("GET", "/api/tasks/4"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual(row);
    const missing = await handleApi(...apiReq("GET", "/api/tasks/9"), deps({ mc }));
    expect(missing?.status).toBe(400);
    expect(mc.calls).toEqual([
      ["task", "get", "4"],
      ["task", "get", "9"],
    ]);
    // Only a numeric id reaches the verb.
    const bad = await handleApi(...apiReq("GET", "/api/tasks/four"), deps({ mc }));
    expect(bad?.status).toBe(404);
  });

  test("GET /api/leases mirrors mc lease list, one row per Project", async () => {
    const rows = {
      leases: [
        { project: "alpha", status: "held", run_id: "r-1", owner: "worker", subject: 4 },
        { project: "beta", status: "free", run_id: null, owner: null, subject: null },
      ],
    };
    const mc = new FakeMc().on(["lease", "list"], ok(rows));
    const res = await handleApi(...apiReq("GET", "/api/leases"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual(rows);
    expect(mc.calls).toEqual([["lease", "list"]]);
  });

  test("GET /api/settings mirrors mc settings get", async () => {
    const mc = new FakeMc().on(["settings", "get"], ok({ review_wip_cap: 3, heartbeat_interval_s: 60 }));
    const res = await handleApi(...apiReq("GET", "/api/settings"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual({ review_wip_cap: 3, heartbeat_interval_s: 60 });
    expect(mc.calls).toEqual([["settings", "get"]]);
  });

  test("an uninitialized spine surfaces the settings verb's own rejection", async () => {
    const mc = new FakeMc().on(["settings", "get"], domain("no settings row (uninitialized spine)"));
    const res = await handleApi(...apiReq("GET", "/api/settings"), deps({ mc }));
    expect(res?.status).toBe(400);
  });

  test("the retired /api/lock is gone, not quietly aliased", async () => {
    const res = await handleApi(...apiReq("GET", "/api/lock"), deps());
    expect(res?.status).toBe(404);
  });

  test("GET /api/activity fetches ONE stream, unsplit", async () => {
    // The page shows one activity stream and a separate error stream.
    // Splitting by kind is
    // interpretation, so the API fetches once and the browser splits — asking
    // mc twice would also double the process cost per poll.
    const mc = new FakeMc().on(["activity", "list"], ok({ activity: [], limit: 100 }));
    const res = await handleApi(...apiReq("GET", "/api/activity"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(mc.calls).toEqual([["activity", "list"]]);
  });
});

describe("system health", () => {
  test("GET /api/activity?kind= passes the verb's own filter through", async () => {
    const mc = new FakeMc().on(["activity", "list"], ok({ activity: [], limit: 100 }));
    await handleApi(...apiReq("GET", "/api/activity?kind=dispatch.health"), deps({ mc }));
    expect(mc.calls).toEqual([["activity", "list", "--kind", "dispatch.health"]]);
  });

  test("an empty kind is not sent as an empty filter", async () => {
    const mc = new FakeMc().on(["activity", "list"], ok({ activity: [], limit: 100 }));
    await handleApi(...apiReq("GET", "/api/activity?kind="), deps({ mc }));
    expect(mc.calls).toEqual([["activity", "list"]]);
  });

  // Without this the browser split the verb's 100-row default, and on a busy
  // deployment all hundred were dispatch.spawn — so the error pane claimed
  // nothing had refused while dispatch.health rows sat unread in the spine.
  test("GET /api/activity?limit= passes the verb's own limit through", async () => {
    const mc = new FakeMc().on(["activity", "list"], ok({ activity: [], limit: 1000 }));
    await handleApi(...apiReq("GET", "/api/activity?limit=1000"), deps({ mc }));
    expect(mc.calls).toEqual([["activity", "list", "--limit", "1000"]]);
  });

  test("kind and limit travel together", async () => {
    const mc = new FakeMc().on(["activity", "list"], ok({ activity: [], limit: 1000 }));
    await handleApi(...apiReq("GET", "/api/activity?kind=dispatch.health&limit=250"), deps({ mc }));
    expect(mc.calls).toEqual([["activity", "list", "--kind", "dispatch.health", "--limit", "250"]]);
  });

  // The clamp stays the verb's; only the argv shape is guarded here.
  test("a non-numeric limit is refused before any spawn", async () => {
    const mc = new FakeMc().on(["activity", "list"], ok({ activity: [] }));
    const res = await handleApi(...apiReq("GET", "/api/activity?limit=all"), deps({ mc }));
    expect(res!.status).toBe(400);
    expect(mc.calls).toEqual([]);
  });

  test("an empty limit is not sent as an empty flag", async () => {
    const mc = new FakeMc().on(["activity", "list"], ok({ activity: [], limit: 100 }));
    await handleApi(...apiReq("GET", "/api/activity?limit="), deps({ mc }));
    expect(mc.calls).toEqual([["activity", "list"]]);
  });

  test("GET /api/doctor mirrors mc doctor", async () => {
    const mc = new FakeMc().on(["doctor"], ok({ findings: [{ check: "spine", status: "ok" }] }));
    const res = await handleApi(...apiReq("GET", "/api/doctor"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(mc.calls).toEqual([["doctor"]]);
  });
});

// The review packet is the human gate, and a gate no one can open is
// not one. Every render is written inside a `--rm` container and recorded as
// a container path, so serving it is a two-hop crossing: the spine holds the
// path, the mount allowlist maps it to the host.
describe("packet render", () => {
  const packets = ok({
    packets: [
      { task_id: 37, render_path: "/mc/artifacts/llm-council/37/packet/packet.md", archived: 0 },
      { task_id: 38, render_path: "", archived: 0 },
    ],
  });

  test("GET /api/packets/:task/render serves the resolved bytes", async () => {
    const mc = new FakeMc()
      .on(["packet", "list"], packets)
      .on(["__artifact-host-path"], ok({ host_path: "/ws/.mc-artifacts/37/packet/packet.md" }));
    const res = await handleApi(
      ...apiReq("GET", "/api/packets/37/render"),
      deps({ mc, readRender: () => Promise.resolve("# packet 37\n") }),
    );
    expect(res?.status).toBe(200);
    // The recorded filename says markdown, so the content type does too.
    expect(res?.headers.get("content-type")).toContain("text/markdown");
    expect(await res?.text()).toBe("# packet 37\n");
    // The path handed to the resolver comes from the SPINE row, never the URL.
    expect(mc.calls).toEqual([
      ["packet", "list"],
      ["__artifact-host-path", "/mc/artifacts/llm-council/37/packet/packet.md"],
    ]);
  });

  test("GET /api/artifact-roots mirrors the map the browser translates with", async () => {
    const mc = new FakeMc().on(["__artifact-roots"], ok({ roots: { "llm-council": "/ws/.mc-artifacts" } }));
    const res = await handleApi(...apiReq("GET", "/api/artifact-roots"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual({ roots: { "llm-council": "/ws/.mc-artifacts" } });
    expect(mc.calls).toEqual([["__artifact-roots"]]);
  });

  test("an HTML render is typed html and can never run in the dashboard's origin", async () => {
    const htmlPackets = ok({
      packets: [{ task_id: 41, render_path: "/mc/artifacts/llm-council/41/packet/packet.html", archived: 0 }],
    });
    const mc = new FakeMc()
      .on(["packet", "list"], htmlPackets)
      .on(["__artifact-host-path"], ok({ host_path: "/ws/.mc-artifacts/41/packet/packet.html" }));
    const res = await handleApi(
      ...apiReq("GET", "/api/packets/41/render"),
      deps({ mc, readRender: () => Promise.resolve("<h1>packet 41</h1>") }),
    );
    expect(res?.status).toBe(200);
    expect(res?.headers.get("content-type")).toContain("text/html");
    // Agent-authored bytes opened directly in a tab get a forced sandbox:
    // unique origin, no scripts — the Review pane's own frame is the reader.
    expect(res?.headers.get("content-security-policy")).toBe("sandbox");
    expect(res?.headers.get("x-content-type-options")).toBe("nosniff");
  });

  test("an extensionless render stays plain text", async () => {
    const barePackets = ok({
      packets: [{ task_id: 42, render_path: "/mc/artifacts/llm-council/42/packet/render", archived: 0 }],
    });
    const mc = new FakeMc()
      .on(["packet", "list"], barePackets)
      .on(["__artifact-host-path"], ok({ host_path: "/ws/.mc-artifacts/42/packet/render" }));
    const res = await handleApi(
      ...apiReq("GET", "/api/packets/42/render"),
      deps({ mc, readRender: () => Promise.resolve("prose") }),
    );
    expect(res?.headers.get("content-type")).toContain("text/plain");
  });

  test("a packet with no recorded render is 404, not a resolver call", async () => {
    const mc = new FakeMc().on(["packet", "list"], packets);
    const res = await handleApi(...apiReq("GET", "/api/packets/38/render"), deps({ mc }));
    expect(res?.status).toBe(404);
    expect(mc.calls).toEqual([["packet", "list"]]);
  });

  test("an unknown task is 404", async () => {
    const mc = new FakeMc().on(["packet", "list"], packets);
    const res = await handleApi(...apiReq("GET", "/api/packets/9999/render"), deps({ mc }));
    expect(res?.status).toBe(404);
  });

  test("a resolver refusal passes through as the verb's own rejection", async () => {
    const mc = new FakeMc()
      .on(["packet", "list"], packets)
      .on(["__artifact-host-path"], domain("no mount allowlist entry carries artifact target"));
    const res = await handleApi(...apiReq("GET", "/api/packets/37/render"), deps({ mc }));
    expect(res?.status).toBe(400);
    expect(await res?.json()).toMatchObject({
      error: { message: expect.stringContaining("artifact target") },
    });
  });

  test("an unreadable render is an environment failure, never empty 200 bytes", async () => {
    const mc = new FakeMc()
      .on(["packet", "list"], packets)
      .on(["__artifact-host-path"], ok({ host_path: "/ws/.mc-artifacts/37/packet/packet.md" }));
    const res = await handleApi(
      ...apiReq("GET", "/api/packets/37/render"),
      deps({ mc, readRender: () => Promise.reject(new Error("ENOENT")) }),
    );
    expect(res?.status).toBe(500);
  });
});

// The attachment bytes: the endpoint is a thin mirror of
// `mc __attachment-bytes`. The four address fields ride to the verb verbatim
// (never a path), its verified metadata becomes the headers unchanged, and
// each of its distinguishable refusals keeps its own status.
describe("attachment bytes", () => {
  const sha = "a".repeat(64);
  const claimPath = `/api/sessions/h-9/attachments/in/${sha}/4`;
  const claimArgv = ["__attachment-bytes", "h-9", "in", sha, "4"];
  const servedPng = ok({
    session_id: "h-9",
    direction: "in",
    sha256: sha,
    size: 4,
    media_type: "image/png",
    display_name: "capture.png",
    inline: true,
    host_path: `/home/attachments/h-9/in/published/sha256/aa/${sha}`,
  });
  const bytes = () => Promise.resolve(new Blob([new Uint8Array([1, 2, 3, 4])]));

  test("an inline claim streams with its declared type and the recorded length", async () => {
    const mc = new FakeMc().on(["__attachment-bytes"], servedPng);
    const res = await handleApi(...apiReq("GET", claimPath), deps({ mc, openAttachment: bytes }));
    expect(res?.status).toBe(200);
    expect(res?.headers.get("content-type")).toBe("image/png");
    expect(res?.headers.get("content-length")).toBe("4");
    expect(res?.headers.get("content-disposition")).toBe("inline");
    expect(res?.headers.get("x-content-type-options")).toBe("nosniff");
    expect(new Uint8Array(await res!.arrayBuffer())).toEqual(new Uint8Array([1, 2, 3, 4]));
    // The verb sees exactly the four claim fields, never a path.
    expect(mc.calls).toEqual([claimArgv]);
  });

  test("a non-inline claim is a named download, never its declared type", async () => {
    const served = ok({
      ...(servedPng.json as Record<string, unknown>),
      media_type: "application/pdf",
      display_name: "notes.pdf",
      inline: false,
    });
    const mc = new FakeMc().on(["__attachment-bytes"], served);
    const res = await handleApi(...apiReq("GET", claimPath), deps({ mc, openAttachment: bytes }));
    expect(res?.status).toBe(200);
    // The declared type is metadata for the download, not a render hint: a
    // surface-chosen content type would let agent bytes render in-origin.
    expect(res?.headers.get("content-type")).toBe("application/octet-stream");
    expect(res?.headers.get("content-disposition")).toContain('attachment; filename="notes.pdf"');
  });

  test("a malformed address is refused before any mc spawn", async () => {
    const mc = new FakeMc();
    for (const bad of [
      `/api/sessions/h-9/attachments/up/${sha}/4`, // direction outside in|out
      `/api/sessions/h-9/attachments/in/${sha.slice(0, 63)}z/4`, // non-hex digest
      `/api/sessions/h-9/attachments/in/${sha.toUpperCase()}/4`, // uppercase digest
      `/api/sessions/h-9/attachments/in/${sha}/04`, // not an exact decimal
      `/api/sessions/h-9/attachments/in/${sha}/four`,
    ]) {
      const res = await handleApi(...apiReq("GET", bad), deps({ mc }));
      expect(res?.status).toBe(400);
      expect(await res?.json()).toMatchObject({ error: { code: "usage" } });
    }
    expect(mc.calls).toEqual([]);
  });

  test("an unconfirmed claim is 404", async () => {
    const mc = new FakeMc().on(
      ["__attachment-bytes"],
      domain("no conversation attachment claim matches that identity"),
    );
    const res = await handleApi(...apiReq("GET", claimPath), deps({ mc }));
    expect(res?.status).toBe(404);
  });

  test("unmaterialized bytes are 404", async () => {
    const mc = new FakeMc().on(
      ["__attachment-bytes"],
      domain("attachment bytes are not materialized in this deployment's attachment tree"),
    );
    const res = await handleApi(...apiReq("GET", claimPath), deps({ mc }));
    expect(res?.status).toBe(404);
  });

  test("an over-cap claim is 413", async () => {
    const mc = new FakeMc().on(
      ["__attachment-bytes"],
      domain("attachment claim of 268435456 bytes exceeds the 67108864-byte serve cap"),
    );
    const res = await handleApi(...apiReq("GET", claimPath), deps({ mc }));
    expect(res?.status).toBe(413);
  });

  test("bytes that disagree with the spine are a 500, never served", async () => {
    const mc = new FakeMc().on(
      ["__attachment-bytes"],
      domain("published attachment bytes disagree with the recorded digest"),
    );
    const res = await handleApi(...apiReq("GET", claimPath), deps({ mc }));
    expect(res?.status).toBe(500);
  });

  test("a verb usage refusal is 400 and an environment failure is 500", async () => {
    const usage = new FakeMc().on(["__attachment-bytes"], {
      code: 2,
      json: { error: { code: "usage", message: "attachment session id is not an exact canonical id" } },
      stderr: "mc: usage",
    });
    expect((await handleApi(...apiReq("GET", claimPath), deps({ mc: usage })))?.status).toBe(400);
    const env = new FakeMc().on(["__attachment-bytes"], environment("ensure warm helper: docker gone"));
    expect((await handleApi(...apiReq("GET", claimPath), deps({ mc: env })))?.status).toBe(500);
  });

  test("a confirmed claim whose file vanished under the GC race is 404", async () => {
    const mc = new FakeMc().on(["__attachment-bytes"], servedPng);
    const res = await handleApi(
      ...apiReq("GET", claimPath),
      deps({ mc, openAttachment: () => Promise.resolve(null) }),
    );
    expect(res?.status).toBe(404);
  });

  test("a file swapped to a different length after verification is a 500, never served", async () => {
    // The verb verified 4 exact bytes; what the open now finds is 3. The
    // published tree's outbound half is agent-writable inside its container,
    // so a swap between the verb's verification and this open is an attack,
    // not a race — refused like a spine disagreement.
    const mc = new FakeMc().on(["__attachment-bytes"], servedPng);
    const res = await handleApi(
      ...apiReq("GET", claimPath),
      deps({ mc, openAttachment: () => Promise.resolve(new Blob([new Uint8Array([1, 2, 3])])) }),
    );
    expect(res?.status).toBe(500);
  });
});

describe("the real openAttachment seam", () => {
  test("refuses a symlink where the verified file stood, serves the regular file", async () => {
    const { mkdtempSync, rmSync, symlinkSync, writeFileSync } = await import("node:fs");
    const { tmpdir } = await import("node:os");
    const { join } = await import("node:path");
    const scratch = mkdtempSync(join(tmpdir(), "attachment-open-"));
    try {
      const regular = join(scratch, "regular");
      const secret = join(scratch, "secret");
      const planted = join(scratch, "planted");
      writeFileSync(regular, new Uint8Array([1, 2, 3, 4]));
      writeFileSync(secret, "operator-private");
      symlinkSync(secret, planted);
      const served = await openAttachmentFile(regular);
      expect(served).not.toBeNull();
      expect(new Uint8Array(await served!.arrayBuffer())).toEqual(new Uint8Array([1, 2, 3, 4]));
      // A link planted at the verified path after the verb's O_NOFOLLOW walk
      // must not be followed by the serving open either.
      expect(await openAttachmentFile(planted)).toBeNull();
      expect(await openAttachmentFile(join(scratch, "vanished"))).toBeNull();
    } finally {
      rmSync(scratch, { recursive: true, force: true });
    }
  });
});

// The dashboard acks no polled row on sight, which would leave
// `blocked_alert` and `health` undelivered forever and would hold the System
// Health outbox delivery lag red by construction.
//
// A tab already renders these two kinds' content from the spine rather than
// from the row: the Board draws every task card with `blocked: <reason>`, and
// System Health draws the `dispatch.health` stream. So the pane render is the
// confirmed delivery, and it delivers strictly MORE than the pending rows
// carry — current truth for the whole board, not one stale announcement each.
describe("outbox delivery for displayed records", () => {
  for (const [kind, ids] of [["blocked_alert", [8]], ["health", [14]]] as const) {
    test(`${kind} confirms exactly the displayed records`, async () => {
      const mc = new FakeMc().on(["outbox", "ack-rendered"], ok({ acked: 1 }));
      const res = await handleApi(...apiReq("POST", `/api/outbox/${kind}/ack`, { ids }), deps({ mc }));
      expect(res?.status).toBe(200);
      expect(await res?.json()).toEqual({ acked: 1 });
      expect(mc.calls).toEqual([["outbox", "ack-rendered", "--surface", "dashboard", "--kind", kind, "--ids", JSON.stringify(ids)]]);
    });
  }
  test("an unknown kind reaches no command", async () => {
    const mc = new FakeMc();
    const res = await handleApi(...apiReq("POST", "/api/outbox/nonsense/ack", { ids: [8] }), deps({ mc }));
    expect(res?.status).toBe(400);
    expect(mc.calls).toEqual([]);
  });
  test("a failed acknowledgment reaches the browser", async () => {
    const mc = new FakeMc().on(["outbox", "ack-rendered"], domain("acknowledgment refused"));
    const res = await handleApi(...apiReq("POST", "/api/outbox/health/ack", { ids: [14] }), deps({ mc }));
    expect(res?.status).toBe(400);
  });
});

// System Health shows the outbox delivery lag. It is the one signal that
// shows rows that pile up undelivered.
describe("outbox delivery lag", () => {
  const pollFor = (surface: string, rows: unknown[]) => ({ rows, surface });

  test("reports the count and the oldest row per surface", async () => {
    const mc = new FakeMc()
      .on(["outbox", "poll", "--surface", "discord"], ok(pollFor("discord", [])))
      .on(
        ["outbox", "poll", "--surface", "dashboard"],
        ok(
          pollFor("dashboard", [
            { id: 1, kind: "blocked_alert", created_at: "2026-08-07 16:59:50" },
            { id: 2, kind: "health", created_at: "2026-08-08 10:00:00" },
          ]),
        ),
      )
      .on(
        ["outbox", "poll", "--surface", "cli"],
        ok(pollFor("cli", [{ id: 40, kind: "collie_reply", created_at: "2026-08-08 22:14:21" }])),
      );
    const res = await handleApi(...apiReq("GET", "/api/outbox/lag"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual({
      surfaces: [
        { surface: "discord", undelivered: 0, oldest_created_at: null, capped: false },
        { surface: "dashboard", undelivered: 2, oldest_created_at: "2026-08-07 16:59:50", capped: false },
        { surface: "cli", undelivered: 1, oldest_created_at: "2026-08-08 22:14:21", capped: false },
      ],
    });
  });

  test("says so when the poll limit truncates the count", async () => {
    // A count that is really a floor must not read as a total. Silence here
    // would make a growing backlog look like a steady one.
    const many = Array.from({ length: 100 }, (_, i) => ({
      id: i + 1,
      kind: "health",
      created_at: "2026-08-01 00:00:00",
    }));
    const mc = new FakeMc()
      .on(["outbox", "poll", "--surface", "discord"], ok(pollFor("discord", [])))
      .on(["outbox", "poll", "--surface", "dashboard"], ok(pollFor("dashboard", many)))
      .on(["outbox", "poll", "--surface", "cli"], ok(pollFor("cli", [])));
    const body = (await (await handleApi(...apiReq("GET", "/api/outbox/lag"), deps({ mc })))?.json()) as {
      surfaces: { surface: string; capped: boolean }[];
    };
    expect(body.surfaces.find((s) => s.surface === "dashboard")?.capped).toBe(true);
  });

  test("a refused poll refuses the whole reading", async () => {
    // Half the surfaces is not a lag reading; it is a smaller number that looks
    // like good news.
    const mc = new FakeMc()
      .on(["outbox", "poll", "--surface", "discord"], ok(pollFor("discord", [])))
      .on(["outbox", "poll", "--surface", "dashboard"], domain("spine unreachable"))
      .on(["outbox", "poll", "--surface", "cli"], ok(pollFor("cli", [])));
    const res = await handleApi(...apiReq("GET", "/api/outbox/lag"), deps({ mc }));
    expect(res?.status).toBe(400);
  });
});

// The reads the Pipeline, Configs, and System Logs panes consume (PAGES.md
// pages 5-7, operator-directed 2026-08-17). Each is a one-line mirror; the
// verbs carry their own filters, redaction, and refusals.
describe("pipeline, configs, and logs reads", () => {
  test("GET /api/runs mirrors mc run list", async () => {
    const mc = new FakeMc().on(["run", "list"], ok({ runs: [{ id: "r-1", role: "worker" }] }));
    const res = await handleApi(...apiReq("GET", "/api/runs"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(mc.calls).toEqual([["run", "list"]]);
  });

  test("GET /api/wiki-states mirrors mc wiki-state list", async () => {
    const rows = { wiki_states: [{ project: "alpha", state: "current", state_text: "The Project wiki is current." }] };
    const mc = new FakeMc().on(["wiki-state", "list"], ok(rows));
    const res = await handleApi(...apiReq("GET", "/api/wiki-states"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual(rows);
    expect(mc.calls).toEqual([["wiki-state", "list"]]);
  });

  test("a failed wiki state read returns the failure of the verb", async () => {
    const mc = new FakeMc().on(["wiki-state", "list"], environment("spine helper unreachable"));
    const res = await handleApi(...apiReq("GET", "/api/wiki-states"), deps({ mc }));
    expect(res?.status).toBe(500);
    expect(((await res?.json()) as { error: { code: string } }).error.code).toBe("environment");
  });

  test("GET /api/projects mirrors mc project list", async () => {
    const mc = new FakeMc().on(["project", "list"], ok({ projects: [{ id: "ws-test" }] }));
    const res = await handleApi(...apiReq("GET", "/api/projects"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(mc.calls).toEqual([["project", "list"]]);
  });

  test("the dashboard address mirrors no wiki command", async () => {
    const mc = new FakeMc();
    expect((await handleApi(...apiReq("GET", "/api/projects/alpha/wiki"), deps({ mc })))?.status).toBe(404);
    expect((await handleApi(...apiReq("GET", "/api/projects/alpha/wiki/file?commit=a&path=b"), deps({ mc })))?.status).toBe(404);
    expect(mc.calls).toEqual([]);
  });

  test("GET /api/config mirrors mc config show", async () => {
    const mc = new FakeMc().on(["config", "show"], ok({ families: [{ family: "budgets", knobs: [] }] }));
    const res = await handleApi(...apiReq("GET", "/api/config"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(mc.calls).toEqual([["config", "show"]]);
  });

  test("POST /api/config sends the exact edit request through standard input", async () => {
    const body = { changes: [{ key: "timing.backup_retain", expected: 48, value: 12 }] };
    const result = { families: [{ family: "timing", knobs: [{ key: "timing.backup_retain", value: 12 }] }] };
    const calls: unknown[] = [];
    const mc = { async run(args: string[], input?: string) { calls.push([args, input]); return ok(result); } };
    const response = await handleApi(...apiReq("POST", "/api/config", body), deps({ mc }));
    expect(response?.status).toBe(200);
    expect(await response?.json()).toEqual(result);
    expect(calls).toEqual([[["config", "save"], JSON.stringify(body)]]);
  });

  test("POST /api/config preserves a save rejection", async () => {
    const mc = new FakeMc().on(["config", "save"], domain("The setting changed elsewhere."));
    const response = await handleApi(...apiReq("POST", "/api/config", { changes: [] }), deps({ mc }));
    expect(response?.status).toBe(400);
    expect((await response?.json()).error.message).toContain("changed elsewhere");
  });

  test("a missing config.toml surfaces the verb's own rejection", async () => {
    const mc = new FakeMc().on(["config", "show"], domain("load operator config failed"));
    const res = await handleApi(...apiReq("GET", "/api/config"), deps({ mc }));
    expect(res?.status).toBe(400);
  });

  test("GET /api/routing mirrors mc routing show", async () => {
    const mc = new FakeMc().on(["routing", "show"], ok({ routes: [{ role: "collie" }] }));
    const res = await handleApi(...apiReq("GET", "/api/routing"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(mc.calls).toEqual([["routing", "show"]]);
  });

  test("POST /api/routing mirrors mc routing set and returns the verb's JSON verbatim", async () => {
    const table = {
      routes: [{ role: "worker", harness: "claude-sdk", binding: "claude", model: "claude-opus-4-8" }],
      bindings: [{ id: "claude", harness: "claude-sdk", model: "claude-opus-4-8" }],
    };
    const mc = new FakeMc().on(["routing", "set"], ok(table));
    const res = await handleApi(
      ...apiReq("POST", "/api/routing", { role: "worker", binding: "claude" }),
      deps({ mc }),
    );
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual(table);
    expect(mc.calls).toEqual([["routing", "set", "--role", "worker", "--binding", "claude"]]);
  });

  test("POST /api/routing passes the verb's rejection through as the mc envelope", async () => {
    // A disabled binding and an unknown role both reach the verb; the API
    // restates neither list.
    for (const [body, message] of [
      [{ role: "worker", binding: "endpoint" }, 'routing set: binding "endpoint" is not enabled'],
      [{ role: "painter", binding: "claude" }, 'routing set: unknown role "painter"'],
    ] as const) {
      const mc = new FakeMc().on(["routing", "set"], domain(message));
      const res = await handleApi(...apiReq("POST", "/api/routing", body), deps({ mc }));
      expect(res?.status).toBe(400);
      const envelope = (await res?.json()) as { error: { code: string; message: string } };
      expect(envelope.error.code).toBe("domain-rejection");
      expect(envelope.error.message).toBe(message);
      expect(mc.calls).toEqual([["routing", "set", "--role", body.role, "--binding", body.binding]]);
    }
  });

  test("POST /api/routing refuses a missing, empty, or non-string field before any spawn", async () => {
    for (const body of [{}, { role: "worker" }, { binding: "claude" }, { role: "", binding: "claude" }, { role: "worker", binding: "" }, { role: 7, binding: "claude" }, { role: "worker", binding: ["claude"] }]) {
      const mc = new FakeMc();
      const res = await handleApi(...apiReq("POST", "/api/routing", body), deps({ mc }));
      expect(res?.status).toBe(400);
      expect(((await res?.json()) as { error: { code: string } }).error.code).toBe("usage");
      expect(mc.calls).toEqual([]);
    }
  });

  test("POST /api/routing reports an environment failure as 500", async () => {
    const mc = new FakeMc().on(["routing", "set"], environment("spine unreachable"));
    const res = await handleApi(
      ...apiReq("POST", "/api/routing", { role: "worker", binding: "claude" }),
      deps({ mc }),
    );
    expect(res?.status).toBe(500);
  });

  test("GET /api/logs/:unit forwards stream and tail to the verb", async () => {
    const mc = new FakeMc().on(
      ["logs", "resident"],
      ok({ unit: "resident", stream: "stdout", lines: ["a"], exists: true }),
    );
    const res = await handleApi(...apiReq("GET", "/api/logs/resident?stream=stdout&tail=500"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(mc.calls).toEqual([["logs", "resident", "--stream", "stdout", "--tail", "500"]]);
  });

  test("GET /api/logs/:unit without params sends the bare verb", async () => {
    const mc = new FakeMc().on(["logs", "dashboard"], ok({ unit: "dashboard", lines: [] }));
    await handleApi(...apiReq("GET", "/api/logs/dashboard"), deps({ mc }));
    expect(mc.calls).toEqual([["logs", "dashboard"]]);
  });

  test("a non-numeric tail is refused before any spawn", async () => {
    const mc = new FakeMc();
    const res = await handleApi(...apiReq("GET", "/api/logs/resident?tail=abc"), deps({ mc }));
    expect(res?.status).toBe(400);
    expect(mc.calls).toEqual([]);
  });

  test("GET /api/logs/:unit forwards format to the verb", async () => {
    const mc = new FakeMc().on(
      ["logs", "resident"],
      ok({ unit: "resident", stream: "events", format: "json", lines: [], records: [], rendered: [], exists: true }),
    );
    await handleApi(...apiReq("GET", "/api/logs/resident?stream=events&format=json"), deps({ mc }));
    expect(mc.calls).toEqual([["logs", "resident", "--stream", "events", "--format", "json"]]);
  });

  test("GET /api/logs/runs mirrors the server-produced run list", async () => {
    // "runs" must never read as a unit named runs: the run lane's list route
    // matches first, so the operator picks from the verb's own list.
    const mc = new FakeMc().on(["logs", "runs", "--list"], ok({ runs: ["ab12", "cd34"] }));
    const res = await handleApi(...apiReq("GET", "/api/logs/runs"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual({ runs: ["ab12", "cd34"] });
    expect(mc.calls).toEqual([["logs", "runs", "--list"]]);
  });

  test("GET /api/logs/run/:id forwards the id opaquely with the window flags", async () => {
    const mc = new FakeMc().on(
      ["logs", "run", "ab12"],
      ok({ run_id: "ab12", format: "json", lines: [], records: [], rendered: [], exists: true }),
    );
    const res = await handleApi(...apiReq("GET", "/api/logs/run/ab12?tail=400&format=json"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(mc.calls).toEqual([["logs", "run", "ab12", "--tail", "400", "--format", "json"]]);
  });

  test("a run id the verb refuses passes through as the usage envelope", async () => {
    // Id validity is the VERB's authority: its safe-id charset and path fences
    // judge the token, and the route restates none of them.
    const mc = new FakeMc().on(["logs", "run", "../etc"], {
      code: 2,
      json: { error: { code: "usage", message: 'run id "../etc" is not a run id' } },
      stderr: "mc: run id is not a run id",
    });
    const res = await handleApi(...apiReq("GET", "/api/logs/run/..%2Fetc"), deps({ mc }));
    expect(res?.status).toBe(400);
  });

  test("an unknown unit comes back as the verb's own usage envelope at 400", async () => {
    // Unit validity is the VERB's authority — the route forwards and maps the
    // usage envelope to caller-caused.
    const mc = new FakeMc().on(["logs", "runner"], {
      code: 2,
      json: { error: { code: "usage", message: "unknown log unit" } },
      stderr: "mc: unknown log unit",
    });
    const res = await handleApi(...apiReq("GET", "/api/logs/runner"), deps({ mc }));
    expect(res?.status).toBe(400);
  });

  test("an environment failure on a read is 500", async () => {
    const mc = new FakeMc().on(["run", "list"], environment("spine helper unreachable"));
    const res = await handleApi(...apiReq("GET", "/api/runs"), deps({ mc }));
    expect(res?.status).toBe(500);
  });
});

describe("host-plane health (GET /api/health)", () => {
  test("forwards the bare verb, and the tail when the browser sets one", async () => {
    const mc = new FakeMc()
      .on(["health"], ok({ ok: true, tail: 200, findings: [], attention: [] }));
    const res = await handleApi(...apiReq("GET", "/api/health"), deps({ mc }));
    expect(res?.status).toBe(200);
    expect(mc.calls).toEqual([["health"]]);

    const withTail = new FakeMc().on(["health"], ok({ ok: true, tail: 50, findings: [], attention: [] }));
    await handleApi(...apiReq("GET", "/api/health?tail=50"), deps({ mc: withTail }));
    expect(withTail.calls).toEqual([["health", "--tail", "50"]]);
  });

  test("a non-numeric tail is refused before anything is spawned", async () => {
    const mc = new FakeMc();
    const res = await handleApi(...apiReq("GET", "/api/health?tail=all"), deps({ mc }));
    expect(res?.status).toBe(400);
    expect(mc.calls).toEqual([]);
  });

  test("the verb's own refusal passes through as the mc envelope", async () => {
    const mc = new FakeMc().on(["health"], domain("mc health is host scope only"));
    const res = await handleApi(...apiReq("GET", "/api/health"), deps({ mc }));
    expect(res?.status).toBe(400);
    expect(await res?.json()).toEqual({
      error: { code: "domain-rejection", message: "mc health is host scope only" },
    });
  });
});


test("the packet render requires the displayed packet and version", async () => {
 const mc=new FakeMc().on(["packet","list"],ok({packets:[{task_id:42,packet_id:8,version_id:12,render_path:"/mc/artifacts/report.html"}]}));
 const response=await handleApi(...apiReq("GET","/api/packets/42/render?packet=7&version=12"),deps({mc}));
 expect(response?.status).toBe(409);
 expect(mc.calls).toEqual([["packet","list"]]);
});

test("the packet render verifies the recorded report bytes", async () => {
 const report="<html><body>The report identifies its submitted version.</body></html>";
 const digest=new Bun.CryptoHasher("sha256").update(report).digest("hex");
 for (const content of [report,"<html><body>Another report.</body></html>"]){
  const mc=new FakeMc().on(["packet","list"],ok({packets:[{task_id:42,packet_id:8,version_id:12,render_path:"/mc/artifacts/report.html",report_sha256:digest}]}))
   .on(["__artifact-host-path"],ok({host_path:"/host/report.html"}));
  const response=await handleApi(...apiReq("GET","/api/packets/42/render?packet=8&version=12"),deps({mc,readRender:async()=>content}));
  expect(response?.status).toBe(content===report?200:409);
 }
});


test("a task report remains readable without a current review packet", async () => {
 const report="The task satisfies its requirements.";
 const mc=new FakeMc().on(["task","get","42"],ok({id:42,delivery:{report_path:"/mc/artifacts/task.txt",report_sha256:new Bun.CryptoHasher("sha256").update(report).digest("hex")}}))
 .on(["__artifact-host-path"],ok({host_path:"/host/task.txt"}));
 const response=await handleApi(...apiReq("GET","/api/tasks/42/report"),deps({mc,readRender:async()=>report}));
 expect(response?.status).toBe(200);
 expect(await response?.text()).toBe(report);
 expect(mc.calls.some((args)=>args[0]==="packet")).toBe(false);
});

test("Activity forwards its Project filter with the row limit", async () => {
  const mc = new FakeMc().on(["activity", "list"], ok({ activity: [] }));
  await handleApi(...apiReq("GET", "/api/activity?project=alpha&limit=30"), deps({ mc }));
  expect(mc.calls).toEqual([["activity", "list", "--limit", "30", "--project", "alpha"]]);
});

test("a pane cannot acknowledge records without a displayed record list", async () => {
  const mc = new FakeMc();
  const response = await handleApi(...apiReq("POST", "/api/outbox/health/ack", {}), deps({ mc }));
  expect(response?.status).toBe(400);
  expect(mc.calls).toEqual([]);
});


describe("Agent profile command interface", () => {
  test("A connection secret travels through standard input only", async () => {
    const calls: Array<{ argv: string[]; input?: string }> = [];
    const mc = { async run(argv: string[], input?: string) {
      calls.push({ argv, input });
      return ok({ connection_id: "private-connection" });
    } };
    const body = { name: "Account", template_id: "claude-api-key", api_key: "test-secret" };
    const response = await handleApi(...apiReq("POST", "/api/agent-profiles/connection-save", body), deps({ mc }));
    expect(response?.status).toBe(200);
    expect(calls).toEqual([{ argv: ["agent-profile", "connection-save"], input: JSON.stringify(body) }]);
    expect(calls[0]?.argv).toEqual(["agent-profile", "connection-save"]);
    expect(await response?.text()).not.toContain("test-secret");
  });
  test("A model list request travels through standard input", async () => {
    const calls: Array<{ argv: string[]; input?: string }> = [];
    const mc = { async run(argv: string[], input?: string) {
      calls.push({ argv, input });
      return ok({ connection_id: "team", models: [{ id: "model-a", name: "Model A" }] });
    } };
    const response = await handleApi(...apiReq("POST", "/api/agent-profiles/models", { connection_id: "team" }), deps({ mc }));
    expect(response?.status).toBe(200);
    expect(calls).toEqual([{ argv: ["agent-profile", "models"], input: JSON.stringify({ connection_id: "team" }) }]);
  });
  test("The dashboard cannot invoke a background worker", async () => {
    const mc = new FakeMc();
    const response = await handleApi(...apiReq("POST", "/api/agent-profiles/operation-run", {}), deps({ mc }));
    expect(response?.status).toBe(404);
    expect(mc.calls).toEqual([]);
  });
  test("A connection disconnect preserves its compare-and-swap request", async () => {
    const calls: Array<{ argv: string[]; input?: string }> = [];
    const mc = { async run(argv: string[], input?: string) {
      calls.push({ argv, input });
      return ok({ disconnected: true, provider_revoked: false });
    } };
    const body = { connection_id: "team", authorization_id: "revision", confirm: true };
    const response = await handleApi(...apiReq("POST", "/api/agent-profiles/connection-disconnect", body), deps({ mc }));
    expect(response?.status).toBe(200);
    expect(calls).toEqual([{ argv: ["agent-profile", "connection-disconnect"], input: JSON.stringify(body) }]);
  });
});

test("an expired task report preserves its history and returns an explicit expiry result", async () => {
  const mc = new FakeMc().on(["task", "get", "42"], ok({ delivery: {
    report_path: "/mc/artifacts/project/42/packet/report.html",
    artifact_retention: { status: "expired", expired_at: "2026-10-20 00:00:00" },
  } }));
  const res = await handleApi(...apiReq("GET", "/api/tasks/42/report"), deps({ mc }));
  expect(res?.status).toBe(410);
  expect((await res?.json()).error.code).toBe("artifact-expired");
  expect(mc.calls).toEqual([["task", "get", "42"]]);
});


test("Outbox reads start concurrently and preserve every surface", async () => {
  const pending: ((value: ReturnType<typeof ok>) => void)[] = [];
  const surfaces: string[] = [];
  const result = handleApi(...apiReq("GET", "/api/outbox/lag"), deps({ mc: {
    run: args => { surfaces.push(args[3]); return new Promise(resolve => pending.push(resolve)); },
  } }));
  expect(surfaces).toEqual(["discord", "dashboard", "cli"]);
  for (const resolve of pending) resolve(ok({ rows: [] }));
  const response = await result;
  expect((await response!.json()).surfaces.map(row => row.surface)).toEqual(surfaces);
});

describe("packet conversation", () => {
  test("a packet preparation maps to collie start with the packet and the version", async () => {
    const nudges: string[] = [];
    const mc = new FakeMc().on(["collie", "start"], ok({ session_id: "h-packet", status: "active" }));
    const prepared = await handleApi(...apiReq("POST", "/api/sessions", { packet_id: 41, version_id: 88 }),
      deps({ mc, nudgeResident: async (id) => { nudges.push(id); } }));
    expect(prepared?.status).toBe(200);
    expect(await prepared?.json()).toEqual({ session_id: "h-packet", status: "active" });
    const cleared = await handleApi(...apiReq("POST", "/api/sessions", { packet_id: 41, version_id: 88, clear: true }), deps({ mc }));
    expect(cleared?.status).toBe(200);
    expect(mc.calls).toEqual([
      ["collie", "start", "--from", "dashboard:dash-feedf00d", "--packet", "41", "--version", "88"],
      ["collie", "start", "--from", "dashboard:dash-feedf00d", "--packet", "41", "--version", "88", "--clear"],
    ]);
    expect(nudges).toEqual(["h-packet"]);
  });

  test("a packet preparation for a replaced packet is a conflict", async () => {
    const mc = new FakeMc().on(["collie", "start"], {
      code: 1,
      json: { error: { code: "record-conflict", message: "The review packet changed. Open the current packet." } },
      stderr: "",
    });
    const res = await handleApi(...apiReq("POST", "/api/sessions", { packet_id: 41, version_id: 87 }), deps({ mc }));
    expect(res?.status).toBe(409);
    expect(await res?.json()).toEqual({ error: { code: "record-conflict", message: "The review packet changed. Open the current packet." } });
  });

  test("a packet preparation refuses an incomplete identity", async () => {
    const mc = new FakeMc();
    const refusals: [Record<string, unknown>, string][] = [
      [{ packet_id: 41 }, "A packet conversation requires a packet and a version."],
      [{ version_id: 88 }, "A packet conversation requires a packet and a version."],
      [{ packet_id: "41", version_id: 88 }, "A packet conversation requires a packet and a version."],
      [{ packet_id: 0, version_id: 88 }, "A packet conversation requires a packet and a version."],
      [{ packet_id: 41, version_id: 1.5 }, "A packet conversation requires a packet and a version."],
      [{ packet_id: 41, version_id: 2 ** 53 }, "A packet conversation requires a packet and a version."],
      [{ packet_id: 41, version_id: 88, vision_project: "garden" }, "A packet conversation accepts only a packet, a version, and clear."],
      [{ packet_id: 41, version_id: 88, ensure: true }, "A packet conversation accepts only a packet, a version, and clear."],
    ];
    for (const [body, message] of refusals) {
      const res = await handleApi(...apiReq("POST", "/api/sessions", body), deps({ mc }));
      expect(res?.status).toBe(400);
      expect(await res?.json()).toEqual({ error: { code: "usage", message } });
    }
    expect(mc.calls).toEqual([]);
  });
});

describe("Project Vision", () => {
  test("a Vision conversation retains its explicit Project", async () => {
    const mc = new FakeMc().on(["collie", "start"], ok({ session_id: "h-vision" }));
    const response = await handleApi(...apiReq("POST", "/api/sessions", { vision_project: "garden" }), deps({ mc }));
    expect(response?.status).toBe(200);
    expect(mc.calls).toEqual([["collie", "start", "--from", "dashboard:vision-garden", "--vision-project", "garden"]]);
  });

  test("a clear starts an empty Vision conversation and refuses an ordinary one", async () => {
    const mc = new FakeMc().on(["collie", "start"], ok({ session_id: "h-fresh" }));
    const response = await handleApi(...apiReq("POST", "/api/sessions", { vision_project: "garden", clear: true }), deps({ mc }));
    expect(response?.status).toBe(200);
    expect(mc.calls).toEqual([["collie", "start", "--from", "dashboard:vision-garden", "--vision-project", "garden", "--clear"]]);
    const refused = await handleApi(...apiReq("POST", "/api/sessions", { clear: true }), deps({ mc }));
    expect(refused?.status).toBe(400);
    expect(await refused?.json()).toEqual({ error: { code: "usage", message: "Only a Vision or packet conversation can be cleared." } });
    expect(mc.calls.length).toBe(1);
  });

  test("draft operations forward their exact revision and contents", async () => {
    for (const action of ["vision-open", "vision-draft", "vision-edit", "vision-submit", "vision-rebase", "vision-workspace"]) {
      const mc = new FakeMc().on(["project", action], ok({ draft: { id: "garden", revision: "r2" } }));
      const body = { id: "garden", revision: "r1", content: "Grow vegetables.", workspace_revision: "absent", workspace_identity: "root" };
      const response = await handleApi(...apiReq("POST", `/api/projects/${action}`, body), deps({ mc }));
      expect(response?.status).toBe(200);
      expect(mc.calls).toEqual([["project", action]]);
      expect(mc.inputs).toEqual([JSON.stringify(body)]);
    }
  });

  test("uploads use the addressed session and the host command", async () => {
    const mc = new FakeMc().on(["__dashboard-upload"], ok({ session_id: "h-vision" }));
    const body = { body: "Use this note.", request_id: "upload-1", files: [{ name: "note.txt", media_type: "text/plain", data: "Z29hbA==" }], session_id: "other" };
    const response = await handleApi(...apiReq("POST", "/api/sessions/h-vision/upload", body), deps({ mc }));
    expect(response?.status).toBe(200);
    expect(mc.calls).toEqual([["__dashboard-upload"]]);
    expect(mc.inputs).toEqual([JSON.stringify({ ...body, session_id: "h-vision" })]);
  });
});

test("automatic conversation creation uses the atomic session command", async () => {
  const mc = new FakeMc().on(["collie", "start"], ok({ session_id: "h-new", status: "active" }));
  const res = await handleApi(...apiReq("POST", "/api/sessions", { ensure: true }), deps({ mc }));
  expect(res?.status).toBe(200);
  expect(mc.calls).toEqual([["collie", "start", "--from", "dashboard:dash-feedf00d", "--ensure"]]);
});

test("conversation deletion passes the session identity and its failure through", async () => {
  const mc = new FakeMc().on(["collie", "delete"], domain("The conversation is unavailable."));
  const res = await handleApi(...apiReq("DELETE", "/api/sessions/h-one"), deps({ mc }));
  expect(res?.status).toBe(400);
  expect(mc.calls).toEqual([["collie", "delete", "h-one"]]);
});


test("conversation rename forwards its title and reports a domain rejection", async () => {
  const mc = new FakeMc().on(["collie", "title"], ok({ session_id: "h-1", title: "Database options", changed: true }));
  const result = await handleApi(...apiReq("POST", "/api/sessions/h-1/title", { title: "Database options" }), deps({ mc }));
  expect(result?.status).toBe(200);
  expect(mc.calls).toEqual([["collie", "title", "h-1", "--title", "Database options"]]);
  const invalid = await handleApi(...apiReq("POST", "/api/sessions/h-1/title", { title: 3 }), deps({ mc }));
  expect(invalid?.status).toBe(400);
  const rejected = new FakeMc().on(["collie", "title"], domain("The conversation is unavailable."));
  expect((await handleApi(...apiReq("POST", "/api/sessions/h-1/title", { title: "Gone" }), deps({ mc: rejected })))?.status).toBe(400);
});

test("model selection forwards the conversation, profile, and retry identity", async () => {
  const mc = new FakeMc().on(["collie", "start"], ok({ session_id: "h-new", replaced: "h-old", binding: "fake/fake" }));
  const response = await handleApi(...apiReq("POST", "/api/sessions/h-old/model", { binding: "fake", request_id: "request-model-0001" }), deps({ mc }));
  expect(response?.status).toBe(200);
  expect(await response?.json()).toMatchObject({ session_id: "h-new", replaced: "h-old" });
  expect(mc.calls).toEqual([["collie", "start", "--from", "dashboard:dash-feedf00d", "--previous", "h-old", "--binding", "fake", "--request", "request-model-0001"]]);
  const refused = await handleApi(...apiReq("POST", "/api/sessions/h-old/model", { binding: "fake" }), deps({ mc }));
  expect(refused?.status).toBe(400);
  expect(mc.calls.length).toBe(1);
});

// Files changed reads the recorded change data of one comparison. The spine
// row names the files and their digests; the URL names only the comparison.
describe("packet change data", () => {
  const encoder = new TextEncoder();
  const patch = encoder.encode("diff --git a/a.txt b/a.txt\n@@ -1 +1 @@\n-a\n+b\ndiff --git a/c.bin b/c.bin\nBinary files a/c.bin and b/c.bin differ\n");
  const split = new TextDecoder().decode(patch).indexOf("diff --git a/c.bin");
  const inventory = encoder.encode(JSON.stringify({
    from_commit: "a".repeat(40), to_commit: "b".repeat(40), patch_bytes: patch.length, patch_sha256: "unused",
    files: [
      { status: "renamed", path: "a.txt", previous_path: "old/a.txt", text_diff: true, patch_offset: 0, patch_bytes: split },
      { status: "modified", path: "c.bin", binary: true, patch_offset: split, patch_bytes: patch.length - split },
    ],
  }));
  const sha = (bytes: Uint8Array) => new Bun.CryptoHasher("sha256").update(bytes).digest("hex");
  const record = (over: Record<string, unknown> = {}) => ({
    from_commit: "a".repeat(40), to_commit: "b".repeat(40),
    inventory_path: "/mc/artifacts/p/7/changes/m/candidate.json", inventory_sha256: sha(inventory),
    patch_path: "/mc/artifacts/p/7/changes/m/candidate.patch", patch_sha256: sha(patch), ...over,
  });
  const packets = (row: Record<string, unknown> = {}) => ok({
    packets: [{ task_id: 7, project: "p", packet_id: 3, version_id: 9, candidate_changes: record(), ...row }],
  });
  const files = (): Partial<ApiDeps> => ({
    readArtifact: async (path) => path.endsWith(".json") ? inventory : patch,
  });
  const resolver = () => new FakeMc()
    .on(["__artifact-host-path", "/mc/artifacts/p/7/changes/m/candidate.json"], ok({ host_path: "/ws/.mc-artifacts/7/changes/m/candidate.json" }))
    .on(["__artifact-host-path", "/mc/artifacts/p/7/changes/m/candidate.patch"], ok({ host_path: "/ws/.mc-artifacts/7/changes/m/candidate.patch" }));

  test("GET /api/packets/:task/changes returns the verified file inventory", async () => {
    const mc = resolver().on(["packet", "list"], packets());
    const res = await handleApi(...apiReq("GET", "/api/packets/7/changes?packet=3&version=9"), deps({ mc, ...files() }));
    expect(res?.status).toBe(200);
    const body = await res?.json();
    expect(body.from_commit).toBe("a".repeat(40));
    expect(body.files.map((f: { path: string }) => f.path)).toEqual(["a.txt", "c.bin"]);
    expect(mc.calls).toEqual([["packet", "list"], ["__artifact-host-path", "/mc/artifacts/p/7/changes/m/candidate.json"]]);
  });

  test("GET /api/packets/:task/changes/patch returns the exact patch part of one file", async () => {
    const mc = resolver().on(["packet", "list"], packets());
    const res = await handleApi(...apiReq("GET", "/api/packets/7/changes/patch?packet=3&version=9&file=1"), deps({ mc, ...files() }));
    expect(res?.status).toBe(200);
    expect(res?.headers.get("content-security-policy")).toBe("sandbox");
    expect(await res?.text()).toBe("diff --git a/c.bin b/c.bin\nBinary files a/c.bin and b/c.bin differ\n");
  });

  test("a changed packet, an absent record, an expired packet, and an unknown file are refused", async () => {
    const cases: [Record<string, unknown>, string, number][] = [
      [{}, "/api/packets/7/changes?packet=3&version=8", 409],
      [{ candidate_changes: null }, "/api/packets/7/changes?packet=3&version=9", 404],
      [{ artifact_retention: { status: "expired", expired_at: "2026-09-01" } }, "/api/packets/7/changes?packet=3&version=9", 410],
      [{}, "/api/packets/7/changes/patch?packet=3&version=9&file=2", 400],
    ];
    for (const [row, path, status] of cases) {
      const mc = resolver().on(["packet", "list"], packets(row));
      const res = await handleApi(...apiReq("GET", path), deps({ mc, ...files() }));
      expect(res?.status).toBe(status);
    }
  });

  test("GET /api/packets/:task/changes/files returns the version of the file at each commit", async () => {
    const mc = resolver().on(["packet", "list"], packets())
      .on(["project", "list"], ok({ projects: [{ id: "q", workspace_root: "/other" }, { id: "p", workspace_root: "/ws" }] }))
      .on(["__repository-file", "/ws", "a".repeat(40), "old/a.txt"], ok({ commit: "a".repeat(40), path: "old/a.txt", size: 2, content: "a\n" }))
      .on(["__repository-file", "/ws", "b".repeat(40), "a.txt"], ok({ commit: "b".repeat(40), path: "a.txt", size: 2, content: "b\n" }));
    const res = await handleApi(...apiReq("GET", "/api/packets/7/changes/files?packet=3&version=9&file=0"), deps({ mc, ...files() }));
    expect(res?.status).toBe(200);
    expect(await res?.json()).toEqual({ old_file: { path: "old/a.txt", content: "a\n" }, new_file: { path: "a.txt", content: "b\n" } });
  });

  test("the file versions are refused for a binary file, and a failed repository read keeps its error", async () => {
    const binary = resolver().on(["packet", "list"], packets());
    const refused = await handleApi(...apiReq("GET", "/api/packets/7/changes/files?packet=3&version=9&file=1"), deps({ mc: binary, ...files() }));
    expect(refused?.status).toBe(409);
    expect((await refused?.json()).error.message).toBe("The file has no unmodified lines to show.");
    expect(binary.calls.some((call) => call[0] === "__repository-file")).toBe(false);

    const failed = resolver().on(["packet", "list"], packets())
      .on(["project", "list"], ok({ projects: [{ id: "p", workspace_root: "/ws" }] }))
      .on(["__repository-file", "/ws", "a".repeat(40), "old/a.txt"], ok({ path: "old/a.txt", content: "a\n" }))
      .on(["__repository-file", "/ws", "b".repeat(40), "a.txt"], { code: 1, json: { error: { code: "domain-rejection", message: "the repository file is not UTF-8 text: a.txt" } }, stderr: "" });
    const res = await handleApi(...apiReq("GET", "/api/packets/7/changes/files?packet=3&version=9&file=0"), deps({ mc: failed, ...files() }));
    expect(res?.status).toBe(400);
    expect((await res?.json()).error.message).toBe("the repository file is not UTF-8 text: a.txt");
  });

  test("bytes that differ from the recorded digest are refused", async () => {
    const mc = resolver().on(["packet", "list"], packets({ candidate_changes: record({ patch_sha256: "0".repeat(64) }) }));
    const res = await handleApi(...apiReq("GET", "/api/packets/7/changes/patch?packet=3&version=9&file=0"), deps({ mc, ...files() }));
    expect(res?.status).toBe(409);
    expect((await res?.json()).error.message).toBe("The change data differs from its record.");
  });
});

test("only the connection form accepts an API key", async () => {
  for (const action of ["models", "save", "connection-disconnect", "operation-start", "operation-status", "operation-cancel"]) {
    const mc = new FakeMc();
    const response = await handleApi(...apiReq("POST", `/api/agent-profiles/${action}`, { api_key:"synthetic-private-key" }), deps({mc}));
    expect(response?.status).toBe(400);
    expect(mc.calls).toEqual([]);
    expect(await response?.text()).not.toContain("synthetic-private-key");
  }
});
