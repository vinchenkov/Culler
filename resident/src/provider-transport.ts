// DestinationPolicy execution surface (PS §2.1-2.5): the single outbound
// transport for provider traffic. Every connection attempt validates at
// resolve time against the canonical destination, TLS is pinned to the same
// canonical host, connections are never pooled or reused, redirects are never
// followed, and no process-level HTTP(S)/SOCKS proxy is inherited — the
// node:https client path ignores environment proxies by construction.
import * as https from "node:https";
import * as net from "node:net";
import * as dns from "node:dns";
import type { IncomingMessage } from "node:http";

export interface DestinationPolicy {
  // The ports https may run on for this deployment (§2.1 scheme×port matching).
  readonly portAllowlist: readonly number[];
  // Optional in-entry relaxation (§2.2): a hit widens within the entry only;
  // a malformed or non-canonical entry is refused at transport build time.
  readonly allowCidrs?: readonly string[];
}

export class DestinationError extends Error {
  constructor(public readonly code: string) {
    super(`destination_${code}`);
  }
}

type Cidr = { base: bigint; bits: number; width: number };
type BuiltPolicy = DestinationPolicy & { parsedCidrs: readonly Cidr[] };

const strictIPv4 = /^(?:0|[1-9][0-9]{0,2})(?:\.(?:0|[1-9][0-9]{0,2})){3}$/;

function isCanonicalIPv4(value: string): boolean {
  if (!strictIPv4.test(value)) return false;
  return value.split(".").every(part => Number(part) <= 255);
}

// Unmaps an IPv4-mapped IPv6 address (the dotted form or the WHATWG hex form)
// to its pure IPv4 text; §2.1 judges mapped values with the IPv4 policy.
function unmapV4(address: string): string {
  const lower = address.toLowerCase();
  const dotted = lower.match(/^::ffff:(\d{1,3}(?:\.\d{1,3}){3})$/);
  if (dotted?.[1] !== undefined) return dotted[1];
  const hex = lower.match(/^::ffff:([0-9a-f]{1,4}):([0-9a-f]{1,4})$/);
  if (hex?.[1] !== undefined && hex[2] !== undefined) {
    const high = Number.parseInt(hex[1], 16);
    const low = Number.parseInt(hex[2], 16);
    if (high <= 0xffff && low <= 0xffff) return `${high >> 8}.${high & 0xff}.${low >> 8}.${low & 0xff}`;
  }
  return address;
}

