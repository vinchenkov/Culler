import type { Options } from "@anthropic-ai/claude-agent-sdk";

export function configureClaudeStructuredOutput(options: Options, schema: unknown): void {
  if (schema === undefined) return;
  if (schema === null || typeof schema !== "object" || Array.isArray(schema)) {
    throw new Error("The structured output requires a JSON schema object.");
  }
  options.outputFormat = { type: "json_schema", schema: schema as Record<string, unknown> };
}

export function claudeStructuredOutputEmitter(emit: (event: unknown) => void): (event: unknown) => void {
  return event => {
    const record = event as Record<string, unknown>;
    // The product adapter normalizes prose. The evaluation uses the SDK's validated data.
    if (record.event === "turn-complete") return;
    emit(event);
    if (record.type !== "result" || record.subtype !== "success" || record.is_error !== false) return;
    const output = record.structured_output;
    if (output === null || typeof output !== "object" || Array.isArray(output)) {
      throw new Error("The harness returned no validated structured output object.");
    }
    emit({ event: "turn-complete", status: "success", output: JSON.stringify(output), output_format: "json_schema" });
  };
}
