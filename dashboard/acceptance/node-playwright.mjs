import { existsSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";

const core = process.env.MC_SMOKE_PLAYWRIGHT_CORE;
if (!core) throw new Error("MC_SMOKE_PLAYWRIGHT_CORE must name the matching playwright-core directory.");
const node = Bun.which("node");
if (!node) throw new Error("The remote browser driver requires Node.js.");
const require = createRequire(import.meta.url);
const bundle = join(core, "lib/coreBundle.js");
const api = existsSync(bundle) ? require(bundle).oop : require(join(core, "lib/outofprocess.js"));

// The Node.js driver handles the WebSocket transport that stalls under Bun.
const previous = process.execPath;
let driver;
try {
  process.execPath = node;
  driver = await api.start();
} finally {
  process.execPath = previous;
}
export const chromium = driver.playwright.chromium;
process.on("exit", () => driver.playwright.driverProcess.kill());
