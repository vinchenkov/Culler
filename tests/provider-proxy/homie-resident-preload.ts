import { appendFile, readFile, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { syntheticFinalResponse } from "./native-harness";

const root = process.env.CULLER_HOMIE_PROXY_FIXTURE;
if (!root) throw new Error("The controlled resident requires its private fixture directory.");
const config = JSON.parse(await readFile(join(root, "config.json"), "utf8")) as {
  harness: "codex" | "claude-sdk"; key: string; model: string; text: string;
};
if (!config.key.startsWith("synthetic-homie-key-") || config.model !== "fixture-model") {
  throw new Error("The controlled resident requires synthetic credentials and the fixture model.");
}

// Only this test process replaces the transport before the production resident starts.
globalThis.fetch = (async (input, options) => {
  const headers = new Headers(options?.headers);
  const authorized = config.harness === "codex" ? headers.get("authorization") === `Bearer ${config.key}`
    : headers.get("x-api-key") === config.key;
  if (!authorized || options?.method !== "POST") throw new Error("The controlled resident refused an external request.");
  const body = JSON.parse(Buffer.from(options.body as Uint8Array).toString());
  if (body.model !== config.model) throw new Error("The native harness requested a different model.");
  await appendFile(join(root, "requests.jsonl"), JSON.stringify({ harness: config.harness, model: body.model }) + "\n", { mode: 0o600 });
  const response = syntheticFinalResponse(config.harness, body, config.text, "commentary");
  const text = await response.text();
  const terminal = config.harness === "codex" ? "event: response.completed" : "event: message_stop";
  const at = text.lastIndexOf(terminal);
  if (at < 0) throw new Error("The controlled response has no terminal event.");
  let canceled = false;
  return new Response(new ReadableStream<Uint8Array>({
    async start(controller) {
      controller.enqueue(new TextEncoder().encode(text.slice(0, at)));
      await writeFile(join(root, "stream-open"), "open", { mode: 0o600 });
      const until = Date.now() + 120_000;
      while (!canceled && !options.signal?.aborted && Date.now() < until) {
        try {
          await stat(join(root, "release"));
          controller.enqueue(new TextEncoder().encode(text.slice(at)));
          controller.close();
          await writeFile(join(root, "stream-complete"), "complete", { mode: 0o600 });
          return;
        } catch (error) {
          if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
        }
        await new Promise(resolve => setTimeout(resolve, 20));
      }
      if (!canceled) controller.error(new Error("The controlled response ended before release."));
    },
    cancel() { canceled = true; },
  }), { headers: response.headers });
}) as typeof fetch;
