#!/usr/bin/env python3
"""Report the vulnerability results of a release image."""

import argparse
import datetime
import hashlib
import json
import os
import re
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

from security_tools import PINS

SEVERITIES = ("CRITICAL", "HIGH", "MEDIUM", "LOW", "UNKNOWN")


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def execute(command, cwd=None):
    env = {key: value for key, value in os.environ.items()
           if not key.startswith("TRIVY_") and key not in {"GH_TOKEN", "GITHUB_TOKEN"}}
    result = subprocess.run(command, cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=3600)
    if result.returncode != 0:
        # Scanner output can contain environment values.
        raise ValueError("A required command failed; its output remains private.")
    return result


class Scanners:
    def __init__(self, tools, scratch, report):
        self.tools = tools.resolve()
        self.scratch = scratch
        self.report = report
        self.pins = json.loads(PINS.read_text())
        self.empty = scratch / "empty"
        self.empty.write_text("")
        self.config = scratch / "trivy.yaml"
        self.config.write_text("{}\n")
        self.checked = False

    def tool(self):
        self.report["stage"] = "Verify the trivy executable."
        path = self.tools / "trivy"
        if not self.checked:
            result = execute([str(path), "--version"])
            version = self.pins["trivy"]["version"]
            if not re.search(r"(?<![0-9.])" + re.escape(version) + r"(?![0-9.])", result.stdout.decode()):
                raise ValueError("The scanner version differs from its pin.")
            self.report["scanners"]["trivy"] = {"version": version, "executable_sha256": digest(path)}
            self.checked = True
        return str(path)

    def trivy(self, archive, image):
        raw = self.scratch / "trivy.json"
        cache = self.scratch / "trivy-cache"
        command = [self.tool(), "image", "--config", str(self.config),
                   "--cache-dir", str(cache), "--quiet", "--timeout=45m"]
        self.report["stage"] = "Retrieve current advisory data."
        execute([*command, "--download-db-only"], cwd=self.scratch)
        metadata = json.loads((cache / "db/metadata.json").read_text())
        retrieved = now()
        updated = datetime.datetime.fromisoformat(metadata["UpdatedAt"].replace("Z", "+00:00"))
        age = datetime.datetime.now(datetime.timezone.utc) - updated
        if age < datetime.timedelta(minutes=-5) or age > datetime.timedelta(days=2):
            raise ValueError("The vulnerability advisory data is stale.")
        self.report["advisory_data"] = {"retrieved_at": retrieved, "metadata": metadata,
                                        "sha256": digest(cache / "db/trivy.db")}
        args = [*command, "--input", str(archive), "--scanners", "vuln", "--ignorefile", str(self.empty),
                "--format=json", "--output", str(raw), "--exit-code=0", "--list-all-pkgs",
                "--detection-priority=comprehensive", "--skip-db-update", "--removed-pkgs"]
        self.report["stage"] = "Check the image with Trivy."
        result = execute(args, cwd=self.scratch)
        if result.stderr:
            raise ValueError("Trivy reported incomplete coverage; its output remains private.")
        data = json.loads(raw.read_text())
        consume_trivy(data, self.report, image, str(archive))
        java_metadata = cache / "java-db/metadata.json"
        if java_metadata.exists():
            self.report["java_advisory_data"] = {"retrieved_at": now(), "metadata": json.loads(java_metadata.read_text()),
                                                 "sha256": digest(cache / "java-db/trivy-java.db")}
        raw.unlink()


def consume_trivy(data, report, image, input_path=None):
    if data.get("SchemaVersion") != 2 or data.get("Metadata", {}).get("ImageID") != report["image_id"]:
        raise ValueError("Trivy did not identify the exact image.")
    results = data.get("Results")
    if not isinstance(results, list):
        raise ValueError("Trivy omitted its result groups.")
    packages = []
    for group in results:
        target = group["Target"]
        if group.get("Class") == "os-pkgs":
            target = "/"
        elif input_path and target.startswith(input_path):
            target = "image-config" + target[len(input_path):]
        for item in group.get("Packages", []):
            packages.append({"target": target, "type": group["Type"], "package": item["Name"],
                             "version": item.get("Version", ""), "identifier": item.get("Identifier", {})})
        for item in group.get("Vulnerabilities", []):
            report["vulnerabilities"].append({"image": image, "target": target, "package": item["PkgName"],
                                              "version": item["InstalledVersion"], "advisory": item["VulnerabilityID"],
                                              "severity": item["Severity"], "fixed_version": item.get("FixedVersion", "")})
    if not packages or not any(group.get("Class") == "os-pkgs" for group in results):
        raise ValueError("Trivy omitted the installed package inventory.")
    report["packages"] = packages
    # Trivy finds no advisories for a package without a version.
    report["packages_without_version"] = sorted({item["target"] + " " + item["package"]
                                                 for item in packages if not item["version"]})
    report["operating_system"] = data.get("Metadata", {}).get("OS", {})


