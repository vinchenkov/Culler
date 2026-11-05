export const PROVIDER_TOKEN_PREFIX = "culler_proxy_";
export const PROVIDER_TOKEN_LENGTH = PROVIDER_TOKEN_PREFIX.length + 43;

export function isProviderToken(value: string): boolean {
  return /^culler_proxy_[A-Za-z0-9_-]{43}$/.test(value);
}

export function redactProviderTokens(value: string): string {
  return value.replace(/culler_proxy_[A-Za-z0-9_-]{43}/g, token => "*".repeat(token.length));
}

/** Equal-length masks preserve the source offsets of trace copies. */
export function maskProviderTokens(value: Buffer): Buffer {
  const result = Buffer.from(value);
  const prefix = Buffer.from(PROVIDER_TOKEN_PREFIX);
  for (let index = result.indexOf(prefix); index >= 0; index = result.indexOf(prefix, index + prefix.length)) {
    const end = index + PROVIDER_TOKEN_LENGTH;
    if (end <= result.length && isProviderToken(result.subarray(index, end).toString("latin1"))) {
      result.fill(0x2a, index, end);
    }
  }
  return result;
}
