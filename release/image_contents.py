#!/usr/bin/env python3
"""Check the contents of the default Project image and the product image before publication."""

import argparse
import os
import re
import sys

# `python3 -I` omits the script directory from the import path.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from image_archive import compressed_size, read_image  # noqa: E402

RUNTIME = "opt/culler-runtime/"
TOOL_PREFIXES = ("opt/", "ms-playwright/", "usr/local/bin/", "usr/local/libexec/")
GIT_CORE = "usr/local/libexec/git-core/"
BROWSER_FOLDER = "ms-playwright/"
RELEASE_PATHS = (RUNTIME + "libexec/mc-real", RUNTIME + "release-id", RUNTIME + "manifest.json")
RELEASE_LAYER_LIMIT = 50_000_000
LAYER_ORDER = (
    ("node/",),
    ("browser/", "ms-playwright/"),
    ("openwiki/",),
    ("bun/", "app/node_modules/"),
    ("app/src/", "commands/"),
    ("libexec/mc-real", "release-id", "manifest.json"),
)
DEBUG_SECTIONS = (".debug_", ".zdebug_")
OWNER_CHANGE = re.compile(r"\bch(?:own|mod)\b[^&|;]*mc-real")
WHOLE_TREE_CHMOD = re.compile(r"\bchmod -R [^&|;]*\s/opt/?(?=\s|$|[&|;])")
COPY_STEP = re.compile(r"\bCOPY\b")


def under(path, prefix):
    if prefix.endswith("/"):
        return path == prefix[:-1] or path.startswith(prefix)
    return path == prefix or path.startswith(prefix + "/")


def megabytes(value):
    return "{:.1f} MB".format(value / 1e6)


def label(layer):
    return "layer {}".format(layer.index + 1)


def project_elf_paths(path):
    return path.startswith(BROWSER_FOLDER) or path == "usr/local/bin/git" or path.startswith(GIT_CORE)


def project_hash_paths(path):
    return path.startswith(TOOL_PREFIXES)


def runner_elf_paths(path):
    return path.startswith(RUNTIME + BROWSER_FOLDER) or path == RUNTIME + "libexec/mc-real"


def added_layers(runner, project):
    """Return the layers of the product image that the default Project image does not hold."""
    shared = set(project.diff_ids)
    return [layer for layer in runner.layers if layer.diff_id not in shared]


def release_layers(added):
    """Return the layers that hold the files that change at each release."""
    return [layer for layer in added if any(path in layer.entries for path in RELEASE_PATHS)]


def absent(layers, predicate, description, kinds=None, limit=3):
    """Return one failure when an entry of the layers matches the predicate."""
    hits = []
    for layer in layers:
        for entry in layer.entries.values():
            if (kinds is None or entry.kind in kinds) and predicate(entry.path):
                hits.append((entry.path, layer))
    if not hits:
        return []
    hits.sort(key=lambda hit: (hit[0], hit[1].index))
    sample = ", ".join("/{} ({})".format(path, label(layer)) for path, layer in hits[:limit])
    more = "" if len(hits) <= limit else ", and {} more".format(len(hits) - limit)
    return ["{}: {} entries: {}{}".format(description, len(hits), sample, more)]


def present(view, path, kinds, description):
    item = view.get(path)
    if item is None or item[1].kind not in kinds:
        return ["{} is absent: /{}".format(description, path)]
    return []


def debug_sections(entry):
    return [name for name in entry.sections if name.startswith(DEBUG_SECTIONS)]


def browser_programs(layers, root):
    """Return the failures of the ELF files under the browser folder of the layers."""
    programs = []
    for layer in layers:
        for entry in layer.entries.values():
            if entry.kind == "file" and entry.path.startswith(root) and entry.sections is not None:
                programs.append((entry, layer))
    failures = []
    if not any(entry.path.rpartition("/")[2] == "chrome" for entry, _ in programs):
        failures.append("no chrome program under /{}".format(root))
    bad = []
    for entry, layer in programs:
        sections = debug_sections(entry)
        if ".symtab" in entry.sections:
            sections.insert(0, ".symtab")
        if sections:
            bad.append("/{} ({}): {}".format(entry.path, label(layer), ", ".join(sections[:3])))
    if bad:
        more = "" if len(bad) <= 3 else ", and {} more".format(len(bad) - 3)
        failures.append("{} browser programs hold a symbol table or debug sections: {}{}".format(
            len(bad), "; ".join(bad[:3]), more))
    return failures


