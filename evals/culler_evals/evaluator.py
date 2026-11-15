"""Trial execution, Judge assessment, and GEPA score conversion."""

from __future__ import annotations

import difflib
import json
import statistics
import uuid
from pathlib import Path, PurePosixPath

from .assessment import AssessmentError, check_packet, require_inspection, required_sections, trial_score
from .calibration import bind_assessment_context
from .cases import CaseError, content_hash, prepare_case
from .protocol import DIMENSIONS, digest
from .trial_resources import capture_authority_state, capture_repositories, dispose_completed_repositories


class EvaluationError(RuntimeError):
    """An external failure leaves an evaluation incomplete."""


def original_directive(source: Path) -> tuple[str, dict]:
    directive = (source / "mc/verbs/directives/packager.md").read_text()
    writing = (source / "mc/verbs/skills/shared/writing/SKILL.md").read_text()
    if writing.startswith("---\n"):
        _, separator, body = writing[4:].partition("\n---\n")
        if not separator:
            raise EvaluationError("The writing skill frontmatter is invalid.")
        writing_body = body
    else:
        writing_body = writing
    text = directive + "\n\n" + writing_body
    return text, {"directive_hash": digest(directive), "writing_body_hash": digest(writing_body),
                  "merged_hash": digest(text), "separate_writing_skill": False}


def protected_requirements(source: Path) -> dict:
    text = (source / "mc/verbs/directives/packager.md").read_text()
    blocks = {}
    current = "Introduction"
    lines = []
    for line in text.splitlines(keepends=True):
        if line.startswith("## "):
            blocks[current] = "".join(lines)
            current = line[3:].strip()
            lines = []
        lines.append(line)
    blocks[current] = "".join(lines)
    names = ("Sources", "Reader", "Files changed", "Browser evidence", "Packet format", "Claims", "Terminal action", "Cases in the skill")
    return {"spans": [{"name": name, "text": blocks[name]} for name in names if name in blocks],
            "semantic_requirements": [
                "The role derives claims from durable records and preserves evidence limits.",
                "The role preserves the repository and its assigned branch.",
                "The role preserves the permitted commands and exactly one successful terminal action.",
                "The role preserves the application capture procedure.",
                "The packet preserves its format, required section order, and the selected directive's term and glossary rules.",
                "The packet serves the stated operator context and the selected directive's Reader requirements.",
                "The packet describes current Verifier gates and results without a Verification history section.",
                "The role reports unavailable required evidence through the infrastructure terminal.",
            ]}


