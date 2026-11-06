// collie.smoke.test.ts — the Collie/Review/Board/Activity smokes: a real
// server, a real Chromium, and a real `mc` (test_fake_routing build) against
// a scratch spine, with no Docker. The "New session" button starts a session.
// The browser sends "ping". The test then plays the runner with a fabricated
// tier:"collie" run.json (claim, then reply) in place of the woken container.
// The test asserts that the reply renders in the Collie transcript.
//
// Run via dashboard/smoke.sh, which builds mc and exports MC_SMOKE_BIN.

import { Database } from "bun:sqlite";
import { afterAll, beforeAll, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { chmodSync, mkdirSync, statSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import { SMOKE_BEARER, bootSmoke, gotoReady, mc, teardown, type Smoke } from "./harness";

/** Runs one SQL script against the scratch spine through bun:sqlite, so the
 * fixture needs no sqlite3 binary on PATH. Seeding TYPED attachment claims has
 * no host CLI: production rows are born through the resident's private broker
 * protocol (canary receipt, health-guard flock, framed byte streams), which a
 * browser smoke has no business speaking, and mc's own CLI tests seed the
 * identical rows the same direct way (attachmentBytesCLIFixture). This stays a
 * test fixture, never a back door for the server: the dashboard under test
 * still reaches every fact through mc. */
function spineExec(sql: string) {
  const db = new Database(process.env["MC_SPINE"]!);
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    db.exec(sql);
  } finally {
    db.close();
  }
}

// A complete, valid 1×1 RGBA PNG — the inline render is asserted by the
// browser's own decode (naturalWidth), so the fixture must be a real image.
const PNG_1X1 = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
  "base64",
);

let smoke: Smoke;

beforeAll(async () => {
  smoke = await bootSmoke();
}, 60000);

afterAll(async () => {
  await teardown(smoke);
}, 30000);

