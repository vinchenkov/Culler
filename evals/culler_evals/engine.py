"""Native GEPA search with role-independent evaluation and durable evidence."""

from __future__ import annotations

from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from copy import deepcopy
from dataclasses import dataclass
import difflib
import importlib.metadata
import math
import os
from pathlib import Path
from typing import Any
import uuid

import gepa
from gepa.core.adapter import EvaluationBatch
from gepa.core.state import GEPAState
from gepa.gepa_utils import remove_dominated_programs
from gepa.strategies.proposal_sampling import IndependentSampling
from gepa.strategies.proposal_selection import AllImprovements

from .records import RecordError, RecordStore, content_hash, json_value


GEPA_VERSION = "0.1.4"


class SearchError(RuntimeError):
    """An incomplete search cannot produce a qualified candidate."""


@dataclass
class SearchResult:
    best_candidate_id: str
    best_candidate: dict[str, str]
    candidates: dict[str, dict]
    validation_scores: dict[str, dict]
    native_result: Any
    search_complete: bool
    completion_reason: str

    def to_dict(self) -> dict:
        return {"best_candidate_id": self.best_candidate_id, "best_candidate": self.best_candidate,
                "candidates": self.candidates, "validation_scores": self.validation_scores,
                "native_result": self.native_result.to_dict(), "search_complete": self.search_complete,
                "completion_reason": self.completion_reason}


def parent_probabilities(state: GEPAState) -> dict[int, float]:
    """Use native frontier pruning, then the native case-lead frequencies."""
    front = remove_dominated_programs(state.get_pareto_front_mapping(), state.per_program_tracked_scores)
    frequencies = Counter(index for leaders in front.values() for index in leaders)
    total = sum(frequencies.values())
    return {index: frequencies[index] / total if total else 0.0 for index in range(len(state.program_candidates))}


def _case_ids(cases: list[dict]) -> list[str]:
    return [str(case["id"]) for case in cases]


def _batch(result: Any, size: int, capture_traces: bool) -> EvaluationBatch:
    if isinstance(result, EvaluationBatch):
        batch = result
    else:
        batch = EvaluationBatch(**{key: result[key] for key in (
            "outputs", "scores", "trajectories", "objective_scores", "num_metric_calls"
        ) if key in result})
    if len(batch.outputs) != size or len(batch.scores) != size:
        raise SearchError("The evaluator returned a different number of outputs or scores than requested cases.")
    if any(isinstance(score, bool) or not isinstance(score, (int, float)) or not math.isfinite(score)
           or not 0 <= score <= 1 for score in batch.scores):
        raise SearchError("Every case score must be finite and between zero and one.")
    if capture_traces and batch.trajectories is not None and len(batch.trajectories) != size:
        raise SearchError("The evaluator returned misaligned trajectories.")
    return batch


class _QuietLogger:
    def log(self, message):
        pass


class _Sampling(IndependentSampling):
    def __init__(self, n: int, adapter):
        super().__init__(n)
        self.adapter = adapter

    def sample_tasks(self, state, candidate_selector, batch_sampler, trainset):
        adapter = self.adapter
        adapter.barrier()
        adapter.selector = candidate_selector
        adapter.sampler = batch_sampler
        if adapter.restored_random is not None:
            candidate_selector.rng.setstate(adapter.restored_random["selector"])
            batch_sampler.__dict__.update(adapter.restored_random["sampler"])
            if adapter.restored_random.get("shared_rng", True):
                batch_sampler.rng = candidate_selector.rng
            else:
                batch_sampler.rng.setstate(adapter.restored_random["sampler_rng"])
            adapter.restored_random = None
        probabilities = parent_probabilities(state)
        tasks = super().sample_tasks(state, candidate_selector, batch_sampler, trainset)
        adapter.tasks = []
        attempt = uuid.uuid4().hex[:12]
        for index, task in enumerate(tasks):
            task_id = f"{state.i + 1}-{index}"
            parent_id = adapter.pool[task.parent_idx]
            operation_id = adapter.store.begin_operation(
                "proposal_task", stage="search", dataset="trainset", iteration=state.i + 1,
                proposal_task_id=task_id, parent_ids=[parent_id], attempt=attempt,
                dependencies=adapter.validation_events.get(parent_id, []))
            context = {"proposal_task_id": task_id, "iteration": state.i + 1,
                       "operation_id": operation_id, "attempt": attempt, "parent_ids": [parent_id],
                       "parent_candidate": deepcopy(task.parent_candidate), "cases": _case_ids(task.minibatch),
                       "native_minibatch_ids": list(task.minibatch_ids), "reflection": "pending"}
            adapter.tasks.append(context)
            adapter.store.append("parent_selected", {"probabilities": {
                adapter.pool[idx]: value for idx, value in probabilities.items()},
                "case_ids": context["cases"], "native_minibatch_ids": list(task.minibatch_ids)},
                candidate_id=parent_id, proposal_task_id=task_id, iteration=state.i + 1,
                operation_id=operation_id, stage="search", dataset="trainset")
        adapter.store.barrier()
        return tasks


