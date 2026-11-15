import { describe, expect, test } from "bun:test";
import { AttachmentConfinementHealth } from "./attachment-health";
import {
  abortAttachmentAttemptViaMc,
  createAttachmentTerminalChore,
  expireDueAttachmentAttemptsViaMc,
  publishAttachmentPublicationViaMc,
  renewAttachmentPublicationViaMc,
  reconcileAttachmentTerminalsViaMc,
  reserveAttachmentIngressViaMc,
} from "./attachment-ingress";
import { greenAttachmentCanaryReceipt } from "./test-helpers";
import type { Exec, ExecResult } from "./types";

const generation = "a".repeat(32);

function reservation(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    attempt_id: "b".repeat(16),
    batch_id: "7",
    byte_count: 7,
    hard_deadline: "2026-07-30 18:15:00",
    ingress_id: "9",
    lease_until: "2026-07-30 18:00:30",
    object_count: 1,
    owner_token: "c".repeat(32),
    publications: [{
      display_name: "capture.png",
      media_type: "image/png",
      ordinal: 1,
      owner_token: "d".repeat(32),
      publication_id: "e".repeat(16),
      size: 7,
    }],
    replayed: false,
    ...overrides,
  };
}

function terminal(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    attempt: 1,
    attempt_id: "b".repeat(16),
    batch_id: "9007199254740993",
    ingress_id: "9007199254740992",
    outcome: "requeued",
    reason: "explicit_abort",
    receipt: "{\"outcome\":\"requeued\"}",
    replayed: false,
    ...overrides,
  };
}

function healthy(): AttachmentConfinementHealth {
  const health = new AttachmentConfinementHealth();
  health.markHealthy(greenAttachmentCanaryReceipt(generation));
  return health;
}

function result(value: unknown): ExecResult {
  return { exitCode: 0, stdout: JSON.stringify(value), stderr: "" };
}

function decodeFrame(frame: Uint8Array | undefined): unknown {
  if (frame === undefined) throw new Error("missing private frame");
  const body = Buffer.from(frame);
  expect(body.readUInt32BE(0)).toBe(body.length - 4);
  return JSON.parse(body.subarray(4).toString("utf8"));
}

