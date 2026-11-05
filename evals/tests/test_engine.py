from copy import deepcopy
import threading

import pytest

from culler_evals.engine import SearchError, run_search
from culler_evals.records import RecordError, RecordStore


COMPONENT = "test_directive"
TRAIN = [{"id": "T1", "group_id": "t1"}, {"id": "T2", "group_id": "t2"}]
VALIDATION = [{"id": "V1", "group_id": "v1"}, {"id": "V2", "group_id": "v2"}]


def protocol(iterations=1, proposals=1, concurrency=1, skip_perfect=False):
    return {"version": 1, "id": "native-gepa-test", "role": "test", "component": COMPONENT,
            "seed_candidate": {COMPONENT: "original"}, "rubric": {"version": "test"},
            "search": {"proposal_count": proposals, "minibatch_size": 1, "concurrency": concurrency,
                       "seed": 3, "sampler": "epoch_shuffled", "skip_perfect": skip_perfect,
                       "completion": {"max_iterations": iterations}}}


class Evaluator:
    def __init__(self, training=None, validation=None, interrupt=None, no_trajectories=False):
        self.training = training or {"original": .4, "better": .8}
        self.validation = validation or {"original": [.6, .6], "better": [.7, .7]}
        self.calls = []
        self.interrupt = interrupt
        self.no_trajectories = no_trajectories
        self.lock = threading.Lock()

    def evaluate(self, candidate, cases, stage, capture_traces, context):
        text = candidate[COMPONENT]
        with self.lock:
            self.calls.append({"text": text, "cases": [case["id"] for case in cases],
                               "stage": stage, "context": deepcopy(context)})
        if self.interrupt and self.interrupt(text, stage, context):
            raise KeyboardInterrupt("The test interrupted a model attempt.")
        scores = [self.validation[text][int(case["id"][1:]) - 1] if stage == "validation"
                  else self.training[text] for case in cases]
        return {"outputs": [{"result": text} for case in cases], "scores": scores,
                "trajectories": None if self.no_trajectories else [
                    {"inputs": {"case_id": case["id"]}, "feedback": "Explain the actual mechanism."} for case in cases]}


class Proposer:
    def __init__(self, texts, scope=True):
        self.texts = iter(texts)
        self.calls = []
        self.scope = scope

    def propose(self, candidate, reflective_dataset, components_to_update, context):
        self.calls.append({"parent": candidate, "dataset": reflective_dataset, "context": context})
        return {"texts": {COMPONENT: next(self.texts)}, "hypothesis": "A direct explanation improves comprehension.",
                "scope": {"accepted": self.scope, "reason": "The edit respects the permitted text."}}


def events(store, kind):
    return [event for event in store.events() if event["kind"] == kind]


def test_native_acceptance_uses_exact_parent_minibatch_and_preserves_scope(tmp_path):
    evaluator, proposer = Evaluator(), Proposer(["better"])
    with RecordStore(tmp_path) as store:
        result = run_search(protocol(), TRAIN, VALIDATION, evaluator, proposer, store)
        assert result.best_candidate == {COMPONENT: "better"}
        assert result.native_result.val_aggregate_scores == [.6, .7]
        assert store.completeness()["complete"]
        parent = next(call for call in evaluator.calls if call["stage"] == "training_parent")
        child = next(call for call in evaluator.calls if call["stage"] == "training_child")
        assert child["cases"] == parent["cases"]
        decision = events(store, "minibatch_decision")[0]["data"]
        assert decision["parent_scores"] == [.4] and decision["child_scores"] == [.8]
        assert decision["parent_evaluation"]["operation_id"] == parent["context"]["operation_id"]
        assert len([call for call in evaluator.calls if call["stage"] == "training_parent"]) == 1
        assert all(record["case_id"].startswith("T") for record in proposer.calls[0]["dataset"][COMPONENT])
        assert not any("V1" in str(call["dataset"]) for call in proposer.calls)


@pytest.mark.parametrize("score", [.4, .2])
def test_native_tie_or_loss_has_no_child_validation(tmp_path, score):
    evaluator = Evaluator(training={"original": .4, "worse": score})
    with RecordStore(tmp_path) as store:
        result = run_search(protocol(), TRAIN, VALIDATION, evaluator, Proposer(["worse"]), store)
        assert result.best_candidate_id == "D0"
        assert result.native_result.num_candidates == 1
        assert not any(call["text"] == "worse" and call["stage"] == "validation" for call in evaluator.calls)
        assert events(store, "minibatch_decision")[0]["data"]["outcome"] == "minibatch_rejected"
        candidate = next(candidate for key, candidate in result.candidates.items() if key != "D0")
        assert candidate["validation_scores"] is None
        assert events(store, "final_comparison_skipped")


