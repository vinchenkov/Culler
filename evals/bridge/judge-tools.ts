import { realpath } from "node:fs/promises";
import { resolve, relative, isAbsolute } from "node:path";

export async function judgeFileTool(input: { tool_name: string; tool_input: unknown }) {
  const deny = () => ({ hookSpecificOutput: { hookEventName: "PreToolUse" as const,
    permissionDecision: "deny" as const, permissionDecisionReason: "The Judge can inspect only the assigned evidence files." } });
  if (!["Read", "Glob", "Grep"].includes(input.tool_name) || !input.tool_input || typeof input.tool_input !== "object") return deny();
  const values = input.tool_input as Record<string, unknown>;
  const field = input.tool_name === "Read" ? "file_path" : "path";
  const path = values[field] ?? (input.tool_name === "Read" ? undefined : "/evidence");
  if (typeof path !== "string" || !isAbsolute(path)) return deny();
  for (const name of input.tool_name === "Glob" ? ["pattern"] : ["glob"]) {
    const pattern = values[name];
    if (pattern !== undefined && (typeof pattern !== "string" || isAbsolute(pattern) || pattern.split(/[\\/]/).includes(".."))) return deny();
  }
  try {
    const actual = await realpath(resolve(path));
    const child = relative("/evidence", actual);
    if (child === ".." || child.startsWith("../") || isAbsolute(child)) return deny();
    return { hookSpecificOutput: { hookEventName: "PreToolUse" as const, permissionDecision: "allow" as const,
      updatedInput: { ...values, [field]: actual } } };
  } catch {
    return deny();
  }
}
