// wiki-lane.ts — the wiki lane of the resident. At start and at each
// interval, the lane asks mc to bring the wiki export of each Project to its
// accepted commit. One export call runs at a time, and the tick never waits
// for it.

import type { Logger } from "../../runner/shared/log";
import type { Exec, ExecResult } from "./types";
import { PROJECT_ID } from "./pipeline-liveness";

export const WIKI_INTERVAL_MS = 20_000;

export interface WikiLaneDeps {
  runMc: Exec;
  log: Logger;
  /** The runner image that holds the export command. */
  image: string;
  /** One-shot timer. */
  schedule(fn: () => void, ms: number): unknown;
  cancel(handle: unknown): void;
}

export interface WikiLane {
  start(): void;
  /** One export call. A call waits for the call before it. */
  exportNow(): Promise<void>;
  /** Stops the timer, stops the call in flight, and waits for its end. */
  stop(): Promise<void>;
}

interface ProjectResult {
  project: string;
  status: string;
  commit: string | null;
  error?: string;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function detail(result: ExecResult): string {
  return (result.stderr.trim() || result.stdout.trim()).slice(0, 500);
}

function parseExport(stdout: string): ProjectResult[] {
  let body: unknown;
  try {
    body = JSON.parse(stdout);
  } catch {
    throw new Error("the output is not JSON");
  }
  const rows = body !== null && typeof body === "object" ? (body as { projects?: unknown }).projects : undefined;
  if (!Array.isArray(rows)) throw new Error("the output holds no Project list");
  return rows.filter((row): row is ProjectResult => row !== null && typeof row === "object" &&
    typeof row.project === "string" && PROJECT_ID.test(row.project) && typeof row.status === "string")
    .map((row) => ({
      project: row.project,
      status: row.status,
      commit: typeof row.commit === "string" ? row.commit : null,
      ...(typeof row.error === "string" ? { error: row.error } : {}),
    }));
}

export function createWikiLane(deps: WikiLaneDeps): WikiLane {
  const controller = new AbortController();
  // The last logged failure of each Project, and of the call itself.
  const failures = new Map<string, string>();
  let chain: Promise<void> = Promise.resolve();
  let timer: unknown;
  let closed = false;
  let started = false;

  function failOnce(key: string, cause: string, text: string, ctx?: Record<string, string>): void {
    if (failures.get(key) === cause) return;
    failures.set(key, cause);
    deps.log.warn("wiki.export_failed", text, ctx);
  }

  async function exportOnce(): Promise<void> {
    let result: ExecResult;
    try {
      result = await deps.runMc(["__wiki-export", "--image", deps.image], undefined, { signal: controller.signal });
    } catch (error) {
      if (!closed) failOnce("", message(error), `mc __wiki-export failed: ${message(error)}`);
      return;
    }
    if (closed) return;
    if (result.exitCode !== 0) {
      const cause = `exit ${result.exitCode}: ${detail(result)}`;
      failOnce("", cause, `mc __wiki-export exited ${result.exitCode}: ${detail(result)}`);
      return;
    }
    let projects: ProjectResult[];
    try {
      projects = parseExport(result.stdout);
    } catch (error) {
      failOnce("", message(error), `mc __wiki-export returned an invalid response: ${message(error)}`);
      return;
    }
    failures.delete("");
    for (const row of projects) {
      const commit = row.commit ?? "none";
      if (row.status === "failed") {
        const cause = row.error ?? "no cause";
        failOnce(`project:${row.project}`, `${commit} ${cause}`,
          `The wiki export of Project ${row.project} at commit ${commit} failed, so the previous export stays in use: ${cause}`,
          { project: row.project, commit });
        continue;
      }
      failures.delete(`project:${row.project}`);
      if (row.status === "exported") {
        deps.log.info("wiki.exported", `The wiki export of Project ${row.project} is now at commit ${commit}.`,
          { project: row.project, commit });
      }
    }
  }

  function exportNow(): Promise<void> {
    const call = chain.then(() => (closed ? undefined : exportOnce()));
    chain = call.catch(() => undefined);
    return call;
  }

  function schedule(ms: number): void {
    if (closed) return;
    timer = deps.schedule(() => {
      timer = undefined;
      void exportNow()
        .catch((error) => failOnce("", message(error), `The wiki export call failed: ${message(error)}`))
        .finally(() => schedule(WIKI_INTERVAL_MS));
    }, ms);
  }

  return {
    start() {
      if (closed || started) return;
      started = true;
      schedule(0);
    },
    exportNow,
    async stop() {
      closed = true;
      if (timer !== undefined) deps.cancel(timer);
      timer = undefined;
      controller.abort();
      await chain;
    },
  };
}
