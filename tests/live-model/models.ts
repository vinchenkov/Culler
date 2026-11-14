import policy from "./models.json";

export function cheapestLiveModel(harness: string, offered: string[], now = Date.now(), purpose: "proxy" | "scribe" = "proxy"): string {
  const checked = Date.parse(policy.checked_at);
  if (!Number.isFinite(checked) || now - checked > 30 * 86_400_000) {
    throw new Error("Refresh the official model price evidence before a real model test.");
  }
  const rows = harness === "codex" ? policy.codex : harness === "claude-sdk" ? policy["claude-sdk"] : [];
  const eligible = rows.filter(row => offered.includes(row.id) && !("test_exclusions" in row && row.test_exclusions.includes(purpose)));
  eligible.sort((a, b) => 20_000 * (a.input_per_million - b.input_per_million) + 512 * (a.output_per_million - b.output_per_million));
  if (!eligible.length) throw new Error("The connection offers no model with current low-cost test evidence.");
  return eligible[0]!.id;
}


export function liveModelCost(harness: string, model: string, authMode: "api-key" | "account", requests: number) {
  if (!Number.isSafeInteger(requests) || requests < 1) throw new Error("The real model request limit is invalid.");
  const rows = harness === "codex" ? policy.codex : harness === "claude-sdk" ? policy["claude-sdk"] : [];
  const price = rows.find(row => row.id === model);
  if (!price) throw new Error("The selected model has no recorded price evidence.");
  const expectedInput = 20_000, expectedOutput = 512;
  const estimated = requests * (expectedInput * price.input_per_million + expectedOutput * price.output_per_million) / 1_000_000;
  return {
    model, checked_at: policy.checked_at, sources: policy.sources,
    request_limit: requests, expected_input_tokens_per_request: expectedInput, expected_output_tokens_per_request: expectedOutput,
    input_usd_per_million: price.input_per_million, output_usd_per_million: price.output_per_million,
    billing: authMode === "api-key" ? "api-charges" : "subscription-allowance",
    estimated_api_usd: authMode === "api-key" ? estimated : null,
    api_price_comparison_usd: estimated,
    estimate_is_a_spend_cap: false,
    selection: "The model has the lowest recorded price among compatible models in the connection catalog.",
    limitation: authMode === "account" ? "The API price supplies an economical comparison. The test has no comparable subscription allowance prices."
      : "The estimate assumes uncached input. Actual usage determines the charge.",
  };
}

export function liveModelControls(harness: string, model: string, purpose: "proxy" | "scribe") {
  const rows = harness === "codex" ? policy.codex : harness === "claude-sdk" ? policy["claude-sdk"] : [];
  const row = rows.find(candidate => candidate.id === model);
  if (!row) throw new Error("The selected model has no test control policy.");
  if (harness === "codex") return {
    effort: row.test_effort, output_token_limit: null,
    effort_basis: "The native tool set includes web search, which the model rejects at minimal effort.",
    output_limit_status: "The pinned native CLI exposes no supported output-token limit.",
    args: ["--effort", row.test_effort],
  };
  const limit = purpose === "proxy" ? 1024 : 4096;
  return { effort: row.test_effort, output_token_limit: limit,
    output_limit_status: "The native CLI receives a limit for each provider response.",
    args: ["--effort", row.test_effort, "--max-output-tokens", String(limit)],
  };
}
