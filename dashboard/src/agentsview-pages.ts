import { readFileSync } from "node:fs";

// Culler owns access and failure pages. AgentsView keeps a separate browser origin.

const escape = (value: string) => value.replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]!));

export const ACCESS_MESSAGES = {
  unavailable: "AgentsView is unavailable. Retry after the service recovers.",
  credential: "Culler cannot read the AgentsView credential. Ask your coding harness to repair the deployment, then retry.",
  refused: "AgentsView refused the deployment credential. Ask your coding harness to repair the deployment, then retry.",
  trace: "The requested trace is unavailable. If the agent just started, wait for its trace to appear, then retry.",
  dashboard: "Open the dashboard link from setup to restore access, then select Retry.",
  invalid: "This trace link is invalid. Open the trace from Culler again.",
  failed: "Culler could not open AgentsView. Retry the request.",
} as const;
export type AccessFailure = keyof typeof ACCESS_MESSAGES;

const stylesheet = readFileSync(new URL("./ui/style.css", import.meta.url), "utf8");
const paletteRoles = {
  paper: "paper", surface: "surface", subtle: "surface-2", ink: "ink", muted: "ink-2", line: "line",
  header: "panel-head", "header-ink": "panel-head-ink", "header-line": "panel-line",
  primary: "action-fill", "primary-ink": "action-ink", "primary-hover": "action-hover",
  control: "field-line", focus: "focus", shadow: "e3", sans: "sans", mono: "mono",
};
const declarations = (block: string) => Object.fromEntries([...block.matchAll(/--([\w-]+)\s*:\s*([^;]+);/g)]
  .map((match) => [match[1]!, match[2]!.trim()]));
const lightTokens = declarations(stylesheet.match(/:root\s*\{([^}]+)\}/)![1]!);
const darkTokens = { ...lightTokens, ...declarations(stylesheet.match(/:root\[data-theme="dark"\][^{]*\{([^}]+)\}/)![1]!) };
function palette(tokens: Record<string, string>) {
  const resolve = (name: string, visited: string[] = []): string => {
    if (!tokens[name] || visited.includes(name)) throw new Error(`The dashboard theme token ${name} is unavailable.`);
    return tokens[name]!.replace(/var\(--([\w-]+)\)/g, (_, reference) => resolve(reference, [...visited, name]));
  };
  return Object.entries(paletteRoles).map(([role, token]) => `--${role}:${resolve(token)}`).join(";");
}
const lightPalette = palette(lightTokens);
const darkPalette = palette(darkTokens);

export function accessPage(path: string, options: { failure?: AccessFailure; retry?: string } = {}): Response {
  const nonce = crypto.randomUUID();
  const message = options.failure ? ACCESS_MESSAGES[options.failure] : "Culler prepares access to AgentsView…";
  const action = options.retry
    ? `<a id="retry" href="${escape(options.retry)}">Retry</a>`
    : '<button id="retry" type="button" hidden>Retry</button>';
  return new Response(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>Culler · Traces</title>
<style nonce="${nonce}">
:root{color-scheme:light;${lightPalette};font:16px/1.6 var(--sans);background:var(--paper);color:var(--ink)}
:root[data-theme="dark"]{color-scheme:dark;${darkPalette}}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px}
main{width:min(100%,38rem);border:1px solid var(--line);border-radius:16px;overflow:hidden;background:var(--surface);box-shadow:var(--shadow)}
header{padding:24px;background:var(--header);color:var(--header-ink);border-bottom:1px solid var(--header-line)}h1{margin:0;font-size:1.5rem;line-height:1.3;font-weight:700;overflow-wrap:anywhere}
.access-body{padding:24px}p{margin:0 0 20px;overflow-wrap:anywhere}#destination{font:.85rem/1.5 var(--mono);color:var(--muted);padding:12px;border:1px solid var(--line);border-radius:9px;background:var(--subtle)}
footer{padding:16px 24px;background:var(--subtle);border-top:1px solid var(--line);display:flex;justify-content:flex-end}
button,a{display:inline-block;font:inherit;font-weight:650;line-height:1.4;text-decoration:none;border:1px solid var(--primary);border-radius:7px;padding:9px 16px;color:var(--primary-ink);background:var(--primary);cursor:pointer}
footer:has(#retry[hidden]){display:none}button:hover,a:hover{background:var(--primary-hover);border-color:var(--primary-hover)}[hidden]{display:none}:focus-visible{outline:3px solid var(--focus);outline-offset:3px}
@media(pointer:coarse){button,a{min-height:44px;min-width:44px}}@media(max-width:420px){body{padding:12px}header,.access-body{padding:16px}footer{padding:16px}}
</style><script nonce="${nonce}">(${accessTheme.toString()})();</script></head><body><main aria-labelledby="title"><header><h1 id="title">${options.failure ? "Trace access unavailable" : "Open AgentsView"}</h1></header><div class="access-body">
<p id="status" role="status" aria-live="polite">${escape(message)}</p>
<p id="destination">${escape(path)}</p></div><footer>${action}</footer></main>
${options.retry ? "" : `<script nonce="${nonce}">(${openAgentsView.toString()})();</script>`}</body></html>`, {
    headers: {
      "content-type": "text/html; charset=utf-8", "cache-control": "no-store", "referrer-policy": "no-referrer",
      "content-security-policy": `default-src 'none'; script-src 'nonce-${nonce}'; style-src 'nonce-${nonce}'; connect-src 'self'; form-action http://127.0.0.1:*; frame-ancestors 'none'; base-uri 'none'`,
      "x-content-type-options": "nosniff",
    },
  });
}