class _Selection(AllImprovements):
    def __init__(self, adapter):
        self.adapter = adapter

    def select(self, proposals, state, acceptance_criterion):
        adapter = self.adapter
        adapter.barrier()
        selected = super().select(proposals, state, acceptance_criterion)
        accepted_ids = {id(proposal) for proposal in selected}
        seen = set()
        for proposal in proposals:
            task = adapter.task(proposal.metadata["proposal_id"])
            before = list(proposal.subsample_scores_before or [])
            after = list(proposal.subsample_scores_after or [])
            key = content_hash(proposal.candidate)
            if id(proposal) not in accepted_ids:
                outcome = "minibatch_rejected"
            elif key in seen:
                outcome = "duplicate"
            else:
                outcome = "minibatch_accepted"
                seen.add(key)
            task["selection"] = outcome
            event = adapter.store.append("minibatch_decision", {
                "outcome": outcome, "rule": "strict_improvement", "rule_version": GEPA_VERSION,
                "parent_scores": before, "child_scores": after,
                "parent_total": sum(before), "child_total": sum(after),
                "case_ids": task["cases"], "parent_evaluation": task.get("parent_evaluation"),
                "child_evaluation": task.get("child_evaluation"),
            }, candidate_id=task["candidate_id"], parent_ids=task["parent_ids"],
                proposal_task_id=task["proposal_task_id"], iteration=state.i + 1,
                stage="training", dataset="trainset", operation_id=task["operation_id"])
            task["decision_event"] = event["id"]
            adapter.store.append("candidate_status", {"status": outcome}, candidate_id=task["candidate_id"],
                                 stage="training", dataset="trainset", iteration=state.i + 1)
            if outcome != "minibatch_accepted":
                adapter.store.end_operation(task["operation_id"], "rejected", result={"decision_event": event["id"]},
                    absence_reasons={"validation": "Native selection excluded this proposal."})
                task["complete"] = True
        adapter.store.barrier()
        return selected


