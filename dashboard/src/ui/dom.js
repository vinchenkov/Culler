// dom.js — the DOM helpers every browser module shares. Nothing here reads
// state; a module that builds nodes imports these so no pane ever assembles
// a string of markup.

/** Builds one element. Spine text — a task title, a log line — reaches the
 * page only as a text node. */
export function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (value === null || value === undefined || value === false) continue;
    if (key === "class") node.className = value;
    else if (key === "text") node.textContent = String(value);
    else if (key === "testid") node.dataset.testid = value;
    else if (key === "data") Object.assign(node.dataset, value);
    else if (key === "hidden") node.hidden = Boolean(value);
    else if (key === "title" && value === "") continue;
    else if (key.startsWith("on")) node.addEventListener(key.slice(2).toLowerCase(), value);
    else node.setAttribute(key, String(value));
  }
  for (const child of children.flat(2)) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child);
  }
  return remember(node, props);
}

const SVG_NS = "http://www.w3.org/2000/svg";

/** Builds one SVG element. Same props as `el`; the namespace is the only
 * difference, and it is the difference a graph cannot do without. */
export function svg(tag, props = {}, ...children) {
  const node = document.createElementNS(SVG_NS, tag);
  for (const [key, value] of Object.entries(props)) {
    if (value === null || value === undefined || value === false) continue;
    if (key === "text") node.textContent = String(value);
    else if (key === "testid") node.dataset.testid = value;
    else if (key === "data") Object.assign(node.dataset, value);
    else if (key.startsWith("on")) node.addEventListener(key.slice(2).toLowerCase(), value);
    else node.setAttribute(key, String(value));
  }
  for (const child of children.flat(2)) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child);
  }
  return remember(node, props);
}

export const tag = (label, kind = "") => el("span", { class: `tag ${kind}`.trim(), text: label });

export function safeMarkdownHref(value) {
  if (typeof value !== "string" || !value || /[\u0000-\u0020\u007f\\]/.test(value)) return null;
  try {
    const url = new URL(value, "https://markdown.invalid/");
    return ["http:", "https:"].includes(url.protocol) ? value : null;
  } catch {
    return null;
  }
}

/** One labelled record, such as a commit. A click on a value with a copy
 * copies the full value, for example the full commit identifier. */
export function factNode(fact) {
  const copy = async (event) => {
    const button = event.currentTarget;
    try {
      await navigator.clipboard.writeText(fact.copy);
      button.textContent = "Copied";
    } catch {
      button.textContent = "Copy failed";
    }
    setTimeout(() => { button.textContent = fact.text; }, 1500);
  };
  return el("li", { class: "fact", data: { key: fact.key } },
    el("span", { class: "fact-label", text: fact.label }),
    fact.detail && el("span", { class: "fact-detail", title: fact.detail, text: fact.detail }),
    fact.text && (fact.copy
      ? el("button", { type: "button", class: "fact-copy", testid: fact.testid, title: fact.title, text: fact.text, onclick: copy })
      : el("span", { class: "fact-value", title: fact.text, text: fact.text })),
  );
}

/** A markdown block tree (markdown.js) as DOM nodes. Every leaf is a text
 * node — agent-authored bytes are never markup. */
export function markdownNodes(blocks) {
  const inline = (spans) =>
    spans.map((span) => {
      if (span.type === "code") return el("code", { text: span.text });
      if (span.type === "strong") return el("strong", { text: span.text });
      if (span.type === "em") return el("em", { text: span.text });
      if (span.type === "link") {
        const href = safeMarkdownHref(span.href);
        return href
          ? el("a", { href, rel: "noreferrer noopener", target: "_blank", text: span.text })
          : document.createTextNode(`[${span.text}](${span.href})`);
      }
      return document.createTextNode(span.text);
    });
  return blocks.map((block) => {
    if (block.type === "heading") return el(`h${Math.min(3, block.level)}`, {}, inline(block.spans));
    if (block.type === "rule") return el("hr", {});
    if (block.type === "code") return el("pre", {}, el("code", { text: block.text }));
    if (block.type === "quote") return el("blockquote", {}, inline(block.spans));
    if (block.type === "list") {
      return el(
        block.ordered ? "ol" : "ul",
        {},
        block.items.map((item) => el("li", {}, inline(item))),
      );
    }
    return el("p", {}, inline(block.spans));
  });
}

const handlers = new WeakMap();
const attributes = new WeakMap();

function remember(node, props) {
  handlers.set(node, Object.fromEntries(Object.entries(props).filter(([key]) => key.startsWith("on"))));
  attributes.set(node, new Map([...node.attributes].map(attribute => [attribute.name, attribute.value])));
  return node;
}

function nodeKey(node) {
  if (node.nodeType !== 1) return "";
  return JSON.stringify(["id", "data-key", "data-url", "data-task", "data-session", "data-packet", "data-disclosure", "data-report", "data-testid"]
    .map(name => node.getAttribute(name) ?? ""));
}

function patchNode(node, next) {
  if (node.nodeType !== next.nodeType || node.nodeName !== next.nodeName || nodeKey(node) !== nodeKey(next)) return next;
  if (node.nodeType === 3) {
    if (node.data !== next.data) node.data = next.data;
    return node;
  }
  if (!attributes.has(node) || !attributes.has(next)) return node.isEqualNode(next) ? node : next;
  const before = attributes.get(node);
  const after = attributes.get(next);
  for (const [name] of before) if (!after.has(name)) node.removeAttribute(name);
  for (const [name, value] of after) if (before.get(name) !== value) node.setAttribute(name, value);
  attributes.set(node, after);
  for (const [name, handler] of Object.entries(handlers.get(node) ?? {})) node.removeEventListener(name.slice(2).toLowerCase(), handler);
  for (const [name, handler] of Object.entries(handlers.get(next) ?? {})) node.addEventListener(name.slice(2).toLowerCase(), handler);
  handlers.set(node, handlers.get(next));
  // A report keeps its loaded frame, its read position, and its own control.
  if (node.hasAttribute("data-report")) return node;
  if (!/^(INPUT|TEXTAREA|SELECT)$/.test(node.nodeName)) reconcileChildren(node, [...next.childNodes]);
  else if (node.nodeName === "SELECT") {
    const value = node.value;
    reconcileChildren(node, [...next.childNodes]);
    if ([...node.options].some(option => option.value === value)) node.value = value;
  }
  return node;
}

// Retain element identities so a refresh preserves focus and text selection.
export function reconcileChildren(host, children) {
  const unused = new Set(host.childNodes);
  const candidates = new Map();
  const identity = node => `${node.nodeType}:${node.nodeName}:${nodeKey(node)}`;
  for (const node of unused) {
    const key = identity(node);
    if (!candidates.has(key)) candidates.set(key, { nodes: [], index: 0 });
    candidates.get(key).nodes.push(node);
  }
  let cursor = host.firstChild;
  for (const value of children) {
    const next = typeof value === "string" ? document.createTextNode(value) : value;
    const group = candidates.get(identity(next));
    const old = group?.nodes[group.index++];
    const node = old ? patchNode(old, next) : next;
    if (old) unused.delete(old);
    if (node !== cursor) host.insertBefore(node, cursor);
    if (old && old !== node) old.remove();
    cursor = node.nextSibling;
  }
  for (const node of unused) node.remove();
}
