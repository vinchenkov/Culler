import { initializeLiveSpine, summarizeLiveDiagnostics, resolveLiveImage } from "../../tests/live-model/environment";
import { SPINE_SCHEMA_ID } from "./resident-control";
import { expect, test } from "bun:test";
import { cheapestLiveModel, liveModelCost, liveModelControls } from "../../tests/live-model/models";

const checked = Date.parse("2026-11-02");
test("live tests select the least expensive offered model and refuse an unknown fallback", () => {
  expect(cheapestLiveModel("codex", ["gpt-6-luna", "gpt-5-nano"], checked)).toBe("gpt-5-nano");
  expect(cheapestLiveModel("codex", ["gpt-6-luna"], checked)).toBe("gpt-6-luna");
  expect(cheapestLiveModel("claude-sdk", ["claude-haiku-4-5-20251001", "claude-haiku-5-5"], checked)).toBe("claude-haiku-5-5");
  expect(() => cheapestLiveModel("codex", ["expensive-unknown-model"], checked)).toThrow("no model");
  expect(() => cheapestLiveModel("unknown", ["gpt-5-nano"], checked)).toThrow("no model");
  expect(() => cheapestLiveModel("codex", ["gpt-5-nano"], checked + 31 * 86_400_000)).toThrow("price evidence");
});

test("the Scribe compatibility exception preserves the cheapest ordinary proxy model", () => {
  const offered = ["gpt-5-nano", "gpt-5.6-luna", "gpt-6-luna"];
  expect(cheapestLiveModel("codex", offered, checked, "scribe")).toBe("gpt-6-luna");
  expect(cheapestLiveModel("codex", offered, checked, "proxy")).toBe("gpt-5-nano");
  expect(() => cheapestLiveModel("codex", ["gpt-5-nano"], checked, "scribe")).toThrow("no model");
});

test("live cost evidence separates an estimate from subscription allowance and a spend cap", () => {
  const api = liveModelCost("codex", "gpt-5-nano", "api-key", 3);
  expect(api.estimated_api_usd).toBeCloseTo(0.0036144);
  expect(api.billing).toBe("api-charges");
  expect(api.request_limit).toBe(3);
  expect(api.estimate_is_a_spend_cap).toBe(false);
  const account = liveModelCost("claude-sdk", "claude-haiku-5-5", "account", 3);
  expect(account.estimated_api_usd).toBeNull();
  expect(account.billing).toBe("subscription-allowance");
  expect(account.limitation).toContain("no comparable subscription allowance prices");
  expect(() => liveModelCost("codex", "gpt-5-nano", "api-key", Infinity)).toThrow();
});

test("live test controls use the native output and effort interfaces", () => {
  const codex = liveModelControls("codex", "gpt-5-nano", "proxy");
  expect(codex.args).toEqual(["--effort", "low"]);
  expect(codex.output_token_limit).toBeNull();
  expect(liveModelControls("codex", "gpt-6-luna", "scribe").effort).toBe("low");
  expect(liveModelControls("claude-sdk", "claude-haiku-5-5", "proxy").args)
    .toEqual(["--effort", "low", "--max-output-tokens", "1024"]);
  expect(liveModelControls("claude-sdk", "claude-haiku-5-5", "scribe").output_token_limit).toBe(4096);
  expect(() => liveModelControls("codex", "unknown", "proxy")).toThrow("no test control policy");
});

test("live preflight refuses a production binary or an existing spine before the write probe", async () => {
  const calls: string[][] = [];
  const denied = async (argv: string[]) => { calls.push(argv); return { exitCode: 1, stdout: "", stderr: "" }; };
  await expect(initializeLiveSpine(denied)).rejects.toThrow("host binary");
  expect(calls).toEqual([["__onboard-spine"]]);
  await expect(initializeLiveSpine(async () => ({ exitCode: 0,
    stdout: JSON.stringify({ schema_id: SPINE_SCHEMA_ID, status: "ok" }), stderr: "" }))).rejects.toThrow("new isolated spine");
  let request: any;
  await initializeLiveSpine(async (argv, input) => {
    if (argv[0] === "__onboard-spine") {
      request = JSON.parse(new TextDecoder().decode(input));
      return { exitCode: 0, stdout: JSON.stringify({ schema_id: SPINE_SCHEMA_ID, status: "initialized" }), stderr: "" };
    }
    expect(argv).toEqual(["__system-state", "probe"]);
    return { exitCode: 0, stdout: "{}", stderr: "" };
  });
  expect(request.allow_reset).toBe(false);
  expect(request.reset).toBe(false);
  expect(request).not.toHaveProperty("home");
  expect(request).not.toHaveProperty("spine");
});

test("live diagnostic evidence excludes payloads and unbounded values", () => {
  const events = summarizeLiveDiagnostics({ events: [{ request_id: "request", http_status: 200, request_sent: true,
    condition: "raw response", evidence: "raw response", authorization: "secret", run_id: "x".repeat(513), duration: Infinity }] });
  expect(events).toEqual([{ request_id: "request", request_sent: true, http_status: 200 }]);
  expect(() => summarizeLiveDiagnostics({})).toThrow("unavailable");
});

test("the live test resolves its image once and refuses ambiguous image output", async () => {
  const calls: string[][] = [];
  const id = `sha256:${"a".repeat(64)}`;
  const resolved = await resolveLiveImage("culler-fixture:task", async argv => {
    calls.push(argv);
    return { exitCode: 0, stdout: id + "\n" };
  });
  expect(resolved).toBe(id);
  expect(calls).toEqual([["docker", "image", "inspect", "--format", "{{.Id}}", "--", "culler-fixture:task"]]);
  for (const stdout of ["mutable-tag", id + "\n" + id, "sha256:short"]) {
    await expect(resolveLiveImage("fixture", async () => ({ exitCode: 0, stdout }))).rejects.toThrow("immutable image identifier");
  }
  await expect(resolveLiveImage("fixture", async () => ({ exitCode: 1, stdout: id }))).rejects.toThrow("immutable image identifier");
  await expect(resolveLiveImage("--all", async () => { throw new Error("Unexpected command"); })).rejects.toThrow("selector is invalid");
});
