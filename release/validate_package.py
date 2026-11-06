#!/usr/bin/env python3
"""Check a built package through its installer in a private scratch directory."""

import argparse
import fcntl
import hashlib
import json
import os
import re
import stat
import subprocess
import tarfile
import tempfile
import zipfile
from pathlib import Path


PACKAGE_NAME = "culler-darwin-arm64.tar.gz"
PIN_PATH = Path(__file__).resolve().parent.parent / "mc" / "deployment" / "agentsview.json"
HOST_BUN_PIN_PATH = Path(__file__).resolve().parent.parent / "mc" / "deployment" / "hostbun.json"
HOST_BUN_PLATFORM = "darwin-arm64"
EXECUTABLES = ("bin/mc", "bin/install.sh", "bin/agentsview")
DOWNLOAD_URL = "https://api.github.com/repos/culler/package-check/releases/assets/3"
LOCK_REFUSAL = "Another installer or service command controls this deployment. Retry after it completes."
REQUIRED_CHECKS = ("fast-suite-macos", "deployment-linux", "application-package", "product-image", "release-downloads")


def digest_stream(file):
    checksum = hashlib.sha256()
    for block in iter(lambda: file.read(1024 * 1024), b""):
        checksum.update(block)
    return checksum.hexdigest()


def digest(path):
    with path.open("rb") as file:
        return digest_stream(file)


def regular_file(path):
    if not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError("The package input must be a regular file: " + path.name)


def archive_files(archive):
    files = {}
    with tarfile.open(archive, "r:gz") as reader:
        for member in reader:
            name = member.name
            parts = name.split("/")
            allowed = name in EXECUTABLES or re.fullmatch(r"(?:host|runner)/[A-Za-z0-9_./-]+", name)
            if not allowed or any(part in {"", ".", ".."} for part in parts):
                raise ValueError("The package contains an invalid path: " + name)
            if name in files or not member.isfile() or member.size <= 0:
                raise ValueError("The package requires unique, nonempty regular files: " + name)
            mode = 0o700 if name.startswith("bin/") else 0o600
            if member.mode != mode or member.uid != 0 or member.gid != 0:
                raise ValueError("The package contains an invalid file mode or owner: " + name)
            with reader.extractfile(member) as file:
                files[name] = (mode, digest_stream(file))
    if not set(EXECUTABLES).issubset(files) or any(
        not any(name.startswith(prefix) for name in files) for prefix in ("host/", "runner/")
    ):
        raise ValueError("The package lacks a required runtime file group.")
    return files


def validate_staged(package, files):
    found = set()
    for path in package.rglob("*"):
        info = path.lstat()
        name = path.relative_to(package).as_posix()
        if stat.S_ISDIR(info.st_mode):
            continue
        if name not in files or not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
            raise ValueError("The staged package contains an unexpected file: " + name)
        mode, expected = files[name]
        if stat.S_IMODE(info.st_mode) != mode or info.st_uid != os.getuid() or digest(path) != expected:
            raise ValueError("The staged file differs from the package: " + name)
        found.add(name)
    if found != files.keys():
        raise ValueError("The installer omitted package files.")


def agentsview_identity(pin):
    return {
        "version": pin["version"],
        "archive_sha256": pin["archive"]["sha256"],
        "executable_sha256": pin["executable"]["sha256"],
    }


def check_agentsview(metadata, files, pin):
    if metadata.get("agentsview") != agentsview_identity(pin):
        raise ValueError("The package build record names a different AgentsView release.")
    actual = files["bin/agentsview"][1]
    if actual != pin["executable"]["sha256"]:
        raise ValueError("The packaged AgentsView executable differs from its pin: expected SHA-256 "
                         + pin["executable"]["sha256"] + ", found SHA-256 " + actual + ".")


def check_host_bun(metadata, pin):
    if metadata.get("host_bun") != pin:
        raise ValueError("The package build record names a different host Bun release.")


def host_bun_fixture(scratch, pin):
    """Build a local Bun archive whose program prints the pinned version.

    The record for the installer names this archive in place of the pinned
    one, so the check downloads nothing and still verifies the checksum path.
    """
    archive = pin["archives"][HOST_BUN_PLATFORM]
    path = scratch / archive["name"]
    member = archive["name"].removesuffix(".zip") + "/bun"
    info = zipfile.ZipInfo(member)
    info.external_attr = 0o755 << 16
    with zipfile.ZipFile(path, "w") as writer:
        writer.writestr(info, "#!/bin/sh\nprintf '%s\\n' '" + pin["version"] + "'\n")
    record = {"version": pin["version"], "archives": {HOST_BUN_PLATFORM: {
        "name": archive["name"], "url": archive["url"], "size": path.stat().st_size, "sha256": digest(path),
    }}}
    return path, record


