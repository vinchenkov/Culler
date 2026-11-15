import { judgeFileTool } from "./judge-tools";

const allowed = await judgeFileTool({ tool_name: "Read", tool_input: { file_path: "/evidence/repository/example.txt" } });
if (allowed.hookSpecificOutput.permissionDecision !== "allow") throw new Error("The Judge cannot read its assigned evidence.");
for (const [tool_name, tool_input] of [
  ["Read", { file_path: "/proc/self/environ" }],
  ["Read", { file_path: "/eval/request.json" }],
  ["Glob", { path: "/evidence", pattern: "../../proc/*" }],
  ["Grep", { path: "/proc", pattern: "token" }],
  ["Bash", { command: "env" }],
] as const) {
  const denied = await judgeFileTool({ tool_name, tool_input });
  if (denied.hookSpecificOutput.permissionDecision !== "deny") throw new Error("The Judge escaped its evidence file scope.");
}
console.log(JSON.stringify({ check: "judge_file_scope", passed: true, model_calls: 0 }));
