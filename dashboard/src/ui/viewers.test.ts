import { describe, expect, test } from "bun:test";

import { readFileSync } from "node:fs";
import { join } from "node:path";

import {
  TRACE_LIST_PATH, applyViewerLink,  projectWikiLinkView, traceLink, traceLinkView, tracesLink,
  viewerLinkAttributes, viewerOrigin, wikiLink,
} from "./viewers.js";

const commit = "0123456789abcdef0123456789abcdef01234567";
const key = "4b".repeat(32);
const available = {
  agentsview: { url: "http://127.0.0.1:7334", state: "available", reason: "" },
  wiki: { url: "http://127.0.0.1:7335", projects: { alpha: { commit, url: `http://127.0.0.1:7335/${key}/alpha/` } } },
};
const stopped = {
  agentsview: { url: "http://127.0.0.1:7334", state: "unavailable", reason: "Run ./redeploy.sh to restart it." },
  wiki: { url: "http://127.0.0.1:7335", projects: {} },
};
const projects = [{ id: "alpha", title: "Alpha" }, { id: "beta", title: "Beta" }];

describe("viewerOrigin", () => {
  test("accepts only a loopback http address", () => {
    expect(viewerOrigin("http://127.0.0.1:7334")).toBe("http://127.0.0.1:7334");
    expect(viewerOrigin("http://localhost:7334/")).toBe("http://localhost:7334");
    expect(viewerOrigin("http://[::1]:7334")).toBe("http://[::1]:7334");
    expect(viewerOrigin("https://127.0.0.1:7334")).toBeNull();
    expect(viewerOrigin("http://example.com:7334")).toBeNull();
    expect(viewerOrigin("javascript:alert(1)")).toBeNull();
    expect(viewerOrigin(undefined)).toBeNull();
  });
});

describe("the Traces link", () => {
  test("opens the complete AgentsView session list", () => {
    expect(TRACE_LIST_PATH).toBe("/sessions?include_one_shot=true&include_automated=true");
    expect(tracesLink(available)).toEqual({
      href: "/agentsview/open?path=%2Fsessions%3Finclude_one_shot%3Dtrue%26include_automated%3Dtrue",
      title: "All traces in AgentsView. The trace list opens in a new tab.",
    });
  });

  test("states why AgentsView is unavailable", () => {
    expect(tracesLink(stopped)).toEqual({ href: null, title: "AgentsView is unavailable. Run ./redeploy.sh to restart it." });
    expect(tracesLink({ agentsview: { url: "http://127.0.0.1:7334", state: "unavailable" } }))
      .toEqual({ href: null, title: "AgentsView is unavailable." });
    expect(tracesLink({ agentsview: { url: "http://example.com", state: "available" } }).href).toBeNull();
    expect(tracesLink(null, true)).toEqual({ href: null, title: "Loading AgentsView status…" });
    expect(tracesLink(null, false)).toEqual({ href: null, title: "The AgentsView status is unavailable." });
  });
});

describe("the trace link of a run", () => {
  const claude = { session: "5d6c0f5e-0000-4000-8000-000000000001", path: "/sessions/5d6c0f5e-0000-4000-8000-000000000001?msg=last" };
  const codex = { session: "codex:019a", path: "/sessions/codex/019a?msg=last" };

  test("opens the AgentsView session of the run at its last message", () => {
    expect(traceLink(claude, available)).toEqual({
      href: "/agentsview/open?path=%2Fsessions%2F5d6c0f5e-0000-4000-8000-000000000001%3Fmsg%3Dlast",
      title: "The trace opens at its last message in AgentsView.",
    });
    expect(traceLink(codex, available)?.href).toBe("/agentsview/open?path=%2Fsessions%2Fcodex%2F019a%3Fmsg%3Dlast");
  });

  test("a run without a main trace has no link", () => {
    expect(traceLink(null, available)).toBeNull();
    expect(traceLink(undefined, available)).toBeNull();
    expect(traceLink({ session: "x", path: "https://example.com/sessions/x" }, available)).toBeNull();
    expect(traceLink({ session: "x", path: "//example.com/sessions/x" }, available)).toBeNull();
  });

  test("an unavailable AgentsView gives the unavailable state with the reason", () => {
    expect(traceLink(claude, stopped)).toEqual({ href: null, title: "AgentsView is unavailable. Run ./redeploy.sh to restart it." });
    expect(traceLink(claude, null, true)).toEqual({ href: null, title: "Loading AgentsView status…" });
  });
});

describe("the wiki link of a Project", () => {
  test("opens the export of the Project and names the Project and the commit", () => {
    expect(wikiLink(available, "alpha", projects)).toEqual({
      href: `http://127.0.0.1:7335/${key}/alpha/`,
      title: "Wiki of Alpha at commit 0123456",
    });
  });

  test("an export without an address states that the wiki address is not ready", () => {
    const keyless = { wiki: { url: "http://127.0.0.1:7335", projects: { alpha: { commit, url: "" } } } };
    expect(wikiLink(keyless, "alpha", projects)).toEqual({
      href: null,
      title: "The wiki address of this deployment is not ready. To repair it, run install.sh again.",
    });
  });

  test("states each unavailable reason", () => {
    expect(wikiLink(available, "beta", projects)).toEqual({
      href: null,
      title: "Beta has no wiki export. Culler exports the wiki when the accepted commit holds an openwiki/ folder.",
    });
    expect(wikiLink(available, null, [])).toEqual({ href: null, title: "No Project exists. Add a Project to open its wiki." });
    expect(wikiLink(available, "gamma", projects)).toEqual({ href: null, title: "Select a Project to open its wiki." });
    expect(wikiLink(available, "alpha", null)).toEqual({ href: null, title: "Loading Project list…" });
    expect(wikiLink(available, "alpha", null, { registryError: "down" })).toEqual({ href: null, title: "The Project list is unavailable." });
    expect(wikiLink(null, "alpha", projects, { pending: true })).toEqual({ href: null, title: "Loading wiki status…" });
    expect(wikiLink(null, "alpha", projects)).toEqual({ href: null, title: "The wiki status is unavailable." });
  });

  test("refuses an export address outside the loopback interface", () => {
    const foreign = { wiki: { projects: { alpha: { commit, url: "https://example.com/alpha/" } } } };
    expect(wikiLink(foreign, "alpha", projects).href).toBeNull();
  });
});

