"""Prepare and verify the OpenCode package for a source deployment."""
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import stat
import subprocess
import sys
import tarfile


def digest_entries(entries):
    digest = hashlib.sha256()
    for name, mode, kind, value in sorted(entries):
        digest.update(f"{name}\0{mode:o}\0{kind}\0{value}\0".encode())
    return digest.hexdigest()


def file_digest(stream):
    return hashlib.file_digest(stream, "sha256").hexdigest()


def archive_digest(filename):
    entries = []
    with tarfile.open(filename) as archive:
        for member in archive:
            name = member.name.removeprefix("./").rstrip("/")
            if name in ("", "."):
                continue
            mode = member.mode & 0o777
            if member.isdir():
                entries.append((name, mode | 0o555, "directory", ""))
            elif member.isfile():
                mode |= 0o444
                if mode & 0o111:
                    mode |= 0o111
                with archive.extractfile(member) as stream:
                    entries.append((name, mode, "file", file_digest(stream)))
            elif member.issym():
                entries.append((name, 0, "link", member.linkname))
            else:
                raise ValueError("The archive contains an unsupported file.")
    return digest_entries(entries)


def volume_digest(root):
    entries = []

    def visit(directory):
        for item in directory.iterdir():
            name = item.relative_to(root).as_posix()
            if name in ("release-id", "archive-sha256"):
                continue
            info = item.lstat()
            mode = stat.S_IMODE(info.st_mode)
            if stat.S_ISLNK(info.st_mode):
                entries.append((name, 0, "link", os.readlink(item)))
            elif stat.S_ISDIR(info.st_mode):
                entries.append((name, mode, "directory", ""))
                visit(item)
            elif stat.S_ISREG(info.st_mode):
                with item.open("rb") as stream:
                    entries.append((name, mode, "file", file_digest(stream)))
            else:
                raise ValueError("The volume contains an unsupported file.")

    visit(root)
    return digest_entries(entries)


def command(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT).strip()


def main():
    os.umask(0o022)
    action = sys.argv[1]
    if action == "tools":
        if platform.system() != "Linux" or platform.machine() != "aarch64":
            raise ValueError("The OpenCode package requires Linux arm64.")
        with open(shutil.which("node"), "rb") as stream:
            node_digest = file_digest(stream)
        npm_root = Path(shutil.which("npm")).resolve().parent.parent
        print(json.dumps({"platform": "linux-arm64", "node": command("node", "--version"),
                          "node_sha256": node_digest, "npm": command("npm", "--version"),
                          "npm_sha256": volume_digest(npm_root)}))
    elif action == "build":
        archive = "/output/culler-opencode-linux-arm64.tar.gz"
        result = subprocess.run(["/bin/sh", "/source/release/opencode/build.sh", archive],
                                text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if result.returncode:
            lines = result.stdout.strip().splitlines()
            npm_errors = [line for line in lines if "npm error" in line and "log" not in line.lower()]
            cause = "\n".join(npm_errors or lines[-6:])[-1200:]
            stage = "preparation"
            if any(code in cause for code in ("ENETUNREACH", "ECONNREFUSED", "ECONNRESET", "ETIMEDOUT", "EAI_AGAIN", "ENOTFOUND", "E404", "E403")):
                stage = "download"
            elif "ValueError:" in cause:
                stage = "validation"
            print(json.dumps({"stage": stage, "cause": cause}), flush=True)
            sys.exit(1)
        with open(archive, "rb") as stream:
            checksum = file_digest(stream)
        print(json.dumps({"archive_sha256": checksum, "content_sha256": archive_digest(archive)}))
    elif action == "verify":
        root = Path(sys.argv[2])
        # Check the files before execution. A corrupt program receives no execution attempt.
        if volume_digest(root) != sys.argv[3]:
            raise ValueError("The installed OpenCode files differ from the archive.")
        if (root / "archive-sha256").read_text().strip() != sys.argv[4]:
            raise ValueError("The installed OpenCode archive identity differs.")
        if (root / "release-id").read_text().strip() != sys.argv[5]:
            raise ValueError("The installed OpenCode release identity differs.")
        for key in ("DATA", "CONFIG", "CACHE", "STATE"):
            os.environ[f"XDG_{key}_HOME"] = f"/tmp/opencode-{key.lower()}"
        version = command(str(root / "node_modules/@opencode/cli-linux-arm64/bin/opencode"), "--version")
        if version != "opencode v" + sys.argv[6]:
            raise ValueError(f"The installed program reports {version}. This release requires OpenCode {sys.argv[6]}.")
        print(version)
    else:
        raise ValueError("The OpenCode package operation is invalid.")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        detail = error.output if isinstance(error, subprocess.CalledProcessError) else str(error)
        stage = "preparation" if sys.argv[1] in ("build", "tools") else "validation"
        print(json.dumps({"stage": stage, "cause": detail}), flush=True)
        sys.exit(1)
