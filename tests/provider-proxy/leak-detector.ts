import { gunzipSync } from "node:zlib";

export interface LeakFinding { surface: string; marker: number }

export function detectLeaks(surface: string, bytes: Uint8Array, markers: readonly string[]): LeakFinding[] {
  const body = Buffer.from(bytes);
  return markers.flatMap((marker, index) => marker && body.includes(Buffer.from(marker)) ? [{ surface, marker: index }] : []);
}

/** Docker image archives can contain compressed layer archives. */
export function detectArchiveLeaks(surface: string, bytes: Uint8Array, markers: readonly string[], depth = 0): LeakFinding[] {
  if (depth > 6 || bytes.byteLength > 64 * 1024 * 1024) throw new Error("The synthetic archive exceeds its limit.");
  const body = Buffer.from(bytes);
  const findings = detectLeaks(surface, body, markers);
  if (body[0] === 0x1f && body[1] === 0x8b) {
    findings.push(...detectArchiveLeaks(`${surface}:gzip`, gunzipSync(body, { maxOutputLength: 64 * 1024 * 1024 }), markers, depth + 1));
    return findings;
  }
  if (body.length < 512 || body.subarray(257, 262).toString() !== "ustar") return findings;
  for (let offset = 0; offset + 512 <= body.length;) {
    const name = body.subarray(offset, offset + 100).toString().replace(/\0.*$/, "");
    if (!name) break;
    const size = Number.parseInt(body.subarray(offset + 124, offset + 136).toString().replace(/\0.*$/, "").trim(), 8);
    if (!Number.isSafeInteger(size) || size < 0 || offset + 512 + size > body.length) throw new Error("The synthetic archive is malformed.");
    if (size) findings.push(...detectArchiveLeaks(`${surface}:${name}`, body.subarray(offset + 512, offset + 512 + size), markers, depth + 1));
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  return findings;
}

export function requireNoLeaks(findings: readonly LeakFinding[]): void {
  if (findings.length) throw new Error(`Credential markers reached these surfaces: ${[...new Set(findings.map(value => value.surface))].join(", ")}`);
}
