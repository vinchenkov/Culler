import { expect, test } from "bun:test";
import { createTraceMask } from "./trace-mask";

const token = "culler_proxy_" + "Az09_-".repeat(8).slice(0, 43);
const unicode = (s: string) => [...s].map(c => `\\u${c.charCodeAt(0).toString(16).padStart(4, "0")}`).join("");
const percent = (s: string) => [...Buffer.from(s)].map(b => `%${b.toString(16).padStart(2, "0")}`).join("");

test("only known credentials and the complete Culler format authorize masks", () => {
  const secret = "exact-key+value/with=padding";
  const opaque = "AAAA-sk-" + "a".repeat(140);
  const signature = Buffer.from("sk-" + "b".repeat(80)).toString("base64");
  const model = JSON.stringify({ encrypted_content: opaque, thinking: "context", signature, data: signature });
  expect(createTraceMask([secret]).apply(Buffer.from(model)).toString()).toBe(model);
  for (const value of [token, secret]) {
    for (const encoded of [value, unicode(value), unicode(value).replaceAll("\\", "\\\\"), percent(value), Buffer.from(value).toString("base64"), Buffer.from(value).toString("base64url")]) {
      const original = Buffer.from(`{"text":"${encoded}"}\n`);
      const result = createTraceMask([secret]).apply(original);
      expect(result.length).toBe(original.length);
      expect(result.toString()).not.toContain(encoded);
      expect(result.toString()).toContain("***");
      expect(() => JSON.parse(result.toString())).not.toThrow();
      expect(original.toString()).toContain(encoded);
    }
  }
  expect(createTraceMask().apply(Buffer.from(secret)).toString()).toBe(secret);
});

test("expired tokens remain recognizable in Base64 at every byte alignment", () => {
  for (const prefix of ["", "x", "xy", "xyz"]) {
    const encoded = Buffer.from(prefix + token + " suffix").toString("base64");
    const result = createTraceMask().apply(Buffer.from(encoded)).toString();
    expect(result).not.toBe(encoded);
    expect(result).toContain("***");
    expect(result.length).toBe(encoded.length);
  }
});
