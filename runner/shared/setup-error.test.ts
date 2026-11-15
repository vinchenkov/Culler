import { expect, test } from "bun:test";
import { diagnosticRedactor } from "../agent-runner/diagnostics";
import { setupError, setupProcessError, setupText } from "./setup-error";

test("a terminal harness message takes precedence over informational output", () => {
  const failure = setupProcessError({ exitCode: 1,
    stdout: JSON.stringify({ event: "infrastructure-failure", condition: "The API host could not resolve.", evidence: "getaddrinfo ENOTFOUND api.example.test" }),
    stderr: JSON.stringify({ level: "info", event: "runner.skill_installed", msg: "Installed four skills." })
      + '\ntime="2026-10-17T10:36:47-07:00" level=error msg="error waiting for container: unexpected EOF"',
  });
  expect(failure.message).toBe("The API host could not resolve.");
  expect(failure.details).toContain("ENOTFOUND");
  expect(failure.details).toContain("runner.skill_installed");
});

test("informational records cannot become a failure message", () => {
  const failure = setupProcessError({ exitCode: 2, stdout: "", stderr: '{"level":"info","msg":"Installed skills."}' });
  expect(failure.message).toBe("The setup process exited with code 2.");
  expect(failure.details).toContain("Installed skills.");
});

test("a Docker wait error supplies the cause without an assumed shutdown", () => {
  const stderr = '{"level":"info","event":"runner.skill_installed","msg":"Installed skills."}\n'
    + 'time="2026-10-17T10:36:47-07:00" level=error msg="error waiting for container: unexpected EOF"';
  const failure = setupProcessError({ exitCode: 125, stdout: "", stderr });
  expect(failure.message).toBe("Docker could not wait for the setup container: unexpected EOF");
  expect(failure.details).toContain(stderr);
  expect(failure.details).toContain("Exit code: 125");
});

test.each([
  "Error response from daemon: the container is unavailable",
  "docker: Error response from daemon: the container is unavailable",
  "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
  "error during connect: connection refused",
])("a Docker command error supplies the cause: %s", stderr => {
  const failure = setupProcessError({ exitCode: 125, stdout: "", stderr });
  expect(failure.message).toBe(`Docker reported an error: ${stderr.replace(/^docker: /, "")}`);
  expect(failure.details).toContain(stderr);
});

test.each([
  "",
  "Installed four skills.",
  'time="2026-10-17T10:36:47-07:00" level=info msg="Installed skills."',
  'time="2026-10-17T10:36:47-07:00" level=error msg="dependency cache unavailable"',
  'time="2026-10-17T10:36:47-07:00" level=fatal msg="dependency cache unavailable"',
  'time="2026-10-17T10:36:47-07:00" level=error msg="unterminated',
])("an unknown cause uses the exit code: %s", stderr => {
  const failure = setupProcessError({ exitCode: 133, stdout: "", stderr });
  expect(failure.message).toBe("The setup process exited with code 133.");
  if (stderr) expect(failure.details).toContain(stderr);
});

test("a Docker error decodes quotes and removes secrets before the byte limit", () => {
  const secret = "private-project-value";
  const message = `error waiting for container: "setup" ${secret} Bearer private-access-value ${"失".repeat(5000)}`;
  const stderr = `time="2026-10-17T10:36:47-07:00" level=error msg=${JSON.stringify(message)}`;
  const failure = setupProcessError({ exitCode: 125, stdout: "", stderr }, diagnosticRedactor({}, [secret]));
  expect(failure.message).toStartWith('Docker could not wait for the setup container: "setup" [redacted] Bearer [redacted]');
  for (const text of [failure.message, failure.details]) {
    expect(text).not.toContain(secret);
    expect(text).not.toContain("private-access-value");
    expect(text).toContain("[diagnostic text omitted]");
  }
  expect(new TextEncoder().encode(failure.message).length).toBeLessThanOrEqual(4000);
  expect(new TextEncoder().encode(failure.details).length).toBeLessThanOrEqual(24000);
});

test("secret redaction precedes byte limits and preserves the failure message", () => {
  const secret = "private-project-value";
  const redact = diagnosticRedactor({}, [secret]);
  const failure = setupProcessError({ exitCode: 1,
    stdout: JSON.stringify({ event: "infrastructure-failure", condition: `Request failed: ${secret}`, evidence: `${secret}\n${"失".repeat(16000)}` }),
    stderr: `Bearer private-access-value\n${secret}`,
  }, redact);
  expect(failure.message).toBe("Request failed: [redacted]");
  expect(failure.details).not.toContain(secret);
  expect(failure.details).not.toContain("private-access-value");
  expect(failure.details).toContain("[diagnostic text omitted]");
  expect(new TextEncoder().encode(failure.details).length).toBeLessThanOrEqual(24000);
  expect(new TextEncoder().encode(setupError(new Error("失".repeat(5000))).message).length).toBeLessThanOrEqual(4000);
  expect(setupText("unchanged", 40)).toBe("unchanged");
});

test("a command error uses its message and retains the complete envelope", async () => {
  const { setupCommandError } = await import("./setup-error");
  const failure = setupCommandError("The work request failed.", {
    stdout: JSON.stringify({ error: { code: "environment", message: "The helper is unavailable." } }),
    stderr: '{"level":"info","event":"command.started"}',
  });
  expect(failure.message).toBe("The helper is unavailable.");
  expect(failure.details).toContain('"code":"environment"');
  expect(failure.details).toContain('"level":"info"');
});
