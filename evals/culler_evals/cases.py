"""Validate fixed cases and reconstruct isolated source and evidence."""

from __future__ import annotations

from contextlib import contextmanager
import copy
import fcntl
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tempfile
from typing import Any, Iterable
from urllib.parse import urlparse
from urllib.request import urlopen


CASE_TYPES = {
    "small_visible_change": "A small visible change",
    "feature_across_components": "A feature across several components",
    "state_transition_or_recovery": "A state transition or recovery change",
    "configuration_or_compatibility": "A configuration or compatibility change",
    "data_or_access": "A data or access change",
    "internal_refactor": "An internal refactor",
    "performance_change": "A performance change",
}
_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_GIT_HASH = re.compile(r"^(?:[0-9a-f]{40}|[0-9a-f]{64})$")
_ID = re.compile(r"^[a-z0-9][a-z0-9._-]*$")
_CHECKOUT = Path(__file__).resolve().parents[2]


class CaseError(ValueError):
    """A case or its required source cannot support a trial."""


def canonical_json(value: Any) -> bytes:
    return (json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(",", ":")) + "\n").encode()


def content_hash(value: Any) -> str:
    return hashlib.sha256(canonical_json(value)).hexdigest()


def _relative(value: Any, label: str) -> PurePosixPath:
    if not isinstance(value, str) or not value or "\\" in value or "\x00" in value:
        raise CaseError(f"{label} must contain a relative path.")
    path = PurePosixPath(value)
    if path.is_absolute() or any(part in ("..", ".") for part in value.split("/")) or ":" in value:
        raise CaseError(f"{label} must stay inside its case directory.")
    return path


def _within(path: Path, root: Path) -> bool:
    return path.resolve().is_relative_to(root.resolve())


def _file(root: Path, relative: str) -> Path:
    path = root / _relative(relative, "Artifact path")
    if not _within(path, root) or not path.is_file():
        raise CaseError(f"Artifact is absent or leaves its case directory: {relative}")
    return path


def load_case(path: str | Path) -> dict[str, Any]:
    path = Path(path).resolve()
    try:
        value = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as error:
        raise CaseError(f"Cannot read case manifest: {path}") from error
    if not isinstance(value, dict):
        raise CaseError("The case manifest must contain one object.")
    value["_manifest_path"] = str(path)
    _validate(value)
    return value


def _validate(case: dict[str, Any]) -> None:
    try:
        _validate_fields(case)
    except (TypeError, KeyError, AttributeError) as error:
        raise CaseError("The case contains an invalid field type or an absent required field.") from error


