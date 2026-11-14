"""Contributor commands for isolated directive experiments."""

from __future__ import annotations

import argparse
import contextlib
import datetime
import errno
import html
import json
from pathlib import Path
import shutil
import subprocess
import sys
import textwrap
import threading
import uuid
import webbrowser

from .cases import content_hash, load_case, prepare_case, validate_cases
from .comparison import calibration_report, compare, human_review_binding, validate_human_presentation, validate_human_review
from .calibration import (prepare_calibration_review, calibration_assessment_requests,
                          record_calibration_assessments, record_human_calibration, export_calibration_records,
                          bind_assessment_context)
from .engine import run_search
from .evaluator import Evaluator, Proposer, original_directive, protected_requirements
from .protocol import (contract_readiness, dataset_paths, digest, freeze_protocol, read_protocol, readiness,
                       product_wrapper_hashes, calibration_context, source_revision)
from .records import RecordStore
from .storage import DEFAULT_EXPERIMENTS, DEFAULT_CACHE, durable_directory, preserve_source, restore_records
from .runtime import Runtime, control_cancelled
from .browser import PacketBrowser
from .assessment import check_packet, require_inspection, required_sections
from .viewer import export_view, index_mlflow
from .view_server import LiveViewer


SOURCE = Path(__file__).resolve().parents[2]


def message(value, *, stream=sys.stdout):
    width = max(20, shutil.get_terminal_size((80, 24)).columns)
    for line in str(value).splitlines():
        print(textwrap.fill(line, width=width, replace_whitespace=False,
                            break_long_words=False, break_on_hyphens=False), file=stream, flush=True)


class CommandParser(argparse.ArgumentParser):
    def _print_message(self, text, file=None):
        if text:
            message(text, stream=file or sys.stdout)

    def error(self, message_text):
        message(self.format_usage().strip(), stream=sys.stderr)
        message("Required action: " + message_text + ".", stream=sys.stderr)
        self.exit(2)


class PilotHelpFormatter(argparse.HelpFormatter):
    def __init__(self, prog):
        width = max(20, shutil.get_terminal_size((80, 24)).columns)
        super().__init__(prog, width=width, max_help_position=6 if width < 60 else 28)


    def _split_lines(self, text, width):
        return textwrap.wrap(text, width, break_long_words=False, break_on_hyphens=False)


class Progress:
    def __init__(self, store):
        self.store = store
        self.stop = threading.Event()
        self.position = len(store.events())
        self.thread = threading.Thread(target=self.watch, daemon=True)

    def watch(self):
        while not self.stop.wait(.25):
            for event in self.store.events()[self.position:]:
                self.position += 1
                if event["kind"] == "operation_started" and event["data"]["operation_kind"] in ("trial", "contract_trial", "optimizer", "final_comparison", "reference_assessment"):
                    action = event["data"]["operation_kind"].replace("_", " ")
                    number, count = event.get("trial_number"), event.get("trial_count")
                    start = (f"Trial {number} of {count}; attempt {event.get('external_attempt', 1)}"
                             if number is not None and count is not None else f"Start {action}")
                    message(start + f": {event.get('stage', 'experiment')}" +
                            (f"; case {event['case_id']}" if event.get("case_id") else ""), stream=sys.stderr)
                elif event["kind"] in ("minibatch_decision", "search_selected", "final_decision"):
                    message(event["kind"].replace("_", " ").capitalize() + ": " +
                            str(event["data"].get("outcome", event["data"].get("status", event.get("candidate_id", "complete")))), stream=sys.stderr)

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *_):
        self.stop.set()
        self.thread.join()


def _write(path: Path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2) + "\n")
    path.chmod(0o600)


def _cases(protocol):
    return {name: validate_cases(dataset_paths(protocol, name)) for name in ("trainset", "valset", "final")}


def _pilot_cases(protocol, paths):
    cases = validate_cases(paths, require_ready=True)
    declared = protocol.get("pilot", {}).get("cases")
    if declared is not None:
        if not isinstance(declared, list) or not declared or not all(isinstance(path, str) and path for path in declared):
            raise ValueError("Set pilot.cases to a nonempty list of case manifest paths.")
        root = Path(protocol["_config_root"])
        expected = validate_cases([root / path for path in declared], require_ready=True)
        if [digest(case) for case in cases] != [digest(case) for case in expected]:
            raise ValueError("Use the cases listed in pilot.cases, in that order.")
    return cases


def _credentials(args):
    return json.loads(args.credentials.read_text()) if args.credentials else {}


def _runtime(args, protocol, store):
    runtime = Runtime(args.source, source_manifest=protocol.get("runtime_source_manifest"),
                      product_manifest=protocol.get("product_source_manifest"))
    return Evaluator(protocol, args.source, store, runtime, cache=args.cache, credentials=_credentials(args))


def _assert_ready(protocol, cases=None, *, full=True):
    errors = readiness(protocol, cases, full=full)
    if errors:
        raise ValueError("The protocol is incomplete:\n" + "\n".join(f"- {error}" for error in errors))


def _final(evaluator, store, protocol, original, candidate, cases):
    operation = store.begin_operation("final_comparison", stage="final")
    try:
        left, right = evaluator.evaluate_pair(original, candidate, cases)
        completeness = store.completeness()
        complete = (completeness["unfinished_operations"] == [operation]
                    and not completeness["artifact_errors"] and not completeness["record_failure"]
                    and bool(store.read_manifest("search_result", {}).get("search_complete")))
        report = compare(left, right, cases, protocol, complete=complete)
        store.write_manifest("final_comparison", {"original": left, "candidate": right, "report": report})
        store.append("final_decision", report, stage="final")
        store.end_operation(operation, "success", result=report)
        return report
    except BaseException as exc:
        store.barrier()
        store.end_operation(operation, "failed", error={"type": type(exc).__name__, "message": str(exc)})
        raise