class _Adapter:
    propose_new_texts = None

    def __init__(self, protocol, trainset, valset, evaluator, proposer, store, resume):
        self.protocol, self.evaluator, self.proposer, self.store = protocol, evaluator, proposer, store
        self.trainset, self.valset = trainset, valset
        self.component = protocol.get("component", "packager_directive")
        self.seed = deepcopy(protocol["seed_candidate"])
        self.concurrency = protocol["search"]["concurrency"]
        self.pool = {0: "D0"}
        self.validation_events = {}
        self.tasks = []
        self.reflective_context = {}
        self.evaluation_batches = {}
        self.selector = self.sampler = None
        self.restored_random = None
        self.stop_state = {"best": None, "without_gain": 0, "last_iteration": -1,
                           "confirmed_candidate_id": None, "confirmed_score": None}
        self.fatal: BaseException | None = None
        self.resume = resume
        self.completion_reason = ""
        self.propose_new_texts = self._propose
        if resume:
            for event in store.events():
                if event["kind"] == "pool_candidate":
                    self.pool[event["data"]["native_index"]] = event["candidate_id"]
                elif event["kind"] == "validation_scored":
                    self.validation_events[event["candidate_id"]] = [event["id"]]

    def barrier(self):
        self.store.barrier()
        if self.fatal is not None:
            raise SearchError("An infrastructure failure left the search incomplete.") from self.fatal

    def task(self, task_id):
        return next(task for task in self.tasks if task["proposal_task_id"] == task_id)

    def get_adapter_state(self):
        self.barrier()
        random_state = None
        if self.selector is not None:
            random_state = {"selector": self.selector.rng.getstate(),
                            "sampler": deepcopy({key: value for key, value in self.sampler.__dict__.items() if key != "rng"}),
                            "shared_rng": self.sampler.rng is self.selector.rng,
                            "sampler_rng": self.sampler.rng.getstate()}
        return {"pool": deepcopy(self.pool), "validation_events": deepcopy(self.validation_events),
                "random": random_state, "stop_state": deepcopy(self.stop_state)}

    def set_adapter_state(self, state):
        if state:
            self.pool = state["pool"]
            self.validation_events = state["validation_events"]
            self.restored_random = state.get("random")
            self.stop_state = state["stop_state"]

    def _context(self, candidate, cases, capture_traces):
        ids = _case_ids(cases)
        validation = ids == _case_ids(self.valset)
        if validation:
            if candidate == self.seed and not self.tasks:
                candidate_id = "D0"
                task = None
            else:
                task = next((task for task in self.tasks if task.get("selection") == "minibatch_accepted"
                             and task.get("texts") == candidate and not task.get("validation_started")), None)
                if task is None:
                    raise SearchError("A validation request does not identify an accepted candidate.")
                task["validation_started"] = True
                candidate_id = task["candidate_id"]
            return {"candidate_id": candidate_id, "stage": "validation", "dataset": "valset",
                    "case_ids": ids, "proposal_task_id": task["proposal_task_id"] if task else None,
                    "iteration": task["iteration"] if task else 0,
                    "dependencies": [task["decision_event"]] if task else []}
        children = [task for task in self.tasks if task.get("texts") == candidate
                    and task.get("scope_accepted") and task["cases"] == ids and not task.get("child_started")]
        if children:
            task = children[0]
            task["child_started"] = True
            return {"candidate_id": task["candidate_id"], "stage": "training_child", "dataset": "trainset",
                    "case_ids": ids, "proposal_task_ids": [task["proposal_task_id"]],
                    "iteration": task["iteration"], "dependencies": [task["scope_event"]]}
        parents = [task for task in self.tasks if task["parent_candidate"] == candidate and task["cases"] == ids]
        if not parents:
            raise SearchError("A training evaluation has no matching proposal task.")
        return {"candidate_id": parents[0]["parent_ids"][0], "stage": "training_parent", "dataset": "trainset",
                "case_ids": ids, "proposal_task_ids": [task["proposal_task_id"] for task in parents],
                "iteration": parents[0]["iteration"], "dependencies": [task["operation_id"] for task in parents]}

    def evaluate(self, batch, candidate, capture_traces=False):
        context = self._context(candidate, batch, capture_traces)
        return self._evaluate(batch, candidate, capture_traces, context)

    def batch_evaluate(self, items):
        self.barrier()
        contexts = [self._context(candidate, batch, True) for candidate, batch in items]
        if self.concurrency == 1 or len(items) <= 1:
            return [self._evaluate(batch, candidate, True, context)
                    for (candidate, batch), context in zip(items, contexts, strict=True)]
        with ThreadPoolExecutor(max_workers=self.concurrency) as executor:
            futures = [executor.submit(self._evaluate, batch, candidate, True, context)
                       for (candidate, batch), context in zip(items, contexts, strict=True)]
            return [future.result() for future in futures]

    def _evaluate(self, cases, candidate, capture_traces, context):
        self.barrier()
        task_ids = context.get("proposal_task_ids", [context.get("proposal_task_id")])
        context = context | {"sample_scope": content_hash({
            "stage": context["stage"], "candidate": candidate, "iteration": context.get("iteration", 0),
            "case_ids": _case_ids(cases), "proposal_task_ids": sorted(task_id for task_id in task_ids if task_id),
        })}
        if self.resume and context["candidate_id"] == "D0" and context["stage"] == "validation" and not self.tasks:
            saved = self.store.read_manifest("seed_evaluation")
            if saved is not None:
                self.store.append("evaluation_reused", {"operation_id": saved["operation_id"],
                                  "reason": "Native initialization precedes checkpoint restoration."}, **context)
                return _batch(saved["batch"], len(cases), capture_traces)
        operation_id = self.store.begin_operation("evaluation", **context)
        try:
            self.barrier()
            result = self.evaluator.evaluate(deepcopy(candidate), deepcopy(cases), context["stage"],
                                             capture_traces, {**context, "operation_id": operation_id})
            self.barrier()
            evaluated = _batch(result, len(cases), capture_traces)
            raw = json_value(evaluated)
            artifact = self.store.put_blob(raw)
            event = self.store.end_operation(operation_id, "success", result={"batch": artifact,
                "scores": list(evaluated.scores), "case_ids": _case_ids(cases)})
            self.evaluation_batches[id(evaluated)] = {"context": context, "cases": deepcopy(cases),
                                                    "operation_id": operation_id, "event_id": event["id"]}
            if context["stage"] == "validation":
                self.validation_events[context["candidate_id"]] = [event["id"]]
                if context["candidate_id"] == "D0":
                    self.store.write_manifest("seed_evaluation", {"operation_id": operation_id, "batch": raw})
            else:
                key = "parent_evaluation" if context["stage"] == "training_parent" else "child_evaluation"
                for task_id in context["proposal_task_ids"]:
                    self.task(task_id)[key] = {"operation_id": operation_id, "event_id": event["id"]}
            return evaluated
        except BaseException as exc:
            self.fatal = exc
            if self.store._failure is None:
                self.store.end_operation(operation_id, "failed", error=exc,
                    absence_reasons={"case_score": "An infrastructure failure prevented a complete assessment."})
            raise

    def make_reflective_dataset(self, candidate, eval_batch, components_to_update):
        self.barrier()
        evidence = self.evaluation_batches[id(eval_batch)]
        task = next(task for task in self.tasks if task["parent_candidate"] == candidate
                    and task["cases"] == evidence["context"]["case_ids"] and task["reflection"] == "pending")
        task["reflection"] = "prepared"
        examples = []
        for case, output, score, trajectory in zip(evidence["cases"], eval_batch.outputs, eval_batch.scores,
                                                   eval_batch.trajectories or [], strict=True):
            trace = trajectory if isinstance(trajectory, dict) else {"feedback": trajectory}
            examples.append({"Inputs": trace.get("inputs", {"case_id": case["id"]}),
                             "Generated Outputs": trace.get("output", output),
                             "Feedback": trace.get("feedback", trace), "score": score,
                             "case_id": case["id"], "evaluation": task["parent_evaluation"]})
        dataset = {component: deepcopy(examples) for component in components_to_update}
        self.reflective_context[id(dataset)] = task
        return dataset

    def _propose(self, candidate, reflective_dataset, components_to_update):
        self.barrier()
        task = self.reflective_context[id(reflective_dataset)]
        context = {key: task[key] for key in ("proposal_task_id", "iteration", "parent_ids", "attempt")}
        context.update({"case_ids": task["cases"], "parent_evaluation": task["parent_evaluation"],
                        "rubric": self.protocol.get("rubric"), "component": self.component,
                        "dependencies": [task["parent_evaluation"]["event_id"]]})
        operation_id = self.store.begin_operation("optimizer", stage="proposal", dataset="trainset", **context)
        try:
            request = self.store.put_blob({"candidate": candidate, "reflective_dataset": reflective_dataset,
                                          "components_to_update": components_to_update, "context": context})
            self.store.append("optimizer_request", {"request": request}, operation_id=operation_id, **context)
            self.barrier()
            result = self.proposer.propose(deepcopy(candidate), deepcopy(reflective_dataset),
                                           list(components_to_update), {**context, "operation_id": operation_id})
            self.barrier()
            output = self.store.put_blob(result)
            texts = result.get("texts", result)
            scope = result.get("scope", {"accepted": True, "reason": "The proposer returned a permitted component."})
            valid = isinstance(texts, dict) and set(texts) == set(components_to_update) and all(
                isinstance(text, str) and text.strip() for text in texts.values())
            if not valid:
                scope = {"accepted": False, "reason": "The proposal does not contain the complete permitted component."}
                texts = texts if isinstance(texts, dict) else {}
            candidate_id = "D" + uuid.uuid4().hex[:12]
            task.update({"candidate_id": candidate_id, "texts": texts, "scope_accepted": bool(scope["accepted"])})
            diff = "".join(difflib.unified_diff(candidate[self.component].splitlines(keepends=True),
                           str(texts.get(self.component, "")).splitlines(keepends=True), fromfile="parent", tofile="candidate"))
            self.store.append("candidate_created", {"texts": texts, "parent_ids": task["parent_ids"],
                "directive": self.store.put_blob(texts), "diff": self.store.put_blob(diff),
                "request": request, "response": output, "hypothesis": result.get("hypothesis"),
                "status": "proposed", "validation_scores": None}, candidate_id=candidate_id,
                stage="proposal", dataset="trainset", **context)
            self.store.end_operation(operation_id, "success", result={"candidate_id": candidate_id, "response": output})
            scope_event = self.store.append("scope_checked", scope, candidate_id=candidate_id,
                                           stage="scope", dataset="trainset", **context)
            task["scope_event"] = scope_event["id"]
            if not scope["accepted"]:
                outcome = "scope_rejected"
            elif texts == candidate:
                outcome = "no_change"
            else:
                task["reflection"] = "proposed"
                return texts
            self.store.append("candidate_status", {"status": outcome}, candidate_id=candidate_id,
                              stage="scope", dataset="trainset", iteration=task["iteration"])
            self.store.end_operation(task["operation_id"], "rejected" if outcome == "scope_rejected" else "skipped",
                                     result={"outcome": outcome, "candidate_id": candidate_id},
                                     absence_reasons={"child_trials": scope["reason"] if outcome == "scope_rejected"
                                                      else "The directive is unchanged.", "validation": "No child comparison occurred."})
            task.update({"reflection": outcome, "complete": True})
            return {}
        except BaseException as exc:
            self.fatal = exc
            if self.store._failure is None:
                self.store.end_operation(operation_id, "failed", error=exc)
                self.store.end_operation(task["operation_id"], "failed", error=exc,
                    absence_reasons={"child_trials": "The optimizer failed before a usable proposal.",
                                     "validation": "The proposal remains incomplete."})
                task["complete"] = True
            raise