def _validate_fields(case: dict[str, Any]) -> None:
    if case.get("schema_version") != 1:
        raise CaseError("The case schema version must equal 1.")
    for name in ("id", "group"):
        if not isinstance(case.get(name), str) or not _ID.fullmatch(case[name]):
            raise CaseError(f"The case {name} is invalid.")
    types = case.get("types")
    if not isinstance(types, list) or not types or len(types) != len(set(types)) or any(t not in CASE_TYPES for t in types):
        raise CaseError("The case must name distinct established case types.")
    source = case.get("source", {})
    if not isinstance(source, dict) or not isinstance(source.get("url"), str):
        raise CaseError("The case must identify its source repository.")
    _source_url(source["url"])
    for name in ("base_commit", "reviewed_commit", "reviewed_tree"):
        if not isinstance(source.get(name), str) or not _GIT_HASH.fullmatch(source[name]):
            raise CaseError(f"The source {name} must contain a full Git identifier.")
    license_record = source.get("license", {})
    if not license_record.get("spdx") or not _SHA256.fullmatch(license_record.get("sha256", "")):
        raise CaseError("The source license must identify its license identifier and content hash.")
    _relative(license_record.get("path"), "License path")
    if source.get("change_origin") not in ("upstream", "authored"):
        raise CaseError("The source must distinguish upstream changes from authored changes.")
    if source.get("change_origin") == "authored" and not source.get("bundle"):
        raise CaseError("An authored change must provide an immutable Git bundle.")
    if source.get("bundle"):
        _validate_artifact_source(source["bundle"])
        if not _SHA256.fullmatch(source["bundle"].get("sha256", "")):
            raise CaseError("The Git bundle must have a content hash.")
    if not isinstance(case.get("project_skills"), list):
        raise CaseError("The case must record its Project skills, including an empty set.")
    inputs = case.get("role_inputs", {})
    if not isinstance(inputs, dict) or not isinstance(inputs.get("task"), dict):
        raise CaseError("The case must record its fixed task input.")
    task = inputs["task"]
    if not str(task.get("id", "")).isdigit() or int(task["id"]) <= 0:
        raise CaseError("The synthetic task identifier must be a positive integer.")
    if not task.get("title") or not task.get("acceptance_criteria"):
        raise CaseError("The task must contain its title and acceptance criteria.")
    if not isinstance(inputs.get("command_responses"), dict):
        raise CaseError("The case must supply fixed command responses.")
    if not isinstance(inputs.get("brief"), str) and not isinstance(inputs.get("brief_parts"), list):
        raise CaseError("The case must supply a fixed brief or fixed brief parts.")
    references = case.get("references", {})
    if not isinstance(references, dict) or not references.get("facts") or not references.get("limits"):
        raise CaseError("The case must supply reference facts and evidence limits.")
    if references.get("review_status") not in ("pending", "reviewed"):
        raise CaseError("The case must record its independent review status.")
    if references.get("review_status") == "reviewed":
        review = references.get("review_evidence")
        if not isinstance(review, dict) or not _SHA256.fullmatch(review.get("sha256", "")):
            raise CaseError("A reviewed case must identify its independent review record and content hash.")
        _relative(review.get("path"), "Independent review path")
        if case.get("_manifest_path"):
            data = _file(Path(case["_manifest_path"]).parent, review["path"]).read_bytes()
            if hashlib.sha256(data).hexdigest() != review["sha256"]:
                raise CaseError("The independent case review content hash differs.")
            _verify_review(case, data)
    hashes = case.get("content_hashes", {})
    for name in ("role_inputs", "references", "project_skills", "environment", "visual_evidence"):
        if hashes.get(name) != content_hash(case.get(name)):
            raise CaseError(f"The fixed {name} content hash differs from the manifest.")
    if case.get("expected_terminal") not in ("packaged", "infra", "needs_operator"):
        raise CaseError("The expected terminal result is invalid.")
    artifacts = case.get("artifacts")
    if not isinstance(artifacts, list):
        raise CaseError("The case must record its artifacts.")
    names, paths = set(), set()
    for artifact in artifacts:
        if not isinstance(artifact, dict) or not _ID.fullmatch(artifact.get("id", "")):
            raise CaseError("An artifact identifier is invalid.")
        if artifact["id"] in names:
            raise CaseError("Artifact identifiers must be distinct.")
        names.add(artifact["id"])
        path = str(_relative(artifact.get("path"), "Artifact destination"))
        if path in paths or any(path.startswith(p + "/") or p.startswith(path + "/") for p in paths):
            raise CaseError("Artifact destinations must be distinct files.")
        paths.add(path)
        if not _SHA256.fullmatch(artifact.get("sha256", "")):
            raise CaseError("Each artifact must have a Secure Hash Algorithm 256-bit (SHA-256) content hash.")
        if artifact.get("visibility") not in ("role", "judge"):
            raise CaseError("Artifact visibility must identify the role or judge.")
        if artifact.get("kind") not in ("report", "media", "reference", "source_diff", "source_record"):
            raise CaseError("The artifact kind is invalid.")
        _validate_artifact_source(artifact.get("source", {}))
        if artifact["source"]["kind"] == "file" and case.get("_manifest_path"):
            data = _file(Path(case["_manifest_path"]).parent, artifact["source"]["path"]).read_bytes()
            if hashlib.sha256(data).hexdigest() != artifact["sha256"]:
                raise CaseError(f"The required artifact content hash differs: {artifact['id']}")
        if artifact.get("kind") == "media":
            if artifact.get("revision") not in (source["base_commit"], source["reviewed_commit"]):
                raise CaseError("Each media artifact must identify its represented source revision.")
            if not artifact.get("description"):
                raise CaseError("Each media artifact must describe its visual state.")
    visual = case.get("visual_evidence", {})
    if not isinstance(visual.get("required"), bool) or visual.get("status") not in ("supplied", "missing", "not_applicable"):
        raise CaseError("The case must state its visual evidence requirement and status.")
    if visual["required"] and visual["status"] == "not_applicable":
        raise CaseError("Required visual evidence cannot be not applicable.")
    if visual["required"] and visual["status"] == "supplied" and not any(a["kind"] == "media" and a["visibility"] == "role" for a in artifacts):
        raise CaseError("Supplied visual evidence must identify an actual media artifact.")
    environment = case.get("environment", {})
    if not isinstance(environment, dict) or not environment.get("trial") or "capture" not in environment:
        raise CaseError("The case must distinguish trial and capture environments.")
    for lock in environment.get("dependency_locks", []):
        _relative(lock.get("path"), "Dependency path")
        if not _SHA256.fullmatch(lock.get("sha256", "")):
            raise CaseError("A dependency record must contain its content hash.")
    if not isinstance(case.get("paths"), dict):
        raise CaseError("The case must identify its virtual path mappings.")
    absent_paths = case.get("absent_paths", [])
    if not isinstance(absent_paths, list) or len(absent_paths) != len(set(absent_paths)):
        raise CaseError("The intentional absent paths must contain distinct paths.")
    if absent_paths and case["expected_terminal"] != "infra":
        raise CaseError("Only an infrastructure contract case can omit evidence intentionally.")
    for virtual in absent_paths:
        if not isinstance(virtual, str) or not virtual.startswith("/mc/artifacts/"):
            raise CaseError("An intentional absent path must stay inside the role artifact plane.")
        _relative(virtual.removeprefix("/mc/artifacts/"), "Intentional absent path")
        if case["paths"].get(virtual) != "absent":
            raise CaseError("The case must map each intentional absent path explicitly.")
    for virtual, target in case["paths"].items():
        if virtual == "/workspace/source" and target == "repository":
            continue
        if not virtual.startswith("/mc/artifacts/"):
            raise CaseError("The case virtual path is outside the role environment.")
        if target == "absent" and virtual in absent_paths:
            continue
        if target not in names or next(a for a in artifacts if a["id"] == target)["visibility"] != "role":
            raise CaseError("Each role path must identify a role-visible artifact.")
        expected = "/mc/artifacts/" + next(a for a in artifacts if a["id"] == target)["path"]
        if virtual != expected:
            raise CaseError("The virtual artifact path differs from its destination.")


