"""Build a separate evaluation image from immutable inputs."""

import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tarfile
import tempfile
from urllib.request import urlopen


parser = argparse.ArgumentParser(description="Build a separate evaluation image from pinned packages. The build uses no model calls.")
parser.add_argument("--configuration", type=Path, required=True, help="Read the pinned base image and package identities from this JSON file.")
parser.add_argument("--cache", type=Path, required=True, help="Store and verify public package archives in this external directory.")
parser.add_argument("--output", type=Path, required=True, help="Write the exact image identity and input record to this JSON file.")
args = parser.parse_args()
configuration = json.loads(args.configuration.read_text())
if not re.fullmatch(r"sha256:[a-f0-9]{64}", configuration["base_image"]) or not configuration["tag"].startswith("culler-eval:"):
    raise ValueError("The overlay requires an immutable base and a separate evaluation tag.")
args.cache.mkdir(mode=0o700, parents=True, exist_ok=True)
base = subprocess.run(["docker", "image", "inspect", "--format", "{{.Id}}", configuration["base_image"]],
                      capture_output=True, text=True, check=True).stdout.strip()
if base != configuration["base_image"]:
    raise ValueError("The base image differs from its pinned identity.")
base_tag = "culler-eval-base:" + base.split(":", 1)[1]
subprocess.run(["docker", "tag", base, base_tag], check=True)
with tempfile.TemporaryDirectory(prefix="culler-eval-image-") as directory:
    context = Path(directory)
    shutil.copyfile(Path(__file__).with_name("runtime-image.Dockerfile"), context / "Dockerfile")
    for package in configuration["packages"]:
        if package["directory"] not in ("cli", "native") or not re.fullmatch(r"[a-f0-9]{64}", package["sha256"]):
            raise ValueError("The package input contains an invalid directory or digest.")
        archive = args.cache / (package["sha256"] + ".tgz")
        if not archive.exists():
            with urlopen(package["url"], timeout=60) as response, archive.open("wb") as target:
                while block := response.read(1024 * 1024):
                    target.write(block)
        with archive.open("rb") as file:
            actual_hash = hashlib.file_digest(file, "sha256").hexdigest()
        if actual_hash != package["sha256"]:
            raise ValueError("The downloaded package differs from its pinned digest.")
        destination = context / package["directory"]
        destination.mkdir()
        with tarfile.open(archive, "r:gz") as source:
            for member in source.getmembers():
                path = PurePosixPath(member.name)
                if path.is_absolute() or path.parts[0] != "package" or ".." in path.parts or not (member.isfile() or member.isdir()):
                    raise ValueError("The package contains an unsupported archive entry.")
                if len(path.parts) == 1:
                    continue
                member.name = str(PurePosixPath(*path.parts[1:]))
                source.extract(member, destination, filter="data")
        manifest = json.loads((destination / "package.json").read_text())
        if manifest["name"] != package["name"] or manifest["version"] != package["version"]:
            raise ValueError("The package identity differs from the pinned configuration.")
    subprocess.run(["docker", "build", "--platform", configuration["platform"], "--build-arg", "BASE_IMAGE=" + base_tag,
                    "--tag", configuration["tag"], str(context)], check=True)
identity = subprocess.run(["docker", "image", "inspect", "--format", "{{.Id}}", configuration["tag"]], capture_output=True, text=True, check=True).stdout.strip()
args.output.parent.mkdir(parents=True, exist_ok=True)
args.output.write_text(json.dumps({"image": identity, "inputs": configuration, "model_calls": 0}, indent=2) + "\n")
print("The evaluation image build passed. The record contains the exact inputs and image identity.")
print(json.dumps({"image": identity, "record": str(args.output), "model_calls": 0}))
