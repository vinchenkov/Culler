import { describe, expect, spyOn, test } from "bun:test";
import { createEnvironmentLane, checkRuntimeCompatibility, environmentContainerName, projectCacheArguments, projectHomeArguments, projectSecretValues, safeBuildPath, type EnvironmentOperation } from "./environment-lane";
import { makeRig, ok, settle } from "./test-helpers";

const image = "sha256:" + "a".repeat(64);
const candidate = "sha256:" + "b".repeat(64);
function operation(kind: EnvironmentOperation["kind"] = "build"): EnvironmentOperation {
  return {
    id: "op-one", project_id: "demo", kind, image_digest: image, inputs_digest: "sha256:" + "c".repeat(64),
    revision_id: "rev-one", workspace_root: "/projects/demo", brief: "Inspect this Project.", skills: [],
    configuration: { base_digest: image, dockerfile: { path: "Dockerfile", content: `FROM culler-project-base:${image.slice(7)}\nRUN echo ready` },
      build_files: [{ path: "scripts/tool.sh", content: "echo tool" }], build_args: {}, setup: { command: "", directory: "." },
      services: [], variables: [], secrets: [], declarations: [] },
    runtime_binding: { harness: "fake", id: "fake", credential: "", channel: "", base_url: "", model: "" },
  };
}

