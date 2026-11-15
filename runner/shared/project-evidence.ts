import { createHash } from "node:crypto";
import { lstat, readFile, realpath } from "node:fs/promises";
import { basename, dirname, isAbsolute, relative, resolve } from "node:path";
import { workflowDependencies, type ProjectEnvironment, type WorkflowDependencies } from "./project-environment";

export interface ProjectEvidence {
  tool_versions: Record<string, string>;
  package_records: string[];
}
interface Observation { exit_code: number | null; stdout: string }
export interface EvidenceDependencies {
  execute(argv: string[], directory: string, env: Record<string, string | undefined>): Promise<Observation>;
}
const declarations = ["package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "bun.lock", "yarn.lock",
  "pyproject.toml", "uv.lock", "poetry.lock", "requirements.txt", "Pipfile.lock", "go.mod", "go.sum", "Cargo.toml", "Cargo.lock",
  "Gemfile.lock", "composer.lock", "packages.lock.json", ".mise.toml", "mise.toml", "mise.lock", ".tool-versions", ".python-version", ".node-version", ".nvmrc"];
const tools: Array<[string, string[]]> = [
  ["node", ["node", "--version"]], ["npm", ["npm", "--version"]], ["pnpm", ["pnpm", "--version"]],
  ["bun", ["bun", "--version"]], ["python", ["python3", "--version"]], ["uv", ["uv", "--version"]],
  ["go", ["go", "version"]], ["mise", ["mise", "--version"]], ["git", ["git", "--version"]],
  ["bash", ["bash", "--version"]], ["cc", ["cc", "--version"]], ["c++", ["c++", "--version"]],
  ["make", ["make", "--version"]], ["pkg-config", ["pkg-config", "--version"]], ["cmake", ["cmake", "--version"]],
  ["ninja", ["ninja", "--version"]], ["chromium", ["chromium", "--version"]],
];
const safeEnvironmentNames = ["PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
  "MISE_DATA_DIR", "MISE_CONFIG_DIR", "GOTOOLCHAIN", "GOMODCACHE", "GOPATH", "VIRTUAL_ENV", "PYENV_ROOT", "PYENV_VERSION", "JAVA_HOME", "GEM_HOME", "GEM_PATH"];
const offline = { MISE_OFFLINE: "true", MISE_AUTO_INSTALL: "false", MISE_NOT_FOUND_AUTO_INSTALL: "false",
  UV_OFFLINE: "true", PIP_NO_INDEX: "1", COREPACK_ENABLE_NETWORK: "0", GOPROXY: "off", GOSUMDB: "off" };
const dependencies: EvidenceDependencies = {
  async execute(argv, directory, env) {
    try {
      const result = await workflowDependencies.execute(shellCommand(argv), directory, env, 8000);
      return { exit_code: result.exitCode, stdout: result.output };
    } catch { return { exit_code: null, stdout: "" }; }
  },
};

function shellCommand(argv: string[]): string {
  return argv.map(value => `'${value.replaceAll("'", "'\\''")}'`).join(" ");
}

export async function collectProjectImageInventory(
  execute: WorkflowDependencies["execute"] = workflowDependencies.execute,
  packageStatus: string = "/var/lib/dpkg/status",
) {
  const inventory = { platform: "linux/arm64", tools: {} as Record<string, string>,
    packages: [] as Array<{ name: string; version: string; architecture: string }>, unverified: [] as string[] };
  const env = safeEnvironment(process.env, new Set());
  const probes: Array<[string, string[]]> = [...tools, ["curl", ["curl", "--version"]],
    ["rg", ["rg", "--version"]], ["jq", ["jq", "--version"]], ["playwright-cli", ["playwright-cli", "--version"]]];
  for (const [name, argv] of probes) {
    try {
      const output = await execute(shellCommand(argv), "/workspace", env, 8000);
      const version = output.output.match(/\b(?:go|v)?\d+\.\d+(?:\.\d+)*(?:[-+][a-zA-Z0-9.-]+|rc\d+|beta\d+)?\b/)?.[0];
      if (output.exitCode === 0 && version) inventory.tools[name] = version;
      else inventory.unverified.push(name);
    } catch { inventory.unverified.push(name); }
  }
  try {
    const file = Bun.file(packageStatus);
    if (file.size > 16 * 1024 * 1024) throw new Error("The system package record exceeds its size limit.");
    for (const record of (await file.text()).split(/\n\s*\n/)) {
      const fields = Object.fromEntries(record.split("\n").filter(line => /^[A-Za-z-]+: /.test(line))
        .map(line => [line.slice(0, line.indexOf(":")), line.slice(line.indexOf(":") + 2)]));
      if (fields.Status !== "install ok installed") continue;
      if (!/^[a-z0-9][a-z0-9+.-]*$/.test(fields.Package ?? "") || !/^[a-zA-Z0-9.+:~_-]+$/.test(fields.Version ?? "") ||
        !/^[a-z0-9-]+$/.test(fields.Architecture ?? "")) continue;
      inventory.packages.push({ name: fields.Package!, version: fields.Version!, architecture: fields.Architecture! });
    }
  } catch { inventory.unverified.push("system packages"); }
  return inventory;
}

