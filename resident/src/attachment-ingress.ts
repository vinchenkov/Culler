import type { AttachmentConfinementHealth } from "./attachment-health";
import type { Exec, ExecResult } from "./types";

const HEX_16 = /^[0-9a-f]{16}$/;
const HEX_32 = /^[0-9a-f]{32}$/;
const HEX_64 = /^[0-9a-f]{64}$/;
const DECIMAL_ID = /^[1-9][0-9]{0,18}$/;
const SQLITE_TIME = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/;
const MAX_INT64 = 9_223_372_036_854_775_807n;
const MAX_OBJECT_BYTES = 256 * 1024 * 1024;
const MAX_BATCH_BYTES = 512 * 1024 * 1024;

export interface AttachmentPublicationReservation {
  publication_id: string;
  owner_token: string;
  ordinal: number;
  size: number;
  media_type: string;
  display_name: string;
  sha256?: string;
}

export interface AttachmentIngressReservation {
  ingress_id: string;
  attempt_id: string;
  owner_token: string;
  batch_id: string;
  object_count: number;
  byte_count: number;
  lease_until: string;
  hard_deadline: string;
  publications: AttachmentPublicationReservation[];
  replayed: boolean;
}

export interface AttachmentPublicationRenewRequest {
  session: string;
  attempt_id: string;
  attempt_owner_token: string;
  publication_id: string;
  publication_owner_token: string;
}

export interface AttachmentPublicationLeaseReceipt {
  attempt_id: string;
  publication_id: string;
  lease_until: string;
  hard_deadline: string;
}

export interface AttachmentPublicationPublishRequest extends AttachmentPublicationRenewRequest {
  size: number;
}

export interface AttachmentPublicationReceipt {
  publication_id: string;
  sha256: string;
  size: number;
  state: "materialized";
  reused: boolean;
  device: string;
  inode: string;
  file_type: "regular";
}

export interface AttachmentAttemptAbortRequest {
  session: string;
  attempt_id: string;
  owner_token: string;
}

export interface AttachmentAttemptTerminalReceipt {
  attempt_id: string;
  ingress_id: string;
  batch_id: string;
  attempt: number;
  outcome: "requeued" | "failed";
  reason: "explicit_abort" | "hard_expired";
  receipt: string;
  replayed: boolean;
}

export interface AttachmentAttemptExpiryPage {
  receipts: AttachmentAttemptTerminalReceipt[];
  next?: string;
}

export interface AttachmentTerminalBatch {
  batch_id: string;
  ingress_id: string;
  attempt_id: string;
  session_id: string;
  outcome: "consumed" | "requeued" | "failed";
}

export interface AttachmentTerminalPage {
  batches: AttachmentTerminalBatch[];
  next?: string;
}

export interface AttachmentTerminalChore {
  startup(): Promise<void>;
  beforeTick(): Promise<void>;
}

function closedKeys(value: Record<string, unknown>, expected: string[]): boolean {
  return JSON.stringify(Object.keys(value).sort()) === JSON.stringify([...expected].sort());
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined;
}

function canonicalDecimalID(value: unknown): value is string {
  return typeof value === "string" && DECIMAL_ID.test(value) && BigInt(value) <= MAX_INT64;
}

function sqliteTime(value: unknown): value is string {
  return typeof value === "string" && SQLITE_TIME.test(value);
}

function parseResult(label: string, result: ExecResult): unknown {
  if (result.exitCode !== 0) {
    throw new Error(`${label} refused (exit ${result.exitCode}): ${result.stderr.trim()}`);
  }
  try {
    return JSON.parse(result.stdout);
  } catch {
    throw new Error(`${label} returned invalid JSON`);
  }
}

function privateFrame(value: Record<string, unknown>): Uint8Array {
  const body = Buffer.from(JSON.stringify(value), "utf8");
  if (body.length === 0 || body.length > 1024 * 1024) {
    throw new Error("attachment ingress private frame is outside its bound");
  }
  const frame = Buffer.allocUnsafe(body.length + 4);
  frame.writeUInt32BE(body.length, 0);
  body.copy(frame, 4);
  return frame;
}

function privateFrameWithBody(
  value: Record<string, unknown>,
  bytes: Uint8Array,
): Uint8Array {
  const body = Buffer.from(JSON.stringify(value), "utf8");
  if (body.length === 0 || body.length > 1024 * 1024) {
    throw new Error("attachment publication private frame is outside its bound");
  }
  const frame = Buffer.allocUnsafe(body.length + bytes.length + 4);
  frame.writeUInt32BE(body.length, 0);
  body.copy(frame, 4);
  Buffer.from(bytes.buffer, bytes.byteOffset, bytes.byteLength).copy(frame, body.length + 4);
  return frame;
}

