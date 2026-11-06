// main.test.ts — config parsing fails closed on anything malformed, and the
// process serves the dashboard and the wiki exports.

import { describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { parseConfig } from "./main";

const KEY = "7a".repeat(32);

describe("parseConfig", () => {
  test("accepts a release identity and refuses unsafe identities", () => {
    expect(parseConfig('{"mc_bin":"/x/mc","auth_token":"t","release_id":"release-two"}').release_id).toBe("release-two");
    for (const release_id of ["", 3, "<script>", "a b"]) {
      expect(() => parseConfig(JSON.stringify({ mc_bin: "/x/mc", auth_token: "t", release_id }))).toThrow("release_id");
    }
  });
  test("accepts the minimal and the full shape", () => {
    expect(parseConfig('{"mc_bin":"/x/mc","auth_token":"t"}')).toEqual({
      mc_bin: "/x/mc",
      bind: undefined,
      wiki_bind: undefined,
      wiki_key: undefined,
      auth_token: "t",
    });
    expect(parseConfig(`{"mc_bin":"/x/mc","bind":"127.0.0.1:0","wiki_bind":"127.0.0.1:7335","wiki_key":"${KEY}","auth_token":"t"}`)).toEqual({
      mc_bin: "/x/mc",
      bind: "127.0.0.1:0",
      wiki_bind: "127.0.0.1:7335",
      wiki_key: KEY,
      auth_token: "t",
    });
  });

  test("refuses non-objects, a missing mc_bin, mistyped fields, and a missing token", () => {
    expect(() => parseConfig("nope")).toThrow("JSON object");
    expect(() => parseConfig("[1]")).toThrow("JSON object");
    expect(() => parseConfig("{}")).toThrow("mc_bin");
    expect(() => parseConfig('{"mc_bin":""}')).toThrow("mc_bin");
    expect(() => parseConfig('{"mc_bin":"/x","bind":9,"auth_token":"t"}')).toThrow("bind");
    expect(() => parseConfig('{"mc_bin":"/x","wiki_bind":9,"auth_token":"t"}')).toThrow('"wiki_bind" must be a string');
    expect(() => parseConfig('{"mc_bin":"/x","auth_token":9}')).toThrow("auth_token");
    expect(() => parseConfig('{"mc_bin":"/x"}')).toThrow("auth_token");
    expect(() => parseConfig('{"mc_bin":"/x","auth_token":""}')).toThrow("auth_token");
    for (const key of [9, "", "short", KEY.toUpperCase(), `${KEY}0`]) {
      expect(() => parseConfig(JSON.stringify({ mc_bin: "/x", auth_token: "t", wiki_key: key })))
        .toThrow('"wiki_key" must be 64 lowercase hexadecimal characters');
    }
  });
});

describe("the dashboard process", () => {
  async function boot(config: Record<string, unknown>) {
    const home = mkdtempSync(join(tmpdir(), "culler-dashboard-main-"));
    const path = join(home, "dashboard.json");
    writeFileSync(path, JSON.stringify({ mc_bin: "/nonexistent/mc", auth_token: "t", bind: "127.0.0.1:0", ...config }));
    const child = Bun.spawn(["bun", join(import.meta.dir, "main.ts"), "--config", path], {
      env: { ...process.env, MC_HOME: home }, stdout: "ignore", stderr: "pipe",
    });
    const reader = child.stderr.getReader();
    let text = "";
    const deadline = Date.now() + 10_000;
    while (!/wiki serving on \S+|the wiki address did not start/.test(text) && Date.now() < deadline) {
      const chunk = await reader.read();
      if (chunk.done) break;
      text += new TextDecoder().decode(chunk.value);
    }
    return { home, child, text };
  }

  test("serves the wiki exports at the wiki address", async () => {
    const { home, child, text } = await boot({ wiki_bind: "127.0.0.1:0", wiki_key: KEY });
    try {
      const wiki = text.match(/wiki serving on (\S+)/)?.[1];
      expect(wiki).toBeDefined();
      expect(text).not.toContain(KEY);
      const commit = "d".repeat(40);
      mkdirSync(join(home, "wiki", "alpha", commit), { recursive: true });
      writeFileSync(join(home, "wiki", "alpha", commit, "index.html"), "exported page");
      writeFileSync(join(home, "wiki", "alpha", "export.json"), JSON.stringify({ project: "alpha", commit, directory: commit }));
      expect(await (await fetch(`${wiki}${KEY}/alpha/`)).text()).toBe("exported page");
      expect((await fetch(`${wiki}alpha/`)).status).toBe(404);
    } finally {
      child.kill();
      rmSync(home, { recursive: true, force: true });
    }
  });

  test("keeps the dashboard available when the wiki address cannot bind", async () => {
    const taken = Bun.serve({ hostname: "127.0.0.1", port: 0, fetch: () => new Response("taken") });
    const { home, child, text } = await boot({ wiki_bind: `127.0.0.1:${taken.port}`, wiki_key: KEY });
    try {
      expect(text).toContain("the wiki address did not start");
      const dashboard = text.match(/dashboard serving on (\S+)/)?.[1];
      expect((await fetch(dashboard!)).status).toBe(200);
    } finally {
      child.kill();
      taken.stop(true);
      rmSync(home, { recursive: true, force: true });
    }
  });

  test("starts no wiki address without a wiki key", async () => {
    const { home, child, text } = await boot({ wiki_bind: "127.0.0.1:0" });
    try {
      expect(text).toContain('the wiki address did not start: the dashboard config holds no "wiki_key"');
      expect(text).not.toContain("wiki serving on");
      const dashboard = text.match(/dashboard serving on (\S+)/)?.[1];
      expect((await fetch(dashboard!)).status).toBe(200);
    } finally {
      child.kill();
      rmSync(home, { recursive: true, force: true });
    }
  });
});
