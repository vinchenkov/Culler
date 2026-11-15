// fakedom.ts — the part of the DOM that dom.js and the dialog modules use, for the unit lane.
// The document records each change of a connected node, so a test can name the nodes that a render changed.

export interface FakeEvent { type: string; target: FakeNode; currentTarget: FakeNode; preventDefault(): void }
type Listener = (event: FakeEvent) => unknown;

const attributeName = (key: string) => `data-${key.replace(/[A-Z]/g, (letter) => `-${letter.toLowerCase()}`)}`;

export class FakeNode {
  parentNode: FakeNode | null = null;
  readonly childNodes: FakeNode[] = [];
  readonly dataset: Record<string, string>;
  open = false;
  scrollTop = 0;
  private readonly attrs = new Map<string, string>();
  private readonly listeners = new Map<string, Set<Listener>>();
  private text = "";
  // The entry of a person or a script replaces the default that an attribute gives.
  private entered: { value?: string; checked?: boolean } = {};
  // An option follows its selected attribute until a person or a script selects an option.
  private chosen = false;
  private touched = false;

  constructor(readonly ownerDocument: FakeDocument, readonly nodeName: string, readonly nodeType = 1) {
    this.dataset = new Proxy({}, {
      get: (_, key: string) => this.attrs.get(attributeName(key)),
      set: (_, key: string, value: string) => { this.setAttribute(attributeName(key), value); return true; },
    });
  }

  get isConnected(): boolean {
    let top: FakeNode = this;
    while (top.parentNode) top = top.parentNode;
    return top === this.ownerDocument.body;
  }
  get firstChild() { return this.childNodes[0] ?? null; }
  get nextSibling() { return this.parentNode?.childNodes[this.parentNode.childNodes.indexOf(this) + 1] ?? null; }
  contains(node: FakeNode | null): boolean {
    for (let at = node; at; at = at.parentNode) if (at === this) return true;
    return false;
  }

  get data() { return this.text; }
  set data(value: string) { this.text = value; this.ownerDocument.changed(this); }
  get textContent(): string { return this.nodeType === 3 ? this.text : this.childNodes.map((child) => child.textContent).join(""); }
  set textContent(value: string) {
    for (const child of [...this.childNodes]) child.remove();
    if (value) this.append(value);
  }

  get attributes() { return [...this.attrs].map(([name, value]) => ({ name, value })); }
  getAttribute(name: string) { return this.attrs.get(name) ?? null; }
  hasAttribute(name: string) { return this.attrs.has(name); }
  setAttribute(name: string, value: string) {
    this.attrs.set(name, String(value));
    this.ownerDocument.changed(this);
    if (name === "selected" && !this.touched) this.choose(true);
  }
  removeAttribute(name: string) {
    this.attrs.delete(name);
    this.ownerDocument.changed(this);
    if (name === "selected" && !this.touched) this.chosen = false;
  }
  get className() { return this.attrs.get("class") ?? ""; }
  set className(value: string) { this.setAttribute("class", value); }
  get hidden() { return this.attrs.has("hidden"); }
  set hidden(value: boolean) { if (value) this.setAttribute("hidden", ""); else this.removeAttribute("hidden"); }
  readonly classList = {
    add: (...names: string[]) => { for (const name of names) this.classList.toggle(name, true); },
    toggle: (name: string, force?: boolean) => {
      const names = new Set(this.className.split(" ").filter(Boolean));
      const present = force ?? !names.has(name);
      if (present === names.has(name)) return present;
      if (present) names.add(name); else names.delete(name);
      this.className = [...names].join(" ");
      return present;
    },
  };

  // A select with one visible row has one selected option.
  get options() { return this.childNodes.filter((child) => child.nodeName === "OPTION"); }
  get selected() { return this.chosen; }
  set selected(value: boolean) { this.touched = true; this.choose(value); }
  private choose(value: boolean) {
    this.chosen = value;
    if (value) for (const option of this.parentNode?.options ?? []) if (option !== this) option.chosen = false;
  }
  private settle() {
    if (this.nodeName !== "SELECT") return;
    const chosen = this.options.filter((option) => option.chosen);
    for (const option of chosen.slice(0, -1)) option.chosen = false;
    if (!chosen.length) this.options.find((option) => !option.hasAttribute("disabled"))?.choose(true);
  }
  get value(): string {
    if (this.nodeName === "SELECT") return this.options.find((option) => option.chosen)?.value ?? "";
    if (this.nodeName === "OPTION") return this.attrs.get("value") ?? this.textContent;
    return this.entered.value ?? this.attrs.get("value") ?? "";
  }
  set value(value: string) {
    if (this.nodeName !== "SELECT") { this.entered.value = String(value); return; }
    for (const option of this.options) option.chosen = false;
    const match = this.options.find((option) => option.value === value);
    if (match) match.selected = true;
  }
  get checked() { return this.entered.checked ?? this.attrs.has("checked"); }
  set checked(value: boolean) { this.entered.checked = value; }