function validMediaType(value: unknown): value is string {
  return typeof value === "string" && value.length >= 1 && value.length <= 255 &&
    /^[\x20-\x7e]+$/.test(value);
}

function validDisplayName(value: unknown): value is string {
  return typeof value === "string" && Buffer.byteLength(value, "utf8") >= 1 &&
    Buffer.byteLength(value, "utf8") <= 255 &&
    !value.includes("/") && !value.includes("\\") && !/[\x00-\x1f\x7f]/.test(value);
}

function parseReservation(value: unknown): AttachmentIngressReservation {
  const root = record(value);
  const keys = [
    "attempt_id", "batch_id", "byte_count", "hard_deadline", "ingress_id",
    "lease_until", "object_count", "owner_token", "publications", "replayed",
  ];
  if (root === undefined || !closedKeys(root, keys) ||
      typeof root.attempt_id !== "string" || !HEX_16.test(root.attempt_id) ||
      typeof root.owner_token !== "string" || !HEX_32.test(root.owner_token) ||
      !canonicalDecimalID(root.ingress_id) || !canonicalDecimalID(root.batch_id) ||
      !Number.isSafeInteger(root.object_count) ||
      (root.object_count as number) < 1 || (root.object_count as number) > 32 ||
      !Number.isSafeInteger(root.byte_count) ||
      (root.byte_count as number) < 0 || (root.byte_count as number) > MAX_BATCH_BYTES ||
      !sqliteTime(root.lease_until) || !sqliteTime(root.hard_deadline) ||
      root.lease_until > root.hard_deadline ||
      typeof root.replayed !== "boolean" || !Array.isArray(root.publications) ||
      root.publications.length !== root.object_count) {
    throw new Error("attachment ingress returned a malformed reservation receipt");
  }

  let total = 0;
  const publications: AttachmentPublicationReservation[] = [];
  for (let index = 0; index < root.publications.length; index++) {
    const publication = record(root.publications[index]);
    const baseKeys = [
      "display_name", "media_type", "ordinal", "owner_token", "publication_id", "size",
    ];
    const expectedKeys = publication?.sha256 === undefined ? baseKeys : [...baseKeys, "sha256"];
    if (publication === undefined || !closedKeys(publication, expectedKeys) ||
        typeof publication.publication_id !== "string" ||
        !HEX_16.test(publication.publication_id) ||
        typeof publication.owner_token !== "string" ||
        !HEX_32.test(publication.owner_token) ||
        publication.ordinal !== index + 1 ||
        !Number.isSafeInteger(publication.size) ||
        (publication.size as number) < 0 || (publication.size as number) > MAX_OBJECT_BYTES ||
        !validMediaType(publication.media_type) ||
        !validDisplayName(publication.display_name) ||
        (publication.sha256 !== undefined &&
          (typeof publication.sha256 !== "string" || !HEX_64.test(publication.sha256)))) {
      throw new Error("attachment ingress returned a malformed reservation receipt");
    }
    total += publication.size as number;
    publications.push(publication as unknown as AttachmentPublicationReservation);
  }
  if (total !== root.byte_count) {
    throw new Error("attachment ingress returned a malformed reservation receipt");
  }
  return { ...root, publications } as unknown as AttachmentIngressReservation;
}

function parseLeaseReceipt(
  value: unknown,
  expectedAttempt: string,
  expectedPublication: string,
): AttachmentPublicationLeaseReceipt {
  const root = record(value);
  if (root === undefined || !closedKeys(root, [
    "attempt_id", "hard_deadline", "lease_until", "publication_id",
  ]) ||
      root.attempt_id !== expectedAttempt ||
      root.publication_id !== expectedPublication ||
      !sqliteTime(root.lease_until) || !sqliteTime(root.hard_deadline) ||
      root.lease_until > root.hard_deadline) {
    throw new Error("attachment ingress returned a malformed renewal receipt");
  }
  return root as unknown as AttachmentPublicationLeaseReceipt;
}

