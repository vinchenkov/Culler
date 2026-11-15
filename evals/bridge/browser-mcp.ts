import { createInterface } from "node:readline";

const definitions = JSON.parse(await Bun.file("/eval/browser-tools.json").text());
const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
for await (const line of lines) {
  let request: any;
  try {
    request = JSON.parse(line);
    if (request.id === undefined) continue;
    let result: unknown;
    if (request.method === "initialize") {
      result = { protocolVersion: "2024-11-05", capabilities: { tools: {} }, serverInfo: { name: "packet", version: "1" } };
    } else if (request.method === "tools/list") {
      result = { tools: definitions };
    } else if (request.method === "tools/call") {
      if (!definitions.some((tool: any) => tool.name === request.params?.name)) throw new Error("The packet browser tool is unsupported.");
      const response = await fetch(process.env.CULLER_EVAL_BROWSER_URL!, {
        method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${process.env.CULLER_EVAL_BROWSER_TOKEN}` },
        body: JSON.stringify(request.params),
      });
      if (!response.ok) throw new Error(`The packet browser refused the request (${response.status}).`);
      result = await response.json();
    } else if (request.method === "ping") {
      result = {};
    } else {
      throw new Error("The protocol method is unsupported.");
    }
    console.log(JSON.stringify({ jsonrpc: "2.0", id: request.id, result }));
  } catch (error) {
    console.log(JSON.stringify({ jsonrpc: "2.0", id: request?.id ?? null,
      error: { code: -32603, message: error instanceof Error ? error.message : "The packet browser failed." } }));
  }
}
