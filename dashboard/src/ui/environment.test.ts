import { expect, test } from "bun:test";
import {
  environmentAwaitsApproval, environmentBaseReference, environmentBytes, environmentReportSummary, environmentConfiguration,
  environmentExplanation, environmentPayload, environmentProgress, environmentVariableText, environmentVariables,
} from "./environment.js";
import { isReadRequest } from "./requests.js";
import { parseRoute, routeHash } from "./router.js";

test("configuration edits preserve recorded inputs without changing the accepted revision", () => {
  const original = { base_digest: "sha256:base", build_files: [{ path: "install.sh", content: "echo exact" }], secrets: [{ name: "TOKEN", reference: "registry", scope: "setup" }], source_commit: "commit" };
  const draft = environmentConfiguration(original);
  draft.build_files[0].content = "echo revised";
  draft.secrets[0].reference = "another";
  expect(original.build_files[0].content).toBe("echo exact");
  expect(original.secrets[0].reference).toBe("registry");
  expect(draft.source_commit).toBe("commit");
  expect(draft.setup).toEqual({ command: "", directory: "." });
  expect(draft.dockerfile).toBeNull();
});

test("a queued operation reports authorization without active execution", () => {
  const view = { environment: { phase: "queued", readiness: "setup_required", active_operation_id: "op" }, operations: [{ id: "op", status: "queued" }] };
  const result = environmentProgress(view);
  expect(result.active).toBe(false);
  expect(result.pending).toBe(true);
  expect(result.title).toBe("Setup awaits an agent");
  expect(result.progress).toBe("Culler will start the authorized operation.");
  view.operations[0].status = "running";
  expect(environmentProgress(view).active).toBe(true);
});

test("approval requires the recorded proposal and successful setup owns completion", () => {
  const view = { environment: { phase: "approval", readiness: "setup_required" }, draft: { explanation: "The database package needs PostgreSQL headers." } };
  expect(environmentProgress(view).approval).toBe(true);
  expect(environmentProgress(view).explanation).toBe(view.draft.explanation);
  expect(environmentProgress({ environment: view.environment }).approval).toBe(false);
  expect(environmentProgress({ environment: { phase: "complete", readiness: "ready" } }).title).toBe("Ready for development");
  expect(environmentProgress({ environment: { phase: "validate", readiness: "ready" } }).title).toBe("The agent continues Project preparation");
  expect(environmentProgress({ environment: { phase: "failed", readiness: "setup_required", cause: "The registry is unavailable." } }).cause).toBe("The registry is unavailable.");
});

test("the environment route keeps Project identity and a status read has no write effect", () => {
  expect(parseRoute(routeHash("environment", ["garden"]))).toEqual({ page: "environment", rest: ["garden"] });
  expect(isReadRequest("/api/environment/get", { method: "POST" })).toBe(true);
  expect(isReadRequest("/api/environment/approve", { method: "POST" })).toBe(false);
});


test("the variable editor reads and writes each value without loss", () => {
  const entries = [
    { name: "GOFLAGS", value: "-tags=test_scratch_spine -timeout=30m", scope: "both" }, { name: "EMPTY", value: "", scope: "both" },
    { name: "PADDED", value: " lead and trail ", scope: "both" }, { name: "QUOTED", value: '"literal"', scope: "both" },
    { name: "LINES", value: "first\nsecond\\n", scope: "both" }, { name: "WINDOWS", value: "C:\\tools\\new", scope: "both" },
  ];
  const source = environmentVariableText(entries);
  expect(source.split("\n")[0]).toBe("GOFLAGS=-tags=test_scratch_spine -timeout=30m");
  expect(source.split("\n")).toHaveLength(entries.length);
  expect(environmentVariables(source)).toEqual(entries);
});

test("the variable editor keeps a recorded scope and ignores comments", () => {
  expect(environmentVariables("# The agent needs this value.\n\nexport TOKEN_URL = 'https://example.test'\nNEW=1\nBARE", { TOKEN_URL: "agent" })).toEqual([
    { name: "TOKEN_URL", value: "https://example.test", scope: "agent" }, { name: "NEW", value: "1", scope: "both" },
    { name: "BARE", value: "", scope: "both" },
  ]);
});

test("a save selects the source by the draft origin", () => {
  const config = environmentConfiguration({ source_commit: "commit" });
  expect(environmentPayload(config).source_commit).toBeUndefined();
  expect(environmentPayload(config, { keepSource: true }).source_commit).toBe("commit");
  expect(config.source_commit).toBe("commit");
});

