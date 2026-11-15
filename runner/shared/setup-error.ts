import { diagnosticRedactor, readAdapterFailure } from "../agent-runner/diagnostics";

export const setupErrorFallback = "The operation failed without an error message. Technical details contain the available output.";
const omission = "\n[diagnostic text omitted]";

export function setupText(value: string, limit: number): string {
  const bytes = new TextEncoder().encode(value);
  if (bytes.length <= limit) return value;
  return new TextDecoder().decode(bytes.slice(0, limit - omission.length - 3)) + omission;
}

export class SetupError extends Error {
  constructor(message: string, readonly details: string) { super(message); }
}

export function setupCommandError(context: string, output: { stdout: string; stderr: string }): SetupError {
  let message = "";
  try {
    const envelope = JSON.parse(output.stdout);
    if (typeof envelope?.error?.message === "string") message = envelope.error.message;
  } catch {}
  if (!message) message = output.stderr.split("\n").filter(line => {
    try { JSON.parse(line); return false; } catch { return true; }
  }).join("\n").trim();
  return new SetupError(message || context, `${context}\n${output.stderr}\n${output.stdout}`);
}

export function setupError(error: unknown, redact = diagnosticRedactor()): SetupError {
  const message = error instanceof Error ? error.message : String(error ?? "");
  const details = error instanceof SetupError ? error.details : error instanceof Error ? error.stack ?? message : message;
  return new SetupError(setupText(redact(message.trim() ? message : setupErrorFallback), 4000), setupText(redact(details), 24000));
}

function dockerError(stderr: string): string | undefined {
  for (const line of stderr.split("\n")) {
    const record = line.trim();
    const logged = record.match(/^time=\S+ level=(?:error|fatal) msg=("(?:[^"\\]|\\.)*")(?:\s|$)/);
    let message = "";
    if (logged) {
      try { message = JSON.parse(logged[1]); } catch { continue; }
    } else if (/^(?:docker: )?(?:Error response from daemon:|Cannot connect to the Docker daemon|error during connect:)/.test(record)) {
      message = record.replace(/^docker: /, "");
    }
    if (!message.trim()) continue;
    if (message.startsWith("error waiting for container: ")) {
      return `Docker could not wait for the setup container: ${message.slice("error waiting for container: ".length)}`;
    }
    if (/^(?:Error response from daemon:|Cannot connect to the Docker daemon|error during connect:)/.test(message)) {
      return `Docker reported an error: ${message}`;
    }
  }
}

export function setupProcessError(output: { stdout: string; stderr: string; exitCode: number }, redact = diagnosticRedactor()): SetupError {
  let failure;
  for (const line of output.stdout.split("\n")) {
    try { failure = readAdapterFailure(JSON.parse(line)) ?? failure; } catch {}
  }
  const details = [
    failure && JSON.stringify(failure, null, 2),
    `Exit code: ${output.exitCode}`,
    output.stderr && `Standard error:\n${setupText(redact(output.stderr), 6000)}`,
    output.stdout && `Standard output:\n${setupText(redact(output.stdout), 6000)}`,
  ].filter(Boolean).join("\n\n");
  const message = failure?.condition || dockerError(output.stderr) || `The setup process exited with code ${output.exitCode}.`;
  return setupError(new SetupError(message, details), redact);
}
