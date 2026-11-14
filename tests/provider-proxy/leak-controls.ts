import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { randomBytes } from "node:crypto";
import { detectLeaks, detectArchiveLeaks, requireNoLeaks } from "./leak-detector";
import { diagnosticRedactor } from "../../runner/agent-runner/diagnostics";

const imageIndex = process.argv.indexOf("--image");
const image = imageIndex >= 0 ? process.argv[imageIndex + 1] : undefined;
if (!image) throw new Error("The synthetic leak controls require --image.");
const root = await mkdtemp("/private/tmp/culler-leak-controls-").catch(() => mkdtemp("/tmp/culler-leak-controls-"));
const id = randomBytes(8).toString("hex");
const name = `culler-leak-control-${id}`;
const volume = `${name}-volume`;
const tag = `${name}-image`;
const markers = ["access", "refresh", "api-key", "identity", "private-key"].map(kind => `synthetic-${kind}-${randomBytes(24).toString("hex")}`);
const contaminated = markers.join("\n") + "\n";
const surfaces = new Set<string>();
const images = new Set<string>();
async function command(args: string[], stdin?: string): Promise<Buffer> {
  const child = Bun.spawn(args, { stdin: stdin === undefined ? "ignore" : new TextEncoder().encode(stdin), stdout: "pipe", stderr: "pipe" });
  const [code, bytes, error] = await Promise.all([child.exited, new Response(child.stdout).arrayBuffer(), new Response(child.stderr).text()]);
  if (code) throw new Error(`The synthetic ${args[0]} command failed with exit ${code}.`);
  return Buffer.from(bytes);
}
function check(surface: string, bytes: Buffer, expected: boolean, archive = false) {
  const findings = archive ? detectArchiveLeaks(surface, bytes, markers) : detectLeaks(surface, bytes, markers);
  if (expected) {
    if (new Set(findings.map(value => value.marker)).size !== markers.length) throw new Error(`The ${surface} detector missed a deliberate leak.`);
    let failed = false;
    try { requireNoLeaks(findings); } catch { failed = true; }
    if (!failed) throw new Error(`The ${surface} leak gate accepted a deliberate leak.`);
  } else requireNoLeaks(findings);
  surfaces.add(surface);
}
async function capture(expected: boolean) {
  check("docker-inspect", await command(["docker", "inspect", name]), expected);
  check("container-environment", await command(["docker", "exec", name, "env", "-0"]), expected);
  check("process-environment", await command(["docker", "exec", name, "cat", "/proc/1/environ"]), expected);
  check("container-files", await command(["docker", "cp", `${name}:/fixture/provider.txt`, "-"]), expected, true);
  check("named-volume", await command(["docker", "exec", name, "cat", "/cache/provider.txt"]), expected);
  check("container-logs", await command(["docker", "logs", name]), expected);
}
async function container(leaked: boolean) {
  await command(["docker", "run", "--detach", "--name", name, "--network", "none", "--cap-drop", "ALL", "--user", "10002:10002",
    "--mount", `type=bind,src=${join(root, "fixture")},dst=/fixture,readonly`, "--mount", `type=volume,src=${volume},dst=/cache`,
    ...(leaked ? ["-e", `SYNTHETIC_PROVIDER_CREDENTIALS=${contaminated}`] : []), "--entrypoint", "/bin/sh", image,
    "-c", "cat /fixture/provider.txt; exec sleep 120"]);
  await command(["docker", "exec", "-i", name, "sh", "-c", "cat > /cache/provider.txt"], leaked ? contaminated : "clean\n");
}
try {
  await mkdir(join(root, "fixture"), { mode: 0o755 });
  await writeFile(join(root, "fixture/provider.txt"), contaminated, { mode: 0o644 });
  await command(["docker", "volume", "create", volume]);
  await command(["docker", "run", "--rm", "--network", "none", "--mount", `type=volume,src=${volume},dst=/cache`,
    "--entrypoint", "/bin/sh", image, "-c", "chown 10002:10002 /cache"]);
  await container(true);
  await capture(true);
  await command(["docker", "rm", "--force", name]);
  await writeFile(join(root, "fixture/provider.txt"), "clean\n");
  await container(false);
  await capture(false);
  await command(["docker", "rm", "--force", name]);

  const context = join(root, "image");
  await mkdir(context);
  await writeFile(join(context, "Dockerfile"), "FROM scratch\nCOPY provider.txt /provider.txt\n");
  for (const expected of [true, false]) {
    await writeFile(join(context, "provider.txt"), expected ? contaminated : "clean\n");
    await command(["docker", "build", "--network", "none", "--tag", tag, context]);
    images.add((await command(["docker", "image", "inspect", "--format", "{{.Id}}", tag])).toString().trim());
    check("image-archive", await command(["docker", "image", "save", tag]), expected, true);
  }
  for (const surface of ["host-run-config", "dashboard-response", "trace-export", "resident-log"]) {
    const path = join(root, `${surface}.json`);
    await writeFile(path, JSON.stringify({ body: contaminated }));
    check(surface, await readFile(path), true);
    await writeFile(path, diagnosticRedactor({}, markers)(JSON.stringify({ body: contaminated })));
    check(surface, await readFile(path), false);
  }
  console.log(JSON.stringify({ result: "CULLER_LEAK_CONTROLS_OK", marker_types: markers.length,
    surfaces: [...surfaces], each_control_rejected_leak: true, each_control_accepted_clean: true }));
} finally {
  await command(["docker", "rm", "--force", name]).catch(() => {});
  await command(["docker", "volume", "rm", "--force", volume]).catch(() => {});
  await command(["docker", "image", "rm", "--force", tag]).catch(() => {});
  for (const image of images) await command(["docker", "image", "rm", image]).catch(() => {});
  await rm(root, { recursive: true, force: true });
}
