"""Loopback controls for one active experiment and its durable records."""

from __future__ import annotations

import copy
import datetime as dt
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import math
import os
from pathlib import Path
import re
import secrets
import signal
import subprocess
import sys
import threading
import time
import uuid
from urllib.parse import urlsplit

from .cases import CASE_TYPES, load_case
from .storage import durable_directory, backup_directory
from .continuation import prepare_update, recorded_source, repair_preview, update_preview
from .evaluator import original_directive, protected_requirements
from .protocol import (dataset_paths, digest, effort_choices, freeze_protocol, product_wrapper_hashes,
                       read_protocol, readiness, source_revision, verify_frozen_sources)
from .records import RecordStore
from .viewer import render_view, view_payload

BODY_LIMIT = 128 * 1024
BODY_DEPTH_LIMIT = 64
SET_NAMES = ("trainset", "valset", "final")


def _field(path, label, kind, **rules):
    return {"path": path, "label": label, "type": kind, **rules}


def parameter_schema():
    fields = []
    for role in ("packager", "judge", "optimizer"):
        for name, label in (("model", "Model identifier"),
                            ("image", "Container image"), ("channel", "Credential channel")):
            fields.append(_field(f"runtime.{role}.{name}", label, "string"))
        fields.append(_field(f"runtime.{role}.harness", "Harness", "string", enum=["codex", "claude"]))
        fields.append(_field(f"runtime.{role}.fast_mode", "Fast mode", "boolean", default=False,
                             description="This requests faster service at higher usage cost. The model and effort stay the same. Claude Code requires a supported model and account."))
        fields.append(_field(f"runtime.{role}.effort", "Effort", "string",
                             harness_path=f"runtime.{role}.harness",
                             enum_by_harness={harness: list(effort_choices(harness)) for harness in ("codex", "claude")},
                             description="This sets the requested effort. Available levels depend on the selected model and harness."))
    for name in ("width", "height"):
        fields.append(_field(f"runtime.judge.viewport.{name}", f"Judge viewport {name}", "integer", minimum=1))
    for phase in ("training", "validation", "confirmation", "final"):
        fields.append(_field(f"trials.{phase}", f"{phase.capitalize()} trials", "integer", minimum=1))
    for name, label in (("proposal_count", "Proposals per iteration"), ("minibatch_size", "Training cases per minibatch"),
                        ("concurrency", "Concurrent operations")):
        fields.append(_field(f"search.{name}", label, "integer", minimum=1))
    fields.append(_field("search.seed", "Random seed", "integer", minimum=0))
    fields.append(_field("search.sampler", "Training case sampler", "string", enum=["epoch_shuffled"], read_only=True))
    fields.append(_field("search.skip_perfect", "Skip perfect training scores", "boolean"))
    for name, label in (("max_iterations", "Maximum iterations"), ("stagnation_iterations", "Iterations without a gain")):
        fields.append(_field(f"search.completion.{name}", label, "integer", minimum=1, nullable=True))
    fields.append(_field("search.completion.minimum_gain", "Gain that resets stagnation", "number", minimum=0, maximum=100))
    fields.append(_field("search.confirmation.enabled", "Confirm stagnation gains", "boolean"))
    for name, label in (("external_attempts", "External operation attempts"), ("assessment_attempts", "Judge assessment attempts")):
        fields.append(_field(f"retry.{name}", label, "integer", minimum=1))
    for name, label, kind, minimum in (("minimum_gain", "Required final gain", "number", 0),
                                      ("bootstrap_samples", "Bootstrap samples", "integer", 1),
                                      ("minimum_groups", "Minimum independent final groups", "integer", 2),
                                      ("maximum_interval_width", "Maximum interval width", "number", 0)):
        fields.append(_field(f"final_decision.{name}", label, kind, minimum=minimum,
                             **({"maximum": 100} if kind == "number" else {})))
    for name, label in CASE_TYPES.items():
        fields.append(_field(f"final_decision.case_type_regressions.{name}", f"Allowed regression: {label.lower()}",
                             "number", minimum=0, maximum=100))
    return fields


def _finite(value):
    try:
        return math.isfinite(value)
    except OverflowError:
        return False


def _get(value, path):
    for key in path.split("."):
        value = value[key]
    return value


def _set(value, path, item):
    keys = path.split(".")
    for key in keys[:-1]:
        value = value.setdefault(key, {})
    value[keys[-1]] = copy.deepcopy(item)


def _leaves(value, path=""):
    pending = [(path, value)]
    while pending:
        current, item = pending.pop()
        if isinstance(item, dict) and item:
            for key, child in item.items():
                if not isinstance(key, str):
                    yield current
                else:
                    pending.append((f"{current}.{key}" if current else key, child))
        else:
            yield current


