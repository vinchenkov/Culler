import { expect, test } from "bun:test";
import { providerFailureFields, adapterFailure, diagnosticRedactor, failureFacts, readAdapterFailure } from "./diagnostics";

test("diagnostics redact credentials before the Unicode limits apply", () => {
  const redact = diagnosticRedactor({ API_KEY: "secret-value", CODEX_REFRESH_TOKEN_URL_OVERRIDE: "http://private/broker" });
  const failure = adapterFailure("secret-value " + "😀".repeat(3000),
    "http://private/broker Authorization: Bearer hidden-token api_key=another-secret " + "😀".repeat(9000), redact);
  expect(Array.from(failure.condition)).toHaveLength(2048);
  expect(Array.from(failure.evidence)).toHaveLength(8192);
  expect(failure.condition).toContain("[redacted]");
  expect(failure.evidence).toContain("[redacted]");
  for (const secret of ["secret-value", "http://private/broker", "hidden-token", "another-secret"]) {
    expect(JSON.stringify(failure)).not.toContain(secret);
  }
  expect(failure.condition).toEndWith("[diagnostic text omitted]");
  expect(failure.evidence).toEndWith("[diagnostic text omitted]");
});

test("the runner accepts only a complete adapter diagnosis", () => {
  expect(readAdapterFailure({ event: "infrastructure-failure", condition: "" })).toBeUndefined();
  expect(readAdapterFailure({ event: "turn.failed", condition: "failure", evidence: "detail" })).toBeUndefined();
  expect(readAdapterFailure(adapterFailure("OpenWiki failed", "Server: openwiki"))?.evidence).toBe("Server: openwiki");
});


test("Provider facts preserve structured error codes without an invented response status", () => {
  expect(providerFailureFields({status:401,code:"invalid_api_key",body:"secret-body"})).toEqual({http_status:401,provider_error_code:"invalid_api_key"});
  expect(providerFailureFields({message:"HTTP 401 invalid key"})).toEqual({});
  expect(providerFailureFields({type:"assistant",error:"authentication_failed"})).toEqual({provider_error_code:"authentication_failed"});
  expect(providerFailureFields({status:403,code:"secret-body"})).toEqual({http_status:403});
});

test("A request without a provider response is a network error and keeps no provider facts", () => {
  expect(failureFacts({provider_error_code:"server_error"}, "API Error: Connection refused (ECONNREFUSED)")).toEqual({network_error:"connect"});
  expect(failureFacts({provider_error_code:"overloaded", http_status:529}, "API Error: 529 Overloaded; the request timed out")).toEqual({provider_error_code:"overloaded", http_status:529});
  expect(failureFacts({http_status:502}, "API Error: 502 The agent container could not reach the endpoint.", "connect")).toEqual({network_error:"connect"});
  expect(failureFacts({}, "The harness ended without a native session")).toEqual({});
  expect(readAdapterFailure({ event: "infrastructure-failure", condition: "refused", evidence: "", network_error: "connect", provider_error_code: "server_error" }))
    .toEqual({ event: "infrastructure-failure", condition: "refused", evidence: "", network_error: "connect" });
  expect(readAdapterFailure({ event: "infrastructure-failure", condition: "refused", evidence: "", network_error: "offline" })).not.toHaveProperty("network_error");
});

test("proxy request correlation survives native errors without an invented origin", () => {
  const request = "a7d2f5c1-1234-4678-9012-456789abcdef";
  expect(providerFailureFields({ status: 401, request_id: request })).toEqual({ http_status: 401, proxy_request_id: request });
  expect(providerFailureFields({ status: 401, request_id: "untrusted-provider-text", origin: "provider" })).toEqual({ http_status: 401 });
  const failure = adapterFailure(`The proxy refused the request. request_id: ${request}`, "");
  expect(readAdapterFailure(failure)?.proxy_request_id).toBe(request);
  expect(failureFacts({ proxy_request_id: request }, "Connection refused")).toEqual({ network_error: "connect", proxy_request_id: request });
  expect(failure).not.toHaveProperty("origin");
});
