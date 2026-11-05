import { afterEach, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { makeTestLogger } from "../shared/log";
import {
  agentSkillRoot, installEnvelopeSkills, installRunSkills, runSkillFrom, runSkillsFrom, SKILL_FILE,
} from "./skills";

const roots: string[] = [];
afterEach(async () => {
  for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true });
});

async function root(): Promise<string> {
  const value = await mkdtemp(join(tmpdir(), "mc-role-skill-"));
  roots.push(value);
  return value;
}

describe("one skill from the launch envelope", () => {
  test("accepts the resident's nested skill object verbatim", () => {
    expect(runSkillFrom({ name: "mc-worker", body: "---\nname: mc-worker\n---\nbody" }))
      .toEqual({ name: "mc-worker", body: "---\nname: mc-worker\n---\nbody" });
  });

  test("a name that is not one safe path segment refuses before any mkdir", () => {
    expect(() => runSkillFrom({ name: "mc/worker", body: "x" })).toThrow("unsafe skill name");
    expect(() => runSkillFrom({ name: "..", body: "x" })).toThrow("unsafe skill name");
    expect(() => runSkillFrom({ name: "../../etc", body: "x" })).toThrow("unsafe skill name");
    expect(() => runSkillFrom({ name: "", body: "x" })).toThrow("unsafe skill name");
    expect(() => runSkillFrom({ name: "MC-Worker", body: "x" })).toThrow("unsafe skill name");
    expect(() => runSkillFrom({ name: 7, body: "x" })).toThrow("unsafe skill name");
  });

  test("an empty body refuses — a skill file with no instructions is a lie, not a default", () => {
    expect(() => runSkillFrom({ name: "mc-verifier", body: "" })).toThrow("empty body");
    expect(() => runSkillFrom({ name: "mc-verifier" })).toThrow("empty body");
  });
});

describe("the skill set from the launch envelope", () => {
  test("absence is not an error — older run.json and skill-less tiers carry none", () => {
    expect(runSkillsFrom(undefined, () => {})).toEqual([]);
    expect(runSkillsFrom(null, () => {})).toEqual([]);
  });

  test("a malformed entry is reported and skipped; the rest of the set survives", () => {
    const refused: string[] = [];
    const skills = runSkillsFrom([
      { name: "mc-worker", body: "surface" },
      { name: "../escape", body: "x" },
      { name: "deploy", body: "" },
      "not an object",
      { name: "deploy", body: "deploy help" },
      { name: "deploy", body: "a second deploy" },
    ], (reason) => refused.push(reason));
    expect(skills).toEqual([{ name: "mc-worker", body: "surface" }, { name: "deploy", body: "deploy help" }]);
    expect(refused).toEqual([
      'run.json carries an unsafe skill name "../escape"',
      'skill "deploy" carries an empty body',
      "run.json carries a non-object skill (string)",
      'skill "deploy" is carried twice; the second copy is not installed',
    ]);
  });

  test("a member that is not a list is malformed as a whole", () => {
    expect(() => runSkillsFrom({ name: "mc-worker", body: "x" }, () => {})).toThrow("non-list skills member");
  });
});