def local_release_record(output, metadata, host_bun):
    commit = metadata.get("commit", "")
    schema = metadata.get("schema", "")
    if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("The package build record lacks its commit.")
    if not isinstance(schema, str) or not re.fullmatch(r"[0-9a-f]{64}", schema):
        raise ValueError("The package build record lacks its schema identity.")
    installer = output / "assets" / "installer.sh"
    version = (output / "version").read_text().strip()
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version):
        raise ValueError("The release inputs lack the version.")
    # This local record reaches setup validation. It never authorizes publication.
    return {
        "format": 1, "version": version, "commit": commit, "release_id": commit, "tag": "v" + version,
        "sequence": 1, "published_at": "2000-01-01T00:00:00Z", "platform": "darwin-arm64",
        "schema": schema, "runtimes": metadata["runtimes"], "agentsview": metadata["agentsview"], "host_bun": host_bun,
        "package": {key: metadata[key] for key in ("name", "size", "sha256")},
        "installer": {"name": "installer.sh", "size": installer.stat().st_size, "sha256": digest(installer)},
        "image": "ghcr.io/culler/package-check@sha256:" + "0" * 64,
        "project_image": "ghcr.io/culler/project-check@sha256:" + "1" * 64,
        "checks": [{"name": name, "passed": True} for name in REQUIRED_CHECKS],
    }


def validate_host_bun(home, record, bun_archive, check_setup):
    """The setup run installs the host Bun under MC_HOME/bun before setup; the status run installs nothing."""
    root = home / "bun"
    if not check_setup:
        if root.exists():
            raise ValueError("The update check installed a host Bun.")
        return
    if not root.is_dir():
        raise ValueError("The installer did not install the host Bun directory.")
    version = record["host_bun"]["version"]
    program = root / version / "bin" / "bun"
    found = sorted(path.relative_to(root).as_posix() for path in root.rglob("*") if not path.is_dir())
    if found != [version + "/bin/bun"]:
        raise ValueError("The installer left other files under the host Bun directory: " + ", ".join(found))
    info = program.lstat()
    if not stat.S_ISREG(info.st_mode) or not info.st_mode & stat.S_IXUSR:
        raise ValueError("The installed host Bun is not an executable regular file.")
    with zipfile.ZipFile(bun_archive) as reader:
        expected = reader.read(record["host_bun"]["archives"][HOST_BUN_PLATFORM]["name"].removesuffix(".zip") + "/bun")
    if program.read_bytes() != expected:
        raise ValueError("The installed host Bun differs from the archive.")


