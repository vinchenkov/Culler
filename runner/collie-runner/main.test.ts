// main.test.ts — unit tests for the collie runner's pure helpers and the
// lease-free claim→reply loop, driven through injected mc/harness seams.
// Docker-free, token-free: part of the fast suite.

import { describe, expect, test } from "bun:test";
import {
  DRAFT_INTERVAL_MS,
  collieAdapterInvocation,
  collieTurnPrompt,
  inboundAttachmentPath,
  makeDraftWriter,
  makeLineSplitter,
  nativeIdFromEvent,
  nativeLocatorFromEvent,
  parseClaimedMessage,
  replyFromEvent,
  resolveClaimedTurn,
  runCollieLoop,
  idleExitMs,
  parseRefusal,
  CLAIM_WARN_INTERVAL_MS,
  IDLE_EXIT_MARGIN_S,
  SESSION_INACTIVE_CODE,
  RUNTIME_CHANGED_CODE,
  textFromEvent,
  TURN_ENDED_LINE,
  type CollieEnvelope,
  type McOutcome,
  type RunnerDeps,
} from "./main";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, realpath, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { makeLogger, type LogRecord, type Logger } from "../shared/log";

/** Collects whole records: level, event, message, and context. */
function recordLogger(records: LogRecord[]): Logger {
  return makeLogger({
    comp: "collie-runner",
    level: "debug",
    sink: { write: (line) => { records.push(JSON.parse(line) as LogRecord); } },
  });
}

/** Collects each record's prose, so the assertions written against the old
 * free-text seam keep meaning what they meant. */
function msgLogger(msgs: string[]): Logger {
  return makeLogger({
    comp: "collie-runner",
    level: "debug",
    sink: { write: (line) => { msgs.push((JSON.parse(line) as LogRecord).msg); } },
  });
}


const envelope = (over: Partial<CollieEnvelope> = {}): CollieEnvelope => ({
  run_id: "h-abc",
  session_id: "h-abc",
  tier: "collie",
  directive: "# Collie\n\nYou are the operator's assistant.",
  launch: "aaaaaaaaaaaaaaaa",
  container_id: "c".repeat(64),
  binding: "fake/fake",
  harness: "fake",
  model_binding: "fake",
  mode: "fresh",
  session_path: "sessions/h-abc",
  harness_config: { behavior: "/mc/behaviors/collie.json" },
  mounts: {
    session: "/mc/session",
    // The production shape: the resident always names a WRITABLE per-launch
    // cwd.
    workspace: "/mc/scratch",
    attachments: {
      inbound: "/mc/attachments/in",
      outbound: "/mc/private/attachments/out",
    },
  },
  ...over,
});

const claimed = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
  id: "1",
  seq: "1",
  surface: "dashboard",
  channel_ref: null,
  body: "question",
  attachments: [],
  created_at: "2026-07-30 12:00:00",
  claimed_at: "2026-07-30 12:00:01",
  reclaimed: false,
  ...over,
});

/** One scripted refusal: what `mc` reports on a non-zero exit. */
type Refusal = { refused: { exit: number; code: string; message: string } };
type Answer = Record<string, unknown> | null | Refusal;

function refused(exit: number, code: string, message: string): Refusal {
  return { refused: { exit, code, message } };
}

/** A scripted mc: answers from a per-argv[1..2] queue, records every call. */
class FakeMc {
  calls: string[][] = [];
  private answers: Answer[] = [];
  enqueue(...a: Answer[]): void {
    this.answers.push(...a);
  }
  private next(argv: string[]): Answer {
    this.calls.push(argv);
    return this.answers.length > 0 ? this.answers.shift()! : {};
  }
  mcJSON = async (argv: string[]): Promise<Record<string, unknown> | null> => {
    const answer = this.next(argv);
    return answer !== null && "refused" in answer ? null : answer;
  };
  mcCall = async (argv: string[]): Promise<McOutcome> => {
    const answer = this.next(argv);
    if (answer === null) return { ok: false, exit: 1, code: "", message: "scripted failure" };
    if ("refused" in answer) return { ok: false, ...(answer as Refusal).refused };
    return { ok: true, value: answer };
  };
  /** Both seams, for the loop's deps. */
  get seams(): Pick<RunnerDeps, "mcJSON" | "mcCall"> {
    return { mcJSON: this.mcJSON, mcCall: this.mcCall };
  }
}

/** A clock that advances by one poll interval on every sleep, so a scripted
 * run of empty claims walks the runner up to its own exit deadline. */
function fakeClock(stepMs = 1000): { now: () => number; sleep: () => Promise<void>; sleeps: number } {
  const clock = {
    at: 1_000_000,
    sleeps: 0,
    now: () => clock.at,
    sleep: async () => { clock.at += stepMs; clock.sleeps += 1; },
  };
  return clock;
}

function deps(over: Partial<RunnerDeps> = {}): RunnerDeps {
  const logs: string[] = [];
  const clock = fakeClock();
  return {
    mcJSON: async () => ({}),
    mcCall: async () => ({ ok: true, value: {} }),
    runHarnessTurn: async () => ({ reply: "ok" }),
    resolveTurn: async (message) => message.body,
    turnEnded: () => {},
    sleep: clock.sleep,
    now: clock.now,
    log: msgLogger(logs),
    pollMs: 0,
    ...over,
  };
}

/** Empty claims: enough to carry the clock past the runner's exit deadline. */
function emptyPollsPastDeadline(run: CollieEnvelope): Record<string, unknown>[] {
  return Array.from({ length: idleExitMs(run) / 1000 + 1 }, () => ({ message: null }));
}

describe("replyFromEvent", () => {
  test("extracts a successful turn-complete output", () => {
    expect(replyFromEvent(JSON.stringify({ event: "turn-complete", status: "success", output: "hi there" }))).toBe("hi there");
  });
  test("ignores non-completion and failed events", () => {
    expect(replyFromEvent(JSON.stringify({ event: "session-start" }))).toBeUndefined();
    expect(replyFromEvent(JSON.stringify({ event: "turn-complete", status: "error", output: "x" }))).toBeUndefined();
  });
  test("ignores non-JSON lines", () => {
    expect(replyFromEvent("not json")).toBeUndefined();
  });
});

describe("textFromEvent", () => {
  test("extracts a turn-text piece and ignores everything else", () => {
    expect(textFromEvent(JSON.stringify({ event: "turn-text", text: "Four " }))).toBe("Four ");
    expect(textFromEvent(JSON.stringify({ event: "turn-text", text: "" }))).toBeUndefined();
    expect(textFromEvent(JSON.stringify({ event: "turn-complete", status: "success", output: "x" }))).toBeUndefined();
    expect(textFromEvent("not json")).toBeUndefined();
  });
});

/** A clock the test advances by hand; `sleep` moves it forward. */
function manualClock(): { now: () => number; sleep: (ms: number) => Promise<void>; advance: (ms: number) => void } {
  let t = 0;
  return {
    now: () => t,
    sleep: async (ms) => { t += ms; },
    advance: (ms) => { t += ms; },
  };
}

