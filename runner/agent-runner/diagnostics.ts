import { readFile } from "node:fs/promises";
import { isNetworkErrorClass, networkErrorFromText, type NetworkErrorClass } from "../shared/network-error";
import { redactProviderTokens } from "../shared/provider-token";

export interface AdapterFailure {
  event: "infrastructure-failure";
  proxy_request_id?: string;
  http_status?: number;
  provider_error_code?: string;
  network_error?: NetworkErrorClass;
  condition: string;
  evidence: string;
}

type FailureFacts = Pick<AdapterFailure, "http_status" | "provider_error_code" | "network_error" | "proxy_request_id">;

const proxyRequestPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

function proxyRequestFields(value: unknown): Pick<AdapterFailure, "proxy_request_id"> {
  if (!value || typeof value !== "object") return {};
  const fields = value as Record<string, unknown>;
  const request = fields.proxy_request_id ?? fields.request_id;
  return typeof request === "string" && proxyRequestPattern.test(request) ? { proxy_request_id: request } : {};
}

const omission = "\n[diagnostic text omitted]";

export function limitDiagnostic(value: string, limit: number): string {
  const chars = Array.from(value);
  return chars.length <= limit ? value : chars.slice(0, limit - omission.length).join("") + omission;
}

export function diagnosticRedactor(env: Record<string, string | undefined> = process.env, extra: string[] = []) {
  const secrets = [...extra, ...Object.entries(env)
    .filter(([key]) => /TOKEN|SECRET|PASSWORD|CREDENTIAL|API_KEY|AUTH/i.test(key))
    .map(([, value]) => value ?? "")].filter(Boolean).sort((a, b) => b.length - a.length);
  return (value: string): string => {
    for (const secret of secrets) value = value.split(secret).join("[redacted]");
    return redactProviderTokens(value).replace(/\0/g, "[NUL]")
      .replace(/(Bearer\s+)[^\s,;"']+/gi, "$1[redacted]")
      .replace(/((?:[\w-]*(?:token|secret|password|api[_-]?key|authorization)[\w-]*)["']?\s*[:=]\s*["']?)[^\s,;"'}]+/gi, "$1[redacted]")
      .replace(/(https?:\/\/)[^\s/@]+:[^\s/@]+@/gi, "$1[redacted]@");
  };
}

export async function loadDiagnosticRedactor(
  env: Record<string, string | undefined> = process.env,
  secrets = new Set<string>(),
) {
  if (env.MC_PROVIDER_ACCESS_FILE) {
    try {
      const access = JSON.parse(await readFile(env.MC_PROVIDER_ACCESS_FILE, "utf8"));
      if (typeof access.token === "string") secrets.add(access.token);
    } catch { /* An unavailable access file supplies no token. */ }
  }
  const ambient = diagnosticRedactor(process.env);
  const current = diagnosticRedactor(env, [...secrets]);
  return (value: string): string => current(ambient(value));
}

export function redactDiagnosticEvent(value: unknown, redact: (text: string) => string): unknown {
  if (typeof value === "string") return redact(value);
  if (Array.isArray(value)) return value.map((item) => redactDiagnosticEvent(item, redact));
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, redactDiagnosticEvent(item, redact)]));
  }
  return value;
}

export function adapterFailure(condition: string, evidence: string, redact = diagnosticRedactor()): AdapterFailure {
  const request = `${condition}\n${evidence}`.match(/(?:proxy_request_id|request_id|request id|x-culler-request-id|x-request-id)["']?\s*[:=]\s*["']?([0-9a-f-]{36})/i)?.[1];
  return {
    event: "infrastructure-failure",
    ...(request && proxyRequestPattern.test(request) ? { proxy_request_id: request } : {}),
    condition: limitDiagnostic(redact(condition), 2048),
    evidence: limitDiagnostic(redact(evidence), 8192),
  };
}

export function readAdapterFailure(value: unknown): AdapterFailure | undefined {
  if (!value || typeof value !== "object") return;
  const event = value as Partial<AdapterFailure>;
  if (event.event !== "infrastructure-failure" || typeof event.condition !== "string" ||
      !event.condition.trim() || typeof event.evidence !== "string") return;
  return { ...adapterFailure(event.condition, event.evidence),
    ...proxyRequestFields(event),
    ...(isNetworkErrorClass(event.network_error) ? { network_error: event.network_error } : providerFailureFields(event)) };
}

/** The host observation establishes the origin of a response from the provider proxy. */
export function failureFacts(provider: FailureFacts, text: string, proxied?: NetworkErrorClass): FailureFacts {
  const network = proxied ?? (provider.http_status === undefined ? networkErrorFromText(text) : undefined);
  return network === undefined ? provider : { network_error: network, ...proxyRequestFields(provider) };
}

export function providerFailureFields(value: unknown): Pick<AdapterFailure, "http_status" | "provider_error_code" | "proxy_request_id"> {
  if (!value || typeof value !== "object") return {};
  const fields = value as Record<string, unknown>;
  const status = fields.http_status ?? fields.status ?? fields.api_error_status;
  const result: Pick<AdapterFailure, "http_status" | "provider_error_code" | "proxy_request_id"> = proxyRequestFields(fields);
  if (Number.isInteger(status) && (status as number) >= 400 && (status as number) <= 599) result.http_status = status as number;
  const code = fields.provider_error_code ?? fields.code ?? fields.error;
  if (typeof code === "string" && ["invalid_grant", "invalid_client", "refresh_token_reused", "temporarily_unavailable", "invalid_api_key", "insufficient_quota", "rate_limit_exceeded", "model_not_found", "permission_denied", "authentication_failed", "oauth_org_not_allowed", "billing_error", "rate_limit", "overloaded", "invalid_encrypted_content", "invalid_signature", "invalid_request_error", "invalid_request", "server_error"].includes(code)) result.provider_error_code = code;
  return result;
}
