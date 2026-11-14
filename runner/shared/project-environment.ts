import { resolve, relative, isAbsolute } from "node:path";
import { realpath } from "node:fs/promises";
import { spawn } from "node:child_process";
import { importLegacyNpmCache, limitProjectCache } from "./project-cache";

export interface EnvironmentCommand {
  command: string;
  directory: string;
}

export interface ProjectEnvironment {
  base_digest: string;
  dockerfile?: { path: string; content: string } | null;
  build_files: Array<{ path: string; content: string }>;
  build_args: Record<string, string>;
  setup: EnvironmentCommand;
  services: Array<EnvironmentCommand & { name: string; ready_command: string }>;
  variables: Array<{ name: string; value: string; scope: "setup" | "agent" | "both" }>;
  secrets: Array<{ name: string; reference: string; scope: "setup" | "agent" | "both" }>;
  declarations: string[];
  source_commit?: string;
}

export type CommandResult = { exitCode: number; output: string };

export async function readEnvironmentSecrets(stream: ReadableStream<Uint8Array> = Bun.stdin.stream()): Promise<Record<string, string>> {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let input = "";
  try {
    while (true) {
      const chunk = await reader.read();
      if (chunk.done) throw new Error("The Project secret input is incomplete.");
      input += decoder.decode(chunk.value, { stream: true });
      const end = input.indexOf("\n");
      if (end > 1024 * 1024 || end < 0 && input.length > 1024 * 1024) throw new Error("The Project secret input exceeds its size limit.");
      if (end < 0) continue;
      let values: unknown;
      try { values = JSON.parse(input.slice(0, end)); } catch { throw new Error("The Project secret input is invalid."); }
      if (!values || typeof values !== "object" || Array.isArray(values) || Object.values(values).some(value => typeof value !== "string")) {
        throw new Error("The Project secret input requires named text values.");
      }
      return values as Record<string, string>;
    }
  } finally { reader.releaseLock(); }
}

export interface WorkflowDependencies {
  execute(command: string, directory: string, env: Record<string, string | undefined>, timeoutMs?: number): Promise<CommandResult>;
  start(command: string, directory: string, env: Record<string, string | undefined>): { stop(): Promise<void>; exited(): boolean };
  directory(workspace: string, directory: string): Promise<string>;
  sleep(milliseconds: number): Promise<void>;
}

export async function workflowDirectory(workspace: string, directory: string): Promise<string> {
  if (isAbsolute(directory) || directory.includes("\0")) throw new Error("The command directory must stay within the Project.");
  const root = await realpath(workspace);
  const target = await realpath(resolve(root, directory || "."));
  const suffix = relative(root, target);
  if (suffix === ".." || suffix.startsWith("../") || isAbsolute(suffix)) {
    throw new Error("The command directory leaves the Project.");
  }
  return target;
}

export function workflowEnvironment(
  configuration: ProjectEnvironment,
  scope: "setup" | "agent",
  base: Record<string, string | undefined>,
  secrets: Record<string, string> = {},
): Record<string, string | undefined> {
  const env = { ...base };
  for (const entry of configuration.variables ?? []) {
    delete env[entry.name];
    if (entry.scope === scope || entry.scope === "both") env[entry.name] = entry.value;
  }
  for (const entry of configuration.secrets ?? []) {
    delete env[entry.name];
    if (entry.scope !== scope && entry.scope !== "both") continue;
    if (secrets[entry.name] === undefined) throw new Error(`The secret reference for ${entry.name} is unavailable.`);
    env[entry.name] = secrets[entry.name];
  }
  return env;
}

export function redactWorkflowOutput(output: string, secrets: Record<string, string>): string {
  let redacted = output;
  for (const secret of Object.values(secrets).sort((a, b) => b.length - a.length)) {
    if (secret) redacted = redacted.split(secret).join("[secret]");
  }
  return redacted.slice(-8192);
}

function workflowProcess(command: string, cwd: string, env: Record<string, string | undefined>, capture: boolean) {
  const child = spawn("/bin/bash", ["-e", "-o", "pipefail", "-c", command], {
    cwd, env, detached: true, stdio: ["ignore", capture ? "pipe" : "ignore", capture ? "pipe" : "ignore"],
  });
  let ended = false;
  const exited = new Promise<number>((resolve, reject) => {
    child.once("error", error => { ended = true; reject(error); });
    child.once("exit", code => { ended = true; resolve(code ?? 128); });
  });
  const closed = new Promise<void>(resolve => { child.once("close", () => resolve()); });
  let killed = false;
  const signal = (value: NodeJS.Signals) => {
    if (!child.pid || killed) return;
    try { process.kill(-child.pid, value); }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== "ESRCH") throw error; }
    if (value === "SIGKILL") killed = true;
  };
  const close = async () => {
    await Promise.race([closed, Bun.sleep(250)]);
    child.stdout?.destroy(); child.stderr?.destroy();
  };
  return { child, exited, ended: () => ended, signal, close };
}

