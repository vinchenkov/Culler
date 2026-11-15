let url = process.env.CULLER_EVAL_COMMAND_URL;
let token = process.env.CULLER_EVAL_COMMAND_TOKEN;
try {
  const file = Bun.file("/mc/eval-command.json");
  if (await file.exists()) {
    const capability = await file.json();
    url = capability.url;
    token = capability.token;
  }
} catch {
  console.error("The evaluation command capability is invalid.");
  process.exit(2);
}
if (typeof url !== "string" || !url || typeof token !== "string" || !token) {
  console.error("The evaluation command adapter is absent.");
  process.exit(2);
}
try {
  const response = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ argv: process.argv.slice(2) }),
  });
  if (!response.ok) throw new Error(`The command adapter refused the request (${response.status}).`);
  const result = await response.json() as { exit_code: number; stdout: unknown; stderr: string };
  if (result.stdout !== null) console.log(JSON.stringify(result.stdout));
  if (result.stderr) console.error(result.stderr);
  process.exit(result.exit_code);
} catch (error) {
  console.error(error instanceof Error ? error.message : "The command adapter failed.");
  process.exit(2);
}