def project_tool_files_once(project):
    copies = {}
    for layer in project.layers:
        for entry in layer.entries.values():
            if entry.kind == "file" and entry.sha256 and entry.path.startswith(TOOL_PREFIXES):
                copies.setdefault(entry.path, []).append((layer, entry))
    duplicates = sorted(path for path, holders in copies.items() if len(holders) > 1)
    if not duplicates:
        return []
    second = sum(sum(entry.size for _, entry in holders[1:]) for holders in copies.values() if len(holders) > 1)
    layers = [label(layer) for layer in sorted({layer for holders in copies.values() for layer, _ in holders[1:]},
                                               key=lambda layer: layer.index)]
    sample = ", ".join("/" + path for path in duplicates[:3])
    more = "" if len(duplicates) <= 3 else ", and {} more".format(len(duplicates) - 3)
    return ["{} tool files appear in two layers ({} of second copies in {}): {}{}".format(
        len(duplicates), megabytes(second), ", ".join(layers), sample, more)]


def project_pak_info(project):
    return absent(project.layers, lambda path: path.startswith(BROWSER_FOLDER) and path.endswith(".pak.info"),
                  ".pak.info files under /ms-playwright")


def project_git_translations(project):
    return absent(project.layers, lambda path: under(path, "usr/local/share/locale/"),
                  "Git translations under /usr/local/share/locale")


ROOT_FILES = ("root/.bashrc", "root/.profile")
HOME_CACHE = re.compile(r"^home/[^/]+/\.(bun|cache|npm|node)(/|$)")


def build_caches(layers):
    failures = absent(layers, lambda path: path.startswith("root/") and path not in ROOT_FILES,
                      "entries under /root other than .bashrc and .profile")
    failures += absent(layers, lambda path: HOME_CACHE.match(path) is not None, "tool caches under /home")
    failures += absent(layers, lambda path: path.startswith("tmp/"), "files under /tmp", kinds=("file", "link", "symlink"))
    return failures


def project_build_caches(project):
    return build_caches(project.layers)


def project_browser_programs(project):
    return browser_programs(project.layers, BROWSER_FOLDER)


def project_git_programs(project):
    """Return the failures of /usr/local/bin/git and of each ELF file under /usr/local/libexec/git-core."""
    failures = []
    found = False
    bad = []
    for layer in project.layers:
        git = layer.resolve(layer.entries.get("usr/local/bin/git"))
        if git is not None:
            found = True
            if git.sections is None:
                failures.append("/usr/local/bin/git is not an ELF file ({})".format(label(layer)))
        for entry in layer.entries.values():
            if entry.kind != "file" or entry.sections is None:
                continue
            if entry.path == "usr/local/bin/git" or entry.path.startswith(GIT_CORE):
                sections = debug_sections(entry)
                if sections:
                    bad.append("/{} ({}): {}".format(entry.path, label(layer), ", ".join(sections[:4])))
    if not found:
        failures.append("/usr/local/bin/git is absent")
    if bad:
        more = "" if len(bad) <= 3 else ", and {} more".format(len(bad) - 3)
        failures.append("{} Git programs hold debug sections: {}{}".format(len(bad), "; ".join(bad[:3]), more))
    return failures


def project_setuid(project):
    view = project.merged()
    bad = sorted(path for path, (_, entry) in view.items() if entry.kind == "file" and entry.mode & 0o6000)
    if not bad:
        return []
    more = "" if len(bad) <= 5 else ", and {} more".format(len(bad) - 5)
    return ["{} setuid or setgid files remain in the image: {}{}".format(
        len(bad), ", ".join("/" + path for path in bad[:5]), more)]


def runner_base(runner, added):
    if len(added) == len(runner.layers):
        return ["the product image shares no layer with the default Project image"]
    return []


def runner_browser(runner, added):
    root = RUNTIME + BROWSER_FOLDER
    view = runner.merged()
    folders = {path[len(root):].split("/")[0] for path in view if path.startswith(root)}
    failures = []
    for name in ("chromium-", "ffmpeg-"):
        if not any(folder.startswith(name) for folder in folders):
            failures.append("no {}* folder under /{}".format(name, root))
    failures += absent(added, lambda path: path.startswith(root) and path[len(root):].startswith("chromium_headless_shell-"),
                       "headless shell files under /" + root)
    failures += absent(added, lambda path: path.startswith(root) and path.endswith(".pak.info"),
                       ".pak.info files under /" + root)
    failures += browser_programs(added, root)
    return failures


def runner_openwiki(runner, added):
    root = RUNTIME + "openwiki/node_modules/"
    failures = present(runner.merged(), root + "openwiki", ("dir", "symlink"), "the OpenWiki package")
    failures += absent(added, lambda path: path.startswith(root) and path.endswith((".map", ".d.ts", ".d.mts", ".d.cts")),
                       "source maps and type files under /" + root)
    return failures


