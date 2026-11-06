// skill-store.ts — the resident's read of the operator skill store.
//
// The operator writes `<skill-name>/SKILL.md` directories under
// $MC_HOME/skills; `mc dispatch` names the one directory a run reads on its
// effect (`skill_store_dir`, MC_HOME-relative); this module reads that
// directory at spawn and turns it into the `skills` list run.json carries into
// the container, where the runner installs each entry at
// `<root>/<skill-name>/SKILL.md`. The container holds the set that existed
// when it started; a later change to the store does not reach it.
//
// Failure never stops the run. Every skill the store cannot deliver — a name
// outside the permitted set, a missing or unreadable SKILL.md, a file with no
// frontmatter, no `name`/`description`, a `name` that disagrees with its
// directory, or no body — becomes one line in `problems`, which the runner
// writes into the run's log; the remainder of the set is delivered.

/** One skill as run.json carries it: the directory name the harness discovers
 * it by, and the whole SKILL.md text. */
export interface RunSkill {
  name: string;
  body: string;
}

export interface OperatorSkills {
  skills: RunSkill[];
  problems: string[];
}

/** The file a skill directory holds. */
export const SKILL_FILE = "SKILL.md";

/** The permitted skill name: lowercase letters, digits and hyphens, starting
 * with a letter or digit. Also exactly one safe path segment, which is what
 * lets the runner join it under a root it wipes. */
export const SKILL_NAME = /^[a-z0-9][a-z0-9-]*$/;

/** The resident creates these directories for Projects that Collie adds.
 * The Strategist directory preserves access to existing operator skills. */
export const SKILL_STORE_ROLE_DIRS = [
  "arbiter",
  "packager",
  "scribe",
  "setup",
  "strategist-propose",
  "verifier",
  "worker",
] as const;

/** An identifier that can be one directory name under the store. Mirrors the
 * Go side's validSkillStoreSegment; a Project id outside it has no store
 * directory and its runs receive no operator skills. */
export function validStoreSegment(id: string): boolean {
  if (id === "" || id === "." || id === "..") return false;
  return !/[/\\\0]/.test(id);
}

export interface StoreFs {
  readdir(path: string): Promise<string[]>;
  readFile(path: string): Promise<string>;
  mkdir(path: string): Promise<void>;
}

/** `skill_store_dir` is a carrier value the resident joins under its own
 * MC_HOME. It must be a clean relative path under `skills/`; anything else is
 * refused rather than resolved, because the resident is about to read files
 * at it. */
export function skillStoreHostDir(mcHome: string, relDir: unknown): string | undefined {
  if (typeof relDir !== "string" || relDir === "") return undefined;
  const parts = relDir.split("/");
  if (parts[0] !== "skills" || parts.length < 2) return undefined;
  if (parts.some((p) => p === "" || p === "." || p === ".." || /[\\\0]/.test(p))) return undefined;
  return `${mcHome}/${relDir}`;
}

function isNotFound(err: unknown): boolean {
  return typeof err === "object" && err !== null && (err as { code?: unknown }).code === "ENOENT";
}

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** Reads one frontmatter scalar out of a SKILL.md. Deliberately not a YAML
 * parser, like the Go side's SkillFrontmatterField: two flat keys are what
 * the harness loader reads, and a real parser would admit shapes it does not.
 * Returns undefined when the block or the key is absent. */
export function frontmatterField(text: string, field: string): string | undefined {
  const block = frontmatterBlock(text);
  if (block === undefined) return undefined;
  for (const line of block.split("\n")) {
    const i = line.indexOf(":");
    if (i < 0) continue;
    if (line.slice(0, i).trim() === field) return line.slice(i + 1).trim();
  }
  return undefined;
}

/** The text between the opening `---` line and the closing one, or undefined
 * when the file does not start with a frontmatter block. */
function frontmatterBlock(text: string): string | undefined {
  const rest = text.replace(/^\n+/, "");
  if (!rest.startsWith("---\n")) return undefined;
  const end = rest.indexOf("\n---", "---\n".length - 1);
  if (end < 0) return undefined;
  return rest.slice("---\n".length, end + 1);
}

/** The text after the frontmatter block, or undefined when there is no block. */
function bodyAfterFrontmatter(text: string): string | undefined {
  const rest = text.replace(/^\n+/, "");
  if (!rest.startsWith("---\n")) return undefined;
  const end = rest.indexOf("\n---", "---\n".length - 1);
  if (end < 0) return undefined;
  const afterMarker = rest.slice(end + "\n---".length);
  // The closing marker owns its line; what follows the newline is the body.
  const nl = afterMarker.indexOf("\n");
  return nl < 0 ? "" : afterMarker.slice(nl + 1);
}

