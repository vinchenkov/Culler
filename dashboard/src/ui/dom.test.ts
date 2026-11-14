import { afterEach, beforeEach, expect, test } from "bun:test";
import { el, reconcileChildren } from "./dom.js";
import { installFakeDocument, type FakeDocument, type FakeNode } from "./fakedom";

let page: FakeDocument;
let restore: () => void;
beforeEach(() => { ({ document: page, restore } = installFakeDocument()); });
afterEach(() => restore());

const make = (tag: string, props: Record<string, unknown> = {}, ...children: unknown[]) => el(tag, props, ...children) as unknown as FakeNode;
// The host is in the document, and the record of changes starts after its first content.
function host(...children: FakeNode[]) {
  const node = make("div", {}, ...children);
  page.body.append(node);
  page.changes = [];
  return node;
}
const refresh = (node: FakeNode, children: FakeNode[], controlled = false) => (reconcileChildren as (...args: unknown[]) => void)(node, children, controlled);
// The comparison is by identity. An equal copy of a node is another node.
function expectNodes(actual: FakeNode[], expected: FakeNode[]) {
  expect(actual.length).toBe(expected.length);
  actual.forEach((node, index) => expect(node).toBe(expected[index]!));
}

test("a refresh with the same content changes no node", () => {
  const content = () => [
    make("p", { class: "note", text: "The connection is saved." }),
    make("label", {}, make("span", { text: "Model" }), make("select", { testid: "model" },
      make("option", { value: "", text: "Select a model" }), make("option", { value: "first", text: "First", selected: true }))),
  ];
  const node = host(...content());
  const before = [...node.childNodes];
  refresh(node, content(), true);
  expectNodes(page.changes, []);
  expectNodes(node.childNodes, before);
});

test("a node that leaves moves no retained node, and the control after it keeps its focus", () => {
  const input = make("input", { testid: "name" });
  const node = host(make("p", { testid: "notice", text: "Saved" }), input, make("p", { text: "After" }));
  const after = node.childNodes[2]!;
  input.focus();
  refresh(node, [make("input", { testid: "name" }), make("p", { text: "After" })]);
  expectNodes(node.childNodes, [input, after]);
  expect(page.activeElement).toBe(input);
  // The one change is the removal of the notice from the host.
  expectNodes(page.changes, [node]);
});

test("a new node enters before a retained node without a move of that node", () => {
  const input = make("input", { testid: "name" });
  const node = host(input);
  input.focus();
  refresh(node, [make("p", { testid: "notice", text: "Saved" }), make("input", { testid: "name" })]);
  expect(node.childNodes.map((child) => child.nodeName)).toEqual(["P", "INPUT"]);
  expect(node.childNodes[1]).toBe(input);
  expect(page.activeElement).toBe(input);
});

test("a changed order moves a retained node to its new place", () => {
  const first = make("p", { testid: "first", text: "First" });
  const second = make("p", { testid: "second", text: "Second" });
  const node = host(first, second);
  refresh(node, [make("p", { testid: "second", text: "Second" }), make("p", { testid: "first", text: "First" })]);
  expectNodes(node.childNodes, [second, first]);
});

test("a controlled refresh gives each retained input and select the value of the new content", () => {
  const content = (name: string, selected: string, checked: boolean) => [
    make("input", { testid: "name", value: name }),
    make("input", { testid: "choice", type: "radio", checked }),
    make("select", { testid: "model" }, ["first", "second"].map((value) => make("option", { value, text: value, selected: value === selected }))),
  ];
  for (const controlled of [true, false]) {
    const node = host(...content("Suggested", "first", false));
    const [name, choice, model] = node.childNodes as [FakeNode, FakeNode, FakeNode];
    name.value = "Typed";
    choice.checked = true;
    model.value = "second";
    refresh(node, content("Other suggestion", "first", false), controlled);
    expectNodes(node.childNodes, [name, choice, model]);
    expect([name.value, choice.checked, model.value]).toEqual(controlled ? ["Other suggestion", false, "first"] : ["Typed", true, "second"]);
    node.remove();
  }
});

test("a controlled refresh leaves a control alone when the new content holds its value", () => {
  const content = (text: string) => [make("input", { testid: "name", value: text })];
  const node = host(...content(""));
  const name = node.childNodes[0]!;
  name.value = "Typed";
  name.focus();
  refresh(node, content("Typed"), true);
  expect(name.value).toBe("Typed");
  expect(page.activeElement).toBe(name);
  // The one change is the value attribute of the new content.
  expectNodes(page.changes, [name]);
});

test("a new option with a key enters a select and leaves each other option and the selection in place", () => {
  const option = (value: string, selected = false) => make("option", { value, text: value, selected, data: { key: value } });
  const node = host(make("select", { testid: "model" }, option("first"), option("second", true)));
  const model = node.childNodes[0]!;
  const before = [...model.options];
  model.value = "second";
  refresh(node, [make("select", { testid: "model" }, option("live"), option("first"), option("second", true))], true);
  expect(model.options.map((item) => item.value)).toEqual(["live", "first", "second"]);
  expectNodes(model.options.slice(1), before);
  expect(model.value).toBe("second");
  // The one change is the insertion of the new option into the select.
  expectNodes(page.changes, [model]);
});