class _Callbacks:
    def __init__(self, adapter):
        self.adapter = adapter

    def __getattr__(self, name):
        if name.startswith("on_"):
            def observe(event):
                adapter = self.adapter
                try:
                    adapter.store.append("native_event", {"hook": name, "event": {
                        key: json_value(value) for key, value in event.items()
                        if key not in {"state", "final_state", "trainset_loader"}}})
                    handler = getattr(self, "handle_" + name[3:], None)
                    if handler is not None:
                        handler(event)
                except BaseException as exc:
                    adapter.store.fail(exc)
                    raise
            return observe
        raise AttributeError(name)

    def handle_valset_evaluated(self, event):
        adapter = self.adapter
        if event["candidate_idx"] == 0:
            candidate_id = "D0"
            task = None
        else:
            task = next(task for task in adapter.tasks if task.get("selection") == "minibatch_accepted"
                        and task.get("texts") == event["candidate"] and not task.get("pool_added"))
            task["pool_added"] = True
            candidate_id = task["candidate_id"]
            adapter.pool[event["candidate_idx"]] = candidate_id
        adapter.store.append("pool_candidate", {"native_index": event["candidate_idx"],
            "parent_ids": task["parent_ids"] if task else []}, candidate_id=candidate_id,
            stage="validation", dataset="valset", iteration=event["iteration"])
        scored = adapter.store.append("validation_scored", {
            "scores": {adapter.valset[int(index)]["id"]: score for index, score in event["scores_by_val_id"].items()},
            "mean": event["average_score"], "evaluation_events": adapter.validation_events.get(candidate_id, [])},
            candidate_id=candidate_id, dataset="valset", stage="validation", iteration=event["iteration"])
        adapter.store.append("candidate_status", {"status": "stored", "validation_mean": event["average_score"],
                            "validation_scores": scored["data"]["scores"]}, candidate_id=candidate_id,
                            stage="validation", dataset="valset", iteration=event["iteration"])
        if task:
            adapter.store.end_operation(task["operation_id"], "success", result={"candidate_id": candidate_id,
                                       "decision_event": task["decision_event"], "validation_event": scored["id"]})
            task["complete"] = True

    def handle_evaluation_skipped(self, event):
        adapter = self.adapter
        parent_id = adapter.pool[event["candidate_idx"]]
        task = next(task for task in adapter.tasks if task["parent_ids"] == [parent_id]
                    and task["reflection"] == "pending")
        task.update({"reflection": "skipped", "complete": True})
        adapter.store.end_operation(task["operation_id"], "skipped", result={"reason": event["reason"],
            "parent_evaluation": task.get("parent_evaluation")},
            absence_reasons={"candidate": "Native GEPA skipped the proposal.", "child_trials": event["reason"]})

    def handle_iteration_end(self, event):
        adapter = self.adapter
        adapter.barrier()
        state = event["state"]
        for task in adapter.tasks:
            if not task.get("complete"):
                adapter.store.end_operation(task["operation_id"], "interrupted",
                    absence_reasons={"completion": "Native GEPA omitted a terminal proposal outcome."})
                adapter.fatal = SearchError("A proposal task lacks a completed outcome.")
        adapter.barrier()
        probabilities = parent_probabilities(state)
        adapter.store.append("pool_updated", {"candidates": {
            adapter.pool[index]: {"probability": probability,
                "validation_mean": state.program_full_scores_val_set[index],
                "validation_scores": {adapter.valset[int(case_index)]["id"]: score
                    for case_index, score in state.prog_candidate_val_subscores[index].items()},
                "evaluation_events": adapter.validation_events.get(adapter.pool[index], [])}
            for index, probability in probabilities.items()}}, stage="search", iteration=event["iteration"])
        self._update_completion(state)
        state.adapter_state = adapter.get_adapter_state()
        state.save(str(adapter.store.root / "native"))
        self._preserve_checkpoint(state)

    def handle_state_saved(self, event):
        self.adapter.barrier()
        state = GEPAState.load(str(self.adapter.store.root / "native"))
        self._preserve_checkpoint(state)

    def _update_completion(self, state):
        adapter = self.adapter
        current = max(state.program_full_scores_val_set)
        previous = adapter.stop_state["best"]
        minimum = adapter.protocol["search"]["completion"].get("minimum_gain")
        threshold = (minimum or 0) / 100
        if previous is None or current > previous + threshold:
            adapter.stop_state.update({"best": current, "without_gain": 0})
        else:
            adapter.stop_state["without_gain"] += 1
        adapter.stop_state["last_iteration"] = state.i

    def _preserve_checkpoint(self, state):
        adapter = self.adapter
        path = adapter.store.root / "native" / "gepa_state.bin"
        artifact = adapter.store.put_blob(path.read_bytes())
        adapter.store.write_manifest("checkpoint", {"artifact": artifact, "iteration": state.i,
            "pool": adapter.pool, "event_sequence": len(adapter.store.events())})
        adapter.store.append("checkpoint_saved", {"artifact": artifact, "iteration": state.i})


