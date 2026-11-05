import { installRunSkills, runSkillsFrom } from "/app/src/agent-runner/skills";
import { codexAdapterEnv, codexArgv } from "/app/src/agent-runner/adapters/codex";
import { claudeAdapterEnv, claudeOptions } from "/app/src/agent-runner/adapters/claude";
import { NativeSessionStore } from "/app/src/agent-runner/adapters/session-store";
import { mkdir, rm } from "node:fs/promises";
import { nativeSdkExecutable } from "./harness-paths";
import { codexEffortArgv, configureClaudeEffort } from "./effort";

const root = "/tmp/culler-eval-contract-check";
await mkdir(root, { recursive: true });
try {
  const skills = runSkillsFrom([{ name: "mc-packager", body: "Fixed skill fixture" }], reason => { throw new Error(reason); });
  const installed = await installRunSkills(`${root}/skills`, skills);
  if (await Bun.file(installed[0]!).text() !== "Fixed skill fixture") throw new Error("The product skill installation differs.");
  const access = { version: 1 as const, url: "https://host.docker.internal:12345", token: "culler_proxy_" + "x".repeat(43),
    ca: "-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----", harness: "codex" as const, authMode: "account" as const };
  const codexEnv = codexAdapterEnv({ CODEX_HOME: `${root}/auth`, CODEX_REFRESH_TOKEN_URL_OVERRIDE: "http://127.0.0.1:9/oauth/token", ANTHROPIC_AUTH_TOKEN: "fixture-token" }, access, `${root}/ca.pem`, `${root}/auth`);
  if (codexEnv.ANTHROPIC_AUTH_TOKEN !== undefined) throw new Error("The product environment retained another credential channel.");
  const args = codexEffortArgv(codexArgv("fresh", undefined, "fixture-model"), "high");
  if (args[args.indexOf("--model") + 1] !== "fixture-model") throw new Error("The product model argument differs.");
  if (!args.includes('model_reasoning_effort="high"') || !args.includes('agents.default_subagent_reasoning_effort="high"')) {
    throw new Error("The evaluation Codex effort differs.");
  }
  const claudeEnv = claudeAdapterEnv("claude", { ANTHROPIC_AUTH_TOKEN: "fixture-token" }, `${root}/config`, { ...access, harness: "claude-sdk" }, `${root}/ca.pem`);
  if (claudeEnv.ANTHROPIC_AUTH_TOKEN !== undefined || claudeEnv.CLAUDE_CODE_OAUTH_TOKEN !== access.token || codexEnv.CULLER_PROXY_TOKEN !== access.token) {
    throw new Error("The product environment requires only Culler provider access.");
  }
  const options = claudeOptions("fixture-model", root, claudeEnv, new NativeSessionStore(`${root}/session`));
  configureClaudeEffort(options, "high");
  if (options.effort !== "high" || options.thinking?.type !== "adaptive" || options.env?.CLAUDE_CODE_EFFORT_LEVEL !== "high") {
    throw new Error("The evaluation Claude effort differs.");
  }
  if (options.model !== "fixture-model" || JSON.stringify(options.settingSources) !== '["user"]') throw new Error("The product options differ.");
  const versions: Record<string, string> = {};
  for (const [family, argv] of Object.entries({ codex: [args[0]!, "--version"], claude: [await nativeSdkExecutable(), "--version"] })) {
    const child = Bun.spawn(argv, { stdout: "pipe", stderr: "pipe" });
    const output = (await new Response(child.stdout).text()).trim();
    const error = await new Response(child.stderr).text();
    const code = await child.exited;
    if (code !== 0 || !output) throw new Error(`The image lacks the ${family} version response (${code}): ${error.slice(0,300)}`);
    versions[family] = output;
  }
  console.log(JSON.stringify({ check: "runtime_bridge", passed: true, model_calls: 0, versions,
    contracts: ["product_imports", "skill_installation", "credential_environment", "model_argument", "explicit_effort", "isolated_settings"] }));
} finally {
  await rm(root, { recursive: true, force: true });
}