def _source_url(value: str) -> None:
    parsed = urlparse(value)
    if parsed.scheme == "https" and parsed.netloc and not parsed.username and not parsed.password:
        return
    if not parsed.scheme and Path(value).is_absolute():
        return
    raise CaseError("The repository source must use HTTPS or an absolute local test path.")


def _verify_review(case: dict[str, Any], data: bytes) -> None:
    try:
        review = json.loads(data)
        if not isinstance(review, dict) or not isinstance(review.get("reviewer"), str) or not review["reviewer"].strip():
            raise CaseError("The independent case review must name its reviewer.")
        entries = [entry for entry in review.get("cases", []) if entry.get("id") == case["id"]]
        if len(entries) != 1 or entries[0].get("result") != "source_facts_accepted":
            raise CaseError("The independent review does not accept this case's source facts.")
        entry = entries[0]
        for name in ("base_commit", "reviewed_commit", "reviewed_tree"):
            if entry.get(name) != case["source"][name]:
                raise CaseError("The independent case review addresses a different source range.")
        if entry.get("reference_facts_hash") != content_hash(case["references"]["facts"]):
            raise CaseError("The reference facts differ from their independently reviewed facts.")
        if entry.get("reference_limits_hash") != content_hash(case["references"]["limits"]):
            raise CaseError("The reference limits differ from their independently reviewed limits.")
        if case["visual_evidence"]["required"] and case["visual_evidence"]["status"] == "supplied":
            hashes = sorted(artifact["sha256"] for artifact in case["artifacts"] if artifact["kind"] == "media" and artifact["visibility"] == "role")
            if hashes and (entry.get("media_review_result") != "accepted" or sorted(entry.get("media_artifact_hashes", [])) != hashes):
                raise CaseError("The supplied application media requires an independent review of its exact artifacts.")
    except (json.JSONDecodeError, TypeError, AttributeError) as error:
        raise CaseError("The independent case review record is invalid.") from error


