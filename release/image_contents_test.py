import io
import tempfile
import unittest

import image_contents
from image_archive import read_image
from image_archive_test import build_archive, make_elf, member
from image_contents import RUNTIME, added_layers, run

CLEAN = make_elf([".text", ".rodata"])
WITH_SYMTAB = make_elf([".text", ".symtab"])
WITH_DEBUG = make_elf([".text", ".symtab", ".debug_info", ".debug_line"])
BROWSER = "ms-playwright/chromium-1232/chrome-linux/"
MC_REAL = RUNTIME + "libexec/mc-real"


def project_layers():
    base = [
        member("dir", "bin"), member("file", "bin/sh", b"sh", mode=0o755),
        member("file", "usr/bin/su", b"su", mode=0o4755), member("dir", "tmp", mode=0o1777),
    ]
    tools = [
        member("dir", "opt"), member("file", "opt/go/bin/go", b"go program", mode=0o755),
        member("file", "opt/node/bin/node", b"node program", mode=0o755),
        member("file", "usr/local/bin/git", WITH_SYMTAB, mode=0o755),
        member("link", "usr/local/libexec/git-core/git-add", linkname="usr/local/bin/git"),
        member("file", "usr/local/libexec/git-core/git-remote-http", CLEAN, mode=0o755),
        member("file", BROWSER + "chrome", CLEAN, mode=0o755),
        member("file", BROWSER + "libEGL.so", CLEAN, mode=0o755),
        member("file", BROWSER + "resources.pak", b"pak"),
        member("file", "ms-playwright/chromium_headless_shell-1232/chrome-linux/headless_shell", CLEAN, mode=0o755),
        member("file", "ms-playwright/ffmpeg-1011/ffmpeg-linux", CLEAN, mode=0o755),
    ]
    accounts = [member("file", "usr/bin/su", b"su", mode=0o755), member("file", "etc/passwd", b"root:x:0:0")]
    return [base, tools, accounts]


def runner_layers():
    node = [
        member("file", RUNTIME + "node/bin/node", CLEAN, mode=0o755),
        member("file", RUNTIME + "artifacts.txt", b"node 24"),
        member("symlink", RUNTIME + "bin/node", linkname="../node/bin/node"),
    ]
    browser = [
        member("file", RUNTIME + "browser/package.json", b"{}"),
        member("file", RUNTIME + "browser/node_modules/@playwright/cli/playwright-cli.js", b"js"),
        member("file", RUNTIME + BROWSER + "chrome", CLEAN, mode=0o755),
        member("file", RUNTIME + BROWSER + "libEGL.so", CLEAN, mode=0o755),
        member("file", RUNTIME + "ms-playwright/ffmpeg-1011/ffmpeg-linux", CLEAN, mode=0o755),
    ]
    openwiki = [
        member("file", RUNTIME + "openwiki/package.json", b"{}"),
        member("dir", RUNTIME + "openwiki/node_modules/openwiki"),
        member("file", RUNTIME + "openwiki/node_modules/openwiki/dist/server.js", b"js"),
    ]
    harness = [
        member("file", RUNTIME + "bun/bin/bun", CLEAN, mode=0o755),
        member("file", RUNTIME + "app/package.json", b"{}"),
        member("dir", RUNTIME + "app/node_modules/@anthropic-ai/claude-agent-sdk"),
        member("file", RUNTIME + "app/node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs", b"mjs"),
        member("file", RUNTIME + "app/node_modules/@anthropic-ai/claude-agent-sdk-linux-arm64/claude", CLEAN, mode=0o755),
        member("file", RUNTIME + "app/node_modules/@openai/codex-linux-arm64/vendor/aarch64-unknown-linux-musl/bin/codex",
               CLEAN, mode=0o755),
        member("file", RUNTIME + "app/node_modules/hono/dist/index.js", b"hono"),
        member("link", RUNTIME + "app/node_modules/hono/dist/index.cjs", linkname=RUNTIME + "app/node_modules/hono/dist/index.js"),
    ]
    source = [
        member("file", RUNTIME + "app/src/index.ts", b"ts"),
        member("file", RUNTIME + "commands/mc", b"sh", mode=0o755),
        member("file", RUNTIME + "commands/mc-land", b"sh", mode=0o755),
        member("file", RUNTIME + "commands/playwright-cli", b"sh", mode=0o755),
        member("file", RUNTIME + "bin/check", b"sh", mode=0o755),
    ]
    mc_real = [member("file", MC_REAL, WITH_SYMTAB, mode=0o6755, uid=10001, gid=10001)]
    release = [
        member("file", RUNTIME + "release-id", b"abc\n"), member("file", RUNTIME + "manifest.json", b"{}"),
        member("symlink", "usr/local/bin/mc", linkname="/opt/culler-runtime/commands/mc"),
        member("symlink", "usr/local/bin/mc-land", linkname="/opt/culler-runtime/commands/mc-land"),
        member("symlink", "opt/openwiki", linkname="/opt/culler-runtime/openwiki"),
        member("symlink", "app", linkname="/opt/culler-runtime/app"),
        member("symlink", "usr/local/libexec/mc-real", linkname="/opt/culler-runtime/libexec/mc-real"),
    ]
    return [node, browser, openwiki, harness, source, mc_real, release]


