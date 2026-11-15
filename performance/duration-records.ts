import { join } from "node:path";
import { makeLogger } from "../runner/shared/log";
import { openFileSink } from "../runner/shared/log-sink";
import { observeCommand } from "../dashboard/src/main";
import { makeMcRunner as productRunner } from "../dashboard/src/mc";

export function makeMcRunner(mcBin: string) {
  const home = process.env.MC_HOME;
  if (!home?.startsWith("/private/tmp/culler-performance/")) throw new Error("The observer requires a scratch home.");
  const log = makeLogger({ comp: "dashboard", level: "info",
    sink: openFileSink({ dir: join(home, "logs"), name: "benchmark-duration.ndjson" }) });
  return productRunner(mcBin, { onComplete: (record) => observeCommand(log, record) });
}
