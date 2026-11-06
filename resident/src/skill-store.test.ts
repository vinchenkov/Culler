import { describe, expect, test } from "bun:test";
import { readdirSync } from "node:fs";
import { join, resolve } from "node:path";
import {
  composeRunSkills, ensureProjectSkillStore, frontmatterField, readOperatorSkills,
  shippedSkillsFrom, SKILL_STORE_ROLE_DIRS, skillFileProblem, skillStoreHostDir, validStoreSegment,
} from "./skill-store";
import { FakeFs } from "./test-helpers";

const good = (name: string) => `---\nname: ${name}\ndescription: ${name} help\n---\nUse ${name}.\n`;

test("the Project store preserves the operator skill paths for each role", () => {
  const shipped = readdirSync(resolve(import.meta.dir, "..", "..", "mc", "verbs", "skills"))
    .filter((name) => name !== "collie" && name !== "shared")
    .map((name) => name === "strategist" ? "strategist-propose" : name)
    .sort();
  expect([...SKILL_STORE_ROLE_DIRS]).toEqual(shipped);
});

describe("the store directory the effect names", () => {
  test("is joined under MC_HOME only when it is a clean path under skills/", () => {
    expect(skillStoreHostDir("/mc", "skills/collie")).toBe("/mc/skills/collie");
    expect(skillStoreHostDir("/mc", "skills/projects/ws/worker")).toBe("/mc/skills/projects/ws/worker");
    for (const bad of ["", "skills", "runs/x", "../skills/collie", "skills/../runs", "skills//collie", "skills/a\\b", 7, null]) {
      expect(skillStoreHostDir("/mc", bad)).toBeUndefined();
    }
  });

  test("a Project id is one directory name or it has no store directory", () => {
    expect(validStoreSegment("ws-one")).toBe(true);
    for (const bad of ["", ".", "..", "a/b", "a\\b", "a\0b"]) expect(validStoreSegment(bad)).toBe(false);
  });
});

describe("a skill file", () => {
  test("reads its frontmatter scalars without a YAML parser", () => {
    expect(frontmatterField(good("deploy"), "name")).toBe("deploy");
    expect(frontmatterField(good("deploy"), "description")).toBe("deploy help");
    expect(frontmatterField("no frontmatter", "name")).toBeUndefined();
    expect(frontmatterField("---\nname: x\n", "name")).toBeUndefined();
  });

  test("is deliverable exactly when it meets the store contract", () => {
    expect(skillFileProblem("deploy", good("deploy"))).toBeUndefined();
    expect(skillFileProblem("deploy", "Use deploy.\n")).toContain("does not start with a YAML frontmatter block");
    expect(skillFileProblem("deploy", "---\ndescription: d\n---\nbody\n")).toContain("no name field");
    expect(skillFileProblem("deploy", "---\nname: other\ndescription: d\n---\nbody\n")).toContain('frontmatter name "other" is not the directory name');
    expect(skillFileProblem("deploy", "---\nname: deploy\n---\nbody\n")).toContain("no description field");
    expect(skillFileProblem("deploy", "---\nname: deploy\ndescription: d\n---\n")).toContain("no body after the frontmatter block");
    expect(skillFileProblem("deploy", "---\nname: deploy\ndescription: d\n---\n\n  \n")).toContain("no body after the frontmatter block");
  });
});

