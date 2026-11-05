// Builds the local Pierre Diffs and Pierre Trees modules into
// dashboard/src/ui/pierre/, with the license notice of each bundled package
// and a provenance record of each file. Run after a change of the pinned
// packages:
//
//   cd dashboard/pierre && mise exec -- bun install --frozen-lockfile && mise exec -- bun build.ts
//
// The build keeps a subset of the Shiki grammars and two Pierre themes. It
// replaces internal modules of the pinned Shiki and @pierre/theming versions,
// so a version change requires a check of each replacement below.
import { bundledLanguagesInfo } from "shiki";
import { grammars as grammarRecords } from "tm-grammars";
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, relative, sep } from "node:path";

const root = import.meta.dir;
const outdir = join(root, "..", "src", "ui", "pierre");

const LANGUAGES = new Set([
  "css", "diff", "docker", "dotenv", "go", "html", "ini", "javascript", "json", "jsonc", "jsx",
  "make", "markdown", "python", "rust", "shellscript", "sql", "toml", "tsx", "typescript", "xml", "yaml",
]);
const THEMES = ["pierre-light", "pierre-dark"];

function languageModule(): string {
  const kept = bundledLanguagesInfo.filter((language) => LANGUAGES.has(language.id));
  const missing = [...LANGUAGES].filter((id) => !kept.some((language) => language.id === id));
  if (missing.length > 0) throw new Error(`Shiki has no language: ${missing.join(", ")}`);
  const entries = kept.map((language) => `{id:${JSON.stringify(language.id)},name:${JSON.stringify(language.name)},` +
    `aliases:${JSON.stringify(language.aliases ?? [])},import:()=>import(${JSON.stringify(`@shikijs/langs/${language.id}`)})}`);
  return `const bundledLanguagesInfo=[${entries.join(",")}];
const bundledLanguagesBase=Object.fromEntries(bundledLanguagesInfo.map(i=>[i.id,i.import]));
const bundledLanguagesAlias=Object.fromEntries(bundledLanguagesInfo.flatMap(i=>i.aliases.map(a=>[a,i.import])));
const bundledLanguages={...bundledLanguagesBase,...bundledLanguagesAlias};
export { bundledLanguagesInfo as i, bundledLanguagesAlias as n, bundledLanguagesBase as r, bundledLanguages as t };`;
}

const subset: Bun.BunPlugin = {
  name: "culler-pierre-subset",
  setup(build) {
    build.onLoad({ filter: /[\\/]shiki[\\/]dist[\\/]langs-bundle-full-[^\\/]+\.mjs$/ }, () => ({
      contents: languageModule(), loader: "js",
    }));
    build.onLoad({ filter: /[\\/]shiki[\\/]dist[\\/]themes\.mjs$/ }, () => ({
      contents: "export const bundledThemesInfo=[];export const bundledThemes={};", loader: "js",
    }));
    build.onLoad({ filter: /[\\/]@pierre[\\/]theming[\\/]dist[\\/]collections[\\/]shiki\.js$/ }, () => ({
      contents: `import { createThemeCollection } from "../modules/createThemeCollection.js";
export const shikiThemes = createThemeCollection({ themes: [] });`, loader: "js",
    }));
    build.onLoad({ filter: /[\\/]@pierre[\\/]theming[\\/]dist[\\/]collections[\\/]pierre\.js$/ }, async (args) => {
      const source = await Bun.file(args.path).text();
      const imports = /const PIERRE_THEME_IMPORTS = \{[\s\S]*?\n\};/;
      const list = "const PIERRE_THEMES = [...LIGHT_PIERRE_THEMES, ...DARK_PIERRE_THEMES];";
      if (!imports.test(source) || !source.includes(list)) throw new Error("The @pierre/theming theme collection changed.");
      return {
        contents: source
          .replace(imports, `const PIERRE_THEME_IMPORTS = {${THEMES.map((name) =>
            `${JSON.stringify(name)}: () => import(${JSON.stringify(`@pierre/theme/${name}`)})`).join(",")}};`)
          .replace(list, `const PIERRE_THEMES = ${JSON.stringify(THEMES)};`),
        loader: "js",
      };
    });
    build.onResolve({ filter: /^shiki\/wasm$/ }, () => ({ path: "shiki-wasm", namespace: "culler-absent" }));
    build.onLoad({ filter: /.*/, namespace: "culler-absent" }, () => ({
      contents: "export default () => { throw new Error(\"The local build holds no WebAssembly engine.\"); };", loader: "js",
    }));
  },
};

rmSync(outdir, { recursive: true, force: true });
mkdirSync(outdir, { recursive: true });
const result = await Bun.build({
  entrypoints: [join(root, "entry.js")],
  outdir,
  target: "browser",
  format: "esm",
  splitting: true,
  minify: true,
  naming: { entry: "pierre.[ext]", chunk: "[name]-[hash].[ext]" },
  plugins: [subset],
  metafile: true,
});
if (!result.success) {
  console.error(result.logs);
  process.exit(1);
}

// Each package that contributes bytes ships its license notice.
const packages = new Map<string, string>();
const contributing = Object.values(result.metafile!.outputs).flatMap((output) =>
  Object.entries(output.inputs).filter(([, input]) => input.bytesInOutput > 0).map(([path]) => path));
