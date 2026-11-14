"""Recorded continuation with fixed cases, directives, and product sources."""

from __future__ import annotations

import copy
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess

from .assessment import JUDGE_INSTRUCTIONS
from .protocol import assessment_contract, digest, framework_source_hashes, freeze_protocol, source_revision, verify_frozen_sources
from .records import RecordStore, content_hash
from .runtime_sources import verify_runtime_sources


REPAIR_FILES = {"evals/culler_evals/trial_resources.py", "evals/culler_evals/continuation.py",
                "evals/culler_evals/control_server.py"}


def repair_preview(store, source):
    """Limit the repair to repository disposal and browser continuation."""
    protocol = store.read_manifest("protocol")
    current = framework_source_hashes(source)
    before = protocol["framework_source_hashes"]
    changed = sorted(name for name in before.keys() | current.keys() if before.get(name) != current.get(name))
    if not changed:
        return None
    if set(changed) - REPAIR_FILES:
        raise ValueError("The repair changes other evaluation files. Resume with the recorded source instead.")
    update_preview(store)
    return {"files": changed, "assessments_to_repeat": 0,
            "changes": ["Repair repository disposal after the Judge reassesses a retained packet.",
                        "Reuse the completed Judge scores and preserve the original cleanup records."]}


def _prepare_repair(store, source, source_factory):
    repair = store.read_manifest("framework_repair")
    protocol = store.read_manifest("protocol")
    if repair and not repair.get("applied"):
        frozen = json.loads(store.read_blob(repair["after"]))
        resume_source = Path(repair["source"])
        verify_source(frozen, resume_source)
    else:
        preview = repair_preview(store, source)
        if preview is None:
            return protocol, recorded_source(protocol, source)
        resume_source = source_factory(protocol, Path(source), store.root.name)
        updated = copy.deepcopy(protocol)
        updated.pop("framework_source_hashes")
        updated.pop("protocol_hash")
        frozen = freeze_protocol(updated, resume_source)
        if {key: value for key, value in frozen.items() if key not in ("framework_source_hashes", "protocol_hash")} != {
                key: value for key, value in protocol.items() if key not in ("framework_source_hashes", "protocol_hash")}:
            raise ValueError("The repair changes the experiment settings. Resume with the recorded source instead.")
        search = copy.deepcopy(store.read_manifest("search_identity"))
        if search:
            search["identity"]["protocol"] = frozen
            search["hash"] = content_hash(search["identity"])
        repair = {"before": store.put_blob(protocol), "after": store.put_blob(frozen),
                  "source": str(resume_source), "search_identity": search,
                  "previous_search_identity": store.put_blob(store.read_manifest("search_identity")),
                  "preview": preview, "applied": False}
        store.write_manifest("framework_repair", repair)
    if repair["search_identity"]:
        store.write_manifest("search_identity", repair["search_identity"])
    store.write_manifest("protocol", frozen)
    if not any(event["kind"] == "framework_repaired" and event["data"].get("protocol_hash") == frozen["protocol_hash"]
               for event in store.events()):
        store.append("framework_repaired", {"protocol_hash": frozen["protocol_hash"],
                     "previous_protocol_hash": json.loads(store.read_blob(repair["before"]))["protocol_hash"],
                     "before": repair["before"], "after": repair["after"], **repair["preview"]}, stage="preparation")
    repair["applied"] = True
    store.write_manifest("framework_repair", repair)
    return frozen, resume_source


def verify_source(protocol, source):
    if source_revision(source) != protocol["source_revision"]:
        raise ValueError("The source revision differs from the recorded experiment.")
    verify_frozen_sources(protocol, source)
    verify_runtime_sources(source, protocol["runtime_source_manifest"])


