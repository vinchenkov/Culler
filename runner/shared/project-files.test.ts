import { expect, test } from "bun:test";
import { mkdtemp, readFile, rm, writeFile, mkdir, symlink } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { acceptProjectFiles, materializeProjectBuildFiles, projectFilesDigest, captureProjectInput, restoreDeniedProjectBuildFiles } from "./project-files";

test("setup acceptance captures checked files once and excludes ignored dependency files", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-project-files-"));
  const git = (...args: string[]) => {
    const result = Bun.spawnSync(["git", "-C", root, ...args], { env: process.env });
    if (result.exitCode !== 0) throw new Error(result.stderr.toString());
    return result.stdout.toString().trim();
  };
  try {
    git("init", "-b", "main");
    git("config", "user.name", "Culler test"); git("config", "user.email", "test@example.invalid");
    await writeFile(join(root, ".gitignore"), "installed.txt\n");
    await writeFile(join(root, "package.json"), '{"private":true}\n');
    await writeFile(join(root, "reference.txt"), "User reference\n");
    await writeFile(join(root, "installed.txt"), "download\n");
    const digest = await projectFilesDigest(root);
    await writeFile(join(root, "installed.txt"), "another download\n");
    expect(await projectFilesDigest(root)).toBe(digest);
    const commit = await acceptProjectFiles(root, digest);
    expect(git("show", `${commit}:reference.txt`)).toBe("User reference");
    expect(git("ls-files")).not.toContain("installed.txt");
    expect(await acceptProjectFiles(root, digest)).toBe(commit);
    expect(git("rev-list", "--count", "HEAD")).toBe("1");
    await writeFile(join(root, "package.json"), '{"private":false}\n');
    await expect(acceptProjectFiles(root, digest)).rejects.toThrow("changed after their checks");
    expect(git("rev-parse", "HEAD")).toBe(commit);
  } finally { await rm(root, { recursive: true, force: true }); }
});

async function projectFixture() {
  const root = await mkdtemp(join(tmpdir(), "culler-project-accept-"));
  const git = (...args: string[]) => {
    const result = Bun.spawnSync(["git", "-C", root, ...args], { env: process.env });
    if (result.exitCode !== 0) throw new Error(result.stderr.toString());
    return result.stdout.toString().trim();
  };
  git("init", "-b", "main");
  git("config", "user.name", "Culler test");
  git("config", "user.email", "test@example.invalid");
  await writeFile(join(root, "README.md"), "Project source\n");
  await writeFile(join(root, "Dockerfile"), "FROM initial\n");
  git("add", ".");
  git("commit", "-m", "Initial source");
  const source = git("rev-parse", "HEAD");
  const files = [
    { path: "Dockerfile", content: "FROM selected\nRUN install-tools\n" },
    { path: "install.sh", content: "#!/bin/bash\nset -eu\n" },
  ];
  for (const file of files) await writeFile(join(root, file.path), file.content);
  const digest = await projectFilesDigest(root);
  git("reset", "--hard", source);
  git("clean", "-fd");
  return { root, git, source, files, digest };
}

for (const staged of [false, true]) {
  test(`direct acceptance resumes after approved files are ${staged ? "staged" : "written"}`, async () => {
    const fixture = await projectFixture();
    const { root, git, files, source, digest } = fixture;
    try {
      // A prior attempt stopped after its first approved file.
      await writeFile(join(root, files[0].path), files[0].content);
      if (staged) git("add", "--", files[0].path);
      const commit = await acceptProjectFiles(root, digest, source, files, "operation-one");
      expect(commit).not.toBe(source);
      for (const file of files) expect(git("show", `${commit}:${file.path}`)).toBe(file.content.trim());
      expect(git("status", "--porcelain")).toBe("");
      expect(await acceptProjectFiles(root, digest, source, files, "operation-one")).toBe(commit);
      expect(git("rev-list", "--count", "HEAD")).toBe("2");
    } finally { await rm(root, { recursive: true, force: true }); }
  });
}

