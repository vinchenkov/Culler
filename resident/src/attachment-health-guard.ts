import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";

export interface AttachmentHealthGuardHandle {
  stop(): Promise<void>;
}

function closedReadyReceipt(value: unknown, generation: string): boolean {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const record = value as Record<string, unknown>;
  return JSON.stringify(Object.keys(record).sort()) ===
      JSON.stringify(["generation", "state", "version"]) &&
    record.generation === generation && record.state === "guarded" && record.version === 1;
}

function guardEnvironment(mcHome: string): NodeJS.ProcessEnv {
  const env = { ...process.env, MC_HOME: mcHome };
  delete env.MC_SPINE;
  delete env.MC_RUN_JSON;
  return env;
}

export async function startAttachmentHealthGuard(
  command: string[],
  mcHome: string,
  generation: string,
): Promise<AttachmentHealthGuardHandle> {
  if (command.length === 0) throw new Error("attachment health guard command is empty");
  const child = spawn(command[0]!, [...command.slice(1), "__attachment-health-guard"], {
    stdio: ["pipe", "pipe", "pipe"],
    env: guardEnvironment(mcHome),
  });
  const exited = childExit(child);
  child.stdin.write(JSON.stringify({ generation, version: 1 }) + "\n");
  await waitForReady(child, generation);
  let stopped = false;
  return {
    async stop(): Promise<void> {
      if (stopped) return;
      stopped = true;
      child.stdin.end();
      const code = await exited;
      if (code !== 0) {
        throw new Error(`attachment health guard exited ${code} during shutdown`);
      }
    },
  };
}

async function waitForReady(
  child: ChildProcessWithoutNullStreams,
  generation: string,
): Promise<void> {
  let stdout = Buffer.alloc(0);
  let stderr = Buffer.alloc(0);
  await new Promise<void>((resolve, reject) => {
    const onExit = (code: number | null): void => {
      finish(new Error(
        `attachment health guard exited before readiness (${code}): ${stderr.toString("utf8").trim()}`,
      ));
    };
    const timeout = setTimeout(() => {
      child.kill("SIGKILL");
      reject(new Error("attachment health guard readiness timed out"));
    }, 2_000);
    const finish = (err?: Error): void => {
      clearTimeout(timeout);
      child.stdout.removeAllListeners();
      child.stderr.removeAllListeners();
      child.removeListener("exit", onExit);
      if (err !== undefined) reject(err);
      else resolve();
    };
    child.stderr.on("data", (chunk: Buffer) => {
      stderr = Buffer.concat([stderr, chunk]).subarray(0, 2_048);
    });
    child.stdout.on("data", (chunk: Buffer) => {
      stdout = Buffer.concat([stdout, chunk]);
      if (stdout.length > 4_096) {
        child.kill("SIGKILL");
        finish(new Error("attachment health guard readiness exceeded its bound"));
        return;
      }
      const newline = stdout.indexOf(0x0a);
      if (newline < 0) return;
      if (newline !== stdout.length - 1) {
        child.kill("SIGKILL");
        finish(new Error("attachment health guard returned trailing readiness bytes"));
        return;
      }
      let value: unknown;
      try {
        value = JSON.parse(stdout.subarray(0, newline).toString("utf8"));
      } catch {
        child.kill("SIGKILL");
        finish(new Error("attachment health guard returned invalid readiness JSON"));
        return;
      }
      if (!closedReadyReceipt(value, generation)) {
        child.kill("SIGKILL");
        finish(new Error("attachment health guard returned a mismatched readiness receipt"));
        return;
      }
      finish();
    });
    child.once("exit", onExit);
  });
}

function childExit(child: ChildProcessWithoutNullStreams): Promise<number | null> {
  return new Promise((resolve) => child.once("exit", (code) => resolve(code)));
}
