import { createHash } from "node:crypto";
import { isProviderToken, PROVIDER_TOKEN_PREFIX, PROVIDER_TOKEN_LENGTH } from "../../runner/shared/provider-token";

export interface TraceMask {
  revision: string;
  overlap: number;
  apply(value: Buffer): Buffer;
}

const escape = (value: string) => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
const hexPattern = (value: string) => value.replace(/[a-f]/g, c => `[${c}${c.toUpperCase()}]`);
const encodedCharacter = (char: string) => {
  const hex = char.charCodeAt(0).toString(16).padStart(2, "0");
  return `(?:${escape(char)}|\\\\+u00${hexPattern(hex)}|%${hexPattern(hex)})`;
};
const tokenPattern = new RegExp([...PROVIDER_TOKEN_PREFIX].map(encodedCharacter).join("") +
  "(?:[A-Za-z0-9_-]|\\\\+u00[0-9a-fA-F]{2}|%[0-9a-fA-F]{2}){43}", "g");

// These markers identify the Culler format before any Base64 decode.
const base64Markers = [0, 1, 2].map(offset => {
  const encoded = Buffer.from(" ".repeat(offset) + PROVIDER_TOKEN_PREFIX).toString("base64");
  const start = Math.ceil(offset * 8 / 6);
  const marker = encoded.slice(start, Math.floor((offset + PROVIDER_TOKEN_PREFIX.length) * 8 / 6));
  return { offset, start, marker, length: Math.ceil((offset + PROVIDER_TOKEN_LENGTH) * 8 / 6) };
});

/** The mask changes copies only. A key-shaped string supplies no credential evidence. */
export function createTraceMask(credentials: readonly string[] = []): TraceMask {
  const values = [...new Set(credentials.filter(Boolean))].sort();
  const patterns = values.flatMap(value => {
    const bytes = Buffer.from(value);
    const variants = new Set([value, JSON.stringify(value).slice(1, -1), encodeURIComponent(value),
      bytes.toString("base64"), bytes.toString("base64url"), bytes.toString("base64").replace(/=+$/, "")]);
    return [new RegExp([...bytes.toString("latin1")].map(encodedCharacter).join(""), "g"),
      ...[...variants].map(item => new RegExp(escape(Buffer.from(item).toString("latin1")), "g"))];
  });
  return {
    revision: createHash("sha256").update(JSON.stringify([1, values])).digest("hex"),
    overlap: Math.max(PROVIDER_TOKEN_LENGTH * 6, ...values.map(value => Buffer.byteLength(value) * 6)),
    apply(value) {
      const text = value.toString("latin1");
      const out = Buffer.from(value);
      const mask = (start: number, length: number) => out.fill(0x2a, start, start + length);
      for (const pattern of patterns) {
        for (const match of text.matchAll(pattern)) mask(match.index!, match[0].length);
      }
      for (const match of text.matchAll(tokenPattern)) {
        const decoded = match[0].replace(/\\+u00([0-9a-f]{2})|%([0-9a-f]{2})/gi,
          (_, unicode, percent) => String.fromCharCode(parseInt(unicode ?? percent, 16)));
        if (isProviderToken(decoded)) mask(match.index!, match[0].length);
      }
      for (const { offset, start, marker, length } of base64Markers) {
        for (let at = text.indexOf(marker); at >= 0; at = text.indexOf(marker, at + marker.length)) {
          const from = at - start;
          if (from < 0) continue;
          const candidate = text.slice(from, from + length);
          if (candidate.length !== length || !/^[A-Za-z0-9+/_-]+$/.test(candidate)) continue;
          const decoded = Buffer.from(candidate, "base64").subarray(offset, offset + PROVIDER_TOKEN_LENGTH).toString("latin1");
          if (isProviderToken(decoded)) mask(from, length);
        }
      }
      return out;
    },
  };
}

export const defaultTraceMask = createTraceMask();
