import { createHmac, randomBytes } from "node:crypto";

const SESSION = /^h-[a-z0-9-]+$/;
const LAUNCH = /^[0-9a-f]{16}$/;
const SHA256 = /^[0-9a-f]{64}$/;

export interface CollieRuntimePlan {
  session: string;
  launch: string;
  envelope_digest: string;
}

export interface CollieRuntimeAttestation extends CollieRuntimePlan {
  container_id: string;
  runtime_generation: string;
}

interface RuntimeRow extends CollieRuntimePlan {
  hasAuthority?: () => boolean;
  artifact_envelope?: string;
  container_id?: string;
  docker_envelope?: string;
  inspected_envelope_digest?: string;
  runtime_generation: string;
}

/**
 * Resident-boot-local proof that a Collie container belongs to the create
 * envelope this process prepared. The key and rows are deliberately
 * memory-only: after a resident restart an old Docker object can still be
 * inventoried for cleanup, but can never be adopted and started.
 */
export class CollieRuntimeRegistry {
  private readonly bootKey: Buffer;
  private readonly rows = new Map<string, RuntimeRow>();
  private sequence = 0;

  constructor(bootKey: Uint8Array = randomBytes(32)) {
    if (bootKey.byteLength < 32) {
      throw new Error("Collie runtime registry boot key must be at least 32 bytes");
    }
    this.bootKey = Buffer.from(bootKey);
  }

  /** HMAC rather than a bare hash: the canonical envelope contains projected
   * short-lived credential bytes, which must not become an offline oracle when
   * the digest crosses the resident control channel. */
  digestEnvelope(canonicalEnvelope: string): string {
    if (canonicalEnvelope.length === 0 ||
        Buffer.byteLength(canonicalEnvelope) > 1024 * 1024) {
      throw new Error("Collie runtime envelope is empty or oversized");
    }
    return createHmac("sha256", this.bootKey)
      .update("mc.collie-envelope.v1\0")
      .update(canonicalEnvelope)
      .digest("hex");
  }

  register(plan: CollieRuntimePlan, hasAuthority?: () => boolean): string {
    validatePlan(plan);
    const key = rowKey(plan.session, plan.launch);
    if (this.rows.has(key)) {
      throw new Error(`Collie runtime ${plan.session}/${plan.launch} is already registered`);
    }
    this.sequence += 1;
    const runtime_generation = createHmac("sha256", this.bootKey)
      .update(JSON.stringify({
        envelope_digest: plan.envelope_digest,
        launch: plan.launch,
        sequence: this.sequence,
        session: plan.session,
      }))
      .digest("hex");
    this.rows.set(key, { ...plan, hasAuthority, runtime_generation });
    return runtime_generation;
  }

  bind(attestation: CollieRuntimeAttestation & { docker_envelope?: string }): void {
    validateAttestation(attestation);
    const row = this.exactRow(attestation);
    if (row.container_id !== undefined && row.container_id !== attestation.container_id) {
      throw new Error(`Collie runtime ${attestation.session}/${attestation.launch} is already bound`);
    }
    row.container_id = attestation.container_id;
    if (attestation.docker_envelope !== undefined) {
      validateEnvelope(attestation.docker_envelope);
      row.docker_envelope = attestation.docker_envelope;
    }
  }

  sealArtifacts(
    expected: CollieRuntimeAttestation & { artifact_envelope: string },
  ): string {
    validateAttestation(expected);
    validateEnvelope(expected.artifact_envelope);
    const row = this.exactRow(expected);
    if (row.container_id !== expected.container_id || row.docker_envelope === undefined) {
      throw new Error(`Collie runtime ${expected.session}/${expected.launch} has no inspected container`);
    }
    row.artifact_envelope = expected.artifact_envelope;
    row.inspected_envelope_digest = this.digestEnvelope(JSON.stringify({
      artifacts: row.artifact_envelope,
      docker: row.docker_envelope,
      plan: row.envelope_digest,
    }));
    return row.inspected_envelope_digest;
  }

  attest(attestation: CollieRuntimeAttestation): boolean {
    try {
      validateAttestation(attestation);
      const row = this.rows.get(rowKey(attestation.session, attestation.launch));
      return row !== undefined &&
        row.hasAuthority?.() !== false &&
        row.runtime_generation === attestation.runtime_generation &&
        row.envelope_digest === attestation.envelope_digest &&
        row.container_id === attestation.container_id;
    } catch {
      return false;
    }
  }

