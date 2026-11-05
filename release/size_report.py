#!/usr/bin/env python3
"""Write the download and disk sizes of a fresh install and an update from the release image archives."""

import argparse
import os
import sys

# `python3 -I` omits the script directory from the import path.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from image_archive import compressed_sizes, read_image  # noqa: E402
from image_contents import RUNTIME, added_layers, release_layers  # noqa: E402

BASELINE_RELEASE = "9055488a"
BASELINE = {
    "fresh_download": 2_200_000_000,
    "fresh_disk": 8_630_000_000,
    "update_download": 2_060_000_000,
    "update_disk": 5_810_000_000,
}
BASELINE_HOST_FILES = 170_000_000
VALUES = (
    ("fresh_download", "Fresh install download"),
    ("fresh_disk", "Fresh install disk"),
    ("update_download", "Update download"),
    ("update_disk", "Update disk"),
)


def gb(value):
    return "{:.2f} GB ({} bytes)".format(value / 1e9, value)


def signed(value):
    return "{:+.2f} GB ({:+d} bytes)".format(value / 1e9, value)


def runtime_volume(runner):
    """Return the bytes of the runtime volume. A hard-linked file counts once."""
    view = runner.merged()
    return sum(entry.size for path, (_, entry) in view.items() if entry.kind == "file" and path.startswith(RUNTIME))


class Measure:
    """The sizes of one pair of image archives."""

    def __init__(self, project, runner, compressed):
        self.project = project
        self.runner = runner
        self.compressed = compressed
        self.added = added_layers(runner, project)
        self.release = release_layers(self.added)
        self.volume = runtime_volume(runner)

    def size(self, layers):
        return sum(layer.size for layer in layers)

    def gzip(self, layers):
        """Return the download size of the layers. A pull fetches a repeated layer once."""
        return sum(self.compressed[diff_id] for diff_id in {layer.diff_id for layer in layers})

    def values(self):
        base = self.project.layers[:1]
        return {
            "fresh_download": self.gzip(self.project.layers) + self.gzip(self.added),
            "fresh_disk": self.size(self.project.layers) + self.size(self.added) + self.volume,
            "update_download": self.gzip(self.project.layers) - self.gzip(base) + self.gzip(self.added),
            "update_disk": self.size(self.project.layers) - self.size(base) + self.size(self.added),
        }


def changed_layers(current, previous):
    """Return the layers of the current pair that the previous pair does not hold."""
    known = set(previous.project.diff_ids) | set(previous.runner.diff_ids)
    layers = list(current.project.layers) + list(current.added)
    return [layer for layer in layers if layer.diff_id not in known]


def table(rows):
    return "\n".join("| " + " | ".join(row) + " |" for row in rows)


def report(current, previous=None):
    values = current.values()
    lines = ["# Size report", ""]
    lines.append("The report reads `{}` (default Project image, {} layers) and `{}` (product image, {} layers).".format(
        current.project.path, len(current.project.layers), current.runner.path, len(current.runner.layers)))
    lines.append("The product image adds {} layers above the default Project image.".format(len(current.added)))
    lines.append("The registry stores each layer with gzip compression. The compressed size of a layer is the gzip level 6 size of its tar. "
                 "A pull fetches a repeated layer once, so a download size counts it once.")
    lines.append("The runtime volume size is the sum of the regular files under `/{}`. A hard-linked file counts once.".format(RUNTIME))
    lines += ["", "## Images", ""]
    lines.append(table([
        ("Image", "Layers", "Uncompressed", "Compressed"),
        ("---", "---", "---", "---"),
        ("Default Project image", str(len(current.project.layers)),
         gb(current.size(current.project.layers)), gb(current.gzip(current.project.layers))),
        ("Product image", str(len(current.runner.layers)),
         gb(current.size(current.runner.layers)), gb(current.gzip(current.runner.layers))),
        ("Product image layers above the default Project image", str(len(current.added)),
         gb(current.size(current.added)), gb(current.gzip(current.added))),
        ("Per-release layers of the product image", str(len(current.release)),
         gb(current.size(current.release)), gb(current.gzip(current.release))),
    ]))
    lines += ["", "## Fresh install", ""]
    lines.append(table([
        ("Part", "Download", "Disk"),
        ("---", "---", "---"),
        ("Default Project image", gb(current.gzip(current.project.layers)), gb(current.size(current.project.layers))),
        ("Product image layers above the default Project image", gb(current.gzip(current.added)), gb(current.size(current.added))),
        ("Runtime volume", gb(0), gb(current.volume)),
        ("Total", gb(values["fresh_download"]), gb(values["fresh_disk"])),
    ]))
    lines.append("")
    lines.append("The host files are outside the image archives. The totals exclude them.")
    lines += ["", "## Update", ""]
    rows = [
        ("Case", "Download", "Disk added"),
        ("---", "---", "---"),
        ("Every built layer changed; the base image layer stays", gb(values["update_download"]), gb(values["update_disk"])),
        ("Only the per-release layers changed", gb(current.gzip(current.release)), gb(current.size(current.release))),
    ]
    if previous is not None:
        changed = changed_layers(current, previous)
        rows.append(("Layers that differ from the previous archives ({} layers)".format(len(changed)),
                     gb(current.gzip(changed)), gb(current.size(changed))))
    lines.append(table(rows))
    lines.append("")
    lines.append("The disk added counts the new layers while the previous images stay on disk. "
                 "The runtime volume install replaces the volume content in place.")
    lines += ["", "## Comparison with the baseline", ""]
    if previous is None:
        baseline = dict(BASELINE)
        baseline["fresh_disk"] -= BASELINE_HOST_FILES
        lines.append("The baseline is release {}. Its fresh install disk of {} includes {} of host files. "
                     "The comparison uses the value without them.".format(
                         BASELINE_RELEASE, gb(BASELINE["fresh_disk"]), gb(BASELINE_HOST_FILES)))
    else:
        baseline = previous.values()
        lines.append("The baseline is the previous archives `{}` and `{}`.".format(previous.project.path, previous.runner.path))
    lines.append("")
    rows = [("Value", "Baseline", "Current", "Difference"), ("---", "---", "---", "---")]
    for key, name in VALUES:
        rows.append((name, gb(baseline[key]), gb(values[key]), signed(values[key] - baseline[key])))
    lines.append(table(rows))
    lines.append("")
    return "\n".join(lines)


def measure(project_path, runner_path, workers):
    project = read_image(project_path)
    runner = read_image(runner_path)
    return Measure(project, runner, compressed_sizes((project, runner), workers))


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", required=True, help="the archive of the default Project image from `docker image save`")
    parser.add_argument("--runner", required=True, help="the archive of the product image from `docker image save`")
    parser.add_argument("--previous-project", help="the archive of the previous default Project image")
    parser.add_argument("--previous-runner", help="the archive of the previous product image")
    parser.add_argument("--output", required=True, help="the Markdown report path")
    parser.add_argument("--workers", type=int, default=4, help="the number of parallel compression threads")
    args = parser.parse_args(argv)
    if (args.previous_project is None) != (args.previous_runner is None):
        parser.error("--previous-project and --previous-runner go together.")
    current = measure(args.project, args.runner, args.workers)
    previous = None
    if args.previous_project is not None:
        previous = measure(args.previous_project, args.previous_runner, args.workers)
    text = report(current, previous)
    with open(args.output, "w", encoding="utf-8") as handle:
        handle.write(text)
    print(text, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
