// The wiki server is the second listener of the dashboard process. It serves
// the wiki exports under MC_HOME/wiki at a separate loopback origin, so an
// exported page never runs at the dashboard origin and never sees its token.
// Each agent container can reach the loopback interface of the host, so the
// server answers only below a secret path prefix, the wiki key.

import { timingSafeEqual } from "node:crypto";
import { readFileSync } from "node:fs";
import { lstat, readFile } from "node:fs/promises";
import { extname, join } from "node:path";

import type { Logger } from "../../runner/shared/log";
import { parseBind } from "./server";

export const DEFAULT_WIKI_BIND = "127.0.0.1:7335";

export const WIKI_POLICY = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

const WIKI_HEADERS = {
  "x-culler-service": "wiki",
  "content-security-policy": WIKI_POLICY,
  "x-content-type-options": "nosniff",
  "referrer-policy": "no-referrer",
  "cache-control": "no-store",
};

export interface WikiLibrary {
  package: string;
  version: string;
  file: string;
  source: string;
  integrity: string;
  license: string;
  license_file: string;
}

export function parseProvenance(raw: string): WikiLibrary[] {
  const parsed = JSON.parse(raw) as { libraries?: unknown };
  if (!Array.isArray(parsed?.libraries) || parsed.libraries.length === 0) {
    throw new Error("the OpenWiki provenance record must list the libraries");
  }
  const files = new Set<string>();
  return parsed.libraries.map((entry: Record<string, unknown>) => {
    for (const key of ["package", "version", "file", "source", "integrity", "license", "license_file"]) {
      if (typeof entry?.[key] !== "string" || entry[key] === "") {
        throw new Error(`the OpenWiki provenance record requires "${key}" for each library`);
      }
    }
    const library = entry as unknown as WikiLibrary;
    if (!/^[a-z0-9][a-z0-9.-]*\.js$/.test(library.file) || files.has(library.file)) {
      throw new Error(`the OpenWiki provenance record names an unsafe or repeated file ${JSON.stringify(library.file)}`);
    }
    files.add(library.file);
    if (!/^LICENSE-[a-z0-9-]+$/.test(library.license_file)) {
      throw new Error(`the OpenWiki provenance record names an unsafe license file ${JSON.stringify(library.license_file)}`);
    }
    if (!/^sha384-[A-Za-z0-9+/]+={0,2}$/.test(library.integrity)) {
      throw new Error(`the OpenWiki provenance record requires a sha384 integrity value for ${library.package}`);
    }
    if (!library.source.startsWith(`https://cdn.jsdelivr.net/npm/${library.package}@${library.version}/`)) {
      throw new Error(`the OpenWiki provenance source of ${library.package} must name its package and version`);
    }
    return library;
  });
}

export const WIKI_LIBRARIES_ROOT = join(import.meta.dir, "openwiki");
export const WIKI_LIBRARIES = parseProvenance(String(readFileSync(join(import.meta.dir, "./openwiki/provenance.json"))));

function attribute(tag: string, name: string): { text: string; value: string } | null {
  const match = tag.match(new RegExp(`\\s${name}="([^"]*)"`));
  return match ? { text: match[0], value: match[1]! } : null;
}

const REMOTE = /^(?:[a-z][a-z0-9+.-]*:)?\/\//i;

/** The exported page with each matching library reference pointed at its
 * local copy below the wiki key and without the Google Fonts links. A
 * reference must match the provenance source and integrity value exactly.
 * Other bytes stay the same. `remote` receives each network reference that
 * stays in the page. */
export function rewriteExportPage(
  html: string, key: string, libraries: WikiLibrary[] = WIKI_LIBRARIES, remote: (reference: string) => void = () => {},
): string {
  const bySource = new Map(libraries.map((library) => [library.source, library]));
  return html
    .replace(/<script\b[^>]*>/g, (tag) => {
      const src = attribute(tag, "src");
      const library = src ? bySource.get(src.value) : undefined;
      if (!src || !library || attribute(tag, "integrity")?.value !== library.integrity) {
        if (src && REMOTE.test(src.value)) remote(src.value);
        return tag;
      }
      return tag.replace(src.text, src.text.replace(src.value, `/${key}/_openwiki/${library.file}`));
    })
    .replace(/<link\b[^>]*>/g, (tag) => {
      const href = attribute(tag, "href")?.value ?? "";
      if (/^https:\/\/fonts\.(?:googleapis|gstatic)\.com(?:\/|$)/.test(href)) return "";
      if (REMOTE.test(href)) remote(href);
      return tag;
    });
}

/** A browser names the wiki origin as a loopback host with the bound port.
 * Any other Host value is a rebound DNS name or a foreign origin. */
export function wikiHostAllowed(rawHost: string | null, port: number): boolean {
  const match = rawHost?.toLowerCase().match(/^(?:127\.0\.0\.1|localhost|\[::1\]):(\d+)$/);
  return !!match && Number(match[1]) === port;
}

const NAME = "[A-Za-z0-9][A-Za-z0-9._-]*";
const LIBRARY_PATH = /^\/_openwiki\/([^/]+)$/;
const PROJECT_PATH = new RegExp(`^/(${NAME})(?:(/)(${NAME})?)?$`);
const KEY_PATH = /^\/([^/]*)(\/.*)$/;

/** A wiki key: 64 lowercase hexadecimal characters. */
export const WIKI_KEY = /^[0-9a-f]{64}$/;

/** Compares a path segment with the wiki key in constant time. */
export function wikiKeyMatches(segment: string, key: string): boolean {
  const given = Buffer.from(segment);
  const wanted = Buffer.from(key);
  if (given.length !== wanted.length) {
    timingSafeEqual(wanted, wanted);
    return false;
  }
  return timingSafeEqual(given, wanted);
}

