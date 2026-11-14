import { describe, expect, test } from "bun:test";
import { PAGES, packetHash, packetRoute, parseRoute, routeHash } from "./router.js";

describe("router", () => {
  test("saved Activity links open Pipeline", () => {
    expect(parseRoute("#activity")).toEqual({ page: "pipeline", rest: [] });
    expect(parseRoute("#/activity")).toEqual({ page: "pipeline", rest: [] });
    expect(routeHash(parseRoute("#activity").page)).toBe("#pipeline");
  });

  test("names a page and its extra segments", () => {
    expect(parseRoute("#board")).toEqual({ page: "board", rest: [] });
    expect(parseRoute("#/configs")).toEqual({ page: "configs", rest: [] });
    expect(parseRoute("#logs/resident/stderr")).toEqual({
      page: "logs",
      rest: ["resident", "stderr"],
    });
  });

  test("an unknown or token fragment names no page", () => {
    expect(parseRoute("#nope")).toBe(null);
    expect(parseRoute("#wiki")).toBe(null);
    expect(parseRoute("")).toBe(null);
    expect(parseRoute(null)).toBe(null);
    // The auth fragment must never be read as a route.
    expect(parseRoute("#token=abc123")).toBe(null);
  });

  test("hashes round-trip", () => {
    for (const page of PAGES) {
      expect(parseRoute(routeHash(page))?.page).toBe(page);
    }
    expect(routeHash("logs", ["resident", "stdout"])).toBe("#logs/resident/stdout");
    expect(parseRoute(routeHash("logs", ["resident", "stdout"]))).toEqual({
      page: "logs",
      rest: ["resident", "stdout"],
    });
    expect(routeHash("not-a-page")).toBe("#");
  });

  test("the address of a packet names the Review page and the task", () => {
    expect(packetHash(7)).toBe("#review/7");
    expect(packetHash(1234)).toBe("#review/1234");
    expect(parseRoute(packetHash(7))).toEqual({ page: "review", rest: ["7"] });
  });

  test("the address of a packet parses back to its task", () => {
    for (const task of [1, 7, 1234]) expect(packetRoute(packetHash(task))).toBe(task);
    expect(packetRoute("#/review/7")).toBe(7);
  });

  test("any other address names no packet", () => {
    for (const hash of ["#review", "#review/", "#review/0", "#review/-1", "#review/07", "#review/7a",
      "#review/1.5", "#review/7/extra", "#board/7", "#logs/7", "#token=abc", "", null]) {
      expect(packetRoute(hash)).toBe(null);
    }
  });
});
