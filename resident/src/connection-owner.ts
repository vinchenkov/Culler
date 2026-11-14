import { spawn } from "node:child_process";

export async function acquireConnectionOwner(binary: string, home: string, connection: string): Promise<() => Promise<void>> {
  const env = { ...process.env, MC_HOME: home };
  delete env.MC_SPINE;
  delete env.MC_RUN_JSON;
  const child = spawn(binary, ["__connection-owner", connection], { env, stdio: ["pipe", "pipe", "ignore", "ignore"] });
  let lost = false;
  const exited = new Promise<void>((resolve) => {
    child.once("exit", () => { lost = true; resolve(); });
    child.once("error", () => { lost = true; resolve(); });
  });
  try {
    await new Promise<void>((resolve, reject) => {
      let body = "";
      const timer = setTimeout(() => reject(new Error("The connection owner is unavailable.")), 31_000);
      const finish = (err?: Error) => { clearTimeout(timer); err ? reject(err) : resolve(); };
      child.stdout!.on("data", chunk => {
        body += chunk.toString();
        if (body.includes("\n")) finish(body.trim() === '{"held":true}' ? undefined : new Error("The connection owner is unavailable."));
        else if (body.length > 1024) finish(new Error("The connection owner response is invalid."));
      });
      void exited.then(() => finish(new Error("The connection owner stopped.")));
    });
  } catch (error) {
    child.kill();
    await exited;
    throw error;
  }
  return async () => {
    const interrupted = lost;
    child.stdin!.end();
    await exited;
    if (interrupted) throw new Error("The connection owner stopped before the request completed.");
  };
}
