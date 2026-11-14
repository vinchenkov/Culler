// skills.ts — install the skill set this run is entitled to.
//
// The spawn effect carries the shipped set and the operator skill store
// directory. The resident reads the store and writes the whole set into
// run.json. This module is the last leg: run.json to the skill root the
// harnesses read, written at container start.
//
// Absence is never fatal. A tier that ships no skill carries nothing, and the
// caller logs and continues. A malformed entry is logged and skipped, and the
// rest of the set still installs.

import { mkdir, mkdtemp, rename, rm, writeFile } from "node:fs/promises";
import { isAbsolute, join, resolve, dirname, posix } from "node:path";
import type { Logger } from "../shared/log";

/** The single file basename both harnesses discover a skill by. */
export const SKILL_FILE = "SKILL.md";

/** A skill name is a single path segment used verbatim as a directory under a
 * root this module is about to remove recursively. Lowercase letters, digits
 * and hyphens only, and it starts with a letter or a digit. */
const SAFE_SKILL_NAME = /^[a-z0-9][a-z0-9-]*$/;

export interface RunSkill {
  name: string;
  body: string;
  files?: Record<string, string>;
}

function assertSafeName(name: unknown): asserts name is string {
  if (typeof name !== "string" || !SAFE_SKILL_NAME.test(name)) {
    throw new Error(`run.json carries an unsafe skill name ${JSON.stringify(name)}`);
  }
}

/** Validate one element of run.json's `skills` member. The name check happens
 * here, before any caller can hand it to a writer, so a traversal-shaped name
 * never reaches a mkdir under the wiped root. */
export function runSkillFrom(value: unknown): RunSkill {
  if (typeof value !== "object" || value === null) {
    throw new Error(`run.json carries a non-object skill (${value === null ? "null" : typeof value})`);
  }
  const { name, body, files } = value as { name?: unknown; body?: unknown; files?: unknown };
  assertSafeName(name);
  if (typeof body !== "string" || body === "") {
    throw new Error(`skill ${JSON.stringify(name)} carries an empty body`);
  }
  const checkedFiles = skillFilesFrom(files);
  return { name, body, ...(checkedFiles === undefined ? {} : { files: checkedFiles }) };
}

export function skillFilesFrom(value: unknown): Record<string, string> | undefined {
  if (value === undefined) return undefined;
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("Skill files must be an object of relative paths and text.");
  }
  const files: Record<string, string> = Object.create(null);
  const entries = Object.entries(value);
  if (entries.length > 256) throw new Error("A skill can contain at most 256 reference files.");
  for (const [path, body] of entries) {
    if (!path || path === "." || path === SKILL_FILE || path.startsWith("/") || path.startsWith("../") ||
        /[\\\0:]/.test(path) || posix.normalize(path) !== path || typeof body !== "string") {
      throw new Error(`Unsafe skill file ${JSON.stringify(path)}.`);
    }
    files[path] = body;
  }
  const paths = Object.keys(files);
  if (paths.some((path) => paths.some((other) => other.startsWith(path + "/")))) {
    throw new Error("A skill file conflicts with a reference directory.");
  }
  return files;
}

/** Validate run.json's `skills` member. An absent member returns an empty
 * list; a present member that is not a list throws. Each element is judged on
 * its own and `refused` reports each malformed one, so one bad entry never
 * costs the run the rest of its set. */
export function runSkillsFrom(value: unknown, refused: (reason: string) => void): RunSkill[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) {
    throw new Error(`run.json carries a non-list skills member (${typeof value})`);
  }
  const skills: RunSkill[] = [];
  const seen = new Set<string>();
  for (const entry of value) {
    try {
      const skill = runSkillFrom(entry);
      if (seen.has(skill.name)) {
        refused(`skill ${JSON.stringify(skill.name)} is carried twice; the second copy is not installed`);
        continue;
      }
      seen.add(skill.name);
      skills.push(skill);
    } catch (err) {
      refused(err instanceof Error ? err.message : String(err));
    }
  }
  return skills;
}

/** Replace `root` with exactly this set: `<root>/<name>/SKILL.md` per skill.
 *
 * The container mounts separate skill storage for each run within the Project home.
 * Replacement affects only that run. Names must pass validation before removal. */
export async function installRunSkills(root: string, skills: RunSkill[]): Promise<string[]> {
  const checked = skills.map(runSkillFrom);
  if (new Set(checked.map((skill) => skill.name)).size !== checked.length) {
    throw new Error("The skill set contains a duplicate name.");
  }
  await mkdir(dirname(resolve(root)), { recursive: true });
  const stage = await mkdtemp(join(dirname(resolve(root)), ".skills-stage-"));
  const paths: string[] = [];
  try {
    for (const skill of checked) {
      const dir = join(stage, skill.name);
      await mkdir(dir, { recursive: true });
      await writeFile(join(dir, SKILL_FILE), skill.body, { encoding: "utf8", mode: 0o600 });
      for (const [relative, body] of Object.entries(skill.files ?? {})) {
        const reference = join(dir, relative);
        await mkdir(dirname(reference), { recursive: true });
        await writeFile(reference, body, { encoding: "utf8", mode: 0o600 });
      }
      paths.push(join(resolve(root), skill.name, SKILL_FILE));
    }
    await rm(root, { recursive: true, force: true });
    await rename(stage, root);
    return paths;
  } finally {
    await rm(stage, { recursive: true, force: true });
  }
}

/** The one skill root both harness families read, derived from the container
 * HOME. Undefined when HOME is unset or relative: a relative HOME resolves
 * against the runner's cwd, which is a workspace mount. */
export function agentSkillRoot(env: Record<string, string | undefined>): string | undefined {
  const home = env["HOME"];
  if (!home || !isAbsolute(home)) return undefined;
  return join(home, ".agents", "skills");
}

/** Install the envelope's skill set at the container's private root. Both
 * tiers carry the same `skills` member and the same fixed root.
 *
 * `problems` is what the host side could not deliver. Each one is written into
 * the run's log here, the first place that log exists.
 *
 * It returns false when a required skill or the complete install is unavailable. */
export async function installEnvelopeSkills(
  skills: unknown,
  problems: unknown,
  log: Logger,
  env: Record<string, string | undefined> = process.env,
  requiredSkills: string[] = [],
): Promise<boolean> {
  if (Array.isArray(problems)) {
    for (const problem of problems) {
      log.warn("runner.skill_refused", `skill not delivered: ${String(problem)}`);
    }
  }
  const root = agentSkillRoot(env);
  if (root === undefined) {
    log.warn("runner.skill_skipped", "no absolute HOME; the skill set was not installed");
    return false;
  }
  try {
    const parsed = runSkillsFrom(skills, (reason) =>
      log.warn("runner.skill_refused", `skill not installed: ${reason}`));
    for (const name of requiredSkills) {
      if (!parsed.some((skill) => skill.name === name)) throw new Error(`The required skill ${name} is unavailable.`);
    }
    if (parsed.length === 0) {
      log.warn("runner.skill_skipped", "envelope carries no skills");
      // Still wipe: a previous run's set must not survive into an empty one.
      await installRunSkills(root, []);
      return true;
    }
    const at = await installRunSkills(root, parsed);
    log.info("runner.skill_installed",
      `installed ${parsed.length} skill(s) under ${root}: ${parsed.map((s) => s.name).join(", ")}`,
      { count: parsed.length, root, skills: parsed.map((s) => s.name).join(","), first: at[0] ?? "" });
    return true;
  } catch (err) {
    log.warn("runner.skill_skipped",
      `skill set was not installed: ${err instanceof Error ? err.message : String(err)}`);
    return false;
  }
}
