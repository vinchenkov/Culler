import { prepareProjectEnvironment, readEnvironmentSecrets, redactWorkflowOutput, type ProjectEnvironment } from "./project-environment";
import { verifyProjectBuildFiles } from "./project-files";
import { collectProjectEvidence } from "./project-evidence";

export async function checkProject(configuration: ProjectEnvironment, workspace: string, secrets: Record<string, string>) {
  const files = [
    ...(configuration.dockerfile ? [configuration.dockerfile] : []), ...configuration.build_files,
  ];
  await verifyProjectBuildFiles(workspace, files);
  const workflow = await prepareProjectEnvironment(configuration, workspace, process.env, secrets);
  try {
    await verifyProjectBuildFiles(workspace, files);
    const evidence = await collectProjectEvidence(configuration, workspace, workflow.agentEnvironment);
    return evidence;
  } finally { await workflow.stop(); }
}

if (import.meta.main) {
  let secrets: Record<string, string> = {};
  try {
    const { configuration } = await Bun.file("/mc/environment.json").json();
    secrets = await readEnvironmentSecrets();
    const result = await checkProject(configuration, "/workspace/source", secrets);
    console.log(JSON.stringify(result));
    process.exitCode = 0;
  } catch (error) {
    console.error(redactWorkflowOutput(String(error), secrets));
    process.exitCode = 1;
  }
}
