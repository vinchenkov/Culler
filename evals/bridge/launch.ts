import { installRunSkills, runSkillsFrom } from "/app/src/agent-runner/skills";
import { codexAdapterEnv, codexArgv, executeCodexProcess } from "/app/src/agent-runner/adapters/codex";
import { claudeAdapterEnv, claudeOptions, executeClaudeQuery, mirrorRoleSkill } from "/app/src/agent-runner/adapters/claude";
import { NativeSessionStore } from "/app/src/agent-runner/adapters/session-store";
import { loadProviderAccess, writeProviderCA, codexProviderArgs, redactNativeAccess } from "/app/src/agent-runner/adapters/provider-access";
import { mkdir, symlink } from "node:fs/promises";
import { nativeSdkExecutable } from "./harness-paths";
import { judgeFileTool } from "./judge-tools";
import { codexEffortArgv, configureClaudeEffort, requireEffort } from "./effort";

import { codexFastModeArgv, configureClaudeFastMode } from "./fast-mode";
import { configureClaudeStructuredOutput, claudeStructuredOutputEmitter } from "./structured-output";
import type { Options } from "@anthropic-ai/claude-agent-sdk";

const spec = JSON.parse(await Bun.file("/eval/request.json").text());
const effort = requireEffort(spec.harness, spec.effort);
const prompt = await Bun.stdin.text();
const access = await loadProviderAccess(spec.harness === "claude" ? "claude-sdk" : "codex");
await mkdir("/eval/private-config", { recursive: true, mode: 0o700 });
const ca = await writeProviderCA(access, "/eval/private-config");
async function repositoryCheck(args: string[]) {
  const child = Bun.spawn(["git", "-C", spec.workspace, ...args], { stdout: "pipe", stderr: "pipe" });
  const output = (await new Response(child.stdout).text()).trim();
  await new Response(child.stderr).text();
  return { code: await child.exited, output };
}
const startBranch = spec.role === "packager" ? await repositoryCheck(["rev-parse", "--symbolic-full-name", "HEAD"]) : null;
async function recordVersion(argv: string[], env: Record<string, string>, configuredEffort: string, configuredFastMode: boolean,
  outputFormat?: Options["outputFormat"]) {
  const check = Bun.spawn(argv, { env, stdout: "pipe", stderr: "pipe" });
  const version = (await new Response(check.stdout).text()).trim();
  await new Response(check.stderr).text();
  if (await check.exited !== 0 || !version) throw new Error("The harness version is unavailable.");
  console.log(JSON.stringify({ event: "runtime-config", harness: spec.harness, harness_version: version,
    requested_model: spec.model, requested_effort: effort, configured_effort: configuredEffort,
    requested_fast_mode: spec.fast_mode ?? false, configured_fast_mode: configuredFastMode,
    ...(spec.harness === "codex" ? { configured_service_tier: configuredFastMode ? "fast" : "default" } : {}),
    ...(outputFormat ? { configured_output_format: outputFormat.type, configured_output_schema: outputFormat.schema } : {}) }));
}
async function finish(code: number) {
  if (spec.role === "packager") {
    const head = await repositoryCheck(["rev-parse", "HEAD"]);
    const branch = await repositoryCheck(["rev-parse", "--symbolic-full-name", "HEAD"]);
    const status = await repositoryCheck(["status", "--porcelain=v1", "--untracked-files=all"]);
    console.log(JSON.stringify({ event: "repository-state", preserved: startBranch?.code === 0 && branch.code === 0 &&
      branch.output === startBranch.output && head.code === 0 && status.code === 0 &&
      head.output === process.env.CULLER_EVAL_START_COMMIT && status.output === "" }));
  }
  await redactNativeAccess("/eval/session", access.token);
  process.exit(code);
}
await mkdir("/eval/session", { recursive: true });
const skills = runSkillsFrom(spec.skills ?? [], reason => { throw new Error(reason); });
await installRunSkills("/home/mc-model/.agents/skills", skills);
if (spec.harness === "codex") {
  await mkdir("/eval/private-config/codex", { recursive: true, mode: 0o700 });
  await symlink("/eval/session", "/eval/private-config/codex/sessions");
  const env = codexAdapterEnv(process.env, access, ca, "/eval/private-config/codex");
  const argv = codexFastModeArgv(codexEffortArgv(codexArgv("fresh", undefined, spec.model), effort), spec.fast_mode ?? false);
  argv.splice(argv.length - 1, 0, ...codexProviderArgs(access));
  await recordVersion([argv[0]!, "--version"], env, effort, spec.fast_mode ?? false);
  const processHandle = Bun.spawn(argv, { cwd: spec.workspace, env, stdin: new TextEncoder().encode(prompt), stdout: "pipe", stderr: "pipe" });
  await finish(await executeCodexProcess(processHandle, "/eval/session", false, { model: spec.model, env }));
} else if (spec.harness === "claude") {
  const configDir = "/eval/private-config";
  await mkdir(configDir, { recursive: true, mode: 0o700 });
  await mirrorRoleSkill(configDir);
  const env = claudeAdapterEnv("claude", process.env, configDir, access, ca);
  const store = new NativeSessionStore("/eval/session");
  const options = claudeOptions(spec.model, spec.workspace, env, store);
  configureClaudeEffort(options, effort);
  configureClaudeFastMode(options, spec.fast_mode ?? false);
  configureClaudeStructuredOutput(options, spec.output_schema);
  await recordVersion([await nativeSdkExecutable(), "--version"], options.env as Record<string, string>, options.effort!,
    typeof options.settings === "object" && options.settings.fastMode === true, options.outputFormat);
  if (spec.role === "scope_judge") {
    options.tools = [];
    options.skills = undefined;
    options.allowedTools = [];
  }
  if (spec.role === "judge") {
    options.tools = ["Read", "Glob", "Grep"];
    options.skills = undefined;
    options.allowedTools = ["Read", "Glob", "Grep", "mcp__packet__inspect", "mcp__packet__scroll", "mcp__packet__follow_link", "mcp__packet__click"];
    options.mcpServers = { packet: { command: "bun", args: ["/app/eval/browser-mcp.ts"],
      env: { CULLER_EVAL_BROWSER_URL: process.env.CULLER_EVAL_BROWSER_URL!, CULLER_EVAL_BROWSER_TOKEN: process.env.CULLER_EVAL_BROWSER_TOKEN! } } };
    options.hooks = { PreToolUse: [{ matcher: "Read|Glob|Grep", hooks: [judgeFileTool] }] };
  }
  const emit = spec.output_schema ? claudeStructuredOutputEmitter(event => console.log(JSON.stringify(event))) : undefined;
  await finish(await executeClaudeQuery(prompt, options, false, emit ? { emit } : {}));
} else {
  throw new Error("The configured harness family is unsupported.");
}
