import { rename, writeFile } from "node:fs/promises";

export function cleanShellEnvironment(env) {
  for (const name of Object.keys(env)) {
    if (name.startsWith("OPENCODE_") || name.startsWith("CULLER_OPENCODE_") ||
        name === "CULLER_PROXY_TOKEN" || name === "MC_PROVIDER_ACCESS_FILE" ||
        (typeof env[name] === "string" && env[name].includes("culler_proxy_"))) delete env[name];
  }
  return env;
}

export async function waitForOpenWiki(ctx, required, timeoutMs = 30_000, intervalMs = 100) {
  let stopped = false;
  let timer;
  const ready = (async () => {
    while (!stopped) {
      const server = (await ctx.mcp.list()).data.find(server => server.name === "openwiki");
      if (server && ["failed", "disabled", "needs_auth"].includes(server.status.status)) {
        throw new Error(`OpenWiki failed to start: ${server.status.error ?? server.status.status}`);
      }
      const names = new Set((await ctx.tool.list())
        .filter(tool => tool.options?.namespace === "openwiki").map(tool => tool.name));
      if (server?.status.status === "connected" && required.every(name => names.has(name))) return;
      await new Promise(resolve => setTimeout(resolve, intervalMs));
    }
  })();
  try {
    await Promise.race([ready, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error("OpenWiki did not expose all required tools before the startup deadline.")), timeoutMs);
    })]);
  } finally {
    stopped = true;
    clearTimeout(timer);
  }
}

export default {
  id: "culler.control",
  async setup(ctx) {
    await ctx.shell.hook("create.before", input => { cleanShellEnvironment(input.env); });
    await ctx.tool.transform(tools => {
      tools.remove("question");
      if (process.env.CULLER_OPENCODE_WEBSEARCH !== "1") tools.remove("websearch");
    });
    const sessionFile = process.env.CULLER_OPENCODE_SESSION_FILE;
    const required = JSON.parse(process.env.CULLER_OPENCODE_OPENWIKI_TOOLS ?? "[]");
    if (sessionFile || required.length) {
      let registered = false;
      await ctx.session.hook("prompt", async ({ sessionID }) => {
        if (sessionFile && !registered) {
          await writeFile(`${sessionFile}.tmp`, sessionID, { flag: "wx", mode: 0o600 });
          await rename(`${sessionFile}.tmp`, sessionFile);
          registered = true;
        }
        if (required.length) await waitForOpenWiki(ctx, required);
      });
    }
  },
};