describe("the link attributes", () => {
  const present = (attributes: Record<string, string | null>) =>
    Object.fromEntries(Object.entries(attributes).filter(([, value]) => value !== null));

  /** The attributes of one anchor of index.html. */
  function headerAnchor(id: string): Record<string, string> {
    const html = readFileSync(join(import.meta.dir, "index.html"), "utf8");
    const tag = html.match(new RegExp(`<a\\s[^>]*\\bid="${id}"[^>]*>`))![0];
    return Object.fromEntries([...tag.matchAll(/([a-z-]+)="([^"]*)"/g)].map((match) => [match[1]!, match[2]!]));
  }

  test("an available link opens a new tab without an opener and names the new tab", () => {
    expect(viewerLinkAttributes("Traces", tracesLink(available))).toEqual({
      target: "_blank", rel: "noopener noreferrer", "aria-label": "Traces, opens a new tab",
      href: "/agentsview/open?path=%2Fsessions%3Finclude_one_shot%3Dtrue%26include_automated%3Dtrue",
      "aria-disabled": null, tabindex: null, title: tracesLink(available).title,
    });
  });

  test("an unavailable link has no address, stays in the focus order, and states the reason", () => {
    expect(viewerLinkAttributes("Traces", tracesLink(stopped))).toEqual({
      target: "_blank", rel: "noopener noreferrer", "aria-label": "Traces, opens a new tab",
      href: null, "aria-disabled": "true", tabindex: "0", title: "AgentsView is unavailable. Run ./redeploy.sh to restart it.",
    });
  });

  test("the header shows only the Traces viewer link before the status response", () => {
    const html = readFileSync(join(import.meta.dir, "index.html"), "utf8");
    expect([...html.matchAll(/<a\s[^>]*\bclass="viewer-link"[^>]*\bid="([^"]*)"/g)].map((match) => match[1]))
      .toEqual(["header-traces"]);
    expect(headerAnchor("header-traces")).toEqual({
      class: "viewer-link", id: "header-traces", "data-testid": "header-traces",
      ...present(viewerLinkAttributes("Traces", tracesLink(null, true))),
    });
  });

  test("a header link follows each change of its state", () => {
    const attributes = new Map<string, string>();
    const node = {
      title: "",
      setAttribute: (name: string, value: string) => { attributes.set(name, value); },
      removeAttribute: (name: string) => { attributes.delete(name); },
    };
    applyViewerLink(node, tracesLink(stopped));
    expect(Object.fromEntries(attributes)).toEqual({ "aria-disabled": "true", tabindex: "0" });
    expect(node.title).toBe("AgentsView is unavailable. Run ./redeploy.sh to restart it.");
    applyViewerLink(node, wikiLink(available, "alpha", projects));
    expect(Object.fromEntries(attributes)).toEqual({ href: `http://127.0.0.1:7335/${key}/alpha/` });
    expect(node.title).toBe("Wiki of Alpha at commit 0123456");
    applyViewerLink(node, wikiLink(available, "beta", projects));
    expect(Object.fromEntries(attributes)).toEqual({ "aria-disabled": "true", tabindex: "0" });
  });

  test("the Project menu wiki link names its Project", () => {
    const view = projectWikiLinkView("alpha", "Alpha", wikiLink(available, "alpha", projects));
    expect(view).toMatchObject({ text: "Wiki", testid: "project-wiki-alpha", className: "ws-wiki", data: { project: "alpha" } });
    expect(present(viewerLinkAttributes(view.label, view.link))).toEqual({
      target: "_blank", rel: "noopener noreferrer", "aria-label": "Wiki of Alpha, opens a new tab",
      href: `http://127.0.0.1:7335/${key}/alpha/`, title: "Wiki of Alpha at commit 0123456",
    });
    const missing = projectWikiLinkView("beta", "Beta", wikiLink(available, "beta", projects));
    expect(present(viewerLinkAttributes(missing.label, missing.link))).toMatchObject({ "aria-disabled": "true", tabindex: "0" });
  });

  test("a run has a trace link only with a main trace", () => {
    const run = { id: "run-1", trace: { path: "/sessions/abc" } };
    const view = traceLinkView(run, available, false, "run-trace-link")!;
    expect(view).toMatchObject({ text: "Trace", testid: "run-trace-link", className: "trace-link" });
    expect(present(viewerLinkAttributes(view.label, view.link))).toEqual({
      target: "_blank", rel: "noopener noreferrer", "aria-label": "Trace of run run-1, opens a new tab",
      href: "/agentsview/open?path=%2Fsessions%2Fabc", title: "The trace opens at its last message in AgentsView.",
    });
    const closed = traceLinkView(run, stopped, false, "task-run-trace-run-1")!;
    expect(present(viewerLinkAttributes(closed.label, closed.link))).toMatchObject({
      "aria-disabled": "true", tabindex: "0", title: "AgentsView is unavailable. Run ./redeploy.sh to restart it.",
    });
    expect(traceLinkView({ id: "run-2", trace: null }, available, false, "run-trace-link")).toBeNull();
    expect(traceLinkView({ id: "run-3" }, available, false, "run-trace-link")).toBeNull();
  });
});
