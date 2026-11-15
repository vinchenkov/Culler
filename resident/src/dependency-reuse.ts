#!/usr/bin/env bun
import assert from "node:assert/strict";
import { createHash, randomBytes } from "node:crypto";
import { chmod, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rename, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { applyEffect } from "./effects";
import { createEnvironmentLane, environmentContainerName, projectCacheArguments, type EnvironmentOperation } from "./environment-lane";
import { pipelineContainerName } from "./pipeline-liveness";
import { makeRig, ok, runtimeBinding } from "./test-helpers";
import type { SpawnEffect, ExecResult, TickDeps } from "./types";
import type { ProjectEnvironment } from "../../runner/shared/project-environment";

const repository = resolve(import.meta.dir, "../..");
const restart = process.argv[2] === "--restart" ? JSON.parse(await readFile(process.argv[3]!, "utf8")) : undefined;
if (restart) {
  assert.match(restart.token, /^[0-9a-f]{12}$/);
  assert.match(restart.root, /\/mc-dependency-reuse-[A-Za-z0-9]+$/);
  assert.equal(await realpath(restart.root), restart.root);
}
const token = restart?.token ?? randomBytes(6).toString("hex");
const runtimeVolume = `mc-reuse-runtime-${token}`;
const spineVolume = `mc-reuse-spine-${token}`;
const imageTag = `mc-reuse-image-${token}`;
const baseTag = `mc-reuse-base-${token}`;
const root = restart?.root ?? await realpath(await mkdtemp(join(tmpdir(), "mc-dependency-reuse-")));
const home = join(root, "home"), source = join(root, "source"), fixtures = join(root, "fixtures");
const containers = new Set<string>(), volumes = new Set([runtimeVolume, spineVolume]);
const checks: string[] = [];
const mcBinary = join(root, "mc");
const mcEnvironment = { ...process.env, MC_HOME: home, MC_RUN_JSON: join(root, "absent.json"), MC_SPINE: undefined, MC_HELPER: undefined };
let image = restart?.image ?? "", activeImage = restart?.activeImage ?? "", commit = restart?.commit ?? "";
let operation: EnvironmentOperation | undefined;
let configuration: ProjectEnvironment = restart?.configuration;
const results: any[] = [];
const fixtureRuntime = process.env.CULLER_ACCEPT_RUNTIME_IMAGE ?? "mc-prod";
const fixtureImage = process.env.CULLER_ACCEPT_PROJECT_IMAGE ?? "mc-project";

async function execute(argv: string[], stdin?: Uint8Array, timeout = 120_000, env = process.env): Promise<ExecResult> {
  const child = Bun.spawn(argv, { stdin: stdin ?? "ignore", stdout: "pipe", stderr: "pipe", env });
  const timer = setTimeout(() => child.kill("SIGKILL"), timeout);
  try {
    const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]);
    return { exitCode, stdout, stderr };
  } finally { clearTimeout(timer); }
}
async function successful(argv: string[], stdin?: Uint8Array): Promise<string> {
  const result = await execute(argv, stdin);
  assert.equal(result.exitCode, 0, `${argv.slice(0, 4).join(" ")}: ${result.stderr}\n${result.stdout}`);
  return result.stdout;
}
const docker = (argv: string[], stdin?: Uint8Array) => execute(["docker", ...argv], stdin);
const git = (...args: string[]) => successful(["git", "-C", source, ...args]);
async function put(path: string, content: string | Uint8Array, mode = 0o644) {
  await mkdir(dirname(path), { recursive: true, mode: 0o777 });
  await writeFile(path, content, { mode });
}
function passed(message: string) { checks.push(message); console.log(`PASS ${message}`); }
async function waitFor(test: () => Promise<boolean>, message: string, timeout = 120_000) {
  const until = Date.now() + timeout;
  while (!await test()) {
    assert(Date.now() < until, message);
    await Bun.sleep(100);
  }
}
async function fetchBytes(url: string): Promise<Uint8Array> {
  const response = await fetch(url, { signal: AbortSignal.timeout(60_000) });
  assert(response.ok, `The dependency fixture request failed: ${url}`);
  return new Uint8Array(await response.arrayBuffer());
}
async function hostCommand(argv: string[]): Promise<ExecResult> {
  return execute([mcBinary, ...argv], undefined, 30_000, mcEnvironment);
}
async function homeIdentity(project: string) {
  const result = await hostCommand(["__project-home", project]);
  assert.equal(result.exitCode, 0, result.stderr);
  const identity = JSON.parse(result.stdout);
  assert.equal((await stat(identity.canonical)).mode & 0o777, 0o700);
  return identity;
}
const rig = makeRig();
const deps: TickDeps = {
  ...rig.deps,
  config: { ...rig.deps.config, mcHome: home, runtimeVolume, projectImage: fixtureImage, spineVolume,
    behaviorsDir: join(root, "behaviors"), image: fixtureRuntime,
    agentCmd: ["/opt/culler-runtime/bin/bun", "/opt/culler-runtime/app/src/agent-runner/main.ts"],
    roleBehaviors: { setup: "/mc/behaviors/setup.json", worker: "/mc/behaviors/worker.json", scribe: "/mc/behaviors/scribe.json" } },
  fs: {
    mkdir: async (path, opts) => { await mkdir(path, { recursive: !opts?.exclusive, mode: opts?.mode ?? 0o777 }); },
    writeFile: async (path, body, opts) => { await writeFile(path, body, { mode: opts?.mode ?? 0o644 }); },
    readFile: path => readFile(path, "utf8"), readdir, rename,
    rm: async (path, opts) => { await rm(path, { recursive: opts?.recursive, force: true }); },
  },
  docker: async (argv, stdin) => {
    let command = [...argv];
    if (command[0] === "create") {
      const name = command[command.indexOf("--name") + 1]!;
      containers.add(name);
      if (command.includes("mc-tier=setup")) {
        command.splice(1, 0, "--mount", `type=bind,src=${fixtures},dst=/mc/dependency-fixtures,readonly`);
      }
      const cache = command.find(value => value.startsWith("type=volume,src=mc-cache-"));
      if (cache) volumes.add(cache.split("src=")[1]!.split(",")[0]!);
    }
    return docker(command, stdin);
  },
  runMc: async (argv, input) => {
    if (argv[0] === "__project-home") return ok(JSON.stringify(await homeIdentity(argv[1]!)));
    if (argv[0] === "__mount-recheck") return hostCommand(argv);
    if (argv[0] === "__repository") {
      const envelope = JSON.parse(await readFile(argv[1]!, "utf8"));
      const workspace = join(envelope.run_root, "repo");
      if (envelope.operation === "allocate") {
        await mkdir(envelope.run_root, { recursive: true, mode: 0o777 });
        await successful(["git", "clone", "--quiet", "--no-hardlinks", source, workspace]);
        await successful(["git", "-C", workspace, "checkout", "--quiet", "--detach", commit]);
        assert(!await Bun.file(join(workspace, "prepared.txt")).exists());
        assert(!await Bun.file(join(workspace, "npm/node_modules/is-number/index.js")).exists());
        assert(!await Bun.file(join(workspace, "bun/node_modules/is-number/index.js")).exists());
        await mkdir(join(workspace, "openwiki"), { recursive: true, mode: 0o777 });
      }
      return ok(JSON.stringify({ input_commit: commit, destination_commit: commit }));
    }
    if (argv[0] === "environment") {
      if (argv[1] === "work") return ok(JSON.stringify({ operations: operation ? [operation] : [] }));
      if (argv[1] === "selections") return ok('{"selections":[]}');
      if (argv[1] === "resolve") return ok(JSON.stringify({ image_digest: activeImage, configuration, configuration_revision: "fixture" }));
      if (argv[1] === "operation-result") results.push(JSON.parse(new TextDecoder().decode(input)));
    }
    return ok("{}");
  },
};

