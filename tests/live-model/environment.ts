import { SPINE_SCHEMA_ID } from "../../resident/src/resident-control";
import type { Exec } from "../../resident/src/types";

export async function initializeLiveSpine(runMc: Exec): Promise<void> {
  const result = await runMc(["__onboard-spine"], new TextEncoder().encode(JSON.stringify({
    protocol_version: 1, schema_id: SPINE_SCHEMA_ID, mirror_state: "absent", allow_reset: false, reset: false,
  })));
  if (result.exitCode !== 0) {
    throw new Error("The isolated live test requires a host binary with test_fake_routing and test_scratch_spine.");
  }
  const initialized = JSON.parse(result.stdout);
  if (initialized.schema_id !== SPINE_SCHEMA_ID || initialized.status !== "initialized") {
    throw new Error("The live test requires a new isolated spine with the current schema.");
  }
  const probe = await runMc(["__system-state", "probe"]);
  if (probe.exitCode !== 0) throw new Error("The live test cannot write its isolated diagnostic spine.");
}

export function summarizeLiveDiagnostics(value: unknown): Record<string, unknown>[] {
  const events = (value as { events?: unknown })?.events;
  if (!Array.isArray(events)) throw new Error("The live diagnostic evidence is unavailable.");
  const fields = ["request_id", "origin", "operation", "outcome", "connection_id", "profile_id", "run_id",
    "request_sent", "http_status", "provider_error_code", "observed_at", "credential_scope"];
  return events.map(event => Object.fromEntries(fields.flatMap(field => {
    const value = event?.[field];
    if (typeof value === "string" && value.length <= 512 || typeof value === "boolean" || typeof value === "number" && Number.isFinite(value)) return [[field, value]];
    return [];
  })));
}

export async function resolveLiveImage(selector: string,
  execute: (argv: string[]) => Promise<{ exitCode: number; stdout: string }>): Promise<string> {
  if (!selector || selector.startsWith("-") || /[\0\r\n]/.test(selector)) throw new Error("The test image selector is invalid.");
  const result = await execute(["docker", "image", "inspect", "--format", "{{.Id}}", "--", selector]);
  const id = result.stdout.trim();
  if (result.exitCode !== 0 || !/^sha256:[a-f0-9]{64}$/.test(id)) {
    throw new Error("The test image requires one locally available immutable image identifier.");
  }
  return id;
}
