// mc.test.ts — the real seam spawns the binary, parses one JSON object from
// stdout, and reports exit code + stderr faithfully.

import { describe, expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { makeMcRunner } from "./mc";

function stubMc(script: string): string {
  const dir = mkdtempSync(join(tmpdir(), "mc-stub-"));
  const path = join(dir, "mc");
  writeFileSync(path, `#!/bin/sh\n${script}\n`, { mode: 0o755 });
  return path;
}

describe("makeMcRunner", () => {
  test("success: parses the JSON object and passes argv through", async () => {
    const bin = stubMc(`echo "{\\"argv\\":\\"$*\\"}"`);
    const res = await makeMcRunner(bin).run(["collie", "list"]);
    expect(res.code).toBe(0);
    expect(res.json).toEqual({ argv: "collie list" });
  });

  test("domain rejection: exit 1 with the error envelope and stderr", async () => {
    const bin = stubMc(`echo '{"error":{"code":"domain-rejection","message":"no"}}'; echo 'mc: no' >&2; exit 1`);
    const res = await makeMcRunner(bin).run(["collie", "history", "h-x"]);
    expect(res.code).toBe(1);
    expect((res.json?.["error"] as Record<string, unknown>)["code"]).toBe("domain-rejection");
    expect(res.stderr).toContain("mc: no");
  });

  test("non-JSON stdout yields json: null, never a throw", async () => {
    const bin = stubMc(`echo 'not json'; exit 2`);
    const res = await makeMcRunner(bin).run(["doctor"]);
    expect(res.code).toBe(2);
    expect(res.json).toBeNull();
  });
});


test("The command receives structured input outside its arguments", async () => {
  const bin = stubMc('cat');
  const result = await makeMcRunner(bin).run(["agent-profile", "save"], JSON.stringify({ model: "local-model" }));
  expect(result.code).toBe(0);
  expect(result.json).toEqual({ model: "local-model" });
});


test("A connection token reaches standard input and stays outside command arguments", async () => {
  const bin = stubMc(`if [ "$#" -ne 2 ] || [ "$1" != "agent-profile" ] || [ "$2" != "connection-save" ]; then exit 9; fi\ncat >/dev/null\necho '{"accepted":true}'`);
  const result = await makeMcRunner(bin).run(["agent-profile", "connection-save"], JSON.stringify({ token: "private-token-fixture" }));
  expect(result.code).toBe(0);
  expect(result.json).toEqual({ accepted: true });
  expect(JSON.stringify(result)).not.toContain("private-token-fixture");
});