def _prepare_review(store, source, output):
    final = store.read_manifest("final_comparison")
    if not final:
        raise ValueError("The human review requires a completed final comparison.")
    if store.read_manifest("human_presentation"):
        raise ValueError("The experiment already contains a fixed human presentation.")
    import random
    generator = random.SystemRandom()
    output.mkdir(parents=True, exist_ok=False)
    mapping, records, pages = [], [], []
    for left, right in zip(final["original"], final["candidate"], strict=True):
        if left["case_id"] != right["case_id"]:
            raise ValueError("The final case order differs between directives.")
        for original, candidate in zip(left["trials"], right["trials"], strict=True):
            pair_id = "comparison-" + uuid.uuid4().hex[:12]
            pair = [("original", original), ("candidate", candidate)]
            generator.shuffle(pair)
            presentation = {"id": pair_id, "case_id": left["case_id"], "packets": {}}
            for alias, (identity, trial) in zip(("A", "B"), pair, strict=True):
                reference = trial.get("artifacts", {}).get("packet_path")
                if reference is None:
                    raise ValueError("The human review requires every final packet artifact.")
                internal = store.root / "human-display" / pair_id / alias
                internal.mkdir(parents=True)
                packet = internal / "packet.html"
                packet.write_bytes(store.read_blob(reference))
                with PacketBrowser(source, packet, internal / "browser") as browser:
                    import asyncio
                    async def serialize():
                        return await browser.frame.content(), await browser.page.locator("#packet").get_attribute("sandbox")
                    document, sandbox = asyncio.run_coroutine_threadsafe(serialize(), browser.loop).result(timeout=30)
                page_name = f"{pair_id}-{alias}.html"
                page = '<!doctype html><meta charset="utf-8"><title>Packet ' + alias + '</title><style>body{margin:0}iframe{width:100%;height:100vh;border:0}</style>'
                page += '<iframe title="Packet ' + alias + '" sandbox="' + sandbox + '" srcdoc="' + html.escape(document, quote=True) + '"></iframe>'
                (output / page_name).write_text(page)
                presentation["packets"][alias] = {"identity": identity, "trial_id": trial["trial_id"], "sha256": reference["sha256"]}
                pages.append(f'<li>{html.escape(pair_id)} · <a href="{page_name}">Packet {alias}</a></li>')
            mapping.append(presentation)
            records.append({"id": pair_id, "preference": None, "reason": None})
    template = {"version": 1, "reviewer": None, "blinded": None, "reason": None, "comparisons": records}
    _write(output / "review.json", template)
    (output / "index.html").write_text('<!doctype html><meta charset="utf-8"><title>Human packet comparison</title>'
        '<style>body{max-width:850px;margin:40px auto;font:18px/1.6 system-ui;padding:20px}</style><h1>Human packet comparison</h1>'
        '<p>Compare each pair without directive labels. Select A, B, tie, or insufficient_evidence in review.json.</p>'
        '<p>Favor representative understanding with less effort. Explain each preference. Keep the experiment viewer closed during this review.</p><ul>'
        + ''.join(pages) + '</ul>')
    store.write_manifest("human_presentation", {"binding": human_review_binding(final["original"], final["candidate"]),
                                               "comparisons": mapping, "rule": "A strict majority of pairs favors the candidate; ties count against confirmation."})
    store.append("human_comparison_prepared", {"pairs": len(mapping), "output": str(output)}, stage="human_review")
    return {"review": str(output / "index.html"), "record": str(output / "review.json")}


def _record_review(store, path, operation_id):
    presentation = store.read_manifest("human_presentation")
    final = store.read_manifest("final_comparison")
    if not presentation or not final:
        raise ValueError("Prepare the human comparison before its review record.")
    presentation_errors = validate_human_presentation(presentation, final["original"], final["candidate"])
    if presentation_errors:
        raise ValueError("\n".join(presentation_errors))
    record = json.loads(path.read_text())
    if not isinstance(record, dict) or record.get("version") != 1:
        raise ValueError("The human review requires a version 1 object.")
    comparisons = record.get("comparisons", [])
    if not isinstance(comparisons, list) or any(not isinstance(item, dict) or not isinstance(item.get("id"), str) for item in comparisons):
        raise ValueError("The review comparisons require objects with identifiers.")
    if len(comparisons) != len(presentation["comparisons"]) or len({item.get("id") for item in comparisons}) != len(comparisons):
        raise ValueError("The review must assess each presented pair exactly once.")
    answers = {item["id"]: item for item in comparisons}
    preferred, packets = [], []
    for pair in presentation["comparisons"]:
        answer = answers.get(pair["id"], {})
        preference = answer.get("preference")
        if preference not in ("A", "B", "tie", "insufficient_evidence") or not isinstance(answer.get("reason"), str) or not answer["reason"].strip():
            raise ValueError("Each pair requires an explicit preference and reason.")
        preferred.append(pair["packets"][preference]["identity"] if preference in ("A", "B") else preference)
        packets.extend({key: value for key, value in pair["packets"][alias].items() if key != "identity"} for alias in ("A", "B"))
    majority = len(preferred) / 2
    decision = "improved" if preferred.count("candidate") > majority else "worse" if preferred.count("original") > majority else "insufficient_evidence" if "insufficient_evidence" in preferred else "tie"
    proof = {"version": 1, "reviewer": record.get("reviewer"), "recorded_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
             "blinded": record.get("blinded"), "decision": decision, "reason": record.get("reason"),
             "binding": presentation["binding"], "reviewed_packets": packets}
    errors = validate_human_review(proof, final["original"], final["candidate"])
    if errors:
        raise ValueError("\n".join(errors))
    store.write_manifest("human_review", {"proof": proof, "input": store.put_blob(record)})
    protocol = store.read_manifest("protocol")
    cases = store.read_manifest("case_corpus")["final"]
    completeness = store.completeness()
    complete = (completeness["unfinished_operations"] == [operation_id]
                and not completeness["artifact_errors"] and not completeness["record_failure"]
                and bool(store.read_manifest("search_result", {}).get("search_complete")))
    report = compare(final["original"], final["candidate"], cases, protocol, complete=complete, human_review=proof)
    final["report"] = report
    store.write_manifest("final_comparison", final)
    store.append("human_review_recorded", {"proof": store.put_blob(proof), "result": report}, stage="human_review")
    return {"result": report}


