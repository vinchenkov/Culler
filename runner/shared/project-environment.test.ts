import { describe, expect, test } from "bun:test";
import { mkdtemp, mkdir, symlink, rm, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { prepareProjectEnvironment, prepareAvailableProjectEnvironment, readEnvironmentSecrets, workflowDirectory, workflowEnvironment, workflowDependencies, type ProjectEnvironment, type WorkflowDependencies } from "./project-environment";

const configuration = (): ProjectEnvironment => ({
  base_digest: "sha256:" + "a".repeat(64), build_files: [], build_args: {},
  setup: { command: "npm ci", directory: "." }, services: [], variables: [], secrets: [], declarations: [],
});

describe("Project workflow", () => {
  test("installs dependencies before services and leaves repository commands to the agent", async () => {
    const calls: string[] = [];
    const config = configuration();
    config.services = [{ name: "database", command: "database", directory: ".", ready_command: "database-ready" }];
    const deps: WorkflowDependencies = {
      directory: async () => "/workspace/source", sleep: async () => {},
      execute: async (command) => { calls.push(command); return { exitCode: command === "npm test" ? 1 : 0, output: "actual output" }; },
      start(command) { calls.push(command); return { exited: () => false, stop: async () => { calls.push("stop"); } }; },
    };
    const workflow = await prepareProjectEnvironment(config, "/workspace/source", {}, {}, deps);
    await workflow.stop();
    expect(calls).toEqual(["npm ci", "database", "database-ready", "stop"]);
  });

  test("stops services after readiness failure and excludes secrets from output", async () => {
    const config = configuration();
    let stopped = 0;
    config.services = [{ name: "database", command: "database", directory: ".", ready_command: "ready" }];
    const deps: WorkflowDependencies = {
      directory: async () => "/workspace/source", sleep: async () => {},
      execute: async (command) => ({ exitCode: command === "ready" ? 1 : 0, output: "private-value" }),
      start: () => ({ exited: () => false, stop: async () => { stopped += 1; } }),
    };
    await expect(prepareProjectEnvironment(config, "/workspace/source", {}, {}, deps)).rejects.toThrow("did not become ready");
    expect(stopped).toBe(1);
    config.services = [];
    deps.execute = async () => ({ exitCode: 1, output: "failed with private-value" });
    await expect(prepareProjectEnvironment(config, "/workspace/source", {}, { TOKEN: "private-value" }, deps)).rejects.toThrow("failed with [secret]");
  });

  test("keeps secret values within their command scope", () => {
    const config = configuration();
    config.secrets = [{ name: "TOKEN", reference: "registry", scope: "setup" }];
    expect(workflowEnvironment(config, "setup", {}, { TOKEN: "secret" }).TOKEN).toBe("secret");
    expect(workflowEnvironment(config, "agent", { TOKEN: "secret" }, { TOKEN: "secret" }).TOKEN).toBeUndefined();
    expect(() => workflowEnvironment(config, "setup", {})).toThrow("unavailable");
    config.variables = [{ name: "BUILD_MODE", value: "production", scope: "setup" }];
    expect(workflowEnvironment(config, "agent", { BUILD_MODE: "production" }, { TOKEN: "secret" }).BUILD_MODE).toBeUndefined();
  });

  test("secret input completes at its newline while Docker keeps standard input open", async () => {
    const encoder = new TextEncoder();
    const stream = new ReadableStream<Uint8Array>({ start(controller) {
      controller.enqueue(encoder.encode('{"TOKEN":"first\\n'));
      controller.enqueue(encoder.encode('second"}\n'));
    } });
    expect(await readEnvironmentSecrets(stream)).toEqual({ TOKEN: "first\nsecond" });
  });

  test("invalid secret input exposes no secret value in its failure", async () => {
    for (const input of ['{"TOKEN":123}\n', '"private-value"\n', '{"TOKEN":"private-value"}', 'private-value\n']) {
      const stream = new ReadableStream<Uint8Array>({ start(controller) { controller.enqueue(new TextEncoder().encode(input)); controller.close(); } });
      try { await readEnvironmentSecrets(stream); throw new Error("The input unexpectedly passed."); }
      catch (error) {
        expect(String(error)).toContain("Project secret input");
        expect(String(error)).not.toContain("private-value");
      }
    }
  });

  test("refuses command directories through symlinks outside the Project", async () => {
    const root = await mkdtemp(join(tmpdir(), "culler-environment-test-"));
    try {
      await mkdir(join(root, "project"));
      await symlink(root, join(root, "project", "outside"));
      await expect(workflowDirectory(join(root, "project"), "outside")).rejects.toThrow("leaves the Project");
      expect(await workflowDirectory(join(root, "project"), ".")).toContain("project");
    } finally { await rm(root, { recursive: true, force: true }); }
  });

  test("a preparation failure preserves source work and reports the actual limit", async () => {
    const config = configuration();
    const deps: WorkflowDependencies = { ...workflowDependencies, directory: async () => "/workspace/source",
      execute: async () => ({ exitCode: 127, output: "The compiler is unavailable. private-value" }) };
    const result = await prepareAvailableProjectEnvironment(config, "/workspace/source", {}, { TOKEN: "private-value" }, deps);
    expect(result.workflow).toBeUndefined();
    expect(result.agentEnvironment).toEqual({});
    expect(result.evidence).toContain("exit 127");
    expect(result.evidence).toContain("compiler is unavailable");
    expect(result.evidence).not.toContain("private-value");
  });

  test("an absent secret remains a preparation limit without an inherited value", async () => {
    const config = configuration();
    config.secrets = [{ name: "DATABASE", reference: "database", scope: "agent" }];
    const result = await prepareAvailableProjectEnvironment(config, "/workspace/source", { DATABASE: "unrelated" }, {});
    expect(result.workflow).toBeUndefined();
    expect(result.agentEnvironment.DATABASE).toBeUndefined();
    expect(result.evidence).toContain("DATABASE");
    expect(result.evidence).not.toContain("unrelated");
  });
});

function descendantCommand(path: string) {
  const script = `process.on("SIGTERM", () => {}); require("node:fs").writeFileSync(${JSON.stringify(path)}, String(process.pid)); setInterval(() => {}, 1000);`;
  const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;
  return `${quote(process.execPath)} -e ${quote(script)} & wait`;
}

async function waitForPID(path: string) {
  for (let attempt = 0; attempt < 100; attempt++) {
    const value = await readFile(path, "utf8").catch(() => "");
    if (value) return Number(value);
    await Bun.sleep(10);
  }
  throw new Error("The test descendant did not start.");
}

async function expectTerminated(pid: number) {
  for (let attempt = 0; attempt < 100; attempt++) {
    const result = Bun.spawnSync(["ps", "-o", "stat=", "-p", String(pid)], { env: process.env });
    const state = result.stdout.toString().trim();
    if (!state || state.startsWith("Z")) { expect(true).toBe(true); return; }
    await Bun.sleep(10);
  }
  throw new Error(`The test descendant ${pid} remains active.`);
}

test("workflow timeout terminates descendants that keep command output open", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-workflow-timeout-"));
  try {
    const path = join(root, "child.pid"), start = Date.now();
    const result = await workflowDependencies.execute(descendantCommand(path), root, process.env, 300);
    expect(result.exitCode).toBe(124);
    expect(Date.now() - start).toBeLessThan(2000);
    await expectTerminated(await waitForPID(path));
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("service teardown terminates descendants after the shell exits", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-workflow-service-"));
  let service: ReturnType<WorkflowDependencies["start"]> | undefined;
  try {
    const path = join(root, "child.pid");
    service = workflowDependencies.start(descendantCommand(path).replace(/ & wait$/, " &"), root, process.env);
    const pid = await waitForPID(path);
    const start = Date.now();
    await service.stop();
    expect(Date.now() - start).toBeLessThan(3000);
    await expectTerminated(pid);
  } finally { await service?.stop(); await rm(root, { recursive: true, force: true }); }
});

test("readiness timeouts stop the service and each predicate process group", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-workflow-readiness-"));
  try {
    const config = configuration(), path = join(root, "service.pid");
    config.setup.command = "";
    config.services = [{ name: "database", command: descendantCommand(path), directory: ".", ready_command: "sleep 30 & wait" }];
    let attempts = 0;
    const deps: WorkflowDependencies = {
      ...workflowDependencies, sleep: async () => {},
      execute: async (command, directory, env, timeout) => {
        attempts++; expect(timeout).toBe(1000);
        return workflowDependencies.execute(command, directory, env, 25);
      },
    };
    const start = Date.now();
    await expect(prepareProjectEnvironment(config, root, process.env, {}, deps)).rejects.toThrow("did not become ready");
    expect(attempts).toBe(30);
    expect(Date.now() - start).toBeLessThan(4000);
    await expectTerminated(await waitForPID(path));
  } finally { await rm(root, { recursive: true, force: true }); }
});