describe("reading one store directory", () => {
  const dir = "/mc/skills/projects/ws/worker";

  test("delivers every well-formed skill in name order and names every problem", async () => {
    const fs = new FakeFs();
    fs.writes.set(`${dir}/zeta/SKILL.md`, good("zeta"));
    fs.writes.set(`${dir}/alpha/SKILL.md`, good("alpha"));
    fs.dir(`${dir}/UPPER`);
    fs.dir(`${dir}/no-file`);
    fs.writes.set(`${dir}/bare/SKILL.md`, "no frontmatter\n");
    const { skills, problems } = await readOperatorSkills(dir, fs.fs);
    expect(skills.map((s) => s.name)).toEqual(["alpha", "zeta"]);
    expect(skills[0].body).toBe(good("alpha"));
    expect(problems).toEqual([
      'refused skill name "UPPER": a name holds only lowercase letters, digits and hyphens, and starts with a letter or digit',
      "skill bare: SKILL.md does not start with a YAML frontmatter block",
      "skill no-file: cannot read SKILL.md: fake file absent: /mc/skills/projects/ws/worker/no-file/SKILL.md",
    ]);
  });

  test("an absent directory is no skills and no problem", async () => {
    const fs = new FakeFs();
    expect(await readOperatorSkills(dir, fs.fs)).toEqual({ skills: [], problems: [] });
  });

  test("a directory that cannot be listed is one problem", async () => {
    const fs = new FakeFs();
    fs.fs.readdir = async () => { throw new Error("EACCES: permission denied"); };
    const { skills, problems } = await readOperatorSkills(dir, fs.fs);
    expect(skills).toEqual([]);
    expect(problems).toEqual([`skill store ${dir}: cannot list: EACCES: permission denied`]);
  });
});

describe("the run's set", () => {
  test("is the shipped set first, then the operator skills, and a shipped name is not the operator's to take", () => {
    const shipped = [{ name: "mc-verifier", body: "surface" }, { name: "verify-behavior", body: "extra" }];
    const { skills, problems } = composeRunSkills(shipped, {
      skills: [{ name: "alpha", body: "a" }, { name: "verify-behavior", body: "shadow" }, { name: "mc-verifier", body: "shadow" }],
      problems: ["earlier"],
    });
    expect(skills).toEqual([...shipped, { name: "alpha", body: "a" }]);
    expect(problems).toEqual([
      "earlier",
      "refused skill verify-behavior: the name is one of the role's shipped skills",
      "refused skill mc-verifier: the name is one of the role's shipped skills",
    ]);
  });

  test("without a shipped set the operator skills stand alone", () => {
    expect(composeRunSkills([], { skills: [{ name: "a", body: "b" }], problems: [] }))
      .toEqual({ skills: [{ name: "a", body: "b" }], problems: [] });
  });
});

describe("the effect's shipped set", () => {
  test("is read as mc emits it, and absence is an empty set", () => {
    expect(shippedSkillsFrom([{ name: "mc-worker", body: "s" }, { name: "extra", body: "c" }]))
      .toEqual([{ name: "mc-worker", body: "s" }, { name: "extra", body: "c" }]);
    expect(shippedSkillsFrom(undefined)).toEqual([]);
  });

  test("any other shape is refused whole", () => {
    for (const bad of ["mc-worker", null, [null], [{ name: "Bad Name", body: "s" }], [{ name: "ok", body: "" }], [{ name: "../x", body: "s" }]]) {
      expect(shippedSkillsFrom(bad)).toBeUndefined();
    }
  });
});

describe("creating a Project's store directories at first spawn", () => {
  test("extends an existing store with the Project and one directory per role", async () => {
    const fs = new FakeFs().dir("/mc/skills/collie");
    await ensureProjectSkillStore("/mc", "ws-one", fs.fs);
    expect(fs.events).toEqual([
      "mkdir:/mc/skills/projects/ws-one",
      ...SKILL_STORE_ROLE_DIRS.map((role) => `mkdir:/mc/skills/projects/ws-one/${role}`),
    ]);
  });

  test("leaves a home without a store alone, and refuses an id that is not one directory name", async () => {
    const fs = new FakeFs();
    await ensureProjectSkillStore("/mc", "ws-one", fs.fs);
    expect(fs.events).toEqual([]);
    fs.dir("/mc/skills");
    await ensureProjectSkillStore("/mc", "../ws", fs.fs);
    expect(fs.events).toEqual([]);
  });
});
