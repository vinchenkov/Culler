import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "../..");
const workflow = Bun.YAML.parse(readFileSync(resolve(root, ".github/workflows/release.yml"), "utf8")) as any;
const runs = (job: any) => job.steps.map((step: any) => String(step.run ?? "")).join("\n");
const scans = ["security-acceptance", "image-vulnerabilities"];

test("the required release check rejects every failed or unverified prerequisite", () => {
  const gate = workflow.jobs["release-checks"];
  expect(gate.if).toBe("always()");
  expect(gate.needs).toEqual(["fast-suite-macos", "application-package", "deployment-linux", "dashboard-browser", "release-images"]);
  expect(runs(gate)).toContain('item["result"] != "success"');
  const rules = JSON.parse(readFileSync(resolve(root, ".github/release-ruleset.json"), "utf8"));
  expect(rules.enforcement).toBe("active");
  expect(rules.bypass_actors).toEqual([{ actor_id: 5, actor_type: "RepositoryRole", bypass_mode: "always" }]);
  expect(rules.rules[0].parameters.required_status_checks).toEqual([{ context: "release-checks", integration_id: 15368 }]);
  expect(rules.rules[0].parameters.strict_required_status_checks_policy).toBe(true);
});

test("no job waits for a security scan", () => {
  for (const [name, job] of Object.entries(workflow.jobs) as [string, any][]) {
    const needs = [job.needs ?? []].flat();
    for (const scan of scans) expect(needs, name).not.toContain(scan);
  }
  expect(runs(workflow.jobs["fast-suite-macos"])).not.toContain("security_scan.py");
  expect(runs(workflow.jobs["release-images"])).not.toContain("security_scan.py");
  expect(runs(workflow.jobs["release-downloads"])).not.toContain("security_tools.py");
});

test("pull request jobs hold read permissions and fixed action references", () => {
  expect(workflow.permissions).toEqual({ contents: "read" });
  for (const name of [...scans, "fast-suite-macos", "application-package", "release-images", "release-checks"]) {
    const job = workflow.jobs[name];
    expect(job.if).toBe(name === "release-checks" ? "always()" : undefined);
    expect(job.permissions ?? workflow.permissions).toEqual({ contents: "read" });
    expect(JSON.stringify(job)).not.toMatch(/secrets\.|github\.token|login-action|GH_TOKEN|packages:.*write/);
    for (const step of job.steps) {
      if (step.uses) expect(step.uses).toMatch(/@[0-9a-f]{40}$/);
      expect(step["continue-on-error"]).toBeUndefined();
    }
  }
  expect(workflow.jobs["source-secrets"]).toBeUndefined();
});

test("the scans and the upload use the exact checked images", () => {
  const images = workflow.jobs["release-images"];
  expect(runs(images)).toContain("release/image_layers.py");
  expect(runs(images)).toContain("for image in project runner");
  const checked = "${{ needs.release-images.outputs.artifact-id }}";
  const download = (job: any) => job.steps.find((step: any) => String(step.uses).startsWith("actions/download-artifact")).with["artifact-ids"];
  const scan = workflow.jobs["image-vulnerabilities"];
  expect(runs(scan)).toContain("release/security_scan.py --archive");
  expect(download(scan)).toBe(checked);
  const upload = workflow.jobs["product-image"];
  expect(upload.needs).toContain("release-checks");
  expect(runs(upload)).toContain("release/image_load.py");
  expect(runs(upload)).not.toContain("docker build");
  expect(download(upload)).toBe(checked);
  expect(workflow.jobs["release-downloads"].needs).toContain("release-checks");
  expect(runs(workflow.jobs["fast-suite-macos"])).toContain("./release/check.sh");
});

test("only a new version reaches the image upload and the publication", () => {
  const decision = workflow.jobs["release-version"];
  expect(decision.if).toBe("github.event_name == 'push' && github.ref == 'refs/heads/main'");
  expect(decision.permissions).toEqual({ contents: "read" });
  expect(runs(decision)).toContain("release/publish.py --decide");
  expect(decision.outputs.publish).toBe("${{ steps.decide.outputs.publish }}");
  for (const name of ["product-image", "release-downloads"]) {
    const job = workflow.jobs[name];
    expect(job.needs).toContain("release-version");
    expect(job.if).toBe("github.event_name == 'push' && github.ref == 'refs/heads/main' && needs.release-version.outputs.publish == 'true'");
  }
  expect(workflow.jobs["release-checks"].needs).not.toContain("release-version");
  expect(runs(workflow.jobs["fast-suite-macos"])).toContain("./release/check.sh");
  expect(readFileSync(resolve(root, "release/check.sh"), "utf8")).toContain('version.py" check');
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
