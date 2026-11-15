import type { AttachmentCanaryReceipt } from "./types";

const HEX_32 = /^[0-9a-f]{32}$/;
const HEX_64 = /^[0-9a-f]{64}$/;
const DENIALS = [
  "create:EACCES",
  "open:EACCES",
  "rename:EACCES",
  "traverse:EACCES",
  "unlink:EACCES",
] as const;

/** Boot-local capability minted only from this resident process's successful
 * composed canary command. Persisted diagnostics are never read here. */
export class AttachmentConfinementHealth {
  #receipt?: AttachmentCanaryReceipt;

  markHealthy(value: unknown): AttachmentCanaryReceipt {
    if (this.#receipt !== undefined) {
      throw new Error("attachment confinement health is already bound for this boot");
    }
    if (value === null || typeof value !== "object" || Array.isArray(value)) {
      throw new Error("attachment canary returned a malformed receipt");
    }
    const record = value as Record<string, unknown>;
    const keys = Object.keys(record).sort();
    const expectedKeys = [
      "denied_outbound", "effective_uid", "fs_uid", "generation",
      "host_outbound_read", "inbound_sha256", "outside_sessions",
      "outbound_sha256", "private_gate_healthy", "real_uid", "restart_inbound_read",
      "restart_outbound_read", "saved_uid", "version", "warm_inbound_read",
    ].sort();
    if (JSON.stringify(keys) !== JSON.stringify(expectedKeys) ||
        record.version !== 1 ||
        typeof record.generation !== "string" || !HEX_32.test(record.generation) ||
        typeof record.inbound_sha256 !== "string" || !HEX_64.test(record.inbound_sha256) ||
        typeof record.outbound_sha256 !== "string" || !HEX_64.test(record.outbound_sha256) ||
        record.real_uid !== 10002 || record.effective_uid !== 10001 ||
        record.saved_uid !== 10001 || record.fs_uid !== 10001 ||
        record.warm_inbound_read !== true || record.host_outbound_read !== true ||
        record.private_gate_healthy !== true ||
        record.restart_inbound_read !== true || record.restart_outbound_read !== true ||
        record.outside_sessions !== true ||
        !Array.isArray(record.denied_outbound) ||
        JSON.stringify(record.denied_outbound) !== JSON.stringify(DENIALS)) {
      throw new Error("attachment canary returned a malformed or red receipt");
    }
    this.#receipt = value as AttachmentCanaryReceipt;
    return this.#receipt;
  }

  assertHealthy(): string {
    if (this.#receipt === undefined) {
      throw new Error("attachment confinement is unhealthy for this resident boot");
    }
    return this.#receipt.generation;
  }
}
