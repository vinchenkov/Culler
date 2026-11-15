import { adapterInvocation, makeLineSplitter, nativeLocator, type RunEnvelope } from "../agent-runner/main";
import { adapterFailure, diagnosticRedactor, readAdapterFailure, type AdapterFailure } from "../agent-runner/diagnostics";
import { setupError } from "../shared/setup-error";
import { installEnvelopeSkills } from "../agent-runner/skills";
import { makeLogger } from "../shared/log";
import { stderrSink } from "../shared/log-sink";
import { prepareProjectEnvironment, prepareAvailableProjectEnvironment, readEnvironmentSecrets, type ProjectEnvironment, type WorkflowDependencies } from "../shared/project-environment";
import { materializeProjectBuildFiles, captureProjectInput, restoreDeniedProjectBuildFiles, type ProjectBuildFile } from "../shared/project-files";

interface SetupEnvelope extends RunEnvelope {
  project: string;
  environment: { configuration: ProjectEnvironment; inputs_digest: string; revision_id: string; kind: "setup" | "validate"; removed_files?: ProjectBuildFile[]; initial_setup?: boolean; setup_id?: string; input_tree?: string; denied_build_files?: ProjectBuildFile[] };
}

export async function prepareSetupWorkflow(
  configuration: ProjectEnvironment, kind: "setup" | "validate", workspace: string,
  base: Record<string, string | undefined>, secrets: Record<string, string>,
  dependencies?: WorkflowDependencies,
  removedFiles: ProjectBuildFile[] = [],
) {
  if (kind === "validate") await materializeProjectBuildFiles(workspace,
    [...(configuration.dockerfile ? [configuration.dockerfile] : []), ...configuration.build_files], removedFiles);
  return prepareAvailableProjectEnvironment(configuration, workspace, base, secrets, dependencies);
}

async function control(action: string, body: unknown): Promise<boolean> {
  const child = Bun.spawn(["mc", "environment", action], {
    stdin: "pipe", stdout: "ignore", stderr: "inherit",
  });
  child.stdin.write(JSON.stringify(body));
  child.stdin.end();
  return await child.exited === 0;
}

export async function forwardSetupEvents(
  stream: ReadableStream<Uint8Array>, fallback: string | undefined,
  register: (nativeRef: string, filename: string) => Promise<boolean>,
  emit: (line: string) => void, failed: (cause: string) => void,
  redact = diagnosticRedactor(),
): Promise<AdapterFailure | undefined> {
  let failure: AdapterFailure | undefined;
  let registration: Promise<void> | undefined;
  const split = makeLineSplitter(line => {
    if (!line) return;
    let event;
    try { event = JSON.parse(line); }
    catch { emit(redact(line)); return; }
    emit(JSON.stringify(event, (_key, value) => typeof value === "string" ? redact(value) : value));
    failure = readAdapterFailure(event) ?? failure;
    if (registration || event?.event !== "session-start") return;
    try {
      if (typeof event.session_id !== "string" || !/^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$/.test(event.session_id)) {
        throw new Error("The setup adapter supplied an invalid session identifier.");
      }
      const filename = nativeLocator(event.native_file, fallback);
      registration = register(event.session_id, filename).then(ok => {
        if (!ok) failed("The setup trace registration failed. The resident will try recovery.");
      }).catch(error => failed(String(error)));
    } catch (error) { failed(String(error)); }
  });
  const decoder = new TextDecoder();
  for await (const chunk of stream) split(decoder.decode(chunk, { stream: true }));
  split(decoder.decode() + "\n");
  await registration;
  return failure && { ...failure, ...adapterFailure(failure.condition, failure.evidence, redact) };
}

export async function forwardSetupDiagnostics(stream: ReadableStream<Uint8Array>, emit: (line: string) => void, redact: (line: string) => string) {
  const split = makeLineSplitter(line => { if (line) emit(redact(line)); });
  const decoder = new TextDecoder();
  for await (const chunk of stream) split(decoder.decode(chunk, { stream: true }));
  split(decoder.decode() + "\n");
}