test("an explanation stays with its image inputs only", () => {
  const before = environmentConfiguration({ base_digest: "sha256:base", dockerfile: { path: ".culler/Dockerfile", content: "FROM base\n" } });
  const script = { ...before, setup: { command: "npm ci", directory: "." } };
  const image = { ...before, dockerfile: { path: ".culler/Dockerfile", content: "FROM base\nRUN true\n" } };
  expect(environmentExplanation(before, script, "The image adds a library.")).toBe("The image adds a library.");
  expect(environmentExplanation(before, image, "The image adds a library.")).toBe("");
});

test("only a draft without an approval awaits approval", () => {
  expect(environmentAwaitsApproval({ draft: null, approvals: [] })).toBe(false);
  expect(environmentAwaitsApproval({ draft: { id: "new" }, approvals: [{ revision_id: "old" }] })).toBe(true);
  expect(environmentAwaitsApproval({ draft: { id: "new" }, approvals: [{ revision_id: "old" }, { revision_id: "new" }] })).toBe(false);
});

test("the report belongs to the current draft or the selected environment", () => {
  const view = { environment: { draft_revision_id: "draft", selected_revision_id: "selected" }, results: [
    { id: "unrelated", revision_id: "other", summary: "Other report." },
    { id: "current", revision_id: "draft", outcome: "complete", summary: "Preparation is complete.", details: "The tests remain unverified." },
    { id: "accepted", revision_id: "selected", outcome: "complete", summary: "The earlier preparation is complete." },
  ] };
  expect(environmentReportSummary(view)?.id).toBe("current");
  expect(environmentReportSummary(view)?.details).toBe("The tests remain unverified.");
  view.environment.draft_revision_id = "new";
  expect(environmentReportSummary(view)?.id).toBe("accepted");
  expect(environmentReportSummary({ results: [] })).toBeNull();
});

test("storage sizes use decimal units and keep an absent measurement", () => {
  expect(environmentBytes(undefined)).toBe("Not measured");
  expect(environmentBytes(null)).toBe("Not measured");
  expect(environmentBytes(0)).toBe("0 bytes");
  expect(environmentBytes(1)).toBe("1 byte");
  expect(environmentBytes(348_200_000)).toBe("348 MB");
  expect(environmentBytes(2_184_000_000)).toBe("2.2 GB");
});

test("the status tone separates work, approval, failure, and readiness", () => {
  const tone = (environment: object, extra: object = {}) => environmentProgress({ environment, operations: [], ...extra })!.tone;
  expect(tone({ phase: "idle", readiness: "setup_required" })).toBe("idle");
  expect(tone({ phase: "queued", readiness: "setup_required" })).toBe("active");
  expect(tone({ phase: "complete", readiness: "ready" })).toBe("ready");
  expect(tone({ phase: "approval", readiness: "ready" }, { draft: { explanation: "" } })).toBe("attention");
  expect(tone({ phase: "failed", readiness: "ready" })).toBe("failed");
  expect(tone({ phase: "validate", readiness: "ready", active_operation_id: "op" }, { operations: [{ id: "op", status: "queued" }] })).toBe("active");
});

test("source acceptance and cache removal report host work accurately", () => {
  const view = { environment: { phase: "validate", readiness: "ready", active_operation_id: "op", runtime_condition: "The image requires repair." },
    operations: [{ id: "op", kind: "accept", status: "running" }] };
  expect(environmentProgress(view).title).toBe("Culler saves the checked Project files");
  expect(environmentProgress(view).cause).toBe("The image requires repair.");
  view.operations[0].kind = "cache-clear";
  view.operations[0].status = "queued";
  expect(environmentProgress(view).title).toBe("Cache removal awaits execution");
});


test("the Dockerfile reference identifies the exact selected base", () => {
  expect(environmentBaseReference(`sha256:${"a".repeat(64)}`)).toBe(`culler-project-base:${"a".repeat(64)}`);
  expect(environmentBaseReference("ubuntu:latest")).toBe("");
});

test("an unavailable accepted image exposes repair instead of a ready status", () => {
  const view = { environment: { phase: "complete", readiness: "ready", runtime_condition: "The Project image is unavailable." }, operations: [] };
  const failed = environmentProgress(view);
  expect(failed.title).toBe("The Project environment needs repair");
  expect(failed.readiness).toBe("Runtime repair required");
  expect(failed.repair).toBe(true);
  view.environment.active_operation_id = "retry";
  view.operations.push({ id: "retry", kind: "runtime-check", status: "queued" });
  expect(environmentProgress(view).title).toBe("The runtime check awaits execution");
  expect(environmentProgress(view).repair).toBe(false);
});