describe("makeDraftWriter", () => {
  test("writes at once, then at most once per second with the text so far", async () => {
    const mc = new FakeMc();
    const clock = manualClock();
    const logs: string[] = [];
    const writer = makeDraftWriter({ mcJSON: mc.mcJSON, ...clock, log: msgLogger(logs) }, "h-abc", "7");
    writer.push("Four");
    await Promise.resolve();
    expect(mc.calls).toEqual([["collie", "draft", "h-abc", "--to", "7", "--body", "Four"]]);
    // Within the second: pieces accumulate into one trailing write.
    writer.push(" seeded");
    writer.push(", one");
    clock.advance(200);
    writer.push(" packaged");
    await Bun.sleep(0); // the trailing write drains on the fake clock
    expect(mc.calls.length).toBe(2);
    expect(mc.calls[1]![6]).toBe("Four seeded, one packaged");
    expect(clock.now()).toBe(DRAFT_INTERVAL_MS);
    // Nothing is queued after the harness ends: the reply supersedes.
    writer.push(" late");
    await writer.stop();
    expect(mc.calls.length).toBe(2);
    expect(logs).toEqual([]);
  });

  test("a refused write is logged once and stops drafts for the turn", async () => {
    const mc = new FakeMc();
    mc.enqueue(null); // the verb is absent from an old session's allowlist
    const clock = manualClock();
    const logs: string[] = [];
    const writer = makeDraftWriter({ mcJSON: mc.mcJSON, ...clock, log: msgLogger(logs) }, "h-abc", "7");
    writer.push("Four");
    await Promise.resolve();
    clock.advance(5_000);
    writer.push(" seeded");
    clock.advance(5_000);
    writer.push(", one packaged");
    await writer.stop();
    expect(mc.calls.length).toBe(1);
    expect(logs).toEqual(["turn 7: draft write refused; drafts stop for this turn"]);
  });
});

describe("nativeIdFromEvent", () => {
  test("extracts the session-start native id", () => {
    expect(nativeIdFromEvent(JSON.stringify({ event: "session-start", session_id: "abc" }))).toBe("abc");
  });
  test("ignores other events and non-JSON", () => {
    expect(nativeIdFromEvent(JSON.stringify({ event: "turn-complete" }))).toBeUndefined();
    expect(nativeIdFromEvent("x")).toBeUndefined();
  });
});

describe("nativeLocatorFromEvent", () => {
  test("extracts the adapter's exact durable relative trace locator", () => {
    expect(nativeLocatorFromEvent(JSON.stringify({
      event: "session-start",
      session_id: "native-xyz",
      native_file: "2026/07/25/rollout-native-xyz.jsonl",
    }))).toEqual({
      sessionId: "native-xyz",
      nativeFile: "2026/07/25/rollout-native-xyz.jsonl",
    });
  });

  test("refuses an absent or escaping native file", () => {
    expect(() => nativeLocatorFromEvent(JSON.stringify({
      event: "session-start", session_id: "native-xyz",
    }))).toThrow();
    expect(() => nativeLocatorFromEvent(JSON.stringify({
      event: "session-start", session_id: "native-xyz", native_file: "../other.jsonl",
    }))).toThrow();
  });
});

describe("typed attachment claims", () => {
  test("strictly parses the closed immutable identity set", () => {
    const digest = "a".repeat(64);
    expect(parseClaimedMessage(claimed({
      attachments: [{
        ordinal: 1,
        session_id: "h-abc",
        direction: "in",
        sha256: digest,
        size: 7,
        media_type: "image/png",
        display_name: "map.png",
      }],
    }), "h-abc")?.attachments).toEqual([{
      ordinal: 1,
      session_id: "h-abc",
      direction: "in",
      sha256: digest,
      size: 7,
      media_type: "image/png",
      display_name: "map.png",
    }]);
    for (const malformed of [
      claimed({ id: 1 }),
      claimed({ extra: "authority" }),
      claimed({ attachments: [{ ordinal: 1, path: "/etc/passwd" }] }),
      claimed({
        attachments: [{
          ordinal: 1, session_id: "h-other", direction: "in",
          sha256: digest, size: 7, media_type: "image/png", display_name: "map.png",
        }],
      }),
    ]) {
      expect(() => parseClaimedMessage(malformed, "h-abc")).toThrow();
    }
  });

  test("derives, verifies, and renders only the fixed read-only digest path", async () => {
    const body = new TextEncoder().encode("mapdata");
    const digest = createHash("sha256").update(body).digest("hex");
    const root = await mkdtemp(join(await realpath(tmpdir()), "mc-collie-attachment-"));
    const shard = join(root, "sha256", digest.slice(0, 2));
    await mkdir(shard, { recursive: true });
    const path = inboundAttachmentPath(root, digest);
    await writeFile(path, body);
    const message = parseClaimedMessage(claimed({
      body: "inspect this",
      attachments: [{
        ordinal: 1,
        session_id: "h-abc",
        direction: "in",
        sha256: digest,
        size: body.length,
        media_type: "image/png",
        display_name: "map.png",
      }],
    }), "h-abc")!;
    const turn = await resolveClaimedTurn(message, root);
    expect(turn).toContain(JSON.stringify({
      version: 1,
      body: "inspect this",
      attachments: [{
        ordinal: 1,
        session_id: "h-abc",
        direction: "in",
        sha256: digest,
        size: body.length,
        media_type: "image/png",
        display_name: "map.png",
        path,
      }],
    }));

    await writeFile(path, "mapdAta");
    await expect(resolveClaimedTurn(message, root)).rejects.toThrow("drifted");
  });

  test("refuses a symlink at the digest-derived leaf", async () => {
    const body = new TextEncoder().encode("linked");
    const digest = createHash("sha256").update(body).digest("hex");
    const root = await mkdtemp(join(await realpath(tmpdir()), "mc-collie-attachment-"));
    const shard = join(root, "sha256", digest.slice(0, 2));
    await mkdir(shard, { recursive: true });
    const target = join(root, "target");
    await writeFile(target, body);
    await symlink(target, inboundAttachmentPath(root, digest));
    const message = parseClaimedMessage(claimed({
      attachments: [{
        ordinal: 1,
        session_id: "h-abc",
        direction: "in",
        sha256: digest,
        size: body.length,
        media_type: "application/octet-stream",
        display_name: "linked.bin",
      }],
    }), "h-abc")!;
    await expect(resolveClaimedTurn(message, root)).rejects.toThrow("regular file");
  });
});