for (const input of new Set(contributing)) {
  const parts = input.split(/[\\/]/);
  const at = parts.lastIndexOf("node_modules");
  if (at < 0) continue;
  const name = parts[at + 1]!.startsWith("@") ? `${parts[at + 1]}/${parts[at + 2]}` : parts[at + 1]!;
  packages.set(parts.slice(0, at + 1).join(sep) + sep + name, name);
}
const notices = /^(licen[sc]e|notice)(\.md|\.txt)?$/i;
const records = [...packages].map(([directory, name]) => {
  const manifest = JSON.parse(readFileSync(join(directory, "package.json"), "utf8"));
  const texts = readdirSync(directory).filter((file) => notices.test(file)).sort()
    .map((file) => readFileSync(join(directory, file), "utf8").trim());
  if (texts.length === 0) throw new Error(`${name} has no license notice.`);
  const licenseFile = `LICENSE-${name.replace("@", "").replace("/", "-")}-${manifest.version}.txt`;
  writeFileSync(join(outdir, licenseFile), `${name} ${manifest.version}\n\n${texts.join("\n\n")}\n`);
  return { name, version: manifest.version, license: manifest.license, license_file: licenseFile };
}).sort((a, b) => a.name.localeCompare(b.name) || a.version.localeCompare(b.version));

// Each bundled grammar ships the notice that tm-grammars records for it.
const grammars = new Set(contributing.map((path) => path.match(/[\\/]@shikijs[\\/]langs[\\/]dist[\\/]([^\\/]+)\.mjs$/)?.[1])
  .filter((id): id is string => id !== undefined));
const grammarPackage = join(root, "node_modules", "tm-grammars");
const grammarVersion = JSON.parse(readFileSync(join(grammarPackage, "package.json"), "utf8")).version;
const sections = readFileSync(join(grammarPackage, "NOTICE"), "utf8").split(/^=+\n/m).slice(1).filter((section) => {
  const names = section.match(/^Files:\s+(.+)$/m)?.[1]?.split(",").map((name) => name.trim().replace(/\.json$/, "")) ?? [];
  return names.some((name) => grammars.has(name));
});
const covered = new Set(sections.flatMap((section) =>
  section.match(/^Files:\s+(.+)$/m)![1]!.split(",").map((name) => name.trim().replace(/\.json$/, ""))));
// tm-grammars records no NOTICE section for a grammar from a TextMate bundle.
// The README of each such bundle states this license.
const TEXTMATE_BUNDLE_LICENSE = `Permission to copy, use, modify, sell and distribute this
software is granted. This software is provided "as is" without
express or implied warranty, and with no claim as to its
suitability for any purpose.`;
for (const id of [...grammars].sort().filter((name) => !covered.has(name))) {
  const source = grammarRecords.find((grammar) => grammar.name === id)?.source ?? "";
  if (!source.startsWith("https://github.com/textmate/")) throw new Error(`tm-grammars records no notice for: ${id}`);
  sections.push(`Files:   ${id}.json\nSource:  ${source}\nLicense: the license of the README of the TextMate bundle\n${"-".repeat(80)}\n${TEXTMATE_BUNDLE_LICENSE}\n`);
}
writeFileSync(join(outdir, "LICENSE-grammars.txt"),
  `The notices of the bundled grammars, from the NOTICE file of tm-grammars ${grammarVersion}.\n\n${sections.join("=".repeat(80) + "\n")}`);
records.push({ name: "tm-grammars", version: grammarVersion, license: "see LICENSE-grammars.txt", license_file: "LICENSE-grammars.txt",
  grammars: [...grammars].sort() } as (typeof records)[number]);

const files = result.outputs.map((output) => {
  const file = relative(outdir, output.path);
  if (file.includes(sep)) throw new Error(`The build wrote a nested file: ${file}`);
  const sha256 = new Bun.CryptoHasher("sha256").update(readFileSync(output.path)).digest("hex");
  return { file, sha256 };
}).sort((a, b) => a.file.localeCompare(b.file));
if (!existsSync(join(outdir, "pierre.js"))) throw new Error("The build wrote no pierre.js entry.");
await Bun.write(join(outdir, "provenance.json"), JSON.stringify({ entry: "pierre.js", packages: records, files }, null, 2) + "\n");
// The host release manifest ships each file of the build.
const manifestPath = join(root, "..", "..", "mc", "deployment", "host_release.go");
const manifest = readFileSync(manifestPath, "utf8");
const block = /(\t\/\/ writes each line from here to the end marker\.\n)[\s\S]*?(\t\/\/ The end of the local Pierre modules\.\n)/;
if (!block.test(manifest)) throw new Error("The host release manifest has no block for the local Pierre modules.");
const shipped = ["provenance.json", ...files.map((f) => f.file), ...records.map((r) => r.license_file)]
  .sort().map((file) => `\t"dashboard/src/ui/pierre/${file}",\n`).join("");
writeFileSync(manifestPath, manifest.replace(block, (_, start, end) => `${start}${shipped}${end}`));
console.log(`${files.length} modules and ${records.length} license notices in ${relative(process.cwd(), outdir)}`);
