import type { Options } from "@anthropic-ai/claude-agent-sdk";

const levels = {
  codex: new Set(["low", "medium", "high", "xhigh", "max", "ultra"]),
  claude: new Set(["low", "medium", "high", "xhigh", "max"]),
};

export function requireEffort(harness: string, effort: unknown): string {
  if (typeof effort !== "string" || !levels[harness as keyof typeof levels]?.has(effort)) {
    throw new Error("The evaluation requires an explicit effort supported by its harness.");
  }
  return effort;
}

export function codexEffortArgv(argv: string[], effort: unknown): string[] {
  const value = requireEffort("codex", effort);
  return [...argv.slice(0, -1), "--config", `model_reasoning_effort="${value}"`,
    "--config", `agents.default_subagent_reasoning_effort="${value}"`, argv.at(-1)!];
}

export function configureClaudeEffort(options: Options, effort: unknown): void {
  const value = requireEffort("claude", effort) as NonNullable<Options["effort"]>;
  options.effort = value;
  options.thinking = { type: "adaptive" };
  options.env = { ...options.env, CLAUDE_CODE_EFFORT_LEVEL: value };
}