class _Stopper:
    def __init__(self, adapter):
        self.adapter = adapter

    def __call__(self, state):
        adapter = self.adapter
        adapter.barrier()
        completion = adapter.protocol["search"]["completion"]
        maximum = completion.get("max_iterations")
        stagnation = completion.get("stagnation_iterations")
        if maximum is not None and state.i + 1 >= maximum:
            self._confirm(state, "max_iterations")
            adapter.completion_reason = "max_iterations"
            return True
        if stagnation is not None and adapter.stop_state["without_gain"] >= stagnation:
            if self._confirm(state, "stagnation_iterations"):
                adapter.stop_state["without_gain"] = 0
                adapter.store.append("completion_counter_reset", {
                    "reason": "A new best candidate met the confirmation gain rule.",
                    "candidate_id": adapter.stop_state["confirmed_candidate_id"],
                    "rule": "reset_on_confirmed_gain"}, stage="confirmation", iteration=state.i + 1)
                return False
            adapter.completion_reason = "stagnation_iterations"
            return True
        if adapter.stop_state["best"] is None:
            adapter.stop_state["best"] = max(state.program_full_scores_val_set)
        return False

    def _confirm(self, state, condition):
        adapter = self.adapter
        confirmation = adapter.protocol["search"].get("confirmation", {"enabled": False})
        if not confirmation.get("enabled"):
            return False
        best_index = max(range(len(state.program_candidates)), key=lambda index: state.program_full_scores_val_set[index])
        candidate_id = adapter.pool[best_index]
        if best_index == 0 or candidate_id == adapter.stop_state["confirmed_candidate_id"]:
            adapter.store.append("confirmation_skipped", {
                "reason": "The best candidate is the original directive." if best_index == 0
                          else "This best candidate already received confirmation.",
                "candidate_id": candidate_id, "completion_condition": condition}, stage="confirmation")
            return False
        operation = adapter.store.begin_operation("confirmation", stage="confirmation", dataset="valset",
            candidate_id=candidate_id, parent_ids=["D0"], dependencies=adapter.validation_events.get(candidate_id, []))
        try:
            original, child = adapter.evaluator.evaluate_pair(
                deepcopy(adapter.seed), deepcopy(state.program_candidates[best_index]), deepcopy(adapter.valset), stage="confirmation")
            adapter.barrier()
            if len(original) != len(adapter.valset) or len(child) != len(adapter.valset):
                raise SearchError("Confirmation did not assess the complete validation set.")
            if any(left["case_id"] != case["id"] or right["case_id"] != case["id"]
                   for left, right, case in zip(original, child, adapter.valset, strict=True)):
                raise SearchError("Confirmation did not preserve case order and identity.")
            gain = sum(right["mean"] - left["mean"] for left, right in zip(original, child, strict=True)) / len(child)
            if not math.isfinite(gain):
                raise SearchError("Confirmation returned a non-finite gain.")
            minimum = adapter.protocol["search"]["completion"].get("minimum_gain", 0) or 0
            confirmed = gain > minimum
            result = {"original": adapter.store.put_blob(original), "candidate": adapter.store.put_blob(child),
                      "gain": gain, "minimum_gain": minimum, "confirmed_gain": confirmed,
                      "completion_condition": condition, "counter_policy": confirmation["counter_policy"]}
            adapter.store.append("confirmation_decision", result, candidate_id=candidate_id, stage="confirmation", dataset="valset")
            adapter.store.end_operation(operation, "success", result=result)
            adapter.stop_state["confirmed_candidate_id"] = candidate_id
            adapter.stop_state["confirmed_score"] = sum(row["mean"] for row in child) / len(child)
            return confirmed and condition == "stagnation_iterations"
        except BaseException as exc:
            adapter.fatal = exc
            if adapter.store._failure is None:
                adapter.store.end_operation(operation, "interrupted" if isinstance(exc, KeyboardInterrupt) else "failed", error=exc,
                    absence_reasons={"confirmation_score": "The confirmation comparison remains incomplete."})
            raise


