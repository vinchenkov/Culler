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

import { mkdir, rm, writeFile } from "node:fs/promises";
import { isAbsolute, join, resolve } from "node:path";
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
  const { name, body } = value as { name?: unknown; body?: unknown };
  assertSafeName(name);
  if (typeof body !== "string" || body === "") {
    throw new Error(`skill ${JSON.stringify(name)} carries an empty body`);
  }
  return { name, body };
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
  for (const skill of skills) {
    assertSafeName(skill.name);
    if (typeof skill.body !== "string" || skill.body === "") {
      throw new Error(`skill ${JSON.stringify(skill.name)} carries an empty body`);
    }
  }
  await rm(root, { recursive: true, force: true });
  const paths: string[] = [];
  for (const skill of skills) {
    const dir = resolve(root, skill.name);
    await mkdir(dir, { recursive: true });
    const path = join(dir, SKILL_FILE);
    await writeFile(path, skill.body, { encoding: "utf8", mode: 0o600 });
    paths.push(path);
  }
  return paths;
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
 * It never throws. A missing skill degrades an agent's guidance, and a run
 * that works degraded is worth more than a run refused at startup. */
export async function installEnvelopeSkills(
  skills: unknown,
  problems: unknown,
  log: Logger,
  env: Record<string, string | undefined> = process.env,
): Promise<void> {
  if (Array.isArray(problems)) {
    for (const problem of problems) {
      log.warn("runner.skill_refused", `skill not delivered: ${String(problem)}`);
    }
  }
  const root = agentSkillRoot(env);
  if (root === undefined) {
    log.warn("runner.skill_skipped", "no absolute HOME; the skill set was not installed");
    return;
  }
  try {
    const parsed = runSkillsFrom(skills, (reason) =>
      log.warn("runner.skill_refused", `skill not installed: ${reason}`));
    if (parsed.length === 0) {
      log.warn("runner.skill_skipped", "envelope carries no skills");
      // Still wipe: a previous run's set must not survive into an empty one.
      await installRunSkills(root, []);
      return;
    }
    const at = await installRunSkills(root, parsed);
    log.info("runner.skill_installed",
      `installed ${parsed.length} skill(s) under ${root}: ${parsed.map((s) => s.name).join(", ")}`,
      { count: parsed.length, root, skills: parsed.map((s) => s.name).join(","), first: at[0] ?? "" });
  } catch (err) {
    log.warn("runner.skill_skipped",
      `skill set was not installed: ${err instanceof Error ? err.message : String(err)}`);
  }
}
