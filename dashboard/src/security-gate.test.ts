import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "../..");
const workflow = Bun.YAML.parse(readFileSync(resolve(root, ".github/workflows/release.yml"), "utf8")) as any;
const runs = (job: any) => job.steps.map((step: any) => String(step.run ?? "")).join("\n");

test("the required release check rejects every failed or unverified prerequisite", () => {
  const gate = workflow.jobs["release-security"];
  expect(gate.if).toBe("always()");
  expect(gate.needs).toEqual(["source-secrets", "security-acceptance", "fast-suite-macos", "deployment-linux", "dashboard-browser", "release-images"]);
  expect(runs(gate)).toContain('item["result"] != "success"');
  const rules = JSON.parse(readFileSync(resolve(root, ".github/release-ruleset.json"), "utf8"));
  expect(rules.enforcement).toBe("active");
  expect(rules.rules[0].parameters.required_status_checks).toContainEqual({ context: "release-security", integration_id: 15368 });
  expect(rules.rules[0].parameters.strict_required_status_checks_policy).toBe(true);
});

test("pull request scans hold read permissions and fixed action references", () => {
  expect(workflow.permissions).toEqual({ contents: "read" });
  for (const name of ["source-secrets", "security-acceptance", "fast-suite-macos", "release-images", "release-security"]) {
    const job = workflow.jobs[name];
    expect(job.if).toBe(name === "release-security" ? "always()" : undefined);
    expect(job.permissions ?? workflow.permissions).toEqual({ contents: "read" });
    expect(JSON.stringify(job)).not.toMatch(/secrets\.|github\.token|login-action|GH_TOKEN|packages:.*write/);
    for (const step of job.steps) {
      if (step.uses) expect(step.uses).toMatch(/@[0-9a-f]{40}$/);
      expect(step["continue-on-error"]).toBeUndefined();
    }
  }
  expect(workflow.jobs["source-secrets"].steps[0].with["fetch-depth"]).toBe(0);
  expect(runs(workflow.jobs["source-secrets"])).toContain("--public-refs");
});

test("publication uses the exact scanned files and saved images", () => {
  const images = workflow.jobs["release-images"];
  expect(runs(images)).toContain("release/image_layers.py");
  expect(runs(images)).toContain("release/security_scan.py image");
  expect(runs(images)).toContain("for image in project runner");
  const upload = workflow.jobs["product-image"];
  expect(upload.needs).toContain("release-security");
  expect(runs(upload)).toContain("release/security_images.py");
  expect(runs(upload)).not.toContain("docker build");
  expect(upload.steps.find((step: any) => String(step.uses).startsWith("actions/download-artifact")).with["artifact-ids"])
    .toBe("${{ needs.release-images.outputs.artifact-id }}");
  expect(workflow.jobs["release-downloads"].needs).toContain("release-security");
  const fast = runs(workflow.jobs["fast-suite-macos"]);
  expect(fast.indexOf("security_scan.py files")).toBeLessThan(fast.indexOf("COPYFILE_DISABLE=1 tar"));
});

test("the nightly workflow retains results without a source change", () => {
  const nightly = Bun.YAML.parse(readFileSync(resolve(root, ".github/workflows/security-nightly.yml"), "utf8")) as any;
  expect(nightly.on.schedule.length).toBe(1);
  expect(nightly.permissions).toEqual({ contents: "read", packages: "read" });
  const job = nightly.jobs["current-release-images"];
  expect(runs(job)).toContain("security_nightly.py");
  const upload = job.steps.find((step: any) => String(step.uses).startsWith("actions/upload-artifact"));
  expect(upload.if).toBe("always()");
  expect(upload.with["retention-days"]).toBe(90);
});