def recorded_source(protocol, source, *, store=None, recover=True):
    """Find an intact checkout without changing any checkout."""
    candidates = [Path(source)]
    output = subprocess.check_output(["git", "worktree", "list", "--porcelain"], cwd=source, text=True)
    candidates.extend(Path(line[9:]) for line in output.splitlines() if line.startswith("worktree "))
    for candidate in dict.fromkeys(candidates):
        try:
            verify_source(protocol, candidate)
            return candidate
        except (ValueError, RuntimeError, OSError, subprocess.SubprocessError):
            pass
    if store is not None:
        from .storage import restore_source
        saved = store.read_manifest("source_archive")
        if saved and saved["protocol_hash"] == protocol["protocol_hash"]:
            store.read_blob(saved["archive"])
            store.read_blob(saved["bundle"])
            if recover:
                return restore_source(store, protocol, source)
            destination = Path(saved["source"])
            if destination.parent.resolve() != Path("/private/tmp/culler-worktrees").resolve() or destination.exists() or destination.is_symlink():
                raise ValueError("The recorded source requires an absent task worktree for automatic recovery.")
            return destination
    raise ValueError("The recorded source is unavailable. Restore its checkout before resume.")


def _compatible_assessment(old):
    current = assessment_contract()
    if old == current:
        return True
    previous = copy.deepcopy(current)
    previous.pop("judge_output_schema_template")
    text = JUDGE_INSTRUCTIONS.replace(
        "The semantic_checks value is an array of objects, with one object for each required identifier. ", "")
    previous["judge_instructions_hash"] = hashlib.sha256(text.encode()).hexdigest()
    return old == previous


def update_preview(store):
    protocol = store.read_manifest("protocol")
    if not protocol:
        raise ValueError("The experiment requires its recorded protocol.")
    if protocol.get("protocol_hash") != digest({key: value for key, value in protocol.items() if key != "protocol_hash"}):
        raise ValueError("The recorded protocol checksum differs from its content.")
    events = store.events()
    if (store.read_manifest("checkpoint") or store.read_manifest("seed_evaluation")
            or store.read_manifest("search_result") or any(event["kind"] in
            ("parent_selected", "validation_scored", "optimizer_response_received") for event in events)):
        raise ValueError("A runtime update requires an incomplete initial validation. Resume with the recorded settings instead.")
    if protocol.get("assessment_status") != "uncalibrated" or not _compatible_assessment(protocol.get("assessment_contract")):
        raise ValueError("The assessment rules differ. Start a separate experiment with the current rules.")
    if protocol.get("runtime", {}).get("judge", {}).get("harness") != "claude":
        raise ValueError("This runtime update requires a Claude Code Judge.")
    original = store.candidates().get("D0")
    if original and original.get("texts") != protocol["seed_candidate"]:
        raise ValueError("The original candidate differs from its recorded directive.")
    search = store.read_manifest("search_identity")
    if search and (search.get("hash") != content_hash(search.get("identity")) or search["identity"].get("protocol") != protocol):
        raise ValueError("The native search identity differs from the recorded protocol.")
    slots = [json.loads(path.read_bytes()) for path in sorted((store.root / "manifests").glob("trial-slot-*.json"))]
    return {"packets_retained": len(slots), "assessments_to_repeat": sum(bool(slot.get("completed")) for slot in slots),
            "changes": ["Enable fast mode for the Packager, Judge, and Optimizer.",
                        "Require validated JavaScript Object Notation (JSON) from the Judge.",
                        "Reuse the retained packets and repeat their Judge assessments.",
                        "Keep the original directive, cases, rubric, models, and effort levels."]}