test("open the tab, send ping, see the runner's reply render", async () => {
  const { page, server, scratch, browserLog } = smoke;
  await gotoReady(smoke);
  // The first page open prepares one conversation.
  await page.click('[data-testid="tab-collie"]');
  await page.waitForFunction(() => !(document.getElementById("send-body") as HTMLTextAreaElement).disabled);
  expect(await page.locator('[data-testid="session-row"]').count()).toBe(1);

  const selected = page.locator('[data-testid="session-row"].selected');
  try {
    await selected.waitFor({ timeout: 10000 });
  } catch {
    const errorBox = await page.locator('[data-testid="error"]').textContent();
    throw new Error(
      `no session appeared (error box: ${JSON.stringify(errorBox)}; browser log: ${browserLog.join(" | ")})`,
    );
  }
  const sessionID = (await selected.getAttribute("data-session")) ?? "";
  expect(sessionID).toMatch(/^h-[0-9a-f]+$/);
  // The session list carries the registry facts the tab asks for.
  expect(await selected.textContent()).toContain("active");

  await page.locator('[data-testid="send-body"]').fill("ping");
  await page.locator('[data-testid="send"]').click();
  try {
    await page
      .locator('[data-testid="collie-message"]')
      .filter({ hasText: "ping" })
      .first()
      .waitFor({ timeout: 10000 });
  } catch {
    const content = (await page.content().catch(() => "(unreadable)")).slice(0, 800);
    throw new Error(
      `ping never rendered (url ${page.url()}; content head ${JSON.stringify(content)}; browser log: ${browserLog.join(" | ")})`,
    );
  }

  // Play the woken runner: claim the turn, post the reply.
  const runJSON = join(scratch, "run.json");
  writeFileSync(runJSON, JSON.stringify({ run_id: sessionID, tier: "collie", role: "collie" }));
  let claimed: Record<string, unknown> | null = null;
  for (let i = 0; i < 20 && claimed === null; i++) {
    const res = await mc(["collie", "claim", sessionID], { MC_RUN_JSON: runJSON });
    claimed = (res["message"] as Record<string, unknown> | null) ?? null;
    if (claimed === null) {
      await Bun.sleep(250);
    }
  }
  if (claimed === null) {
    throw new Error("the sent turn never became claimable");
  }
  expect(claimed["body"]).toBe("ping");
  await mc(
    ["collie", "reply", sessionID, "--to", String(claimed["id"]), "--body", "collie ack"],
    { MC_RUN_JSON: runJSON },
  );

  // The view polls every second while the send owes a reply, so the reply
  // renders without a reload, attributed to Collie.
  const reply = page.locator('[data-testid="collie-message"]').filter({ hasText: "collie ack" });
  try {
    await reply.first().waitFor({ timeout: 15000 });
  } catch {
    const content = (await page.content().catch(() => "(unreadable)")).slice(0, 400);
    throw new Error(
      `the reply never rendered (url ${page.url()}; content head ${JSON.stringify(content)}; browser log: ${browserLog.join(" | ")})`,
    );
  }
  expect(await reply.first().textContent()).toContain("Collie");

  // The dashboard outbox drains to empty. The browser's own drain may still
  // be in flight after the reply renders (render precedes drain), so poll.
  let polled = -1;
  for (let i = 0; i < 40; i++) {
    const drained = await fetch(`${server.url}api/outbox/drain`, {
      method: "POST",
      headers: SMOKE_BEARER,
      signal: AbortSignal.timeout(10000),
    });
    polled = ((await drained.json()) as { polled: number }).polled;
    if (polled === 0) {
      break;
    }
    await Bun.sleep(250);
  }
  expect(polled).toBe(0);

  // End the session out-of-band, then resume it from the Collie tab.
  // Resume requires a registered native locator, which the real runner
  // registers on its first turn. Sending into an ended session must surface
  // the resume affordance (send disabled, resume visible, a sentence saying
  // so), not a raw error.
  await mc(
    ["run", "register-session", sessionID, "--native-ref", "smoke-native-1", "--file", "native.jsonl"],
    { MC_RUN_JSON: runJSON },
  );
  await mc(["collie", "end", sessionID, "--reason", "smoke end"]);

  const resume = page.locator('[data-testid="resume"]');
  try {
    await resume.waitFor({ timeout: 30000 });
  } catch {
    const content = (await page.content().catch(() => "(unreadable)")).slice(0, 600);
    throw new Error(
      `resume never appeared (url ${page.url()}; content head ${JSON.stringify(content)}; browser log: ${browserLog.join(" | ")})`,
    );
  }
  await expect(page.locator('[data-testid="send"]').isDisabled()).resolves.toBe(true);
  expect(await page.textContent('[data-testid="collie-hint"]')).toContain("resume it");
  await resume.click();
  await page.waitForFunction(
    () => !(document.getElementById("send") as HTMLButtonElement).disabled,
    undefined,
    { timeout: 15000 },
  );

  await page.locator('[data-testid="send-body"]').fill("ping again");
  await page.locator('[data-testid="send"]').click();
  let second: Record<string, unknown> | null = null;
  for (let i = 0; i < 20 && second === null; i++) {
    const res = await mc(["collie", "claim", sessionID], { MC_RUN_JSON: runJSON });
    second = (res["message"] as Record<string, unknown> | null) ?? null;
    if (second === null) {
      await Bun.sleep(250);
    }
  }
  if (second === null) {
    throw new Error("the post-resume turn never became claimable");
  }
  await mc(
    ["collie", "reply", sessionID, "--to", String(second["id"]), "--body", "collie ack after resume"],
    { MC_RUN_JSON: runJSON },
  );
  await page
    .locator('[data-testid="collie-message"]')
    .filter({ hasText: "collie ack after resume" })
    .first()
    .waitFor({ timeout: 15000 });
}, 120000);

// A tab open with no active session resumes the last session and starts no
// new one. The first test registered the session's native locator pair, so
// the resume succeeds and the row reads active again.
test("a tab open after the session ended resumes it and starts no new session", async () => {
  const { page, browserLog } = smoke;
  const selected = page.locator('[data-testid="session-row"].selected');
  const sessionID = (await selected.getAttribute("data-session")) ?? "";
  expect(sessionID).toMatch(/^h-[0-9a-f]+$/);
  await mc(["collie", "end", sessionID, "--reason", "smoke end before tab open"]);

  await page.click('[data-testid="tab-board"]');
  await page.waitForSelector('[data-testid="board-pane"]', { state: "visible" });
  await page.click('[data-testid="tab-collie"]');
  await page.waitForSelector('[data-testid="collie-pane"]', { state: "visible" });

  try {
    await page.waitForFunction(
      (id) => {
        const row = document.querySelector(`[data-testid="session-row"][data-session="${id}"]`);
        return row?.classList.contains("selected") === true && row.getAttribute("data-status") === "active";
      },
      sessionID,
      { timeout: 30000 },
    );
  } catch {
    const content = (await page.content().catch(() => "(unreadable)")).slice(0, 600);
    throw new Error(
      `the tab open never resumed the session (content head ${JSON.stringify(content)}; browser log: ${browserLog.join(" | ")})`,
    );
  }
  // The list still holds one session. That session is selected, and no error box shows.
  expect(await page.locator('[data-testid="session-row"]').count()).toBe(1);
  expect(await selected.getAttribute("data-session")).toBe(sessionID);
  expect(await selected.textContent()).toContain("active");
  expect(await page.isVisible('[data-testid="error"]')).toBe(false);
  await expect(page.locator('[data-testid="send"]').isDisabled()).resolves.toBe(false);
}, 60000);