class ControlServer:
    def __init__(self, protocol: Path, source: Path, credentials: Path | None, cache: Path,
                 output_root: Path, *, port: int = 0, preset: str | None = None,
                 process_factory=None, docker_runner=None, group_signal=None):
        self.source = Path(source).resolve()
        self.credentials = Path(credentials).resolve() if credentials else None
        self.cache = Path(cache).resolve()
        self.output_root = durable_directory(output_root)
        self.protocol_path = Path(protocol).resolve()
        self._base = read_protocol(self.protocol_path)
        _, self._source_input = original_directive(self.source)
        if self._base.get("source_revision") == "checkout":
            self._base["source_revision"] = source_revision(self.source)
        self._base.setdefault("product_wrapper_hashes", product_wrapper_hashes(self.source))
        if not self._base.get("protected", {}).get("spans"):
            self._base["protected"] = protected_requirements(self.source)
        self.schema = parameter_schema()
        self.defaults = {}
        for field in self.schema:
            try:
                value = _get(self._base, field["path"])
            except KeyError:
                value = field.get("default")
            _set(self.defaults, field["path"], value)
        self._cases, self._case_paths, self._case_sets, self.catalog = {}, {}, {}, []
        self.defaults["datasets"] = {partition: [] for partition in SET_NAMES}
        self._preset_entries = self._preset_catalog()
        available = [self._base] + [read_protocol(self.protocol_path.parent / entry["protocol_file"])
                                   for entry in self._preset_entries]
        for index, protocol in enumerate(available):
            for partition in SET_NAMES:
                for path in dataset_paths(protocol, partition):
                    case = load_case(path)
                    identifier = case["id"]
                    if index == 0:
                        self.defaults["datasets"][partition].append(identifier)
                    if identifier in self._cases:
                        if index == 0 or self._case_sets[identifier] != partition or self._cases[identifier] != case:
                            raise ValueError("Each available case must keep its content and configured set across presets.")
                        continue
                    self._cases[identifier], self._case_paths[identifier] = case, str(path)
                    self._case_sets[identifier] = partition
                    self.catalog.append({"id": identifier, "title": case["role_inputs"]["task"]["title"],
                                         "types": case["types"], "group": case["group"], "default_set": partition})
        self._process_factory = process_factory or subprocess.Popen
        self._docker_runner = docker_runner or subprocess.run
        self._group_signal = group_signal or os.killpg
        self._lock = threading.RLock()
        self._runs = {}
        self._current = None
        self.presets = self._load_presets()
        self.default_preset = self._matching_preset(self.defaults)
        if preset is not None:
            selected = next((record for record in self.presets if record["id"] == preset), None)
            if selected is None:
                if self.presets:
                    raise ValueError("Select a saved preset: " + ", ".join(record["id"] for record in self.presets) + ".")
                raise ValueError("Remove --preset for a configuration without saved presets.")
            self.defaults = copy.deepcopy(selected["parameters"])
            self.default_preset = selected["id"]
        self.prefix = "/" + secrets.token_urlsafe(24) + "/"
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def reply(self, status, data, content_type="application/json; charset=utf-8"):
                if not isinstance(data, bytes):
                    data = json.dumps(data, allow_nan=False).encode()
                self.send_response(status)
                self.send_header("Content-Type", content_type)
                self.send_header("Content-Length", str(len(data)))
                self.send_header("Cache-Control", "no-store")
                self.send_header("X-Content-Type-Options", "nosniff")
                self.send_header("Referrer-Policy", "no-referrer")
                self.send_header("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src 'self' blob: data:; frame-src 'self' blob:; frame-ancestors 'self'")
                self.end_headers()
                self.wfile.write(data)

            def resource(self):
                path = urlsplit(self.path).path
                host = f"127.0.0.1:{owner.server.server_port}"
                if self.headers.get("Host") != host or not path.startswith(owner.prefix):
                    self.reply(404, {"error": "The view address is unknown."})
                    return None
                return path[len(owner.prefix):]

            def do_GET(self):
                resource = self.resource()
                if resource is None:
                    return
                if resource in ("", "index.html"):
                    try:
                        page = (Path(__file__).resolve().parents[1] / "web/control.html").read_bytes()
                    except OSError:
                        return self.reply(503, {"error": "The experiment control page is unavailable."})
                    return self.reply(200, page, "text/html; charset=utf-8")
                if resource == "config.json":
                    return self.reply(200, owner.configuration())
                if resource == "status.json":
                    try:
                        return self.reply(200, owner.status())
                    except (OSError, RuntimeError, ValueError):
                        return self.reply(503, {"error": "The control status is unavailable. Check the local output directory."})
                experiment, route = owner.experiment_route(resource)
                if experiment is not None:
                    try:
                        with RecordStore(experiment, read_only=True) as store:
                            if not store.read_manifest("protocol"):
                                return self.reply(503, {"error": "The experiment has not created its fixed records yet."})
                            if route in ("", "index.html"):
                                return self.reply(200, render_view(view_payload(store, live=True)).encode(), "text/html; charset=utf-8")
                            if route == "records.json":
                                return self.reply(200, view_payload(store, live=True))
                            if re.fullmatch(r"blobs/[0-9a-f]{64}", route):
                                checksum = route.split("/")[1]
                                data = store.read_blob({"sha256": checksum})
                                if hashlib.sha256(data).hexdigest() != checksum:
                                    raise RuntimeError("The artifact content hash differs from its address.")
                                return self.reply(200, data, "application/octet-stream")
                    except (RuntimeError, OSError, ValueError):
                        return self.reply(503, {"error": "The experiment records are temporarily unavailable."})
                self.reply(404, {"error": "The requested resource is absent."})

            def do_POST(self):
                resource = self.resource()
                if resource is None:
                    return
                if self.headers.get("Origin") != owner.origin:
                    return self.reply(403, {"error": "Use the experiment page to change its configuration."})
                if resource not in ("validate.json", "start.json", "resume.json", "select.json"):
                    return self.reply(404, {"error": "The requested action is absent."})
                if self.headers.get_content_type() != "application/json" or self.headers.get("Transfer-Encoding"):
                    return self.reply(400, {"error": "Send one JSON configuration object."})
                try:
                    length = int(self.headers.get("Content-Length", ""))
                except ValueError:
                    return self.reply(411, {"error": "Set the request content length."})
                if not 0 < length <= BODY_LIMIT:
                    return self.reply(413, {"error": "The configuration request exceeds the supported size."})
                self.connection.settimeout(10)
                try:
                    body = json.loads(self.rfile.read(length), parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
                    pending = [(body, 0)]
                    while pending:
                        value, depth = pending.pop()
                        if depth > BODY_DEPTH_LIMIT:
                            raise ValueError()
                        children = value.values() if isinstance(value, dict) else value if isinstance(value, list) else ()
                        pending.extend((child, depth + 1) for child in children)
                    expected = {"run_id", "update_runtime"} if resource == "resume.json" else {"run_id"} if resource == "select.json" else {"parameters"}
                    if not isinstance(body, dict) or set(body) != expected:
                        raise ValueError()
                except (ValueError, OSError, RecursionError):
                    return self.reply(400, {"error": "Send an object with one parameters field."})
                if resource == "validate.json":
                    return self.reply(200, owner.validate(body["parameters"]))
                try:
                    if resource == "resume.json":
                        status, result = owner.resume(body["run_id"], body["update_runtime"])
                    elif resource == "select.json":
                        status, result = owner.select(body["run_id"])
                    else:
                        status, result = owner.start(body["parameters"])
                except (OSError, RuntimeError, ValueError):
                    return self.reply(503, {"error": "The experiment could not preserve its launch records. Check the local output directory."})
                self.reply(status, result)

        self.server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
        self.server.daemon_threads = True
        self.origin = f"http://127.0.0.1:{self.server.server_port}"
        self.url = self.origin + self.prefix
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self._restore_runs()

    def _restore_runs(self):
        for output in sorted(self.output_root.glob("*/manifests/browser_launch.json")):
            run_id = output.parent.parent.name
            if not re.fullmatch(r"[a-zA-Z0-9-]+", run_id) or output.is_symlink():
                continue
            receipt = self.output_root / ".control" / run_id / "launch.json"
            if not receipt.exists():
                with RecordStore(output.parent.parent, read_only=True) as store:
                    launch = store.read_manifest("browser_launch")
                    protocol = store.read_manifest("protocol")
                receipt.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                self._private_write(receipt.parent / "protocol.json", json.dumps(protocol).encode())
                self._private_write(receipt, json.dumps(launch).encode())
        for path in sorted((self.output_root / ".control").glob("*/launch.json")):
            try:
                if path.is_symlink() or path.parent.is_symlink():
                    continue
                saved = json.loads(path.read_bytes())
                run_id = path.parent.name
                output = self.output_root / run_id
                if (not re.fullmatch(r"[a-zA-Z0-9-]+", run_id) or saved.get("run_id") != run_id
                        or Path(saved["output"]).resolve() != output or output.is_symlink()
                        or not (output / "manifests/protocol.json").is_file()):
                    continue
                run = {"run_id": run_id, "output": output, "config": path.parent / "protocol.json",
                       "log": None, "process": None, "state": saved["state"], "message": saved["message"],
                       "experiment_url": self.url + "runs/" + run_id + "/", "records_ready": True,
                       "exit_code": saved.get("exit_code"), "parameters": saved["parameters"],
                       "source": Path(saved.get("source", self.source)), "restored": True}
                self._runs[run_id] = run
                self._current = run_id
            except (KeyError, ValueError, OSError):
                continue

    def select(self, run_id):
        with self._lock:
            if not isinstance(run_id, str) or run_id not in self._runs:
                return 404, {"error": "The experiment is unavailable."}
            self._current = run_id
            return 200, self.status()

    def _resume_options(self, run):
        if run["state"] not in ("failed", "interrupted") or not run["records_ready"]:
            return {"available": False}
        try:
            with RecordStore(run["output"], read_only=True) as store:
                protocol = store.read_manifest("protocol")
                archive = store.read_manifest("source_archive")
                if archive and archive["protocol_hash"] == protocol["protocol_hash"]:
                    recorded_source(protocol, self.source, store=store, recover=False)
                    return {"available": True, "update_runtime": False,
                            "note": "Resume uses the preserved source and fixed settings. Completed trials remain available."}
                update = store.read_manifest("resume_update")
                if update:
                    if update.get("applied"):
                        pending = store.read_manifest("framework_repair")
                        repair = pending["preview"] if pending and not pending.get("applied") else repair_preview(store, self.source)
                        if repair:
                            return {"available": True, "update_runtime": True, **repair,
                                    "note": "Resume repairs repository disposal. It reuses completed Judge scores and preserves the original cleanup records."}
                    return {"available": True, "update_runtime": not update.get("applied"),
                            "note": "Continue the same experiment with its recorded runtime update."}
                try:
                    verify_frozen_sources(protocol, self.source)
                    same = protocol["source_revision"] == source_revision(self.source)
                except (ValueError, RuntimeError, OSError):
                    same = False
                if same:
                    return {"available": True, "update_runtime": False,
                            "note": "Continue the same experiment. Reuse its completed trials and fixed settings."}
                preview = update_preview(store)
                return {"available": True, "update_runtime": True, **preview,
                        "note": f"Reuse {preview['packets_retained']} packets. The Judge scores them again. Resume requests fast mode and validated JavaScript Object Notation (JSON) responses."
                                " The original directive, cases, rubric, models, and effort levels stay fixed."}
        except (ValueError, RuntimeError, OSError) as error:
            try:
                with RecordStore(run["output"], read_only=True) as store:
                    recorded_source(store.read_manifest("protocol"), self.source, store=store, recover=False)
                return {"available": True, "update_runtime": False,
                        "note": "Resume uses the original runtime and settings. A runtime update is unavailable after initial validation."}
            except (ValueError, RuntimeError, OSError, subprocess.SubprocessError):
                return {"available": False, "note": self._safe_error(error)}

    def configuration(self):
        return {"storage": {"experiments": str(self.output_root), "backups": str(backup_directory(self.output_root).parent)},
                "defaults": copy.deepcopy(self.defaults), "schema": copy.deepcopy(self.schema),
                "presets": copy.deepcopy(self.presets), "default_preset": self.default_preset,
                "cases": copy.deepcopy(self.catalog), "fixed": {
                    "role": self._base["role"], "source_revision": self._base["source_revision"],
                    "assessment_status": self._base.get("assessment_status", "calibrated"),
                    "operator_context": self._base["operator_context"],
                    "model_names": {settings["model"]: settings.get("model_name", settings["model"])
                                    for settings in self._base["runtime"].values()},
                    "rubric_dimensions": list(self._base["rubric"]["dimensions"]),
                    "human_review_required": True}}

    def _preset_catalog(self):
        reference = self._base.get("presets_file")
        if reference is None:
            return []
        root = self.protocol_path.parent
        path = (root / reference).resolve() if isinstance(reference, str) else None
        if path is None or path.parent != root:
            raise ValueError("Use a preset catalog in the experiment configuration directory.")
        catalog = json.loads(path.read_text())
        if (not isinstance(catalog, dict) or catalog.get("version") != 1
                or not isinstance(catalog.get("presets"), list) or not catalog["presets"]):
            raise ValueError("Use a preset catalog with version 1 and a preset list.")
        identifiers = set()
        for entry in catalog["presets"]:
            if (not isinstance(entry, dict) or
                    any(not isinstance(entry.get(key), str) or not entry[key].strip()
                        for key in ("id", "label", "description", "purpose", "protocol_file"))):
                raise ValueError("Each preset requires its identifier, label, description, purpose, and protocol file.")
            if entry["id"] in identifiers:
                raise ValueError("Each preset requires a distinct identifier.")
            identifiers.add(entry["id"])
            path = (root / entry["protocol_file"]).resolve()
            if path.parent != root:
                raise ValueError("Use a preset protocol in the experiment configuration directory.")
        return catalog["presets"]

    def _load_presets(self):
        records = []
        base = read_protocol(self.protocol_path)
        controls = {field["path"] for field in self.schema}
        for entry in self._preset_entries:
            path = self.protocol_path.parent / entry["protocol_file"]
            protocol = read_protocol(path)
            for key in ("version", "role", "source_revision", "operator_context", "rubric", "assessment_status",
                        "calibration", "protected"):
                if protocol.get(key) != base.get(key):
                    raise ValueError("Each preset must preserve the source, rubric, and fixed assessment requirements.")
            for role in ("packager", "judge", "optimizer"):
                for key, value in protocol["runtime"][role].items():
                    if f"runtime.{role}.{key}" not in controls:
                        if value != base["runtime"][role].get(key):
                            raise ValueError("Each preset must preserve runtime settings outside the browser controls.")
            parameters = {}
            for field in self.schema:
                _set(parameters, field["path"], _get(protocol, field["path"]) if not field["path"].endswith(".fast_mode")
                     else protocol["runtime"][field["path"].split(".")[1]].get("fast_mode", False))
            parameters["datasets"] = {partition: [load_case(item)["id"] for item in dataset_paths(protocol, partition)]
                                      for partition in SET_NAMES}
            errors = self._parameter_errors(parameters)
            if errors:
                raise ValueError("A saved preset contains invalid parameters. Check its configuration.")
            candidate = copy.deepcopy(self._base)
            for field in self.schema:
                _set(candidate, field["path"], _get(parameters, field["path"]))
            datasets = {partition: [self._cases[identifier] for identifier in parameters["datasets"][partition]]
                        for partition in SET_NAMES}
            if readiness(candidate, datasets):
                raise ValueError("A saved preset fails experiment readiness. Check its cases and parameters.")
            records.append({key: entry[key] for key in ("id", "label", "description", "purpose")} |
                           {"parameters": parameters})
        return records

    def _matching_preset(self, parameters):
        return next((record["id"] for record in self.presets if record["parameters"] == parameters), None)

    def _parameter_errors(self, parameters):
        errors = []
        if not isinstance(parameters, dict):
            return [{"field": "parameters", "message": "Use one complete parameter object."}]
        known = {field["path"] for field in self.schema} | {f"datasets.{name}" for name in SET_NAMES}
        for path in _leaves(parameters):
            if path not in known:
                errors.append({"field": path, "message": "This field is fixed or unsupported."})
        for field in self.schema:
            path = field["path"]
            try:
                value = _get(parameters, path)
            except (KeyError, TypeError):
                errors.append({"field": path, "message": "Set this parameter."})
                continue
            if value is None and field.get("nullable"):
                continue
            kind = field["type"]
            valid = (type(value) is int if kind == "integer" else
                     type(value) in (float, int) and _finite(value) if kind == "number" else
                     type(value) is bool if kind == "boolean" else
                     isinstance(value, str) and bool(value.strip()) and not any(ord(char) < 32 for char in value))
            if not valid:
                errors.append({"field": path, "message": f"Use a valid {kind} value."})
                continue
            if "minimum" in field and value < field["minimum"]:
                errors.append({"field": path, "message": f"Use a value of at least {field['minimum']}."})
            if "maximum" in field and value > field["maximum"]:
                errors.append({"field": path, "message": f"Use a value of at most {field['maximum']}."})
            if "enum" in field and value not in field["enum"]:
                errors.append({"field": path, "message": "Select one supported value."})
            if "enum_by_harness" in field:
                try:
                    harness = _get(parameters, field["harness_path"])
                except (KeyError, TypeError):
                    harness = None
                choices = field["enum_by_harness"].get(harness, []) if isinstance(harness, str) else []
                if value not in choices:
                    action = ("Select an effort for the selected harness: " + ", ".join(choices) + "." if choices
                              else "Select a supported harness before you set effort.")
                    errors.append({"field": path, "message": action})
        for partition in SET_NAMES:
            try:
                cases = parameters["datasets"][partition]
            except (KeyError, TypeError):
                cases = None
            if (not isinstance(cases, list) or not cases or
                    any(not isinstance(identifier, str) or identifier not in self._cases for identifier in cases)):
                errors.append({"field": f"datasets.{partition}", "message": "Select available cases for this set."})
            elif any(self._case_sets[identifier] != partition for identifier in cases):
                errors.append({"field": f"datasets.{partition}", "message": "Keep each case in its configured set. Select or remove cases within that set."})
        return errors

    def _resolved(self, parameters):
        protocol = copy.deepcopy(self._base)
        if self.presets:
            identifier = self._matching_preset(parameters)
            record = next((item for item in self.presets if item["id"] == identifier), None)
            protocol["preset"] = ({key: record[key] for key in ("id", "label", "purpose")} if record
                                  else {"id": "custom", "label": "Custom", "purpose": "custom"})
            protocol["preset"]["parameters_hash"] = digest(parameters)
        for field in self.schema:
            _set(protocol, field["path"], _get(parameters, field["path"]))
        for role in ("packager", "judge", "optimizer"):
            if protocol["runtime"][role]["model"] != self._base["runtime"][role]["model"]:
                protocol["runtime"][role]["model_name"] = protocol["runtime"][role]["model"]
        for identifier, path in self._case_paths.items():
            if digest(load_case(path)) != digest(self._cases[identifier]):
                raise ValueError("An available case changed after the control page opened. Reopen the control server for the current cases.")
        if protocol["search"]["confirmation"]["enabled"]:
            protocol["search"]["confirmation"].update(reference="original", counter_policy="reset_on_confirmed_gain")
        protocol["datasets"] = {name: [self._case_paths[identifier] for identifier in parameters["datasets"][name]]
                                for name in SET_NAMES}
        protocol.pop("rubric_file", None)
        for name in ("record", "check_record"):
            reference = protocol.get("calibration", {}).get(name)
            if isinstance(reference, dict) and isinstance(reference.get("path"), str):
                reference["path"] = str((self.protocol_path.parent / reference["path"]).resolve())
        datasets = {name: [self._cases[identifier] for identifier in parameters["datasets"][name]] for name in SET_NAMES}
        errors = readiness(protocol, datasets)
        if errors:
            return None, [{"field": "protocol", "message": message} for message in errors]
        directive, source_input = original_directive(self.source)
        if source_input != self._source_input:
            raise ValueError("The Packager directive or writing skill changed after the control page opened. Reopen the control server for the current source.")
        protocol["seed_candidate"] = {"packager_directive": directive}
        protocol["case_hashes"] = {name: {case["id"]: digest(case) for case in cases} for name, cases in datasets.items()}
        return freeze_protocol(protocol, self.source), []

    def _safe_error(self, error):
        message = str(error)
        for path in (self.credentials, self.cache, self.output_root, self.source, self.protocol_path):
            if path:
                message = message.replace(str(path), "[private path]")
        return message

    def _validation(self, parameters):
        errors = self._parameter_errors(parameters)
        frozen = None
        if not errors:
            try:
                frozen, errors = self._resolved(parameters)
            except (ValueError, RuntimeError, OSError, subprocess.CalledProcessError) as error:
                errors = [{"field": "protocol", "message": self._safe_error(error)}]
        result = {"valid": not errors, "errors": errors}
        if not errors:
            result["parameters"] = copy.deepcopy(parameters)
            counts = {name: len(parameters["datasets"][name]) for name in SET_NAMES}
            search, trials = parameters["search"], parameters["trials"]
            iterations = search["completion"]["max_iterations"]
            maximum = None
            if iterations is not None:
                per_proposal = search["minibatch_size"] * trials["training"] * 2 + counts["valset"] * trials["validation"]
                if search["confirmation"]["enabled"]:
                    per_proposal += counts["valset"] * trials["confirmation"] * 2
                maximum = (counts["valset"] * trials["validation"] +
                           iterations * search["proposal_count"] * per_proposal + counts["final"] * trials["final"] * 2)
            result["summary"] = {"case_counts": counts, "maximum_packets_before_retries": maximum,
                                 "maximum_proposals": None if iterations is None else iterations * search["proposal_count"]}
        return result, frozen

    def validate(self, parameters):
        with self._lock:
            return self._validation(parameters)[0]

    def _update(self, run):
        process = run["process"]
        if process is None and run.get("restored"):
            try:
                with RecordStore(run["output"]):
                    pass
            except RuntimeError:
                run.update(state="running", records_ready=True, exit_code=None,
                           message="Another process runs this experiment. Its live records remain available.")
                return
        code = process.poll() if process is not None else run.get("exit_code") if run.get("exit_code") is not None else 127
        records_ready, outcome = False, None
        try:
            with RecordStore(run["output"], read_only=True) as store:
                records_ready = bool(store.read_manifest("protocol"))
                completed = [event for event in store.events() if event["kind"] == "run_session_completed"]
                outcome = completed[-1]["data"].get("outcome") if completed else None
        except (RuntimeError, ValueError, OSError):
            pass
        run["records_ready"], run["exit_code"] = records_ready, code
        if code is None:
            run["state"] = "running" if records_ready else "starting"
            run["message"] = "The experiment runs with its fixed parameters." if records_ready else "The experiment starts and creates its fixed records."
        else:
            if run["log"] is not None and not run["log"].closed:
                run["log"].close()
            run["state"] = ("complete" if outcome == "success" and (code == 0 or run.get("restored")) else
                            "interrupted" if outcome == "interrupted" or code in (130, -signal.SIGINT) else "failed")
            run["message"] = ("The experiment completed. Inspect its tree and candidates." if run["state"] == "complete" else
                              "The experiment was interrupted. Its records remain available." if run["state"] == "interrupted" else
                              "The experiment failed. Inspect its available records and the private run log.")
        self._write_launch_record(run)

    def _public_run(self, run):
        result = {key: copy.deepcopy(run[key]) for key in (
            "state", "message", "run_id", "experiment_url", "records_ready", "exit_code", "parameters")}
        result["resume"] = self._resume_options(run)
        return result

    def status(self):
        with self._lock:
            for run in self._runs.values():
                self._update(run)
            current = self._runs.get(self._current)
            result = self._public_run(current) if current else {
                "state": "ready", "message": "Configure an experiment, then start it.", "run_id": None,
                "experiment_url": None, "records_ready": False, "exit_code": None, "parameters": None}
            result["history"] = [self._public_run(run) for run in self._runs.values()]
            return result

    def _write_launch_record(self, run):
        path = run["config"].with_name("launch.json")
        value = self._public_run(run) | {"output": str(run["output"]), "protocol": str(run["config"]),
                                       "source": str(run.get("source", self.source)),
                                       "pid": getattr(run["process"], "pid", None)}
        identity = digest(value)
        if run.get("saved_identity") != identity:
            self._private_write(path, json.dumps(value, indent=2).encode())
            run["saved_identity"] = identity

    @staticmethod
    def _private_write(path, data):
        temporary = path.with_name(path.name + ".tmp")
        with open(temporary, "wb", opener=lambda name, flags: os.open(name, flags, 0o600)) as target:
            target.write(data)
            target.flush()
            os.fsync(target.fileno())
        temporary.chmod(0o600)
        temporary.replace(path)
        descriptor = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)

    def start(self, parameters):
        with self._lock:
            for current in self._runs.values():
                self._update(current)
                if current["state"] in ("running", "starting"):
                    return 409, {"error": "One experiment already runs. Inspect its tree before you start another."}
            validation, frozen = self._validation(parameters)
            if not validation["valid"]:
                return 422, validation
            run_id = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d-%H%M%S-") + uuid.uuid4().hex[:12]
            self.output_root.mkdir(parents=True, exist_ok=True, mode=0o700)
            control = self.output_root / ".control" / run_id
            control.mkdir(parents=True, mode=0o700)
            control.parent.chmod(0o700)
            output = self.output_root / run_id
            if output.exists() or output.is_symlink():
                raise ValueError("The experiment output directory already exists. Start with a fresh output directory.")
            config = control / "protocol.json"
            self._private_write(config, json.dumps(frozen, indent=2).encode())
            log = open(control / "session.log", "wb", opener=lambda name, flags: os.open(name, flags, 0o600))
            argv = [sys.executable, "-m", "culler_evals.cli", "run", "--protocol", str(config),
                    "--source", str(self.source), "--output", str(output), "--cache", str(self.cache),
                    "--no-browser", "--no-wait"]
            if self.credentials is not None:
                argv += ["--credentials", str(self.credentials)]
            run = {"run_id": run_id, "output": output, "config": config, "log": log, "process": None, "source": self.source,
                   "state": "starting", "message": "The experiment starts and creates its fixed records.",
                   "experiment_url": self.url + "runs/" + run_id + "/", "records_ready": False,
                   "exit_code": None, "parameters": copy.deepcopy(parameters)}
            try:
                self._write_launch_record(run)
            except BaseException:
                log.close()
                raise
            self._runs[run_id], self._current = run, run_id
            try:
                process = self._process_factory(argv, cwd=self.source / "evals", stdin=subprocess.DEVNULL,
                                                stdout=log, stderr=subprocess.STDOUT, start_new_session=True,
                                                env=os.environ | {"CULLER_EVAL_CONTROL_RUN": run_id,
                                                    "CULLER_EVAL_CONTROL_CANCEL_FILE": str(control / "stop")})
            except (ValueError, OSError, subprocess.SubprocessError) as error:
                log.write(("The experiment process could not start: " + type(error).__name__ + "\n").encode())
                log.close()
                process = None
            run["process"] = process
            try:
                self._write_launch_record(run)
            except BaseException:
                if process is not None and process.poll() is None:
                    self._interrupt_run(run)
                log.close()
                raise
            result = self.status()
            if process is None:
                result["error"] = "The experiment process could not start. Its private launch records remain available."
                return 503, result
            return 202, result

    def resume(self, run_id, update_runtime):
        with self._lock:
            if not isinstance(run_id, str) or run_id not in self._runs or type(update_runtime) is not bool:
                return 400, {"error": "Select a recorded experiment and its displayed resume option."}
            for current in self._runs.values():
                self._update(current)
                if current["state"] in ("running", "starting"):
                    return 409, {"error": "One experiment already runs. Resume waits until that experiment stops."}
            run = self._runs[run_id]
            if run["state"] not in ("failed", "interrupted"):
                return 409, {"error": "Resume requires a failed or interrupted experiment."}
            try:
                if update_runtime:
                    frozen, source = prepare_update(run["output"], self.source)
                else:
                    with RecordStore(run["output"]) as store:
                        frozen = store.read_manifest("protocol")
                        source = recorded_source(frozen, self.source, store=store)
            except (ValueError, RuntimeError, OSError, subprocess.SubprocessError) as error:
                return 422, {"error": self._safe_error(error)}
            parameters = copy.deepcopy(run["parameters"])
            for field in self.schema:
                if field["path"].startswith("runtime."):
                    try:
                        _set(parameters, field["path"], _get(frozen, field["path"]))
                    except KeyError:
                        _set(parameters, field["path"], field.get("default"))
            self._private_write(run["config"], json.dumps(frozen, indent=2).encode())
            stop = run["config"].parent / "stop"
            if stop.exists() or stop.is_symlink():
                stop.unlink()
            log = open(run["config"].with_name("session.log"), "ab", opener=lambda name, flags: os.open(name, flags, 0o600))
            argv = [sys.executable, "-m", "culler_evals.cli", "run", "--protocol", str(run["config"]),
                    "--source", str(source), "--output", str(run["output"]), "--cache", str(self.cache),
                    "--resume", "--no-browser", "--no-wait"]
            if self.credentials is not None:
                argv += ["--credentials", str(self.credentials)]
            run.update(log=log, process=None, state="starting", message="The experiment resumes from its retained records.",
                       exit_code=None, source=source, parameters=parameters, restored=False)
            self._current = run_id
            try:
                self._write_launch_record(run)
            except BaseException:
                log.close()
                raise
            try:
                run["process"] = self._process_factory(argv, cwd=source / "evals", stdin=subprocess.DEVNULL,
                    stdout=log, stderr=subprocess.STDOUT, start_new_session=True,
                    env=os.environ | {"CULLER_EVAL_CONTROL_RUN": run_id, "CULLER_EVAL_CONTROL_CANCEL_FILE": str(stop)})
            except (ValueError, OSError, subprocess.SubprocessError):
                log.close()
                run["process"] = None
                run["exit_code"] = 127
                self._write_launch_record(run)
                return 503, {"error": "The resume process could not start. The experiment records remain available."}
            try:
                self._write_launch_record(run)
            except BaseException:
                if run["process"] is not None and run["process"].poll() is None:
                    self._interrupt_run(run)
                log.close()
                raise
            return 202, self.status()

    def experiment_route(self, resource):
        with self._lock:
            if resource.startswith("experiment/") and self._current:
                return self._runs[self._current]["output"], resource[len("experiment/"):]
            match = re.fullmatch(r"runs/([a-zA-Z0-9-]+)/(.*)", resource)
            if match and match[1] in self._runs:
                return self._runs[match[1]]["output"], match[2]
            return None, None

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *_):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        with self._lock:
            for run in self._runs.values():
                process = run["process"]
                if process is not None and process.poll() is None:
                    self._interrupt_run(run)
                self._update(run)

    def _remove_control_containers(self, run):
        label = "org.culler.evals.control-run"
        expected = run["run_id"]
        try:
            result = self._docker_runner(["docker", "ps", "--all", "--quiet", "--no-trunc", "--filter", f"label={label}={expected}"],
                                         capture_output=True, text=True, timeout=5)
            if result.returncode:
                raise RuntimeError("The experiment container list is unavailable.")
            identifiers = result.stdout.split()
            if any(re.fullmatch(r"[0-9a-f]{64}", identifier) is None for identifier in identifiers):
                raise RuntimeError("The experiment container list contains an invalid identifier.")
            for identifier in dict.fromkeys(identifiers):
                inspected = self._docker_runner(["docker", "inspect", "--format", "{{json .Config.Labels}}", identifier],
                                                 capture_output=True, text=True, timeout=5)
                if inspected.returncode:
                    if self._container_absent(identifier):
                        continue
                    raise RuntimeError("The experiment container identity is unavailable.")
                labels = json.loads(inspected.stdout)
                if not isinstance(labels, dict) or labels.get(label) != expected:
                    raise RuntimeError("A container label differs from this experiment. The container remains intact.")
                removed = self._docker_runner(["docker", "rm", "--force", identifier], capture_output=True, text=True, timeout=5)
                if removed.returncode and not self._container_absent(identifier):
                    raise RuntimeError("An experiment container could not be removed.")
        except (OSError, ValueError, subprocess.SubprocessError) as error:
            raise RuntimeError("Experiment container cleanup remains unverified. Inspect its private launch record.") from error

    def _container_absent(self, identifier):
        result = self._docker_runner(["docker", "ps", "--all", "--quiet", "--no-trunc", "--filter", f"id={identifier}"],
                                     capture_output=True, text=True, timeout=5)
        return result.returncode == 0 and not result.stdout.strip()

    def _interrupt_run(self, run):
        process = run["process"]
        marker_error = None
        try:
            self._private_write(run["config"].with_name("stop"), b"The control server interrupted this experiment.\n")
        except OSError as error:
            marker_error = error
        try:
            self._group_signal(process.pid, signal.SIGINT)
        except (OSError, AttributeError):
            process.send_signal(signal.SIGINT)
        deadline = time.monotonic() + 30
        cleanup_error = None
        while True:
            try:
                self._remove_control_containers(run)
                cleanup_error = None
            except RuntimeError as error:
                cleanup_error = error
            if process.poll() is not None:
                process.wait(timeout=1)
                try:
                    self._remove_control_containers(run)
                    cleanup_error = None
                except RuntimeError as error:
                    cleanup_error = error
                break
            if time.monotonic() >= deadline:
                raise RuntimeError("The experiment did not stop after the interrupt. Its process remains active; inspect the private launch record.")
            time.sleep(.1)
        if marker_error is not None:
            raise RuntimeError("The control server could not save its interrupt record. Inspect the private launch record.") from marker_error
        if cleanup_error is not None:
            raise cleanup_error

    def wait(self):
        try:
            while self.thread.is_alive():
                self.thread.join(1)
        except KeyboardInterrupt:
            return