describe("resident attachment ingress capability", () => {
  test("an unhealthy boot refuses reservation before invoking mc", async () => {
    let called = false;
    const runMc: Exec = async () => {
      called = true;
      return result(reservation());
    };
    await expect(reserveAttachmentIngressViaMc(
      runMc, new AttachmentConfinementHealth(), "h-session",
    )).rejects.toThrow("unhealthy for this resident boot");
    expect(called).toBeFalse();
  });

  test("reservation receives only this boot's exact healthy generation", async () => {
    const calls: Array<{ argv: string[]; frame: unknown }> = [];
    const receipt = await reserveAttachmentIngressViaMc(async (argv, stdin) => {
      calls.push({ argv, frame: decodeFrame(stdin) });
      return result(reservation());
    }, healthy(), "h-session");
    expect(calls).toEqual([{
      argv: ["__attachment-ingress-reserve"],
      frame: { owner_generation: generation, session: "h-session" },
    }]);
    expect(receipt.attempt_id).toBe("b".repeat(16));
    expect(receipt.publications[0]?.publication_id).toBe("e".repeat(16));
  });

  test("renewal carries the exact paired capabilities and boot generation", async () => {
    const calls: Array<{ argv: string[]; frame: unknown }> = [];
    const receipt = await renewAttachmentPublicationViaMc(async (argv, stdin) => {
      calls.push({ argv, frame: decodeFrame(stdin) });
      return result({
        attempt_id: "b".repeat(16),
        hard_deadline: "2026-07-30 18:15:00",
        lease_until: "2026-07-30 18:01:00",
        publication_id: "e".repeat(16),
      });
    }, healthy(), {
      session: "h-session",
      attempt_id: "b".repeat(16),
      attempt_owner_token: "c".repeat(32),
      publication_id: "e".repeat(16),
      publication_owner_token: "d".repeat(32),
    });
    expect(calls).toEqual([{
      argv: ["__attachment-publication-renew"],
      frame: {
        attempt_id: "b".repeat(16),
        attempt_owner_token: "c".repeat(32),
        owner_generation: generation,
        publication_id: "e".repeat(16),
        publication_owner_token: "d".repeat(32),
        session: "h-session",
      },
    }]);
    expect(receipt.publication_id).toBe("e".repeat(16));
  });

  test("abort carries the exact attempt capability and preserves large identities", async () => {
    const calls: Array<{ argv: string[]; frame: unknown }> = [];
    const receipt = await abortAttachmentAttemptViaMc(async (argv, stdin) => {
      calls.push({ argv, frame: decodeFrame(stdin) });
      return result(terminal());
    }, healthy(), {
      session: "h-session",
      attempt_id: "b".repeat(16),
      owner_token: "c".repeat(32),
    });
    expect(calls).toEqual([{
      argv: ["__attachment-attempt-abort"],
      frame: {
        attempt_id: "b".repeat(16),
        owner_generation: generation,
        owner_token: "c".repeat(32),
        session: "h-session",
      },
    }]);
    expect(receipt.ingress_id).toBe("9007199254740992");
    expect(receipt.batch_id).toBe("9007199254740993");
  });

  test("hard-expiry is one boot-authorized bounded sweep from zero", async () => {
    const calls: Array<{ argv: string[]; frame: unknown }> = [];
    const page = await expireDueAttachmentAttemptsViaMc(async (argv, stdin) => {
      calls.push({ argv, frame: decodeFrame(stdin) });
      return result({
        next: "b".repeat(16),
        receipts: [terminal({
          outcome: "failed",
          reason: "hard_expired",
        })],
      });
    }, healthy());
    expect(calls).toEqual([{
      argv: ["__attachment-attempts-expire-due"],
      frame: {
        after: "",
        limit: 128,
        owner_generation: generation,
      },
    }]);
    expect(page.next).toBe("b".repeat(16));
    expect(page.receipts[0]?.reason).toBe("hard_expired");
  });

  test("the resident runs one bounded expiry page at startup and before each tick", async () => {
    const calls: Array<{ argv: string[]; frame: unknown }> = [];
    const chore = createAttachmentTerminalChore(async (argv, stdin) => {
      calls.push({ argv, frame: decodeFrame(stdin) });
      return argv[0] === "__attachment-attempts-expire-due"
        ? result({ receipts: [] })
        : result({ batches: [] });
    }, healthy());
    await chore.startup();
    await chore.beforeTick();
    expect(calls).toEqual([
      {
        argv: ["__attachment-attempts-expire-due"],
        frame: { after: "", limit: 128, owner_generation: generation },
      },
      {
        argv: ["__attachment-terminals-reconcile"],
        frame: { after_batch_id: "0", limit: 128, owner_generation: generation },
      },
      {
        argv: ["__attachment-attempts-expire-due"],
        frame: { after: "", limit: 128, owner_generation: generation },
      },
      {
        argv: ["__attachment-terminals-reconcile"],
        frame: { after_batch_id: "0", limit: 128, owner_generation: generation },
      },
    ]);
  });

  test("terminal compaction preserves exact batch identities in one bounded page", async () => {
    const calls: Array<{ argv: string[]; frame: unknown }> = [];
    const page = await reconcileAttachmentTerminalsViaMc(async (argv, stdin) => {
      calls.push({ argv, frame: decodeFrame(stdin) });
      return result({
        batches: [{
          attempt_id: "b".repeat(16),
          batch_id: "9007199254740993",
          ingress_id: "9007199254740992",
          outcome: "requeued",
          session_id: "h-session",
        }],
        next: "9007199254740993",
      });
    }, healthy());
    expect(calls).toEqual([{
      argv: ["__attachment-terminals-reconcile"],
      frame: {
        after_batch_id: "0",
        limit: 128,
        owner_generation: generation,
      },
    }]);
    expect(page.next).toBe("9007199254740993");
    expect(page.batches[0]?.ingress_id).toBe("9007199254740992");
  });

  test("publication carries one canonical capability header followed by exact bytes", async () => {
    const calls: Array<{ argv: string[]; header: unknown; bytes: string }> = [];
    const receipt = await publishAttachmentPublicationViaMc(async (argv, stdin) => {
      if (stdin === undefined) throw new Error("missing publication body");
      const frame = Buffer.from(stdin);
      const length = frame.readUInt32BE(0);
      calls.push({
        argv,
        header: JSON.parse(frame.subarray(4, length + 4).toString("utf8")),
        bytes: frame.subarray(length + 4).toString("utf8"),
      });
      return result({
        device: "16777231",
        file_type: "regular",
        inode: "9007199254740993",
        publication_id: "e".repeat(16),
        reused: false,
        sha256: "f".repeat(64),
        size: 7,
        state: "materialized",
      });
    }, healthy(), {
      session: "h-session",
      attempt_id: "b".repeat(16),
      attempt_owner_token: "c".repeat(32),
      publication_id: "e".repeat(16),
      publication_owner_token: "d".repeat(32),
      size: 7,
    }, Buffer.from("publish"));
    expect(calls).toEqual([{
      argv: ["__attachment-publication-publish"],
      header: {
        attempt_id: "b".repeat(16),
        attempt_owner_token: "c".repeat(32),
        owner_generation: generation,
        publication_id: "e".repeat(16),
        publication_owner_token: "d".repeat(32),
        session: "h-session",
        size: 7,
      },
      bytes: "publish",
    }]);
    expect(receipt.inode).toBe("9007199254740993");
  });

  test("publication refuses unhealthy, mismatched bytes, and expanded receipts", async () => {
    const request = {
      session: "h-session",
      attempt_id: "b".repeat(16),
      attempt_owner_token: "c".repeat(32),
      publication_id: "e".repeat(16),
      publication_owner_token: "d".repeat(32),
      size: 7,
    };
    let called = false;
    await expect(publishAttachmentPublicationViaMc(async () => {
      called = true;
      return result({});
    }, new AttachmentConfinementHealth(), request, Buffer.from("publish")))
      .rejects.toThrow("unhealthy for this resident boot");
    expect(called).toBeFalse();

    await expect(publishAttachmentPublicationViaMc(
      async () => result({}), healthy(), request, Buffer.from("short"),
    )).rejects.toThrow("request is malformed");

    await expect(publishAttachmentPublicationViaMc(async () => result({
      device: "1", file_type: "regular", inode: "2",
      publication_id: "e".repeat(16), reused: false,
      sha256: "f".repeat(64), size: 7, state: "materialized", expanded: true,
    }), healthy(), request, Buffer.from("publish")))
      .rejects.toThrow("malformed publication receipt");
  });

  test("closed receipt parsing refuses expanded or inconsistent reservation authority", async () => {
    for (const malformed of [
      reservation({ extra: true }),
      reservation({ ingress_id: 9 }),
      reservation({ attempt_id: 1111111111111111 }),
      reservation({ object_count: 2 }),
      reservation({ publications: [{
        display_name: "capture.png", media_type: "image/png", ordinal: 2,
        owner_token: "d".repeat(32), publication_id: "e".repeat(16), size: 7,
      }] }),
    ]) {
      await expect(reserveAttachmentIngressViaMc(
        async () => result(malformed), healthy(), "h-session",
      )).rejects.toThrow("malformed reservation receipt");
    }
  });

  test("renewal refuses malformed output and an unhealthy boot before invoking mc", async () => {
    let called = false;
    await expect(renewAttachmentPublicationViaMc(async () => {
      called = true;
      return result({});
    }, new AttachmentConfinementHealth(), {
      session: "h-session",
      attempt_id: "b".repeat(16),
      attempt_owner_token: "c".repeat(32),
      publication_id: "e".repeat(16),
      publication_owner_token: "d".repeat(32),
    })).rejects.toThrow("unhealthy for this resident boot");
    expect(called).toBeFalse();

    await expect(renewAttachmentPublicationViaMc(async () => result({
      attempt_id: "f".repeat(16),
      hard_deadline: "2026-07-30 18:15:00",
      lease_until: "2026-07-30 18:01:00",
      publication_id: "e".repeat(16),
    }), healthy(), {
      session: "h-session",
      attempt_id: "b".repeat(16),
      attempt_owner_token: "c".repeat(32),
      publication_id: "e".repeat(16),
      publication_owner_token: "d".repeat(32),
    })).rejects.toThrow("malformed renewal receipt");
  });

  test("terminal operations refuse unhealthy boots and expanded or rounded receipts", async () => {
    let called = false;
    await expect(abortAttachmentAttemptViaMc(async () => {
      called = true;
      return result(terminal());
    }, new AttachmentConfinementHealth(), {
      session: "h-session",
      attempt_id: "b".repeat(16),
      owner_token: "c".repeat(32),
    })).rejects.toThrow("unhealthy for this resident boot");
    expect(called).toBeFalse();

    for (const malformed of [
      terminal({ expanded: true }),
      terminal({ ingress_id: 9_007_199_254_740_992 }),
      terminal({ attempt_id: "f".repeat(16) }),
      terminal({ outcome: "unknown" }),
    ]) {
      await expect(abortAttachmentAttemptViaMc(
        async () => result(malformed), healthy(), {
          session: "h-session",
          attempt_id: "b".repeat(16),
          owner_token: "c".repeat(32),
        },
      )).rejects.toThrow("malformed terminal receipt");
    }

    await expect(expireDueAttachmentAttemptsViaMc(
      async () => result({ receipts: [], expanded: true }), healthy(),
    )).rejects.toThrow("malformed expiry page");

    await expect(reconcileAttachmentTerminalsViaMc(
      async () => result({
        batches: [],
        next: "9007199254740993",
      }), healthy(),
    )).rejects.toThrow("malformed reconciliation page");
  });
});