function safeEnvironment(base: Record<string, string | undefined>, secretNames: Set<string>) {
  return { ...Object.fromEntries(safeEnvironmentNames.filter(name => !secretNames.has(name) && base[name] !== undefined)
    .map(name => [name, base[name]])), ...offline };
}
function safePath(root: string, path: string): string | undefined {
  if (!path || isAbsolute(path) || path.includes("\0")) return undefined;
  const result = resolve(root, path), suffix = relative(root, result);
  return suffix === ".." || suffix.startsWith("../") || isAbsolute(suffix) ? undefined : result;
}
async function contained(root: string, path: string): Promise<boolean> {
  try {
    const suffix = relative(root, await realpath(path));
    return suffix !== ".." && !suffix.startsWith("../") && !isAbsolute(suffix);
  } catch { return false; }
}
function packageVersion(name: unknown, version: unknown): string | undefined {
  if (typeof name !== "string" || typeof version !== "string" || !/^@?[a-zA-Z0-9._+/-]{1,180}$/.test(name) ||
      !/^v?\d[a-zA-Z0-9.+_~-]{0,100}$/.test(version)) return undefined;
  return `${name}@${version}`;
}
function resolvedPackages(name: string, content: string): string[] {
  const found = new Set<string>();
  const add = (name: unknown, version: unknown) => { const value = packageVersion(name, version); if (value) found.add(value); };
  if (["package-lock.json", "npm-shrinkwrap.json"].includes(name)) {
    const value = JSON.parse(content);
    if (value.packages) for (const [path, entry] of Object.entries(value.packages)) {
      if (path) add(path.split("node_modules/").at(-1), (entry as any)?.version);
    }
    else {
      const walk = (entries: any) => { for (const [key, entry] of Object.entries(entries ?? {})) {
        add(key, (entry as any)?.version); walk((entry as any)?.dependencies);
      } };
      walk(value.dependencies);
    }
  } else if (["uv.lock", "poetry.lock", "Cargo.lock"].includes(name)) {
    const value = Bun.TOML.parse(content) as any;
    for (const entry of value.package ?? []) add(entry.name, entry.version);
  } else if (name === "bun.lock") {
    const value = Bun.JSONC.parse(content) as any;
    for (const entry of Object.values(value.packages ?? {})) {
      const selected = Array.isArray(entry) ? entry[0] : null;
      if (typeof selected === "string") { const at = selected.lastIndexOf("@"); add(selected.slice(0, at), selected.slice(at + 1)); }
    }
  } else if (name === "pnpm-lock.yaml") {
    const value = Bun.YAML.parse(content) as any;
    for (const key of Object.keys(value.packages ?? {})) {
      const selected = key.replace(/^\//, "").split("(")[0], at = selected.lastIndexOf("@");
      if (at > 0) add(selected.slice(0, at), selected.slice(at + 1));
    }
  } else if (name === "Pipfile.lock") {
    const value = JSON.parse(content);
    for (const entry of [value.default, value.develop]) for (const [name, value] of Object.entries(entry ?? {})) add(name, String((value as any)?.version ?? "").replace(/^==/, ""));
  } else if (name === "composer.lock") {
    const value = JSON.parse(content);
    for (const entry of [...value.packages ?? [], ...value["packages-dev"] ?? []]) add(entry.name, entry.version);
  } else if (name === "go.sum") {
    for (const line of content.split("\n")) { const [name, version] = line.split(/\s+/); add(name, version?.replace(/\/go.mod$/, "")); }
  }
  return [...found].sort();
}

export async function collectProjectEvidence(
  configuration: ProjectEnvironment, workspace: string, base: Record<string, string | undefined>,
  deps: EvidenceDependencies = dependencies,
): Promise<ProjectEvidence> {
  const result: ProjectEvidence = { tool_versions: {}, package_records: [] };
  let recordBytes = 0, truncated = false;
  const record = (value: string) => {
    const limited = value.slice(0, 2048), bytes = Buffer.byteLength(limited);
    if (result.package_records.length >= 999 || recordBytes + bytes > 240 * 1024) { truncated = true; return; }
    result.package_records.push(limited); recordBytes += bytes;
  };
  const root = await realpath(workspace);
  const secretNames = new Set((configuration.secrets ?? []).map(entry => entry.name));
  const secretValues = [...secretNames].map(name => base[name]).filter((value): value is string => Boolean(value));
  const scrub = (value: string) => secretValues.reduce((text, secret) => text.split(secret).join("[secret]"), value);
  const clean = safeEnvironment(base, secretNames);
  const roots = new Set([root]);
  const paths = new Set<string>();
  const explicit = new Set<string>();
  for (const declaration of (configuration.declarations ?? []).slice(0, 128)) {
    const path = safePath(root, declaration);
    if (path) { paths.add(path); explicit.add(path); roots.add(dirname(path)); }
    else record("A declaration path is outside the Project; its evidence remains unavailable.");
  }
  for (const directory of [configuration.setup?.directory, ...(configuration.services ?? []).map(service => service.directory)]) {
    const path = safePath(root, directory || ".");
    if (path) roots.add(path);
  }
  if (roots.size > 12) record("Tool observations cover the first 12 declared directories. Additional directories remain unverified.");
  let observed = 0;
  for (const directory of [...roots].slice(0, 12)) {
    const label = relative(root, directory) || ".";
    if (!await contained(root, directory) || !(await lstat(directory)).isDirectory()) {
      record(`Directory ${label}: unavailable; tools remain unverified.`); continue;
    }
    for (const name of declarations) paths.add(resolve(directory, name));
    const selected = await deps.execute(["mise", "env", "--json"], directory, clean);
    let env = clean;
    if (selected.exit_code === 0) {
      try { env = safeEnvironment({ ...clean, ...JSON.parse(selected.stdout) }, secretNames); }
      catch { record(`Tool selection at ${label}: mise returned invalid environment data.`); }
    } else record(`Tool selection at ${label}: mise exit ${selected.exit_code ?? "unavailable"}; observations use the supplied environment.`);
    const probes = [...(directory === root ? tools : tools.slice(0, 8))];
    const extras: Array<[string, Array<[string, string[]]>]> = [
      ["Cargo.toml", [["rustc", ["rustc", "--version"]], ["cargo", ["cargo", "--version"]]]],
      ["Gemfile.lock", [["ruby", ["ruby", "--version"]], ["bundle", ["bundle", "--version"]]]],
      ["composer.lock", [["php", ["php", "--version"]]]],
      ["pom.xml", [["java", ["java", "--version"]]]],
      ["packages.lock.json", [["dotnet", ["dotnet", "--version"]]]],
    ];
    for (const [file, commands] of extras) if (await lstat(resolve(directory, file)).catch(() => undefined)) probes.push(...commands);
    for (const [name, command] of probes) {
      if (observed++ >= 120) { record("Additional tool probes exceed the observation limit and remain unverified."); break; }
      let argv = command;
      if (name === "python" && await lstat(resolve(directory, ".venv/bin/python")).catch(() => undefined)) argv = [resolve(directory, ".venv/bin/python"), "--version"];
      const output = await deps.execute(argv, directory, env);
      const version = scrub(output.stdout).match(/\b(?:go|v)?\d+\.\d+(?:\.\d+)*(?:[-+][a-zA-Z0-9.-]+|rc\d+|beta\d+)?\b/)?.[0];
      const invoked = [isAbsolute(argv[0]) ? relative(root, argv[0]) : argv[0], ...argv.slice(1)].join(" ");
      record(`Tool ${name} at ${label}: ${invoked}; exit ${output.exit_code ?? "unavailable"}${version ? `; output version ${version}` : "; no version observed"}.`);
      if (output.exit_code === 0 && version) result.tool_versions[directory === root ? name : `${label}:${name}`] = version;
    }
  }
  let count = 0;
  for (const path of [...paths].sort()) {
    if (count >= 192) { record("Additional declaration files exceed the observation limit and remain unverified."); break; }
    const entry = await lstat(path).catch(() => undefined);
    const label = relative(root, path);
    if (!entry) { if (explicit.has(path)) record(`Declaration ${label}: unavailable.`); continue; }
    if (!entry.isFile() || !await contained(root, path)) { record(`Declaration ${label}: unavailable file type or outside the Project.`); continue; }
    count++;
    const hash = createHash("sha256");
    for await (const chunk of Bun.file(path).stream()) hash.update(chunk);
    record(`Declaration ${label}: sha256:${hash.digest("hex")}; ${entry.size} bytes.`);
    if (entry.size > 16 * 1024 * 1024) { record(`Declaration ${label}: package details exceed the parse limit.`); continue; }
    try {
      const packages = resolvedPackages(basename(path), await readFile(path, "utf8"));
      if (packages.length) {
        record(`Resolved packages in ${label}: ${packages.length} entries; ${scrub(packages.slice(0, 20).join(", "))}${packages.length > 20 ? "; further entries remain identified by the declaration hash" : ""}.`);
      }
    } catch { record(`Declaration ${label}: package details could not be parsed; the file hash remains recorded.`); }
  }
  if (truncated) result.package_records.push("Additional evidence exceeds the report limit and remains unverified.");
  return result;
}