function accessTheme() {
  const media = matchMedia("(prefers-color-scheme: dark)");
  const follow = () => {
    let stored = "";
    try { stored = localStorage.getItem("mc.theme") || ""; } catch {}
    document.documentElement.dataset.theme = stored === "light" || stored === "dark" ? stored : media.matches ? "dark" : "light";
  };
  follow();
  media.addEventListener("change", follow);
  window.addEventListener("storage", event => { if (event.key === "mc.theme") follow(); });
}

function openAgentsView() {
  const path = document.getElementById("destination").textContent;
  const status = document.getElementById("status");
  const retry = document.getElementById("retry");
  let busy = false;
  async function open() {
    if (busy) return;
    busy = true;
    retry.hidden = true;
    status.textContent = "Culler prepares access to AgentsView…";
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 15000);
    try {
      let token = "";
      try { token = localStorage.getItem("mc-dashboard-token") || ""; } catch {}
      const response = await fetch("/api/agentsview/access", {
        method: "POST", headers: { authorization: `Bearer ${token}`, "content-type": "application/json" },
        body: JSON.stringify({ path }), signal: controller.signal, cache: "no-store",
      });
      if (response.status === 401) throw new Error("Open the dashboard link from setup to restore access, then select Retry.");
      const result = await response.json();
      if (!response.ok) throw new Error(result.error?.message || "Culler could not open AgentsView. Retry the request.");
      const form = document.createElement("form");
      form.method = "POST";
      form.action = result.url;
      const grant = document.createElement("input");
      grant.type = "hidden";
      grant.name = "grant";
      grant.value = result.grant;
      form.append(grant);
      document.body.append(form);
      form.submit();
    } catch (error) {
      document.getElementById("title").textContent = "Trace access unavailable";
      status.textContent = error.name === "AbortError" ? "AgentsView did not answer in time. Retry the request." : error.message;
      retry.hidden = false;
    } finally { clearTimeout(timer); busy = false; }
  }
  retry.addEventListener("click", open);
  window.addEventListener("storage", event => { if (event.key === "mc-dashboard-token") open(); });
  open();
}

// The proxy inserts this script before the stock client starts.
export const AGENTSVIEW_BRIDGE = `(${function () {
  try {
    localStorage.removeItem("agentsview-server-url");
    for (const key of Object.keys(localStorage)) {
      if (key.startsWith("agentsview-auth-token")) localStorage.removeItem(key);
    }
  } catch {}
  const original = window.fetch.bind(window);
  window.fetch = async (...args) => {
    const response = await original(...args);
    const failure = response.headers.get("x-culler-access-error");
    if (failure) {
      const query = new URLSearchParams({ code: failure, path: location.pathname + location.search });
      location.replace("/__culler/error?" + query);
      return new Promise(() => {});
    }
    return response;
  };
}.toString()})();`;