// The tab shell in a real browser. What this can assert is
// switching and the empty-queue render — a POPULATED queue is out of reach
// here, because packet birth requires a `packaged` task completed against a
// live lease. The populated row render is covered in the fast lane.
test("the Review Queue tab activates and renders an empty queue", async () => {
  const { page } = smoke;
  await page.click('[data-testid="tab-review"]');

  await page.waitForSelector('[data-testid="review-pane"]', { state: "visible" });
  expect(await page.isVisible('[data-testid="collie-pane"]')).toBe(false);
  await page.waitForSelector('[data-testid="reader-empty"]:text-is("Nothing awaiting decision.")', { state: "visible" });
  expect(await page.locator('[data-testid="packet-row"]').count()).toBe(0);
  // With no open packet the reader is hidden, and the decision buttons are
  // absent: there is no packet to decide without reading one.
  expect(await page.isVisible('[data-testid="packet-reader"]')).toBe(false);
  expect(await page.isVisible('[data-testid="packet-decide"]')).toBe(false);

  // The ledger is a browser-side filter over the same read.
  await page.click('[data-testid="packet-view-ledger"]');
  await page.waitForFunction(() =>
    (document.querySelector('[data-testid="packets"]')?.textContent ?? "").includes("No approved packets yet."),
  );
  await page.click('[data-testid="packet-view-live"]');
  await page.waitForSelector('[data-testid="reader-empty"]:text-is("Nothing awaiting decision.")', { state: "visible" });

  // Switching back restores the Collie tab with its transcript intact — the
  // tab shell hides panes, it does not reset them.
  await page.click('[data-testid="tab-collie"]');
  await page.waitForSelector('[data-testid="collie-pane"]', { state: "visible" });
  expect(await page.isVisible('[data-testid="review-pane"]')).toBe(false);
  expect(await page.textContent('[data-testid="collie-transcript"]')).toContain("collie ack");
}, 30000);

test("the Board offers project setup after a Collie conversation without a Project", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-board").click();
  await page.getByTestId("board-add-project").waitFor();
  expect(await page.getByTestId("board-columns").isVisible()).toBe(false);
  expect(await page.getByTestId("board-project").getByRole("heading", { name: "Add your first Project" }).isVisible()).toBe(true);
  expect(await page.locator(".card.active").count()).toBe(0);
}, 30000);

test("Pipeline activity excludes the Collie conversation", async () => {
  const { page } = smoke;
  await page.getByTestId("tab-pipeline").click();
  await page.getByTestId("activity-pane").waitFor({ state: "visible" });
  expect(await page.getByTestId("activity-events").getByTestId("activity-row").count()).toBe(0);
  expect(await page.getByTestId("activity-errors").getByTestId("activity-row").count()).toBe(0);
  expect(await page.getByTestId("error").isVisible()).toBe(false);
}, 30000);

