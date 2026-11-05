import { describe, expect, test } from "bun:test";
import { hostZone } from "./host-zone";

const linkTo = (target: string) => (path: string) => {
  if (path !== "/etc/localtime") throw new Error(`unexpected readlink ${path}`);
  return target;
};
const noLink = () => {
  throw Object.assign(new Error("EINVAL"), { code: "EINVAL" });
};

describe("hostZone", () => {
  test("TZ wins when the environment sets one", () => {
    expect(hostZone({ TZ: "Asia/Tokyo" }, noLink)).toBe("Asia/Tokyo");
    expect(hostZone({ TZ: ":Europe/Paris" }, noLink)).toBe("Europe/Paris");
  });

  test("the /etc/localtime link names the zone on a Mac", () => {
    expect(hostZone({}, linkTo("/var/db/timezone/zoneinfo/America/Los_Angeles"))).toBe("America/Los_Angeles");
    expect(hostZone({ TZ: "" }, linkTo("/usr/share/zoneinfo/Etc/UTC"))).toBe("Etc/UTC");
  });

  test("a host that names no zone yields undefined", () => {
    expect(hostZone({}, noLink)).toBeUndefined();
    expect(hostZone({}, linkTo("/etc/localtime.bak"))).toBeUndefined();
    expect(hostZone({ TZ: "../etc" }, linkTo("/zoneinfo/../x"))).toBeUndefined();
  });
});
