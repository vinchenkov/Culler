import type { Options } from "@anthropic-ai/claude-agent-sdk";

export function codexFastModeArgv(argv: string[], enabled: boolean): string[] {
  if (typeof enabled !== "boolean") throw new Error("The fast mode setting must be a boolean.");
  const tier = enabled ? "fast" : "default";
  return [...argv.slice(0, -1), "--config", `service_tier="${tier}"`,
    "--config", `features.fast_mode=${enabled}`, argv.at(-1)!];
}

export function configureClaudeFastMode(options: Options, enabled: boolean): void {
  if (typeof enabled !== "boolean") throw new Error("The fast mode setting must be a boolean.");
  if (typeof options.settings === "string") {
    throw new Error("The evaluation requires inline session settings for fast mode.");
  }
  options.settings = { ...options.settings, fastMode: enabled };
  if (enabled) {
    options.env = { ...options.env };
    // The harness needs its account availability check before it can use fast mode.
    delete options.env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC;
  }
}
