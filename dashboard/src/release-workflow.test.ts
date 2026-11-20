import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "../..");
const workflow = Bun.YAML.parse(readFileSync(resolve(root, ".github/workflows/release.yml"), "utf8")) as any;
const jobs = workflow.jobs;

test("publication requires success from all five checks", () => {
  const gate = jobs["release-checks"];
  const command = gate.steps[0].run;
  const results = Object.fromEntries(gate.needs.map((name: string) => [name, { result: "success" }]));
  const check = (value: unknown) => Bun.spawnSync(["bash", "-c", command], {
    env: { ...process.env, RESULTS: JSON.stringify(value) },
    stdout: "pipe", stderr: "pipe",
  }).exitCode;
  expect(check(results)).toBe(0);
  for (const name of gate.needs) {
    for (const result of ["failure", "cancelled", "skipped"]) {
      expect(check({ ...results, [name]: { result } })).toBe(1);
    }
    const missing = { ...results };
    delete missing[name];
    expect(check(missing)).toBe(1);
  }
});

test("the package starts independently and supplies the same inputs to the image and publication jobs", () => {
  for (const [packaging, images] of [["application-package", "release-images"], ["application-package-linux", "release-images-linux"]] as const) {
    const job = jobs[packaging];
    expect(job.needs).toBeUndefined();
    expect(job.steps[0].with).toEqual({ ref: "${{ github.sha }}", "fetch-depth": 0, "persist-credentials": false });
    const commands = job.steps.map((step: any) => step.run ?? "").join("\n");
    expect(commands).toContain('./release/build.sh "$CULLER_RELEASE_OUTPUT"');
    expect(commands).toContain("release/validate_package.py --output \"$CULLER_RELEASE_OUTPUT\"");
    expect(jobs[images].needs).toEqual([packaging]);
  }
  for (const name of ["release-images", "release-images-linux", "release-downloads"]) {
    const job = jobs[name];
    const packaging = name === "release-images-linux" ? "application-package-linux" : "application-package";
    const steps = job.steps;
    const download = steps.find((step: any) => String(step.uses).startsWith("actions/download-artifact@"));
    expect(download.with["artifact-ids"]).toBe(`\${{ needs.${packaging}.outputs.artifact-id }}`);
    const verify = steps.find((step: any) => step.env?.ARCHIVE_SHA256);
    expect(verify.env.ARCHIVE_SHA256).toBe(`\${{ needs.${packaging}.outputs.archive-sha256 }}`);
    expect(verify.run).toContain("sha256sum -c -");
  }
  // The Linux inputs join the darwin tree only after the shared parts prove byte-equal.
  const merge = jobs["release-downloads"].steps.find((step: any) => String(step.name).includes("Merge the Linux platform inputs"));
  expect(merge).toBeDefined();
  expect(merge.run).toContain('cmp "$RUNNER_TEMP/culler-release/version"');
  expect(merge.run).toContain("package.linux-amd64.json");
  const publish = jobs["release-downloads"].steps.at(-1);
  expect(publish.run).toContain("--linux-image");
  expect(publish.run).toContain("--linux-project-image");
});

test("the Go build caches retain source changes separately from dependency downloads and image caches", () => {
  for (const name of ["fast-suite-macos", "application-package", "application-package-linux", "deployment-linux", "deployment-linux-amd64", "dashboard-browser", "dashboard-browser-amd64"]) {
    const steps = jobs[name].steps;
    const restore = steps.find((step: any) => step.id === "go-cache");
    expect(restore.with.path).toBe("${{ runner.temp }}/go-build");
    expect(restore.with.key).toContain(`-${name}-`);
    expect(restore.with.key).toEndWith("-${{ github.sha }}");
    expect(restore.with["restore-keys"]).toBe(restore.with.key.replace("${{ github.sha }}", ""));
    const save = steps.find((step: any) => String(step.uses).startsWith("actions/cache/save@"));
    expect(save.if).toBe("github.event_name == 'push' && github.ref == 'refs/heads/main' && steps.go-cache.outputs.cache-hit != 'true'");
    expect(save.with.key).toBe("${{ steps.go-cache.outputs.cache-primary-key }}");
    const dependencies = steps.find((step: any) => String(step.uses).startsWith("actions/cache@"));
    expect(dependencies.with.path).not.toContain("/go-build");
    expect(dependencies.with.key).not.toContain("github.sha");
  }
  for (const [name, key] of [["release-images", "release-images-${{ runner.os }}-${{ runner.arch }}-${{ github.sha }}"],
    ["release-images-linux", "release-images-linux-${{ runner.os }}-${{ runner.arch }}-${{ github.sha }}"]] as const) {
    const imageCache = jobs[name].steps.find((step: any) => String(step.uses).startsWith("actions/cache/restore@"));
    expect(imageCache.with.key).toBe(key);
    expect(imageCache.with.path).toBe("${{ runner.temp }}/build-cache");
  }
});

test("cache cleanup has write permission only on main pushes and cannot block publication", () => {
  const cleanup = jobs["go-cache-cleanup"];
  expect(cleanup.if).toBe("always() && github.event_name == 'push' && github.ref == 'refs/heads/main'");
  expect(cleanup.permissions).toEqual({ contents: "read", actions: "write" });
  expect(cleanup.needs).toEqual(["fast-suite-macos", "application-package", "application-package-linux", "deployment-linux", "deployment-linux-amd64", "dashboard-browser", "dashboard-browser-amd64"]);
  for (const job of Object.values(jobs) as any[]) {
    expect(job.needs ?? []).not.toContain("go-cache-cleanup");
  }
});