test("direct acceptance preserves changes outside the approved paths", async () => {
  const { root, git, files, source, digest } = await projectFixture();
  try {
    await writeFile(join(root, files[0].path), files[0].content);
    await writeFile(join(root, "README.md"), "Operator edit\n");
    git("add", "README.md");
    await expect(acceptProjectFiles(root, digest, source, files, "operation-one")).rejects.toThrow("outside the approved build files");
    expect(git("rev-parse", "HEAD")).toBe(source);
    expect(await readFile(join(root, "README.md"), "utf8")).toBe("Operator edit\n");
    expect(git("show", ":README.md")).toBe("Operator edit");
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("direct acceptance preserves different bytes in an approved path or its index entry", async () => {
  const { root, git, files, source, digest } = await projectFixture();
  try {
    await writeFile(join(root, files[0].path), "Operator Dockerfile\n");
    await expect(acceptProjectFiles(root, digest, source, files, "operation-one")).rejects.toThrow("differ from the approved build files");
    git("add", "Dockerfile");
    await writeFile(join(root, files[0].path), files[0].content);
    await expect(acceptProjectFiles(root, digest, source, files, "operation-one")).rejects.toThrow("staged changes differ");
    expect(git("show", ":Dockerfile")).toBe("Operator Dockerfile");
    expect(git("rev-parse", "HEAD")).toBe(source);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("direct acceptance checks the final files after the commit", async () => {
  const { root, git, files, source, digest } = await projectFixture();
  const tools = await mkdtemp(join(tmpdir(), "culler-accept-git-"));
  try {
    const realGit = Bun.which("git");
    if (!realGit) throw new Error("Git is unavailable.");
    await writeFile(join(tools, "git"), `#!/bin/bash
"$CULLER_TEST_GIT" "$@"
result=$?
if [ "$result" -eq 0 ]; then
  for argument in "$@"; do
    if [ "$argument" = commit ]; then
      printf 'Concurrent edit\\n' > "$CULLER_TEST_ROOT/Dockerfile"
      break
    fi
  done
fi
exit "$result"
`, { mode: 0o755 });
    const input = join(tools, "files.json");
    await writeFile(input, JSON.stringify({ files, removed_files: [] }));
    const child = Bun.spawn([process.execPath, join(import.meta.dir, "project-files.ts"), "accept", root, digest, source, input, "operation-one"], {
      env: { ...process.env, PATH: `${tools}:${process.env.PATH}`, CULLER_TEST_GIT: realGit, CULLER_TEST_ROOT: root },
      stdin: "ignore", stdout: "pipe", stderr: "pipe",
    });
    const [stdout, stderr, code] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited]);
    expect(code).toBe(1);
    expect(stdout).toBe("");
    expect(stderr).toContain("changed after source capture");
    expect(git("rev-list", "--count", "HEAD")).toBe("2");
    expect(await readFile(join(root, "Dockerfile"), "utf8")).toBe("Concurrent edit\n");
  } finally {
    await rm(root, { recursive: true, force: true });
    await rm(tools, { recursive: true, force: true });
  }
});

for (const replay of ["none", "deleted", "staged"] as const) {
  test(`direct acceptance captures approved removals and new files after ${replay} replay`, async () => {
    const { root, git, source, files } = await projectFixture();
    const additions = [files[1]];
    const removed = [{ path: "Dockerfile", content: "FROM initial\n" }];
    try {
      await materializeProjectBuildFiles(root, additions, removed);
      const digest = await projectFilesDigest(root);
      git("reset", "--hard", source);
      git("clean", "-fd");
      if (replay !== "none") await rm(join(root, "Dockerfile"));
      if (replay === "staged") git("add", "-u");
      const commit = await acceptProjectFiles(root, digest, source, additions, "remove-tools", removed);
      expect(commit).not.toBe(source);
      expect(git("ls-tree", "--name-only", commit)).not.toContain("Dockerfile");
      expect(git("show", `${commit}:install.sh`)).toBe(additions[0].content.trim());
      expect(git("show", `${commit}:README.md`)).toBe("Project source");
      expect(git("status", "--porcelain")).toBe("");
      expect(await acceptProjectFiles(root, digest, source, additions, "remove-tools", removed)).toBe(commit);
      expect(git("rev-list", "--count", "HEAD")).toBe("2");
    } finally { await rm(root, { recursive: true, force: true }); }
  });
}

test("direct acceptance captures an approved removal without new files", async () => {
  const { root, git, source } = await projectFixture();
  const removed = [{ path: "Dockerfile", content: "FROM initial\n" }];
  try {
    await materializeProjectBuildFiles(root, [], removed);
    const digest = await projectFilesDigest(root);
    git("reset", "--hard", source);
    const commit = await acceptProjectFiles(root, digest, source, [], "remove-tools", removed);
    expect(git("ls-tree", "--name-only", commit)).toBe("README.md");
    expect(git("status", "--porcelain")).toBe("");
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("approved removal preserves different source bytes and unrelated operator changes", async () => {
  const { root, git, source, digest } = await projectFixture();
  try {
    const invalid = [{ path: "Dockerfile", content: "Different prior contents\n" }];
    await expect(materializeProjectBuildFiles(root, [], invalid)).rejects.toThrow("differs from its accepted contents");
    expect(await readFile(join(root, "Dockerfile"), "utf8")).toBe("FROM initial\n");
    await rm(join(root, "Dockerfile"));
    git("add", "-u");
    await expect(acceptProjectFiles(root, digest, source, [], "remove-tools", invalid)).rejects.toThrow("source file differs");
    git("reset", "--hard", source);
    await writeFile(join(root, "README.md"), "Operator edit\n");
    const removed = [{ path: "Dockerfile", content: "FROM initial\n" }];
    await expect(acceptProjectFiles(root, digest, source, [], "remove-tools", removed)).rejects.toThrow("outside the approved build files");
    expect(await readFile(join(root, "README.md"), "utf8")).toBe("Operator edit\n");
    expect(await readFile(join(root, "Dockerfile"), "utf8")).toBe("FROM initial\n");
    expect(git("rev-parse", "HEAD")).toBe(source);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("approved removal rejects symlink parents and all invalid paths before any mutation", async () => {
  const root = await mkdtemp(join(tmpdir(), "culler-remove-path-"));
  const outside = await mkdtemp(join(tmpdir(), "culler-remove-outside-"));
  try {
    await writeFile(join(root, "keep.txt"), "Keep\n");
    await writeFile(join(outside, "tools.txt"), "Tools\n");
    await symlink(outside, join(root, "linked"));
    await expect(materializeProjectBuildFiles(root, [], [
      { path: "keep.txt", content: "Keep\n" }, { path: "linked/tools.txt", content: "Tools\n" },
    ])).rejects.toThrow("symbolic link");
    expect(await readFile(join(root, "keep.txt"), "utf8")).toBe("Keep\n");
    expect(await readFile(join(outside, "tools.txt"), "utf8")).toBe("Tools\n");
    await expect(materializeProjectBuildFiles(root, [{ path: "new.txt", content: "New" }], [
      { path: "../outside", content: "" },
    ])).rejects.toThrow("invalid path");
    expect(await Bun.file(join(root, "new.txt")).exists()).toBe(false);
    await expect(materializeProjectBuildFiles(root, [{ path: "keep.txt", content: "New" }], [
      { path: "keep.txt", content: "Keep\n" },
    ])).rejects.toThrow("repeated path");
    await mkdir(join(root, "folder"));
    await materializeProjectBuildFiles(root, [], [{ path: "folder/absent.txt", content: "Absent" }]);
    expect(await readFile(join(root, "keep.txt"), "utf8")).toBe("Keep\n");
  } finally {
    await rm(root, { recursive: true, force: true });
    await rm(outside, { recursive: true, force: true });
  }
});


test("denial restores original build files and excludes proposed additions from accepted source", async () => {
  const { root, git, files } = await projectFixture();
  try {
    const original = Buffer.from([0, 255, 12, 42]);
    await writeFile(join(root, "Dockerfile"), original);
    await writeFile(join(root, "untracked.txt"), "Original user file\n");
    await writeFile(join(root, ".gitignore"), "retained.txt\n");
    await writeFile(join(root, "retained.txt"), "Original tracked file\n");
    git("add", "-f", "retained.txt");
    const index = git("diff", "--cached");
    const tree = await captureProjectInput(root, join(root, ".git", "setup-input-index"));
    expect(git("rev-parse", `refs/culler/setup-input/${tree}`)).toBe(tree);
    expect(git("diff", "--cached")).toBe(index);
    expect(git("show", `${tree}:retained.txt`)).toBe("Original tracked file");
    for (const file of files) await writeFile(join(root, file.path), file.content);
    await writeFile(join(root, "dependency.txt"), "Useful preparation\n");
    await restoreDeniedProjectBuildFiles(root, files, tree);
    await restoreDeniedProjectBuildFiles(root, files, tree);
    expect(await readFile(join(root, "Dockerfile"))).toEqual(original);
    expect(await Bun.file(join(root, "install.sh")).exists()).toBe(false);
    const accepted = await acceptProjectFiles(root, await projectFilesDigest(root));
    expect(git("show", `${accepted}:untracked.txt`)).toBe("Original user file");
    expect(git("show", `${accepted}:dependency.txt`)).toBe("Useful preparation");
    expect(git("ls-tree", accepted, "--", "install.sh")).toBe("");
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("denial preserves a later operator edit and refuses partial restoration", async () => {
  const { root, git, files } = await projectFixture();
  try {
    const tree = await captureProjectInput(root, join(root, ".git", "setup-input-index"));
    expect(git("rev-parse", `refs/culler/setup-input/${tree}`)).toBe(tree);
    for (const file of files) await writeFile(join(root, file.path), file.content);
    await writeFile(join(root, files[1].path), "Operator edit\n");
    await expect(restoreDeniedProjectBuildFiles(root, files, tree)).rejects.toThrow("changed after the proposal");
    expect(await readFile(join(root, files[0].path), "utf8")).toBe(files[0].content);
    expect(await readFile(join(root, files[1].path), "utf8")).toBe("Operator edit\n");
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("initial source acceptance preserves files that differ from the approved image", async () => {
  const { root, git, files, source } = await projectFixture();
  try {
    const digest = await projectFilesDigest(root);
    await expect(acceptProjectFiles(root, digest, undefined, files)).rejects.toThrow("differs from the checked image");
    expect(git("rev-parse", "HEAD")).toBe(source);
    expect(await readFile(join(root, "Dockerfile"), "utf8")).toBe("FROM initial\n");
    await materializeProjectBuildFiles(root, files);
    const prepared = await projectFilesDigest(root);
    const commit = await acceptProjectFiles(root, prepared, undefined, files);
    expect(commit).not.toBe(source);
    expect(git("show", `${commit}:Dockerfile`)).toBe(files[0].content.trim());
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("a setup retry retains the files from before the first agent", async () => {
  const { root, git, files } = await projectFixture();
  try {
    const original = await readFile(join(root, "Dockerfile"), "utf8");
    const first = await captureProjectInput(root, join(root, ".git", "first-index"), "initial-setup");
    for (const file of files) await writeFile(join(root, file.path), file.content);
    const retry = await captureProjectInput(root, join(root, ".git", "retry-index"), "initial-setup");
    expect(retry).toBe(first);
    await restoreDeniedProjectBuildFiles(root, files, retry);
    expect(await readFile(join(root, "Dockerfile"), "utf8")).toBe(original);
    expect(await Bun.file(join(root, "install.sh")).exists()).toBe(false);
    expect(git("status", "--porcelain")).toBe("");
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("source acceptance refuses denied image files that the continuation recreates", async () => {
  const { root, git, source, files } = await projectFixture();
  try {
    const tree = await captureProjectInput(root, join(root, ".git", "input-index"), "initial-setup");
    await materializeProjectBuildFiles(root, files);
    await restoreDeniedProjectBuildFiles(root, files, tree);
    await writeFile(join(root, files[1].path), files[1].content);
    const recreated = await projectFilesDigest(root);
    const denied = { files, tree };
    await expect(acceptProjectFiles(root, recreated, undefined, [], undefined, [], denied)).rejects.toThrow("changed after the proposal");
    expect(git("rev-parse", "HEAD")).toBe(source);
    expect(await readFile(join(root, files[1].path), "utf8")).toBe(files[1].content);
    await restoreDeniedProjectBuildFiles(root, files, tree);
    const commit = await acceptProjectFiles(root, await projectFilesDigest(root), undefined, [], undefined, [], denied);
    expect(git("ls-tree", commit, "--", files[1].path)).toBe("");
  } finally { await rm(root, { recursive: true, force: true }); }
});