def _validate_artifact_source(source: dict[str, Any]) -> None:
    kind = source.get("kind")
    if kind == "file":
        _relative(source.get("path"), "Artifact source")
    elif kind == "https":
        _source_url(source.get("url", ""))
        if urlparse(source.get("url", "")).scheme != "https":
            raise CaseError("A remote artifact must use HTTPS.")
    elif kind == "git_blob":
        if not _GIT_HASH.fullmatch(source.get("commit", "")):
            raise CaseError("An artifact source must contain a full Git commit identifier.")
        _relative(source.get("path"), "Git artifact path")
    elif kind != "git_diff":
        raise CaseError("The artifact source kind is invalid.")


def case_readiness(case: dict[str, Any]) -> dict[str, Any]:
    problems = []
    if case["references"]["review_status"] != "reviewed":
        problems.append("The reference facts require independent review.")
    if case["visual_evidence"]["required"] and case["visual_evidence"]["status"] != "supplied":
        problems.append("The case requires supplied application media.")
    return {"ready": not problems, "problems": problems}


def validate_cases(paths: Iterable[str | Path | dict[str, Any]], require_ready: bool = False) -> list[dict[str, Any]]:
    cases, identifiers = [], set()
    for path in paths:
        case = load_case(path) if not isinstance(path, dict) else copy.deepcopy(path)
        _validate(case)
        if case["id"] in identifiers:
            raise CaseError(f"The case identifier occurs twice: {case['id']}")
        identifiers.add(case["id"])
        readiness = case_readiness(case)
        if require_ready and not readiness["ready"]:
            raise CaseError(f"The case is not ready: {case['id']}: {' '.join(readiness['problems'])}")
        cases.append(case)
    return cases