def _assess_calibration(args, store):
    protocol = store.read_manifest("protocol")
    if not protocol:
        raise ValueError("Prepare a completed pilot before Judge reference assessments.")
    protocol = freeze_protocol(protocol, args.source)
    pilot = store.read_manifest("pilot")
    if not isinstance(pilot, dict) or not isinstance(pilot.get("outputs"), list):
        raise ValueError("Complete the pilot before Judge reference assessments.")
    requests = calibration_assessment_requests(store)
    context = calibration_context(protocol)
    if any(request["assessment_context"] != context for request in requests):
        raise ValueError("The selected Judge context differs from the fixed reference presentation.")
    source_trials = {trial["trial_id"]: trial for row in pilot["outputs"] for trial in row["trials"]}
    if any(request["source_trial_id"] not in source_trials for request in requests):
        raise ValueError("Prepare the reference batch from recorded pilot trials.")
    if not requests:
        return {"status": "reference assessments complete", "recorded": 0, "pending": 0}
    evaluator = _runtime(args, protocol, store)
    recorded = 0
    try:
        for request in requests:
            prepared = prepare_case(request["case"], store.root / "reference-evidence" / request["packet_id"] / uuid.uuid4().hex,
                                    args.cache, require_ready=True)
            prepared["packet_summary"] = source_trials[request["source_trial_id"]].get("terminal", {}).get("summary")
            for attempt in range(protocol["retry"]["assessment_attempts"]):
                operation = store.begin_operation("reference_assessment", stage="calibration", case_id=request["case"]["id"])
                root = store.root / "reference-assessments" / request["packet_id"] / operation
                judgment = None
                try:
                    judgment = evaluator.runtime.assess_packet(prepared, request["packet_path"], root,
                                                                evaluator.settings("judge"), protocol["rubric"] | {
                                                                    "operator_context": protocol["operator_context"]})
                    require_inspection(judgment, check_packet(Path(request["packet_path"]),
                                                             required_sections=required_sections(request["case"])))
                    judgment["assessment"] = {"id": operation, "case_id": request["case"]["id"],
                                              "packet": request["packet"], "reference_hash": content_hash(request["case"]["references"]),
                                              "rubric_hash": digest(protocol["rubric"]), "source_trial_id": request["source_trial_id"]}
                    bind_assessment_context(judgment, protocol, expected_context=request["assessment_context"])
                    judgment["preserved_artifacts"] = evaluator._runtime_artifacts(judgment)
                    result = record_calibration_assessments(store, {request["packet_id"]: judgment})
                    store.end_operation(operation, "success", result=result)
                    recorded += 1
                    break
                except (ValueError, RuntimeError, OSError) as error:
                    store.barrier()
                    store.end_operation(operation, "failed", error={"type": type(error).__name__, "message": str(error)},
                                         result={"artifacts": evaluator._failure_artifacts(error, judgment),
                                                 "judgment": store.put_blob(judgment) if judgment is not None else None},
                                         absence_reasons={"assessment": "The Judge reference assessment did not complete."})
                    if attempt + 1 == protocol["retry"]["assessment_attempts"]:
                        raise RuntimeError("The Judge reference assessment remains incomplete.") from error
    finally:
        evaluator.runtime.close()
    return {"status": "reference assessments complete", "recorded": recorded,
            "pending": len(calibration_assessment_requests(store))}


def _demo(output):
    class DemoEvaluator:
        def evaluate(self, candidate, cases, stage, capture_traces, context):
            version = candidate["demo_directive"]
            scores = {"original": [.55, .55], "clearer": [.8, .7], "specialist": [.45, .85], "worse": [.4, .4]}[version]
            values = [scores[index % 2] for index in range(len(cases))]
            return {"outputs": [{"case_id": case["id"], "demonstration": True} for case in cases], "scores": values,
                    "trajectories": [{"feedback": "Clarify the causal explanation."} for _ in cases] if capture_traces else None}
    class DemoProposer:
        counter = 0
        def propose(self, candidate, reflective_dataset, components_to_update, context):
            self.counter += 1
            child = ("clearer", "specialist", "worse")[min(self.counter - 1, 2)]
            return {"texts": {"demo_directive": child}, "hypothesis": "This demonstration exercises native acceptance."}
    protocol = {"version": 1, "component": "demo_directive", "seed_candidate": {"demo_directive": "original"},
                "search": {"proposal_count": 1, "minibatch_size": 2, "concurrency": 1, "seed": 4,
                           "sampler": "epoch_shuffled", "skip_perfect": False,
                           "completion": {"max_iterations": 3, "stagnation_iterations": None, "minimum_gain": None},
                           "confirmation": {"enabled": False}}}
    with RecordStore(output) as store:
        store.write_manifest("protocol", protocol | {"status": "token-free demonstration", "model_calls": 0})
        result = run_search(protocol, [{"id": "training-1"}, {"id": "training-2"}],
                            [{"id": "validation-1"}, {"id": "validation-2"}], DemoEvaluator(), DemoProposer(), store)
        viewer = export_view(store, output / "viewer")
        return {"status": "token-free demonstration", "best_candidate": result.best_candidate_id,
                "complete": store.completeness()["complete"], "viewer": str(viewer), "model_calls": 0}


