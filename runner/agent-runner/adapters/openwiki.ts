export const OPENWIKI_STARTUP_MS = 30_000;
export const OPENWIKI_TOOLS = [
  "openwiki_begin", "openwiki_submit_plan", "openwiki_next_page",
  "openwiki_inspect_page_claims", "openwiki_submit_page", "openwiki_finish",
];

export interface OpenWikiStatus {
  name: string;
  status: string;
  error?: string;
  tools?: { name: string }[];
}

export class OpenWikiStartup {
  readonly ready: Promise<void>;
  status = "unknown";
  error = "unknown";
  outcome = "unknown";
  private settled = false;
  private resolve!: () => void;
  private reject!: (error: Error) => void;
  private timer: ReturnType<typeof setTimeout>;

  constructor(timeoutMs = OPENWIKI_STARTUP_MS) {
    this.ready = new Promise((resolve, reject) => { this.resolve = resolve; this.reject = reject; });
    // A failure can precede the first stream event or tool hook.
    void this.ready.catch(() => {});
    this.timer = setTimeout(() => this.fail(`startup deadline expired after ${timeoutMs} ms`), timeoutMs);
  }

  observe(servers: OpenWikiStatus[]): void {
    if (this.settled) return;
    const server = servers.find((server) => server.name === "openwiki");
    if (!server) return;
    this.status = server.status;
    if (server.error) this.error = server.error;
    if (["failed", "needs-auth", "disabled"].includes(server.status)) {
      this.fail("the harness reported a conclusive connection failure");
    } else if (server.status === "connected" && server.tools) {
      const names = new Set(server.tools.map((tool) => tool.name.replace(/^mcp__openwiki__/, "")));
      const missing = OPENWIKI_TOOLS.filter((name) => !names.has(name));
      if (missing.length) {
        this.error = `The connection lacks required tools: ${missing.join(", ")}`;
        this.fail("the connected server omitted required tools");
      } else {
        this.settled = true;
        clearTimeout(this.timer);
        this.resolve();
      }
    }
  }

  fail(outcome: string, error?: string): void {
    if (this.settled) return;
    this.outcome = outcome;
    if (error) this.error = error;
    this.settled = true;
    clearTimeout(this.timer);
    this.reject(new Error(`OpenWiki ${outcome}`));
  }

  cancel(): void {
    if (this.settled) return;
    this.settled = true;
    clearTimeout(this.timer);
    this.reject(new DOMException("The run stopped", "AbortError"));
  }

  async poll(status: () => Promise<OpenWikiStatus[]>, intervalMs = 100): Promise<void> {
    while (!this.settled) {
      // The fixed deadline also bounds a status request that never returns.
      const servers = await Promise.race([status(), this.ready.then(() => undefined)]);
      if (servers) this.observe(servers);
      if (!this.settled) await Promise.race([
        new Promise<void>((resolve) => setTimeout(resolve, intervalMs)), this.ready,
      ]);
    }
    await this.ready;
  }

  evidence(outcome = this.outcome): string {
    return `Server: openwiki\nLast connection status: ${this.status}\nOutcome: ${outcome}\nHarness error: ${this.error}`;
  }
}

export interface OpenWikiServer {
  command: string;
  args: string[];
}

export function openWikiServer(workspace: string, inputCommit?: string): OpenWikiServer {
  if (!workspace.startsWith("/") || workspace.includes("\0")) {
    throw new Error("OpenWiki requires an absolute repository path");
  }
  if (inputCommit !== undefined && !/^(?:[0-9a-f]{40}|[0-9a-f]{64})$/.test(inputCommit)) {
    throw new Error("OpenWiki requires a canonical input commit");
  }
  return {
    command: "/usr/bin/env",
    // Clear credentials before Node loads the OpenWiki package.
    args: [
      "-i", "PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp/mc-openwiki",
      process.env.MC_PRIVATE_NODE || "/usr/local/bin/node",
      process.env.MC_PRIVATE_NODE ? "/opt/culler-runtime/app/src/openwiki/server.ts" : "/app/src/openwiki/server.ts", "--repo", workspace,
      "--receipt", "/tmp/openwiki-completion.json",
      ...(inputCommit === undefined ? [] : ["--input-commit", inputCommit]),
    ],
  };
}

export function openWikiCodexConfig(workspace: string, inputCommit?: string): string[] {
  const server = openWikiServer(workspace, inputCommit);
  return [
    "--config", `mcp_servers.openwiki.command=${JSON.stringify(server.command)}`,
    "--config", `mcp_servers.openwiki.args=${JSON.stringify(server.args)}`,
    "--config", "mcp_servers.openwiki.required=true",
    "--config", `mcp_servers.openwiki.startup_timeout_sec=${OPENWIKI_STARTUP_MS / 1000}`,
  ];
}
