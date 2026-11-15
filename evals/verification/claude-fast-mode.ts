import { query, type Options } from "@anthropic-ai/claude-agent-sdk";
import { configureClaudeFastMode } from "../bridge/fast-mode";

const results: unknown[] = [];
for (const enabled of [true, false]) {
  let intercepted = false;
  const options: Options = {
    model: "fixed-model", effort: "high", settingSources: ["user"],
    settings: { autoMemoryEnabled: false, autoDreamEnabled: false },
    env: { CLAUDE_CONFIG_DIR: "/tmp/private-config", CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1" },
    spawnClaudeCodeProcess: ({ args, env }) => {
      intercepted = true;
      const index = args.indexOf("--settings");
      if (index < 0) throw new Error("The SDK omitted the session settings.");
      const settings = JSON.parse(args[index + 1]!);
      if (settings.fastMode !== enabled || settings.autoMemoryEnabled !== false || settings.autoDreamEnabled !== false) {
        throw new Error("The SDK altered the session settings.");
      }
      if (args[args.indexOf("--model") + 1] !== "fixed-model" || args[args.indexOf("--effort") + 1] !== "high") {
        throw new Error("The SDK altered the model or effort.");
      }
      if (enabled && env?.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC === "1") {
        throw new Error("The SDK suppressed the account check.");
      }
      results.push({ enabled, inline_settings: settings, effort: "high", intercepted_before_spawn: true });
      throw new Error("TOKEN_FREE_STOP");
    },
  };
  configureClaudeFastMode(options, enabled);
  try {
    for await (const _ of query({ prompt: "This check must never reach a model.", options })) {}
  } catch (error) {
    if (!(error instanceof Error) || !error.message.includes("TOKEN_FREE_STOP")) throw error;
  }
  if (!intercepted) throw new Error("The SDK did not reach the process boundary.");
}
console.log(JSON.stringify({ passed: true, model_calls: 0, results }));