describe("installing the skill set", () => {
  test("writes each body at <root>/<name>/SKILL.md", async () => {
    const dir = await root();
    const paths = await installRunSkills(dir, [
      { name: "mc-worker", body: "worker instructions" },
      { name: "deploy", body: "deploy instructions" },
    ]);
    expect(paths).toEqual([join(dir, "mc-worker", SKILL_FILE), join(dir, "deploy", SKILL_FILE)]);
    expect(await readFile(join(dir, "mc-worker", SKILL_FILE), "utf8")).toBe("worker instructions");
    expect(await readFile(join(dir, "deploy", SKILL_FILE), "utf8")).toBe("deploy instructions");
    expect((await readdir(dir)).sort()).toEqual(["deploy", "mc-worker"]);
  });

  test("creates missing parents — the skills root need not exist yet", async () => {
    const dir = join(await root(), "nested", "skills");
    await installRunSkills(dir, [{ name: "mc-packager", body: "packager instructions" }]);
    expect(await readFile(join(dir, "mc-packager", SKILL_FILE), "utf8")).toBe("packager instructions");
  });

  test("a previous run's skills cannot survive into this run", async () => {
    // A repeated installation replaces only the skills in this run's private directory.
    const dir = await root();
    await mkdir(join(dir, "mc-verifier"), { recursive: true });
    await writeFile(join(dir, "mc-verifier", SKILL_FILE), "verifier instructions");
    await mkdir(join(dir, "retired-operator-skill", "stale"), { recursive: true });
    await writeFile(join(dir, "retired-operator-skill", SKILL_FILE), "old instructions");

    await installRunSkills(dir, [{ name: "mc-worker", body: "worker instructions" }]);

    // Read the directory back rather than trusting the returned path: the claim under
    // test is what the role can DISCOVER, not what we believe we wrote.
    expect((await readdir(dir)).sort()).toEqual(["mc-worker"]);
    expect(await readFile(join(dir, "mc-worker", SKILL_FILE), "utf8")).toBe("worker instructions");
  });

  test("an empty set still wipes the root", async () => {
    const dir = await root();
    await mkdir(join(dir, "mc-verifier"), { recursive: true });
    await writeFile(join(dir, "mc-verifier", SKILL_FILE), "verifier instructions");
    await installRunSkills(dir, []);
    expect(await Bun.file(join(dir, "mc-verifier", SKILL_FILE)).exists()).toBe(false);
  });

  test("a traversal-shaped name anywhere in the set never reaches the wipe or the mkdir", async () => {
    const dir = await root();
    await mkdir(join(dir, "keep"), { recursive: true });
    await expect(installRunSkills(dir, [{ name: "fine", body: "x" }, { name: "../escape", body: "x" }]))
      .rejects.toThrow("unsafe skill name");
    expect(await readdir(dir)).toEqual(["keep"]);
  });
});

describe("installing from the envelope, as the runners do", () => {
  test("installs the set, logs each problem the host could not deliver, and never throws", async () => {
    const home = await root();
    const log = makeTestLogger("runner");
    await installEnvelopeSkills(
      [{ name: "mc-worker", body: "surface" }, { name: "bad name", body: "x" }, { name: "deploy", body: "d" }],
      ["refused skill name \"Bad Name\": not permitted", "skill empty: cannot read SKILL.md"],
      log,
      { HOME: home },
    );
    expect((await readdir(join(home, ".agents", "skills"))).sort()).toEqual(["deploy", "mc-worker"]);
    const events = log.records.map((r) => [r.event, r.msg]);
    expect(events).toEqual([
      ["runner.skill_refused", 'skill not delivered: refused skill name "Bad Name": not permitted'],
      ["runner.skill_refused", "skill not delivered: skill empty: cannot read SKILL.md"],
      ["runner.skill_refused", 'skill not installed: run.json carries an unsafe skill name "bad name"'],
      ["runner.skill_installed", `installed 2 skill(s) under ${join(home, ".agents", "skills")}: mc-worker, deploy`],
    ]);
  });

  test("no HOME, no install, no throw", async () => {
    const log = makeTestLogger("runner");
    await installEnvelopeSkills([{ name: "mc-worker", body: "surface" }], undefined, log, {});
    expect(log.records.map((r) => r.event)).toEqual(["runner.skill_skipped"]);
  });

  test("an envelope with no skills wipes yesterday's set and says so", async () => {
    const home = await root();
    const shared = join(home, ".agents", "skills");
    await mkdir(join(shared, "mc-verifier"), { recursive: true });
    await writeFile(join(shared, "mc-verifier", SKILL_FILE), "verifier instructions");
    const log = makeTestLogger("runner");
    await installEnvelopeSkills(undefined, undefined, log, { HOME: home });
    expect(await Bun.file(join(shared, "mc-verifier", SKILL_FILE)).exists()).toBe(false);
    expect(log.records.map((r) => r.event)).toEqual(["runner.skill_skipped"]);
  });
});

describe("the codex-native and model-readable skill root", () => {
  test("hangs off the container HOME", () => {
    expect(agentSkillRoot({ HOME: "/home/agent" })).toBe("/home/agent/.agents/skills");
  });

  test("an unset or relative HOME yields nothing to install into, and never throws", () => {
    expect(agentSkillRoot({})).toBeUndefined();
    expect(agentSkillRoot({ HOME: "" })).toBeUndefined();
    expect(agentSkillRoot({ HOME: "home/agent" })).toBeUndefined();
  });
});