async function makeFixtures() {
  const archive = await fetchBytes("https://registry.npmjs.org/is-number/-/is-number-7.0.0.tgz");
  const metadata = JSON.parse(new TextDecoder().decode(await fetchBytes("https://pypi.org/pypi/idna/3.10/json")));
  const wheel = metadata.urls.find((entry: any) => entry.filename === "idna-3.10-py3-none-any.whl");
  assert(wheel, "The fixed Python wheel is unavailable.");
  const python = await fetchBytes(wheel.url);
  assert.equal(createHash("sha256").update(python).digest("hex"), wheel.digests.sha256);
  await put(join(fixtures, "is-number-7.0.0.tgz"), archive);
  await put(join(fixtures, wheel.filename), python);
  const npmPackage = { name: "is-number", version: "7.0.0", dist: {
    tarball: "http://127.0.0.1:4873/is-number-7.0.0.tgz",
    integrity: `sha512-${createHash("sha512").update(archive).digest("base64")}`,
    shasum: createHash("sha1").update(archive).digest("hex"),
  } };
  await put(join(fixtures, "registry.mjs"), `import http from 'node:http';\nimport fs from 'node:fs';\nconst metadata=${JSON.stringify({ name: "is-number", "dist-tags": { latest: "7.0.0" }, versions: { "7.0.0": npmPackage } })};\nhttp.createServer((request,response)=>{\nconst path=request.url;\nif(path==='/is-number'){response.setHeader('Content-Type','application/json');response.end(JSON.stringify(metadata));}\nelse if(path==='/simple/idna/'){response.setHeader('Content-Type','text/html');response.end('<a href="/idna-3.10-py3-none-any.whl#sha256=${wheel.digests.sha256}">idna</a>');}\nelse if(['/is-number-7.0.0.tgz','/idna-3.10-py3-none-any.whl'].includes(path)){response.end(fs.readFileSync('/mc/dependency-fixtures'+path));}\nelse{response.statusCode=404;response.end();}\n}).listen(4873,'127.0.0.1',()=>fs.writeFileSync('/tmp/registry-ready','ready'));\n`);
  await put(join(fixtures, "mc"), "#!/bin/sh\nprintf '{}\\n'\n", 0o755);
}

