// host-zone.ts — the zone of the Mac, carried as TZ into every container the
// resident creates, so an agent and the operator agree on the date.

import { readlinkSync } from "node:fs";

const ZONE = /^[A-Za-z0-9_+-]+(\/[A-Za-z0-9_+-]+)*$/;

/** The zone of the host: TZ when the environment sets one, else the zone
 * that /etc/localtime links to. Undefined when neither names a zone. */
export function hostZone(
  env: Record<string, string | undefined> = process.env,
  readlink: (path: string) => string = readlinkSync,
): string | undefined {
  const fromEnv = (env["TZ"] ?? "").replace(/^:/, "");
  if (fromEnv !== "" && ZONE.test(fromEnv)) return fromEnv;
  let target: string;
  try {
    target = readlink("/etc/localtime");
  } catch {
    return undefined;
  }
  const at = target.lastIndexOf("zoneinfo/");
  if (at === -1) return undefined;
  const zone = target.slice(at + "zoneinfo/".length);
  return ZONE.test(zone) ? zone : undefined;
}
