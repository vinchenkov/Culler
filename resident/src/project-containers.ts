import type { Exec } from "./types";

function label(argv: string[], key: string): string | undefined {
  for (let i = 0; i + 1 < argv.length; i++) {
    if (argv[i] === "--label" && argv[i + 1]!.startsWith(`${key}=`)) return argv[i + 1]!.slice(key.length + 1);
  }
  return undefined;
}

// The host command serializes Project launches with Project deletion.
export function projectContainers(docker: Exec, runMc: Exec): Exec {
  return async (argv, input, options) => {
    let project: string | undefined;
    if (argv[0] === "create" || argv[0] === "run" || argv[0] === "build") {
      project = label(argv, "mc-project") ?? label(argv, "com.culler.project");
    } else if (argv[0] === "start") {
      const inspection = await docker(["inspect", "--type", "container", argv.at(-1)!]);
      if (inspection.exitCode !== 0) return inspection;
      const records = JSON.parse(inspection.stdout);
      if (!Array.isArray(records) || records.length !== 1) throw new Error("The container inspection is invalid.");
      const labels = records[0].Config?.Labels;
      project = labels?.["mc-project"] ?? labels?.["com.culler.project"];
    }
    if (project === undefined) return docker(argv, input, options);
    const response = await runMc(["__project-docker", project, "--", ...argv], input, options);
    if (response.exitCode !== 0) return response;
    const result = JSON.parse(response.stdout);
    if (!result || !Number.isInteger(result.exitCode) || typeof result.stdout !== "string" || typeof result.stderr !== "string") {
      throw new Error("The Project container result is invalid.");
    }
    return result;
  };
}
