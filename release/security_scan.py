#!/usr/bin/env python3
"""Check source history, release files, and distributed images."""

import argparse
import copy
import datetime
import hashlib
import io
import json
import os
import re
import subprocess
import sys
import tarfile
import tempfile
import zipfile
from pathlib import Path

from security_content import Content, digest
from security_policy import EXCEPTIONS, apply_exceptions, load_exceptions
from security_tools import PINS


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def execute(command, cwd=None, allowed=(0,)):
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(("GITLEAKS_", "TRIVY_", "GIT_")) and key not in {"GH_TOKEN", "GITHUB_TOKEN"}}
    env.update(GIT_NO_REPLACE_OBJECTS="1", GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
    result = subprocess.run(command, cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=3600)
    if result.returncode not in allowed:
        # Scanner output can contain source lines or environment values.
        raise ValueError("A required command failed; its output remains private.")
    return result


def history_objects(repo, refs, scanners):
    objects = execute(["git", "-C", str(repo), "rev-list", "--objects", *refs]).stdout
    object_ids = [line.split(b" ", 1)[0] for line in objects.splitlines()]
    requests = scanners.scratch / "git-objects"
    requests.write_bytes(b"\n".join(object_ids) + b"\n")
    content = Content(scanners.scratch / "history-objects")
    binary_count = 0
    # Git patches omit binary contents. Read those blobs from the original object graph.
    with requests.open("rb") as source, (scanners.scratch / "git-errors").open("wb") as errors:
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(GIT_NO_REPLACE_OBJECTS="1", GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
        process = subprocess.Popen(["git", "-C", str(repo), "cat-file", "--batch"], stdin=source,
                                   stdout=subprocess.PIPE, stderr=errors, env=env)
        try:
            for expected in object_ids:
                header = process.stdout.readline().split()
                if len(header) != 3 or header[0] != expected:
                    raise ValueError("The history scan omitted a required Git object.")
                size = int(header[2])
                if size > 512 * 1024**2:
                    raise ValueError("A Git object exceeds the scan limit.")
                body = process.stdout.read(size)
                if len(body) != size or process.stdout.read(1) != b"\n":
                    raise ValueError("The history scan received an incomplete Git object.")
                oid = expected.decode()
                if header[1] == b"commit":
                    content.add(io.BytesIO(body), {"file": "commit-message", "commit": oid, "subject": "commit:" + oid})
                elif header[1] == b"blob" and (b"\0" in body[:8000] or body.startswith((b"PK\x03\x04", b"\x1f\x8b", b"BZh", b"\xfd7zXZ"))):
                    candidates = execute(["git", "-C", str(repo), "log", "--format=%H", "--find-object=" + oid,
                                          "--no-patch", "--root", *refs]).stdout.decode().splitlines()
                    location = None
                    for commit in reversed(candidates):
                        tree = execute(["git", "-C", str(repo), "ls-tree", "-r", "-z", commit]).stdout
                        matches = [entry.split(b"\t", 1)[1] for entry in tree.split(b"\0")
                                   if b"\t" in entry and entry.split(b"\t", 1)[0].split()[-1] == expected]
                        if matches:
                            location = {"file": matches[0].decode("utf-8", "replace"), "commit": commit,
                                        "subject": "commit:" + commit, "blob": oid}
                            break
                    if location is None:
                        raise ValueError("The history scan cannot locate a binary object in its commit.")
                    content.add(io.BytesIO(body), location)
                    binary_count += 1
            if process.wait(timeout=30) != 0:
                raise ValueError("The history object check failed.")
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
    scanners.content(content)
    return binary_count


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
        self.checked = set()

    def tool(self, name):
        self.report["stage"] = "Verify the " + name + " executable."
        path = self.tools / name
        if name not in self.checked:
            result = execute([str(path), "version" if name == "gitleaks" else "--version"])
            version = self.pins[name]["version"]
            if not re.search(r"(?<![0-9.])" + re.escape(version) + r"(?![0-9.])", result.stdout.decode()):
                raise ValueError("The scanner version differs from its pin.")
            self.report["scanners"][name] = {"version": version, "executable_sha256": digest(path)}
            self.checked.add(name)
        return str(path)

    def gitleaks(self, arguments, locations=None):
        raw = self.scratch / "gitleaks.json"
        raw.unlink(missing_ok=True)
        config = self.tools / "gitleaks.toml"
        binary = self.tool("gitleaks")
        self.report["stage"] = "Check secrets with Gitleaks."
        result = execute([binary, *arguments, "--config", str(config),
                          "--gitleaks-ignore-path", str(self.empty), "--ignore-gitleaks-allow",
                          "--redact=100", "--no-banner", "--no-color", "--log-level=error",
                          "--max-target-megabytes=0", "--max-decode-depth=5", "--max-archive-depth=0",
                          "--report-format=json", "--report-path", str(raw)],
                         cwd=self.scratch, allowed=(0, 1))
        findings = json.loads(raw.read_text())
        if not isinstance(findings, list) or (result.returncode == 1 and not findings) or result.stderr:
            raise ValueError("Gitleaks did not complete the required coverage.")
        self.report["scanners"]["gitleaks"]["rules_sha256"] = digest(config)
        for item in findings:
            location = locations[Path(item["File"]).name] if locations is not None else {
                "file": item["File"], "subject": "commit:" + item["Commit"], "commit": item["Commit"]}
            self.report["secrets"].append({**location, "scanner": "gitleaks",
                                           "rule": item["RuleID"], "line": item["StartLine"]})
        raw.unlink()

    def content(self, content):
        if content.locations:
            self.gitleaks(["dir", str(content.root)], content.locations)
        return {"files": len(content.locations), "bytes": content.bytes}

    def trivy(self, archive, image, vulnerabilities=True):
        raw = self.scratch / "trivy.json"
        cache = self.scratch / "trivy-cache"
        command = [self.tool("trivy"), "image", "--config", str(self.config),
                   "--cache-dir", str(cache), "--quiet", "--timeout=45m"]
        if vulnerabilities:
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
        args = [*command, "--input", str(archive), "--scanners", "vuln,secret" if vulnerabilities else "secret",
                "--image-config-scanners=secret", "--secret-config=", "--ignorefile", str(self.empty),
                "--format=json", "--output", str(raw), "--exit-code=0", "--list-all-pkgs",
                "--detection-priority=comprehensive"]
        if vulnerabilities:
            args.extend(["--skip-db-update", "--removed-pkgs"])
        self.report["stage"] = "Check the image with Trivy."
        result = execute(args, cwd=self.scratch)
        if result.stderr:
            raise ValueError("Trivy reported incomplete coverage; its output remains private.")
        data = json.loads(raw.read_text())
        consume_trivy(data, self.report, image, vulnerabilities, str(archive))
        java_metadata = cache / "java-db/metadata.json"
        if java_metadata.exists():
            self.report["java_advisory_data"] = {"retrieved_at": now(), "metadata": json.loads(java_metadata.read_text()),
                                                 "sha256": digest(cache / "java-db/trivy-java.db")}
        raw.unlink()


def consume_trivy(data, report, image, vulnerabilities=True, input_path=None):
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
        for item in group.get("Secrets", []):
            report["secrets"].append({"scanner": "trivy", "subject": report["image_id"],
                                      "file": target, "rule": item["RuleID"], "line": item["StartLine"]})
        for item in group.get("Packages", []):
            packages.append({"target": target, "type": group["Type"], "package": item["Name"],
                             "version": item.get("Version", ""), "identifier": item.get("Identifier", {})})
        for item in group.get("Vulnerabilities", []):
            report["vulnerabilities"].append({"image": image, "target": target, "package": item["PkgName"],
                                              "version": item["InstalledVersion"], "advisory": item["VulnerabilityID"],
                                              "severity": item["Severity"], "fixed_version": item.get("FixedVersion", "")})
    report["packages"] = packages
    if vulnerabilities and (not packages or not any(group.get("Class") == "os-pkgs" for group in results)):
        raise ValueError("Trivy omitted the installed package inventory.")
    if any(not item["version"] for item in packages):
        raise ValueError("An installed package lacks an identified version.")
    if data.get("Metadata", {}).get("OS", {}).get("EOSL"):
        raise ValueError("The image operating system lacks current advisory coverage.")


def history(args, scanners, report):
    repo = args.repo.resolve()
    def git(*values):
        return execute(["git", "-C", str(repo), *values]).stdout.decode().strip()
    if git("rev-parse", "--is-shallow-repository") != "false":
        raise ValueError("The history scan requires the complete repository history.")
    grafts = Path(git("rev-parse", "--git-path", "info/grafts"))
    if not grafts.is_absolute():
        grafts = repo / grafts
    if grafts.exists() and grafts.stat().st_size:
        raise ValueError("The history scan requires the original commit graph.")
    refs = list(args.ref or [])
    if args.public_refs:
        refs.extend(git("for-each-ref", "--format=%(refname)", "refs/remotes/origin", "refs/tags").splitlines())
        refs.append("HEAD")
    if not refs:
        raise ValueError("Name every branch and tag intended for publication.")
    resolved = {}
    for ref in refs:
        if not re.fullmatch(r"[A-Za-z0-9_./-]+", ref) or ref.startswith("-"):
            raise ValueError("The public ref is invalid.")
        resolved[ref] = git("rev-parse", "--verify", ref + "^{commit}")
    commits = git("rev-list", *sorted(set(resolved.values()))).splitlines()
    if not commits:
        raise ValueError("The history scan selected zero commits.")
    report.update(refs=resolved, commits=commits)
    scanners.gitleaks(["git", str(repo), "--log-opts=--full-history -m --text --no-textconv " + " ".join(sorted(set(resolved.values())))])
    binary_count = history_objects(repo, sorted(set(resolved.values())), scanners)
    report["coverage"] = {"commits": len(commits), "binary_blobs": binary_count, "commit_messages": True,
                          "deleted_files": True, "merge_diffs": True}


def file_inputs(directory):
    found = {}
    for path in sorted(directory.rglob("*")):
        if path.is_symlink() or not (path.is_dir() or path.is_file()):
            raise ValueError("Release inputs must contain regular files and directories.")
        if path.is_file():
            found[path.relative_to(directory).as_posix()] = digest(path)
    if not found:
        raise ValueError("The release file scan selected zero files.")
    return found


def files(args, scanners, report):
    directory = args.directory.resolve()
    before = file_inputs(directory)
    content = Content(scanners.scratch / "files")
    for name, checksum in before.items():
        with (directory / name).open("rb") as stream:
            content.add(stream, {"file": name, "subject": "sha256:" + checksum})
    report["inputs"] = before
    report["coverage"] = scanners.content(content)
    if file_inputs(directory) != before:
        raise ValueError("The release files changed during the scan.")


def image(args, scanners, report):
    archive_path = args.archive.resolve()
    before = digest(archive_path)
    report.update(archive_sha256=before, image=args.image,
                  scan_mode="vulnerabilities" if args.vulnerabilities_only else "release")
    coverage = []
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
        metadata = Content(scanners.scratch / "metadata")
        metadata.add(io.BytesIO(config_body), {"file": "image-config.json", "subject": report["image_id"]})
        metadata.add(io.BytesIO(json.dumps(manifest).encode()), {"file": "manifest.json", "subject": report["image_id"]})
        coverage.append({"metadata": scanners.content(metadata)})
        for index, layer in enumerate(layers):
            with tempfile.TemporaryDirectory(dir=scanners.scratch) as directory:
                content = Content(Path(directory) / "files")
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
                        location = {"file": name, "subject": report["image_id"] + "/" + diff_ids[index], "layer": diff_ids[index]}
                        if member.isfile():
                            body = contents.extractfile(member)
                            if name in wanted:
                                value = body.read()
                                records[name] = value.decode()
                                body = io.BytesIO(value)
                            if not args.vulnerabilities_only:
                                content.add(body, location)
                        elif member.issym() or member.islnk():
                            if name in wanted:
                                raise ValueError("A bundled version record requires a regular file.")
                            if not args.vulnerabilities_only:
                                content.add(io.BytesIO(member.linkname.encode()), location)
                        elif not (member.isdir() or member.isdev() or member.isfifo()):
                            raise ValueError("An image layer contains an unsupported file type.")
                coverage.append({"layer": diff_ids[index], **scanners.content(content)})
    report["coverage"] = coverage
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


def verify_files(directory, record, commit):
    if record.get("status") != "passed" or record.get("kind") != "files" or record.get("commit") != commit:
        raise ValueError("The release requires a successful file scan for its source commit.")
    if record.get("inputs") != file_inputs(directory):
        raise ValueError("The release files differ from the scanned files.")
    if not apply_exceptions(copy.deepcopy(record), load_exceptions()):
        raise ValueError("The release file results include an unresolved secret.")


def verify_image(archive, record, commit):
    if record.get("status") != "passed" or record.get("kind") != "image" or record.get("commit") != commit or record.get("scan_mode") != "release":
        raise ValueError("The release requires a successful image scan for its source commit.")
    if record.get("archive_sha256") != digest(archive):
        raise ValueError("The image archive differs from the scanned image archive.")
    if not apply_exceptions(copy.deepcopy(record), load_exceptions()):
        raise ValueError("An image result lacks a current exception.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=("history", "files", "image"))
    parser.add_argument("--tools", required=True, type=Path)
    parser.add_argument("--report", required=True, type=Path)
    parser.add_argument("--commit", default="")
    parser.add_argument("--exceptions", type=Path, default=EXCEPTIONS)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--ref", action="append")
    parser.add_argument("--public-refs", action="store_true")
    parser.add_argument("--directory", type=Path)
    parser.add_argument("--archive", type=Path)
    parser.add_argument("--image", choices=("project", "runner"))
    parser.add_argument("--vulnerabilities-only", action="store_true")
    args = parser.parse_args()
    report = {"format": 1, "kind": args.kind, "commit": args.commit, "started_at": now(),
              "status": "unverified", "scanners": {}, "secrets": [], "vulnerabilities": []}
    code = 2
    try:
        exceptions = load_exceptions(args.exceptions)
        with tempfile.TemporaryDirectory(prefix="culler-security-") as directory:
            scanners = Scanners(args.tools, Path(directory), report)
            {"history": history, "files": files, "image": image}[args.kind](args, scanners, report)
        code = 0 if apply_exceptions(report, exceptions) else 1
        report["stage"] = "The required scan completed."
    except (ValueError, OSError, KeyError, TypeError, AttributeError, EOFError, RuntimeError,
            tarfile.TarError, zipfile.BadZipFile, subprocess.SubprocessError):
        report["error"] = "A required tool, input, or coverage check failed. The check remains unverified."
    report["completed_at"] = now()
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, indent=2) + "\n")
    print("The " + args.kind + " security check is " + report["status"] + ".")
    return code


if __name__ == "__main__":
    sys.exit(main())