const runCommand: WorkflowDependencies["execute"] = async (command, cwd, env, timeoutMs = 20 * 60_000) => {
  const child = workflowProcess(command, cwd, env, true);
  let output = "";
  for (const stream of [child.child.stdout, child.child.stderr]) {
    stream?.setEncoding("utf8");
    stream?.on("data", data => { output = (output + data).slice(-8192); });
  }
  let timeout: ReturnType<typeof setTimeout>;
  const expired = new Promise<number>((resolve, reject) => {
    timeout = setTimeout(() => {
      try { child.signal("SIGKILL"); resolve(124); } catch (error) { reject(error); }
    }, timeoutMs);
  });
  try {
    const exitCode = await Promise.race([child.exited, expired]);
    child.signal("SIGKILL");
    await child.close();
    return { exitCode, output };
  } finally {
    clearTimeout(timeout!);
    child.signal("SIGKILL");
    await child.close();
  }
};

export const workflowDependencies: WorkflowDependencies = {
  execute: runCommand,
  start(command, cwd, env) {
    const child = workflowProcess(command, cwd, env, false);
    void child.exited.catch(() => {});
    return {
      exited: child.ended,
      async stop() {
        child.signal("SIGTERM");
        try { await Promise.race([child.exited.catch(() => {}), Bun.sleep(2000)]); }
        finally { child.signal("SIGKILL"); await child.close(); }
      },
    };
  },
  directory: workflowDirectory,
  sleep: (milliseconds) => Bun.sleep(milliseconds),
};

export async function prepareProjectEnvironment(
  configuration: ProjectEnvironment,
  workspace: string,
  base: Record<string, string | undefined>,
  secrets: Record<string, string> = {},
  deps: WorkflowDependencies = workflowDependencies,
): Promise<{ stop(): Promise<void>; agentEnvironment: Record<string, string | undefined> }> {
  if (base.CULLER_CACHE_LIMIT_BYTES) {
    if (!base.HOME) throw new Error("The Project requires a persistent home.");
    await importLegacyNpmCache("/cache", base.HOME);
    await limitProjectCache("/cache", Number(base.CULLER_CACHE_LIMIT_BYTES));
  }
  const services: Array<{ stop(): Promise<void>; exited(): boolean }> = [];
  const stop = async () => {
    await Promise.all(services.map((service) => service.stop()));
    if (base.CULLER_CACHE_LIMIT_BYTES) await limitProjectCache("/cache", Number(base.CULLER_CACHE_LIMIT_BYTES));
  };
  const setupEnvironment = workflowEnvironment(configuration, "setup", base, secrets);
  const agentEnvironment = workflowEnvironment(configuration, "agent", base, secrets);
  try {
    if (configuration.setup?.command?.trim()) {
      const directory = await deps.directory(workspace, configuration.setup.directory);
      const result = await deps.execute(configuration.setup.command, directory, setupEnvironment);
      if (result.exitCode !== 0) throw new Error(`Project dependency installation failed with exit ${result.exitCode}. ${redactWorkflowOutput(result.output, secrets)}`);
    }
    for (const service of configuration.services ?? []) {
      const directory = await deps.directory(workspace, service.directory);
      const process = deps.start(service.command, directory, agentEnvironment);
      services.push(process);
      let ready = false;
      for (let attempt = 0; attempt < 30; attempt += 1) {
        if (process.exited()) break;
        const result = await deps.execute(service.ready_command, directory, agentEnvironment, 1000);
        if (result.exitCode === 0 && !process.exited()) { ready = true; break; }
        await deps.sleep(1000);
      }
      if (!ready) throw new Error(`The ${service.name} service did not become ready.`);
    }
    return {
      stop,
      agentEnvironment,

    };
  } catch (error) {
    await stop();
    throw error;
  }
}

export async function prepareAvailableProjectEnvironment(
  configuration: ProjectEnvironment, workspace: string, base: Record<string, string | undefined>,
  secrets: Record<string, string>, dependencies?: WorkflowDependencies,
) {
  const available = { ...configuration, secrets: configuration.secrets.filter(secret => secrets[secret.name] !== undefined) };
  const missing = configuration.secrets.filter(secret => secrets[secret.name] === undefined).map(secret => secret.name);
  const safeBase = { ...base };
  for (const name of missing) delete safeBase[name];
  const agentEnvironment = workflowEnvironment(available, "agent", safeBase, secrets);
  let workflow: Awaited<ReturnType<typeof prepareProjectEnvironment>> | undefined;
  let evidence = "";
  try {
    if (missing.length) throw new Error(`The secret references for ${missing.join(", ")} are unavailable.`);
    workflow = await prepareProjectEnvironment(configuration, workspace, base, secrets, dependencies);
  } catch (error) {
    evidence = "\n\n## Project preparation limit\n\n" +
      "The declared Project preparation failed. Prepare what the environment permits and report the limit. " +
      "Project import remains authorized. Unexecuted repository commands remain unverified.\n\n" + redactWorkflowOutput(String(error), secrets);
  }
  return { workflow, evidence, agentEnvironment };
}