  attestEnvelope(
    attestation: CollieRuntimeAttestation & {
      artifact_envelope?: string;
      docker_envelope: string;
    },
  ): { envelope_digest: string; runtime_generation: string } | null {
    try {
      validateAttestation(attestation);
      validateEnvelope(attestation.docker_envelope);
      if (attestation.artifact_envelope !== undefined) {
        validateEnvelope(attestation.artifact_envelope);
      }
      const row = this.rows.get(rowKey(attestation.session, attestation.launch));
      if (row === undefined ||
          row.hasAuthority?.() === false ||
          row.runtime_generation !== attestation.runtime_generation ||
          row.envelope_digest !== attestation.envelope_digest ||
          row.container_id !== attestation.container_id ||
          row.docker_envelope !== attestation.docker_envelope ||
          (attestation.artifact_envelope !== undefined &&
            row.artifact_envelope !== attestation.artifact_envelope) ||
          row.artifact_envelope === undefined ||
          row.inspected_envelope_digest === undefined) {
        return null;
      }
      return {
        envelope_digest: row.inspected_envelope_digest,
        runtime_generation: row.runtime_generation,
      };
    } catch {
      return null;
    }
  }

  revoke(plan: CollieRuntimePlan & { runtime_generation: string }): boolean {
    validateGeneration(plan.runtime_generation);
    const row = this.exactRow(plan);
    return this.rows.delete(rowKey(plan.session, plan.launch));
  }

  revokeObserved(session: string, launch: string, containerId: string): boolean {
    if (!SESSION.test(session) || !LAUNCH.test(launch) || !SHA256.test(containerId)) {
      throw new Error("Collie runtime observed identity is malformed");
    }
    const key = rowKey(session, launch);
    const row = this.rows.get(key);
    return row?.container_id === containerId && this.rows.delete(key);
  }

  revokeContainer(session: string, containerId: string): boolean {
    if (!SESSION.test(session) || !SHA256.test(containerId)) {
      throw new Error("Collie runtime container identity is malformed");
    }
    for (const [key, row] of this.rows) {
      if (row.session === session && row.container_id === containerId) {
        return this.rows.delete(key);
      }
    }
    return false;
  }

  revokeSession(session: string): number {
    if (!SESSION.test(session)) {
      throw new Error("Collie runtime session identity is malformed");
    }
    let removed = 0;
    for (const [key, row] of this.rows) {
      if (row.session === session && this.rows.delete(key)) removed += 1;
    }
    return removed;
  }

  private exactRow(
    expected: CollieRuntimePlan & { runtime_generation: string },
  ): RuntimeRow {
    validatePlan(expected);
    validateGeneration(expected.runtime_generation);
    const row = this.rows.get(rowKey(expected.session, expected.launch));
    if (row === undefined ||
        row.runtime_generation !== expected.runtime_generation ||
        row.envelope_digest !== expected.envelope_digest) {
      throw new Error(`Collie runtime ${expected.session}/${expected.launch} registration mismatch`);
    }
    return row;
  }
}

function rowKey(session: string, launch: string): string {
  return `${session}\0${launch}`;
}

function validatePlan(plan: CollieRuntimePlan): void {
  if (!SESSION.test(plan.session) ||
      !LAUNCH.test(plan.launch) ||
      !SHA256.test(plan.envelope_digest)) {
    throw new Error("Collie runtime plan is malformed");
  }
}

function validateGeneration(generation: string): void {
  if (!SHA256.test(generation)) {
    throw new Error("Collie runtime generation is malformed");
  }
}

function validateEnvelope(envelope: string): void {
  if (envelope.length === 0 || Buffer.byteLength(envelope) > 1024 * 1024) {
    throw new Error("Collie runtime canonical envelope is empty or oversized");
  }
}

function validateAttestation(attestation: CollieRuntimeAttestation): void {
  validatePlan(attestation);
  validateGeneration(attestation.runtime_generation);
  if (!SHA256.test(attestation.container_id)) {
    throw new Error("Collie runtime container id is malformed");
  }
}
