import { afterEach, expect, test } from "bun:test";
import { showDialog } from "./components.js";

const globals = globalThis as { document?: unknown };
const originalDocument = globals.document;
afterEach(() => { globals.document = originalDocument; });

function modal() {
  return {
    open: false,
    dataset: {} as Record<string, string>,
    markers: [] as string[],
    showModal() { this.markers.push(this.dataset.dialogStacked!); this.open = true; },
  };
}

test("a nested dialog receives its stack marker before its native modal opens", () => {
  const lower = modal();
  const upper = modal();
  let active: unknown = null;
  globals.document = { querySelector: () => active };
  showDialog(lower);
  active = lower;
  showDialog(upper);
  expect(lower.markers).toEqual(["false"]);
  expect(upper.markers).toEqual(["true"]);
  expect(lower.dataset.dialogStacked).toBe("false");
});

test("an open lower dialog retains its original backdrop when another dialog opens", () => {
  const lower = modal();
  globals.document = { querySelector: () => null };
  showDialog(lower);
  globals.document = { querySelector: () => modal() };
  showDialog(lower);
  expect(lower.dataset.dialogStacked).toBe("false");
});

test("a reused dialog receives the current stack marker on each new open", () => {
  const dialog = modal();
  globals.document = { querySelector: () => modal() };
  showDialog(dialog);
  dialog.open = false;
  globals.document = { querySelector: () => null };
  showDialog(dialog);
  expect(dialog.markers).toEqual(["true", "false"]);
});
