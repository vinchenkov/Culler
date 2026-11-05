// mc.ts — the single seam through which the dashboard reaches the spine.
// Every read and write is a spawned invocation of the real
// `mc` binary — one JSON object on stdout, exit 0/1/2 — with the process
// environment passed through untouched, so the production darwin binary
// self-delegates into the warm helper container and the test_fake_routing
// build honors MC_SPINE/MC_HELPER. No other file in this
// package may reach the spine, and no SQLite driver may ever appear here.

export interface McResult {
  code: number;
  /** Parsed stdout when it is a single JSON object; null otherwise. */
  json: Record<string, unknown> | null;
  stderr: string;
}

/** Injectable seam: tests substitute a scripted fake. */
export interface McRunner {
  run(argv: string[], input?: string): Promise<McResult>;
}

export interface McCompletionRecord {
  startedAtUnixMs: number;
  spawnedAtUnixMs: number | null;
  exitObservedAtUnixMs: number | null;
  completedAtUnixMs: number;
  /** The total command duration through process exit, closed output streams, and result parse. */
  elapsedMs: number;
  /** The mc process that Bun starts on the host. */
  hostPid: number | null;
  code: number | null;
  outcome: "completed" | "error";
}

export interface McRunnerOptions {
  onComplete?: (record: McCompletionRecord) => void;
}

export function makeMcRunner(mcBin: string, options: McRunnerOptions = {}): McRunner {
  return {
    async run(argv: string[], input?: string): Promise<McResult> {
      const started = options.onComplete ? performance.now() : 0;
      const record: McCompletionRecord | undefined = options.onComplete ? {
        startedAtUnixMs: Date.now(), spawnedAtUnixMs: null, exitObservedAtUnixMs: null,
        completedAtUnixMs: 0, elapsedMs: 0, hostPid: null, code: null, outcome: "error",
      } : undefined;
      try {
        const proc = Bun.spawn([mcBin, ...argv], {
          // The environment reflects each call, including changes after module import.
          env: { ...process.env },
          stdin: input === undefined ? "ignore" : new TextEncoder().encode(input),
          stdout: "pipe",
          stderr: "pipe",
        });
        if (record) {
          record.spawnedAtUnixMs = Date.now();
          record.hostPid = proc.pid;
        }
        const exit = record ? proc.exited.then((code) => {
          record.exitObservedAtUnixMs = Date.now();
          record.code = code;
          return code;
        }) : proc.exited;
        const [stdout, stderr, code] = await Promise.all([
          new Response(proc.stdout).text(),
          new Response(proc.stderr).text(),
          exit,
        ]);
        let json: Record<string, unknown> | null = null;
        try {
          const parsed: unknown = JSON.parse(stdout);
          if (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)) {
            json = parsed as Record<string, unknown>;
          }
        } catch {
          json = null;
        }
        if (record) record.outcome = "completed";
        return { code, json, stderr };
      } finally {
        if (record) {
          record.completedAtUnixMs = Date.now();
          record.elapsedMs = performance.now() - started;
          try { options.onComplete?.(record); } catch { /* The observer preserves the command result. */ }
        }
      }
    },
  };
}