describe("collieAdapterInvocation", () => {
  test("carries a frozen Codex native handle through the shared adapter selection", () => {
    const invocation = collieAdapterInvocation(envelope({
      binding: "codex/openai-sub",
      harness: "codex",
      model_binding: "openai-sub",
      mode: "native",
      native_session_ref: "thread-123",
      trace_filename: "2026/07/25/rollout-thread-123.jsonl",
      runtime: {
        base_url: "", channel: "codex", harness: "codex", id: "openai-sub",
        model: "operator-chosen", effort: "high",
      },
      mounts: {
        session: "/mc/session",
        workspace: "/workspace/collie",
        attachments: {
          inbound: "/mc/attachments/in",
          outbound: "/mc/private/attachments/out",
        },
      },
    }));
    expect(invocation.argv).toEqual([
      "bun",
      "/app/src/agent-runner/adapters/codex.ts",
      "--effort", "high",
      "--session-dir", "/mc/session",
      "--workspace", "/workspace/collie",
      "--mode", "native",
      "--model", "operator-chosen",
      "--native-ref", "thread-123",
    ]);
  });

  test("a real Collie route without its resolved capability refuses", () => {
    expect(() => collieAdapterInvocation(envelope({
      binding: "codex/openai-sub",
      harness: "codex",
      model_binding: "openai-sub",
    }))).toThrow("no resolved runtime capability");
  });

  test("refuses disagreement between the historical and split binding", () => {
    expect(() => collieAdapterInvocation(envelope({
      binding: "claude-sdk/anthropic-sub",
      harness: "fake",
      model_binding: "fake",
    }))).toThrow("historical binding disagrees");
  });

  test("passes the scratch cwd to a claude-sdk Collie", () => {
    const invocation = collieAdapterInvocation(envelope({
      binding: "claude-sdk/hosted-model",
      harness: "claude-sdk",
      model_binding: "hosted-model",
      runtime: {
        base_url: "", channel: "claude",
        harness: "claude-sdk", id: "hosted-model", model: "hosted-model-1",
      },
      mounts: {
        session: "/mc/session",
        workspace: "/mc/scratch",
        attachments: {
          inbound: "/mc/attachments/in",
          outbound: "/mc/private/attachments/out",
        },
      },
    }));
    // cwd is the writable scratch, never the read-only operator source.
    expect(invocation.argv[invocation.argv.indexOf("--workspace") + 1]).toBe("/mc/scratch");
  });

  test("refuses an envelope that names no cwd rather than inheriting the pipeline's", () => {
    expect(() => collieAdapterInvocation(envelope({
      binding: "claude-sdk/hosted-model",
      harness: "claude-sdk",
      model_binding: "hosted-model",
      runtime: {
        base_url: "", channel: "claude",
        harness: "claude-sdk", id: "hosted-model", model: "hosted-model-1",
      },
      mounts: {
        session: "/mc/session",
        attachments: {
          inbound: "/mc/attachments/in",
          outbound: "/mc/private/attachments/out",
        },
      } as CollieEnvelope["mounts"],
    }))).toThrow("must name its own writable workspace cwd");
  });

  test("refuses an absent or changed attachment mount contract", () => {
    for (const attachments of [
      undefined,
      { inbound: "/mc/attachments/in", outbound: "/mc/private" },
      { inbound: "/mc/session", outbound: "/mc/private/attachments/out" },
    ]) {
      expect(() => collieAdapterInvocation(envelope({
        mounts: {
          session: "/mc/session",
          attachments,
        } as CollieEnvelope["mounts"],
      }))).toThrow("attachment mount contract");
    }
  });
});

describe("SESSION_INACTIVE_CODE", () => {
  test("is the wire literal mc/domain CodeSessionInactive emits", () => {
    // mc/domain/codes_test.go pins the same literal on the Go side.
    expect(SESSION_INACTIVE_CODE).toBe("session-inactive");
  });
});

describe("parseRefusal", () => {
  test("reads the code and message out of the error envelope", () => {
    const stdout = JSON.stringify({ error: { code: "session-inactive", message: "session h1 is ended" } });
    expect(parseRefusal(1, stdout, "mc: session h1 is ended")).toEqual({
      exit: 1, code: "session-inactive", message: "session h1 is ended",
    });
  });
  test("falls back to stderr when stdout carries no envelope", () => {
    expect(parseRefusal(2, "", "mc: self-delegation failed: no helper\n")).toEqual({
      exit: 2, code: "", message: "mc: self-delegation failed: no helper",
    });
    expect(parseRefusal(1, "{\"error\":\"plain\"}", "x")).toEqual({ exit: 1, code: "", message: "x" });
  });
});

describe("makeLineSplitter", () => {
  test("reassembles across chunk boundaries and holds a partial tail", () => {
    const lines: string[] = [];
    const split = makeLineSplitter((l) => lines.push(l));
    split("a");
    split("bc\nde");
    expect(lines).toEqual(["abc"]);
    split("f\n");
    expect(lines).toEqual(["abc", "def"]);
  });
});