def _validate(protocol, trainset, valset):
    if importlib.metadata.version("gepa") != GEPA_VERSION:
        raise SearchError(f"The evaluator requires GEPA {GEPA_VERSION}.")
    search = protocol["search"]
    for key in ("proposal_count", "minibatch_size", "concurrency"):
        if isinstance(search.get(key), bool) or not isinstance(search.get(key), int) or search[key] < 1:
            raise SearchError(f"search.{key} must be a positive integer.")
    if not isinstance(search.get("seed"), int) or not isinstance(search.get("skip_perfect"), bool):
        raise SearchError("The search requires an explicit random seed and perfect-score skip policy.")
    if search.get("sampler") != "epoch_shuffled":
        raise SearchError("The declared search sampler must be epoch_shuffled.")
    completion = search["completion"]
    rules = [completion.get("max_iterations"), completion.get("stagnation_iterations")]
    if not any(value is not None for value in rules):
        raise SearchError("The search requires an explicit completion rule.")
    if any(value is not None and (isinstance(value, bool) or not isinstance(value, int) or value < 1) for value in rules):
        raise SearchError("Completion counters must be positive integers.")
    if completion.get("stagnation_iterations") is not None:
        gain = completion.get("minimum_gain")
        if isinstance(gain, bool) or not isinstance(gain, (int, float)) or not math.isfinite(gain) or gain < 0:
            raise SearchError("Stagnation requires an explicit finite nonnegative minimum gain.")
    confirmation = search.get("confirmation", {"enabled": False})
    if confirmation.get("enabled") and (confirmation.get("reference") != "original"
            or confirmation.get("counter_policy") != "reset_on_confirmed_gain"):
        raise SearchError("Confirmation requires the original reference and explicit reset_on_confirmed_gain counter policy.")
    if not trainset or not valset:
        raise SearchError("The training and validation sets must each contain cases.")
    for cases in (trainset, valset):
        if len(set(_case_ids(cases))) != len(cases):
            raise SearchError("Each case identifier must occur once per dataset.")
    if set(_case_ids(trainset)).intersection(_case_ids(valset)):
        raise SearchError("Training and validation case identifiers must be disjoint.")
    train_groups = {case.get("group", case.get("group_id", case["id"])) for case in trainset}
    if train_groups.intersection(case.get("group", case.get("group_id", case["id"])) for case in valset):
        raise SearchError("Training and validation case groups must be disjoint.")
    component = protocol.get("component", "packager_directive")
    if set(protocol["seed_candidate"]) != {component} or not protocol["seed_candidate"][component].strip():
        raise SearchError("The seed must contain one complete permitted directive component.")


