import { constants, lstat, open, readdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { isProviderToken, maskProviderTokens, PROVIDER_TOKEN_LENGTH } from "../../shared/provider-token";

export interface ProviderAccess {
  version: 1;
  url: string;
  token: string;
  ca: string;
  harness: "codex" | "claude-sdk";
  authMode: "api-key" | "account";
}

export function parseProviderAccess(body: string, harness: ProviderAccess["harness"]): ProviderAccess {
  if (Buffer.byteLength(body) > 64 * 1024) throw new Error("The provider access file exceeds its limit.");
  let value: ProviderAccess;
  try { value = JSON.parse(body); } catch { throw new Error("The provider access file contains invalid JSON."); }
  const fields = ["version", "url", "token", "ca", "harness", "authMode"];
  if (!value || typeof value !== "object" || Array.isArray(value) ||
      Object.keys(value).some(key => !fields.includes(key)) || value.version !== 1 ||
      value.harness !== harness || !["api-key", "account"].includes(value.authMode) ||
      typeof value.token !== "string" || !isProviderToken(value.token) ||
      typeof value.ca !== "string" || !value.ca.includes("-----BEGIN CERTIFICATE-----") ||
      value.ca.includes("PRIVATE KEY") || typeof value.url !== "string") {
    throw new Error("The provider access file has an invalid contract.");
  }
  let url: URL;
  try { url = new URL(value.url); } catch { throw new Error("The host proxy address is invalid."); }
  if (url.protocol !== "https:" || url.username || url.password || url.search || url.hash) {
    throw new Error("The host proxy requires an HTTPS address without credentials.");
  }
  return value;
}

export async function loadProviderAccess(harness: ProviderAccess["harness"], env = process.env): Promise<ProviderAccess> {
  const file = env.MC_PROVIDER_ACCESS_FILE;
  if (!file || !file.startsWith("/")) throw new Error("The provider access file is unavailable.");
  return parseProviderAccess(await readFile(file, "utf8"), harness);
}

export async function writeProviderCA(access: ProviderAccess, directory: string): Promise<string> {
  const path = join(directory, "provider-ca.pem");
  await writeFile(path, access.ca, { mode: 0o400, flag: "wx" });
  return path;
}

/** Equal-length masks preserve native transcript offsets. The harness must stop before this pass. */
export async function redactNativeAccess(root: string, token: string): Promise<void> {
  if (!token) return;
  const secret = Buffer.from(token);
  const chunk = 1 << 20;
  let files = 0;
  async function walk(path: string, depth: number): Promise<void> {
    if (depth > 8) throw new Error("The native session exceeds its folder limit.");
    const info = await lstat(path);
    if (info.isSymbolicLink()) throw new Error("The native session contains a symbolic link.");
    if (info.isDirectory()) {
      for (const name of await readdir(path)) await walk(join(path, name), depth + 1);
      return;
    }
    if (!path.endsWith(".jsonl")) return;
    if (!info.isFile() || ++files > 256) throw new Error("The native session exceeds its file limit.");
    const handle = await open(path, constants.O_RDWR | constants.O_NOFOLLOW | constants.O_NONBLOCK);
    try {
      const size = (await handle.stat()).size;
      for (let position = 0; position < size; position += chunk) {
        const bytes = Buffer.alloc(Math.min(chunk + Math.max(secret.length, PROVIDER_TOKEN_LENGTH) - 1, size - position));
        const { bytesRead } = await handle.read(bytes, 0, bytes.length, position);
        const original = bytes.subarray(0, bytesRead);
        const body = maskProviderTokens(original);
        let changed = !body.equals(original);
        for (let index = body.indexOf(secret); index >= 0; index = body.indexOf(secret, index + secret.length)) {
          body.fill(0x2a, index, index + secret.length);
          changed = true;
        }
        if (changed) {
          let written = 0;
          while (written < body.length) written += (await handle.write(body, written, body.length - written, position + written)).bytesWritten;
        }
      }
      await handle.sync();
    } finally { await handle.close(); }
  }
  await walk(root, 0);
}

export function codexProviderArgs(access: ProviderAccess): string[] {
  return [
    "--config", 'model_provider="culler"',
    "--config", 'model_providers.culler.name="Culler"',
    "--config", `model_providers.culler.base_url=${JSON.stringify(access.url)}`,
    "--config", 'model_providers.culler.wire_api="responses"',
    "--config", 'model_providers.culler.env_key="CULLER_PROXY_TOKEN"',
    "--config", "model_providers.culler.requires_openai_auth=false",
    "--config", "model_providers.culler.supports_websockets=false",
    "--config", "model_providers.culler.request_max_retries=0",
    "--config", "model_providers.culler.stream_max_retries=0",
  ];
}
