const states: Record<string, { commit: string | null; branch: string | null; clean: boolean }> = {};
for (const role of process.argv.slice(2)) {
  if (!["authority", "packager"].includes(role)) throw new Error("The repository role is invalid.");
  const run = (args: string[]) => {
    const result = Bun.spawnSync(["git", "--no-pager", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-c", "safe.directory=/evidence/" + role, "-C", "/evidence/" + role, ...args],
      { env: { PATH: "/usr/local/bin:/usr/bin:/bin", HOME: "/tmp", GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: "/dev/null" } });
    return result.exitCode === 0 ? result.stdout.toString().trim() : null;
  };
  states[role] = { commit: run(["rev-parse", "HEAD"]), branch: run(["rev-parse", "--symbolic-full-name", "HEAD"]),
    clean: run(["status", "--porcelain=v1", "--untracked-files=all", "--ignored"]) === "" };
}
console.log(JSON.stringify({ states }));
