// The dashboard authenticates API requests before commands or native dialogs.
// Browser assets remain public so the page can request the operator token.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { handleApi, type ApiDeps } from "./api";
import { accessPage } from "./agentsview-pages";
import { accessBody, accessError, agentsViewDestination, createAgentsViewProxy } from "./agentsview-proxy";

export const DEFAULT_BIND = "127.0.0.1:7333";

const LOOPBACK_HOSTS = new Set(["127.0.0.1", "::1", "localhost"]);

// The dashboard page frames only its own origin. A report frame that
// navigates to another origin loads nothing.
export const PAGE_POLICY = "frame-src 'self'";

// Every UI module needs its entry here: a missing one 404s the import and
// kills the whole app.js module graph (the smokes catch it; unit tests do
// not).
const UI_MODULES = [
  "app.js", "requests.js", "router.js", "markdown.js", "paths.js", "stages.js",
  "format.js", "errors.js", "board.js", "boardwrites.js", "homiefmt.js", "review.js", "evaluation.js", "activity.js",
  "pipeline.js", "systemhealth.js", "topology.js", "flowmap.js", "agentprofiles.js", "projects.js", "projectdelete.js", "customization.js", "vision.js", "conversation.js", "configs.js", "discord.js", "logs.js", "dom.js", "components.js", "taskdialog.js", "viewers.js",
  "changes.js", "changeview.js", "theme.js", "commandbar.js", "packetchat.js", "diagnosticchat.js", "environment.js", "setup-journal.js", "fileview.js",
];

/** The local Pierre Diffs and Pierre Trees modules and their license notices,
 * as the provenance record of the build lists them. */
export function pierreFiles(record: string): Record<string, { path: string; type: string }> {
  const provenance = JSON.parse(record) as { files: { file: string }[]; packages: { license_file: string }[] };
  const name = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;
  const entries: [string, { path: string; type: string }][] = [];
  for (const { file } of provenance.files) {
    if (!name.test(file) || !file.endsWith(".js")) throw new Error(`The Pierre provenance names an invalid module: ${file}`);
    entries.push([`/pierre/${file}`, { path: `ui/pierre/${file}`, type: "text/javascript; charset=utf-8" }]);
  }
  for (const { license_file: file } of provenance.packages) {
    if (!name.test(file) || !file.endsWith(".txt")) throw new Error(`The Pierre provenance names an invalid notice: ${file}`);
    entries.push([`/pierre/${file}`, { path: `ui/pierre/${file}`, type: "text/plain; charset=utf-8" }]);
  }
  return Object.fromEntries(entries);
}

// The dashboard typefaces and their license notices.
const FONT_FILES: Record<string, { path: string; type: string }> = {
  "/fonts/fraunces.woff2": { path: "ui/fonts/fraunces.woff2", type: "font/woff2" },
  "/fonts/figtree.woff2": { path: "ui/fonts/figtree.woff2", type: "font/woff2" },
  "/fonts/LICENSE-fraunces.txt": { path: "ui/fonts/LICENSE-fraunces.txt", type: "text/plain; charset=utf-8" },
  "/fonts/LICENSE-figtree.txt": { path: "ui/fonts/LICENSE-figtree.txt", type: "text/plain; charset=utf-8" },
};

const STATIC_FILES: Record<string, { path: string; type: string }> = {
  "/": { path: "ui/index.html", type: "text/html; charset=utf-8" },
  "/landscape.png": { path: "ui/landscape.png", type: "image/png" },
  "/landscape-dark.png": { path: "ui/landscape-dark.png", type: "image/png" },
  "/style.css": { path: "ui/style.css", type: "text/css; charset=utf-8" },
  ...FONT_FILES,
  ...Object.fromEntries(
    UI_MODULES.map((name) => [
      `/${name}`,
      { path: `ui/${name}`, type: "text/javascript; charset=utf-8" },
    ]),
  ),
  ...pierreFiles(readFileSync(join(import.meta.dir, "./ui/pierre/provenance.json")).toString("utf8")),
};

export interface DashboardServerOpts {
  /** host:port; defaults to 127.0.0.1:7333. Port 0 = ephemeral (tests). */
  bind?: string;
  authToken: string;
  chooseFolder?: () => Promise<string | null>;
}

export interface DashboardServerHandle {
  url: string;
  port: number;
  stop(): void;
}

export function parseBind(bind: string, name = "dashboard bind"): { hostname: string; port: number } {
  const at = bind.lastIndexOf(":");
  const rawPort = at === -1 ? "" : bind.slice(at + 1);
  const port = Number(rawPort);
  if (at === -1 || rawPort === "" || !Number.isInteger(port) || port < 0 || port > 65535) {
    throw new Error(`${name} must be host:port, got ${JSON.stringify(bind)}`);
  }
  const hostname = bind.slice(0, at).replace(/^\[|\]$/g, "");
  if (hostname === "") {
    throw new Error(`${name} must name a host, got ${JSON.stringify(bind)}`);
  }
  return { hostname, port };
}

