// viewers.js — the links to AgentsView and to the OpenWiki viewer. Each link
// opens a separate browser tab. The status response supplies each address and
// state, and each run row supplies its AgentsView session path; the page
// computes no session.

import { el } from "./dom.js";
import { projectName } from "./format.js";

export const TRACE_LIST_PATH = "/sessions?include_one_shot=true&include_automated=true";

export const TRACES_TITLE =
  "All traces in AgentsView. The trace list opens in a new tab.";

export function agentsViewLink(path) {
  return `/agentsview/open?path=${encodeURIComponent(path)}`;
}

const LOOPBACK_HOSTS = new Set(["127.0.0.1", "localhost", "[::1]"]);

/** The origin of a loopback viewer address, or null for any other address. */
export function viewerOrigin(url) {
  try {
    const parsed = new URL(url);
    return parsed.protocol === "http:" && LOOPBACK_HOSTS.has(parsed.hostname) ? parsed.origin : null;
  } catch {
    return null;
  }
}

/** The link to the AgentsView session list, or no address with the reason. */
export function tracesLink(viewers, pending = false) {
  const agentsview = viewers?.agentsview;
  if (!agentsview) {
    return { href: null, title: pending ? "Loading AgentsView status…" : "The AgentsView status is unavailable." };
  }
  const origin = viewerOrigin(agentsview.url);
  if (agentsview.state === "available" && origin !== null) {
    return { href: agentsViewLink(TRACE_LIST_PATH), title: TRACES_TITLE };
  }
  const reason = typeof agentsview.reason === "string" ? agentsview.reason.trim() : "";
  return { href: null, title: reason ? `AgentsView is unavailable. ${reason}` : "AgentsView is unavailable." };
}

/** The trace link of one run: null when the run has no main trace, else the
 * AgentsView session at its last message or the unavailable state. */
export function traceLink(session, viewers, pending = false) {
  const path = typeof session?.path === "string" ? session.path : "";
  if (!path.startsWith("/sessions/")) return null;
  const list = tracesLink(viewers, pending);
  if (list.href === null) return list;
  return { href: agentsViewLink(path), title: "The trace opens at its last message in AgentsView." };
}

/** The wiki link of one Project, or no address with the reason. `projects`
 * is the Project registry, or null while the registry is unknown. */
export function wikiLink(viewers, projectID, projects, { pending = false, registryError = "" } = {}) {
  if (!Array.isArray(projects)) {
    return { href: null, title: registryError ? "The Project list is unavailable." : "Loading Project list…" };
  }
  if (projects.length === 0) return { href: null, title: "No Project exists. Add a Project to open its wiki." };
  const row = projects.find((project) => project.id === projectID);
  if (!row) return { href: null, title: "Select a Project to open its wiki." };
  const title = projectName(row.id, projects);
  if (!viewers?.wiki) {
    return { href: null, title: pending ? "Loading wiki status…" : "The wiki status is unavailable." };
  }
  const wikiExport = viewers.wiki.projects?.[row.id];
  const commit = typeof wikiExport?.commit === "string" ? wikiExport.commit : "";
  if (commit !== "" && wikiExport.url === "") {
    return { href: null, title: "The wiki address of this deployment is not ready. To repair it, run install.sh again." };
  }
  if (!wikiExport || viewerOrigin(wikiExport.url) === null || commit === "") {
    return { href: null, title: `${title} has no wiki export. Culler exports the wiki when the accepted commit holds an openwiki/ folder.` };
  }
  return { href: new URL(wikiExport.url).href, title: `Wiki of ${title} at commit ${commit.slice(0, 7)}` };
}

/** The attributes of one link state. An unavailable link has no address,
 * keeps its place in the focus order, and its tooltip states the reason. */
export function viewerLinkState(link) {
  const available = Boolean(link.href);
  return {
    href: available ? link.href : null,
    "aria-disabled": available ? null : "true",
    tabindex: available ? null : "0",
    title: link.title,
  };
}

/** The attributes of one external viewer link. `label` names the link for
 * assistive technology; the name also states that the link opens a new tab. */
export function viewerLinkAttributes(label, link) {
  return { target: "_blank", rel: "noopener noreferrer", "aria-label": `${label}, opens a new tab`, ...viewerLinkState(link) };
}

/** Applies one link state to an anchor. */
export function applyViewerLink(node, link) {
  const { title, ...state } = viewerLinkState(link);
  for (const [name, value] of Object.entries(state)) {
    if (value === null) node.removeAttribute(name);
    else node.setAttribute(name, value);
  }
  node.title = title;
}

/** One external viewer link. */
export function viewerLinkNode({ text, label, testid, link, className, data = null }) {
  return el("a", { class: className, testid, data, ...viewerLinkAttributes(label, link) },
    `${text} `, el("span", { class: "external-mark", "aria-hidden": "true", text: "↗" }));
}

/** The link of one run row, or null when the run has no main trace. */
export function traceLinkView(run, viewers, pending, testid) {
  const link = traceLink(run?.trace, viewers, pending);
  if (link === null) return null;
  return { text: "Trace", label: `Trace of run ${run.id}`, testid, link, className: "trace-link" };
}

/** The trace link of one run row, or null when the run has no main trace. */
export function traceLinkNode(run, viewers, pending, testid) {
  const view = traceLinkView(run, viewers, pending, testid);
  return view === null ? null : viewerLinkNode(view);
}

/** The wiki link of one Project row in the Project menu. */
export function projectWikiLinkView(id, name, link) {
  return { text: "Wiki", label: `Wiki of ${name}`, testid: `project-wiki-${id}`, link, className: "ws-wiki", data: { project: id } };
}