def run_installer(output, archive, record, files, scratch, bun_archive, check_setup=False):
    scratch.mkdir(mode=0o700)
    bootstrap = scratch / "bootstrap"
    tools = scratch / "tools"
    for directory in (bootstrap, tools, scratch / "home", scratch / "tmp"):
        directory.mkdir(mode=0o700)
    alias = scratch / "alias"
    alias.symlink_to(scratch, target_is_directory=True)
    (bootstrap / "release.json").write_text(json.dumps(record))
    (bootstrap / "github-release.json").write_text(json.dumps({"assets": [{"name": PACKAGE_NAME, "id": 3}]}))
    (bootstrap / "curl.conf").write_text("")
    # The downloads are local. The installer and packaged program are real.
    curl = tools / "curl"
    curl.write_text("""#!/bin/sh
set -eu
output=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in --output) output=$2; shift ;; https://*) url=$1 ;; esac
  shift
done
case "$url" in
  "$CULLER_PACKAGE_CHECK_URL") source=$CULLER_PACKAGE_CHECK_ARCHIVE ;;
  "$CULLER_PACKAGE_CHECK_BUN_URL") source=$CULLER_PACKAGE_CHECK_BUN_ARCHIVE ;;
  *) exit 91 ;;
esac
printf '%s\\n' "$url" >>"$CULLER_PACKAGE_CHECK_FETCHES"
cp "$source" "$output"
printf 200
""")
    curl.chmod(0o700)
    # Setup must stop at the held lock before any host service. The installer never calls mise.
    fixtures = {
        "xcode-select": '[ "$#" = 1 ] && [ "$1" = -p ]',
        "git": '[ "$#" = 1 ] && [ "$1" = --version ]',
        "mise": "exit 93",
    }
    for name, body in fixtures.items():
        path = tools / name
        path.write_text("#!/bin/sh\nset -eu\n" + body + "\n")
        path.chmod(0o700)
    env = {
        "PATH": str(tools) + ":/usr/bin:/bin", "HOME": str(scratch / "home"),
        "TMPDIR": str(scratch / "tmp"), "TERM": "dumb", "NO_COLOR": "1",
        "CULLER_BOOTSTRAP_DIR": str(alias) + "//bootstrap", "CULLER_REPOSITORY": "culler/package-check",
        "CULLER_GITHUB_TOKEN_FILE": str(scratch / "absent-token"),
        "MC_HOME": str(scratch / "deployment"),
        "MC_BIN_DIR": str(scratch / "installed-bin"),
        "CULLER_PACKAGE_CHECK_URL": DOWNLOAD_URL,
        "CULLER_PACKAGE_CHECK_FETCHES": str(scratch / "fetches"),
        "CULLER_PACKAGE_CHECK_ARCHIVE": str(archive),
        "CULLER_PACKAGE_CHECK_BUN_URL": record["host_bun"]["archives"][HOST_BUN_PLATFORM]["url"],
        "CULLER_PACKAGE_CHECK_BUN_ARCHIVE": str(bun_archive),
    }
    home = Path(env["MC_HOME"])
    home.mkdir(mode=0o700)
    lock_path = home / "install.lock"
    with lock_path.open("w") as lock:
        lock_path.chmod(0o600)
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        # The status check stops at the absent credential. Setup stops at this lock.
        result = subprocess.run(
            ["/bin/sh", str(output / "assets" / "installer.sh")] + ([] if check_setup else ["--check-updates"]),
            cwd=scratch, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT, text=True, timeout=120,
        )
    text = " ".join(result.stdout.split())
    code, message = (2, LOCK_REFUSAL) if check_setup else (1, "GitHub refused access.")
    if result.returncode != code or "Culler passed the download checks." not in text or message not in text:
        raise ValueError("The packaged installer did not complete the expected local check: " + text)
    fetches = [DOWNLOAD_URL] + ([env["CULLER_PACKAGE_CHECK_BUN_URL"]] if check_setup else [])
    if (scratch / "fetches").read_text().splitlines() != fetches:
        raise ValueError("The installer used an unexpected download request.")
    validate_staged(bootstrap / "package", files)
    permitted = {lock_path} | ({home / "bun"} if check_setup else set())
    if not set(home.iterdir()) <= permitted or Path(env["MC_BIN_DIR"]).exists():
        raise ValueError("The package check wrote deployment files before the install lock.")
    validate_host_bun(home, record, bun_archive, check_setup)


def validate(output, pin_path=None, host_bun_pin_path=None):
    output = output.resolve(strict=True)
    pin = json.loads((pin_path or PIN_PATH).read_text())
    host_bun_pin = json.loads((host_bun_pin_path or HOST_BUN_PIN_PATH).read_text())
    archive = output / "assets" / PACKAGE_NAME
    for path in (output / "package.json", archive, output / "assets" / "installer.sh"):
        regular_file(path)
    metadata = json.loads((output / "package.json").read_text())
    checksum = digest(archive)
    if metadata.get("name") != PACKAGE_NAME or metadata.get("sha256") != checksum or metadata.get("size") != archive.stat().st_size:
        raise ValueError("The built package differs from its build record.")
    if not isinstance(metadata.get("runtimes"), dict):
        raise ValueError("The package build record lacks its runtime versions.")
    files = archive_files(archive)
    check_agentsview(metadata, files, pin)
    check_host_bun(metadata, host_bun_pin)
    with tempfile.TemporaryDirectory(prefix="culler-package-check-") as directory:
        scratch = Path(directory).resolve()
        bun_archive, host_bun = host_bun_fixture(scratch, host_bun_pin)
        record = local_release_record(output, metadata, host_bun)
        run_installer(output, archive, record, files, scratch / "status", bun_archive)
        run_installer(output, archive, record, files, scratch / "setup", bun_archive, check_setup=True)
    if digest(archive) != checksum:
        raise ValueError("The package changed during the check.")
    return {"name": PACKAGE_NAME, "sha256": checksum, "files": len(files), "installer_handoff": True, "setup_handoff": True}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    try:
        print(json.dumps(validate(args.output)))
    except (ValueError, OSError, tarfile.TarError, subprocess.SubprocessError) as error:
        parser.exit(1, "The built package check failed: " + str(error) + "\n")
