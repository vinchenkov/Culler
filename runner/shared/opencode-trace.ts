import { createHash } from "node:crypto";

export const OPENCODE_DATABASE = "opencode/data/opencode/opencode.db";

/** The identifier of a retained Codex export. */
export function openCodeTraceID(nativeRef: string): string {
  const bytes = createHash("sha256").update("culler-opencode-trace\0" + nativeRef).digest().subarray(0, 16);
  bytes[6] = (bytes[6]! & 15) | 128;
  bytes[8] = (bytes[8]! & 63) | 128;
  const hex = bytes.toString("hex");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
