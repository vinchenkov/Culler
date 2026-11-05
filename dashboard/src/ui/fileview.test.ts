import { describe, expect, test } from "bun:test";
import { changeCounts, contrast, fileViewInput, lineBackgrounds, proposalFiles, readableTheme, setupScriptFile, viewLines } from "./fileview.js";

const pierre = await import("./pierre/pierre.js");

const lines = (count: number, text = "RUN step") => Array.from({ length: count }, (_, at) => `${text} ${at + 1}`).join("\n") + "\n";
const accepted = {
  dockerfile: { path: ".culler/Dockerfile", content: lines(20) },
  build_files: [{ path: "old.sh", content: "rm -rf cache\nexit 0\n" }, { path: "same.sh", content: "echo same\n" }],
};
const proposed = {
  dockerfile: { path: ".culler/Dockerfile", content: lines(20).replace("RUN step 10\n", "RUN step ten\n") },
  build_files: [{ path: "same.sh", content: "echo same\n" }, { path: "new.sh", content: "install tool\nverify tool\nlist tool\n" }],
};

/** The changed lines and the lines of a comparison that the file viewer shows. */
function compare(file: ReturnType<typeof proposalFiles>["buildFiles"][number]) {
  const input = fileViewInput(file);
  if (input.file) return { input, lines: viewLines(input) };
  const comparison = pierre.parseDiffFromFile(input.oldFile, input.newFile);
  return { input, type: comparison.type, ...changeCounts(comparison), lines: viewLines(input, comparison) };
}

describe("file comparison", () => {
  test("a first proposal adds each file", () => {
    const files = proposalFiles(null, proposed);
    expect(files.dockerfile).toEqual({ path: ".culler/Dockerfile", before: null, after: proposed.dockerfile.content, change: "Added", lang: "dockerfile" });
    expect(files.buildFiles.map(file => [file.path, file.change])).toEqual([["same.sh", "Added"], ["new.sh", "Added"]]);
    expect(compare(files.dockerfile!)).toMatchObject({ type: "new", added: 20, removed: 0, lines: 20 });
  });

  test("a later proposal separates changed, added, removed, and unchanged files", () => {
    const files = proposalFiles(accepted, proposed);
    expect(files.dockerfile?.change).toBe("Changed");
    expect(files.buildFiles).toEqual([
      { path: "same.sh", before: "echo same\n", after: "echo same\n", change: "Unchanged" },
      { path: "new.sh", before: null, after: proposed.build_files[1].content, change: "Added" },
      { path: "old.sh", before: accepted.build_files[0].content, after: null, change: "Removed" },
    ]);
  });

  test("a changed file shows its changed lines with unchanged context lines", () => {
    const changed = compare(proposalFiles(accepted, proposed).dockerfile!);
    expect(changed).toMatchObject({ type: "change", added: 1, removed: 1 });
    // Four context lines on each side of the changed pair. The other lines stay collapsed.
    expect(changed.lines).toBe(10);
    const complete = fileViewInput(proposalFiles(accepted, proposed).dockerfile!, true);
    expect(complete).toEqual({ file: { name: "Dockerfile", contents: proposed.dockerfile.content, lang: "dockerfile" } });
    expect(viewLines(complete)).toBe(20);
  });

  test("an added file marks each line as added, and a removed file marks each line as removed", () => {
    const [, added, removed] = proposalFiles(accepted, proposed).buildFiles;
    expect(compare(added!)).toMatchObject({ type: "new", added: 3, removed: 0, lines: 3 });
    expect(compare(removed!)).toMatchObject({ type: "deleted", added: 0, removed: 2, lines: 2 });
  });

  test("an unchanged file shows its contents without a comparison", () => {
    const [unchanged] = proposalFiles(accepted, proposed).buildFiles;
    expect(compare(unchanged!)).toEqual({ input: { file: { name: "same.sh", contents: "echo same\n" } }, lines: 1 });
    expect(fileViewInput({ path: ".culler/tools/build.sh", before: "a\n", after: "a\n", change: "Unchanged" }).file?.name).toBe("build.sh");
    expect(viewLines({ file: { name: "empty", contents: "" } })).toBe(0);
    expect(viewLines({ file: { name: "open", contents: "one\ntwo" } })).toBe(2);
    expect(viewLines({ file: { name: "windows", contents: "one\r\ntwo\r\n" } })).toBe(2);
    expect(viewLines({ file: { name: "blank", contents: "\n" } })).toBe(1);
  });
});

describe("the setup script", () => {
  const setup = (command: string) => ({ setup: { command, directory: "." } });

  test("compares the setup script of the proposal with the accepted script", () => {
    expect(setupScriptFile(null, setup("npm ci\n"))).toEqual({ path: "Setup script", label: "Bash", before: null, after: "npm ci\n", change: "Added", lang: "shellscript" });
    expect(setupScriptFile(setup("npm ci\n"), setup("npm ci --offline\n"))?.change).toBe("Changed");
    expect(setupScriptFile(setup("npm ci\n"), setup(""))?.change).toBe("Removed");
    expect(setupScriptFile(setup("npm ci\n"), setup("npm ci\n"))?.change).toBe("Unchanged");
  });

  test("each version ends with one line break, so the view has no note about a final line break", () => {
    expect(setupScriptFile(null, setup("npm ci"))?.after).toBe("npm ci\n");
    expect(setupScriptFile(null, setup("npm ci\r\n"))?.after).toBe("npm ci\r\n");
    expect(setupScriptFile(setup("npm ci"), setup("npm ci\n"))?.change).toBe("Unchanged");
  });

  test("a proposal without a setup script in either version has no setup script view", () => {
    expect(setupScriptFile(null, setup(""))).toBeNull();
    expect(setupScriptFile(setup(""), {})).toBeNull();
  });
});

describe("readable themes", () => {
  for (const name of ["pierre-light", "pierre-dark"]) {
    test(`each token color of ${name} keeps 4.5:1 on each line background`, async () => {
      const theme = await pierre.resolveTheme(name);
      const readable = readableTheme(theme, `${name}-readable`);
      const rgb = (color: string) => [1, 3, 5].map(at => parseInt(color.slice(at, at + 2), 16));
      const grounds = lineBackgrounds(theme);
      const colors = [readable.fg, ...readable.settings.map((rule: { settings?: { foreground?: string } }) => rule.settings?.foreground)]
        .filter((color): color is string => typeof color === "string");
      expect(colors.length).toBeGreaterThan(200);
      for (const color of colors) for (const ground of grounds) expect(contrast(rgb(color), ground)).toBeGreaterThanOrEqual(4.5);
      // The line colors and the code background stay the same as in the Files changed view.
      expect(readable.colors).toEqual(theme.colors);
      expect(readable.settings[0].settings.background).toBe(theme.settings[0].settings.background);
      expect(readable.name).toBe(`${name}-readable`);
      expect(theme.settings.some((rule: { settings?: { foreground?: string } }, at: number) =>
        rule.settings?.foreground !== readable.settings[at].settings?.foreground)).toBe(true);
    });
  }
});
