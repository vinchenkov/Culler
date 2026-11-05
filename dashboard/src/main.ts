// main.ts — dashboard entry. Wiring only: reads --config <path>
// (JSON: { "mc_bin": "...", "bind"?: "host:port", "wiki_bind"?: "host:port",
// "wiki_key"?: "<64 hex>", "auth_token": "..." }), builds the real mc seam,
// and serves the dashboard and the wiki exports. Stateless by design: on
// death the init system restarts it, it re-reads, and the browser
// reconnects. LaunchAgent generation belongs to install/onboard, not here.

import { mkdirSync } from "node:fs";
import { makeLogger, parseLevel, type Logger } from "../../runner/shared/log";
import { openFileSink } from "../../runner/shared/log-sink";
import { makeGenRef } from "./api";
import { openAttachmentFile } from "./hostfile";
import { makeMcRunner, type McCompletionRecord } from "./mc";
import { nudgeResident, residentSocketPath } from "./resident-nudge";
import { DEFAULT_BIND, startDashboardServer } from "./server";
import { DEFAULT_WIKI_BIND, startWikiServer, WIKI_KEY } from "./wiki-server";

export interface DashboardConfig {
  mc_bin: string;
  bind?: string;
  wiki_bind?: string;
  /** The secret path prefix of the wiki address. Without it, the wiki
   * address does not start. */
  wiki_key?: string;
  auth_token: string;
}

export function observeCommand(log: Logger, record: McCompletionRecord): void {
  const context = { ...record };
  if (record.outcome !== "completed" || record.code !== 0) {
    log.warn("dash.mc_completed", "The dashboard command ended with an error.", context);
  } else {
    log.info("dash.mc_completed", "The dashboard command completed.", context);
  }
}

export function parseConfig(raw: string): DashboardConfig {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error("dashboard config must be a JSON object");
  }
  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("dashboard config must be a JSON object");
  }
  const cfg = parsed as Record<string, unknown>;
  if (typeof cfg["mc_bin"] !== "string" || cfg["mc_bin"] === "") {
    throw new Error('dashboard config requires "mc_bin"');
  }
  if (cfg["bind"] !== undefined && typeof cfg["bind"] !== "string") {
    throw new Error('dashboard config "bind" must be a string');
  }
  if (cfg["wiki_bind"] !== undefined && typeof cfg["wiki_bind"] !== "string") {
    throw new Error('dashboard config "wiki_bind" must be a string');
  }
  if (cfg["wiki_key"] !== undefined && (typeof cfg["wiki_key"] !== "string" || !WIKI_KEY.test(cfg["wiki_key"]))) {
    throw new Error('dashboard config "wiki_key" must be 64 lowercase hexadecimal characters');
  }
  if (typeof cfg["auth_token"] !== "string" || cfg["auth_token"] === "") {
    throw new Error('dashboard config requires "auth_token" — every bind is token-gated');
  }
  return {
    mc_bin: cfg["mc_bin"],
    bind: cfg["bind"] as string | undefined,
    wiki_bind: cfg["wiki_bind"] as string | undefined,
    wiki_key: cfg["wiki_key"] as string | undefined,
    auth_token: cfg["auth_token"],
  };
}

if (import.meta.main) {
  const flag = process.argv.indexOf("--config");
  const configPath = flag === -1 ? undefined : process.argv[flag + 1];
  if (configPath === undefined) {
    console.error("usage: bun src/main.ts --config <path>");
    process.exit(2);
  }
  const cfg = parseConfig(await Bun.file(configPath).text());
  // Same MC_HOME resolution the resident uses, from the same launchd
  // environment: `mc __onboard supervision` sets it for the resident and dashboard units.
  const mcHome = process.env["MC_HOME"] ?? `${process.env["HOME"]}/.culler`;
  // Owned file, not the launchd redirect — see the resident's note: launchd
  // holds StandardErrorPath open, so nothing this process does can rotate it.
  // Before this the dashboard's seam had no prefix and no timestamp at all, so
  // a full day of traffic left four untimestamped boot banners and nothing else.
  // Created here for the reason the resident states: the sink treats a missing
  // directory as a state and writes nothing, which is right inside a container
  // and silently wrong for a process that owns this home.
  const logsDir = `${mcHome}/logs`;
  mkdirSync(logsDir, { recursive: true, mode: 0o700 });
  const log = makeLogger({
    comp: "dashboard",
    level: parseLevel(process.env["MC_LOG_LEVEL"]),
    sink: openFileSink({ dir: logsDir, name: "dashboard.ndjson" }),
  });
  const server = startDashboardServer(
    { bind: cfg.bind ?? DEFAULT_BIND, authToken: cfg.auth_token },
    {
      mc: makeMcRunner(cfg.mc_bin, {
        onComplete: (record) => observeCommand(log, record),
      }),
      genRef: makeGenRef(),
      log,
      readRender: (hostPath) => Bun.file(hostPath).text(),
      readArtifact: (hostPath) => Bun.file(hostPath).bytes(),
      openAttachment: openAttachmentFile,
      nudgeResident: (sessionId) => nudgeResident(residentSocketPath(mcHome), sessionId),
    },
  );
  // Both channels, deliberately, and only here. The structured record is what
  // `mc logs dashboard --stream events` reads, and emitting it at boot is also
  // what makes the file exist at all on a deployment that never fails an ack.
  // The stderr line stays because it is this process's only handshake with an
  // operator who started it by hand and needs the URL on the terminal.
  log.info("boot.started", "dashboard serving", { url: server.url });
  console.error(`dashboard serving on ${server.url}`);
  // A wiki address that cannot bind leaves the dashboard available. The
  // deployment check reports the wiki address that does not answer.
  try {
    if (cfg.wiki_key === undefined) throw new Error('the dashboard config holds no "wiki_key"');
    const wiki = startWikiServer({ bind: cfg.wiki_bind ?? DEFAULT_WIKI_BIND, home: mcHome, key: cfg.wiki_key, log });
    log.info("boot.started", "wiki serving", { url: wiki.url });
    console.error(`wiki serving on ${wiki.url}`);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    log.error("dash.wiki_failed", `the wiki address did not start: ${message}`);
    console.error(`the wiki address did not start: ${message}`);
  }
}