def test_minibatch_acceptance_can_reduce_validation_mean_and_lead_one_case(tmp_path):
    evaluator = Evaluator(validation={"original": [.6, .6], "better": [.3, .8]})
    with RecordStore(tmp_path) as store:
        result = run_search(protocol(), TRAIN, VALIDATION, evaluator, Proposer(["better"]), store)
        assert result.native_result.num_candidates == 2
        assert result.native_result.val_aggregate_scores == [.6, .55]
        assert result.best_candidate_id == "D0"
        snapshot = events(store, "pool_updated")[-1]["data"]["candidates"]
        assert all(candidate["probability"] == .5 for candidate in snapshot.values())


def test_native_same_parent_tasks_remain_distinct_and_duplicate_child_is_recorded(tmp_path):
    evaluator = Evaluator()
    with RecordStore(tmp_path) as store:
        result = run_search(protocol(proposals=3), TRAIN, VALIDATION, evaluator,
                            Proposer(["better", "better", "better"]), store)
        selected = events(store, "parent_selected")
        assert len({event["proposal_task_id"] for event in selected}) == 3
        assert {event["candidate_id"] for event in selected} == {"D0"}
        assert len([call for call in evaluator.calls if call["stage"] == "training_parent"]) == 2
        assert len([call for call in evaluator.calls if call["stage"] == "training_child"]) == 3
        assert len([call for call in evaluator.calls if call["text"] == "better" and call["stage"] == "validation"]) == 1
        outcomes = [event["data"]["outcome"] for event in events(store, "minibatch_decision")]
        assert outcomes.count("duplicate") == 2
        assert result.native_result.num_candidates == 2
        assert store.completeness()["complete"]


def test_dominated_parent_keeps_scores_with_zero_mutation_probability(tmp_path):
    with RecordStore(tmp_path) as store:
        run_search(protocol(), TRAIN, VALIDATION, Evaluator(), Proposer(["better"]), store)
        snapshot = events(store, "pool_updated")[-1]["data"]["candidates"]
        assert snapshot["D0"]["probability"] == 0
        assert snapshot["D0"]["validation_scores"] == {"V1": .6, "V2": .6}


@pytest.mark.parametrize("no_trajectories,skip,training,reason", [
    (False, True, {"original": 1}, "all_scores_perfect"),
    (True, False, {"original": .4}, "no_trajectories"),
])
def test_native_skips_have_explicit_outcomes(tmp_path, no_trajectories, skip, training, reason):
    evaluator = Evaluator(training=training, no_trajectories=no_trajectories)
    proposer = Proposer([])
    with RecordStore(tmp_path) as store:
        result = run_search(protocol(skip_perfect=skip), TRAIN, VALIDATION, evaluator, proposer, store)
        assert result.best_candidate_id == "D0"
        assert not proposer.calls
        completed = events(store, "operation_completed")
        assert any(event["data"]["outcome"] == "skipped" and event["data"]["result"]["reason"] == reason
                   for event in completed)
        assert store.completeness()["complete"]


@pytest.mark.parametrize("scope,text,outcome", [(False, "better", "scope_rejected"), (True, "original", "no_change")])
def test_scope_rejection_and_no_change_keep_text_without_child_scores(tmp_path, scope, text, outcome):
    evaluator = Evaluator()
    with RecordStore(tmp_path) as store:
        result = run_search(protocol(), TRAIN, VALIDATION, evaluator, Proposer([text], scope=scope), store)
        candidate = next(candidate for key, candidate in result.candidates.items() if key != "D0")
        assert candidate["texts"] == {COMPONENT: text}
        assert candidate["status"] == outcome
        assert candidate["validation_scores"] is None
        assert not any(call["stage"] == "training_child" for call in evaluator.calls)
        assert store.completeness()["complete"]