def _resolved_protocol(path, source):
    protocol = read_protocol(path)
    if protocol.get("source_revision") == "checkout":
        protocol["source_revision"] = source_revision(source)
    protocol.setdefault("product_wrapper_hashes", product_wrapper_hashes(source))
    if not protocol.get("protected", {}).get("spans"):
        protocol["protected"] = protected_requirements(source)
    return protocol


def _execute_run(args):
    args.output = durable_directory(args.output)
    if args.resume:
        from .continuation import recorded_source
        with RecordStore(args.output, read_only=True) as previous:
            recorded = previous.read_manifest("protocol")
            if recorded and previous.read_manifest("source_archive"):
                current = args.source if args.source.is_dir() else SOURCE
                args.source = recorded_source(recorded, current, store=previous)
    protocol = _resolved_protocol(args.protocol, args.source)
    cases = _cases(protocol)
    _assert_ready(protocol, cases)
    directive, source_input = original_directive(args.source)
    seed_candidate = {"packager_directive": directive}
    case_hashes = {name: {case["id"]: digest(case) for case in values}
                   for name, values in cases.items()}
    if protocol.get("seed_candidate") is not None and protocol["seed_candidate"] != seed_candidate:
        raise ValueError("The original directive differs from the fixed protocol. Restore the recorded source before continuation.")
    if protocol.get("case_hashes") is not None and protocol["case_hashes"] != case_hashes:
        raise ValueError("The cases differ from the fixed protocol. Restore the recorded cases before continuation.")
    protocol["seed_candidate"] = seed_candidate
    protocol["case_hashes"] = case_hashes
    frozen = freeze_protocol(protocol, args.source)
    with RecordStore(args.output) as store, contextlib.ExitStack() as stack:
        existing = store.read_manifest("protocol")
        if existing and existing != frozen:
            raise ValueError("Use the original protocol and source checkout to continue, or use a new output directory.")
        if existing and not args.resume:
            raise ValueError("Use --resume to continue this run, or use a new output directory.")
        if not existing and args.resume:
            raise ValueError("Use an existing run directory with --resume, or start without --resume.")
        if args.resume:
            store.interrupt_unfinished("The previous run session ended before completion.")
        store.enable_backup()
        store.write_manifest("protocol", frozen)
        store.write_manifest("source_input", source_input)
        store.write_manifest("case_corpus", cases)
        preserve_source(store, frozen, args.source)
        launch = args.protocol.with_name("launch.json")
        if launch.is_file():
            store.write_manifest("browser_launch", json.loads(launch.read_bytes()))
        if frozen.get("assessment_status") != "uncalibrated":
            calibration = {}
            for name in ("record", "check_record"):
                reference = frozen["calibration"][name]
                calibration[name] = store.put_blob((Path(protocol["_config_root"]) / reference["path"]).read_bytes())
            store.write_manifest("calibration", calibration)
        store.append("run_session_started", {"continued": args.resume}, stage="preparation")
        try:
            try:
                live = stack.enter_context(LiveViewer(store.root, port=args.port, experiment_id=store.experiment_id))
            except OSError as error:
                if error.errno == errno.EADDRINUSE:
                    action = "Use another --port value, or leave --port unset. Then continue with --resume."
                elif error.errno in (errno.EACCES, errno.EPERM):
                    action = "Permit a loopback listener in this environment, then continue with --resume."
                else:
                    action = "Resolve the reported listener error, then continue with --resume."
                detail = str(error)
                separator = " " if detail.endswith((".", "!", "?")) else ". "
                raise ValueError(f"The live viewer cannot start: {detail}{separator}{action}") from error
            message("Live viewer: " + live.url, stream=sys.stderr)
            message("Genetic-Pareto (GEPA) evolves the directive. The final holdout set compares its result with the original.", stream=sys.stderr)
            message("The viewer follows new records. Select an earlier event to inspect the run's history.", stream=sys.stderr)
            if frozen.get("assessment_status") == "uncalibrated":
                message("The Judge is uncalibrated. The run produces experimental candidates; source adoption remains separate.", stream=sys.stderr)
            if not args.no_browser and not webbrowser.open(live.url):
                message("Open the live viewer address in your browser.", stream=sys.stderr)
            evaluator = _runtime(args, frozen, store)
            stack.callback(evaluator.runtime.close)
            saved = store.read_manifest("search_result")
            if args.resume and saved and saved.get("search_complete"):
                search = type("SearchResult", (), saved)()
                store.append("search_reused", {"reason": "The native search already completed."}, stage="search")
            else:
                with Progress(store):
                    search = run_search(frozen, cases["trainset"], cases["valset"], evaluator, Proposer(evaluator), store,
                                        resume=args.resume and store.read_manifest("search_identity") is not None)
                store.write_manifest("search_result", search.to_dict())
            if search.best_candidate == frozen["seed_candidate"]:
                report = {"status": "original retained"}
                store.append("final_comparison_skipped", {"reason": "GEPA returned the original directive."}, stage="final")
            elif store.read_manifest("final_comparison"):
                report = store.read_manifest("final_comparison")["report"]
            else:
                with Progress(store):
                    report = _final(evaluator, store, frozen, frozen["seed_candidate"], search.best_candidate, cases["final"])
            store.append("run_session_completed", {"outcome": "success", "result": report}, stage="final")
        except BaseException as error:
            store.barrier()
            store.append("run_session_completed", {"outcome": "interrupted" if isinstance(error, KeyboardInterrupt) or control_cancelled() else "failed",
                         "error": {"type": type(error).__name__, "message": str(error)}}, stage="experiment")
            raise
        finally:
            portable = export_view(store, args.output / "viewer")
            message("Portable viewer: " + str(portable), stream=sys.stderr)
        message("Experiment complete: " + str(report["status"]), stream=sys.stderr)
        if not args.no_wait:
            message("The live viewer stays open. Press Ctrl+C to close the viewer; the experiment records remain intact.", stream=sys.stderr)
            live.wait()
        return {"result": report, "viewer": str(portable), "completeness": store.completeness()}