  // A node that leaves the document takes the focus with it. A move is a removal and an insertion.
  remove() {
    const parent = this.parentNode;
    if (!parent) return;
    parent.childNodes.splice(parent.childNodes.indexOf(this), 1);
    this.parentNode = null;
    if (this.contains(this.ownerDocument.active)) this.ownerDocument.active = null;
    this.ownerDocument.changed(parent);
    parent.settle();
  }
  insertBefore(node: FakeNode, before: FakeNode | null) {
    node.remove();
    this.childNodes.splice(before ? this.childNodes.indexOf(before) : this.childNodes.length, 0, node);
    node.parentNode = this;
    this.ownerDocument.changed(this);
    this.settle();
    return node;
  }
  append(...children: Array<FakeNode | string>) {
    for (const child of children) this.insertBefore(typeof child === "string" ? this.ownerDocument.createTextNode(child) : child, null);
  }
  replaceChildren(...children: Array<FakeNode | string>) {
    for (const child of [...this.childNodes]) child.remove();
    this.append(...children);
  }

  addEventListener(type: string, listener: Listener) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type)!.add(listener);
  }
  removeEventListener(type: string, listener: Listener) { this.listeners.get(type)?.delete(listener); }
  dispatch(type: string) {
    return Promise.all([...(this.listeners.get(type) ?? [])].map((listener) => listener({ type, target: this, currentTarget: this, preventDefault() {} })));
  }
  focus() {
    const previous = this.ownerDocument.active;
    if (previous === this || this.hasAttribute("disabled") || !this.isConnected) return;
    this.ownerDocument.active = null;
    void previous?.dispatch("blur");
    this.ownerDocument.active = this;
  }
  blur() {
    if (this.ownerDocument.active !== this) return;
    this.ownerDocument.active = null;
    void this.dispatch("blur");
  }
  showModal() { this.open = true; }
  close() { this.open = false; }
  scrollTo() { this.scrollTop = 0; }

  all(): FakeNode[] { return [this, ...this.childNodes.flatMap((child) => child.all())]; }
  private matches(selector: string) {
    if (selector.startsWith(".")) return this.className.split(" ").includes(selector.slice(1));
    const testid = /^\[data-testid="(.*)"\]$/.exec(selector);
    return testid ? this.attrs.get("data-testid") === testid[1] : this.nodeName === selector.toUpperCase();
  }
  querySelectorAll(selector: string) { return this.all().slice(1).filter((node) => node.nodeType === 1 && node.matches(selector)); }
  querySelector(selector: string) { return this.querySelectorAll(selector)[0] ?? null; }
  byTestid(testid: string) { return this.querySelector(`[data-testid="${testid}"]`); }
}

export class FakeDocument {
  readonly body = new FakeNode(this, "BODY");
  active: FakeNode | null = null;
  hidden = false;
  changes: FakeNode[] = [];
  get activeElement() { return this.active ?? this.body; }
  createElement(tag: string) { return new FakeNode(this, tag.toUpperCase()); }
  createElementNS(_namespace: string, tag: string) { return new FakeNode(this, tag); }
  createTextNode(text: string) {
    const node = new FakeNode(this, "#text", 3);
    node.data = text;
    return node;
  }
  querySelector(selector: string) { return this.body.querySelector(selector); }
  changed(node: FakeNode) { if (node.isConnected) this.changes.push(node); }
}

/** Replaces the global document until `restore`. */
export function installFakeDocument() {
  const global = globalThis as { document?: unknown };
  const saved = global.document;
  const document = new FakeDocument();
  global.document = document;
  return { document, restore() { global.document = saved; } };
}