class Evaluator:
    def __init__(self, protocol: dict, source: Path, store, runtime, *, cache: Path, credentials: dict | None = None):
        self.protocol = protocol
        self.source = source
        self.store = store
        self.runtime = runtime
        self.cache = cache
        self.credentials = credentials or {}

    def settings(self, role: str) -> dict:
        from .protocol import ProtocolError, verify_frozen_sources
        if any(name in self.protocol for name in ("framework_source_hashes", "product_wrapper_hashes", "product_source_manifest")):
            try:
                verify_frozen_sources(self.protocol, self.source)
            except ProtocolError as error:
                raise EvaluationError(str(error)) from error
        private = self.credentials.get(role, {})
        if not isinstance(private, dict) or set(private) - {"credentials"}:
            raise EvaluationError("The private runtime file can supply credentials only.")
        return self.protocol["runtime"][role] | private

    def verify_runtime_identity(self, result: dict, role: str, *, protocol: dict | None = None) -> None:
        protocol = self.protocol if protocol is None else protocol
        if not isinstance(result, dict):
            raise EvaluationError("The runtime result must contain one object.")
        metadata = result.get("metadata") or result.get("_runtime", {}).get("metadata", {})
        expected_hash = protocol.get("runtime_source_hash")
        if expected_hash and metadata.get("runtime_source_hash") != expected_hash:
            raise EvaluationError("The runtime source identity differs from the frozen protocol.")
        expected_hash = protocol.get("product_source_hash")
        if expected_hash and metadata.get("product_source_hash") != expected_hash:
            raise EvaluationError("The product source identity differs from the frozen protocol. Use the recorded protocol and source before continuation.")
        settings = protocol["runtime"][role]
        for name in ("channel", "base_url"):
            if name in settings and metadata.get(name) != settings[name]:
                raise EvaluationError("The actual credential transport differs from the frozen protocol.")
        if "effort" in settings:
            expected = settings["effort"]
            if any(metadata.get(name) != expected for name in ("requested_effort", "configured_effort")):
                raise EvaluationError("The runtime effort differs from the frozen protocol. Use the selected effort before continuation.")
            observed = metadata.get("observed_efforts")
            if observed is not None and observed != [expected]:
                raise EvaluationError("The runtime reports a different effort. Use the selected effort before continuation.")

    def _artifact(self, path) -> dict | None:
        path = Path(path) if path else None
        return self.store.put_blob(path.read_bytes()) if path and path.is_file() else None

    def _runtime_artifacts(self, result: dict) -> dict:
        return {item["type"] + ":" + str(index): self._artifact(item["path"])
                for index, item in enumerate(result.get("_runtime", {}).get("artifact_paths", []))}

    def _failure_artifacts(self, error: BaseException, result: dict | None = None) -> dict:
        artifacts = self._runtime_artifacts(result or {})
        for index, item in enumerate(getattr(error, "artifact_paths", [])):
            artifacts["failed:" + item.get("type", "file") + ":" + str(index)] = self._artifact(item["path"])
        return artifacts

    @staticmethod
    def _dimension_means(trials: list[dict]) -> dict:
        result = {}
        for name in DIMENSIONS:
            observed = [trial["dimensions"][name] for trial in trials if name in trial["dimensions"]]
            result[name] = statistics.mean(observed) if observed else None
        return result

    @staticmethod
    def _preservation_contract(contract: dict, metadata: dict | None) -> dict:
        metadata = metadata or {}
        changed = metadata.get("evidence_changed", [])
        if changed:
            contract["passed"] = False
            contract["failures"].append("The role altered its fixed evidence files.")
            contract["evidence_changed"] = changed
        if metadata.get("repository_changed") is True:
            contract["passed"] = False
            contract["failures"].append("The role altered its fixed repository during the trial.")
            contract["repository_changed"] = True
        return contract

    @staticmethod
    def _packet_contract(packet, case: dict, terminal: dict) -> dict:
        contract = check_packet(Path(packet), required_sections=required_sections(case)) if packet else {
            "passed": False, "failures": ["The required packet is absent."]}
        submitted = terminal.get("outputs")
        if submitted is not None and (not isinstance(submitted, str) or PurePosixPath(submitted).name != "packet.html"):
            contract["passed"] = False
            contract["failures"].append("The submitted packet filename must equal packet.html.")
        return contract

    def _sample_slot(self, candidate: dict, case: dict, stage: str, context: dict) -> str:
        scope = context.get("sample_scope", {"iteration": context.get("iteration", 0),
                                             "case_ids": context.get("case_ids", [case["id"]]),
                                             "dataset": context.get("dataset", stage)})
        return digest({"protocol": self.protocol.get("sample_protocol_hash", self.protocol.get("protocol_hash", digest(self.protocol))),
                       "candidate": candidate, "case": case, "stage": stage, "scope": scope,
                       "trial_number": context.get("trial_number")})

    def _retained_packet(self, trial_root: Path, reference: dict | None, filename: str) -> str | None:
        if reference is None:
            return None
        if not filename or PurePosixPath(filename).name != filename:
            raise EvaluationError("The retained packet requires its original filename.")
        directory = trial_root / "retained"
        directory.mkdir(mode=0o700, exist_ok=True)
        if directory.is_symlink():
            raise EvaluationError("The retained packet directory cannot be a symbolic link.")
        path = directory / filename
        body = self.store.read_blob(reference)
        if path.exists():
            if path.is_symlink() or path.read_bytes() != body:
                raise EvaluationError("The retained packet differs from its preserved artifact.")
        else:
            path.write_bytes(body)
            path.chmod(0o444)
        return str(path)

    def _generation_protocol(self, saved: dict) -> dict:
        if not saved.get("generation_protocol_hash"):
            return self.protocol
        original = self.store.read_manifest("generation_protocol-" + saved["generation_protocol_hash"])
        if (not original or original.get("product_source_hash") != self.protocol.get("product_source_hash")
                or original.get("seed_candidate") != self.protocol.get("seed_candidate")
                or original.get("case_hashes") != self.protocol.get("case_hashes")):
            raise EvaluationError("The retained packet requires its original generation protocol.")
        return original

    def _trial(self, candidate: dict, case: dict, stage: str, context: dict) -> dict:
        self.settings("packager")
        slot = self._sample_slot(candidate, case, stage, context)
        slot_name = "trial-slot-" + slot
        saved = self.store.read_manifest(slot_name)
        if saved and saved.get("slot") != slot:
            raise EvaluationError("The retained trial belongs to a different sample slot.")
        if saved and saved.get("completed"):
            row = json.loads(self.store.read_blob(saved["completed"]))
            self.verify_runtime_identity({"metadata": row.get("metadata")}, "packager", protocol=self._generation_protocol(saved))
            self._dispose_repositories(saved, row, case, stage)
            self.store.append("trial_reused", {"sample_slot": slot, "source_trial_id": row["trial_id"],
                                               "reason": "The fixed sample already completed."},
                              trial_id=row["trial_id"], case_id=case["id"], candidate_id=context.get("candidate_id"), stage=stage)
            return row
        trial_id = saved["trial_id"] if saved else "trial-" + uuid.uuid4().hex
        operation = self.store.begin_operation("trial", trial_id=trial_id, case_id=case["id"],
                                               stage=stage, dataset=context.get("dataset", stage),
                                               trial_number=context.get("trial_number"), trial_count=context.get("trial_count"),
                                               external_attempt=context.get("external_attempt"),
                                               candidate_id=context.get("candidate_id"), dependencies=context.get("dependencies", []))
        trial_root = self.store.root / "trials" / trial_id
        trial_root.mkdir(parents=True, exist_ok=bool(saved))
        runtime_result = None
        try:
            if saved:
                prepared = json.loads(self.store.read_blob(saved["prepared"]))
                runtime_result = json.loads(self.store.read_blob(saved["runtime_result"]))
                artifacts = saved["artifacts"]
                runtime_result["packet_path"] = self._retained_packet(trial_root, artifacts["packet_path"], saved["packet_filename"])
                self.store.append("packet_reused", {"sample_slot": slot, "source_trial_id": trial_id,
                                                    "source_operation_id": saved["source_operation_id"],
                                                    "packet": artifacts["packet_path"],
                                                    "reason": "The Judge reassesses the same produced packet."},
                                  trial_id=trial_id, case_id=case["id"], candidate_id=context.get("candidate_id"), stage=stage)
                if (saved.get("generation_protocol_hash") or self.store.read_manifest("record_recovery")) and not Path(prepared["repository_path"]).is_dir():
                    restored = prepare_case(case, trial_root / ("resume-evidence-" + uuid.uuid4().hex), self.cache, require_ready=True)
                    if restored["manifest_hash"] != prepared["manifest_hash"] or restored["references"] != prepared["references"]:
                        raise EvaluationError("The restored Judge evidence differs from the original case.")
                    prepared = restored
                    self.store.append("judge_evidence_restored", {"prepared": self.store.put_blob(prepared),
                                      "reason": "The Judge reassesses the retained packet against the same fixed case."},
                                      trial_id=trial_id, case_id=case["id"], stage=stage)
            else:
                prepared = prepare_case(case, trial_root / "case", self.cache, require_ready=True)
                expected_state = capture_authority_state(self.store, trial_id, prepared)
                runtime_result = self.runtime.execute_packager(prepared, candidate["packager_directive"], trial_root, self.settings("packager"))
                artifacts = {key: self._artifact(runtime_result.get(key)) for key in ("packet_path", "commands_path", "trace_path")}
                artifacts.update(self._runtime_artifacts(runtime_result))
                filename = Path(runtime_result["packet_path"]).name if runtime_result.get("packet_path") else "packet.html"
                runtime_result["packet_path"] = self._retained_packet(trial_root, artifacts["packet_path"], filename)
                saved = {"slot": slot, "trial_id": trial_id, "source_operation_id": operation,
                         "prepared": self.store.put_blob(prepared), "runtime_result": self.store.put_blob(runtime_result),
                         "artifacts": artifacts, "packet_filename": filename, "judge_attempts": 0, "completed": None,
                         "generation_external_attempt": context.get("external_attempt"),
                         "repositories": capture_repositories(self.store, trial_id, prepared, expected_state)}
                self.store.write_manifest(slot_name, saved)
                self.store.append("role_result_preserved", {"sample_slot": slot, "trial_id": trial_id,
                                                           "packet": artifacts["packet_path"], "terminal": runtime_result.get("terminal")},
                                  trial_id=trial_id, case_id=case["id"], candidate_id=context.get("candidate_id"), stage=stage)
            self.verify_runtime_identity(runtime_result, "packager", protocol=self._generation_protocol(saved))
            terminal = runtime_result.get("terminal")
            packet = runtime_result.get("packet_path")
            if not terminal or terminal.get("status") != "packaged" or not packet:
                score = {"quality": None, "contribution": 0.0, "contract_passed": False,
                         "critical_failures": [], "dimensions": {},
                         "feedback": ["The role failed to submit its required packet."], "trial_id": trial_id}
                judgment = None
                contract = {"passed": False, "failures": score["feedback"]}
            else:
                prepared["packet_summary"] = terminal.get("summary")
                contract = self._packet_contract(packet, case, terminal)
                self._preservation_contract(contract, runtime_result.get("metadata"))
                judge_operation = self.store.begin_operation("packet_assessment", trial_id=trial_id, case_id=case["id"],
                                                            stage=stage, candidate_id=context.get("candidate_id"), dependencies=[operation])
                judgment = None
                try:
                    for attempt in range(self.protocol["retry"]["assessment_attempts"]):
                        saved["judge_attempts"] += 1
                        self.store.write_manifest(slot_name, saved)
                        attempt_operation = self.store.begin_operation("judge_attempt", trial_id=trial_id, case_id=case["id"],
                                                                       stage=stage, judge_attempt=saved["judge_attempts"], dependencies=[judge_operation])
                        judgment = None
                        try:
                            judgment = self.runtime.assess_packet(prepared, packet, trial_root / f"assessment-{saved['judge_attempts']}",
                                                                  self.settings("judge"), self.protocol["rubric"] | {
                                                                      "operator_context": self.protocol["operator_context"]})
                            self.verify_runtime_identity(judgment, "judge")
                            require_inspection(judgment, contract)
                            judgment["assessment"] = {"id": attempt_operation, "trial_id": trial_id,
                                                      "case_id": case["id"], "reference_hash": content_hash(case["references"]),
                                                      "rubric_hash": digest(self.protocol["rubric"]),
                                                      "manifest_hash": prepared["manifest_hash"],
                                                      "packet": artifacts["packet_path"]}
                            if self.protocol.get("product_wrapper_hashes"):
                                bind_assessment_context(judgment, self.protocol)
                            score = trial_score(judgment, self.protocol["rubric"], contract) | {"trial_id": trial_id}
                            judgment["preserved_artifacts"] = self._runtime_artifacts(judgment)
                            self.store.end_operation(attempt_operation, "success", result=self.store.put_blob(judgment))
                            break
                        except (AssessmentError, RuntimeError, OSError) as exc:
                            self.store.end_operation(attempt_operation, "failed", error={"type": type(exc).__name__, "message": str(exc)},
                                                     result={"artifacts": self._failure_artifacts(exc, judgment),
                                                             "judgment": self.store.put_blob(judgment) if judgment else None},
                                                     absence_reasons={"valid_judgment": "The assessment attempt failed."})
                            if attempt + 1 == self.protocol["retry"]["assessment_attempts"]:
                                raise EvaluationError("The Judge assessment remains incomplete.") from exc
                    self.store.end_operation(judge_operation, "success", result=score)
                except BaseException as exc:
                    self.store.end_operation(judge_operation, "failed", error={"type": type(exc).__name__, "message": str(exc)},
                                             absence_reasons={"score": "The Judge assessment remains incomplete."})
                    raise
            row = score | {"case_id": case["id"], "stage": stage, "contract": contract,
                           "trial_number": context.get("trial_number"), "trial_count": context.get("trial_count"),
                           "external_attempt": context.get("external_attempt"),
                           "generation_external_attempt": saved["generation_external_attempt"], "sample_slot": slot,
                           "judgment": self.store.put_blob(judgment) if judgment else None,
                           "artifacts": artifacts, "terminal": terminal, "metadata": runtime_result.get("metadata"),
                           "limitations": runtime_result.get("limitations", [])}
            self.store.append("trial_assessed", row, trial_id=trial_id, case_id=case["id"],
                              candidate_id=context.get("candidate_id"), stage=stage)
            saved["completed"] = self.store.put_blob(row)
            self.store.write_manifest(slot_name, saved)
            self.store.end_operation(operation, "success", result=row,
                                     absence_reasons={key: "The role did not produce this artifact." for key, value in artifacts.items() if value is None})
        except BaseException as exc:
            self.store.end_operation(operation, "interrupted" if isinstance(exc, KeyboardInterrupt) else "failed",
                                     error={"type": type(exc).__name__, "message": str(exc)},
                                     result={"artifacts": self._failure_artifacts(exc, runtime_result)},
                                     absence_reasons={"score": "The trial did not complete."})
            raise
        self._dispose_repositories(saved, row, case, stage)
        return row

    def _dispose_repositories(self, saved: dict, row: dict, case: dict, stage: str):
        if self.store.read_manifest("record_recovery"):
            trial = self.store.root / "trials" / row["trial_id"]
            if not trial.exists() and not trial.is_symlink():
                return None
        probe = getattr(self.runtime, "repository_states", None)
        return dispose_completed_repositories(self.store, saved, row, case_id=case["id"], stage=stage,
                                              probe=(lambda paths: probe(paths, self.settings("packager"))) if probe else None,
                                              case_manifest=case)

    def evaluate(self, candidate: dict, cases: list[dict], stage: str, capture_traces: bool, context: dict | None = None) -> dict:
        context = context or {}
        phase = {"train": "training", "trainset": "training", "training_parent": "training", "training_child": "training",
                 "val": "validation", "valset": "validation"}.get(stage, stage)
        if phase == "final" and capture_traces:
            raise EvaluationError("Final cases cannot enter Optimizer reflection. Evaluate the final cases without reflection traces.")
        repeats = self.protocol["trials"][phase]
        if type(repeats) is not int or repeats <= 0:
            raise EvaluationError("The trial count must be a positive integer.")
        if any(case["expected_terminal"] != "packaged" for case in cases):
            raise EvaluationError("Contract cases cannot enter packet quality scores.")
        outputs, trajectories, scores = [], [], []
        for case in cases:
            trials = []
            for repetition in range(repeats):
                for attempt in range(self.protocol["retry"]["external_attempts"]):
                    try:
                        trials.append(self._trial(candidate, case, stage, context | {
                            "case_ids": [item["id"] for item in cases],
                            "trial_number": repetition + 1, "trial_count": repeats, "external_attempt": attempt + 1}))
                        break
                    except (CaseError, EvaluationError, RuntimeError, OSError) as exc:
                        self.store.barrier()
                        if attempt + 1 == self.protocol["retry"]["external_attempts"]:
                            raise EvaluationError(f"Case {case['id']} remains incomplete.") from exc
            result = {"case_id": case["id"], "mean": statistics.mean(trial["contribution"] for trial in trials),
                      "trials": trials, "dimensions": self._dimension_means(trials)}
            event = self.store.append("case_assessed", result, case_id=case["id"], stage=stage,
                                      dataset=context.get("dataset", phase), candidate_id=context.get("candidate_id"))
            outputs.append(result | {"event_id": event["id"]})
            scores.append(result["mean"] / 100)
            trajectories.append({"case_id": case["id"], "inputs": {"role_inputs": case["role_inputs"], "references": case["references"]},
                                 "feedback": self._reflection_feedback(trials),
                                 "output": {"case_id": case["id"], "trials": self._reflection_outputs(trials)},
                                 "trials": trials, "references": case["references"]} if capture_traces else None)
        return {"outputs": outputs, "scores": scores, "trajectories": trajectories if capture_traces else None}

    def _reflection_feedback(self, trials: list[dict]) -> list[dict]:
        feedback = []
        for trial in trials:
            reference = trial.get("judgment")
            judgment = json.loads(self.store.read_blob(reference)) if reference else {}
            feedback.append({
                "trial_id": trial["trial_id"], "quality": trial["quality"], "contribution": trial["contribution"],
                "contract_passed": trial["contract_passed"],
                "contract": {"passed": trial["contract"]["passed"], "failures": trial["contract"]["failures"]},
                "dimensions": {name: {key: dimension[key] for key in ("score", "reason", "evidence")}
                               for name, dimension in judgment.get("dimensions", {}).items()},
                "semantic_checks": [{key: check[key] for key in ("id", "passed", "reason", "evidence")}
                                    for check in judgment.get("semantic_checks", [])],
                "critical_failures": [{key: failure[key] for key in ("category", "reason", "evidence")}
                                      for failure in judgment.get("critical_failures", [])],
                "uninspected_content": list(judgment.get("uninspected_content", [])),
                "render_failures": [{key: failure[key] for key in ("kind", "section") if key in failure}
                                    for failure in judgment.get("_runtime", {}).get("render_failures", [])],
                "feedback": list(trial["feedback"]),
            })
        return feedback

    def _reflection_outputs(self, trials: list[dict]) -> list[dict]:
        outputs = []
        for trial in trials:
            reference = trial.get("judgment")
            judgment = json.loads(self.store.read_blob(reference)) if reference else {}
            runtime = judgment.get("_runtime", {})
            trace_path = runtime.get("browser", {}).get("trace_path")
            preserved = judgment.get("preserved_artifacts", {})
            trace = next((preserved.get(item["type"] + ":" + str(index))
                          for index, item in enumerate(runtime.get("artifact_paths", []))
                          if item.get("path") == trace_path), None)
            states = []
            if trace:
                for ordinal, line in enumerate(self.store.read_blob(trace).decode("utf-8").splitlines(), 1):
                    record = json.loads(line)
                    visible = record.get("visible", {})
                    if isinstance(visible.get("text"), str):
                        states.append({"ordinal": ordinal, "action": record.get("action"),
                                       "arguments": record.get("arguments", {}), "screenshot": record.get("screenshot"),
                                       "scroll_y": visible.get("scroll_y"), "viewport": visible.get("viewport"),
                                       "headings": visible.get("headings", []), "at_end": visible.get("at_end"),
                                       "visible_text": visible["text"]})
            outputs.append({"trial_id": trial["trial_id"],
                            "packet_sha256": (trial.get("artifacts", {}).get("packet_path") or {}).get("sha256"),
                            "terminal_status": (trial.get("terminal") or {}).get("status"),
                            "rendered_text_status": "supplied" if states else "unavailable",
                            "display_states": states,
                            "absence_reason": None if states else "This assessment preserves no browser-visible text snapshots."})
        return outputs

    def evaluate_pair(self, original: dict, candidate: dict, cases: list[dict], stage: str = "final") -> tuple[list[dict], list[dict]]:
        import random
        generator = random.Random(self.protocol["search"]["seed"])
        left, right = [], []
        repeats = self.protocol["trials"][stage]
        if type(repeats) is not int or repeats <= 0:
            raise EvaluationError("The trial count must be a positive integer.")
        if any(case["expected_terminal"] != "packaged" for case in cases):
            raise EvaluationError("Contract cases cannot enter packet quality comparisons.")
        for case in cases:
            collected = {"original": [], "candidate": []}
            first = generator.choice(("original", "candidate"))
            for repetition in range(repeats):
                order = [first, "candidate" if first == "original" else "original"]
                if repetition % 2:
                    order.reverse()
                rows = {}
                for attempt in range(self.protocol["retry"]["external_attempts"]):
                    operation = self.store.begin_operation("paired_trial", stage=stage, case_id=case["id"])
                    try:
                        for identity in order:
                            if identity in rows:
                                continue
                            rows[identity] = self._trial(original if identity == "original" else candidate, case, stage,
                                                        {"candidate_id": identity, "dependencies": [operation],
                                                         "sample_scope": {"original": digest(original), "candidate": digest(candidate),
                                                                          "case_ids": [item["id"] for item in cases]},
                                                         "trial_number": repetition + 1, "trial_count": repeats,
                                                         "external_attempt": attempt + 1})
                        self.store.end_operation(operation, "success", result={"sample_trial_ids": {
                            identity: row["trial_id"] for identity, row in rows.items()}})
                        for identity, row in rows.items():
                            collected[identity].append(row)
                        break
                    except (CaseError, EvaluationError, RuntimeError, OSError) as exc:
                        self.store.barrier()
                        self.store.end_operation(operation, "failed", error={"type": type(exc).__name__, "message": str(exc)},
                                                 result={"retained_sample_trial_ids": {identity: row["trial_id"] for identity, row in rows.items()},
                                                         "incomplete_counterparts": [identity for identity in order if identity not in rows]},
                                                 absence_reasons={"paired_sample": "The incomplete counterpart requires another attempt."})
                        if attempt + 1 == self.protocol["retry"]["external_attempts"]:
                            raise EvaluationError("The paired comparison remains incomplete.") from exc
            for identity, output in (("original", left), ("candidate", right)):
                trials = collected[identity]
                row = {"case_id": case["id"], "mean": statistics.mean(trial["contribution"] for trial in trials),
                       "trials": trials, "dimensions": self._dimension_means(trials)}
                output.append(row)
                self.store.append("paired_case_assessed", row, case_id=case["id"], candidate_id=identity, stage=stage)
        return left, right

    def evaluate_contract(self, candidate: dict, cases: list[dict], *, trial_count: int, stage: str = "contract") -> list[dict]:
        if type(trial_count) is not int or trial_count <= 0:
            raise EvaluationError("The contract trial count must be a positive integer.")
        results = []
        for case in cases:
            trials = []
            for repetition in range(trial_count):
                for attempt in range(self.protocol["retry"]["external_attempts"]):
                    trial_id = "trial-" + uuid.uuid4().hex
                    operation = self.store.begin_operation("contract_trial", trial_id=trial_id, case_id=case["id"], stage=stage,
                                                           trial_number=repetition + 1, trial_count=trial_count,
                                                           external_attempt=attempt + 1)
                    root = self.store.root / "trials" / trial_id
                    root.mkdir(parents=True)
                    result = None
                    try:
                        prepared = prepare_case(case, root / "case", self.cache, require_ready=True)
                        result = self.runtime.execute_packager(prepared, candidate["packager_directive"], root, self.settings("packager"))
                        self.verify_runtime_identity(result, "packager")
                        terminal = result.get("terminal")
                        actual = terminal.get("status") if terminal else None
                        passed = actual == case["expected_terminal"]
                        contract = {"passed": passed, "failures": [] if passed else ["The terminal result differs from the expected result."]}
                        if actual == "packaged":
                            packet = result.get("packet_path")
                            packet_contract = self._packet_contract(packet, case, terminal)
                            contract["passed"] = passed and packet_contract["passed"]
                            contract["failures"].extend(packet_contract["failures"])
                        self._preservation_contract(contract, result.get("metadata"))
                        passed = contract["passed"]
                        row = {"trial_id": trial_id, "case_id": case["id"], "expected_terminal": case["expected_terminal"],
                               "actual_terminal": actual, "terminal": terminal, "passed": passed,
                               "contract": contract,
                               "artifacts": self._runtime_artifacts(result), "quality_score": None,
                               "metadata": result.get("metadata")}
                        self.store.end_operation(operation, "success", result=row)
                        self.store.append("contract_trial_assessed", row, trial_id=trial_id, case_id=case["id"], stage=stage)
                        trials.append(row)
                        break
                    except (CaseError, EvaluationError, RuntimeError, OSError) as exc:
                        self.store.barrier()
                        self.store.end_operation(operation, "failed", error={"type": type(exc).__name__, "message": str(exc)},
                                                 result={"artifacts": self._failure_artifacts(exc, result)},
                                                 absence_reasons={"contract_result": "The contract trial did not complete."})
                        if attempt + 1 == self.protocol["retry"]["external_attempts"]:
                            raise EvaluationError("The contract trial remains incomplete.") from exc
            results.append({"case_id": case["id"], "trials": trials, "passed": all(row["passed"] for row in trials), "quality_score": None})
        return results