def runner_harness_packages(runner, added):
    root = RUNTIME + "app/node_modules/"
    view = runner.merged()
    failures = present(view, root + "@anthropic-ai/claude-agent-sdk", ("dir", "symlink"), "the Claude Agent SDK")
    failures += absent(added, lambda path: "node_modules/@opencode/" in path or "node_modules/opencode-websearch/" in path or
                       under(path, "opt/culler-opencode"), "OpenCode programs in the product image")
    failures += present(view, root + "@anthropic-ai/claude-agent-sdk-linux-arm64/claude", ("file", "link"), "the Claude program")
    failures += present(view, root + "@openai/codex-linux-arm64/vendor/aarch64-unknown-linux-musl/bin/codex", ("file", "link"),
                        "the Codex program")
    for name in ("@anthropic-ai/claude-agent-sdk-linux-arm64-musl", "@anthropic-ai/sdk", "@modelcontextprotocol", "zod"):
        failures += absent(added, lambda path, name=name: under(path, root + name), "package {} under /{}".format(name, root))
    return failures


def runner_node(runner, added):
    root = RUNTIME + "node/"
    failures = present(runner.merged(), root + "bin/node", ("file", "link"), "the runtime Node.js program")
    for name in ("lib/node_modules/npm", "lib/node_modules/corepack", "bin/npm", "bin/npx", "bin/corepack"):
        failures += absent(added, lambda path, name=name: under(path, root + name), "/" + root + name)
    return failures


def runner_build_caches(runner, added):
    failures = build_caches(added)
    failures += absent(added, lambda path: path.startswith("tmp/bun-"), "Bun transpiler caches under /tmp")
    failures += absent(added, lambda path: path.startswith(RUNTIME) and ".npm" in path[len(RUNTIME):].split("/"),
                       ".npm folders under /" + RUNTIME)
    return failures


def runner_mc_real(runner, added):
    path = RUNTIME + "libexec/mc-real"
    holders = [(layer, layer.entries[path]) for layer in added if path in layer.entries]
    if not holders:
        return ["/{} is absent".format(path)]
    failures = []
    if len(holders) != 1:
        failures.append("/{} appears in {} layers: {}".format(path, len(holders), ", ".join(label(layer) for layer, _ in holders)))
    layer, entry = holders[-1]
    if entry.kind != "file":
        return failures + ["/{} is not a regular file ({})".format(path, label(layer))]
    if (entry.uid, entry.gid) != (10001, 10001):
        failures.append("/{} has owner {}:{} in {}".format(path, entry.uid, entry.gid, label(layer)))
    if entry.mode != 0o6755:
        failures.append("/{} has mode {:o} in {}".format(path, entry.mode, label(layer)))
    if entry.sections is None:
        failures.append("/{} is not an ELF file".format(path))
    else:
        sections = debug_sections(entry)
        if sections:
            failures.append("/{} holds debug sections: {}".format(path, ", ".join(sections[:4])))
        if ".symtab" not in entry.sections:
            failures.append("/{} holds no symbol table".format(path))
    return failures


def runner_files_once(runner, added):
    holders = {}
    for layer in added:
        for entry in layer.entries.values():
            if entry.kind != "dir" and entry.path.startswith(RUNTIME):
                holders.setdefault(entry.path, []).append((layer, entry))
    duplicates = sorted(path for path, items in holders.items() if len(items) > 1)
    if not duplicates:
        return []
    second = sum(sum(entry.size for _, entry in items[1:]) for items in holders.values() if len(items) > 1)
    sample = ", ".join("/{} ({})".format(path, ", ".join(label(layer) for layer, _ in holders[path])) for path in duplicates[:3])
    more = "" if len(duplicates) <= 3 else ", and {} more".format(len(duplicates) - 3)
    return ["{} runtime files appear in two layers ({} of second copies): {}{}".format(
        len(duplicates), megabytes(second), sample, more)]