def runner_history():
    steps = ["RUN project step"] * len(project_layers()) + [
        "RUN |2 CULLER_NODE_VERSION=24 /bin/sh -c curl node",
        "RUN /bin/sh -c npm ci && playwright-cli install-browser",
        "RUN /bin/sh -c npm ci openwiki",
        "RUN /bin/sh -c bun install --production",
        "COPY runner/runtime-source/ /opt/culler-runtime/app/src/ # buildkit",
        "COPY --chown=10001:10001 --chmod=6755 runner/image/mc /opt/culler-runtime/libexec/mc-real # buildkit",
        "RUN /bin/sh -c printf id >/opt/culler-runtime/release-id && ln -s /opt/culler-runtime/libexec/mc-real /usr/local/libexec/mc-real",
    ]
    return [{"created_by": step} for step in steps]


def with_members(layers, index, *members):
    copy = [list(layer) for layer in layers]
    copy[index] += members
    return copy


def without(layers, path):
    return [[item for item in layer if item[1] != path] for layer in layers]


class ContentsTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.count = 0

    def archive(self, layers, history=None):
        self.count += 1
        return build_archive(self.directory.name, "image-{}.tar".format(self.count), layers, history=history)

    def project(self, layers=None):
        return read_image(self.archive(layers or project_layers()),
                          hash_paths=image_contents.project_hash_paths, elf_paths=image_contents.project_elf_paths)

    def runner(self, added=None, history=None, base=None):
        project = base or project_layers()
        runner = read_image(self.archive(project + (added or runner_layers()), history=history or runner_history()),
                            elf_paths=image_contents.runner_elf_paths)
        return runner, added_layers(runner, self.project(project))

    def assertFails(self, failures, text):
        self.assertTrue(failures, "expected a failure that mentions " + text)
        self.assertIn(text, "\n".join(failures))

    def test_the_good_images_pass_every_check(self):
        project = self.project()
        runner, added = self.runner()
        for name, check in image_contents.PROJECT_CHECKS:
            self.assertEqual(check(project), [], name)
        for name, check in image_contents.RUNNER_CHECKS:
            self.assertEqual(check(runner, added), [], name)

    def test_tool_files_once(self):
        layers = with_members(project_layers(), 2, member("file", "opt/go/bin/go", b"go program", mode=0o755))
        self.assertFails(image_contents.project_tool_files_once(self.project(layers)),
                         "1 tool files appear in two layers (0.0 MB of second copies in layer 3): /opt/go/bin/go")
        changed = with_members(project_layers(), 2, member("file", "opt/go/bin/go", b"other program", mode=0o755))
        self.assertFails(image_contents.project_tool_files_once(self.project(changed)), "/opt/go/bin/go")

    def test_pak_info(self):
        layers = with_members(project_layers(), 1, member("file", BROWSER + "resources.pak.info", b"info"))
        self.assertFails(image_contents.project_pak_info(self.project(layers)), "resources.pak.info")

    def test_git_translations(self):
        layers = with_members(project_layers(), 1, member("file", "usr/local/share/locale/de/LC_MESSAGES/git.mo", b"mo"))
        self.assertFails(image_contents.project_git_translations(self.project(layers)), "/usr/local/share/locale")

    def test_build_caches(self):
        for path in ("root/.npm/_cacache/index", "root/.cache/node/compile", "root/.bun/install/cache/x", "root/.node/x", "tmp/left.tar.xz"):
            layers = with_members(project_layers(), 1, member("file", path, b"cache"))
            self.assertFails(image_contents.project_build_caches(self.project(layers)), "/" + path)
        layers = with_members(project_layers(), 1, member("dir", "tmp/empty"))
        self.assertEqual(image_contents.project_build_caches(self.project(layers)), [])
        layers = with_members(project_layers(), 1, member("dir", "root/.config"), member("file", "root/.config/go/telemetry/mode", b"local"))
        self.assertFails(image_contents.project_build_caches(self.project(layers)), "root/.config")
        layers = with_members(project_layers(), 0, member("dir", "root"), member("file", "root/.bashrc", b"x"), member("file", "root/.profile", b"x"))
        self.assertEqual(image_contents.project_build_caches(self.project(layers)), [])

    def test_browser_programs(self):
        layers = with_members(without(project_layers(), BROWSER + "chrome"), 1, member("file", BROWSER + "chrome", WITH_SYMTAB, mode=0o755))
        self.assertFails(image_contents.project_browser_programs(self.project(layers)), ".symtab")
        layers = with_members(without(project_layers(), BROWSER + "libEGL.so"), 1, member("file", BROWSER + "libEGL.so", WITH_DEBUG, mode=0o755))
        self.assertFails(image_contents.project_browser_programs(self.project(layers)), ".debug_info")
        self.assertFails(image_contents.project_browser_programs(self.project(without(project_layers(), BROWSER + "chrome"))), "no chrome program")

    def test_git_programs(self):
        layers = with_members(without(project_layers(), "usr/local/bin/git"), 1, member("file", "usr/local/bin/git", WITH_DEBUG, mode=0o755))
        self.assertFails(image_contents.project_git_programs(self.project(layers)),
                         "1 Git programs hold debug sections: /usr/local/bin/git (layer 2): .debug_info, .debug_line")
        linked = with_members(without(project_layers(), "usr/local/bin/git"), 1,
                              member("file", "usr/local/libexec/git-core/git", WITH_DEBUG, mode=0o755),
                              member("link", "usr/local/bin/git", linkname="usr/local/libexec/git-core/git"))
        self.assertFails(image_contents.project_git_programs(self.project(linked)), "/usr/local/libexec/git-core/git (layer 2): .debug_info")
        helper = "usr/local/libexec/git-core/git-remote-http"
        layers = with_members(without(project_layers(), helper), 1, member("file", helper, WITH_DEBUG, mode=0o755))
        self.assertFails(image_contents.project_git_programs(self.project(layers)), "/" + helper + " (layer 2): .debug_info")
        script = member("file", "usr/local/libexec/git-core/git-sh-setup", b"#!/bin/sh\n", mode=0o755)
        self.assertEqual(image_contents.project_git_programs(self.project(with_members(project_layers(), 1, script))), [])
        self.assertFails(image_contents.project_git_programs(self.project(without(project_layers(), "usr/local/bin/git"))), "absent")

    def test_setuid(self):
        layers = with_members(project_layers(), 2, member("file", "usr/bin/sudo", b"sudo", mode=0o4755))
        self.assertFails(image_contents.project_setuid(self.project(layers)), "/usr/bin/sudo")
        layers = with_members(project_layers(), 2, member("file", "usr/bin/wall", b"wall", mode=0o2755))
        self.assertFails(image_contents.project_setuid(self.project(layers)), "/usr/bin/wall")

    def test_tool_access(self):
        layers = with_members(project_layers(), 2, member("dir", "opt/project-tools", mode=0o644))
        self.assertFails(image_contents.project_tool_access(self.project(layers)), "/opt/project-tools")
        layers = with_members(project_layers(), 2, member("file", "opt/project-tools/package.json", b"{}", mode=0o600))
        self.assertFails(image_contents.project_tool_access(self.project(layers)), "/opt/project-tools/package.json")
        layers = with_members(project_layers(), 2, member("dir", "opt/project-tools", mode=0o755),
                              member("file", "opt/project-tools/package.json", b"{}", mode=0o644))
        self.assertEqual(image_contents.project_tool_access(self.project(layers)), [])

    def test_runner_base(self):
        runner, _ = self.runner()
        self.assertFails(image_contents.runner_base(runner, list(runner.layers)), "shares no layer")

    def test_runner_browser(self):
        root = RUNTIME + "ms-playwright/"
        runner, added = self.runner(with_members(runner_layers(), 1, member("file", root + "chromium_headless_shell-1232/chrome-linux/headless_shell", CLEAN, mode=0o755)))
        self.assertFails(image_contents.runner_browser(runner, added), "headless shell")
        runner, added = self.runner(with_members(runner_layers(), 1, member("file", RUNTIME + BROWSER + "chrome_100_percent.pak.info", b"i")))
        self.assertFails(image_contents.runner_browser(runner, added), ".pak.info")
        runner, added = self.runner(without(runner_layers(), RUNTIME + "ms-playwright/ffmpeg-1011/ffmpeg-linux"))
        self.assertFails(image_contents.runner_browser(runner, added), "no ffmpeg-* folder")
        runner, added = self.runner(with_members(without(runner_layers(), RUNTIME + BROWSER + "chrome"), 1, member("file", RUNTIME + BROWSER + "chrome", WITH_DEBUG, mode=0o755)))
        self.assertFails(image_contents.runner_browser(runner, added), ".symtab")

    def test_runner_openwiki(self):
        root = RUNTIME + "openwiki/node_modules/"
        for name in ("openwiki/dist/server.js.map", "openwiki/dist/server.d.ts", "x/index.d.mts", "x/index.d.cts"):
            runner, added = self.runner(with_members(runner_layers(), 2, member("file", root + name, b"x")))
            self.assertFails(image_contents.runner_openwiki(runner, added), name)
        runner, added = self.runner(without(without(runner_layers(), root + "openwiki"), root + "openwiki/dist/server.js"))
        self.assertFails(image_contents.runner_openwiki(runner, added), "OpenWiki package is absent")

    def test_runner_harness_packages(self):
        root = RUNTIME + "app/node_modules/"
        for name in ("@anthropic-ai/claude-agent-sdk-linux-arm64-musl/claude", "@anthropic-ai/sdk/index.js", "@modelcontextprotocol/sdk/index.js", "zod/index.js"):
            runner, added = self.runner(with_members(runner_layers(), 3, member("file", root + name, b"x")))
            self.assertFails(image_contents.runner_harness_packages(runner, added), name.split("/")[0])
        runner, added = self.runner(without(runner_layers(), root + "@anthropic-ai/claude-agent-sdk-linux-arm64/claude"))
        self.assertFails(image_contents.runner_harness_packages(runner, added), "Claude program is absent")
        runner, added = self.runner(without(runner_layers(), root + "@openai/codex-linux-arm64/vendor/aarch64-unknown-linux-musl/bin/codex"))
        self.assertFails(image_contents.runner_harness_packages(runner, added), "Codex program is absent")

    def test_runner_node(self):
        root = RUNTIME + "node/"
        for name in ("lib/node_modules/npm/bin/npm-cli.js", "lib/node_modules/corepack/dist/corepack.js", "bin/npm", "bin/npx", "bin/corepack"):
            runner, added = self.runner(with_members(runner_layers(), 0, member("file", root + name, b"x")))
            self.assertFails(image_contents.runner_node(runner, added), name.split("/")[-1])
        runner, added = self.runner(without(runner_layers(), root + "bin/node"))
        self.assertFails(image_contents.runner_node(runner, added), "Node.js program is absent")

    def test_runner_build_caches(self):
        for path in ("root/.bun/install/cache/zod", "root/.npm/_cacache/x", "root/.cache/x", "tmp/bun-10002/x", "tmp/culler-node.tar.xz",
                     RUNTIME + "openwiki/.npm/_logs/x"):
            runner, added = self.runner(with_members(runner_layers(), 3, member("file", path, b"x")))
            self.assertFails(image_contents.runner_build_caches(runner, added), path.split("/")[-1])
        runner, added = self.runner(with_members(runner_layers(), 3, member("dir", "tmp/bun-10002")))
        self.assertFails(image_contents.runner_build_caches(runner, added), "Bun transpiler caches")
        runner, added = self.runner(with_members(runner_layers(), 4, member("file", "home/mc-model/.bun/install/cache/@t@/x.pile", b"x")))
        self.assertFails(image_contents.runner_build_caches(runner, added), "x.pile")
        runner, added = self.runner(with_members(runner_layers(), 4, member("file", "home/mc-model/.bash_history", b"x")))
        self.assertEqual([f for f in image_contents.runner_build_caches(runner, added) if "bash_history" in f], [])

    def test_runner_mc_real(self):
        runner, added = self.runner(with_members(runner_layers(), 6, member("file", MC_REAL, WITH_SYMTAB, mode=0o6755, uid=10001, gid=10001)))
        self.assertFails(image_contents.runner_mc_real(runner, added), "appears in 2 layers")
        runner, added = self.runner(with_members(without(runner_layers(), MC_REAL), 5, member("file", MC_REAL, WITH_SYMTAB, mode=0o6755)))
        self.assertFails(image_contents.runner_mc_real(runner, added), "owner 0:0")
        runner, added = self.runner(with_members(without(runner_layers(), MC_REAL), 5, member("file", MC_REAL, WITH_SYMTAB, mode=0o755, uid=10001, gid=10001)))
        self.assertFails(image_contents.runner_mc_real(runner, added), "mode 755")
        runner, added = self.runner(with_members(without(runner_layers(), MC_REAL), 5, member("file", MC_REAL, WITH_DEBUG, mode=0o6755, uid=10001, gid=10001)))
        self.assertFails(image_contents.runner_mc_real(runner, added), ".debug_info")
        runner, added = self.runner(with_members(without(runner_layers(), MC_REAL), 5, member("file", MC_REAL, CLEAN, mode=0o6755, uid=10001, gid=10001)))
        self.assertFails(image_contents.runner_mc_real(runner, added), "no symbol table")
        runner, added = self.runner(without(runner_layers(), MC_REAL))
        self.assertFails(image_contents.runner_mc_real(runner, added), "is absent")

    def test_runner_files_once(self):
        runner, added = self.runner(with_members(runner_layers(), 6, member("file", RUNTIME + "commands/mc", b"sh", mode=0o755)))
        self.assertFails(image_contents.runner_files_once(runner, added), "/opt/culler-runtime/commands/mc (layer 8, layer 10)")
        runner, added = self.runner(with_members(runner_layers(), 6, member("dir", RUNTIME + "commands")))
        self.assertEqual(image_contents.runner_files_once(runner, added), [])

    def test_runner_layer_order(self):
        layers = runner_layers()
        layers[0].append(member("file", RUNTIME + "bun/bin/bun", CLEAN, mode=0o755))
        runner, added = self.runner(layers)
        self.assertFails(image_contents.runner_layer_order(runner, added), "bun/ and app/node_modules/ (layer 4) comes before openwiki/ (layer 6)")
        layers = runner_layers()
        layers[0], layers[1] = layers[1], layers[0]
        runner, added = self.runner(layers)
        self.assertFails(image_contents.runner_layer_order(runner, added), "does not hold node/")
        runner, added = self.runner(without(runner_layers(), RUNTIME + "app/src/index.ts"))
        self.assertFails(image_contents.runner_layer_order(runner, added), "no runtime file under /opt/culler-runtime/app/src/")

    def test_runner_release_layers(self):
        image_contents.RELEASE_LAYER_LIMIT, limit = 1, image_contents.RELEASE_LAYER_LIMIT
        try:
            runner, added = self.runner()
            self.assertFails(image_contents.runner_release_layers(runner, added), "the limit is 0.0 MB")
        finally:
            image_contents.RELEASE_LAYER_LIMIT = limit
        runner, added = self.runner(without(without(without(runner_layers(), MC_REAL), RUNTIME + "release-id"), RUNTIME + "manifest.json"))
        runner.layers[-1].entries = {path: entry for path, entry in runner.layers[-1].entries.items() if entry.kind != "symlink"}
        self.assertFails(image_contents.runner_release_layers(runner, added), "no layer holds")

    def test_runner_history(self):
        history = runner_history()
        history.insert(3, {"created_by": "RUN /bin/sh -c npm ci && chmod -R a+rX /opt /ms-playwright"})
        runner, added = self.runner(history=history)
        self.assertFails(image_contents.runner_history(runner, added), "entry 4 changes the mode of every tool file")
        history[3] = {"created_by": "RUN /bin/sh -c chmod -R a+rX /usr/local/share/mise /opt"}
        runner, added = self.runner(history=history)
        self.assertFails(image_contents.runner_history(runner, added), "entry 4 changes the mode of every tool file")
        # A chmod of one tool directory stores that tool once.
        history[3] = {"created_by": "RUN /bin/sh -c tar -C /opt -xf /tmp/go.tar.gz && chmod -R a+rX /opt/go && rm /tmp/go.tar.gz"}
        runner, added = self.runner(history=history)
        self.assertEqual(image_contents.runner_history(runner, added), [])
        history = runner_history()
        history[-1] = {"created_by": "RUN /bin/sh -c chown 10001:10001 /opt/culler-runtime/libexec/mc-real && chmod 6755 /opt/culler-runtime/libexec/mc-real"}
        runner, added = self.runner(history=history)
        self.assertFails(image_contents.runner_history(runner, added), "entry 10 changes the owner or mode of mc-real")
        history = runner_history()
        history[-2] = {"created_by": "RUN /bin/sh -c cp mc /opt/culler-runtime/libexec/mc-real"}
        runner, added = self.runner(history=history)
        self.assertFails(image_contents.runner_history(runner, added), "no COPY of mc-real")

    def test_run_prints_one_line_per_check(self):
        project = self.archive(project_layers())
        runner = self.archive(project_layers() + runner_layers(), history=runner_history())
        out = io.StringIO()
        self.assertEqual(run(project, runner, out), 0)
        lines = out.getvalue().splitlines()
        self.assertEqual(len(lines), len(image_contents.PROJECT_CHECKS) + len(image_contents.RUNNER_CHECKS))
        self.assertTrue(all(line.startswith("ok ") for line in lines))
        bad = self.archive(with_members(project_layers(), 1, member("file", BROWSER + "resources.pak.info", b"info")))
        out = io.StringIO()
        self.assertEqual(run(bad, runner, out), 1)
        self.assertIn("fail project-pak-info: .pak.info files under /ms-playwright: 1 entries: /" + BROWSER + "resources.pak.info (layer 2)",
                      out.getvalue())


if __name__ == "__main__":
    unittest.main()