def parser():
    result = CommandParser(prog="culler-evals", description="Evaluate Culler directives with Genetic-Pareto (GEPA) in isolated developer environments.")
    result.add_argument("--json", action="store_true", help="Return machine-readable output.")
    commands = result.add_subparsers(dest="command", required=True, metavar="COMMAND", parser_class=CommandParser)
    commands.add_parser("check", help="Run checks without model calls.")
    validate = commands.add_parser("validate", help="Show protocol and case readiness.")
    validate.add_argument("--protocol", type=Path, required=True)
    prepare = commands.add_parser("prepare", help="Reconstruct pinned case repositories and artifacts.")
    prepare.add_argument("cases", type=Path, nargs="+")
    prepare.add_argument("--output", type=Path, required=True)
    prepare.add_argument("--cache", type=Path, required=True)
    original = commands.add_parser("original", help="Export the original directive and its protected requirements.")
    original.add_argument("--source", type=Path, default=SOURCE)
    original.add_argument("--output", type=Path, required=True)
    run = commands.add_parser("run", help="Run GEPA and the final comparison with a live viewer.",
                              description="Run Genetic-Pareto (GEPA), compare its candidate on the final holdout set, and show live records. Source adoption remains separate.",
                              usage="culler-evals run [OPTIONS]\n       --protocol FILE --output DIR\n       --cache DIR", formatter_class=PilotHelpFormatter)
    run.add_argument("--protocol", type=Path, required=True, metavar="FILE", help="Use this complete experiment configuration.")
    run.add_argument("--source", type=Path, default=SOURCE, metavar="DIR", help="Use this Culler source checkout.")
    run.add_argument("--output", type=Path, required=True, metavar="DIR", help="Save the run in a persistent directory outside temporary storage.")
    run.add_argument("--cache", type=Path, required=True, metavar="DIR", help="Use this cache for pinned case repositories.")
    run.add_argument("--credentials", type=Path, metavar="FILE", help="Use this private credential configuration.")
    run.add_argument("--resume", action="store_true", help="Continue the unchanged run in the output directory.")
    run.add_argument("--port", type=int, default=0, help="Use this loopback port. The default selects an available port.")
    run.add_argument("--no-browser", action="store_true", help="Print the viewer address without opening a browser.")
    run.add_argument("--no-wait", action="store_true", help="Close the live server after the run. Preserve the portable viewer.")
    serve = commands.add_parser("serve", help="Configure and start experiments in the browser.",
                                description="Configure Genetic-Pareto (GEPA) experiments in the browser. Each experiment starts only when you select Start experiment.",
                                usage="culler-evals serve [OPTIONS]\n       --protocol FILE", formatter_class=PilotHelpFormatter)
    serve.add_argument("--protocol", type=Path, required=True, metavar="FILE", help="Use these experiment defaults.")
    serve.add_argument("--preset", metavar="NAME", help="Select a saved preset before the browser opens.")
    serve.add_argument("--source", type=Path, default=SOURCE, metavar="DIR", help="Use this Culler source checkout.")
    serve.add_argument("--output-root", type=Path, default=DEFAULT_EXPERIMENTS, metavar="DIR", help="Save experiments in persistent storage. Default: ~/.local/share/culler-evals/experiments.")
    serve.add_argument("--cache", type=Path, default=DEFAULT_CACHE, metavar="DIR", help="Use this cache for pinned case repositories. Default: ~/.local/share/culler-evals/cache.")
    serve.add_argument("--credentials", type=Path, metavar="FILE", help="Use this private credential configuration.")
    serve.add_argument("--port", type=int, default=0, help="Use this loopback port. The default selects an available port.")
    serve.add_argument("--no-browser", action="store_true", help="Print the browser address without opening a browser.")
    recover = commands.add_parser("recover", help="Restore an absent experiment directory from its local backup.",
                                  description="Restore an absent experiment directory from its local backup. Recovery preserves existing records and makes no model calls.",
                                  formatter_class=PilotHelpFormatter)
    recover.add_argument("backup", type=Path, help="Use this experiment backup. Existing records remain unchanged.")
    view = commands.add_parser("view", help="Open a live viewer for an existing run.", formatter_class=PilotHelpFormatter)
    view.add_argument("experiment", type=Path, help="Read this experiment directory.")
    view.add_argument("--port", type=int, default=0, help="Use this loopback port. The default selects an available port.")
    view.add_argument("--no-browser", action="store_true", help="Print the viewer address without opening a browser.")
    for name in ("contract", "evaluate", "evolve", "continue", "final"):
        pilot_help = {
            "description": "Run Packager trials and Judge assessments on development cases. Scores from an uncalibrated Judge remain exploratory.",
            "usage": "culler-evals evaluate [OPTIONS]\n       --protocol FILE\n       --cases FILE [FILE ...]\n       --output DIR --cache DIR",
            "formatter_class": PilotHelpFormatter,
        } if name == "evaluate" else {}
        command = commands.add_parser(name, help={"evaluate": "Evaluate a directive on development cases.",
                                                  "contract": "Check role requirements without quality scores.",
                                                  "evolve": "Execute one complete GEPA experiment.",
                                                  "continue": "Continue an interrupted experiment.",
                                                  "final": "Execute the frozen final comparison."}[name], **pilot_help)
        command.add_argument("--protocol", type=Path, required=True, metavar="FILE" if name == "evaluate" else None,
                             help="Use this pilot configuration." if name == "evaluate" else None)
        command.add_argument("--source", type=Path, default=SOURCE, metavar="DIR" if name == "evaluate" else None,
                             help="Use this Culler checkout. The default is the framework's checkout." if name == "evaluate" else None)
        command.add_argument("--output", type=Path, required=True, metavar="DIR" if name == "evaluate" else None,
                             help="Save trial records and the viewer in this directory." if name == "evaluate" else None)
        command.add_argument("--cache", type=Path, required=True, metavar="DIR" if name == "evaluate" else None,
                             help="Use this cache for pinned source repositories." if name == "evaluate" else None)
        command.add_argument("--credentials", type=Path, metavar="FILE" if name == "evaluate" else None,
                             help="Use this private credential configuration." if name == "evaluate" else None)
        if name in ("contract", "evaluate"):
            command.add_argument("--cases", type=Path, nargs="+", required=True, metavar="FILE" if name == "evaluate" else None,
                                 help="Select case manifests. Use their order from pilot.cases when that field exists." if name == "evaluate" else None)
            command.add_argument("--directive", type=Path, metavar="FILE" if name == "evaluate" else None,
                                 help="Evaluate this directive instead of the original directive." if name == "evaluate" else None)
        if name == "contract":
            command.add_argument("--trials", type=int, required=True, help="Use this many fresh trials per case.")
    for name in ("inspect", "export", "index"):
        command = commands.add_parser(name, help=f"{name.capitalize()} recorded experiment evidence.")
        command.add_argument("experiment", type=Path)
        if name in ("inspect", "export"):
            command.add_argument("--output", type=Path, required=True)
        else:
            command.add_argument("--tracking-uri", required=True)
    calibrate = commands.add_parser("calibrate", help="Compare Judge assessments against human references.")
    calibrate.add_argument("records", type=Path)
    calibrate.add_argument("--minimum-agreement", type=float, required=True)
    calibrate.add_argument("--minimum-critical-detection", type=float, required=True)
    calibrate.add_argument("--output", type=Path, required=True)
    demo = commands.add_parser("demo", help="Create a native GEPA demonstration without model calls.")
    demo.add_argument("--output", type=Path, required=True)
    human = commands.add_parser("prepare-review", help="Prepare a human comparison without directive labels.")
    human.add_argument("experiment", type=Path)
    human.add_argument("--source", type=Path, default=SOURCE)
    human.add_argument("--output", type=Path, required=True)
    review = commands.add_parser("record-review", help="Record a human comparison and apply the final decision.")
    review.add_argument("experiment", type=Path)
    review.add_argument("--record", type=Path, required=True)
    references = commands.add_parser("prepare-calibration", help="Prepare reference packets without Judge scores.",
                                     usage="culler-evals prepare-calibration\n  EXPERIMENT --batch FILE\n  --output DIRECTORY [--source DIRECTORY]")
    references.add_argument("experiment", type=Path, help="Read the completed pilot directory.")
    references.add_argument("--batch", type=Path, required=True, help="Read the reviewed reference batch manifest.")
    references.add_argument("--source", type=Path, default=SOURCE, help="Use this selected Culler checkout.")
    references.add_argument("--output", type=Path, required=True, help="Create a separate human review directory.")
    reference_judge = commands.add_parser("assess-calibration", help="Run fresh Judge reference assessments.",
                                          usage="culler-evals assess-calibration\n  EXPERIMENT --cache DIRECTORY\n  [--credentials FILE] [--source DIRECTORY]")
    reference_judge.add_argument("experiment", type=Path, help="Read the fixed reference presentation.")
    reference_judge.add_argument("--source", type=Path, default=SOURCE, help="Use this selected Culler checkout.")
    reference_judge.add_argument("--cache", type=Path, required=True, help="Reuse the pinned source cache.")
    reference_judge.add_argument("--credentials", type=Path, help="Read the separate private credential file.")
    human_calibration = commands.add_parser("record-calibration", help="Record human scores without Judge exposure.",
                                            usage="culler-evals record-calibration\n  EXPERIMENT --record FILE")
    human_calibration.add_argument("experiment", type=Path, help="Use the fixed reference presentation.")
    human_calibration.add_argument("--record", type=Path, required=True, help="Read the complete human assessment file.")
    export_calibration = commands.add_parser("export-calibration", help="Join complete human and Judge assessments.",
                                             usage="culler-evals export-calibration\n  EXPERIMENT --output DIRECTORY")
    export_calibration.add_argument("experiment", type=Path, help="Read complete reference assessments.")
    export_calibration.add_argument("--output", type=Path, required=True, help="Create separate calibration and check records.")
    return result