function parsePublicationReceipt(
  value: unknown,
  expectedPublication: string,
  expectedSize: number,
): AttachmentPublicationReceipt {
  const root = record(value);
  if (root === undefined || !closedKeys(root, [
    "device", "file_type", "inode", "publication_id", "reused", "sha256",
    "size", "state",
  ]) ||
      root.publication_id !== expectedPublication ||
      typeof root.sha256 !== "string" || !HEX_64.test(root.sha256) ||
      root.size !== expectedSize ||
      root.state !== "materialized" ||
      typeof root.reused !== "boolean" ||
      typeof root.device !== "string" || !/^(0|[1-9][0-9]{0,19})$/.test(root.device) ||
      typeof root.inode !== "string" || !/^(0|[1-9][0-9]{0,19})$/.test(root.inode) ||
      root.file_type !== "regular") {
    throw new Error("attachment ingress returned a malformed publication receipt");
  }
  return root as unknown as AttachmentPublicationReceipt;
}

function parseTerminalReceipt(
  value: unknown,
  expectedAttempt?: string,
): AttachmentAttemptTerminalReceipt {
  const root = record(value);
  if (root === undefined || !closedKeys(root, [
    "attempt", "attempt_id", "batch_id", "ingress_id", "outcome",
    "reason", "receipt", "replayed",
  ]) ||
      typeof root.attempt_id !== "string" || !HEX_16.test(root.attempt_id) ||
      (expectedAttempt !== undefined && root.attempt_id !== expectedAttempt) ||
      !canonicalDecimalID(root.ingress_id) || !canonicalDecimalID(root.batch_id) ||
      !Number.isSafeInteger(root.attempt) ||
      (root.attempt as number) < 1 || (root.attempt as number) > 3 ||
      (root.outcome !== "requeued" && root.outcome !== "failed") ||
      (root.reason !== "explicit_abort" && root.reason !== "hard_expired") ||
      typeof root.receipt !== "string" ||
      Buffer.byteLength(root.receipt, "utf8") < 2 ||
      Buffer.byteLength(root.receipt, "utf8") > 16 * 1024 ||
      typeof root.replayed !== "boolean") {
    throw new Error("attachment ingress returned a malformed terminal receipt");
  }
  return root as unknown as AttachmentAttemptTerminalReceipt;
}

function parseExpiryPage(value: unknown): AttachmentAttemptExpiryPage {
  const root = record(value);
  const expected = root?.next === undefined ? ["receipts"] : ["next", "receipts"];
  if (root === undefined || !closedKeys(root, expected) ||
      !Array.isArray(root.receipts) || root.receipts.length > 128 ||
      (root.next !== undefined &&
        (typeof root.next !== "string" || !HEX_16.test(root.next)))) {
    throw new Error("attachment ingress returned a malformed expiry page");
  }
  const receipts = root.receipts.map((receipt) => parseTerminalReceipt(receipt));
  if ((receipts.length === 0) !== (root.next === undefined) ||
      (receipts.length > 0 &&
        root.next !== receipts[receipts.length - 1]?.attempt_id)) {
    throw new Error("attachment ingress returned a malformed expiry page");
  }
  return {
    receipts,
    ...(root.next === undefined ? {} : { next: root.next as string }),
  };
}

function parseTerminalPage(value: unknown): AttachmentTerminalPage {
  const root = record(value);
  const expected = root?.next === undefined ? ["batches"] : ["batches", "next"];
  if (root === undefined || !closedKeys(root, expected) ||
      !Array.isArray(root.batches) || root.batches.length > 128 ||
      (root.next !== undefined && !canonicalDecimalID(root.next))) {
    throw new Error("attachment ingress returned a malformed reconciliation page");
  }
  const batches: AttachmentTerminalBatch[] = [];
  for (const value of root.batches) {
    const batch = record(value);
    if (batch === undefined || !closedKeys(batch, [
      "attempt_id", "batch_id", "ingress_id", "outcome", "session_id",
    ]) ||
        typeof batch.attempt_id !== "string" || !HEX_16.test(batch.attempt_id) ||
        !canonicalDecimalID(batch.batch_id) || !canonicalDecimalID(batch.ingress_id) ||
        typeof batch.session_id !== "string" ||
        !privateAttachmentSession(batch.session_id) ||
        (batch.outcome !== "consumed" && batch.outcome !== "requeued" &&
          batch.outcome !== "failed")) {
      throw new Error("attachment ingress returned a malformed reconciliation page");
    }
    batches.push(batch as unknown as AttachmentTerminalBatch);
  }
  if ((batches.length === 0) !== (root.next === undefined) ||
      (batches.length > 0 && root.next !== batches[batches.length - 1]?.batch_id)) {
    throw new Error("attachment ingress returned a malformed reconciliation page");
  }
  return {
    batches,
    ...(root.next === undefined ? {} : { next: root.next as string }),
  };
}

