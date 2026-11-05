import { mkdir } from "node:fs/promises";
import { createIntegration, loadUpstream } from "./integration.mjs";

const args = process.argv.slice(2);
const option = (name) => {
  const index = args.indexOf(name);
  return index < 0 ? undefined : args[index + 1];
};
const root = option("--repo");
if (!root) throw new Error("The OpenWiki server requires its assigned repository.");
if (process.env.HOME) await mkdir(process.env.HOME, { recursive: true, mode: 0o700 });
const upstream = await loadUpstream();
const provider = await createIntegration({ root, inputCommit: option("--input-commit"), receiptPath: option("--receipt"), upstream });
const server = upstream.createOpenWikiMcpServer(provider);
await server.connect(new upstream.StdioServerTransport());
