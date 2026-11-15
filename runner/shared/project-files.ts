import { createHash } from "node:crypto";
import { copyFile, lstat, readFile, readlink, mkdir, writeFile, unlink, chmod, symlink } from "node:fs/promises";
import { join, resolve } from "node:path";

export interface ProjectBuildFile { path: string; content: string }

function assertBuildPaths(files: ProjectBuildFile[], removedFiles: ProjectBuildFile[]): void {
  const paths = new Set<string>();
  for (const file of [...files, ...removedFiles]) {
    if (!file || typeof file.path !== "string" || typeof file.content !== "string" || !file.path ||
        file.path.split("/").some(part => !part || part === "." || part === ".." || part === ".git") || /[\\\0]/.test(file.path)) {
      throw new Error("The approved build file has an invalid path.");
    }
    if (paths.has(file.path)) throw new Error("The approved build files contain a repeated path.");
    paths.add(file.path);
  }
}

async function buildFileEntry(root: string, file: ProjectBuildFile) {
  let path = root;
  const segments = file.path.split("/");
  for (const [index, part] of segments.entries()) {
    path = join(path, part);
    const entry = await lstat(path).catch(error => { if (error.code === "ENOENT") return undefined; throw error; });
    if (!entry) return undefined;
    if (index === segments.length - 1) {
      if (!entry.isFile()) throw new Error("The approved build file replaces a symbolic link or another file type.");
      return entry;
    }
    if (!entry.isDirectory()) throw new Error("A build file directory is a symbolic link or another file.");
  }
}

export async function verifyProjectBuildFiles(root: string, files: ProjectBuildFile[]): Promise<void> {
  for (const file of files) {
    const segments = file.path.split("/");
    if (!file.path || segments.some(part => !part || part === "." || part === ".." || part === ".git") || /[\\\0]/.test(file.path)) {
      throw new Error("The approved build file has an invalid path.");
    }
    let path = root;
    for (const [index, part] of segments.entries()) {
      path = join(path, part);
      const entry = await lstat(path).catch(() => undefined);
      if (!entry || (index === segments.length - 1 ? !entry.isFile() : !entry.isDirectory())) {
        throw new Error(`The approved build file ${file.path} is unavailable in this source.`);
      }
    }
    if (!(await readFile(path)).equals(Buffer.from(file.content))) {
      throw new Error(`The build file ${file.path} differs from the checked image. Propose and validate its environment change.`);
    }
  }
}

export async function materializeProjectBuildFiles(root: string, files: ProjectBuildFile[], removedFiles: ProjectBuildFile[] = []): Promise<void> {
  assertBuildPaths(files, removedFiles);
  for (const file of files) await buildFileEntry(root, file);
  for (const file of removedFiles) {
    const entry = await buildFileEntry(root, file);
    if (entry && !(await readFile(join(root, file.path))).equals(Buffer.from(file.content))) {
      throw new Error(`The build file ${file.path} differs from its accepted contents. Preserve the file and validate its removal.`);
    }
  }
  for (const file of removedFiles) {
    const entry = await buildFileEntry(root, file);
    if (!entry) continue;
    if (!(await readFile(join(root, file.path))).equals(Buffer.from(file.content))) {
      throw new Error(`The build file ${file.path} changed before its removal. Validate the current files.`);
    }
    await unlink(join(root, file.path));
  }
  for (const file of files) {
    const segments = file.path.split("/");
    let directory = root;
    for (const part of segments.slice(0, -1)) {
      directory = join(directory, part);
      await mkdir(directory).catch((error) => { if (error.code !== "EEXIST") throw error; });
      if (!(await lstat(directory)).isDirectory()) throw new Error("A build file directory is a symbolic link or another file.");
    }
    const path = join(root, file.path);
    const entry = await lstat(path).catch((error) => { if (error.code === "ENOENT") return undefined; throw error; });
    if (entry && !entry.isFile()) throw new Error("The approved build file replaces a symbolic link or another file type.");
    await writeFile(path, file.content, { mode: 0o644 });
  }
}

async function git(root: string, args: string[]): Promise<string> {
  const child = Bun.spawn(["git", "--literal-pathspecs", "-C", root, ...args], { stdin: "ignore", stdout: "pipe", stderr: "pipe" });
  const [output, error, code] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited]);
  if (code !== 0) throw new Error(`The Project Git command failed: ${error.slice(-2048)}`);
  return output;
}