describe("runCollieLoop", () => {
  test("boot: reports runner-started with launch + container id", async () => {
    const mc = new FakeMc();
    mc.enqueue({}, ...emptyPollsPastDeadline(envelope())); // started, then idle to the deadline
    await runCollieLoop(envelope(), deps({ ...mc.seams }));
    expect(mc.calls[0]).toEqual([
      "collie", "runner-started", "h-abc", "--launch", "aaaaaaaaaaaaaaaa", "--container-id", "c".repeat(64),
    ]);
  });

  test("a fenced runner-started exits 0 without claiming", async () => {
    const mc = new FakeMc();
    mc.enqueue({ fenced: false });
    const code = await runCollieLoop(envelope(), deps({ ...mc.seams }));
    expect(code).toBe(0);
    expect(mc.calls.map((c) => c[1])).toEqual(["runner-started"]); // never claimed
  });

  test("a missing or invalid directive prevents claims and harness calls", async () => {
    for (const directive of [undefined, null, 7, "", " \n"]) {
      const mc = new FakeMc();
      let harnessCalls = 0;
      const code = await runCollieLoop(
        { ...envelope(), directive } as CollieEnvelope,
        deps({
          ...mc.seams,
          runHarnessTurn: async () => { harnessCalls++; return { reply: "unused" }; },
        }),
      );
      expect(code).toBe(2);
      expect(mc.calls).toEqual([]);
      expect(harnessCalls).toBe(0);
    }
  });

  test("claims a turn, runs the harness, posts the reply, then polls until the deadline", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {}, // runner-started
      { message: claimed({ id: "7", channel_ref: "c1", body: "ping" }) },
      {}, // reply ack
      ...emptyPollsPastDeadline(envelope()),
    );
    const turns: string[] = [];
    const code = await runCollieLoop(
      envelope(),
      deps({ ...mc.seams, runHarnessTurn: async (_r, t) => { turns.push(t); return { reply: "pong" }; } }),
    );
    expect(code).toBe(0);
    expect(turns).toEqual([`${envelope().directive}\n\n# Conversation\n\nThis session receives operator messages from the dashboard or another connected surface. A Board message carries Project context and requires confirmation for a new state change. A packet conversation carries packet context about one packet. Project initialization runs before pipeline work. For environment questions, inspect the Project through mc environment get. Image dependencies require operator approval in the Setup Journal or Environment page.\n\n# Discord access\n\nDiscord setup and account approval belong to Configs → Discord. Culler consumes account confirmation codes before Collie receives operator messages. A channel message starts a Collie session in its own Discord thread. The operator can continue that session from its thread or the dashboard. A reply reaches the same thread when the operator uses the dashboard.\n\n# Trace access\n\nThe read-only trace copies at /mc/traces/<id>/<trace_filename> contain complete records with credential masks. The status.json file states availability; a missing or pending copy requires a later read.\n\n# Diagnostic context\n\nA dashboard diagnostic message contains a short issue summary and references for permitted reads. A proposed state change requires explicit operator confirmation.\n\nConversation identifier: ${envelope().session_id}\nOperator message sequence: 1\n\n# Operator message\n\nping`]);
    const reply = mc.calls.find((c) => c[1] === "reply");
    expect(reply).toEqual(["collie", "reply", "h-abc", "--to", "7", "--body", "pong"]);
  });

  test("a Vision turn receives the current draft and preserves its Project association", async () => {
    const context = {
      project_id: "beta", title: "Beta", workspace_has_files: true,
      workspace_vision_status: "absent", draft: { id: "beta", content: "The current goal", revision: "4" },
    };
    const mc = new FakeMc();
    mc.enqueue({}, { message: claimed({ vision_context: context }) }, {},
      refused(1, SESSION_INACTIVE_CODE, "session is not active"));
    const turns: string[] = [];
    const code = await runCollieLoop(envelope({ vision_project_id: "beta" }), deps({
      ...mc.seams, runHarnessTurn: async (_run, turn) => { turns.push(turn); return { reply: "Done" }; },
    }));
    expect(code).toBe(0);
    expect(turns[0]).toContain('"revision": "4"');
    expect(turns[0]).toContain("The current goal");
    expect(turns[0]).toContain("/workspaces/beta");
    expect(turns[0]).toContain("Save Vision");
    const mismatch = new FakeMc();
    mismatch.enqueue({}, { message: claimed({ vision_context: context }) });
    let called = false;
    const refusedCode = await runCollieLoop(envelope({ vision_project_id: "alpha" }), deps({
      ...mismatch.seams, runHarnessTurn: async () => { called = true; return { reply: "unexpected" }; },
    }));
    expect(refusedCode).toBe(1);
    expect(called).toBe(false);
  });

  test("writes throttled drafts from the stream and still posts the final reply", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {}, // runner-started
      { message: claimed({ id: "7", body: "ping" }) },
      {}, // draft 1
      {}, // draft 2
      {}, // register-session ack
      {}, // reply ack
      refused(1, SESSION_INACTIVE_CODE, "session is not active"), // the session ended: the runner exits
    );
    const clock = manualClock();
    // The adapter stream, as the production turn feeds it line by line.
    const stream = [
      JSON.stringify({ event: "session-start", session_id: "n1", native_file: "native.jsonl" }),
      JSON.stringify({ event: "turn-text", text: "Four" }),
      JSON.stringify({ event: "turn-text", text: " seeded" }),
      JSON.stringify({ event: "turn-text", text: ", one packaged." }),
      JSON.stringify({ event: "turn-complete", status: "success", output: "Four seeded, one packaged." }),
    ];
    const code = await runCollieLoop(
      envelope(),
      deps({
        ...mc.seams,
        ...clock,
        runHarnessTurn: async (_run, _turn, onText) => {
          let reply: string | null = null;
          for (const line of stream) {
            const text = textFromEvent(line);
            if (text !== undefined) onText(text);
            const out = replyFromEvent(line);
            if (out !== undefined) reply = out;
            await Promise.resolve();
            clock.advance(400);
          }
          return { reply, nativeSessionId: "n1" };
        },
      }),
    );
    expect(code).toBe(0);
    const drafts = mc.calls.filter((c) => c[1] === "draft");
    expect(drafts.map((c) => c[6])).toEqual(["Four", "Four seeded, one packaged."]);
    const replyAt = mc.calls.findIndex((c) => c[1] === "reply");
    expect(mc.calls[replyAt]).toEqual(["collie", "reply", "h-abc", "--to", "7", "--body", "Four seeded, one packaged."]);
    // Every draft lands before the reply; nothing drafts after it.
    expect(mc.calls.slice(replyAt).some((c) => c[1] === "draft")).toBe(false);
  });

  test("a refused draft is logged once and the reply still posts", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {}, // runner-started
      { message: claimed({ id: "7", body: "ping" }) },
      null, // draft refused: the session was frozen without the verb
      {}, // reply ack
      refused(1, SESSION_INACTIVE_CODE, "session is not active"), // the session ended: the runner exits
    );
    const clock = manualClock();
    const logs: string[] = [];
    const code = await runCollieLoop(
      envelope(),
      deps({
        ...mc.seams,
        ...clock,
        log: msgLogger(logs),
        runHarnessTurn: async (_run, _turn, onText) => {
          for (const piece of ["Four", " seeded", ", one packaged."]) {
            onText(piece);
            await Promise.resolve();
            clock.advance(2_000);
          }
          return { reply: "Four seeded, one packaged." };
        },
      }),
    );
    expect(code).toBe(0);
    expect(mc.calls.filter((c) => c[1] === "draft").length).toBe(1);
    expect(mc.calls.find((c) => c[1] === "reply")).toEqual(
      ["collie", "reply", "h-abc", "--to", "7", "--body", "Four seeded, one packaged."],
    );
    expect(logs.filter((m) => m.includes("drafts stop"))).toEqual(["turn 7: draft write refused; drafts stop for this turn"]);
  });

  test("the first native session becomes durable before the provider turn ends", async () => {
    const mc = new FakeMc();
    mc.enqueue({}, { message: claimed({ id: "7", body: "first" }) }, {});
    let observed!: () => void;
    const saved = new Promise<void>(resolve => { observed = resolve; });
    let finish!: () => void;
    const held = new Promise<void>(resolve => { finish = resolve; });
    const run = runCollieLoop(envelope(), deps({ ...mc.seams,
      runHarnessTurn: async (_run, _turn, _text, _response, native) => {
        await native("native-first", "native.jsonl");
        observed();
        await held;
        return { reply: null, nativeSessionId: "native-first", nativeFile: "native.jsonl" };
      },
    }));
    await saved;
    expect(mc.calls).toContainEqual(["run", "register-session", "h-abc", "--native-ref", "native-first", "--file", "native.jsonl"]);
    expect(mc.calls.some(call => call[1] === "reply")).toBe(false);
    finish();
    expect(await run).toBe(1);
    expect(mc.calls.filter(call => call[1] === "register-session")).toHaveLength(1);
  });

  test("registers the native locator once on the first observed session-start", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {}, // runner-started
      { message: claimed({ body: "a" }) },
      {}, // register-session ack
      {}, // reply ack
      { message: claimed({ id: "2", seq: "2", body: "b" }) },
      {}, // reply ack (no second register)
      ...emptyPollsPastDeadline(envelope()),
    );
    const code = await runCollieLoop(
      envelope(),
      deps({
        ...mc.seams,
        runHarnessTurn: async () => ({
          reply: "r",
          nativeSessionId: "native-xyz",
          nativeFile: "trace/native.jsonl",
        }),
      }),
    );
    expect(code).toBe(0);
    const regs = mc.calls.filter((c) => c[0] === "run" && c[1] === "register-session");
    expect(regs).toEqual([
      ["run", "register-session", "h-abc", "--native-ref", "native-xyz", "--file", "trace/native.jsonl"],
    ]);
  });

  test("the first native handle moves every later turn into native continuation", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {},
      { message: claimed({ body: "a" }) },
      {},
      {},
      { message: claimed({ id: "2", seq: "2", body: "b" }) },
      {},
      { message: null }, { message: null },
    );
    const invocations: CollieEnvelope[] = [];
    const turns: string[] = [];
    const code = await runCollieLoop(
      envelope(),
      deps({
        ...mc.seams,
        runHarnessTurn: async (run, turn) => {
          invocations.push({ ...run });
          turns.push(turn);
          return {
            reply: "r",
            nativeSessionId: "native-xyz",
            nativeFile: "trace/native.jsonl",
          };
        },
      }),
    );
    expect(code).toBe(0);
    expect(invocations.map((run) => [run.mode, run.native_session_ref, run.trace_filename])).toEqual([
      ["fresh", undefined, undefined],
      ["native", "native-xyz", "trace/native.jsonl"],
    ]);
    expect(turns).toEqual([
      `${envelope().directive}\n\n# Conversation\n\nThis session receives operator messages from the dashboard or another connected surface. A Board message carries Project context and requires confirmation for a new state change. A packet conversation carries packet context about one packet. Project initialization runs before pipeline work. For environment questions, inspect the Project through mc environment get. Image dependencies require operator approval in the Setup Journal or Environment page.\n\n# Discord access\n\nDiscord setup and account approval belong to Configs → Discord. Culler consumes account confirmation codes before Collie receives operator messages. A channel message starts a Collie session in its own Discord thread. The operator can continue that session from its thread or the dashboard. A reply reaches the same thread when the operator uses the dashboard.\n\n# Trace access\n\nThe read-only trace copies at /mc/traces/<id>/<trace_filename> contain complete records with credential masks. The status.json file states availability; a missing or pending copy requires a later read.\n\n# Diagnostic context\n\nA dashboard diagnostic message contains a short issue summary and references for permitted reads. A proposed state change requires explicit operator confirmation.\n\nConversation identifier: ${envelope().session_id}\nOperator message sequence: 1\n\n# Operator message\n\na`,
      `${envelope().directive}\n\n# Conversation\n\nThis session receives operator messages from the dashboard or another connected surface. A Board message carries Project context and requires confirmation for a new state change. A packet conversation carries packet context about one packet. Project initialization runs before pipeline work. For environment questions, inspect the Project through mc environment get. Image dependencies require operator approval in the Setup Journal or Environment page.\n\n# Discord access\n\nDiscord setup and account approval belong to Configs → Discord. Culler consumes account confirmation codes before Collie receives operator messages. A channel message starts a Collie session in its own Discord thread. The operator can continue that session from its thread or the dashboard. A reply reaches the same thread when the operator uses the dashboard.\n\n# Trace access\n\nThe read-only trace copies at /mc/traces/<id>/<trace_filename> contain complete records with credential masks. The status.json file states availability; a missing or pending copy requires a later read.\n\n# Diagnostic context\n\nA dashboard diagnostic message contains a short issue summary and references for permitted reads. A proposed state change requires explicit operator confirmation.\n\nConversation identifier: ${envelope().session_id}\nOperator message sequence: 2\n\n# Operator message\n\nb`,
    ]);
  });

  test("an explicit native resume uses the frozen handle on its first turn", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {},
      { message: claimed({ body: "a", seq: "7" }) },
      {},
      {},
      { message: null }, { message: null },
    );
    const invocations: CollieEnvelope[] = [];
    const turns: string[] = [];
    const code = await runCollieLoop(
      envelope({
        mode: "native",
        native_session_ref: "native-xyz",
        trace_filename: "trace/native.jsonl",
      }),
      deps({
        ...mc.seams,
        runHarnessTurn: async (run, turn) => {
          invocations.push({ ...run });
          turns.push(turn);
          return {
            reply: "continued",
            nativeSessionId: "native-xyz",
            nativeFile: "trace/native.jsonl",
          };
        },
      }),
    );
    expect(code).toBe(0);
    expect(invocations[0]?.mode).toBe("native");
    expect(invocations[0]?.native_session_ref).toBe("native-xyz");
    expect(turns).toEqual([`${envelope().directive}\n\n# Conversation\n\nThis session receives operator messages from the dashboard or another connected surface. A Board message carries Project context and requires confirmation for a new state change. A packet conversation carries packet context about one packet. Project initialization runs before pipeline work. For environment questions, inspect the Project through mc environment get. Image dependencies require operator approval in the Setup Journal or Environment page.\n\n# Discord access\n\nDiscord setup and account approval belong to Configs → Discord. Culler consumes account confirmation codes before Collie receives operator messages. A channel message starts a Collie session in its own Discord thread. The operator can continue that session from its thread or the dashboard. A reply reaches the same thread when the operator uses the dashboard.\n\n# Trace access\n\nThe read-only trace copies at /mc/traces/<id>/<trace_filename> contain complete records with credential masks. The status.json file states availability; a missing or pending copy requires a later read.\n\n# Diagnostic context\n\nA dashboard diagnostic message contains a short issue summary and references for permitted reads. A proposed state change requires explicit operator confirmation.\n\nConversation identifier: ${envelope().session_id}\nOperator message sequence: 7\n\n# Operator message\n\na`]);
  });

  test("writes collie-turn-ended after each turn", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {}, // runner-started
      { message: claimed({ id: "7", body: "first" }) },
      {}, // reply ack
      { message: claimed({ id: "8", body: "second" }) },
      {}, // reply ack
      { message: claimed({ id: "9", body: "third" }) },
    );
    const order: string[] = [];
    const code = await runCollieLoop(envelope(), deps({
      ...mc.seams,
      runHarnessTurn: async (_run, turn) => {
        order.push(`turn:${turn.slice(turn.lastIndexOf("\n") + 1)}`);
        return { reply: turn.endsWith("third") ? null : "reply" };
      },
      turnEnded: () => order.push("ended"),
    }));
    // A turn without a reply still ends its harness turn.
    expect(code).toBe(1);
    expect(order).toEqual(["turn:first", "ended", "turn:second", "ended", "turn:third", "ended"]);
    expect(TURN_ENDED_LINE).toBe("collie-turn-ended");
  });

  test("a harness that yields no reply leaves the turn claimed and exits 1", async () => {
    const mc = new FakeMc();
    mc.enqueue({}, { message: claimed({ id: "9", body: "x" }) });
    const code = await runCollieLoop(
      envelope(),
      deps({ ...mc.seams, runHarnessTurn: async () => ({ reply: null }) }),
    );
    expect(code).toBe(1);
    expect(mc.calls.some((c) => c[1] === "reply")).toBe(false);
  });

  test("an unverifiable attachment leaves the turn claimed before the harness", async () => {
    const mc = new FakeMc();
    mc.enqueue({}, { message: claimed({ id: "9", body: "x" }) });
    let harnessCalls = 0;
    const code = await runCollieLoop(
      envelope(),
      deps({
        ...mc.seams,
        resolveTurn: async () => {
          throw new Error("attachment identity drifted");
        },
        runHarnessTurn: async () => {
          harnessCalls++;
          return { reply: "must not run" };
        },
      }),
    );
    expect(code).toBe(1);
    expect(harnessCalls).toBe(0);
    expect(mc.calls.some((c) => c[1] === "reply")).toBe(false);
  });

  test("the exit deadline is the envelope's idle timeout plus the margin", () => {
    expect(idleExitMs(envelope({ idle_timeout_s: 600 }))).toBe((600 + IDLE_EXIT_MARGIN_S) * 1000);
    // An envelope from an older `mc` carries none: the fixed default applies.
    expect(idleExitMs(envelope())).toBe((1800 + IDLE_EXIT_MARGIN_S) * 1000);
    expect(idleExitMs(envelope({ idle_timeout_s: 0 }))).toBe((1800 + IDLE_EXIT_MARGIN_S) * 1000);
  });

  test("empty polls before the idle deadline never end the loop", async () => {
    const run = envelope({ idle_timeout_s: 600 });
    const clock = fakeClock();
    const mc = new FakeMc();
    // One second short of the deadline, then a message: the runner must still
    // be there to claim it.
    const polls = idleExitMs(run) / 1000 - 1;
    mc.enqueue(
      {},
      ...Array.from({ length: polls }, () => ({ message: null })),
      { message: claimed({ id: "9", body: "late" }) },
      {}, // reply ack
      ...emptyPollsPastDeadline(run),
    );
    const turns: string[] = [];
    const code = await runCollieLoop(run, deps({
      ...mc.seams, now: clock.now, sleep: clock.sleep,
      runHarnessTurn: async (_r, t) => { turns.push(t); return { reply: "r" }; },
    }));
    expect(code).toBe(0);
    expect(turns).toEqual([`${run.directive}\n\n# Conversation\n\nThis session receives operator messages from the dashboard or another connected surface. A Board message carries Project context and requires confirmation for a new state change. A packet conversation carries packet context about one packet. Project initialization runs before pipeline work. For environment questions, inspect the Project through mc environment get. Image dependencies require operator approval in the Setup Journal or Environment page.\n\n# Discord access\n\nDiscord setup and account approval belong to Configs → Discord. Culler consumes account confirmation codes before Collie receives operator messages. A channel message starts a Collie session in its own Discord thread. The operator can continue that session from its thread or the dashboard. A reply reaches the same thread when the operator uses the dashboard.\n\n# Trace access\n\nThe read-only trace copies at /mc/traces/<id>/<trace_filename> contain complete records with credential masks. The status.json file states availability; a missing or pending copy requires a later read.\n\n# Diagnostic context\n\nA dashboard diagnostic message contains a short issue summary and references for permitted reads. A proposed state change requires explicit operator confirmation.\n\nConversation identifier: ${run.session_id}\nOperator message sequence: 1\n\n# Operator message\n\nlate`]);
    expect(mc.calls.filter((c) => c[1] === "claim").length).toBe(polls + 1 + idleExitMs(run) / 1000 + 1);
  });

  test("the runner exits on its own once the idle deadline passes with no message", async () => {
    const run = envelope({ idle_timeout_s: 600 });
    const clock = fakeClock();
    const mc = new FakeMc();
    mc.enqueue({}, ...emptyPollsPastDeadline(run));
    const logs: string[] = [];
    const code = await runCollieLoop(run, deps({
      ...mc.seams, now: clock.now, sleep: clock.sleep, log: msgLogger(logs),
    }));
    expect(code).toBe(0);
    // One claim per second up to the deadline, then the one that sees it.
    expect(mc.calls.filter((c) => c[1] === "claim").length).toBe(idleExitMs(run) / 1000 + 1);
    expect(clock.sleeps).toBe(idleExitMs(run) / 1000);
    expect(logs.some((m) => m.startsWith("no message for 720s"))).toBe(true);
  });

  test("a claim refused on an inactive session ends the runner with exit 0 and one line", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {}, // runner-started
      refused(1, SESSION_INACTIVE_CODE, 'Collie session "h-abc" is ended; transport requires an active session'),
    );
    const records: LogRecord[] = [];
    const code = await runCollieLoop(envelope(), deps({ ...mc.seams, log: recordLogger(records) }));
    expect(code).toBe(0);
    expect(mc.calls.filter((c) => c[1] === "claim").length).toBe(1);
    expect(records.length).toBe(1);
    expect(records[0]!.level).toBe("info");
    expect(records[0]!.event).toBe("runner.exited");
    expect(records[0]!.msg).toContain("is not active");
  });

  test("a transient claim failure keeps the runner polling and warns at most once a minute", async () => {
    const run = envelope({ idle_timeout_s: 600 });
    const clock = fakeClock();
    const mc = new FakeMc();
    const failures = CLAIM_WARN_INTERVAL_MS / 1000 + 30; // 90 s of failures
    mc.enqueue(
      {}, // runner-started
      ...Array.from({ length: failures }, () => refused(2, "", "helper container unreachable")),
      { message: claimed({ id: "9", body: "late" }) },
      {}, // reply ack
      ...emptyPollsPastDeadline(run),
    );
    const records: LogRecord[] = [];
    const turns: string[] = [];
    const code = await runCollieLoop(run, deps({
      ...mc.seams, now: clock.now, sleep: clock.sleep, log: recordLogger(records),
      runHarnessTurn: async (_r, t) => { turns.push(t); return { reply: "r" }; },
    }));
    expect(code).toBe(0);
    expect(turns).toEqual([`${run.directive}\n\n# Conversation\n\nThis session receives operator messages from the dashboard or another connected surface. A Board message carries Project context and requires confirmation for a new state change. A packet conversation carries packet context about one packet. Project initialization runs before pipeline work. For environment questions, inspect the Project through mc environment get. Image dependencies require operator approval in the Setup Journal or Environment page.\n\n# Discord access\n\nDiscord setup and account approval belong to Configs → Discord. Culler consumes account confirmation codes before Collie receives operator messages. A channel message starts a Collie session in its own Discord thread. The operator can continue that session from its thread or the dashboard. A reply reaches the same thread when the operator uses the dashboard.\n\n# Trace access\n\nThe read-only trace copies at /mc/traces/<id>/<trace_filename> contain complete records with credential masks. The status.json file states availability; a missing or pending copy requires a later read.\n\n# Diagnostic context\n\nA dashboard diagnostic message contains a short issue summary and references for permitted reads. A proposed state change requires explicit operator confirmation.\n\nConversation identifier: ${run.session_id}\nOperator message sequence: 1\n\n# Operator message\n\nlate`]);
    const warnings = records.filter((r) => r.event === "runner.mc_failed");
    // The first failure warns at once; the next warning waits a full minute.
    expect(warnings.length).toBe(2);
    expect(warnings.map((r) => r.level)).toEqual(["warn", "warn"]);
    expect(warnings[0]!.msg).toContain("helper container unreachable");
    expect(warnings[1]!["failures"]).toBe(CLAIM_WARN_INTERVAL_MS / 1000);
  });

  test("a changed runtime exits before the first turn without another claim", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {},
      refused(1, RUNTIME_CHANGED_CODE, "The agent profile changed."),
      { message: claimed() },
    );
    const clock = fakeClock();
    const records: LogRecord[] = [];
    let turns = 0;
    const code = await runCollieLoop(envelope(), deps({
      ...mc.seams, now: clock.now, sleep: clock.sleep, log: recordLogger(records),
      runHarnessTurn: async () => { turns += 1; return { reply: "unexpected" }; },
    }));
    expect(code).toBe(0);
    expect(turns).toBe(0);
    expect(clock.sleeps).toBe(0);
    expect(mc.calls.map(call => call[1])).toEqual(["runner-started", "claim"]);
    expect(records).toHaveLength(1);
    expect(records[0]!.event).toBe("runner.exited");
    expect(records[0]!["code"]).toBe("runtime-changed");
    expect(records[0]!.msg).toContain("resident can start a replacement");
  });

  test("a changed runtime preserves the completed reply and native locator without another turn", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {},
      { message: claimed({ attempt_id: "a".repeat(32) }) },
      {},
      {},
      refused(1, RUNTIME_CHANGED_CODE, "The connection authorization changed."),
      { message: claimed({ id: "2", seq: "2", body: "next" }) },
    );
    const clock = fakeClock();
    let turns = 0;
    const code = await runCollieLoop(envelope(), deps({
      ...mc.seams, now: clock.now, sleep: clock.sleep,
      runHarnessTurn: async () => {
        turns += 1;
        return { reply: "completed", nativeSessionId: "native-first", nativeFile: "trace/native.jsonl" };
      },
    }));
    expect(code).toBe(0);
    expect(turns).toBe(1);
    expect(clock.sleeps).toBe(0);
    expect(mc.calls.map(call => call[1])).toEqual([
      "runner-started", "claim", "register-session", "reply", "claim",
    ]);
    expect(mc.calls[2]).toEqual([
      "run", "register-session", "h-abc", "--native-ref", "native-first", "--file", "trace/native.jsonl",
    ]);
    expect(mc.calls[3]).toEqual([
      "collie", "reply", "h-abc", "--to", "1", "--body", "completed", "--attempt", "a".repeat(32),
    ]);
  });

  test("a reply restarts the idle clock", async () => {
    const run = envelope({ idle_timeout_s: 600 });
    const clock = fakeClock();
    const mc = new FakeMc();
    const half = idleExitMs(run) / 2000;
    mc.enqueue(
      {},
      ...Array.from({ length: half }, () => ({ message: null })),
      { message: claimed({ id: "3", body: "mid" }) },
      {},
      ...Array.from({ length: half }, () => ({ message: null })),
      { message: claimed({ id: "4", seq: "2", body: "still here" }) },
      {},
      ...emptyPollsPastDeadline(run),
    );
    const turns: string[] = [];
    const code = await runCollieLoop(run, deps({
      ...mc.seams, now: clock.now, sleep: clock.sleep,
      runHarnessTurn: async (_r, t) => { turns.push(t); return { reply: "r" }; },
    }));
    expect(code).toBe(0);
    expect(turns).toEqual([
      `${run.directive}\n\n# Conversation\n\nThis session receives operator messages from the dashboard or another connected surface. A Board message carries Project context and requires confirmation for a new state change. A packet conversation carries packet context about one packet. Project initialization runs before pipeline work. For environment questions, inspect the Project through mc environment get. Image dependencies require operator approval in the Setup Journal or Environment page.\n\n# Discord access\n\nDiscord setup and account approval belong to Configs → Discord. Culler consumes account confirmation codes before Collie receives operator messages. A channel message starts a Collie session in its own Discord thread. The operator can continue that session from its thread or the dashboard. A reply reaches the same thread when the operator uses the dashboard.\n\n# Trace access\n\nThe read-only trace copies at /mc/traces/<id>/<trace_filename> contain complete records with credential masks. The status.json file states availability; a missing or pending copy requires a later read.\n\n# Diagnostic context\n\nA dashboard diagnostic message contains a short issue summary and references for permitted reads. A proposed state change requires explicit operator confirmation.\n\nConversation identifier: ${run.session_id}\nOperator message sequence: 1\n\n# Operator message\n\nmid`,
      `${run.directive}\n\n# Conversation\n\nThis session receives operator messages from the dashboard or another connected surface. A Board message carries Project context and requires confirmation for a new state change. A packet conversation carries packet context about one packet. Project initialization runs before pipeline work. For environment questions, inspect the Project through mc environment get. Image dependencies require operator approval in the Setup Journal or Environment page.\n\n# Discord access\n\nDiscord setup and account approval belong to Configs → Discord. Culler consumes account confirmation codes before Collie receives operator messages. A channel message starts a Collie session in its own Discord thread. The operator can continue that session from its thread or the dashboard. A reply reaches the same thread when the operator uses the dashboard.\n\n# Trace access\n\nThe read-only trace copies at /mc/traces/<id>/<trace_filename> contain complete records with credential masks. The status.json file states availability; a missing or pending copy requires a later read.\n\n# Diagnostic context\n\nA dashboard diagnostic message contains a short issue summary and references for permitted reads. A proposed state change requires explicit operator confirmation.\n\nConversation identifier: ${run.session_id}\nOperator message sequence: 2\n\n# Operator message\n\nstill here`,
    ]);
  });
});