def run_search(protocol: dict, trainset: list[dict], valset: list[dict], evaluator, proposer,
               store: RecordStore, *, resume: bool = False) -> SearchResult:
    """Execute GEPA. The caller owns preparation, final comparison, and adoption."""
    _validate(protocol, trainset, valset)
    identity = {"protocol": protocol, "trainset": trainset, "valset": valset, "gepa_version": GEPA_VERSION}
    identity_hash = content_hash(identity)
    saved = store.read_manifest("search_identity")
    if saved is not None and saved["hash"] != identity_hash:
        raise SearchError("The continued search must preserve its complete protocol and case inputs.")
    if saved is not None and not resume:
        raise SearchError("This experiment already contains a search. Use continuation or a separate record store.")
    if resume and saved is None:
        raise SearchError("No recorded search exists to continue.")
    if resume:
        checkpoint = store.read_manifest("checkpoint")
        if checkpoint is None and any(event["kind"] == "parent_selected" for event in store.events()):
            raise SearchError("The native search advanced without a durable checkpoint. Use a separate run directory.")
        native_path = store.root / "native" / "gepa_state.bin"
        if checkpoint is not None:
            raw = store.read_blob(checkpoint["artifact"])
            store._atomic_write(native_path, raw)
        elif native_path.exists():
            artifact = store.put_blob(native_path.read_bytes())
            store.append("uncheckpointed_state_preserved", {"artifact": artifact,
                         "reason": "Native initialization resumes from fixed inputs without an attested checkpoint."}, stage="search")
            native_path.unlink()
            store._sync_directory(native_path.parent)
        interrupted = store.interrupt_unfinished("Continuation preserved this attempt and started a replacement attempt.")
        store.append("search_resumed", {"interrupted_operations": interrupted, "checkpoint": checkpoint,
                     "mode": "checkpoint" if checkpoint else "native_initialization"})
        recorded = store.candidates()
        if checkpoint is not None:
            state = GEPAState.load(str(store.root / "native"))
            for index, candidate in enumerate(state.program_candidates):
                candidate_id = checkpoint["pool"].get(str(index), checkpoint["pool"].get(index))
                if candidate_id not in recorded or recorded[candidate_id]["texts"] != candidate:
                    raise SearchError("The native checkpoint disagrees with the recorded candidate texts.")
        elif recorded.get("D0", {}).get("texts") != protocol["seed_candidate"]:
            raise SearchError("The recorded original directive differs from the continued initialization.")
    else:
        store.write_manifest("search_identity", {"hash": identity_hash, "identity": identity})
        store.append("candidate_created", {"texts": protocol["seed_candidate"], "parent_ids": [],
                     "directive": store.put_blob(protocol["seed_candidate"]), "status": "original",
                     "validation_scores": None}, candidate_id="D0", stage="preparation")
    adapter = _Adapter(protocol, trainset, valset, evaluator, proposer, store, resume)
    callbacks = _Callbacks(adapter)
    operation = store.begin_operation("search", stage="search")
    operation_ended = False
    try:
        native = gepa.optimize(
            seed_candidate=deepcopy(protocol["seed_candidate"]), trainset=trainset, valset=valset,
            adapter=adapter, candidate_selection_strategy="pareto", frontier_type="instance",
            batch_sampler="epoch_shuffled", reflection_minibatch_size=protocol["search"]["minibatch_size"],
            sampling_strategy=_Sampling(protocol["search"]["proposal_count"], adapter),
            selection_strategy=_Selection(adapter), acceptance_criterion="strict_improvement",
            val_evaluation_policy="full_eval", skip_perfect_score=protocol["search"]["skip_perfect"],
            seed=protocol["search"]["seed"], use_merge=False, cache_evaluation=False,
            max_metric_calls=None, max_reflection_cost=None, stop_callbacks=_Stopper(adapter),
            run_dir=str(store.root / "native"), callbacks=[callbacks], logger=_QuietLogger(),
            raise_on_exception=True, display_progress_bar=False,
        )
        adapter.barrier()
        # Native state files lack fsync. The artifact store preserves the final checkpoint durably.
        state = GEPAState.load(str(store.root / "native"))
        callbacks._preserve_checkpoint(state)
        candidate_id = adapter.pool[native.best_idx]
        store.append("search_selected", {"candidate_id": candidate_id, "completion_reason": adapter.completion_reason,
                     "validation_mean": native.val_aggregate_scores[native.best_idx],
                     "native_best_index": native.best_idx}, stage="search", candidate_id=candidate_id)
        if native.best_idx == 0:
            store.append("final_comparison_skipped", {"reason": "GEPA returned the original directive."}, stage="final")
        store.end_operation(operation, "success", result={"best_candidate_id": candidate_id,
                            "completion_reason": adapter.completion_reason})
        operation_ended = True
        if not store.completeness()["complete"]:
            raise SearchError("The search records contain unexplained gaps.")
        scores = {adapter.pool[index]: {valset[int(case_index)]["id"]: value
                  for case_index, value in subscores.items()} for index, subscores in enumerate(native.val_subscores)}
        result = SearchResult(candidate_id, native.best_candidate, store.candidates(), scores, native,
                              True, adapter.completion_reason)
        store.write_manifest("search_result", result.to_dict())
        return result
    except BaseException as exc:
        if store._failure is None and not operation_ended:
            store.end_operation(operation, "interrupted" if isinstance(exc, (KeyboardInterrupt, SystemExit)) else "failed", error=exc)
        raise