def execute(args):
    if args.command == "recover":
        path = restore_records(args.backup)
        return {"status": "experiment records restored", "experiment": str(path), "model_calls": 0}
    if args.command in ("contract", "evaluate", "evolve", "continue", "final"):
        args.output = durable_directory(args.output)
    if args.command == "run":
        return _execute_run(args)
    if args.command == "serve":
        from .control_server import ControlServer
        options = {"preset": args.preset} if args.preset is not None else {}
        with ControlServer(args.protocol, args.source, args.credentials, args.cache,
                           args.output_root, port=args.port, **options) as control:
            message("Experiment browser: " + control.url, stream=sys.stderr)
            message("Experiment records: " + str(args.output_root.expanduser().resolve()), stream=sys.stderr)
            message("Configure a new experiment, or select a saved experiment to resume.", stream=sys.stderr)
            message("The server invokes models only after Start experiment or Resume experiment.", stream=sys.stderr)
            if not args.no_browser and not webbrowser.open(control.url):
                message("Open the experiment browser address in your browser.", stream=sys.stderr)
            message("Press Ctrl+C to close the server and interrupt an active experiment. The records remain intact.", stream=sys.stderr)
            control.wait()
        return {"status": "experiment browser closed"}
    if args.command == "view":
        with RecordStore(args.experiment, read_only=True):
            with LiveViewer(args.experiment, port=args.port) as live:
                message("Live viewer: " + live.url, stream=sys.stderr)
                if not args.no_browser and not webbrowser.open(live.url):
                    message("Open the live viewer address in your browser.", stream=sys.stderr)
                message("Press Ctrl+C to close the viewer. The experiment records remain intact.", stream=sys.stderr)
                live.wait()
        return {"status": "viewer closed"}
    if args.command == "check":
        subprocess.run([sys.executable, "-m", "pytest"], cwd=SOURCE / "evals", check=True)
        return {"status": "checks passed", "model_calls": 0}
    if args.command == "validate":
        protocol = _resolved_protocol(args.protocol, SOURCE)
        cases = _cases(protocol)
        errors = readiness(protocol, cases)
        return {"ready": not errors, "missing_inputs": errors, "cases": {name: len(values) for name, values in cases.items()}}
    if args.command == "prepare":
        cases = validate_cases(args.cases)
        prepared = [prepare_case(case, args.output / case["id"], args.cache) for case in cases]
        return {"cases": [{key: case[key] for key in ("case_id", "repository_path", "readiness")} for case in prepared]}
    if args.command == "original":
        directive, manifest = original_directive(args.source)
        args.output.mkdir(parents=True, exist_ok=True)
        (args.output / "directive.md").write_text(directive)
        _write(args.output / "input.json", manifest)
        _write(args.output / "protected.json", protected_requirements(args.source))
        return {"directive": str(args.output / "directive.md"), "input": manifest}
    if args.command in ("inspect", "export", "index"):
        with RecordStore(args.experiment, read_only=True) as store:
            if args.command == "index":
                return {"mlflow_run_id": index_mlflow(store, args.tracking_uri)}
            return {"viewer": str(export_view(store, args.output)), "completeness": store.completeness()}
    if args.command == "calibrate":
        records = json.loads(args.records.read_text())
        report = calibration_report(records, minimum_agreement=args.minimum_agreement,
                                    minimum_critical_detection=args.minimum_critical_detection)
        _write(args.output, report)
        return report
    if args.command == "demo":
        return _demo(args.output)
    if args.command in ("prepare-calibration", "assess-calibration", "record-calibration", "export-calibration"):
        with RecordStore(args.experiment) as store:
            operation = store.begin_operation(args.command.replace("-", "_"), stage="calibration")
            try:
                if args.command == "prepare-calibration":
                    frozen = store.read_manifest("protocol")
                    if not frozen:
                        raise ValueError("Complete the pilot before the human reference presentation.")
                    freeze_protocol(frozen, args.source)
                    result = prepare_calibration_review(store, args.source, args.output, json.loads(args.batch.read_text()),
                                                        batch_root=args.batch.parent)
                elif args.command == "assess-calibration":
                    with Progress(store):
                        result = _assess_calibration(args, store)
                elif args.command == "record-calibration":
                    result = record_human_calibration(store, json.loads(args.record.read_text()))
                else:
                    result = export_calibration_records(store, args.output)
                store.end_operation(operation, "success", result=result)
                return result
            except BaseException as error:
                store.barrier()
                store.end_operation(operation, "failed", error={"type": type(error).__name__, "message": str(error)},
                                     absence_reasons={"calibration": "The calibration operation did not complete."})
                raise
    if args.command in ("prepare-review", "record-review"):
        with RecordStore(args.experiment) as store:
            operation = store.begin_operation(args.command.replace("-", "_"), stage="human_review")
            try:
                result = _prepare_review(store, args.source, args.output) if args.command == "prepare-review" else _record_review(store, args.record, operation)
                store.end_operation(operation, "success", result=result)
                if args.command == "record-review":
                    result["viewer"] = str(export_view(store, store.root / "viewer"))
                return result
            except BaseException as exc:
                store.barrier()
                store.end_operation(operation, "failed", error={"type": type(exc).__name__, "message": str(exc)},
                                    absence_reasons={"human_review": "The review operation did not complete."})
                raise
    protocol = read_protocol(args.protocol)
    protocol.setdefault("product_wrapper_hashes", product_wrapper_hashes(args.source))
    if args.command == "contract":
        errors = contract_readiness(protocol)
        if errors:
            raise ValueError("The contract protocol is incomplete:\n" + "\n".join(errors))
        if args.trials <= 0:
            raise ValueError("The contract trial count must be positive.")
        frozen = freeze_protocol(protocol, args.source)
        cases = validate_cases(args.cases, require_ready=True)
        directive, input_record = original_directive(args.source)
        candidate = {"packager_directive": args.directive.read_text() if args.directive else directive}
        frozen["contract_trials"] = args.trials
        frozen["case_hashes"] = {case["id"]: digest(case) for case in cases}
        frozen["seed_candidate"] = candidate
        frozen = freeze_protocol(frozen, args.source)
        with RecordStore(args.output) as store, contextlib.ExitStack() as stack:
            existing = store.read_manifest("protocol")
            if existing and existing != frozen:
                raise ValueError("The contract run must preserve its frozen inputs.")
            store.enable_backup()
            store.write_manifest("protocol", frozen)
            store.write_manifest("source_input", input_record)
            store.write_manifest("case_corpus", {"contract": cases})
            preserve_source(store, frozen, args.source)
            evaluator = _runtime(args, protocol, store)
            stack.callback(evaluator.runtime.close)
            with Progress(store):
                rows = evaluator.evaluate_contract(candidate, cases, trial_count=args.trials)
            store.write_manifest("contract", rows)
            return {"contract_passed": all(row["passed"] for row in rows), "quality_score": None,
                    "viewer": str(export_view(store, args.output / "viewer"))}
    all_cases = _cases(protocol)
    corpus = dict(all_cases)
    if args.command == "evaluate":
        cases = _pilot_cases(protocol, args.cases)
        if {case["group"] for case in cases}.intersection(case["group"] for case in all_cases["final"]):
            raise ValueError("The pilot cannot use final case groups.")
        corpus["pilot"] = cases
    _assert_ready(protocol, {"pilot": cases} if args.command == "evaluate" else all_cases, full=args.command != "evaluate")
    frozen = freeze_protocol(protocol, args.source)
    frozen["case_hashes"] = {name: {case["id"]: digest(case)
                                   for case in values} for name, values in corpus.items()}
    directive, input_record = original_directive(args.source)
    frozen["seed_candidate"] = {"packager_directive": directive}
    if args.command == "evaluate":
        candidate = {"packager_directive": args.directive.read_text() if args.directive else directive}
        frozen["evaluated_candidate"] = candidate
        frozen["assessment_status"] = "calibrated" if protocol["rubric"].get("status") == "calibrated" else "exploratory"
    frozen = freeze_protocol(frozen, args.source)
    with RecordStore(args.output) as store, contextlib.ExitStack() as stack:
        existing = store.read_manifest("protocol")
        if existing and existing != frozen:
            raise ValueError("The experiment must preserve its frozen protocol.")
        store.write_manifest("protocol", frozen)
        store.write_manifest("source_input", input_record)
        store.enable_backup()
        store.write_manifest("case_corpus", corpus)
        preserve_source(store, frozen, args.source)
        if args.command != "evaluate":
            calibration = {}
            for name in ("record", "check_record"):
                reference = protocol["calibration"][name]
                path = Path(protocol["_config_root"]) / reference["path"]
                calibration[name] = store.put_blob(path.read_bytes())
            store.write_manifest("calibration", calibration)
        evaluator = _runtime(args, protocol, store)
        stack.callback(evaluator.runtime.close)
        if args.command == "evaluate":
            with Progress(store):
                result = evaluator.evaluate(candidate, cases, "pilot", True)
            result["assessment_status"] = frozen["assessment_status"]
            store.write_manifest("pilot", result)
            return {"status": frozen["assessment_status"] + " pilot complete", "assessment_status": frozen["assessment_status"],
                    "scores": result["scores"], "viewer": str(export_view(store, args.output / "viewer"))}
        if args.command in ("evolve", "continue"):
            with Progress(store):
                search = run_search(frozen, all_cases["trainset"], all_cases["valset"], evaluator, Proposer(evaluator), store,
                                    resume=args.command == "continue")
            store.write_manifest("search_result", search.to_dict())
        else:
            saved = store.read_manifest("search_result")
            if not saved:
                raise ValueError("The final comparison requires a completed search result.")
            search = type("SearchResult", (), saved)()
        if search.best_candidate == frozen["seed_candidate"]:
            store.append("final_comparison_skipped", {"reason": "GEPA returned the original directive."}, stage="final")
            report = {"status": "original retained"}
        elif store.read_manifest("final_comparison"):
            report = store.read_manifest("final_comparison")["report"]
        else:
            with Progress(store):
                report = _final(evaluator, store, protocol, frozen["seed_candidate"], search.best_candidate, all_cases["final"])
        return {"result": report, "viewer": str(export_view(store, args.output / "viewer")),
                "completeness": store.completeness()}


def main():
    args = parser().parse_args()
    try:
        result = execute(args)
        if args.json:
            print(json.dumps(result, indent=2, default=str))
        else:
            for key, value in result.items():
                if key == "missing_inputs":
                    message("Required experiment inputs:")
                    for item in value:
                        message("- " + item)
                elif isinstance(value, dict):
                    for subkey, subvalue in value.items():
                        if not isinstance(subvalue, (list, dict)):
                            field = "Content hash" if subkey == "sha256" else subkey.replace('_', ' ')
                            message(f"{key.replace('_', ' ').capitalize()} · {field}: {subvalue}")
                        elif subkey == "reasons":
                            for reason in subvalue:
                                message("Reason: " + reason)
                elif isinstance(value, list):
                    message(f"{key.replace('_', ' ').capitalize()}: {len(value)} records")
                else:
                    message(f"{key.replace('_', ' ').capitalize()}: {value}")
        if result.get("ready") is False:
            return 1
        if result.get("contract_passed") is False:
            return 1
    except (ValueError, RuntimeError, OSError, subprocess.CalledProcessError) as error:
        message(str(error), stream=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