test("Collie reports a complete response during its reply attempt", async () => {
  const mc = new FakeMc();
  const attempt = "b".repeat(32);
  mc.enqueue({}, { message: claimed({ id: "7", body: "ping", attempt_id: attempt }) }, {}, {}, ...emptyPollsPastDeadline(envelope()));
  const code = await runCollieLoop(envelope(), deps({
    ...mc.seams,
    runHarnessTurn: async (_run, _turn, _text, response) => {
      response();
      await Promise.resolve();
      expect(mc.calls).toContainEqual(["collie", "model-response", attempt]);
      return { reply: "pong" };
    },
  }));
  expect(code).toBe(0);
  expect(mc.calls.findIndex(call => call[1] === "model-response")).toBeLessThan(mc.calls.findIndex(call => call[1] === "reply"));
});

describe("packet conversations", () => {
  const packet = { packet_id: 41, version_id: 88, task_id: 21, project_id: "project-53d8af33-b12" };
  const packetContext = "# Packet context\n\nThis conversation belongs to the packet below. The packet content is complete up to its size limit, and each identifier names a record that you read on demand.\n\n## Packet identity\n\n- Task: 21 (Add the export command)";
  const contextAnswer = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
    session_id: "h-abc", packet_id: 41, version_id: 88, context: packetContext, ...over,
  });
  const message = (over: Record<string, unknown> = {}) =>
    parseClaimedMessage(claimed(over), "h-abc")!;
  const contextReads = (mc: FakeMc) => mc.calls.filter((c) => c[1] === "packet-context");

  test("the turn prompt puts the packet context after the identifiers and before the operator message", () => {
    const run = envelope({ packet });
    expect(collieTurnPrompt(run, message({ seq: "3" }), "Why this approach?", packetContext)).toBe(
      `${run.directive}\n\n# Conversation\n\nThis session receives operator messages from the dashboard or another connected surface. A Board message carries Project context and requires confirmation for a new state change. A packet conversation carries packet context about one packet. Project initialization runs before pipeline work. For environment questions, inspect the Project through mc environment get. Image dependencies require operator approval in the Setup Journal or Environment page.\n\n# Discord access\n\nDiscord setup and account approval belong to Configs → Discord. Culler consumes account confirmation codes before Collie receives operator messages. A channel message starts a Collie session in its own Discord thread. The operator can continue that session from its thread or the dashboard. A reply reaches the same thread when the operator uses the dashboard.\n\n# Trace access\n\nThe read-only trace copies at /mc/traces/<id>/<trace_filename> contain complete records with credential masks. The status.json file states availability; a missing or pending copy requires a later read.\n\n# Diagnostic context\n\nA dashboard diagnostic message contains a short issue summary and references for permitted reads. A proposed state change requires explicit operator confirmation.\n\nConversation identifier: h-abc\nOperator message sequence: 3\n\n${packetContext}\n\n# Operator message\n\nWhy this approach?`,
    );
  });

  test("a packet conversation adds the packet context to the first turn of a native session only", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {}, // runner-started
      { message: claimed({ id: "7", body: "first" }) },
      contextAnswer(),
      {}, // register-session ack
      {}, // reply ack
      { message: claimed({ id: "8", seq: "2", body: "second" }) },
      {}, // reply ack
      refused(1, SESSION_INACTIVE_CODE, "session is not active"),
    );
    const run = envelope({ packet });
    const turns: string[] = [];
    const code = await runCollieLoop(run, deps({
      ...mc.seams,
      runHarnessTurn: async (_run, turn) => {
        turns.push(turn);
        return { reply: "r", nativeSessionId: "native-xyz", nativeFile: "native.jsonl" };
      },
    }));
    expect(code).toBe(0);
    expect(mc.calls.map((c) => c[1])).toEqual([
      "runner-started", "claim", "packet-context", "register-session", "reply", "claim", "reply", "claim",
    ]);
    expect(contextReads(mc)).toEqual([["collie", "packet-context", "h-abc"]]);
    expect(turns).toEqual([
      collieTurnPrompt(run, message({ id: "7", body: "first" }), "first", packetContext),
      collieTurnPrompt(run, message({ id: "8", seq: "2", body: "second" }), "second"),
    ]);
    expect(turns[0]).toContain(packetContext);
    expect(turns[1]).not.toContain("# Packet context");
  });

  test("a turn without a native session start keeps the packet context without a second read", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {},
      { message: claimed({ id: "7", body: "first" }) },
      contextAnswer(),
      {}, // reply ack
      { message: claimed({ id: "8", seq: "2", body: "second" }) },
      {}, // reply ack
      refused(1, SESSION_INACTIVE_CODE, "session is not active"),
    );
    const turns: string[] = [];
    const code = await runCollieLoop(envelope({ packet }), deps({
      ...mc.seams,
      runHarnessTurn: async (_run, turn) => { turns.push(turn); return { reply: "r" }; },
    }));
    expect(code).toBe(0);
    expect(contextReads(mc).length).toBe(1);
    expect(turns.map((turn) => turn.includes(packetContext))).toEqual([true, true]);
  });

  test("a failed native session save leaves the reply incomplete", async () => {
    const mc = new FakeMc();
    mc.enqueue({}, { message: claimed({ id: "7", body: "first" }) }, contextAnswer(), null);
    let calls = 0;
    const code = await runCollieLoop(envelope({ packet }), deps({ ...mc.seams,
      runHarnessTurn: async () => { calls++; return { reply: "r", nativeSessionId: "native-first", nativeFile: "native.jsonl" }; },
    }));
    expect(code).toBe(1);
    expect(calls).toBe(1);
    expect(mc.calls.some(call => call[1] === "reply")).toBe(false);
    expect(mc.calls.filter(call => call[1] === "claim")).toHaveLength(1);
  });

  test("a native resume omits the packet context", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {},
      { message: claimed({ id: "7", seq: "4", body: "continue" }) },
      {}, // register-session ack for the same pair
      {}, // reply ack
      refused(1, SESSION_INACTIVE_CODE, "session is not active"),
    );
    const run = envelope({
      packet, mode: "native", native_session_ref: "native-xyz", trace_filename: "native.jsonl",
    });
    const turns: string[] = [];
    const code = await runCollieLoop(run, deps({
      ...mc.seams,
      runHarnessTurn: async (_run, turn) => {
        turns.push(turn);
        return { reply: "r", nativeSessionId: "native-xyz", nativeFile: "native.jsonl" };
      },
    }));
    expect(code).toBe(0);
    expect(contextReads(mc)).toEqual([]);
    expect(turns).toEqual([collieTurnPrompt(run, message({ id: "7", seq: "4", body: "continue" }), "continue")]);
    expect(turns[0]).not.toContain("# Packet context");
  });

  test("a packet context for another packet leaves the turn claimed", async () => {
    const { context: _context, ...withoutContext } = contextAnswer();
    for (const answer of [
      contextAnswer({ packet_id: 40 }),
      contextAnswer({ version_id: 87 }),
      contextAnswer({ packet_id: "41" }),
      contextAnswer({ session_id: "h-other" }),
      contextAnswer({ context: "" }),
      contextAnswer({ context: 7 }),
      contextAnswer({ task_id: 21 }),
      withoutContext,
    ]) {
      const mc = new FakeMc();
      mc.enqueue({}, { message: claimed({ id: "7", body: "question" }) }, answer);
      const records: LogRecord[] = [];
      let harnessCalls = 0;
      const code = await runCollieLoop(envelope({ packet }), deps({
        ...mc.seams,
        log: recordLogger(records),
        runHarnessTurn: async () => { harnessCalls++; return { reply: "must not run" }; },
      }));
      expect(code).toBe(1);
      expect(harnessCalls).toBe(0);
      expect(mc.calls.map((c) => c[1])).toEqual(["runner-started", "claim", "packet-context"]);
      expect(records.map((r) => [r.level, r.event, r.msg])).toEqual([[
        "error", "runner.turn_started",
        "turn 7: The packet context does not match this conversation; leaving it claimed",
      ]]);
    }
  });

  test("a refusal of the packet context keeps its reason in the log", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {},
      { message: claimed({ id: "7", body: "question" }) },
      refused(1, "stale-run", 'a Collie runner reads only its own session; run.json identifies "h-other"'),
    );
    const records: LogRecord[] = [];
    let harnessCalls = 0;
    const code = await runCollieLoop(envelope({ packet }), deps({
      ...mc.seams,
      log: recordLogger(records),
      runHarnessTurn: async () => { harnessCalls++; return { reply: "must not run" }; },
    }));
    expect(code).toBe(1);
    expect(harnessCalls).toBe(0);
    expect(mc.calls.map((c) => c[1])).toEqual(["runner-started", "claim", "packet-context"]);
    expect(records.map((r) => [r.level, r.event, r.msg, r.code ?? null])).toEqual([
      ["warn", "runner.mc_failed",
        'mc collie packet-context h-abc exited 1: a Collie runner reads only its own session; run.json identifies "h-other"',
        "stale-run"],
      ["error", "runner.turn_started",
        "turn 7: The packet context does not match this conversation; leaving it claimed", null],
    ]);
  });

  test("the operator message holds no packet context", async () => {
    const mc = new FakeMc();
    mc.enqueue(
      {},
      { message: claimed({ id: "7", body: "Summarize the report." }) },
      contextAnswer(),
      {}, // reply ack
      refused(1, SESSION_INACTIVE_CODE, "session is not active"),
    );
    const resolved: Record<string, unknown>[] = [];
    const turns: string[] = [];
    const code = await runCollieLoop(envelope({ packet }), deps({
      ...mc.seams,
      resolveTurn: async (claim) => { resolved.push({ ...claim }); return claim.body; },
      runHarnessTurn: async (_run, turn) => { turns.push(turn); return { reply: "r" }; },
    }));
    expect(code).toBe(0);
    expect(resolved).toEqual([{ ...claimed({ id: "7", body: "Summarize the report." }) }]);
    const [before, operator] = turns[0]!.split("# Operator message\n\n");
    expect(operator).toBe("Summarize the report.");
    expect(before).toEndWith(`${packetContext}\n\n`);
    expect(turns[0]!.split(packetContext).length).toBe(2);
    // The claim frame stays closed: it cannot carry packet context.
    expect(() => parseClaimedMessage(claimed({ packet_context: packetContext }), "h-abc")).toThrow();
  });
});