def test_required_callback_failure_stops_before_dependent_model_attempt(tmp_path, monkeypatch):
    evaluator = Evaluator()
    with RecordStore(tmp_path) as store:
        append = store.append
        def fail_selection(kind, data=None, **identity):
            if kind == "native_event" and data["hook"] == "on_iteration_start":
                raise OSError("The journal is unavailable.")
            return append(kind, data, **identity)
        monkeypatch.setattr(store, "append", fail_selection)
        with pytest.raises(RecordError, match="Dependent work"):
            run_search(protocol(), TRAIN, VALIDATION, evaluator, Proposer(["better"]), store)
        assert [call["stage"] for call in evaluator.calls] == ["validation"]
        assert not store.completeness()["complete"]


def test_parallel_batch_has_actual_overlap_and_ordered_case_results(tmp_path):
    evaluator = Evaluator()
    entered = threading.Barrier(2, timeout=5)
    original = evaluator.evaluate
    def evaluate(candidate, cases, stage, capture_traces, context):
        if stage == "training_parent":
            entered.wait()
        return original(candidate, cases, stage, capture_traces, context)
    evaluator.evaluate = evaluate
    with RecordStore(tmp_path) as store:
        result = run_search(protocol(proposals=2, concurrency=2), TRAIN, VALIDATION, evaluator,
                            Proposer(["better", "better"]), store)
        assert result.search_complete
        assert store.completeness()["complete"]


def test_continuation_preserves_pool_and_completed_trials(tmp_path):
    configuration = protocol(iterations=2)
    evaluator = Evaluator(interrupt=lambda text, stage, context: stage == "training_parent" and context["iteration"] == 2)
    with RecordStore(tmp_path) as store:
        with pytest.raises(KeyboardInterrupt):
            run_search(configuration, TRAIN, VALIDATION, evaluator, Proposer(["better"]), store)
        first_calls = deepcopy(evaluator.calls)
    continued = Evaluator(training={"original": .4, "better": .8, "best": .9},
                          validation={"original": [.6, .6], "better": [.7, .7], "best": [.9, .9]})
    with RecordStore(tmp_path) as store:
        result = run_search(configuration, TRAIN, VALIDATION, continued, Proposer(["best"]), store, resume=True)
        assert result.native_result.val_aggregate_scores == [.6, .7, .9]
        assert not any(call["stage"] == "validation" and call["text"] in {"original", "better"} for call in continued.calls)
        assert len([call for call in first_calls if call["stage"] == "validation"]) == 2
        assert events(store, "evaluation_reused")
        assert any(event["data"]["outcome"] == "interrupted" for event in events(store, "operation_completed"))
        assert store.completeness()["complete"]


def test_continuation_rejects_changed_protocol(tmp_path):
    configuration = protocol()
    with RecordStore(tmp_path) as store:
        run_search(configuration, TRAIN, VALIDATION, Evaluator(), Proposer(["better"]), store)
        configuration["rubric"]["version"] = "changed"
        with pytest.raises(SearchError, match="preserve its complete protocol"):
            run_search(configuration, TRAIN, VALIDATION, Evaluator(), Proposer([]), store, resume=True)


def test_stagnation_completion_uses_configured_gain_and_preserves_counters(tmp_path):
    configuration = protocol(iterations=20)
    configuration["search"]["completion"] = {"stagnation_iterations": 2, "minimum_gain": 1}
    evaluator = Evaluator(training={"original": .4, "worse": .2})
    with RecordStore(tmp_path) as store:
        result = run_search(configuration, TRAIN, VALIDATION, evaluator, Proposer(["worse", "worse"]), store)
        assert result.completion_reason == "stagnation_iterations"
        assert len(events(store, "parent_selected")) == 2


def test_score_errors_remain_infrastructure_failures(tmp_path):
    evaluator = Evaluator(validation={"original": [float("nan"), .6]})
    with RecordStore(tmp_path) as store:
        with pytest.raises(SearchError, match="Every case score"):
            run_search(protocol(), TRAIN, VALIDATION, evaluator, Proposer([]), store)
        assert not events(store, "search_selected")