const verifyDependencies = `set -eu
node -e "if(!require('./npm/node_modules/is-number')('42'))process.exit(1)"
bun -e "if(!require('./bun/node_modules/is-number')('42'))process.exit(1)"
PYTHONPATH=python-deps python3 -c "import idna; assert idna.encode('example.com') == b'example.com'"
test -f prepared.txt
"$HOME/.fixture-store/tool-link"
test "$(cat "$HOME/.fixture-store/config")" = retained
test "$(cat /cache/extracted/dependency)" = retained
`;
const roleState = `set -eu
test "$(cat "$HOME/.agents/skills/role-proof/SKILL.md")" = "$MC_ROLE"
mkdir -p "$HOME/.codex" "$HOME/.claude"
printf '%s' "$MC_ROLE" > "$HOME/.codex/role"
printf '%s' "$MC_ROLE" > "$HOME/.claude/role"
printf '%s' "$MC_ROLE" > /mc/session/role
if [ "$MC_RUN_ID" = worker-concurrent ]; then
  touch /cache/worker-ready
  until [ -f /cache/scribe-removed ]; do sleep 0.1; done
elif [ "$MC_RUN_ID" = a123456789abcdef ]; then
  touch /cache/scribe-ready
  until [ -f /cache/worker-ready ] && [ -f /cache/setup-finished ]; do sleep 0.1; done
elif [ "$MC_RUN_ID" = setup-concurrent ]; then
  touch /cache/setup-finished
fi
  test "$(cat "$HOME/.agents/skills/role-proof/SKILL.md")" = "$MC_ROLE"
  test "$(cat "$HOME/.codex/role")" = "$MC_ROLE"
  test "$(cat "$HOME/.claude/role")" = "$MC_ROLE"
  test "$(cat /mc/session/role)" = "$MC_ROLE"
`;
async function behavior(role: string, command: string) {
  await put(join(root, "behaviors", `${role}.json`), JSON.stringify({ steps: [
    { do: "exec", command }, { do: "succeed", output: "The dependency check completed." },
  ] }));
}
async function setup(id: string, workspace = source) {
  operation = { id, project_id: "reuse", kind: "setup", image_digest: activeImage,
    workspace_root: workspace, configuration, inputs_digest: `sha256:${"c".repeat(64)}`, revision_id: "fixture",
    brief: "Verify the dependency fixture.", skills: [{ name: "role-proof", body: "setup" }],
    runtime_binding: runtimeBinding("fake", "fake") };
  const lane = createEnvironmentLane(deps);
  try {
    await lane.request();
    await waitFor(async () => lane.activeCount() === 0, "The Setup Agent exceeded its deadline.");
    assert.equal(results.filter(result => result.status === "failed").length, 0, JSON.stringify(results));
    await checkTrace(join(home, "environment-runs", id, "session"));
    const name = environmentContainerName(home, id);
    await successful(["docker", "rm", name]); containers.delete(name);
  } finally { operation = undefined; await lane.stop(); }
}
async function spawn(role: "worker" | "scribe", id: string, project = "reuse", launchDeps = deps) {
  const effect: SpawnEffect = { action: "spawn", project, role, run_id: id, subject_id: role === "worker" ? 1 : null,
    session_path: `sessions/${id}`, skill_store_dir: `skills/projects/${project}/${role}`,
    heartbeat_interval_s: 1, brief: "Verify the dependency fixture.", git_identity_override: null,
    skills: [{ name: "role-proof", body: role }], harness: "fake", model_binding: "fake", runtime_binding: runtimeBinding("fake", "fake"),
    mount_plan: { version: 1, entries: [], tool_home: await homeIdentity(project), repository: {
      ...(role === "worker" ? { task_id: 1 } : { wiki_update: id }), workspace_root: source, input_commit: commit,
      destination_branch: "culler", max_temporary_bytes: 64 * 1024 ** 2,
    } } };
  await applyEffect(effect, launchDeps);
  const name = pipelineContainerName(project, id);
  assert(containers.has(name), `The ${role} container did not start: ${rig.logs.join("\n")}`);
  return name;
}
async function checkTrace(session: string) {
  const events = (await readFile(join(session, "native.jsonl"), "utf8")).trim().split("\n").map(line => JSON.parse(line));
  const tools = events.filter(event => event.event === "tool-use");
  assert(tools.length > 0, "The harness produced no dependency evidence.");
  for (const event of tools) assert.equal(event.exit_code, 0, event.stderr + event.stdout + event.command);
  assert(events.some(event => event.event === "turn-complete"), "The harness did not complete its check.");
  return events;
}
async function finish(id: string, project = "reuse") {
  await waitFor(async () => (await docker(["container", "inspect", pipelineContainerName(project, id)])).exitCode !== 0,
    `The container ${id} exceeded its deadline.`);
  return checkTrace(join(home, "sessions", id));
}

