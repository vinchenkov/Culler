// network-error.ts — the class of a request that received no provider response.
//
// The resident classifies a failed token request, and the runners classify a
// failed harness request. A network error is a fact about the host network,
// so it never describes the credential or the task.

export type NetworkErrorClass = "connect" | "reset" | "timeout" | "tls" | "other";

const codes: Record<string, NetworkErrorClass> = {
  ConnectionRefused: "connect", ECONNREFUSED: "connect", FailedToOpenSocket: "connect", ENOTFOUND: "connect",
  EAI_AGAIN: "connect", ENETUNREACH: "connect", ENETDOWN: "connect", EHOSTUNREACH: "connect", EHOSTDOWN: "connect",
  ERR_PROXY_TUNNEL: "connect",
  ECONNRESET: "reset", EPIPE: "reset", ECONNABORTED: "reset", ConnectionClosed: "reset", ERR_SOCKET_CLOSED: "reset",
  UND_ERR_SOCKET: "reset", StreamSuspended: "reset", StreamNoResponse: "reset",
  ETIMEDOUT: "timeout", TimeoutError: "timeout",
};

const tlsCode = /^(?:CERT_|ERR_TLS_|ERR_SSL_|UNABLE_TO_GET_ISSUER_CERT|UNABLE_TO_VERIFY_LEAF_SIGNATURE|DEPTH_ZERO_SELF_SIGNED_CERT|SELF_SIGNED_CERT_IN_CHAIN|HOSTNAME_MISMATCH)/;

/** The class of an error code, or undefined when the code names no network failure. */
export function networkErrorFromCode(code: unknown): NetworkErrorClass | undefined {
  if (typeof code !== "string") return undefined;
  if (Object.hasOwn(codes, code)) return codes[code];
  return tlsCode.test(code) ? "tls" : undefined;
}

/** The class of a failed fetch. A fetch failure without a known code is still a network failure. */
export function networkErrorFromFetch(error: unknown): NetworkErrorClass {
  const { name, code } = (error ?? {}) as { name?: unknown; code?: unknown };
  if (name === "TimeoutError" || name === "AbortError") return "timeout";
  return networkErrorFromCode(code) ?? "other";
}

// Harness texts name the failure, for example "Connection refused (ECONNREFUSED)"
// or "error trying to connect: tcp connect error: Connection refused".
const phrases: Array<[RegExp, NetworkErrorClass]> = [
  [/unable to connect|connection refused|error trying to connect|dns error|failed to lookup address|can't reach the api server|no internet route|network is unreachable|no route to host/i, "connect"],
  [/connection (?:reset|dropped|closed before)|stream disconnected|broken pipe|connection lost while your computer was asleep|no response from api/i, "reset"],
  [/timed out/i, "timeout"],
];

/** The class of a harness failure text, or undefined when the text names no network failure. */
export function networkErrorFromText(text: string): NetworkErrorClass | undefined {
  for (const word of text.match(/[A-Za-z_]{5,}/g) ?? []) {
    const known = networkErrorFromCode(word);
    if (known !== undefined) return known;
  }
  return phrases.find(([pattern]) => pattern.test(text))?.[1];
}

export function isNetworkErrorClass(value: unknown): value is NetworkErrorClass {
  return value === "connect" || value === "reset" || value === "timeout" || value === "tls" || value === "other";
}