describe("Project environment execution", () => {
  test("maintenance blocks new setup requests without a container action", async () => {
    const { deps } = makeRig();
    deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
    deps.runMc = async () => { throw new Error("maintenance permitted new work"); };
    deps.docker = async () => { throw new Error("maintenance changed a container"); };
    const lane = createEnvironmentLane(deps, { maintenance: async () => true });
    await lane.request();
    expect(lane.activeCount()).toBe(0);
    await lane.stop();
  });

  test("an incomplete setup request prevents an idle maintenance acknowledgement", async () => {
    const { deps } = makeRig();
    deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
    let finish!: (value: boolean) => void;
    const pause = new Promise<boolean>(resolve => { finish = resolve; });
    const lane = createEnvironmentLane(deps, { maintenance: () => pause });
    const request = lane.request();
    expect(lane.activeCount()).toBe(1);
    finish(true);
    await request;
    expect(lane.activeCount()).toBe(0);
    await lane.stop();
  });
  test("stops an expired setup attempt before it starts more work", async () => {
    const { deps } = makeRig();
    deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
    const op = operation("setup");
    op.created_at = new Date(Date.now() - 30 * 60_000).toISOString();
    const events: string[] = [];
    const reports: any[] = [];
    deps.runMc = async (argv, input) => {
      if (argv[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${argv[1]}/home` }));
      if (argv[1] === "work") return ok(JSON.stringify({ operations: [op] }));
      if (argv[1] === "operation-result") {
        events.push("report");
        reports.push(JSON.parse(new TextDecoder().decode(input)));
      }
      return ok("{}");
    };
    deps.docker = async (argv) => {
      events.push(argv[0]!);
      if (argv[0] === "image") return ok(JSON.stringify([{ Id: image }]));
      if (argv[0] === "container") return ok(JSON.stringify([{ State: { Running: true },
        Config: { Labels: { "com.culler.environment.operation": op.id } } }]));
      return ok("");
    };
    const lane = createEnvironmentLane(deps);
    try {
      await lane.request();
      expect(events.indexOf("stop")).toBeLessThan(events.indexOf("report"));
      expect(events).toContain("stop");
      expect(events).not.toContain("create");
      expect(reports).toEqual([{ operation_id: op.id, inputs_digest: op.inputs_digest, status: "failed",
        error: "The environment execution reached its 30-minute limit. Culler retains its files." }]);
      expect(lane.activeCount()).toBe(0);
    } finally { await lane.stop(); }
  });

  for (const ageMinutes of [0, 29]) {
    test(`stops file acceptance at 30 minutes with an initial age of ${ageMinutes} minutes`, async () => {
      const { deps } = makeRig();
      deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
      const op = operation("accept");
      op.created_at = new Date(Date.now() - ageMinutes * 60_000).toISOString();
      op.metadata = { initial_setup: true, files_digest: "sha256:" + "d".repeat(64) };
      const reports: any[] = [];
      const calls: string[][] = [];
      let finish!: (result: ReturnType<typeof ok>) => void;
      const containerExit = new Promise<ReturnType<typeof ok>>(resolve => { finish = resolve; });
      deps.runMc = async (argv, input) => {
      if (argv[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${argv[1]}/home` }));
        if (argv[1] === "work") return ok(JSON.stringify({ operations: [op] }));
        if (argv[1] === "operation-result") reports.push(JSON.parse(new TextDecoder().decode(input)));
        return ok("{}");
      };
      deps.docker = async (argv) => {
        calls.push(argv);
        if (argv[0] === "image") return ok(JSON.stringify([{ Id: image }]));
        if (argv[0] === "container") return ok(JSON.stringify([{ State: { Running: true }, Config: { Labels: {
          "com.culler.environment.operation": op.id, "com.culler.environment.inputs": op.inputs_digest,
        } } }]));
        if (argv[0] === "wait") return containerExit;
        if (argv[0] === "stop") finish(ok("137"));
        return ok("");
      };
      const originalTimeout = globalThis.setTimeout;
      let deadline: (() => void) | undefined;
      let delay = 0;
      const timeout = spyOn(globalThis, "setTimeout").mockImplementation((callback, milliseconds, ...args) => {
        if (milliseconds! >= 59_000) { deadline = callback as () => void; delay = milliseconds!; }
        return originalTimeout(callback, milliseconds, ...args);
      });
      const lane = createEnvironmentLane(deps);
      try {
        await lane.request();
        await settle();
        expect(lane.activeCount()).toBe(1);
        expect(reports).toHaveLength(0);
        expect(delay).toBeGreaterThan((30 - ageMinutes) * 60_000 - 1000);
        expect(delay).toBeLessThanOrEqual((30 - ageMinutes) * 60_000);
        expect(deadline).toBeDefined();
        deadline!();
        await settle();
        expect(calls).toContainEqual(["stop", environmentContainerName(deps.config.mcHome, op.id)]);
        expect(reports).toHaveLength(1);
        expect(reports[0]).toMatchObject({ operation_id: op.id, status: "failed" });
        expect(lane.activeCount()).toBe(0);
      } finally { timeout.mockRestore(); await lane.stop(); }
    });
  }

  test("builds retained contents and checks the image without the spine or Docker socket", async () => {
    const { deps } = makeRig();
    deps.config = { ...deps.config };
    deps.config.runtimeVolume = "runtime-test";
    deps.config.projectImage = "project-test";
    const op = operation();
    op.project_id = "demo.web";
    const calls: string[][] = [];
    const reports: any[] = [];
    let built = false;
    deps.runMc = async (argv, input) => {
      if (argv[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${argv[1]}/home` }));
      if (argv[1] === "work") return ok(JSON.stringify({ operations: [op] }));
      reports.push(JSON.parse(new TextDecoder().decode(input)));
      return ok("{}");
    };
    deps.docker = async (argv) => {
      calls.push(argv);
      if (argv[0] === "image" && ["project-test", image, `culler-project-base:${image.slice(7)}`].includes(argv[2]!)) return ok(JSON.stringify([{ Id: image }]));
      if (argv[0] === "image") return built
        ? ok(JSON.stringify([{ Id: candidate, Config: { Labels: { "com.culler.environment.operation": op.id, "com.culler.environment.inputs": op.inputs_digest } } }]))
        : { exitCode: 1, stdout: "", stderr: "No such image" };
      if (argv[0] === "container") return { exitCode: 1, stdout: "", stderr: "No such container" };
      if (argv[0] === "build") built = true;
      return ok("");
    };
    const lane = createEnvironmentLane(deps);
    await lane.request();
    await settle();
    await lane.stop();
    expect(calls.filter((argv) => argv[0] === "build")).toHaveLength(1);
    const check = calls.find((argv) => argv[0] === "run")!;
    expect(check).toContain("--network");
    expect(check).toContain("none");
    expect(check.join(" ")).not.toContain(deps.config.spineVolume);
    expect(check.join(" ")).not.toContain("docker.sock");
    expect(reports.at(-1)).toMatchObject({ operation_id: op.id, status: "succeeded", image_digest: candidate, runtime_compatible: true });
  });

  test("recovers an exact prior image without another build", async () => {
    const { deps } = makeRig();
    deps.config = { ...deps.config };
    deps.config.runtimeVolume = "runtime-test";
    deps.config.projectImage = "project-test";
    const op = operation();
    const calls: string[][] = [];
    deps.runMc = async (argv) => ok(JSON.stringify(argv[0] === "__project-home"
      ? { canonical: `${deps.config.mcHome}/state/projects/${argv[1]}/home` }
      : argv[1] === "work" ? { operations: [op] } : {}));
    deps.docker = async (argv) => {
      calls.push(argv);
      if (argv[0] === "container") return { exitCode: 1, stdout: "", stderr: "No such container" };
      if (argv[0] === "image") return ok(JSON.stringify([{ Id: ["project-test", image].includes(argv[2]!) ? image : candidate,
        Config: { Labels: { "com.culler.environment.operation": op.id, "com.culler.environment.inputs": op.inputs_digest } } }]));
      return ok("");
    };
    const lane = createEnvironmentLane(deps);
    await lane.request(); await settle(); await lane.stop();
    expect(calls.some((argv) => argv[0] === "build")).toBe(false);
    expect(calls.some((argv) => argv[0] === "run")).toBe(true);
  });

  test("adopts a matching active setup container and refuses mismatched contents", async () => {
    const { deps } = makeRig();
    deps.config = { ...deps.config };
    deps.config.runtimeVolume = "runtime-test";
    deps.config.projectImage = "project-test";
    const op = operation("setup");
    const reports: any[] = [];
    const calls: string[][] = [];
    let matching = true;
    deps.runMc = async (argv, input) => {
      if (argv[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${argv[1]}/home` }));
      if (argv[1] === "work") return ok(JSON.stringify({ operations: [op] }));
      reports.push(JSON.parse(new TextDecoder().decode(input)));
      return ok("{}");
    };
    deps.docker = async (argv) => {
      calls.push(argv);
      if (argv[0] === "image") return ok(JSON.stringify([{ Id: image }]));
      return ok(JSON.stringify([{ Image: image, State: { Running: true }, Config: { Labels: {
        "com.culler.environment.operation": op.id, "com.culler.environment.inputs": matching ? op.inputs_digest : "wrong",
      } } }]));
    };
    const lane = createEnvironmentLane(deps);
    await lane.request(); await settle();
    expect(reports.filter((report) => report.operation_id)).toHaveLength(0);
    matching = false;
    await lane.request(); await settle(); await lane.stop();
    expect(calls.some((argv) => argv[0] === "create")).toBe(false);
    expect(reports.at(-1).status).toBe("failed");
  });

  test.each(["running", "created"])("closes a %s setup container without current provider authority", async (status) => {
    const { deps } = makeRig();
    deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
    const op = operation("setup");
    op.runtime_binding = { harness: "codex", id: "test-profile", credential: "test-connection", channel: "codex", base_url: "", model: "test-model" };
    const reports: any[] = [];
    const calls: string[][] = [];
    const revoked: string[][] = [];
    deps.credentials = { project: () => { throw new Error("The prior attempt must not restart."); },
      has: () => false, revoke: (...identity) => { revoked.push(identity as string[]); }, extend: () => {} };
    deps.runMc = async (argv, input) => {
      if (argv[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${argv[1]}/home` }));
      if (argv[1] === "work") return ok(JSON.stringify({ operations: [op] }));
      if (argv[1] === "operation-result") reports.push(JSON.parse(new TextDecoder().decode(input)));
      return ok("{}");
    };
    deps.docker = async (argv) => {
      calls.push(argv);
      if (argv[0] === "image") return ok(JSON.stringify([{ Id: image }]));
      return ok(JSON.stringify([{ Image: image, State: { Running: status === "running", Status: status }, Config: { Labels: {
        "com.culler.environment.operation": op.id, "com.culler.environment.inputs": op.inputs_digest,
      } } }]));
    };
    const lane = createEnvironmentLane(deps);
    await lane.request(); await settle(); await lane.stop();
    expect(calls).toContainEqual(["rm", "--force", environmentContainerName(deps.config.mcHome, op.id)]);
    expect(calls.some(argv => ["create", "start"].includes(argv[0]!))).toBe(false);
    expect(revoked).toContainEqual([op.id, op.id]);
    expect(reports).toContainEqual(expect.objectContaining({ operation_id: op.id, status: "failed" }));
    expect(reports.at(-1).error).toContain("resident restarted");
  });

  test("shares cache storage across images within one Project and deployment", () => {
    expect(environmentContainerName("/a", "run")).not.toBe(environmentContainerName("/b", "run"));
    expect(projectCacheArguments("/a", "project", image)).toEqual(projectCacheArguments("/a", "project", candidate));
    expect(projectCacheArguments("/a", "project", image)).not.toEqual(projectCacheArguments("/a", "another", image));
    expect(projectCacheArguments("/a", "project", image)).not.toEqual(projectCacheArguments("/b", "project", image));
    expect(projectCacheArguments("/a", "project", image).some(value => value.startsWith("npm_config_cache="))).toBe(false);
    expect(safeBuildPath("scripts/install.sh")).toBe(true);
    for (const path of ["../secret", "/secret", "a/../b", ".git/hooks/pre-commit", ".dockerignore"]) expect(safeBuildPath(path)).toBe(false);
  });

  test("separates role state from the persistent home without package cache mounts", () => {
    const args = projectHomeArguments("/private/project/home");
    expect(args).toContain("type=bind,src=/private/project/home,dst=/home/agent");
    expect(args).toContain("HOME=/home/agent");
    expect(args.filter(value => value.startsWith("/home/agent/"))).toEqual([
      "/home/agent/.agents:rw,uid=10002,gid=10002,mode=0700",
      "/home/agent/.claude:rw,uid=10002,gid=10002,mode=0700",
      "/home/agent/.codex:rw,uid=10002,gid=10002,mode=0700",
    ]);
    for (const path of ["relative", "/home/../operator", "/home,readonly", "/home:operator"]) {
      expect(() => projectHomeArguments(path)).toThrow("Project home path");
    }
  });

  test("cache removal reports the home separately and removes only cache storage", async () => {
    const { deps } = makeRig();
    deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
    const op = operation("cache-clear"), reports: any[] = [], calls: string[][] = [];
    deps.runMc = async (args, input) => {
      if (args[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/demo/home` }));
      if (args[1] === "work") return ok(JSON.stringify({ operations: [op] }));
      if (args[1] === "storage-report") reports.push(JSON.parse(new TextDecoder().decode(input)));
      return ok("{}");
    };
    deps.docker = async args => {
      calls.push(args);
      if (args[0] === "image") return ok(JSON.stringify([{ Id: image, Size: 300 }]));
      return ok(JSON.stringify({ cache_bytes: 0, home_bytes: 500, inventory: {} }));
    };
    const lane = createEnvironmentLane(deps);
    try {
      await lane.request(); await settle();
      const measurement = calls.find(args => args[0] === "run")!;
      expect(measurement).toContain(`type=bind,src=${deps.config.mcHome}/state/projects/demo/home,dst=/home/agent`);
      expect(measurement.at(-1)).toContain("clearProjectCache('/cache')");
      expect(measurement.at(-1)).toContain("projectHomeUsage()");
      expect(reports).toEqual([{ project_id: "demo", image_digest: image,
        metadata: { cache_bytes: 0, home_bytes: 500, image_bytes: 300, inventory: {} } }]);
    } finally { await lane.stop(); }
  });

  test("checks the image and preserves original files for an initial continuation", async () => {
    const { deps, fakeFs } = makeRig();
    deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
    const op = operation("validate");
    op.metadata = { initial_setup: true };
    op.runtime_binding!.effort = "high";
    op.configuration.source_commit = "d".repeat(40);
    const events: string[] = [];
    const creates: string[][] = [];
    deps.runMc = async (argv) => {
      if (argv[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${argv[1]}/home` }));
      events.push(`mc:${argv[1]}`);
      return ok(JSON.stringify(argv[1] === "work" ? { operations: [op] } : {}));
    };
    deps.docker = async (argv, stdin) => {
      if (argv[0] === "image") return ok(JSON.stringify([{ Id: image }]));
      if (argv[0] === "container") return { exitCode: 1, stdout: "", stderr: "No such container" };
      events.push(argv[0]!);
      if (argv[0] === "run") {
        expect(argv).toContain("/opt/culler-runtime/bin/bun");
        expect(argv.join(" ")).not.toContain(deps.config.spineVolume);
      }
      if (argv[0] === "create") creates.push(argv);
      if (argv[0] === "start") expect(new TextDecoder().decode(stdin)).toBe("{}\n");
      return ok("");
    };
    const lane = createEnvironmentLane(deps);
    await lane.request(); await settle(); await lane.stop();
    expect(events.indexOf("mc:image-check")).toBeLessThan(events.indexOf("create"));
    expect(events.indexOf("run")).toBeLessThan(events.indexOf("create"));
    expect(creates).toHaveLength(1);
    const envelope = JSON.parse(fakeFs.writes.get(`${deps.config.mcHome}/environment-runs/${op.id}/run.json`)!);
    expect(envelope.runtime.effort).toBe("high");
    expect(creates[0]!.join(" ")).toContain("src=/projects/demo,dst=/workspace/source");
    expect(creates[0]!.join(" ")).toContain(deps.config.spineVolume);
    expect(creates[0]!.join(" ")).not.toContain("no-new-privileges");
    expect(creates[0]!.join(" ")).toContain(`src=${deps.config.behaviorsDir},dst=/mc/behaviors,readonly`);
  });

  test("restores the recorded base for a runtime retry without a setup agent", async () => {
    const { deps } = makeRig();
    deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
    const op = operation("runtime-check");
    const reference = `registry.example/project@sha256:${"d".repeat(64)}`;
    op.metadata = { base_source: { registry_reference: reference, release_identity: "release-one" } };
    let restored = false;
    const calls: string[][] = [];
    const reports: any[] = [];
    deps.runMc = async (argv, input) => {
      if (argv[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${argv[1]}/home` }));
      if (argv[1] === "work") return ok(JSON.stringify({ operations: [op] }));
      if (argv[1] === "operation-result") reports.push(JSON.parse(new TextDecoder().decode(input)));
      return ok("{}");
    };
    deps.docker = async (argv) => {
      calls.push(argv);
      if (argv[0] === "pull") restored = true;
      if (argv[0] === "image") return argv[2] === "project-test" || restored
        ? ok(JSON.stringify([{ Id: image }])) : { exitCode: 1, stdout: "", stderr: "No such image" };
      if (argv[0] === "container") return { exitCode: 1, stdout: "", stderr: "No such container" };
      return ok("");
    };
    const lane = createEnvironmentLane(deps);
    await lane.request(); await settle(); await lane.stop();
    expect(calls.filter(args => args[0] === "pull")).toEqual([["pull", "--platform", "linux/arm64", reference]]);
    expect(calls.some(args => ["create", "build"].includes(args[0]!))).toBe(false);
    expect(reports).toEqual([{ operation_id: op.id, inputs_digest: op.inputs_digest, status: "succeeded", runtime_compatible: true }]);
  });

  test("detects a removed image after an earlier successful compatibility check", async () => {
    const { deps } = makeRig();
    deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
    let available = true;
    let audits = 0;
    const results: any[] = [];
    deps.runMc = async (argv, input) => {
      if (argv[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${argv[1]}/home` }));
      if (argv[1] === "selections") return ok(JSON.stringify({ selections: [{ project_id: "demo", image_digest: candidate, base_digest: image }] }));
      if (argv[1] === "runtime-check") results.push(JSON.parse(new TextDecoder().decode(input)));
      if (argv[1] === "image-check") audits++;
      return ok("{}");
    };
    deps.docker = async (argv) => {
      if (argv[0] === "image") return argv[2] === image || available
        ? ok(JSON.stringify([{ Id: argv[2] }])) : { exitCode: 1, stdout: "", stderr: "No such image" };
      if (argv[0] === "container") return { exitCode: 1, stdout: "", stderr: "No such container" };
      return ok(JSON.stringify({ cache_bytes: 0, inventory: {} }));
    };
    const lane = createEnvironmentLane(deps);
    await lane.checkSelections();
    await lane.checkSelections();
    available = false;
    await lane.checkSelections();
    expect(audits).toBe(1);
    expect(results.map(result => result.status)).toEqual(["succeeded", "succeeded", "failed"]);
    expect(results.at(-1).metadata).toEqual({ image_missing: true });
  });

  function selectionRig(record: Record<string, string>) {
    const { deps } = makeRig();
    deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test", releaseBuildId: "release-test" };
    const seen = { audits: 0, results: [] as any[], checkContainers: [] as string[][] };
    deps.runMc = async (argv, input) => {
      if (argv[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${argv[1]}/home` }));
      if (argv[1] === "selections") return ok(JSON.stringify({ selections: [{ project_id: "demo", image_digest: candidate, base_digest: image,
        checked_image_digest: candidate, checked_runtime_identity: "release-test", checked_status: "succeeded", ...record }] }));
      if (argv[1] === "runtime-check") seen.results.push(JSON.parse(new TextDecoder().decode(input)));
      if (argv[1] === "image-check") seen.audits++;
      return ok("{}");
    };
    deps.docker = async (argv) => {
      if (argv[0] === "image") return ok(JSON.stringify([{ Id: argv[2] }]));
      if (argv[0] === "container") return { exitCode: 1, stdout: "", stderr: "No such container" };
      if (argv[0] === "run" && argv.includes("--name")) seen.checkContainers.push(argv);
      return ok(JSON.stringify({ cache_bytes: 0, inventory: {} }));
    };
    return { deps, seen };
  }

  test("a recorded check of the selected image and Culler runtime needs no second check", async () => {
    const { deps, seen } = selectionRig({});
    const lane = createEnvironmentLane(deps);
    await lane.prepare();
    expect(() => lane.assertPrepared()).not.toThrow();
    expect(seen.audits).toBe(0);
    expect(seen.checkContainers).toEqual([]);
    expect(seen.results).toEqual([]);
    await checkRuntimeCompatibility(candidate, { ...operation("accept"), image_digest: candidate }, deps);
    expect(seen.audits).toBe(0);
  });

  for (const [name, record] of [
    ["another Culler runtime", { checked_runtime_identity: "release-old" }],
    ["another image", { checked_image_digest: image }],
    ["a failed check", { checked_status: "failed" }],
  ] as const) {
    test(`a recorded check of ${name} runs the full check again`, async () => {
      const { deps, seen } = selectionRig(record);
      const lane = createEnvironmentLane(deps);
      await lane.checkSelections();
      expect(seen.audits).toBe(1);
      expect(seen.checkContainers).toHaveLength(1);
      expect(seen.results).toEqual([{ project_id: "demo", image_digest: candidate, runtime_identity: "release-test", status: "succeeded" }]);
    });
  }

  test("a recorded check does not hide a removed image", async () => {
    const { deps, seen } = selectionRig({});
    const docker = deps.docker;
    deps.docker = async (argv) => argv[0] === "image" && argv[2] === candidate
      ? { exitCode: 1, stdout: "", stderr: "No such image" } : docker(argv);
    const lane = createEnvironmentLane(deps);
    await lane.checkSelections();
    expect(seen.audits).toBe(0);
    expect(seen.results).toEqual([{ project_id: "demo", image_digest: candidate, runtime_identity: "release-test", status: "failed",
      error: "The selected Project image is unavailable.", metadata: { image_missing: true } }]);
  });

  test("a failed Project preflight preserves another Project's setup eligibility", async () => {
    const { deps } = makeRig();
    deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
    const bad = { ...operation("setup"), id: "bad-operation", workspace_root: "relative" };
    const valid = { ...operation("setup"), id: "good-operation", project_id: "app.web" };
    const reports: any[] = [];
    const inspected: string[] = [];
    deps.runMc = async (args, input) => {
      if (args[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${args[1]}/home` }));
      if (args[1] === "work") return ok(JSON.stringify({ operations: [bad, valid] }));
      if (args[1] === "operation-result") reports.push(JSON.parse(new TextDecoder().decode(input)));
      return ok("{}");
    };
    deps.docker = async args => {
      if (args[0] === "image") return ok(JSON.stringify([{ Id: image }]));
      if (args[0] === "container") {
        inspected.push(args[2]!);
        return ok(JSON.stringify([{ Image: image, State: { Running: true }, Config: { Labels: {
          "com.culler.environment.operation": valid.id, "com.culler.environment.inputs": valid.inputs_digest,
        } } }]));
      }
      return ok("");
    };
    const lane = createEnvironmentLane(deps);
    await lane.request(); await settle(); await lane.stop();
    expect(reports).toHaveLength(1);
    expect(reports[0]).toMatchObject({ operation_id: bad.id, status: "failed" });
    expect(inspected).toContain(environmentContainerName(deps.config.mcHome, valid.id));
  });

  test("secret references accept the same dotted names as the backend", async () => {
    const { deps, fakeFs } = makeRig();
    const config = operation().configuration;
    config.secrets = [{ name: "TOKEN", reference: "registry.v1", scope: "setup" }];
    fakeFs.writes.set(`${deps.config.mcHome}/project-secrets/app.web/registry.v1`, "scratch-value");
    expect(await projectSecretValues(config, "app.web", deps)).toEqual({ TOKEN: "scratch-value" });
  });
});

test.each([
  {
    stdout: JSON.stringify({ event: "infrastructure-failure", condition: "The API host could not resolve.", evidence: "ENOTFOUND api.example.test" }),
    stderr: '{"level":"info","event":"runner.skill_installed"}',
    message: "The API host could not resolve.", evidence: "ENOTFOUND", exitCode: 1,
  },
  {
    stdout: "", stderr: '{"level":"info","event":"runner.skill_installed"}\n'
      + 'time="2026-10-17T10:36:47-07:00" level=error msg="error waiting for container: unexpected EOF"',
    message: "Docker could not wait for the setup container: unexpected EOF", evidence: "unexpected EOF", exitCode: 125,
  },
  {
    stdout: "", stderr: '{"level":"info","event":"runner.skill_installed"}',
    message: "The setup process exited with code 133.", evidence: "Exit code: 133", exitCode: 133,
  },
])("setup failures retain the cause and technical details: $message", async ({ stdout, stderr, message, evidence, exitCode }) => {
  const { deps } = makeRig();
  deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
  const op = operation("setup");
  const reports: any[] = [];
  deps.runMc = async (args, input) => {
      if (args[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${args[1]}/home` }));
    if (args[1] === "work") return ok(JSON.stringify({ operations: [op] }));
    if (args[1] === "operation-result") reports.push(JSON.parse(new TextDecoder().decode(input)));
    return ok("{}");
  };
  let exited = false;
  deps.docker = async args => {
    if (args[0] === "image") return ok(JSON.stringify([{ Id: image }]));
    if (args[0] === "container") return exited ? ok(JSON.stringify([{ State: { Status: "exited" } }]))
      : { exitCode: 1, stdout: "", stderr: "No such container" };
    if (args[0] === "start") {
      exited = true;
      return { exitCode, stdout, stderr };
    }
    return ok("");
  };
  const lane = createEnvironmentLane(deps);
  await lane.request(); await settle(); await lane.stop();
  expect(reports).toHaveLength(1);
  expect(reports[0]).toMatchObject({ operation_id: op.id, error: message, status: "failed" });
  expect(reports[0].metadata.failure_details).toContain("runner.skill_installed");
  expect(reports[0].metadata.failure_details).toContain(evidence);
});