function authorityHost(raw: string): string {
  const match = raw.match(/^[A-Za-z][A-Za-z0-9+.-]*:\/\/((?:[^/?#]*)?)/);
  if (!match) throw new DestinationError("unparseable");
  let host = match[1] ?? "";
  const at = host.lastIndexOf("@");
  if (at >= 0) host = host.slice(at + 1);
  const colon = host.lastIndexOf(":");
  const bracket = host.lastIndexOf("]");
  if (colon > bracket) host = host.slice(0, colon);
  return host;
}

// canonicalDestination computes the canonical destination with the WHATWG
// URL as the single parser (§2.1: case folding, punycode, and trailing-dot
// removal happen only the way that one parser does them; there is no second
// lenient opinion). IP literal authorities are held to the strict input
// rules: WHATWG's radix tolerance (0x7f, octal, decimal, short counts) is
// exactly the parser-difference surface §2.1 closes, so an IPv4 destination
// must arrive already dotted-decimal canonical. A v4-mapped IPv6 value is
// unmapped and judged with the IPv4 rules.
export function canonicalDestination(raw: string, policy: DestinationPolicy): { url: URL; host: string; port: number } {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    throw new DestinationError("unparseable");
  }
  if (url.protocol !== "https:") throw new DestinationError("scheme");
  if (url.username !== "" || url.password !== "") throw new DestinationError("userinfo");
  const host = url.hostname; // bracketed for IPv6
  const original = authorityHost(raw);
  if (host.startsWith("[")) {
    const inner = host.slice(1, -1);
    if (inner.includes("%")) throw new DestinationError("zone_id");
    const pure = unmapV4(inner);
    if (pure !== inner) {
      if (!isCanonicalIPv4(pure)) throw new DestinationError("ipv4_form");
      return finish(url, pure, policy);
    }
    if (inner !== normalizeIPv6(inner)) throw new DestinationError("ipv6_form");
    if (original !== host) throw new DestinationError("non_canonical_host"); // case-strict: canonical IPv6 text is lowercase
    return finish(url, host, policy);
  }
  if (net.isIPv4(host)) {
    if (!isCanonicalIPv4(original) || original !== host) throw new DestinationError("ipv4_form");
    return finish(url, host, policy);
  }
  // §2.1 trailing-dot stripping: the canonical host is the parser's host text
  // with one trailing dot removed; input may carry either form, nothing else.
  const canonicalHost = host.endsWith(".") ? host.slice(0, -1) : host;
  const lowered = original.toLowerCase();
  if (lowered !== canonicalHost && lowered !== `${canonicalHost}.`) throw new DestinationError("non_canonical_host");
  if (!/^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/.test(canonicalHost)) throw new DestinationError("non_canonical_host");
  for (const label of canonicalHost.split(".")) {
    if (label === "" || label.length > 63 || label.startsWith("-") || label.endsWith("-")) throw new DestinationError("non_canonical_host");
  }
  return finish(url, canonicalHost, policy);
}

// normalizeIPv6 returns the RFC 5952 serialization the URL parser produces.
function normalizeIPv6(value: string): string {
  try {
    return new URL(`https://[${value}]/`).hostname.slice(1, -1);
  } catch {
    return value;
  }
}

function finish(url: URL, host: string, policy: DestinationPolicy): { url: URL; host: string; port: number } {
  const port = url.port === "" ? 443 : Number(url.port);
  if (!Number.isInteger(port) || !policy.portAllowlist.includes(port)) throw new DestinationError("port_denied");
  return { url, host, port };
}

// --- CIDR membership (v4 and v6, canonical entries only) ---

function parseCidr(entry: string): Cidr | null {
  const [network, prefixText] = entry.split("/");
  if (network === undefined || prefixText === undefined || !/^\d+$/.test(prefixText)) return null;
  const prefix = Number(prefixText);
  if (net.isIPv4(network)) {
    if (prefix > 32 || !isCanonicalIPv4(network)) return null;
    const value = ipv4ToBigInt(network);
    if ((value & mask(32 - prefix)) !== 0n) return null; // host bits set
    return { base: value, bits: prefix, width: 32 };
  }
  if (net.isIPv6(network)) {
    if (prefix > 128) return null;
    const value = ipv6ToBigInt(network);
    if ((value & mask(128 - prefix)) !== 0n) return null;
    return { base: value, bits: prefix, width: 128 };
  }
  return null;
}

function mask(bits: number): bigint {
  if (bits <= 0) return 0n;
  return (1n << BigInt(bits)) - 1n;
}

function ipv4ToBigInt(value: string): bigint {
  return value.split(".").reduce((acc, part) => (acc << 8n) | BigInt(Number(part)), 0n);
}

function ipv6ToBigInt(value: string): bigint {
  const [head, tail] = value.split("::");
  const groupsOf = (text: string | undefined): bigint[] => {
    if (text === undefined || text === "") return [];
    return text.split(":").flatMap(segment => {
      if (!segment.includes(".")) return [BigInt(`0x${segment}`)];
      // Embedded IPv4 (e.g. ::ffff:10.0.0.0) occupies the last two groups.
      if (!isCanonicalIPv4(segment)) throw new DestinationError("ipv6_form");
      const octets = segment.split(".").map(Number);
      const [a, b, c, d] = octets as [number, number, number, number];
      return [BigInt(a * 256 + b), BigInt(c * 256 + d)];
    });
  };
  let groups: bigint[];
  if (tail === undefined) {
    groups = groupsOf(head);
  } else {
    const left = groupsOf(head);
    const right = groupsOf(tail);
    groups = [...left, ...Array<bigint>(8 - left.length - right.length).fill(0n), ...right];
  }
  if (groups.length !== 8 || groups.some(group => group > 0xffffn)) throw new DestinationError("ipv6_form");
  return groups.reduce((acc, group) => (acc << 16n) | group, 0n);
}

function addressAllowed(address: string, cidrs: readonly Cidr[]): boolean {
  const pure = unmapV4(address);
  let value: bigint;
  let width: number;
  if (net.isIPv4(pure)) {
    value = ipv4ToBigInt(pure);
    width = 32;
  } else if (net.isIPv6(pure)) {
    value = ipv6ToBigInt(pure);
    width = 128;
  } else {
    return false;
  }
  return cidrs.some(entry => entry.width === width
    && (value >> BigInt(width - entry.bits)) === (entry.base >> BigInt(width - entry.bits)));
}

export function buildDestinationPolicy(policy: DestinationPolicy): BuiltPolicy {
  const parsedCidrs = (policy.allowCidrs ?? []).map(entry => {
    const parsed = parseCidr(entry);
    if (parsed === null) throw new DestinationError("cidr_entry");
    return parsed;
  });
  return { ...policy, parsedCidrs };
}

// --- resolve-time enforcement (§2.2: every attempt passes the policy at its
// own connect; the address dialed is the address judged) ---

type LookupCallback = (error: Error | null, address: unknown, family?: number) => void;

function policyLookup(policy: BuiltPolicy) {
  return (hostname: string, options: { all?: boolean } | LookupCallback, callback?: LookupCallback): void => {
    const done = typeof options === "function" ? options : callback;
    const wantAll = typeof options === "object" && options.all === true;
    if (!done) return;
    const literal = hostname.replace(/^\[|\]$/g, "");
    if (net.isIP(literal) !== 0) {
      // A literal destination never reaches a resolver; judge it directly.
      const pure = unmapV4(literal);
      const ok = policy.parsedCidrs.length === 0 || addressAllowed(pure, policy.parsedCidrs);
      if (!ok) {
        done(new DestinationError("no_permitted_address"), undefined as never);
        return;
      }
      const family = net.isIPv4(pure) ? 4 : 6;
      done(null, wantAll ? [{ address: hostname, family }] : hostname, family);
      return;
    }
    void dns.promises.lookup(hostname, { all: true, verbatim: true }).then(addresses => {
      const permitted = addresses.filter(entry => policy.parsedCidrs.length === 0 || addressAllowed(entry.address, policy.parsedCidrs));
      const first = permitted[0];
      if (permitted.length === 0 || (!wantAll && first === undefined)) {
        done(new DestinationError("no_permitted_address"), undefined as never);
        return;
      }
      if (wantAll) {
        done(null, permitted.map(entry => ({ address: entry.address, family: entry.family })));
        return;
      }
      done(null, first.address, first.family);
    }).catch(error => done(error instanceof Error ? error : new DestinationError("resolve_failed"), undefined as never));
  };
}

// --- fetch-shaped transport ---

type TransportOverrides = Pick<https.RequestOptions, "ca">;

export function providerTransport(policy: DestinationPolicy, overrides: TransportOverrides = {}): typeof fetch {
  const built = buildDestinationPolicy(policy);
  return async (input: string | URL | Request, init: RequestInit = {}): Promise<Response> => {
    if (typeof input !== "string" && !(input instanceof URL)) throw new DestinationError("url_form");
    const raw = typeof input === "string" ? input : input.href;
    const { url, host, port } = canonicalDestination(raw, built);
    // An IP-literal destination never reaches the resolver, so the §2.2
    // entry filter applies here, at the one point that sees the dialed
    // address before the socket opens.
    if (net.isIP(host) !== 0 && built.parsedCidrs.length > 0 && !addressAllowed(host, built.parsedCidrs)) {
      throw new DestinationError("no_permitted_address");
    }
    const method = init.method ?? "GET";
    if (!["GET", "POST", "PUT", "DELETE", "HEAD"].includes(method)) throw new DestinationError("method_form");
    if (init.body !== undefined && typeof init.body !== "string" && !(init.body instanceof Uint8Array) && !(init.body instanceof ArrayBuffer)) throw new DestinationError("request_body_form");
    const headers: Record<string, string> = {};
    new Headers(init.headers ?? {}).forEach((value, name) => {
      headers[name] = value;
    });
    return await new Promise<Response>((resolve, reject) => {
      const request = https.request({
        ...overrides,
        method,
        path: `${url.pathname}${url.search}`,
        // The dial target is the canonical host from this very request, and
        // the lookup hook is the last word before the socket connects.
        hostname: host,
        port,
        servername: net.isIP(host) === 0 ? host : undefined,
        lookup: policyLookup(built) as never,
        agent: false,
        rejectUnauthorized: true,
        headers,
      }, (message: IncomingMessage) => {
        resolve(webResponse(message, request));
      });
      request.on("error", reject);
      const signal = init.signal;
      if (signal) {
        if (signal.aborted) request.destroy(new DestinationError("aborted"));
        else signal.addEventListener("abort", () => request.destroy(new DestinationError("aborted")), { once: true });
      }
      if (init.body instanceof Uint8Array) request.end(Buffer.from(init.body));
      else if (init.body instanceof ArrayBuffer) request.end(Buffer.from(new Uint8Array(init.body)));
      else if (typeof init.body === "string") request.end(init.body);
      else request.end();
    });
  };
}

function webResponse(message: IncomingMessage, request: https.ClientRequest): Response {
  const headers = new Headers();
  for (const [name, value] of Object.entries(message.headers)) {
    if (Array.isArray(value)) for (const item of value) headers.append(name, item);
    else if (value !== undefined) headers.append(name, value);
  }
  // Integrity over event ordering: this runtime can surface `end` before the
  // abort of a truncated message, so the stream closes only at `close`, and
  // only when the parser completed the message. A truncated body fails the
  // reader instead of masquerading as a clean end.
  let settled = false;
  let failure: unknown;
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      message.on("data", chunk => {
        if (!settled) controller.enqueue(new Uint8Array(chunk));
      });
      message.on("aborted", () => {
        failure ??= new DestinationError("aborted");
      });
      message.on("error", error => {
        failure ??= error;
      });
      message.on("close", () => {
        if (settled) return;
        settled = true;
        if (message.complete && failure === undefined) controller.close();
        else controller.error(failure instanceof Error ? failure : new DestinationError("aborted"));
      });
    },
    cancel() {
      settled = true;
      request.destroy();
    },
  });
  const nullBody = message.statusCode === 204 || message.statusCode === 304;
  return new Response(nullBody ? null : body, {
    status: message.statusCode ?? 0,
    statusText: message.statusMessage ?? "",
    headers,
  });
}
