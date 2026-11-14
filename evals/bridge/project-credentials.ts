import { resolve } from "node:path";
import { readFile, realpath, stat } from "node:fs/promises";
import { createInterface } from "node:readline";
import { residentProjection } from "./resident-credentials";

const [profilePath, binding, sourceRoot] = process.argv.slice(2);
let handle: Awaited<ReturnType<typeof residentProjection>> | undefined;
let category = "credentials";
try {
  if (!profilePath || !binding || !sourceRoot) throw new Error("The projection requires a profile and binding.");
  const profileLocation = await realpath(profilePath);
  const info = await stat(profileLocation);
  if (!info.isFile() || (info.mode & 0o777) !== 0o600 || (process.getuid && info.uid !== process.getuid())) {
    throw new Error("The profile requires private permissions.");
  }
  const profile = JSON.parse(await readFile(profileLocation, "utf8"));
  if (profile.purpose !== "directive-evaluation") throw new Error("The profile must declare its evaluation purpose.");
  if (typeof profile.resident_home !== "string" || profile.grant_files !== undefined) {
    category = "connection_required";
    throw new Error("The evaluation requires a saved Culler connection.");
  }
  const spec = profile.catalog?.[binding];
  const fields = ["harness", "credential", "channel", "kind", "inference_url", "token_url", "client_id"];
  if (!spec || typeof spec !== "object" || Array.isArray(spec) || Object.keys(spec).some(key => !fields.includes(key)) ||
      !/^[a-z0-9][a-z0-9_.-]{0,63}$/.test(binding) || !/^[a-z0-9][a-z0-9_.-]{0,63}$/.test(spec.credential) ||
      !["codex", "claude"].includes(spec.harness) || spec.channel !== spec.harness ||
      ![`${spec.channel}-oauth`, `${spec.channel}-api-key`].includes(spec.kind) || typeof spec.inference_url !== "string") {
    throw new Error("The evaluation connection profile is invalid.");
  }
  handle = await residentProjection(profile.resident_home, binding, spec, resolve(sourceRoot));
  console.log(JSON.stringify({ status: "ready" }));
  const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
  for await (const line of lines) {
    const request = JSON.parse(line);
    if (request.action === "close") break;
    if (request.action === "project") {
      console.log(JSON.stringify({ status: "ready", projection: await handle.current(request.model, request.duration_seconds) }));
    } else if (request.action === "release") {
      console.log(JSON.stringify({ status: "ready", provider_requests: await handle.release(request.grant_id) }));
    } else throw new Error("The credential protocol action is unsupported.");
  }
  await handle.stop();
  console.log(JSON.stringify({ status: "closed" }));
} catch {
  try { await handle?.stop(); } catch {}
  console.log(JSON.stringify({ status: "failure", category }));
  process.exitCode = 2;
}