async function registerTrace(operationID: string, nativeRef: string, filename: string): Promise<boolean> {
  const child = Bun.spawn(["mc", "run", "register-session", operationID, "--native-ref", nativeRef, "--file", filename], {
    stdin: "ignore", stdout: "ignore", stderr: "inherit",
  });
  const timeout = setTimeout(() => child.kill("SIGKILL"), 15_000);
  try { return await child.exited === 0; } finally { clearTimeout(timeout); }
}

async function run(): Promise<number> {
  const envelope = await Bun.file(process.env.MC_RUN_JSON || "/mc/run.json").json() as SetupEnvelope;
  const workspace = envelope.mounts.workspace ?? "/workspace/source";
  const log = makeLogger({ comp: "agent-runner", level: "info", sink: stderrSink() });
  const heartbeat = setInterval(() => { void control("heartbeat", { operation_id: envelope.run_id }); }, 20_000);
  let workflow: Awaited<ReturnType<typeof prepareProjectEnvironment>> | undefined;
  let secrets: Record<string, string> = {};
  try {
    if (!await control("heartbeat", { operation_id: envelope.run_id })) return 2;
    await installEnvelopeSkills(envelope.skills, envelope.skill_problems, log);
    secrets = await readEnvironmentSecrets();
    if (envelope.environment.initial_setup && envelope.environment.kind === "setup") {
      const snapshot = `${envelope.mounts.session ?? "/mc/session"}/project-input-tree`;
      if (!await Bun.file(snapshot).exists()) await Bun.write(snapshot, await captureProjectInput(workspace, `${snapshot}.index`, envelope.environment.setup_id));
    }
    if (envelope.environment.input_tree) {
      await restoreDeniedProjectBuildFiles(workspace, envelope.environment.denied_build_files ?? [], envelope.environment.input_tree);
    }
    const prepared = await prepareSetupWorkflow(envelope.environment.configuration, envelope.environment.kind, workspace, process.env, secrets,
      undefined, envelope.environment.removed_files);
    workflow = prepared.workflow;
    const invocation = adapterInvocation(envelope);
    invocation.argv[0] = process.env.MC_PRIVATE_BUN || process.execPath;
    const processEnv = { ...prepared.agentEnvironment, MC_RUN_ID: envelope.run_id, MC_ROLE: "setup" };
    const child = Bun.spawn(invocation.argv, { cwd: workspace, env: processEnv, stdin: "pipe", stdout: "pipe", stderr: "pipe" });
    const stop = () => child.kill("SIGTERM");
    process.on("SIGTERM", stop);
    process.on("SIGINT", stop);
    try {
      child.stdin.write(envelope.brief + prepared.evidence);
      child.stdin.end();
      const redact = diagnosticRedactor(processEnv, Object.values(secrets));
      await Promise.all([
        forwardSetupEvents(child.stdout, invocation.defaultNativeFile,
          (nativeRef, filename) => envelope.harness === "fake" ? Promise.resolve(true) : registerTrace(envelope.run_id, nativeRef, filename),
          line => console.log(line), cause => log.warn("runner.session_lost", cause), redact),
        forwardSetupDiagnostics(child.stderr, line => console.error(line), redact),
      ]);
      return await child.exited;
    } finally {
      process.off("SIGTERM", stop);
      process.off("SIGINT", stop);
    }
  } catch (error) {
    const redact = diagnosticRedactor(process.env, Object.values(secrets));
    const failure = setupError(error, redact);
    console.log(JSON.stringify(adapterFailure(failure.message, failure.details, redact)));
    return 2;
  } finally {
    clearInterval(heartbeat);
    await workflow?.stop();
  }
}

if (import.meta.main) process.exit(await run());
