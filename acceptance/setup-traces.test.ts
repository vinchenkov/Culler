import { expect, test } from "bun:test";
import { appendFile, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { createHash } from "node:crypto";
import { createTraceLane, type TraceSession } from "../resident/src/trace-lane";
import { agentsViewClient } from "../resident/src/agentsview";
import { makeTestLogger } from "../runner/shared/log";
import type { SetupTraceCandidate } from "../resident/src/setup-traces";

test("the pinned AgentsView imports setup traces, incremental bytes, and restored archives", async () => {
  const binary = process.env.CULLER_AGENTSVIEW_BIN;
  if (!binary) throw new Error("Set CULLER_AGENTSVIEW_BIN to the pinned AgentsView executable.");
  const pin = await Bun.file(new URL("../mc/deployment/agentsview.json", import.meta.url)).json();
  expect(createHash("sha256").update(await readFile(binary)).digest("hex")).toBe(pin.executable.sha256);

  const home = await mkdtemp(join(tmpdir(), "setup-agentsview-acceptance-"));
  const token = "f1".repeat(32);
  const data = join(home, "agentsview", "data");
  const reservation = Bun.serve({ hostname: "127.0.0.1", port: 0, fetch: () => new Response() });
  const port = reservation.port!;
  reservation.stop(true);
  const bind = `127.0.0.1:${port}`;
  const origin = `http://${bind}`;
  const claude = "11111111-1111-4111-8111-111111111111";
  const codex = "22222222-2222-4222-8222-222222222222";
  const pending: SetupTraceCandidate[] = [
    {
      kind: "setup", id: "setup-fixture", session_path: "environment-runs/setup-fixture/session",
      format: "claude", display_name: "Fixture · setup · setup-fixture", ended: false,
    },
    {
      kind: "setup", id: "validate-fixture", session_path: "environment-runs/validate-fixture/session",
      format: "codex", display_name: "Fixture · validate · validate-fixture", ended: true,
    },
  ];
  const files = ["native.jsonl", `2026/10/12/rollout-2026-10-12T12-00-00-${codex}.jsonl`];
  const stamp = (n: number) => `2026-10-12T12:00:${String(n).padStart(2, "0")}.000Z`;
  const claudeLine = (n: number) => JSON.stringify({
    type: n % 2 ? "assistant" : "user", sessionId: claude, uuid: `fixture-${n}`,
    cwd: "/workspace/source", timestamp: stamp(n),
    message: { role: n % 2 ? "assistant" : "user", content: `Synthetic setup message ${n}.` },
  }) + "\n";
  const codexLine = (n: number) => JSON.stringify({
    timestamp: stamp(n), type: "response_item",
    payload: {
      type: "message", role: n % 2 ? "assistant" : "user",
      content: [{ type: n % 2 ? "output_text" : "input_text", text: `Synthetic validation message ${n}.` }],
    },
  }) + "\n";
  const sources = pending.map((row, index) => join(home, row.session_path, files[index]!));
  for (const source of sources) await mkdir(join(source, ".."), { recursive: true });
  await writeFile(sources[0]!, claudeLine(0) + claudeLine(1));
  await writeFile(sources[1]!, JSON.stringify({
    timestamp: stamp(0), type: "session_meta",
    payload: { id: codex, cwd: "/workspace/source", originator: "codex_exec", source: "exec" },
  }) + "\n" + codexLine(0) + codexLine(1));

  const registered: TraceSession[] = [];
  const runMc = async (argv: string[]) => {
    if (argv[0] === "__trace-register-setup") {
      const row = pending.find(row => row.id === argv[1])!;
      registered.push({
        ...row, native_session_ref: argv[2]!, trace_filename: argv[3]!,
        agentsview_session: row.format === "codex" ? `codex:${argv[2]}` : argv[2]!,
      });
      return { exitCode: 0, stdout: "{}", stderr: "" };
    }
    return {
      exitCode: 0,
      stdout: JSON.stringify({
        sessions: registered,
        setup_pending: pending.filter(row => !registered.some(session => session.id === row.id)),
      }),
      stderr: "",
    };
  };
  const config = async () => {
    await mkdir(data, { recursive: true });
    await mkdir(join(home, "empty-home"), { recursive: true });
    await writeFile(join(data, "config.toml"), `host = "127.0.0.1"
port = ${port}
require_auth = true
auth_token = "${token}"
disable_update_check = true
archive_content = "full"
disabled_agents = ${JSON.stringify(pin.disabled_agents)}
[terminal]
mode = "clipboard"
[agents.claude]
dirs = [${JSON.stringify(join(home, "agentsview/traces/claude/projects"))}]
[agents.codex]
dirs = [${JSON.stringify(join(home, "agentsview/traces/codex/sessions"))}]
`);
  };
  const client = agentsViewClient(bind, { token: async () => token });
  const log = makeTestLogger("resident");
  const makeLane = () => createTraceLane({
    runMc, log, mcHome: home, agentsView: client, now: Date.now, schedule: setTimeout,
    cancel: handle => clearTimeout(handle as ReturnType<typeof setTimeout>),
  });
  let lane = makeLane();
  let child: ReturnType<typeof Bun.spawn> | undefined;
  const start = async () => {
    await config();
    child = Bun.spawn([
      binary, "serve", "--host", "127.0.0.1", "--port", String(port), "--no-browser", "--no-update-check",
    ], {
      cwd: home,
      env: {
        HOME: join(home, "empty-home"), PATH: "/usr/bin:/bin", AGENTSVIEW_DATA_DIR: data,
        AGENTSVIEW_DISABLE_UPDATE_CHECK: "1", AGENTSVIEW_TELEMETRY_ENABLED: "0",
        HTTPS_PROXY: "http://127.0.0.1:9", HTTP_PROXY: "http://127.0.0.1:9",
      },
      stdout: "ignore", stderr: "ignore",
    });
  };
  const stop = async () => {
    if (child) {
      child.kill("SIGTERM");
      await child.exited;
      child = undefined;
    }
  };
  const session = async (id: string) => {
    const response = await fetch(`${origin}/api/v1/sessions/${encodeURIComponent(id).replaceAll("%3A", ":")}`, {
      headers: { authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(3000),
    });
    return response.ok ? await response.json() : null;
  };
  const until = async (check: () => Promise<boolean>) => {
    const deadline = Date.now() + 30_000;
    while (Date.now() < deadline) {
      await lane.copyPass();
      await lane.reconcilePass();
      try {
        if (await check()) return;
      } catch {}
      await Bun.sleep(250);
    }
    throw new Error(`AgentsView acceptance timed out. ${JSON.stringify(log.records.slice(-4))}`);
  };

  try {
    await lane.copyPass();
    await lane.copyPass();
    await start();
    await until(async () => {
      const a = await session(claude);
      const b = await session(`codex:${codex}`);
      return a?.display_name === pending[0]!.display_name && b?.display_name === pending[1]!.display_name;
    });
    expect((await fetch(`${origin}/api/ping`)).status).toBe(401);
    for (const path of [`/sessions/${claude}?msg=last`, `/sessions/codex/${codex}?msg=last`]) {
      expect((await fetch(origin + path, { headers: { authorization: `Bearer ${token}` } })).status).toBe(200);
    }

    await appendFile(sources[0]!, claudeLine(2) + claudeLine(3));
    await appendFile(sources[1]!, codexLine(2) + codexLine(3));
    await until(async () =>
      Date.parse((await session(claude))?.ended_at) >= Date.parse(stamp(3)) &&
      Date.parse((await session(`codex:${codex}`))?.ended_at) >= Date.parse(stamp(3)));

    await lane.stop();
    await stop();
    await rm(join(home, "agentsview"), { recursive: true });
    lane = makeLane();
    await lane.copyPass();
    await lane.copyPass();
    await start();
    await until(async () => {
      const a = await session(claude);
      const b = await session(`codex:${codex}`);
      return a?.display_name === pending[0]!.display_name && b?.display_name === pending[1]!.display_name &&
        Date.parse(a.ended_at) >= Date.parse(stamp(3)) && Date.parse(b.ended_at) >= Date.parse(stamp(3));
    });
  } finally {
    await lane.stop();
    await stop();
    await rm(home, { recursive: true, force: true });
  }
}, 120_000);