export async function reserveAttachmentIngressViaMc(
  runMc: Exec,
  health: AttachmentConfinementHealth,
  session: string,
): Promise<AttachmentIngressReservation> {
  const generation = health.assertHealthy();
  const result = await runMc(["__attachment-ingress-reserve"], privateFrame({
    owner_generation: generation,
    session,
  }));
  return parseReservation(parseResult("attachment ingress reservation", result));
}

export async function renewAttachmentPublicationViaMc(
  runMc: Exec,
  health: AttachmentConfinementHealth,
  request: AttachmentPublicationRenewRequest,
): Promise<AttachmentPublicationLeaseReceipt> {
  const generation = health.assertHealthy();
  const result = await runMc(["__attachment-publication-renew"], privateFrame({
    attempt_id: request.attempt_id,
    attempt_owner_token: request.attempt_owner_token,
    owner_generation: generation,
    publication_id: request.publication_id,
    publication_owner_token: request.publication_owner_token,
    session: request.session,
  }));
  return parseLeaseReceipt(
    parseResult("attachment publication renewal", result),
    request.attempt_id,
    request.publication_id,
  );
}

export async function publishAttachmentPublicationViaMc(
  runMc: Exec,
  health: AttachmentConfinementHealth,
  request: AttachmentPublicationPublishRequest,
  bytes: Uint8Array,
): Promise<AttachmentPublicationReceipt> {
  const generation = health.assertHealthy();
  if (!privateAttachmentSession(request.session) ||
      !HEX_16.test(request.attempt_id) ||
      !HEX_32.test(request.attempt_owner_token) ||
      !HEX_16.test(request.publication_id) ||
      !HEX_32.test(request.publication_owner_token) ||
      !Number.isSafeInteger(request.size) ||
      request.size < 0 || request.size > MAX_OBJECT_BYTES ||
      bytes.byteLength !== request.size) {
    throw new Error("attachment publication request is malformed");
  }
  const result = await runMc(["__attachment-publication-publish"], privateFrameWithBody({
    attempt_id: request.attempt_id,
    attempt_owner_token: request.attempt_owner_token,
    owner_generation: generation,
    publication_id: request.publication_id,
    publication_owner_token: request.publication_owner_token,
    session: request.session,
    size: request.size,
  }, bytes));
  return parsePublicationReceipt(
    parseResult("attachment publication", result),
    request.publication_id,
    request.size,
  );
}

export async function abortAttachmentAttemptViaMc(
  runMc: Exec,
  health: AttachmentConfinementHealth,
  request: AttachmentAttemptAbortRequest,
): Promise<AttachmentAttemptTerminalReceipt> {
  const generation = health.assertHealthy();
  if (!privateAttachmentSession(request.session) ||
      !HEX_16.test(request.attempt_id) ||
      !HEX_32.test(request.owner_token)) {
    throw new Error("attachment attempt abort request is malformed");
  }
  const result = await runMc(["__attachment-attempt-abort"], privateFrame({
    attempt_id: request.attempt_id,
    owner_generation: generation,
    owner_token: request.owner_token,
    session: request.session,
  }));
  return parseTerminalReceipt(
    parseResult("attachment attempt abort", result),
    request.attempt_id,
  );
}

export async function expireDueAttachmentAttemptsViaMc(
  runMc: Exec,
  health: AttachmentConfinementHealth,
): Promise<AttachmentAttemptExpiryPage> {
  const generation = health.assertHealthy();
  const result = await runMc(["__attachment-attempts-expire-due"], privateFrame({
    after: "",
    limit: 128,
    owner_generation: generation,
  }));
  return parseExpiryPage(parseResult("attachment hard-expiry sweep", result));
}

export async function reconcileAttachmentTerminalsViaMc(
  runMc: Exec,
  health: AttachmentConfinementHealth,
): Promise<AttachmentTerminalPage> {
  const generation = health.assertHealthy();
  const result = await runMc(["__attachment-terminals-reconcile"], privateFrame({
    after_batch_id: "0",
    limit: 128,
    owner_generation: generation,
  }));
  return parseTerminalPage(
    parseResult("attachment terminal reconciliation", result),
  );
}

export function createAttachmentTerminalChore(
  runMc: Exec,
  health: AttachmentConfinementHealth,
): AttachmentTerminalChore {
  const sweep = async (): Promise<void> => {
    await expireDueAttachmentAttemptsViaMc(runMc, health);
    await reconcileAttachmentTerminalsViaMc(runMc, health);
  };
  return {
    startup: sweep,
    beforeTick: sweep,
  };
}

function privateAttachmentSession(value: string): boolean {
  return /^h-[a-z0-9-]{1,253}$/.test(value) && value.length <= 255;
}
