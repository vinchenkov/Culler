// paths.js — display-time path translation for the packet render.
//
// It lives in its own module for one reason: app.js is a browser script that
// touches `document` at import time and cannot be unit-tested, and a rewrite
// rule that silently stops matching would show the operator a citation it
// cannot open while looking exactly as correct as one it can.

/** Rewrites every `/mc/artifacts/<target>/…` a render cites into the host path
 * the operator can actually open. Display only — the render itself is the
 * role's own prose and is never modified at rest.
 *
 * A target with no root is left exactly as written: an unmapped path is better
 * read as the container path it truthfully is than as a host path that does
 * not exist. */
export function hostPaths(text, roots) {
  if (roots === null || roots === undefined) {
    return text;
  }
  return text.replace(/\/mc\/artifacts\/([A-Za-z0-9._-]+)(?=\/)/g, (whole, target) =>
    typeof roots[target] === "string" ? roots[target] : whole);
}