/** Validates one operator skill file against the store's contract. Returns the
 * problem, or undefined when the file is deliverable. */
export function skillFileProblem(name: string, text: string): string | undefined {
  if (frontmatterBlock(text) === undefined) {
    return `skill ${name}: SKILL.md does not start with a YAML frontmatter block`;
  }
  const declared = frontmatterField(text, "name");
  if (declared === undefined || declared === "") {
    return `skill ${name}: frontmatter has no name field`;
  }
  if (declared !== name) {
    return `skill ${name}: frontmatter name ${JSON.stringify(declared)} is not the directory name`;
  }
  const description = frontmatterField(text, "description");
  if (description === undefined || description === "") {
    return `skill ${name}: frontmatter has no description field`;
  }
  const body = bodyAfterFrontmatter(text);
  if (body === undefined || body.trim() === "") {
    return `skill ${name}: no body after the frontmatter block`;
  }
  return undefined;
}

/** Reads every deliverable skill in one store directory. An absent directory
 * means the role has no operator skills there and is not a problem; anything
 * else the store cannot deliver is. Entries are visited in name order so the
 * envelope is deterministic. */
export async function readOperatorSkills(dir: string, fs: StoreFs): Promise<OperatorSkills> {
  const skills: RunSkill[] = [];
  const problems: string[] = [];
  let names: string[];
  try {
    names = (await fs.readdir(dir)).slice().sort();
  } catch (err) {
    if (isNotFound(err)) return { skills, problems };
    problems.push(`skill store ${dir}: cannot list: ${describe(err)}`);
    return { skills, problems };
  }
  for (const name of names) {
    if (!SKILL_NAME.test(name)) {
      problems.push(`refused skill name ${JSON.stringify(name)}: a name holds only lowercase letters, digits and hyphens, and starts with a letter or digit`);
      continue;
    }
    let text: string;
    try {
      text = await fs.readFile(`${dir}/${name}/${SKILL_FILE}`);
    } catch (err) {
      problems.push(`skill ${name}: cannot read ${SKILL_FILE}: ${describe(err)}`);
      continue;
    }
    const problem = skillFileProblem(name, text);
    if (problem !== undefined) {
      problems.push(problem);
      continue;
    }
    skills.push({ name, body: text });
  }
  return { skills, problems };
}

/** The run's whole skill set: the skills Culler ships for the role
 * (in the effect's order, the command surface first), then each operator
 * skill. An operator skill that takes a shipped skill's name is refused — the
 * container's shipped guidance is not the operator's to shadow. */
export function composeRunSkills(shipped: RunSkill[], operator: OperatorSkills): OperatorSkills {
  const skills: RunSkill[] = shipped.slice();
  const problems = operator.problems.slice();
  const taken = new Set(shipped.map((skill) => skill.name));
  for (const skill of operator.skills) {
    if (taken.has(skill.name)) {
      problems.push(`refused skill ${skill.name}: the name is one of the role's shipped skills`);
      continue;
    }
    skills.push(skill);
  }
  return { skills, problems };
}

/** The `skills` member of a spawn or wake effect: the role's shipped set, as
 * `mc` emitted it. Anything else is refused whole — the effect is `mc`'s, and a
 * shape it does not emit means the two sides disagree. */
export function shippedSkillsFrom(value: unknown): RunSkill[] | undefined {
  if (value === undefined) return [];
  if (!Array.isArray(value)) return undefined;
  const skills: RunSkill[] = [];
  for (const entry of value) {
    if (typeof entry !== "object" || entry === null) return undefined;
    const { name, body } = entry as { name?: unknown; body?: unknown };
    if (typeof name !== "string" || !SKILL_NAME.test(name) || typeof body !== "string" || body === "") return undefined;
    skills.push({ name, body });
  }
  return skills;
}

/** Creates a Project's store directories when they are absent: the
 * Project directory and one directory per role directory name. Extends an
 * existing store only — the store root is `mc onboard home`'s to create.
 * Best-effort and silent: an operator who never writes a skill loses nothing
 * when this cannot run. */
export async function ensureProjectSkillStore(mcHome: string, project: string, fs: StoreFs): Promise<void> {
  if (!validStoreSegment(project)) return;
  try {
    await fs.readdir(`${mcHome}/skills`);
  } catch {
    return;
  }
  const wsDir = `${mcHome}/skills/projects/${project}`;
  for (const dir of [wsDir, ...SKILL_STORE_ROLE_DIRS.map((role) => `${wsDir}/${role}`)]) {
    try {
      await fs.mkdir(dir);
    } catch {
      return;
    }
  }
}