class Proposer:
    def __init__(self, evaluator: Evaluator):
        self.evaluator = evaluator

    @staticmethod
    def _semantic_feedback(reflective_dataset):
        normalized = json.loads(json.dumps(reflective_dataset))
        if isinstance(normalized, dict):
            for examples in normalized.values():
                if not isinstance(examples, list):
                    continue
                for example in examples:
                    if not isinstance(example, dict):
                        continue
                    example.pop("evaluation", None)
                    output = example.get("Generated Outputs")
                    if isinstance(output, dict):
                        output.pop("event_id", None)
        return normalized

    def propose(self, candidate, reflective_dataset, components_to_update, context):
        evaluator = self.evaluator
        if components_to_update != ["packager_directive"]:
            raise EvaluationError("The Optimizer can edit only the Packager directive.")
        task_id = context.get("proposal_task_id") or uuid.uuid4().hex
        attempt = context.get("attempt") or uuid.uuid4().hex
        root = evaluator.store.root / "proposals" / (task_id + "-" + attempt)
        slot = digest({"protocol": evaluator.protocol.get("protocol_hash", digest(evaluator.protocol)),
                       "candidate": candidate, "feedback": self._semantic_feedback(reflective_dataset),
                       "components": components_to_update, "task_id": task_id})
        slot_name = "proposal-slot-" + slot
        saved = evaluator.store.read_manifest(slot_name)
        if saved and saved.get("slot") != slot:
            raise EvaluationError("The retained proposal belongs to a different proposal slot.")
        request = evaluator.store.put_blob({"parent": candidate, "feedback": reflective_dataset,
                                            "protected": evaluator.protocol["protected"], "rubric": evaluator.protocol["rubric"]})
        if saved:
            evaluator.settings("optimizer")
            result = json.loads(evaluator.store.read_blob(saved["response"]))
            evaluator.verify_runtime_identity(result, "optimizer")
            evaluator.store.append("optimizer_response_reused", {"proposal_slot": slot, "source_response": saved["response"],
                                                                "source_request": saved["request"], "request": request,
                                                                "reason": "The fixed proposal already completed."},
                                   proposal_task_id=context.get("proposal_task_id"), stage="proposal")
        else:
            result = None
            for external_attempt in range(evaluator.protocol["retry"]["external_attempts"]):
                result = None
                operation = evaluator.store.begin_operation("optimizer_attempt", proposal_task_id=context.get("proposal_task_id"),
                                                            stage="proposal", external_attempt=external_attempt + 1,
                                                            dependencies=[context["operation_id"]] if context.get("operation_id") else [])
                try:
                    result = evaluator.runtime.propose(candidate["packager_directive"], reflective_dataset,
                                                        root / f"attempt-{external_attempt + 1}", evaluator.settings("optimizer"),
                                                        evaluator.protocol["rubric"] | {"protected": evaluator.protocol["protected"]})
                    evaluator.verify_runtime_identity(result, "optimizer")
                    if not isinstance(result, dict) or not isinstance(result.get("directive"), str) or not result["directive"].strip():
                        raise EvaluationError("The Optimizer must return the complete directive as nonempty text.")
                    saved = {"slot": slot, "request": request, "response": evaluator.store.put_blob(result), "edit": None}
                    evaluator.store.write_manifest(slot_name, saved)
                    evaluator.store.end_operation(operation, "success", result={"response": saved["response"],
                                                                                "artifacts": evaluator._runtime_artifacts(result)})
                    break
                except BaseException as error:
                    evaluator.store.end_operation(operation, "interrupted" if isinstance(error, KeyboardInterrupt) else "failed",
                                                 error={"type": type(error).__name__, "message": str(error)},
                                                 result={"request": request, "artifacts": evaluator._failure_artifacts(error, result)},
                                                 absence_reasons={"proposal": "The Optimizer attempt did not complete."})
                    if not isinstance(error, (EvaluationError, RuntimeError, OSError)) or external_attempt + 1 == evaluator.protocol["retry"]["external_attempts"]:
                        raise
        if isinstance(result, dict):
            result["preserved_artifacts"] = evaluator._runtime_artifacts(result)
        response = evaluator.store.put_blob(result)
        child = result.get("directive") if isinstance(result, dict) else None
        diff = "".join(difflib.unified_diff(candidate["packager_directive"].splitlines(True), child.splitlines(True),
                                            fromfile="parent", tofile="proposal")) if isinstance(child, str) else None
        artifacts = {"request": request, "response": response,
                     "directive": evaluator.store.put_blob(child) if isinstance(child, str) else None,
                     "diff": evaluator.store.put_blob(diff) if diff is not None else None}
        event = evaluator.store.append("optimizer_response_received", artifacts,
                                       proposal_task_id=context.get("proposal_task_id"), stage="proposal",
                                       dependencies=[context["operation_id"]] if context.get("operation_id") else [])
        evaluator.store.barrier()
        evaluator.verify_runtime_identity(result, "optimizer")
        if not isinstance(child, str) or not child.strip():
            raise EvaluationError("The Optimizer must return the complete directive as nonempty text.")
        protected = evaluator.protocol["protected"]
        failures = [span["name"] for span in protected["spans"] if span["text"] not in child]
        edit = {"permitted": not failures, "reasons": [f"The edit changes protected text: {name}." for name in failures]}
        operation = evaluator.store.begin_operation("edit_scope", proposal_task_id=context.get("proposal_task_id"),
                                                    stage="edit_scope", dependencies=[event["id"]])
        try:
            if saved.get("edit"):
                edit = json.loads(evaluator.store.read_blob(saved["edit"]))
                if edit.get("_runtime"):
                    evaluator.settings("judge")
                    evaluator.verify_runtime_identity(edit, "judge")
                evaluator.store.append("edit_scope_reused", {"proposal_slot": slot, "source_edit": saved["edit"]},
                                       proposal_task_id=context.get("proposal_task_id"), stage="edit_scope")
            elif not failures and child != candidate["packager_directive"]:
                for assessment_attempt in range(evaluator.protocol["retry"]["assessment_attempts"]):
                    assessment_operation = evaluator.store.begin_operation("edit_scope_attempt", proposal_task_id=context.get("proposal_task_id"),
                                                                           stage="edit_scope", external_attempt=assessment_attempt + 1,
                                                                           dependencies=[operation])
                    try:
                        edit = None
                        edit = evaluator.runtime.assess_edit(candidate["packager_directive"], child, protected,
                                                            evaluator.settings("judge") | {"trial_root": str(root / "scope" / f"attempt-{assessment_attempt + 1}")})
                        if isinstance(edit, dict):
                            edit["preserved_artifacts"] = evaluator._runtime_artifacts(edit)
                            evaluator.verify_runtime_identity(edit, "judge")
                        if not isinstance(edit, dict) or type(edit.get("permitted")) is not bool or not isinstance(edit.get("reasons"), list):
                            raise EvaluationError("The edit scope assessment requires a Boolean result and a reasons list.")
                        evaluator.store.end_operation(assessment_operation, "success", result=artifacts | {"semantic_result": edit})
                        break
                    except BaseException as error:
                        evaluator.store.end_operation(assessment_operation, "interrupted" if isinstance(error, KeyboardInterrupt) else "failed",
                                                     error={"type": type(error).__name__, "message": str(error)},
                                                     result=artifacts | {"scope_artifacts": evaluator._failure_artifacts(error, edit if isinstance(edit, dict) else None)},
                                                     absence_reasons={"scope_result": "The scope assessment attempt did not complete."})
                        if not isinstance(error, (EvaluationError, RuntimeError, OSError)) or assessment_attempt + 1 == evaluator.protocol["retry"]["assessment_attempts"]:
                            raise
            if not isinstance(edit, dict) or type(edit.get("permitted")) is not bool or not isinstance(edit.get("reasons"), list):
                raise EvaluationError("The edit scope assessment requires a Boolean result and a reasons list.")
            saved["edit"] = evaluator.store.put_blob(edit)
            evaluator.store.write_manifest(slot_name, saved)
            scope_result = artifacts | {"deterministic_failures": failures, "semantic_result": edit}
            evaluator.store.append("edit_scope_assessed", scope_result,
                                   proposal_task_id=context.get("proposal_task_id"), stage="edit_scope")
            evaluator.store.end_operation(operation, "success", result=scope_result)
        except BaseException as exc:
            evaluator.store.end_operation(operation, "interrupted" if isinstance(exc, KeyboardInterrupt) else "failed",
                                         error={"type": type(exc).__name__, "message": str(exc)},
                                         result=artifacts | {"scope_artifacts": evaluator._failure_artifacts(exc, edit if isinstance(edit, dict) else None)},
                                         absence_reasons={"scope_result": "The scope assessment did not complete."})
            raise
        return {"texts": {"packager_directive": child}, "hypothesis": result.get("hypothesis"),
                "scope": {"accepted": edit["permitted"], "reason": edit["reasons"]}}