export async function captureProjectInput(root: string, index: string, setup?: string): Promise<string> {
  if (setup && !/^[A-Za-z0-9_-]{1,128}$/.test(setup)) throw new Error("The original Project file identity is invalid.");
  const reference = setup ? `refs/culler/setup-input/${setup}` : undefined;
  if (reference) {
    const child = Bun.spawn(["git", "-C", root, "show-ref", "--verify", "--quiet", reference], { stdout: "ignore", stderr: "pipe" });
    const [error, code] = await Promise.all([new Response(child.stderr).text(), child.exited]);
    if (code === 0) return (await git(root, ["rev-parse", "--verify", `${reference}^{tree}`])).trim();
    if (code !== 1 || error.trim()) throw new Error("The original Project files are unavailable.");
  }
  const run = async (args: string[]) => {
    const child = Bun.spawn(["git", "--literal-pathspecs", "-C", root, ...args], {
      env: { ...process.env, GIT_INDEX_FILE: index }, stdin: "ignore", stdout: "pipe", stderr: "pipe",
    });
    const [output, error, code] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited]);
    if (code !== 0) throw new Error(`The original Project files could not be retained: ${error.slice(-2048)}`);
    return output.trim();
  };
  try {
    const sourceIndex = (await git(root, ["rev-parse", "--git-path", "index"])).trim();
    try { await copyFile(resolve(root, sourceIndex), index); }
    catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
      await run(["read-tree", "--empty"]);
    }
    await run(["add", "-A", "--", "."]);
    const tree = await run(["write-tree"]);
    await run(["update-ref", reference ?? `refs/culler/setup-input/${tree}`, tree]);
    return tree;
  } finally { await unlink(index).catch(() => {}); }
}

async function deniedProjectBuildFileChanges(root: string, proposed: ProjectBuildFile[], tree: string) {
  if (!/^[0-9a-f]{40}(?:[0-9a-f]{24})?$/.test(tree)) throw new Error("The original Project file identity is invalid.");
  assertBuildPaths(proposed, []);
  const changes: Array<{ file: ProjectBuildFile; content?: Buffer; mode?: string }> = [];
  for (const file of proposed) {
    const original = (await git(root, ["ls-tree", "-z", tree, "--", file.path])).split("\0").filter(Boolean);
    const mode = original[0]?.split(" ")[0];
    if (original.length > 1 || mode && !["100644", "100755", "120000"].includes(mode)) {
      throw new Error("A denied build file conflicts with an original Project directory.");
    }
    let content: Buffer | undefined;
    if (mode) {
      const child = Bun.spawn(["git", "--literal-pathspecs", "-C", root, "show", `${tree}:${file.path}`], { stdin: "ignore", stdout: "pipe", stderr: "pipe" });
      const [bytes, error, code] = await Promise.all([new Response(child.stdout).arrayBuffer(), new Response(child.stderr).text(), child.exited]);
      if (code !== 0) throw new Error(`The original Project file is unavailable: ${error.slice(-2048)}`);
      content = Buffer.from(bytes);
    }
    const path = join(root, file.path);
    const current = await lstat(path).catch(error => { if (error.code === "ENOENT") return undefined; throw error; });
    if (!current && content === undefined) continue;
    if (current?.isSymbolicLink() && mode === "120000" && await readlink(path) === content?.toString()) continue;
    await buildFileEntry(root, file);
    const actual = current ? await readFile(path) : undefined;
    if (actual && content && actual.equals(content)) continue;
    if (!actual?.equals(Buffer.from(file.content))) throw new Error(`The Project file ${file.path} changed after the proposal. Preserve it before setup continues.`);
    changes.push({ file, content, mode });
  }
  return changes;
}

export async function restoreDeniedProjectBuildFiles(root: string, proposed: ProjectBuildFile[], tree: string): Promise<void> {
  for (const { file, content, mode } of await deniedProjectBuildFileChanges(root, proposed, tree)) {
    const path = join(root, file.path);
    await buildFileEntry(root, file);
    if (!(await readFile(path)).equals(Buffer.from(file.content))) {
      throw new Error(`The Project file ${file.path} changed before restoration. Preserve it before setup continues.`);
    }
    if (content === undefined) await unlink(path);
    else if (mode === "120000") { await unlink(path); await symlink(content.toString(), path); }
    else { await writeFile(path, content); await chmod(path, mode === "100755" ? 0o755 : 0o644); }
  }
}