def test_confirmation_uses_fresh_pair_and_cannot_reset_same_candidate_forever(tmp_path):
    configuration = protocol(iterations=20)
    configuration["search"]["completion"] = {"stagnation_iterations": 1, "minimum_gain": 1}
    configuration["search"]["confirmation"] = {
        "enabled": True, "reference": "original", "counter_policy": "reset_on_confirmed_gain"}
    evaluator = Evaluator(training={"original": .4, "better": .8, "worse": .2})
    pairs = []
    def evaluate_pair(original, candidate, cases, stage):
        pairs.append((original, candidate, stage))
        return ([{"case_id": case["id"], "mean": 60} for case in cases],
                [{"case_id": case["id"], "mean": 65} for case in cases])
    evaluator.evaluate_pair = evaluate_pair
    with RecordStore(tmp_path) as store:
        result = run_search(configuration, TRAIN, VALIDATION, evaluator,
                            Proposer(["better", "worse", "worse"]), store)
        assert result.completion_reason == "stagnation_iterations"
        assert len(pairs) == 1 and pairs[0][2] == "confirmation"
        assert events(store, "confirmation_decision")[0]["data"]["gain"] == 5
        assert len(events(store, "completion_counter_reset")) == 1
        assert events(store, "confirmation_skipped")
        assert result.native_result.val_aggregate_scores == [.6, .7]


def test_first_iteration_interrupt_can_continue_from_seed_checkpoint(tmp_path):
    configuration = protocol()
    evaluator = Evaluator(interrupt=lambda text, stage, context: stage == "training_parent")
    with RecordStore(tmp_path) as store:
        with pytest.raises(KeyboardInterrupt):
            run_search(configuration, TRAIN, VALIDATION, evaluator, Proposer([]), store)
    with RecordStore(tmp_path) as store:
        continued = Evaluator()
        result = run_search(configuration, TRAIN, VALIDATION, continued, Proposer(["better"]), store, resume=True)
        assert result.best_candidate != configuration["seed_candidate"]
        assert not any(call["text"] == "original" and call["stage"] == "validation" for call in continued.calls)


def test_initial_validation_failure_can_continue_without_checkpoint(tmp_path):
    configuration = protocol()
    evaluator = Evaluator(interrupt=lambda text, stage, context: stage == "validation")
    with RecordStore(tmp_path) as store:
        with pytest.raises(KeyboardInterrupt):
            run_search(configuration, TRAIN, VALIDATION, evaluator, Proposer([]), store)
        assert store.read_manifest("checkpoint") is None
        initial_scope = evaluator.calls[0]["context"]["sample_scope"]
    with RecordStore(tmp_path) as store:
        continued = Evaluator()
        result = run_search(configuration, TRAIN, VALIDATION, continued, Proposer(["better"]), store, resume=True)
        assert result.best_candidate != configuration["seed_candidate"]
        assert continued.calls[0]["context"]["sample_scope"] == initial_scope
        assert events(store, "search_resumed")[-1]["data"]["mode"] == "native_initialization"
        assert len(events(store, "candidate_created")) == 2
        assert store.completeness()["complete"]


def test_continuation_preserves_shared_native_sampler_and_parent_random_state(tmp_path):
    configuration = protocol(iterations=3, proposals=2)
    texts = ["alpha", "beta", "gamma", "delta", "epsilon", "zeta"]
    training = {text: .2 + index / 10 for index, text in enumerate(texts)} | {"original": .1}
    validation = {"original": [.3, .3], "alpha": [.7, .4], "beta": [.4, .7],
                  "gamma": [.8, .3], "delta": [.3, .8], "epsilon": [.9, .2], "zeta": [.2, .9]}
    with RecordStore(tmp_path / "whole") as store:
        result = run_search(configuration, TRAIN, VALIDATION, Evaluator(training, validation), Proposer(texts), store)
        complete_trace = [event["data"]["case_ids"] for event in events(store, "parent_selected")]
        complete_parents = result.native_result.parents
    evaluator = Evaluator(training, validation, interrupt=lambda text, stage, context:
                          stage == "training_parent" and context["iteration"] == 2)
    with RecordStore(tmp_path / "continued") as store:
        with pytest.raises(KeyboardInterrupt):
            run_search(configuration, TRAIN, VALIDATION, evaluator, Proposer(texts[:2]), store)
    with RecordStore(tmp_path / "continued") as store:
        result = run_search(configuration, TRAIN, VALIDATION, Evaluator(training, validation),
                            Proposer(texts[2:]), store, resume=True)
        last_tasks = {event["proposal_task_id"]: event["data"]["case_ids"] for event in events(store, "parent_selected")}
        resumed_trace = [last_tasks[key] for key in sorted(last_tasks)]
        assert resumed_trace == complete_trace
        assert result.native_result.parents == complete_parents