def runner_layer_order(runner, added):
    first = {}
    runtime_layers = []
    for layer in added:
        holds_runtime = False
        for entry in layer.entries.values():
            if entry.kind in ("dir", "symlink") or not entry.path.startswith(RUNTIME):
                continue
            holds_runtime = True
            relative = entry.path[len(RUNTIME):]
            for group in LAYER_ORDER:
                for prefix in group:
                    if prefix not in first and under(relative, prefix):
                        first[prefix] = layer
        if holds_runtime:
            runtime_layers.append(layer)
    if not runtime_layers:
        return ["no layer holds a runtime file"]
    failures = []
    if first.get("node/") is not runtime_layers[0]:
        failures.append("the first runtime layer ({}) does not hold node/".format(label(runtime_layers[0])))
    previous = None
    for group in LAYER_ORDER:
        missing = [prefix for prefix in group if prefix not in first]
        if missing:
            failures.append("no runtime file under {}".format(", ".join("/" + RUNTIME + prefix for prefix in missing)))
            continue
        earliest = min((first[prefix] for prefix in group), key=lambda layer: layer.index)
        if previous is not None and earliest.index < previous[1].index:
            failures.append("{} ({}) comes before {} ({})".format(
                " and ".join(group), label(earliest), " and ".join(previous[0]), label(previous[1])))
        latest = max((first[prefix] for prefix in group), key=lambda layer: layer.index)
        if previous is None or latest.index >= previous[1].index:
            previous = (group, latest)
    return failures


def runner_release_layers(runner, added):
    layers = release_layers(added)
    if not layers:
        return ["no layer holds mc-real, the release ID, or the manifest"]
    sizes = [(layer, compressed_size(runner.path, layer)) for layer in layers]
    total = sum(size for _, size in sizes)
    if total < RELEASE_LAYER_LIMIT:
        return []
    detail = ", ".join("{}: {}".format(label(layer), megabytes(size)) for layer, size in sizes)
    return ["the per-release layers compress to {}, the limit is {} ({})".format(
        megabytes(total), megabytes(RELEASE_LAYER_LIMIT), detail)]


def runner_history(runner, added):
    history = runner.history()
    failures = []
    for index, text in enumerate(history):
        if WHOLE_TREE_CHMOD.search(text):
            failures.append("history entry {} changes the mode of every tool file".format(index + 1))
    copies = [index for index, text in enumerate(history) if "mc-real" in text and COPY_STEP.search(text)]
    if not copies:
        return failures + ["the history holds no COPY of mc-real"]
    for index in range(copies[-1] + 1, len(history)):
        if OWNER_CHANGE.search(history[index]):
            failures.append("history entry {} changes the owner or mode of mc-real after its COPY".format(index + 1))
    return failures


def project_tool_access(project):
    """Return one failure when the agent account cannot read a tool file or enter a tool folder."""
    view = project.merged()
    bad = []
    for path, (_, entry) in sorted(view.items()):
        if not any(under(path, prefix) for prefix in TOOL_PREFIXES):
            continue
        if entry.kind == "dir" and entry.mode & 0o005 != 0o005:
            bad.append(path)
        elif entry.kind == "file" and entry.mode & 0o004 == 0:
            bad.append(path)
    if not bad:
        return []
    more = "" if len(bad) <= 5 else ", and {} more".format(len(bad) - 5)
    return ["the agent account cannot read {} tool entries: {}{}".format(
        len(bad), ", ".join("/" + path for path in bad[:5]), more)]


PROJECT_CHECKS = (
    ("project-tool-files-once", project_tool_files_once),
    ("project-pak-info", project_pak_info),
    ("project-git-translations", project_git_translations),
    ("project-build-caches", project_build_caches),
    ("project-browser-programs", project_browser_programs),
    ("project-git-programs", project_git_programs),
    ("project-setuid", project_setuid),
    ("project-tool-access", project_tool_access),
)
RUNNER_CHECKS = (
    ("runner-base", runner_base),
    ("runner-browser", runner_browser),
    ("runner-openwiki", runner_openwiki),
    ("runner-harness-packages", runner_harness_packages),
    ("runner-node", runner_node),
    ("runner-build-caches", runner_build_caches),
    ("runner-mc-real", runner_mc_real),
    ("runner-files-once", runner_files_once),
    ("runner-layer-order", runner_layer_order),
    ("runner-release-layers", runner_release_layers),
    ("runner-history", runner_history),
)


def run(project_path, runner_path, out=sys.stdout):
    """Print one line per check and return the exit status."""
    project = read_image(project_path, hash_paths=project_hash_paths, elf_paths=project_elf_paths)
    runner = read_image(runner_path, elf_paths=runner_elf_paths)
    added = added_layers(runner, project)
    status = 0
    results = [(name, check(project)) for name, check in PROJECT_CHECKS]
    results += [(name, check(runner, added)) for name, check in RUNNER_CHECKS]
    for name, failures in results:
        if failures:
            status = 1
            for failure in failures:
                print("fail {}: {}".format(name, failure), file=out)
        else:
            print("ok {}".format(name), file=out)
    return status


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", required=True, help="the archive of the default Project image from `docker image save`")
    parser.add_argument("--runner", required=True, help="the archive of the product image from `docker image save`")
    args = parser.parse_args(argv)
    return run(args.project, args.runner)


if __name__ == "__main__":
    raise SystemExit(main())