export async function projectFilesDigest(root: string): Promise<string> {
  const names = [...new Set((await git(root, ["ls-files", "--cached", "--others", "--exclude-standard", "-z"]))
    .split("\0").filter(Boolean))].sort();
  const hash = createHash("sha256");
  for (const name of names) {
    if (name.startsWith("/") || name.split("/").includes("..")) throw new Error("The Project file path is invalid.");
    const path = join(root, name);
    const entry = await lstat(path).catch((error) => { if (error.code === "ENOENT") return undefined; throw error; });
    if (!entry) continue;
    if (entry.isSymbolicLink()) {
      hash.update("link\0").update(name).update("\0").update(await readlink(path)).update("\0");
    } else if (entry.isFile()) {
      hash.update("file\0").update(name).update("\0").update(String(entry.mode & 0o111)).update("\0");
      const fileHash = createHash("sha256");
      for await (const chunk of Bun.file(path).stream()) fileHash.update(chunk);
      hash.update(fileHash.digest("hex")).update("\0");
    } else if (entry.isDirectory()) {
      hash.update("submodule\0").update(name).update("\0").update(await projectFilesDigest(path)).update("\0");
    } else {
      throw new Error(`The Project file ${name} requires a separate source declaration.`);
    }
  }
  return `sha256:${hash.digest("hex")}`;
}

async function assertApprovedReplay(root: string, files: ProjectBuildFile[], removedFiles: ProjectBuildFile[], sourceCommit: string): Promise<void> {
  assertBuildPaths(files, removedFiles);
  const approved = new Map(files.map(file => [file.path, file.content]));
  const removed = new Map(removedFiles.map(file => [file.path, file.content]));
  for (const file of removedFiles) {
    const source = (await git(root, ["ls-tree", "-z", sourceCommit, "--", file.path])).split("\0").filter(Boolean);
    if (source.length && (source.length !== 1 || !/^100(644|755) blob /.test(source[0]) ||
        await git(root, ["show", `${sourceCommit}:${file.path}`]) !== file.content)) {
      throw new Error("The source file differs from the accepted contents of the approved removal.");
    }
  }
  const changed = (await git(root, ["diff", "--name-only", "--no-renames", "-z"]))
    .split("\0").filter(Boolean);
  const staged = (await git(root, ["diff", "--cached", "--name-only", "--no-renames", "-z"]))
    .split("\0").filter(Boolean);
  const untracked = (await git(root, ["ls-files", "--others", "--exclude-standard", "-z"]))
    .split("\0").filter(Boolean);
  for (const path of new Set([...changed, ...staged, ...untracked])) {
    if (removed.has(path)) {
      if (await buildFileEntry(root, { path, content: removed.get(path)! })) {
        throw new Error("The checked source contains changes that differ from the approved file removal.");
      }
      if (staged.includes(path) && await git(root, ["ls-files", "--stage", "-z", "--", path])) {
        throw new Error("The staged changes differ from the approved file removal.");
      }
      continue;
    }
    const content = approved.get(path);
    if (content === undefined) throw new Error("The checked source has unaccepted file changes outside the approved build files.");
    const entry = await lstat(join(root, path)).catch(() => undefined);
    if (!entry?.isFile() || !(await readFile(join(root, path))).equals(Buffer.from(content))) {
      throw new Error("The checked source contains changes that differ from the approved build files.");
    }
    if (staged.includes(path)) {
      const index = (await git(root, ["ls-files", "--stage", "-z", "--", path])).split("\0").filter(Boolean);
      const mode = entry.mode & 0o111 ? "100755" : "100644";
      if (index.length !== 1 || !index[0].startsWith(`${mode} `) || !index[0].includes(` 0\t${path}`) ||
          await git(root, ["show", `:${path}`]) !== content) {
        throw new Error("The staged changes differ from the approved build files.");
      }
    }
  }
}

async function assertCapturedFiles(root: string, expected: string): Promise<void> {
  if (await projectFilesDigest(root) !== expected || (await git(root, ["status", "--porcelain"])).trim()) {
    throw new Error("The Project files changed after source capture. Validate the current files.");
  }
}

