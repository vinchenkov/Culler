"""Portable experiment views from the durable journal."""

from __future__ import annotations

import json
import os
import shutil
from pathlib import Path

from .records import RecordError


def view_payload(store, *, live: bool = False) -> dict:
    corpus = store.read_manifest("case_corpus", {})
    if not corpus:
        for partition, rows in store.read_manifest("case_display", {}).get("cases", {}).items():
            corpus[partition] = []
            for row in rows:
                try:
                    prepared = json.loads(store.read_blob(row["prepared"]))
                except (KeyError, OSError, ValueError, RecordError):
                    continue
                if prepared.get("manifest_hash") == row.get("manifest_hash") and prepared.get("case_id") == row.get("id"):
                    corpus[partition].append({"id": row["id"], "title": row["title"],
                                              "group": prepared["group_id"], "types": prepared["types"]})
    cases = {name: [{key: case[key] for key in ("id", "group", "types") if key in case}
                    | {"title": case.get("role_inputs", {}).get("task", {}).get("title")
                       or case.get("title") or case["id"]}
                    for case in values] for name, values in corpus.items()}
    events = store.events()
    batches = {}
    model_events = []
    for event in events:
        metadata = event["data"].get("metadata") or {}
        if event["kind"] in ("optimizer_response_received", "role_result_preserved"):
            if event["kind"] == "optimizer_response_received":
                runtime_reference = event["data"].get("response")
            else:
                slot = store.read_manifest("trial-slot-" + event["data"]["sample_slot"], {})
                runtime_reference = slot.get("runtime_result")
            try:
                runtime_result = json.loads(store.read_blob(runtime_reference)) if runtime_reference else {}
                metadata = runtime_result.get("metadata") or runtime_result.get("_runtime", {}).get("metadata", {})
            except (KeyError, OSError, ValueError, RecordError):
                metadata = {}
        if isinstance(metadata, dict) and metadata.get("runtime_source_hash") and metadata.get("harness") and metadata.get("effective_models"):
            model_events.append(event["id"])
        result = event["data"].get("result") if event["kind"] == "operation_completed" else None
        reference = result.get("batch") if isinstance(result, dict) else None
        if not reference:
            continue
        try:
            batch = json.loads(store.read_blob(reference))
        except (OSError, ValueError, RecordError):
            continue
        rows = batch.get("outputs", []) if isinstance(batch, dict) else []
        batches[event["id"]] = {row["case_id"]: {
            "event_id": row.get("event_id"),
            "trial_count": len(row["trials"]) if isinstance(row.get("trials"), list) else None,
            "trial_ids": [trial["trial_id"] for trial in (row.get("trials") or [])
                          if isinstance(trial, dict) and trial.get("trial_id")],
        } for row in rows if isinstance(row, dict) and row.get("case_id")}
    return {"version": 1, "experiment_id": store.experiment_id, "events": events,
            "protocol": store.read_manifest("protocol", {}), "completeness": store.completeness(),
            "cases": cases, "evaluation_batches": batches, "model_execution_events": model_events, "live": live}


def render_view(payload: dict) -> str:
    template = (Path(__file__).parent.parent / "web" / "experiment.html").read_text()
    data = json.dumps(payload, ensure_ascii=False).replace("<", "\\u003c")
    return template.replace("__EXPERIMENT_DATA__", data)


def _replace(path: Path, data: str) -> None:
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(data)
    os.replace(temporary, path)


def export_view(store, destination: Path, *, include_artifacts: bool = True) -> Path:
    destination.mkdir(parents=True, exist_ok=True)
    payload = view_payload(store)
    target = destination / "index.html"
    _replace(target, render_view(payload))
    _replace(destination / "records.json", json.dumps(payload, indent=2))
    if include_artifacts:
        shutil.copytree(store.root / "blobs", destination / "blobs", dirs_exist_ok=True)
    return target


def index_mlflow(store, tracking_uri: str) -> str:
    """MLflow indexes the portable records; it does not own the evidence."""
    import mlflow
    from urllib.parse import urlparse
    parsed = urlparse(tracking_uri)
    if parsed.scheme not in ("file", "sqlite", "http", "https"):
        raise ValueError("Use a local MLflow location.")
    if parsed.scheme in ("http", "https") and parsed.hostname not in ("127.0.0.1", "localhost", "::1"):
        raise ValueError("MLflow must use a local service.")
    mlflow.set_tracking_uri(tracking_uri)
    name = "Culler directive evaluation"
    if mlflow.get_experiment_by_name(name) is None:
        mlflow.create_experiment(name, artifact_location=(store.root / "mlflow-artifacts").resolve().as_uri())
    mlflow.set_experiment(name)
    events = store.events()
    with mlflow.start_run(run_name=store.experiment_id) as run:
        mlflow.set_tags({"experiment_id": store.experiment_id, "record_version": "1",
                         "record_complete": str(store.completeness()["complete"])})
        mlflow.log_artifact(str(store.root / "events.jsonl"), "portable-records")
        for path in (store.root / "manifests").glob("*.json"):
            mlflow.log_artifact(str(path), "portable-records/manifests")
        mlflow.log_artifacts(str(store.root / "blobs"), "portable-records/blobs")
        final = store.read_manifest("final_comparison", {}).get("report", {})
        if final:
            mlflow.set_tag("final_status", final["status"])
            for metric in ("original_mean", "candidate_mean", "gain", "independent_groups", "trials_per_directive"):
                if final.get(metric) is not None:
                    mlflow.log_metric(metric, final[metric])
        for candidate_id, candidate in store.candidates().items():
            with mlflow.start_run(run_name=candidate_id, nested=True):
                mlflow.set_tags({"candidate_id": candidate_id, "status": candidate.get("status", "unknown"),
                                 "parent_ids": json.dumps(candidate.get("parent_ids", []))})
                mlflow.log_dict(candidate, "candidate.json")
                for event in events:
                    if event.get("candidate_id") != candidate_id:
                        continue
                    data = event["data"]
                    if event["kind"] == "validation_scored":
                        mlflow.log_metric("validation_mean", data["mean"] * 100, step=event["sequence"])
                        for case_id, score in data["scores"].items():
                            mlflow.log_metric("validation_case/" + case_id, score * 100, step=event["sequence"])
                    elif event["kind"] == "case_assessed":
                        for dimension, score in data.get("dimensions", {}).items():
                            if score is not None:
                                mlflow.log_metric(event.get("stage", "assessment") + "/" + dimension,
                                                  score, step=event["sequence"])
        return run.info.run_id