try {
  if (restart) {
    await spawn("worker", "worker-image");
    await finish("worker-image");
  } else {
  await chmod(root, 0o777);
  await mkdir(home, { mode: 0o700 });
  await successful(["mise", "exec", "--", "go", "-C", join(repository, "mc"), "build", "-o", mcBinary, "./cmd/mc"]);
  await successful(["docker", "info", "--format", "{{.ServerVersion}}"]);
  image = (await successful(["docker", "image", "inspect", "--format", "{{.Id}}", fixtureImage])).trim();
  activeImage = image;
  await makeFixtures();
  for (const volume of volumes) await successful(["docker", "volume", "create", volume]);
  await successful(["docker", "run", "--rm", "--network", "none", "--user", "0:0",
    "--entrypoint", "/bin/sh", "-v", `${runtimeVolume}:/runtime-output`, "-v", `${repository}/runner:/current-runner:ro`,
    "-v", `${fixtures}:/fixtures:ro`, fixtureRuntime, "-ec",
    "cp -a /opt/culler-runtime/. /runtime-output/; cp -a /current-runner/. /runtime-output/app/src/; rm /runtime-output/commands/mc; cp /fixtures/mc /runtime-output/commands/mc; chmod 755 /runtime-output/commands/mc"]);
  await mkdir(source, { recursive: true, mode: 0o777 });
  await put(join(source, ".gitignore"), "node_modules/\npython-deps/\nprepared.txt\n");
  for (const manager of ["npm", "bun"]) await put(join(source, manager, "package.json"), JSON.stringify({ name: `fixture-${manager}`, version: "1.0.0", dependencies: { "is-number": "7.0.0" } }));
  await put(join(source, "prepare.sh"), `#!/bin/sh
set -eu
test ! -d npm/node_modules
test ! -d bun/node_modules
test ! -d python-deps
if [ -f /mc/dependency-fixtures/registry.mjs ]; then
  node /mc/dependency-fixtures/registry.mjs &
  until [ -f /tmp/registry-ready ]; do sleep 0.1; done
  (cd npm && npm install --ignore-scripts --no-audit --no-fund --registry=http://127.0.0.1:4873)
  (cd bun && bun install --ignore-scripts --registry=http://127.0.0.1:4873)
  uv pip install --python /usr/local/bin/python3 --target python-deps --index-url http://127.0.0.1:4873/simple/ idna==3.10
  mkdir -p "$HOME/.fixture-store" /cache/extracted
  printf '#!/bin/sh\\nprintf retained\\n' > "$HOME/.fixture-store/tool"
  chmod 755 "$HOME/.fixture-store/tool"
  ln -sf "$HOME/.fixture-store/tool" "$HOME/.fixture-store/tool-link"
  printf retained > "$HOME/.fixture-store/config"
  printf retained > /cache/extracted/dependency
else
  test ! -e /mc/dependency-fixtures
  (cd npm && npm ci --offline --ignore-scripts --no-audit --no-fund)
  (cd bun && bun install --frozen-lockfile --ignore-scripts --registry=http://127.0.0.1:4873)
  uv pip install --offline --python /usr/local/bin/python3 --target python-deps --index-url http://127.0.0.1:4873/simple/ idna==3.10
fi
printf prepared > prepared.txt
`);
  configuration = { base_digest: image, build_files: [], build_args: {}, setup: { command: "sh prepare.sh", directory: "." },
    services: [], variables: [], secrets: [], declarations: [] };
  await behavior("setup", verifyDependencies + roleState);
  await behavior("worker", verifyDependencies + roleState);
  await behavior("scribe", roleState + 'test ! -f prepared.txt\n"$HOME/.fixture-store/tool-link"\n');
  await git("init", "--quiet", "-b", "culler");
  await git("config", "user.name", "Dependency fixture");
  await git("config", "user.email", "fixture@example.invalid");
  await git("add", "."); await git("-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "Add dependency fixture");
  await setup("setup-first");
  const projectHome = (await homeIdentity("reuse")).canonical;
  for (const path of [".npm/_cacache", ".bun/install/cache", ".cache/uv"]) assert((await stat(join(projectHome, path))).isDirectory(), path);
  await git("add", "npm/package-lock.json", "bun/bun.lock");
  await git("-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "Retain fixed dependency declarations");
  commit = (await git("rev-parse", "HEAD")).trim();
  await spawn("worker", "worker-first"); await finish("worker-first");
  passed("Setup and a fresh Worker reuse npm, Bun, and uv dependencies through their normal home caches with network access disabled.");
  passed("The fresh workspace requires preparation; an arbitrary home tool, its symbolic link, and additional cache files survive container removal.");

  await spawn("worker", "worker-concurrent");
  await spawn("scribe", "a123456789abcdef");
  await waitFor(async () => (await docker(["exec", pipelineContainerName("reuse", "worker-concurrent"),
    "/bin/sh", "-c", "test -f /cache/worker-ready && test -f /cache/scribe-ready"])).exitCode === 0,
    "The concurrent roles did not reach the shared barrier.");
  const continuation = join(root, "continuation");
  await successful(["git", "clone", "--quiet", "--no-hardlinks", source, continuation]);
  await setup("setup-concurrent", continuation);
  await finish("a123456789abcdef");
  await successful(["docker", "exec", pipelineContainerName("reuse", "worker-concurrent"),
    "/bin/sh", "-c", "touch /cache/scribe-removed"]);
  await finish("worker-concurrent");
  assert.equal(await readFile(join(home, "sessions/worker-concurrent/role"), "utf8"), "worker");
  assert.equal(await readFile(join(home, "sessions/a123456789abcdef/role"), "utf8"), "scribe");
  passed("Concurrent Setup, Worker, and Scribe containers preserve role files; Worker files survive Scribe removal.");

  await successful(["docker", "tag", image, baseTag]);
  await put(join(root, "build/Dockerfile"), `FROM ${baseTag}\nLABEL culler.dependency-fixture=${token}\n`);
  await successful(["docker", "build", "--network", "none", "--tag", imageTag, "--file", join(root, "build/Dockerfile"), join(root, "build")]);
  activeImage = (await successful(["docker", "image", "inspect", "--format", "{{.Id}}", imageTag])).trim();
  assert.notEqual(activeImage, image);
  const restartPath = join(root, "restart.json");
  await put(restartPath, JSON.stringify({ token, root, image, activeImage, commit, configuration }));
  containers.add(pipelineContainerName("reuse", "worker-image"));
  await successful(["mise", "exec", "--", "bun", import.meta.path, "--restart", restartPath]);
  passed("A new host process and a different Project image retain compatible home dependencies.");

  await behavior("worker", roleState + 'test ! -e "$HOME/.fixture-store/tool"\ntest ! -e "$HOME/.npm/_cacache"\ntest ! -f prepared.txt\n');
  const other = await spawn("worker", "worker-other", "separate");
  const mounts = JSON.parse((await successful(["docker", "inspect", other])).trim())[0].Mounts;
  assert(!mounts.some((mount: any) => mount.Source === projectHome));
  const missing = await finish("worker-other", "separate");
  const brief = missing.find(event => event.event === "turn-start")?.input;
  assert.match(brief, /failed|unavailable|could not/i);
  passed("Another Project receives separate storage and truthful dependency failure evidence while its agent can continue.");

  await behavior("worker", verifyDependencies + roleState);
  const cacheMount = projectCacheArguments(home, "reuse", activeImage).find(value => value.startsWith("type=volume,"))!;
  await successful(["docker", "run", "--rm", "--network", "none", "--user", "10002:10002", "--mount", cacheMount,
    "--mount", `type=bind,src=${projectHome},dst=/home/agent`, "--entrypoint", "/bin/sh", activeImage, "-ec",
    "mkdir -p /cache/npm/_cacache/content-v2; cp -a /home/agent/.npm/_cacache/content-v2/sha512 /cache/npm/_cacache/content-v2/"]);
  await rm(join(projectHome, ".npm"), { recursive: true });
  await spawn("worker", "worker-legacy"); await finish("worker-legacy");
  passed("Retained npm archives remain usable after an update creates the persistent home cache.");

  await successful(["docker", "run", "--rm", "--network", "none", "--user", "10002:10002", "--mount", cacheMount,
    "--mount", `type=bind,src=${projectHome},dst=/home/agent`, "--entrypoint", "/bin/sh", activeImage, "-ec",
    "touch /cache/retained.lock; ln -s /home/agent/.fixture-store/config /cache/home-link"]);
  await successful(["docker", "run", "--rm", "--network", "none", "--user", "10002:10002", "--mount", cacheMount,
    "--mount", `type=bind,src=${projectHome},dst=/home/agent`, "-v", `${runtimeVolume}:/opt/culler-runtime:ro`,
    "--entrypoint", "/opt/culler-runtime/bin/bun", activeImage, "-e",
    "const {clearProjectCache}=await import('/opt/culler-runtime/app/src/shared/project-cache.ts');await clearProjectCache('/cache');if((await (await import('node:fs/promises')).readdir('/cache')).length)throw new Error('The cache remains nonempty.');"]);
  assert.equal(await readFile(join(projectHome, ".fixture-store/config"), "utf8"), "retained");
  assert((await lstat(join(projectHome, ".fixture-store/tool-link"))).isSymbolicLink());
  assert((await stat(join(projectHome, ".fixture-store/tool"))).mode & 0o111);
  passed("Cache removal preserves the Project home, installed executable permissions, and configuration.");
  console.log(`The dependency reuse checks passed: ${checks.length}.`);
  }
} finally {
  if (!restart) {
  for (const name of containers) await docker(["rm", "-f", name]);
  for (const volume of volumes) await docker(["volume", "rm", volume]);
  await docker(["image", "rm", imageTag]);
  await docker(["image", "rm", baseTag]);
  await rm(root, { recursive: true, force: true });
  }
}