// The attachment claims through the byte endpoint (PAGES.md page 2): an
// allowlisted image renders inline, everything else is a download link. The
// conversation and its inbound turn are made through mc; the typed claim rows
// and the published tree files are the fixture (see spineExec) — and the tree
// itself is prepared by the production verb, so the byte read walks exactly
// the directories production materializes.
test("an image claim renders inline and a document claim links as a download", async () => {
  const { page, server, home, browserLog } = smoke;

  const started = await mc(["collie", "start", "--from", "discord:attach-chan"]);
  const session = started["session_id"] as string;
  const sent = await mc(["collie", "send", session, "--from", "discord:attach-chan", "--body", "two files attached"]);
  const messageID = sent["message_id"] as number;

  const pdf = Buffer.from("%PDF-1.4 smoke attachment body\n");
  const pngSha = createHash("sha256").update(PNG_1X1).digest("hex");
  const pdfSha = createHash("sha256").update(pdf).digest("hex");
  await mc(["__attachment-tree-prepare", session]);
  // A live object row records its published file's identity evidence
  // (device, inode, type) — the schema refuses a live row without it.
  const evidence: Record<string, { dev: number; ino: number }> = {};
  for (const [sha, bytes] of [
    [pngSha, PNG_1X1],
    [pdfSha, pdf],
  ] as const) {
    const shard = join(home, "attachments", session, "in", "published", "sha256", sha.slice(0, 2));
    mkdirSync(shard);
    chmodSync(shard, 0o700);
    writeFileSync(join(shard, sha), bytes, { mode: 0o600 });
    const stat = statSync(join(shard, sha));
    evidence[sha] = { dev: stat.dev, ino: stat.ino };
  }
  await spineExec(`
    INSERT INTO attachment_objects (session_id,direction,sha256,size,state,device,inode,file_type) VALUES
      ('${session}','in','${pngSha}',${PNG_1X1.length},'live','${evidence[pngSha]!.dev}','${evidence[pngSha]!.ino}','regular'),
      ('${session}','in','${pdfSha}',${pdf.length},'live','${evidence[pdfSha]!.dev}','${evidence[pdfSha]!.ino}','regular');
    INSERT INTO conversation_attachment_refs
      (message_id,ordinal,session_id,direction,sha256,size,media_type,display_name) VALUES
      (${messageID},1,'${session}','in','${pngSha}',${PNG_1X1.length},'image/png','capture.png'),
      (${messageID},2,'${session}','in','${pdfSha}',${pdf.length},'application/pdf','notes.pdf');
  `);

  await page.click('[data-testid="tab-collie"]');
  await page.waitForSelector('[data-testid="collie-pane"]', { state: "visible" });
  const row = page.locator(`[data-testid="session-row"][data-session="${session}"]`);
  // The session list refreshes on the lazy poll, so the row may take one tick.
  await row.waitFor({ timeout: 25000 });
  await row.click();

  const inline = page.locator('[data-testid="attachment-inline"] img');
  try {
    await inline.waitFor({ timeout: 15000 });
  } catch {
    const content = (await page.content().catch(() => "(unreadable)")).slice(0, 600);
    throw new Error(
      `the inline attachment never rendered (content head ${JSON.stringify(content)}; browser log: ${browserLog.join(" | ")})`,
    );
  }
  // The browser DECODED the bytes the endpoint served — a broken image (401,
  // 404, wrong content type) has naturalWidth 0 and fails here.
  await page.waitForFunction(
    () => {
      const img = document.querySelector('[data-testid="attachment-inline"] img');
      return img instanceof HTMLImageElement && img.complete && img.naturalWidth > 0;
    },
    undefined,
    { timeout: 15000 },
  );
  // The bytes ride the authorized fetch; the DOM carries a blob URL and the
  // claim's endpoint stays on data-url.
  expect(((await inline.getAttribute("src")) ?? "").startsWith("blob:")).toBe(true);
  expect(await page.locator('[data-testid="attachment-inline"]').getAttribute("data-url")).toBe(
    `/api/sessions/${session}/attachments/in/${pngSha}/${PNG_1X1.length}`,
  );

  const download = page.locator('[data-testid="attachment-download"]');
  await download.waitFor({ timeout: 5000 });
  const href = (await download.getAttribute("data-url")) ?? "";
  expect(href).toBe(`/api/sessions/${session}/attachments/in/${pdfSha}/${pdf.length}`);
  expect(await download.textContent()).toContain("notes.pdf");

  // The download face over the wire: octet-stream with the recorded name,
  // never the claim's own type in-origin.
  const res = await fetch(new URL(href, server.url), {
    headers: SMOKE_BEARER,
    signal: AbortSignal.timeout(10000),
  });
  expect(res.status).toBe(200);
  expect(res.headers.get("content-type")).toBe("application/octet-stream");
  expect(res.headers.get("content-disposition")).toContain('filename="notes.pdf"');
  expect(res.headers.get("content-length")).toBe(String(pdf.length));
  expect(Buffer.from(await res.arrayBuffer()).equals(pdf)).toBe(true);

  // The fence holds end-to-end: an identity no spine row confirms is 404.
  const unknown = await fetch(
    new URL(`/api/sessions/${session}/attachments/in/${"0".repeat(64)}/${pdf.length}`, server.url),
    { headers: SMOKE_BEARER, signal: AbortSignal.timeout(10000) },
  );
  expect(unknown.status).toBe(404);
}, 90000);