def _git(*args: str, cwd: Path | None = None) -> bytes:
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    env.update({"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull, "GIT_TERMINAL_PROMPT": "0"})
    command = ["git", "-c", f"core.hooksPath={os.devnull}", "-c", "core.fsmonitor=false", "-c", "protocol.ext.allow=never", *map(str, args)]
    result = subprocess.run(command, cwd=cwd, env=env, capture_output=True)
    if result.returncode:
        detail = result.stderr.decode(errors="replace").strip()
        raise CaseError(f"Source reconstruction failed: {detail}")
    return result.stdout


@contextmanager
def _cache_lock(cache: Path, identifier: str):
    cache.mkdir(parents=True, exist_ok=True)
    with (cache / f"{identifier}.lock").open("a+") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(lock, fcntl.LOCK_UN)


def _artifact_bytes(artifact: dict[str, Any], case: dict[str, Any], repository: Path | None, cache: Path) -> bytes:
    source = artifact.get("source", artifact)
    kind = source["kind"]
    if kind == "file":
        if not case.get("_manifest_path"):
            raise CaseError("A local artifact requires the manifest's file location.")
        data = _file(Path(case["_manifest_path"]).parent, source["path"]).read_bytes()
    elif kind == "https":
        saved = cache / "artifacts" / artifact["sha256"]
        if saved.is_file():
            data = saved.read_bytes()
        else:
            try:
                with urlopen(source["url"], timeout=60) as response:
                    if urlparse(response.geturl()).scheme != "https":
                        raise CaseError("An artifact redirect left HTTPS.")
                    data = response.read()
            except OSError as error:
                raise CaseError("Cannot obtain the required external artifact.") from error
            if hashlib.sha256(data).hexdigest() == artifact["sha256"]:
                saved.parent.mkdir(parents=True, exist_ok=True)
                saved.write_bytes(data)
    elif kind == "git_blob" and repository:
        data = _git("show", f"{source['commit']}:{source['path']}", cwd=repository)
    elif kind == "git_diff" and repository:
        data = _git("diff", "--no-ext-diff", "--no-textconv", "--binary", case["source"]["base_commit"], case["source"]["reviewed_commit"], cwd=repository)
    else:
        raise CaseError("The artifact requires a reconstructed repository.")
    if hashlib.sha256(data).hexdigest() != artifact["sha256"]:
        raise CaseError(f"The required artifact content hash differs: {artifact.get('id', 'source bundle')}")
    return data


def _acquire_source(case: dict[str, Any], cache: Path) -> Path:
    source = case["source"]
    identifier = hashlib.sha256(source["url"].encode()).hexdigest()
    mirror = cache / "sources" / f"{identifier}.git"
    with _cache_lock(cache, identifier):
        if not mirror.exists():
            mirror.parent.mkdir(parents=True, exist_ok=True)
            temporary = Path(tempfile.mkdtemp(prefix="source-", dir=mirror.parent))
            try:
                _git("clone", "--mirror", "--", source["url"], str(temporary))
                temporary.rename(mirror)
            except BaseException:
                shutil.rmtree(temporary)
                raise
        elif _git("config", "--get", "remote.origin.url", cwd=mirror).decode().strip() != source["url"]:
            raise CaseError("The cached repository belongs to a different source.")
        if source.get("bundle"):
            bundle = cache / "artifacts" / source["bundle"]["sha256"]
            data = _artifact_bytes(source["bundle"], case, None, cache)
            bundle.parent.mkdir(parents=True, exist_ok=True)
            bundle.write_bytes(data)
            _git("fetch", str(bundle), source["reviewed_commit"], cwd=mirror)
        for commit in (source["base_commit"], source["reviewed_commit"]):
            try:
                _git("cat-file", "-e", f"{commit}^{{commit}}", cwd=mirror)
            except CaseError:
                _git("fetch", "origin", commit, cwd=mirror)
            resolved = _git("rev-parse", f"{commit}^{{commit}}", cwd=mirror).decode().strip()
            if resolved != commit:
                raise CaseError("The repository commit differs from its recorded identifier.")
        _git("merge-base", "--is-ancestor", source["base_commit"], source["reviewed_commit"], cwd=mirror)
        tree = _git("rev-parse", f"{source['reviewed_commit']}^{{tree}}", cwd=mirror).decode().strip()
        if tree != source["reviewed_tree"]:
            raise CaseError("The reviewed source tree differs from its recorded identifier.")
    return mirror


def prepare_case(manifest: str | Path | dict[str, Any], destination: str | Path, cache: str | Path, require_ready: bool = False) -> dict[str, Any]:
    case = validate_cases([manifest], require_ready=require_ready)[0]
    destination, cache = Path(destination).resolve(), Path(cache).resolve()
    if _within(destination, _CHECKOUT) or _within(cache, _CHECKOUT):
        raise CaseError("External source and its cache must stay outside the Culler checkout.")
    if destination.exists():
        raise CaseError("The prepared case destination must be absent.")
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = Path(tempfile.mkdtemp(prefix="case-", dir=destination.parent))
    try:
        mirror = _acquire_source(case, cache)
        repository = temporary / "repository"
        _git("clone", "--no-hardlinks", "--no-checkout", "--", str(mirror), str(repository))
        _git("remote", "remove", "origin", cwd=repository)
        _git("checkout", "--detach", case["source"]["reviewed_commit"], cwd=repository)
        # The Packager skill finds the start of the reviewed range against this reference.
        _git("update-ref", "refs/mc/destination", case["source"]["base_commit"], cwd=repository)
        license_record = case["source"]["license"]
        license_data = _file(repository, license_record["path"]).read_bytes()
        if hashlib.sha256(license_data).hexdigest() != license_record["sha256"]:
            raise CaseError("The source license content hash differs from the manifest.")
        for lock in case["environment"].get("dependency_locks", []):
            if hashlib.sha256(_file(repository, lock["path"]).read_bytes()).hexdigest() != lock["sha256"]:
                raise CaseError(f"The source dependency content hash differs: {lock['path']}")
        for artifact in case["artifacts"]:
            root = temporary / ("artifacts" if artifact["visibility"] == "role" else "references")
            target = root / artifact["path"]
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(_artifact_bytes(artifact, case, repository, cache))
        (temporary / "artifacts").mkdir(exist_ok=True)
        (temporary / "references").mkdir(exist_ok=True)
        (temporary / "references" / "facts.json").write_bytes(canonical_json(case["references"]))
        if case["references"].get("review_status") == "reviewed":
            review = case["references"]["review_evidence"]
            data = _file(Path(case["_manifest_path"]).parent, review["path"]).read_bytes()
            (temporary / "references" / "review.json").write_bytes(data)
        for virtual in case.get("absent_paths", []):
            relative = virtual.removeprefix("/mc/artifacts/")
            if (temporary / "artifacts" / relative).exists():
                raise CaseError("An intentionally absent evidence path exists.")
        if _git("status", "--porcelain=v1", "--untracked-files=all", cwd=repository):
            raise CaseError("The prepared source checkout is not clean.")
        temporary.rename(destination)
    except BaseException:
        shutil.rmtree(temporary)
        raise
    fixed = {key: value for key, value in case.items() if not key.startswith("_")}
    result = {
        "case_id": case["id"], "group_id": case["group"], "types": case["types"],
        "repository_path": str(destination / "repository"),
        "artifacts_path": str(destination / "artifacts"),
        "references_path": str(destination / "references"),
        "brief": case["role_inputs"].get("brief", ""),
        "brief_parts": copy.deepcopy(case["role_inputs"].get("brief_parts", [])),
        "task_id": str(case["role_inputs"]["task"]["id"]),
        "reviewed_commit": case["source"]["reviewed_commit"], "base_commit": case["source"]["base_commit"],
        "command_responses": copy.deepcopy(case["role_inputs"]["command_responses"]),
        "references": copy.deepcopy(case["references"]), "expected_terminal": case["expected_terminal"],
        "project_skills": copy.deepcopy(case["project_skills"]),
        "absent_paths": copy.deepcopy(case.get("absent_paths", [])),
        "manifest_hash": content_hash(fixed), "readiness": case_readiness(case),
        "paths": {"/workspace/source": str(destination / "repository"), "/mc/artifacts": str(destination / "artifacts")},
    }
    for virtual, identifier in case["paths"].items():
        if identifier == "repository":
            continue
        if identifier == "absent":
            result["paths"][virtual] = str(destination / "artifacts" / virtual.removeprefix("/mc/artifacts/"))
            continue
        artifact = next(a for a in case["artifacts"] if a["id"] == identifier)
        result["paths"][virtual] = str(destination / "artifacts" / artifact["path"])
    (destination / "prepared.json").write_bytes(canonical_json(result))
    return result