export async function acceptProjectFiles(root: string, expected: string, sourceCommit?: string, files?: ProjectBuildFile[], operation?: string,
    removedFiles: ProjectBuildFile[] = [], denied?: { tree: string; files: ProjectBuildFile[] }): Promise<string> {
  if (denied) {
    const changes = await deniedProjectBuildFileChanges(root, denied.files, denied.tree);
    if (changes.length) throw new Error(`The Project file ${changes[0].file.path} changed after the proposal. Preserve it before setup continues.`);
  }
  if (sourceCommit) {
    const current = (await git(root, ["rev-parse", "HEAD"])).trim();
    const message = `Apply Project environment ${operation}`;
    if (files && current !== sourceCommit && operation &&
        (await git(root, ["log", "-1", "--format=%s"])).trim() === message &&
        (await git(root, ["rev-parse", "HEAD^"])).trim() === sourceCommit) {
      await assertCapturedFiles(root, expected);
      return current;
    }
    if (current !== sourceCommit) throw new Error("The checked source commit differs from the accepted Project source.");
    if (files) await assertApprovedReplay(root, files, removedFiles, sourceCommit);
    else if ((await git(root, ["status", "--porcelain"])).trim()) throw new Error("The checked source has unaccepted file changes.");
    if (files) await materializeProjectBuildFiles(root, files, removedFiles);
    if (await projectFilesDigest(root) !== expected) throw new Error("The Project files changed after their checks. Validate the current files.");
    if (files && (files.length || removedFiles.length) && (await git(root, ["status", "--porcelain"])).trim()) {
      if (files.length) await git(root, ["add", "--", ...files.map((file) => file.path)]);
      if (removedFiles.length) await git(root, ["rm", "--cached", "--ignore-unmatch", "--", ...removedFiles.map(file => file.path)]);
      if (await projectFilesDigest(root) !== expected || (await git(root, ["diff", "--name-only", "-z"]))) {
        throw new Error("The Project files changed during setup acceptance. Validate the current files.");
      }
      await assertApprovedReplay(root, files, removedFiles, sourceCommit);
      const changedPaths = (await git(root, ["diff", "--cached", "--name-only", "--no-renames", "-z"])).split("\0").filter(Boolean);
      await git(root, ["-c", "core.hooksPath=/dev/null", "commit", "--only", "-m", message, "--", ...changedPaths]);
      await assertCapturedFiles(root, expected);
      return (await git(root, ["rev-parse", "HEAD"])).trim();
    }
    await assertCapturedFiles(root, expected);
    return current;
  }
  if (files) await verifyProjectBuildFiles(root, files);
  if (await projectFilesDigest(root) !== expected) throw new Error("The Project files changed after their checks. Validate the current files.");
  await git(root, ["add", "-A", "--", "."]);
  if (await projectFilesDigest(root) !== expected) throw new Error("The Project files changed during setup acceptance. Validate the current files.");
  const dirty = (await git(root, ["diff", "--cached", "--name-only"])).trim();
  const head = await git(root, ["rev-parse", "--verify", "HEAD"]).catch(() => "");
  if (dirty || !head) await git(root, ["-c", "core.hooksPath=/dev/null", "commit", "--allow-empty", "-m", "Initialize the Project development environment"]);
  if (await projectFilesDigest(root) !== expected) throw new Error("The Project files changed after source capture. Validate the current files.");
  return (await git(root, ["rev-parse", "HEAD"])).trim();
}

if (import.meta.main) {
  const [action, root, expected, commit, configurationPath, operation] = process.argv.slice(2);
  try {
    if (action === "digest" && root) console.log(await projectFilesDigest(root));
    else if (action === "accept" && root && expected) {
      const configuration = configurationPath ? await Bun.file(configurationPath).json() as { files: ProjectBuildFile[]; removed_files?: ProjectBuildFile[]; denied?: { tree: string; files: ProjectBuildFile[] } } : undefined;
      console.log(JSON.stringify({ source_commit: await acceptProjectFiles(root, expected, commit || undefined,
        configuration?.files, operation, configuration?.removed_files, configuration?.denied), files_digest: expected }));
    }
    else throw new Error("Use digest or accept with the Project directory.");
  } catch (error) { console.error(String(error)); process.exit(1); }
}