test("a container creation failure reaches its operation before any start", async () => {
  const { deps } = makeRig();
  deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
  const op = operation("setup");
  const reports: any[] = [], calls: string[] = [];
  deps.runMc = async (args, input) => {
      if (args[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${args[1]}/home` }));
    if (args[1] === "work") return ok(JSON.stringify({ operations: [op] }));
    if (args[1] === "operation-result") reports.push(JSON.parse(new TextDecoder().decode(input)));
    return ok("{}");
  };
  deps.docker = async args => {
    calls.push(args[0]!);
    if (args[0] === "image") return ok(JSON.stringify([{ Id: image }]));
    if (args[0] === "container") return { exitCode: 1, stdout: "", stderr: "No such container" };
    if (args[0] === "create") return { exitCode: 1, stdout: "", stderr: "The mount source is unavailable." };
    return ok("");
  };
  const lane = createEnvironmentLane(deps);
  await lane.request(); await settle(); await lane.stop();
  expect(calls).not.toContain("start");
  expect(reports[0].error).toBe("The mount source is unavailable.");
  expect(reports[0].metadata.failure_details).toContain("could not be created");
});

test("shared errors refresh only after a completed setup request", async () => {
  const { deps, fakeFs } = makeRig();
  deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
  let time = new Date("2026-10-13T00:00:00Z"), fail = true;
  let release!: () => void;
  let hold: Promise<void> | undefined;
  deps.docker = async () => ok(JSON.stringify([{ Id: image }]));
  deps.runMc = async args => {
      if (args[0] === "__project-home") return ok(JSON.stringify({ canonical: `${deps.config.mcHome}/state/projects/${args[1]}/home` }));
    if (args[1] === "work") {
      if (hold) await hold;
      if (fail) return { exitCode: 2, stdout: "", stderr: "The spine is unavailable." };
      return ok('{"operations":[]}');
    }
    return ok("{}");
  };
  const lane = createEnvironmentLane(deps, { instance: "resident-one", now: () => time });
  const read = () => JSON.parse(fakeFs.writes.get(`${deps.config.mcHome}/health/resident-setup.json`)!).request;
  await lane.request();
  expect(read()).toMatchObject({ status: "failed", message: "The spine is unavailable.", observed_at: time.toISOString() });
  time = new Date("2026-10-13T00:00:05Z");
  await lane.request();
  expect(read().observed_at).toBe(time.toISOString());
  hold = new Promise<void>(resolve => { release = resolve; });
  const request = lane.request();
  await settle();
  time = new Date("2026-10-13T00:01:00Z");
  expect(read().observed_at).toBe("2026-10-13T00:00:05.000Z");
  fail = false; release(); await request;
  expect(read()).toMatchObject({ status: "succeeded", observed_at: time.toISOString() });
  expect(read().message).toBeUndefined();
  await lane.stop();
});

test("an initial preparation failure keeps dispatch closed until runtime checks recover", async () => {
  const { deps, fakeFs } = makeRig();
  deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
  let fail = true;
  deps.runMc = async args => args[1] === "selections" && fail
    ? { exitCode: 2, stdout: "", stderr: "The runtime selections are unavailable." }
    : ok(JSON.stringify(args[1] === "work" ? { operations: [] } : {}));
  deps.docker = async () => ok(JSON.stringify([{ Id: image }]));
  const lane = createEnvironmentLane(deps);
  await lane.prepare();
  expect(() => lane.assertPrepared()).toThrow("runtime checks complete");
  expect(JSON.parse(fakeFs.writes.get(`${deps.config.mcHome}/health/resident-setup.json`)!).request.message).toBe("The runtime selections are unavailable.");
  fail = false;
  await lane.request();
  expect(() => lane.assertPrepared()).not.toThrow();
  await lane.stop();
});

for (const harness of ["fake", "codex"] as const) {
  test(`setup uses the Project container policy for ${harness}`, async () => {
    const { deps } = makeRig();
    deps.config = { ...deps.config, runtimeVolume: "runtime-test", projectImage: "project-test" };
    deps.hostZone = () => "America/Los_Angeles";
    deps.credentials = { project: () => ({ env: {}, proxyConfig: JSON.stringify({ version: 1,
      url: "https://host.docker.internal:7444", token: "culler_proxy_" + "c".repeat(43),
      ca: "-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----", harness: "codex", authMode: "account" }) }) };
    const op = operation("setup");
    op.runtime_binding = { ...op.runtime_binding!, harness, channel: harness === "codex" ? "codex" : "", credential: harness === "codex" ? "account" : "" };
    const calls: string[][] = [];
    deps.runMc = async args => ok(JSON.stringify(args[0] === "__project-home"
      ? { canonical: `${deps.config.mcHome}/state/projects/${args[1]}/home` }
      : args[1] === "work" ? { operations: [op] } : {}));
    deps.docker = async args => {
      calls.push(args);
      if (args[0] === "image") return ok(JSON.stringify([{ Id: image }]));
      if (args[0] === "container") return { exitCode: 1, stdout: "", stderr: "No such container" };
      return ok("");
    };
    const lane = createEnvironmentLane(deps);
    try {
      await lane.request(); await settle();
      const create = calls.find(args => args[0] === "create")!;
      expect(create).toBeDefined();
      expect(create).toContain("--init");
      for (const [flag, value] of [["--user", "10002:10002"], ["--cap-drop", "ALL"],
        ["--memory", "4g"], ["--cpus", "2"], ["--pids-limit", "512"], ["--workdir", "/workspace/source"]]) {
        expect(create[create.indexOf(flag!) + 1]).toBe(value!);
      }
      expect(create).toContain("TZ=America/Los_Angeles");
      expect(create).toContain(image);
      expect(create).toContain("type=bind,src=/projects/demo,dst=/workspace/source");
      expect(create).toContain(`type=bind,src=${deps.config.mcHome}/state/projects/demo/home,dst=/home/agent`);
      expect(create).toContain("HOME=/home/agent");
      expect(create).toContain("/home/agent/.agents:rw,uid=10002,gid=10002,mode=0700");
      expect(create).not.toContain("--security-opt");
      if (harness === "fake") {
        expect(create[create.indexOf("--network") + 1]).toBe("none");
      } else {
        expect(create).not.toContain("--network");
      }
    } finally { await lane.stop(); }
  });
}