def image(args, scanners, report):
    archive_path = args.archive.resolve()
    before = digest(archive_path)
    report.update(archive_sha256=before, image=args.image)
    records = {}
    wanted = {"usr/share/culler-project/manifest.json", "opt/culler-runtime/manifest.json", "opt/culler-runtime/artifacts.txt"}
    with tarfile.open(archive_path) as archive:
        manifest = json.load(archive.extractfile("manifest.json"))
        if len(manifest) != 1 or not manifest[0].get("Layers"):
            raise ValueError("The archive must contain one image with retained layers.")
        config_body = archive.extractfile(manifest[0]["Config"]).read()
        config = json.loads(config_body)
        report["image_id"] = "sha256:" + hashlib.sha256(config_body).hexdigest()
        if config.get("os") != "linux" or config.get("architecture") != "arm64":
            raise ValueError("The release image requires Linux arm64.")
        if report.get("commit") and config.get("config", {}).get("Labels", {}).get("org.opencontainers.image.revision") != report["commit"]:
            raise ValueError("The image revision differs from the source commit of the scan.")
        layers = manifest[0]["Layers"]
        diff_ids = config["rootfs"]["diff_ids"]
        if len(layers) != len(diff_ids):
            raise ValueError("The image configuration omits a retained layer.")
        # The layers supply the version records of the bundled tools.
        for index, layer in enumerate(layers):
            with archive.extractfile(layer) as stream:
                checksum = hashlib.file_digest(stream, "sha256").hexdigest()
            if "sha256:" + checksum != diff_ids[index]:
                raise ValueError("A retained layer differs from the image configuration.")
            with tarfile.open(fileobj=archive.extractfile(layer), mode="r|*") as contents:
                for member in contents:
                    name = member.name.removeprefix("./").lstrip("/")
                    if "/.wh." in name or name.startswith(".wh."):
                        parent, _, leaf = name.rpartition("/")
                        prefix = (parent + "/") if parent else ""
                        if leaf == ".wh..wh..opq":
                            records = {key: value for key, value in records.items() if not key.startswith(prefix)}
                        else:
                            records.pop(prefix + leaf.removeprefix(".wh."), None)
                    if name not in wanted:
                        continue
                    if not member.isfile():
                        raise ValueError("A bundled version record requires a regular file.")
                    records[name] = contents.extractfile(member).read().decode()
    report["layers"] = diff_ids
    scanners.trivy(archive_path, args.image)
    report["bundled_tools"] = bundled_tools(records, args.image)
    if digest(archive_path) != before:
        raise ValueError("The image archive changed during the scan.")


def bundled_tools(records, image_name):
    project = json.loads(records["usr/share/culler-project/manifest.json"])
    tools = dict(project["tools"])
    tools.update(project["browser_versions"])
    if image_name == "runner":
        runtime = json.loads(records["opt/culler-runtime/manifest.json"])
        tools.update({"runtime/" + name: version for name, version in runtime["tools"].items()})
        tools["culler"] = runtime["release"]
    if not tools or any(not isinstance(value, str) or not (re.search(r"\d", value) or re.fullmatch(r"[0-9a-f]{40}", value))
                        for value in tools.values()):
        raise ValueError("A bundled tool lacks its explicit version record.")
    # A version record supports assessment. It does not establish scanner advisory coverage.
    return [{"tool": name, "version_record": version, "coverage": "version record; assess separately"}
            for name, version in sorted(tools.items())]


def cell(value):
    return str(value).replace("\\", "\\\\").replace("|", "\\|").replace("\n", " ")


def summary(report):
    lines = ["## Vulnerability scan of the " + report.get("image", "") + " image", ""]
    if report["status"] != "complete":
        lines.append("The scan did not complete. The last stage was: " + report.get("stage", "the start") +
                     " The report artifact keeps the record.")
        return "\n".join(lines) + "\n"
    results = report["vulnerabilities"]
    lines += ["- Packages: " + str(len(report["packages"])), "- Trivy vulnerability results: " + str(len(results)),
              "- Advisory data update: " + report["advisory_data"]["metadata"]["UpdatedAt"]]
    lines += ["", "| Severity | Results | Results with a fixed version |", "| --- | --- | --- |"]
    for severity in SEVERITIES:
        matched = [item for item in results if item["severity"] == severity]
        lines.append("| " + severity + " | " + str(len(matched)) + " | " +
                     str(sum(1 for item in matched if item["fixed_version"])) + " |")
    if report["packages_without_version"]:
        lines += ["", "Trivy found no version for these packages, so it checked no advisories for them:", ""]
        lines += ["- `" + cell(name) + "`" for name in report["packages_without_version"]]
    if report["operating_system"].get("EOSL"):
        lines += ["", "The image operating system is past its end of support, so its advisory data can be incomplete."]
    lines += ["", "The report artifact lists each result and each package."]
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tools", required=True, type=Path)
    parser.add_argument("--report", required=True, type=Path)
    parser.add_argument("--commit", default="")
    parser.add_argument("--archive", required=True, type=Path)
    parser.add_argument("--image", required=True, choices=("project", "runner"))
    args = parser.parse_args()
    report = {"format": 3, "kind": "image", "commit": args.commit, "started_at": now(),
              "status": "unverified", "scanners": {}, "vulnerabilities": []}
    code = 2
    try:
        with tempfile.TemporaryDirectory(prefix="culler-security-") as directory:
            image(args, Scanners(args.tools, Path(directory), report), report)
        report["status"] = "complete"
        report["stage"] = "The scan completed."
        code = 0
    except (ValueError, OSError, KeyError, TypeError, AttributeError, EOFError, RuntimeError,
            tarfile.TarError, subprocess.SubprocessError):
        report["error"] = "A required tool, input, or coverage check failed. The scan remains unverified."
    report["completed_at"] = now()
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, indent=2) + "\n")
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as stream:
            stream.write(summary(report))
    print("The image vulnerability scan is " + report["status"] + ".")
    return code


if __name__ == "__main__":
    sys.exit(main())