def updated_source(protocol, current, run_id):
    """Use the original product and runner with the current evaluation code."""
    revision = protocol["source_revision"]
    if not re.fullmatch(r"[0-9a-f]{40}", revision) or not re.fullmatch(r"[a-zA-Z0-9-]+", run_id):
        raise ValueError("The experiment source identifier is invalid.")
    paths = sorted((current / "evals/culler_evals").glob("*.py"))
    paths += [current / "evals/pyproject.toml", current / "evals/uv.lock"]
    paths += sorted((current / "evals/bridge").glob("*"))
    paths += sorted((current / "evals/web").glob("*.html"))
    files = {path.relative_to(current).as_posix(): path for path in paths if path.is_file()}
    if any(path.is_symlink() for path in files.values()):
        raise ValueError("The evaluation source cannot contain symbolic links.")
    identity = digest({"revision": revision, "files": {name: hashlib.sha256(path.read_bytes()).hexdigest()
                                                       for name, path in files.items()}})
    name = "eval-resume-" + run_id + "-" + identity[:12]
    destination = Path("/private/tmp/culler-worktrees") / name
    if not destination.exists():
        subprocess.run(["git", "worktree", "add", "-b", name, str(destination), revision],
                       cwd=current, check=True, capture_output=True, text=True)
    if destination.is_symlink() or source_revision(destination) != revision:
        raise ValueError("The resume checkout differs from its recorded source revision.")
    for name, path in files.items():
        target = destination / name
        if target.is_symlink():
            raise ValueError("The resume source cannot contain symbolic links.")
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, target)
    # Product and runner files stay fixed. Only the evaluation code changes.
    verify_frozen_sources({key: protocol[key] for key in
                           ("product_wrapper_hashes", "product_source_manifest", "product_source_hash")}, destination)
    old_runner = {row["path"]: row for row in protocol["runtime_source_manifest"]["files"] if row["path"].startswith("runner/")}
    from .runtime_sources import runtime_source_manifest
    runner = {row["path"]: row for row in runtime_source_manifest(destination)["files"] if row["path"].startswith("runner/")}
    if runner != old_runner:
        raise ValueError("The runner changed. Restore the original runner before resume.")
    return destination


def prepare_update(output, source, *, source_factory=updated_source):
    """Journal the update before replacing derived manifests. Repeated calls recover it."""
    with RecordStore(output) as store:
        update = store.read_manifest("resume_update")
        if update:
            frozen = json.loads(store.read_blob(update["after"]))
            resume_source = Path(update["source"])
            verify_source(frozen, resume_source)
            if update.get("applied"):
                return _prepare_repair(store, Path(source), source_factory)
        else:
            preview = update_preview(store)
            original = store.read_manifest("protocol")
            resume_source = source_factory(original, Path(source), Path(output).name)
            protocol = copy.deepcopy(original)
            for name in ("framework_source_hashes", "runtime_source_manifest", "runtime_source_hash",
                         "assessment_contract", "assessment_contract_hash", "protocol_hash"):
                protocol.pop(name, None)
            protocol["sample_protocol_hash"] = original.get("sample_protocol_hash", original["protocol_hash"])
            for settings in protocol["runtime"].values():
                settings["fast_mode"] = True
            frozen = freeze_protocol(protocol, resume_source)
            search = store.read_manifest("search_identity")
            if search:
                search = copy.deepcopy(search)
                search["identity"]["protocol"] = frozen
                search["hash"] = content_hash(search["identity"])
            slots = {}
            for path in sorted((store.root / "manifests").glob("trial-slot-*.json")):
                value = json.loads(path.read_bytes())
                slots[path.stem] = store.put_blob(value)
            update = {"before": store.put_blob(original), "after": store.put_blob(frozen),
                      "source": str(resume_source), "slots": slots, "search_identity": search,
                      "previous_search_identity": store.put_blob(store.read_manifest("search_identity")),
                      "preview": preview, "applied": False}
            store.write_manifest("resume_update", update)
        original = json.loads(store.read_blob(update["before"]))
        store.write_manifest("generation_protocol-" + original["protocol_hash"], original)
        for name, reference in update["slots"].items():
            slot = json.loads(store.read_blob(reference))
            slot["generation_protocol_hash"] = original["protocol_hash"]
            slot["previous_completed"] = slot.pop("completed", None)
            store.write_manifest(name, slot)
        if update["search_identity"]:
            store.write_manifest("search_identity", update["search_identity"])
        store.write_manifest("protocol", frozen)
        if not any(event["kind"] == "resume_runtime_updated" and event["data"].get("protocol_hash") == frozen["protocol_hash"]
                   for event in store.events()):
            store.append("resume_runtime_updated", {"protocol_hash": frozen["protocol_hash"],
                         "previous_protocol_hash": original["protocol_hash"], **update["preview"],
                         "before": update["before"], "after": update["after"]}, stage="preparation")
        update["applied"] = True
        store.write_manifest("resume_update", update)
        return frozen, resume_source