/** The hostname a browser may name in Host/Origin for this server: the bind
 * host plus the loopback spellings (a loopback bind is reachable as any of
 * them). Anything else is a rebound DNS name or a foreign origin. */
function hostAllowed(rawHost: string | null, bindHost: string): boolean {
  if (rawHost === null) {
    return false;
  }
  const host = rawHost.replace(/:\d+$/, "").replace(/^\[|\]$/g, "").toLowerCase();
  return host === bindHost.toLowerCase() || LOOPBACK_HOSTS.has(host);
}

export async function chooseProjectFolder(): Promise<string | null> {
  if (process.platform !== "darwin") throw new Error("The folder chooser requires macOS. Enter the folder path instead.");
  const child = Bun.spawn(["/usr/bin/osascript", "-e", 'POSIX path of (choose folder with prompt "Choose a Project folder")'], {
    stdout: "pipe", stderr: "pipe",
  });
  const timer = setTimeout(() => child.kill(), 120_000);
  try {
    const [output, error, code] = await Promise.all([
      new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited,
    ]);
    if (code === 0) return output.trimEnd();
    if (error.includes("(-128)")) return null;
    throw new Error("The folder chooser could not finish. Retry or enter the folder path.");
  } finally {
    clearTimeout(timer);
  }
}

export function startDashboardServer(
  opts: DashboardServerOpts,
  deps: ApiDeps,
): DashboardServerHandle {
  const { hostname, port } = parseBind(opts.bind ?? DEFAULT_BIND);
  if (!opts.authToken) {
    throw new Error(
      `refusing bind ${hostname}: an auth token is required on every bind, loopback included (fail-closed)`,
    );
  }
  let choosingFolder = false;
  const agentsview = createAgentsViewProxy(deps.mc);
  const server = Bun.serve({
    hostname,
    port,
    async fetch(req, server) {
      if (!hostAllowed(req.headers.get("host"), hostname)) {
        return Response.json(
          { error: { code: "forbidden", message: "request names a foreign host" } },
          { status: 403 },
        );
      }
      const url = new URL(req.url);
      const origin = req.headers.get("origin");
      if (origin !== null) {
        let originHost: string | null = null;
        try {
          originHost = new URL(origin).hostname;
        } catch {
          originHost = null;
        }
        if (originHost === null || !hostAllowed(originHost, hostname)) {
          return Response.json(
            { error: { code: "forbidden", message: "cross-origin requests are refused" } },
            { status: 403 },
          );
        }
      }
      if (url.pathname.startsWith("/api/")) {
        if (req.headers.get("authorization") !== `Bearer ${opts.authToken}`) {
          return Response.json(
            { error: { code: "unauthorized", message: "missing or wrong bearer token" } },
            { status: 401 },
          );
        }
      }
      if (url.pathname === "/agentsview/open" && req.method === "GET") {
        const path = agentsViewDestination(url.searchParams.get("path"));
        return path === null ? new Response("Invalid trace link", { status: 400 }) : accessPage(path);
      }
      if (url.pathname === "/api/agentsview/access") {
        if (req.method !== "POST") return new Response("Method not allowed", { status: 405 });
        server.timeout(req, 25);
        if (origin !== null && origin !== url.origin || req.headers.get("sec-fetch-site") === "cross-site") {
          return new Response("Forbidden", { status: 403 });
        }
        try {
          const body = JSON.parse(await accessBody(req));
          const grant = await agentsview.grant(body?.path, url.origin);
          return Response.json(grant, { headers: { "cache-control": "no-store" } });
        } catch (error) { return accessError(error); }
      }
      if (url.pathname === "/api/projects/choose-folder") {
        if (req.method !== "POST") return new Response("method not allowed", { status: 405 });
        if (choosingFolder) return Response.json({ error: { message: "A folder chooser is already open on this Mac." } }, { status: 409 });
        choosingFolder = true;
        server.timeout(req, 0);
        try {
          return Response.json({ path: await (opts.chooseFolder ?? chooseProjectFolder)() });
        } catch (error) {
          return Response.json({ error: { message: error instanceof Error ? error.message : "The folder chooser failed." } }, { status: 503 });
        } finally {
          choosingFolder = false;
        }
      }
      const api = await handleApi(req, url, deps);
      if (api !== null) {
        return api;
      }
      const entry = STATIC_FILES[url.pathname];
      if (entry === undefined || req.method !== "GET") {
        return new Response("not found", { status: 404 });
      }
      const file = Bun.file(`${import.meta.dir}/${entry.path}`);
      if (!(await file.exists())) {
        return new Response("not found", { status: 404 });
      }
      return new Response(file, { headers: {
        "content-type": entry.type,
        ...(url.pathname === "/" ? { "content-security-policy": PAGE_POLICY } : {}),
      } });
    },
  });
  return {
    url: `http://${hostname}:${server.port}/`,
    port: server.port as number,
    stop() {
      agentsview.stop();
      server.stop(true);
    },
  };
}