const EXPORT_DIRECTORY = /^[0-9a-f]{40}(?:[0-9a-f]{24})?$/;

const TYPES: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
};

function reply(body: BodyInit | null, status: number, headers: Record<string, string> = {}): Response {
  return new Response(body, { status, headers: { ...WIKI_HEADERS, ...headers } });
}

const notFound = () => reply("not found", 404, { "content-type": "text/plain; charset=utf-8" });

async function regularFile(path: string): Promise<boolean> {
  try {
    return (await lstat(path)).isFile();
  } catch {
    return false;
  }
}

/** The folder of the current export of one Project. The export record names
 * it. A Project without a valid record or folder has no export. */
export async function currentExport(home: string, project: string): Promise<string | null> {
  const root = join(home, "wiki", project);
  let record: { project?: unknown; directory?: unknown };
  try {
    record = JSON.parse(await readFile(join(root, "export.json"), "utf8"));
  } catch {
    return null;
  }
  if (record?.project !== project || typeof record.directory !== "string" || !EXPORT_DIRECTORY.test(record.directory)) {
    return null;
  }
  const folder = join(root, record.directory);
  try {
    return (await lstat(folder)).isDirectory() ? folder : null;
  } catch {
    return null;
  }
}

export async function handleWikiRequest(
  req: Request, home: string, port: number, key: string,
  remote: (project: string, reference: string) => void = () => {},
): Promise<Response> {
  if (!wikiHostAllowed(req.headers.get("host"), port)) {
    return reply("forbidden", 403, { "content-type": "text/plain; charset=utf-8" });
  }
  if (req.method !== "GET" && req.method !== "HEAD") {
    return reply("method not allowed", 405, { allow: "GET, HEAD", "content-type": "text/plain; charset=utf-8" });
  }
  // Each address is /<key>/<project>/..., or /<key>/_openwiki/<library>.
  const keyed = new URL(req.url).pathname.match(KEY_PATH);
  if (!keyed || !wikiKeyMatches(keyed[1]!, key)) return notFound();
  const path = keyed[2]!;
  const library = path.match(LIBRARY_PATH);
  if (library) {
    const entry = WIKI_LIBRARIES.find((item) => item.file === library[1]);
    if (!entry) return notFound();
    return reply(Bun.file(join(WIKI_LIBRARIES_ROOT, entry.file)), 200, { "content-type": TYPES[".js"]! });
  }
  const route = path.match(PROJECT_PATH);
  if (!route) return notFound();
  const [, project, slash, name] = route;
  const folder = await currentExport(home, project!);
  if (folder === null) return notFound();
  if (slash === undefined) return reply(null, 302, { location: `/${key}/${project}/` });
  const file = name ?? "index.html";
  const target = join(folder, file);
  if (!(await regularFile(target))) return notFound();
  if (file === "index.html") {
    const page = rewriteExportPage(await readFile(target, "utf8"), key, WIKI_LIBRARIES, (reference) => remote(project!, reference));
    return reply(page, 200, { "content-type": TYPES[".html"]! });
  }
  return reply(Bun.file(target), 200, { "content-type": TYPES[extname(file)] ?? "application/octet-stream" });
}

export interface WikiServerOpts {
  /** host:port. Port 0 selects an ephemeral port (tests). */
  bind: string;
  /** MC_HOME: the server reads MC_HOME/wiki. */
  home: string;
  /** The secret path prefix of each address. */
  key: string;
  log?: Logger;
}

/** The most network references that one wiki server logs. */
const REMOTE_LOG_LIMIT = 64;

/** The text as a JSON string with only printable ASCII characters. */
function quoted(text: string): string {
  return JSON.stringify(text).replace(/[\u007f-\uffff]/g,
    (char) => `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}`);
}

export interface WikiServerHandle {
  /** The origin with a trailing slash. It holds no wiki key. */
  url: string;
  port: number;
  stop(): void;
}

export function startWikiServer(opts: WikiServerOpts): WikiServerHandle {
  if (!WIKI_KEY.test(opts.key)) throw new Error("the wiki key must be 64 lowercase hexadecimal characters");
  const { hostname, port } = parseBind(opts.bind, "wiki bind");
  let bound = port;
  // Each network reference is logged once, because each page request finds it again.
  const logged = new Set<string>();
  const remote = (project: string, reference: string) => {
    const shown = quoted(reference.slice(0, 300));
    const item = `${project} ${shown}`;
    if (opts.log === undefined || logged.has(item) || logged.size >= REMOTE_LOG_LIMIT) return;
    logged.add(item);
    opts.log.warn("dash.wiki_library_remote",
      `The wiki page of Project ${project} loads ${shown} from the network. ` +
        "The OpenWiki provenance record names no local copy for it, so the page policy blocks the load.",
      { project, reference: shown });
  };
  const server = Bun.serve({
    hostname,
    port,
    async fetch(req) {
      let response: Response;
      try {
        response = await handleWikiRequest(req, opts.home, bound, opts.key, remote);
      } catch {
        response = reply("the wiki export could not be read", 500, { "content-type": "text/plain; charset=utf-8" });
      }
      return req.method === "HEAD" ? new Response(null, { status: response.status, headers: response.headers }) : response;
    },
  });
  bound = server.port as number;
  const host = hostname.includes(":") ? `[${hostname}]` : hostname;
  return {
    url: `http://${host}:${bound}/`,
    port: bound,
    stop() {
      server.stop(true);
    },
  };
}
