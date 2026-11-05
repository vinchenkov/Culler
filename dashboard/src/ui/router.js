export const PAGES = ["board", "homie", "review", "pipeline", "configs", "logs", "vision", "environment"];

/** The page (and its extra path segments) a location hash names, or null. */
export function parseRoute(hash) {
  const raw = String(hash ?? "").replace(/^#\/?/, "");
  if (raw === "" || raw.includes("=")) return null;
  const parts = raw.split("/").filter((p) => p !== "");
  if (parts[0] === "activity") return { page: "pipeline", rest: [] };
  if (parts.length === 0 || !PAGES.includes(parts[0])) return null;
  return { page: parts[0], rest: parts.slice(1) };
}

/** The canonical hash for a page and its extra segments. */
export function routeHash(page, rest = []) {
  if (!PAGES.includes(page)) return "#";
  const tail = (rest ?? []).filter((p) => p !== null && p !== undefined && p !== "");
  return `#${[page, ...tail].join("/")}`;
}

/** The address of the reader for the packet of one task. */
export function packetHash(taskID) {
  return routeHash("review", [String(taskID)]);
}

/** The task whose packet a Review address names, or null for the packet list
 * and for any other address. */
export function packetRoute(hash) {
  const route = parseRoute(hash);
  if (route?.page !== "review" || route.rest.length !== 1 || !/^[1-9]\d*$/.test(route.rest[0])) return null;
  return Number(route.rest[0]);
}
