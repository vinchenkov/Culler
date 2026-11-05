"""Human reference assessments without exposure to Judge scores."""

from __future__ import annotations

import asyncio
import hashlib
import html
import json
import math
from pathlib import Path
import secrets

from .assessment import AssessmentError, validate_judgment
from .browser import PacketBrowser
from .cases import content_hash as case_hash
from .protocol import DIMENSIONS, dimension_maximum, calibration_context, digest, freeze_protocol


CHALLENGE_CONDITIONS = {
    "weak_explanation", "excess_repetition", "material_error", "instruction_injection",
}


class CalibrationError(ValueError):
    """The reference batch lacks fixed evidence or a complete assessment."""


def _text(value) -> bool:
    return isinstance(value, str) and bool(value.strip())


def _file(reference: dict, root: Path) -> bytes:
    if not isinstance(reference, dict) or not _text(reference.get("path")):
        raise CalibrationError("The reference requires an artifact path and content hash.")
    path = Path(reference["path"])
    path = path if path.is_absolute() else root / path
    try:
        data = path.read_bytes()
    except OSError as exc:
        raise CalibrationError("A required reference artifact is absent.") from exc
    if hashlib.sha256(data).hexdigest() != reference.get("sha256"):
        raise CalibrationError("A reference artifact differs from its content hash.")
    return data


def _cases(store) -> dict:
    corpus = store.read_manifest("case_corpus") or {}
    return {case["id"]: case for case in corpus.get("pilot", [])}


def _pilot_trials(store) -> dict:
    pilot = store.read_manifest("pilot")
    if not isinstance(pilot, dict):
        raise CalibrationError("The reference batch requires completed pilot results.")
    trials = {}
    for case in pilot.get("outputs", []):
        for trial in case["trials"]:
            if trial["trial_id"] in trials:
                raise CalibrationError("The pilot repeats a trial identifier.")
            trials[trial["trial_id"]] = trial | {"case_id": case["case_id"]}
    return trials


def _fixed_context(store, presentation: dict) -> tuple[dict, dict]:
    cases = _cases(store)
    protocol = store.read_manifest("protocol")
    rubric = protocol["rubric"]
    if digest(rubric) != presentation["rubric_hash"]:
        raise CalibrationError("The rubric differs from the fixed reference presentation.")
    if digest(protocol.get("operator_context")) != presentation["operator_context_hash"]:
        raise CalibrationError("The operator context differs from the fixed reference presentation.")
    declared = protocol.get("product_wrapper_hashes", presentation["assessment_context"]["wrapper_hashes"])
    if calibration_context(protocol | {"product_wrapper_hashes": declared}) != presentation["assessment_context"]:
        raise CalibrationError("The Judge context differs from the fixed reference presentation.")
    for item in presentation["items"]:
        if item["case_id"] not in cases or digest(cases[item["case_id"]]) != item["case_hash"]:
            raise CalibrationError("The case differs from the fixed reference presentation.")
    return cases, rubric


def bind_assessment_context(judgment: dict, protocol: dict, *, expected_context: dict | None = None) -> dict:
    """Bind the actual Judge runtime and display to the fixed assessment context."""
    try:
        context = calibration_context(protocol)
    except (ValueError, KeyError, TypeError) as error:
        raise AssessmentError("The Judge context lacks its fixed runtime and product wrapper hashes.") from error
    if expected_context is not None and context != expected_context:
        raise AssessmentError("The Judge context differs from the fixed reference presentation.")
    runtime = judgment.get("_runtime", {})
    metadata, browser = runtime.get("metadata", {}), runtime.get("browser", {})
    judge = context["judge_runtime"]
    for actual, expected in (("requested_model", "model"), ("harness", "harness"), ("image", "image")):
        if metadata.get(actual) != judge[expected]:
            raise AssessmentError("The actual Judge runtime differs from its fixed assessment context.")
    effective = metadata.get("effective_models")
    if effective is not None and effective != [judge["model"]]:
        raise AssessmentError("The Judge reports a different effective model.")
    if "effort" in judge:
        expected = judge["effort"]
        if any(metadata.get(name) != expected for name in ("requested_effort", "configured_effort")):
            raise AssessmentError("The Judge effort differs from its fixed assessment context. Assess the reference again with the selected effort.")
        observed = metadata.get("observed_efforts")
        if observed is not None and observed != [expected]:
            raise AssessmentError("The Judge reports a different effort. Assess the reference again with the selected effort.")
    for name in ("channel", "base_url"):
        if name in judge and metadata.get(name) != judge[name]:
            raise AssessmentError("The actual Judge transport differs from its fixed assessment context.")
    if browser.get("wrapper_hashes") != context["wrapper_hashes"]:
        raise AssessmentError("The Judge display used different or unverified product wrapper bytes.")
    if browser.get("viewport") != judge["viewport"]:
        raise AssessmentError("The Judge display used a different or unverified viewport.")
    if runtime.get("assessment_contract_hash") != context["assessment_contract_hash"]:
        raise AssessmentError("The Judge used different or unverified assessment instructions and packet contracts.")
    binding = judgment.get("assessment")
    if not isinstance(binding, dict):
        raise AssessmentError("The Judge result lacks its assessment identity.")
    if "context" in binding and binding["context"] != context:
        raise AssessmentError("The Judge result claims a different assessment context.")
    binding["context"] = context
    return context


def _bound_assessment(judgment: dict, packet: dict, case: dict, rubric: dict) -> None:
    validate_judgment(judgment, rubric)
    if judgment["uninspected_content"] or judgment.get("_runtime", {}).get("render_failures"):
        raise CalibrationError("A calibration reference requires complete Judge inspection without a render failure.")
    binding = judgment.get("assessment", {})
    if not isinstance(binding, dict) or not _text(binding.get("id")) or binding.get("case_id") != case["id"]:
        raise CalibrationError("The Judge assessment requires its identifier and exact case.")
    if binding.get("packet", {}).get("sha256") != packet["sha256"]:
        raise CalibrationError("The Judge assessment addresses different packet bytes.")
    if binding.get("reference_hash") != case_hash(case["references"]):
        raise CalibrationError("The Judge assessment addresses different reference facts.")
    if binding.get("rubric_hash") != digest(rubric):
        raise CalibrationError("The Judge assessment addresses a different rubric.")


def _public_facts(value) -> str:
    if isinstance(value, list):
        return "<ul>" + "".join("<li>" + _public_facts(item) + "</li>" for item in value) + "</ul>"
    if isinstance(value, dict):
        return "<dl>" + "".join("<dt>" + html.escape(str(key).replace("_", " ")) + "</dt><dd>" +
                                _public_facts(item) + "</dd>" for key, item in value.items()) + "</dl>"
    return html.escape(str(value))


def _packet_page(source: Path, packet: Path, output: Path, viewport: dict) -> dict:
    with PacketBrowser(source, packet, output, viewport["width"], viewport["height"]) as browser:
        async def document():
            return await browser.frame.content(), await browser.page.locator("#packet").get_attribute("sandbox")
        body, sandbox = asyncio.run_coroutine_threadsafe(document(), browser.loop).result(timeout=30)
        wrapper_hashes = browser.report()["wrapper_hashes"]
    page = ('<!doctype html><meta charset="utf-8"><title>Reference packet</title>'
            '<style>html,body{margin:0}iframe{display:block;width:' + str(int(viewport["width"])) +
            'px;height:' + str(int(viewport["height"])) + 'px;border:0}</style>'
            '<iframe title="Reference packet" sandbox="' + html.escape(sandbox, quote=True) + '" srcdoc="' +
            html.escape(body, quote=True) + '"></iframe>')
    return {"html": page, "wrapper_hashes": wrapper_hashes}


def prepare_calibration_review(store, source: Path, output: Path, batch: dict, *, batch_root: Path | None = None) -> dict:
    """Fix reviewed reference packets and prepare a score-blind human directory."""
    if store.read_manifest("calibration_presentation"):
        raise CalibrationError("The experiment already contains a fixed reference presentation.")
    if not isinstance(batch, dict) or batch.get("version") != 1:
        raise CalibrationError("The reference batch requires format version 1.")
    groups = batch.get("groups", {})
    if set(groups) != {"record", "check_record"} or any(not isinstance(values, list) or not values for values in groups.values()):
        raise CalibrationError("Assign independent groups to the calibration and check records.")
    flat = groups["record"] + groups["check_record"]
    if any(not _text(group) for group in flat) or len(flat) != len(set(flat)):
        raise CalibrationError("The calibration and check groups must be distinct.")
    corpus = store.read_manifest("case_corpus") or {}
    if set(flat) & {case["group"] for case in corpus.get("final", [])}:
        raise CalibrationError("The reference batch cannot use final case groups.")
    cases, trials = _cases(store), _pilot_trials(store)
    protocol = store.read_manifest("protocol")
    fixed_protocol = freeze_protocol(protocol, source)
    assessment_context = calibration_context(fixed_protocol)
    rubric = protocol["rubric"]
    operator_context = protocol.get("operator_context")
    if not _text(operator_context):
        raise CalibrationError("The reference review requires the fixed operator context.")
    if set(rubric.get("dimensions", {})) != set(DIMENSIONS):
        raise CalibrationError("The reference review requires all six rubric dimensions.")
    items, identifiers, coverage = [], set(), set()
    root = Path(batch_root or ".").resolve()
    batch_items = batch.get("items")
    if not isinstance(batch_items, list) or not batch_items or any(not isinstance(item, dict) for item in batch_items):
        raise CalibrationError("The reference batch requires a list of packet items.")
    for item in batch_items:
        identifier = item.get("id")
        if not _text(identifier) or identifier in identifiers:
            raise CalibrationError("Each reference item requires a distinct identifier.")
        identifiers.add(identifier)
        case = cases.get(item.get("case_id"))
        trial = trials.get(item.get("source_trial_id"))
        if not case or not trial or trial["case_id"] != case["id"] or trial.get("stage") != "pilot":
            raise CalibrationError("Each reference item must identify its actual pilot case and trial.")
        group = case["group"]
        if group not in flat:
            raise CalibrationError("A reference item's group lacks a dataset assignment.")
        source_packet = trial.get("artifacts", {}).get("packet_path")
        if not source_packet or item.get("source_packet_sha256") != source_packet["sha256"]:
            raise CalibrationError("A reference item must bind its actual source packet hash.")
        source_data = store.read_blob(source_packet)
        packet = item.get("packet", {})
        if not isinstance(packet, dict):
            raise CalibrationError("The reference packet requires its origin and content hash.")
        origin = packet.get("origin")
        if origin == "pilot":
            data = source_data
        elif origin == "authored_variant":
            data = _file(packet, root)
            if data == source_data or not _text(item.get("author")):
                raise CalibrationError("An authored variant requires changed packet bytes and a named author.")
        else:
            raise CalibrationError("A reference packet must identify its pilot or authored origin.")
        packet_hash = hashlib.sha256(data).hexdigest()
        if packet.get("sha256") != packet_hash:
            raise CalibrationError("The reference packet differs from its fixed content hash.")
        kind = item.get("kind")
        conditions = item.get("conditions", [])
        if kind not in {"accurate", "challenge"} or not isinstance(conditions, list) or any(not isinstance(condition, str) for condition in conditions) or len(conditions) != len(set(conditions)):
            raise CalibrationError("Each reference item must identify its accurate or challenge purpose.")
        if kind == "accurate" and conditions or kind == "challenge" and (not conditions or not set(conditions) <= CHALLENGE_CONDITIONS):
            raise CalibrationError("The challenge conditions differ from the reference purpose.")
        coverage.update(conditions)
        review_data = _file(item.get("review"), root)
        try:
            review = json.loads(review_data)
        except ValueError as exc:
            raise CalibrationError("The independent packet review is not valid JSON.") from exc
        if not isinstance(review, dict) or not _text(review.get("reviewer")) or review.get("result") != "accepted":
            raise CalibrationError("Each reference packet requires an accepted independent review.")
        if origin == "authored_variant" and review["reviewer"] == item["author"]:
            raise CalibrationError("The reference variant requires a reviewer other than its author.")
        if any(review.get(key) != expected for key, expected in {
            "packet_sha256": packet_hash, "case_id": case["id"], "source_trial_id": trial["trial_id"],
            "kind": kind, "conditions": conditions,
        }.items()):
            raise CalibrationError("The reference review addresses different packet content or provenance.")
        packet_id = "reference-" + secrets.token_hex(8)
        items.append({"packet_id": packet_id, "item_id": identifier, "case_id": case["id"], "group": group,
                      "dataset": "record" if group in groups["record"] else "check_record", "kind": kind,
                      "conditions": conditions, "origin": origin, "source_trial_id": trial["trial_id"],
                      "source_packet_sha256": source_packet["sha256"], "packet": store.put_blob(data),
                      "review": store.put_blob(review_data), "author": item.get("author"),
                      "case_hash": digest(case), "source_judgment": trial.get("judgment") if origin == "pilot" else None})
    for group in flat:
        matching = [item for item in items if item["group"] == group]
        if len(matching) != 2 or {item["kind"] for item in matching} != {"accurate", "challenge"}:
            raise CalibrationError("Each assigned group requires one accurate packet and one challenge packet.")
    if coverage != CHALLENGE_CONDITIONS:
        raise CalibrationError("The reference batch must cover all four challenge conditions.")
    packet_hashes = [item["packet"]["sha256"] for item in items]
    if len(packet_hashes) != len(set(packet_hashes)):
        raise CalibrationError("Each reference item requires distinct packet bytes.")
    output = Path(output).resolve()
    if output.is_relative_to(store.root.resolve()) or output.exists():
        raise CalibrationError("The human directory must be absent and outside the private experiment directory.")
    output.mkdir(parents=True)
    secrets.SystemRandom().shuffle(items)
    human_items, reused = [], {}
    batch_id = "reference-batch-" + secrets.token_hex(8)
    for item in items:
        internal = store.root / "reference-packets" / item["packet_id"]
        internal.mkdir(parents=True)
        packet = internal / "packet.html"
        packet.write_bytes(store.read_blob(item["packet"]))
        page_name = item["packet_id"] + ".html"
        rendered = _packet_page(Path(source), packet, internal / "browser", assessment_context["judge_runtime"]["viewport"])
        if rendered["wrapper_hashes"] != assessment_context["wrapper_hashes"]:
            raise CalibrationError("The human display used different product wrapper bytes.")
        (output / page_name).write_text(rendered["html"])
        case = cases[item["case_id"]]
        human_items.append({"packet_id": item["packet_id"], "page": page_name,
                            "facts_html": _public_facts(case["references"]["facts"]),
                            "limits_html": _public_facts(case["references"]["limits"])})
        if item["source_judgment"]:
            judgment = json.loads(store.read_blob(item["source_judgment"]))
            _bound_assessment(judgment, item["packet"], case, rubric)
            if judgment["assessment"].get("trial_id") != item["source_trial_id"]:
                raise CalibrationError("The saved Judge assessment addresses a different source trial.")
            if judgment["assessment"].get("context") is None:
                item["source_nonreuse_reason"] = "The source assessment lacks a complete runtime and product wrapper binding."
            else:
                bind_assessment_context(judgment, fixed_protocol, expected_context=assessment_context)
                reused[item["packet_id"]] = {"origin": "source_trial", "judgment": item["source_judgment"]}
    template = {"version": 1, "batch_id": batch_id, "reviewer": None, "blinded": None,
                "records": [{"packet_id": item["packet_id"], "dimensions": {name: None for name in DIMENSIONS},
                             "critical_failure": None, "reason": None} for item in items]}
    (output / "review.json").write_text(json.dumps(template, indent=2) + "\n")
    (output / "index.html").write_text(_review_page(template, human_items, rubric, operator_context))
    rendered = [{"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}
                for path in [output / "index.html", *(output / item["page"] for item in human_items)]]
    store.write_manifest("calibration_presentation", {"version": 1, "batch_id": batch_id,
                                                     "groups": groups, "items": items, "rubric_hash": digest(rubric),
                                                     "operator_context_hash": digest(operator_context),
                                                     "assessment_context": assessment_context,
                                                     "rendered_files": rendered})
    store.write_manifest("calibration_assessments", reused)
    store.append("calibration_references_prepared", {"batch_id": batch_id, "packets": len(items),
                                                    "source": store.put_blob(batch)}, stage="calibration")
    return {"review": str(output / "index.html"), "record": str(output / "review.json"),
            "packets": len(items), "pending_judge_assessments": len(items) - len(reused)}


def calibration_assessment_requests(store) -> list[dict]:
    """Expose pending packets and fixed evidence without human scores or variant labels."""
    presentation = store.read_manifest("calibration_presentation")
    if not presentation:
        raise CalibrationError("Prepare the reference review before Judge assessments.")
    assessments = store.read_manifest("calibration_assessments") or {}
    cases, _ = _fixed_context(store, presentation)
    pending = [item for item in presentation["items"] if item["packet_id"] not in assessments]
    for item in pending:
        path = store.root / "reference-packets" / item["packet_id"] / "packet.html"
        if not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != item["packet"]["sha256"]:
            raise CalibrationError("A pending reference packet differs from its fixed content hash.")
    return [{"packet_id": item["packet_id"], "case": cases[item["case_id"]], "packet": item["packet"],
             "packet_path": str(store.root / "reference-packets" / item["packet_id"] / "packet.html"),
             "source_trial_id": item["source_trial_id"], "rubric_hash": presentation["rubric_hash"],
             "assessment_context": presentation["assessment_context"]}
            for item in pending]


def record_calibration_assessments(store, assessments: dict) -> dict:
    """Accept fresh Judge results only for their exact reference packet bytes."""
    presentation = store.read_manifest("calibration_presentation")
    if not presentation or not isinstance(assessments, dict):
        raise CalibrationError("The Judge results require a fixed reference presentation.")
    items = {item["packet_id"]: item for item in presentation["items"]}
    existing = store.read_manifest("calibration_assessments") or {}
    cases, rubric = _fixed_context(store, presentation)
    source_ids = {json.loads(store.read_blob(trial["judgment"])).get("assessment", {}).get("id")
                  for trial in _pilot_trials(store).values() if trial.get("judgment")}
    seen_ids = {json.loads(store.read_blob(entry["judgment"]))["assessment"]["id"] for entry in existing.values()}
    for packet_id, judgment in assessments.items():
        raw = store.put_blob(judgment)
        store.append("calibration_judge_result_received", {"packet_id": packet_id, "judgment": raw}, stage="calibration")
        item = items.get(packet_id)
        if not item or packet_id in existing:
            raise CalibrationError("A Judge result identifies an unknown or already assessed reference packet.")
        _bound_assessment(judgment, item["packet"], cases[item["case_id"]], rubric)
        if judgment["assessment"].get("context") is None:
            raise CalibrationError("A fresh Judge result requires its verified assessment context.")
        protocol = store.read_manifest("protocol") | {"product_wrapper_hashes": presentation["assessment_context"]["wrapper_hashes"]}
        bind_assessment_context(judgment, protocol, expected_context=presentation["assessment_context"])
        if judgment["assessment"]["id"] in source_ids:
            raise CalibrationError("An authored variant requires a fresh Judge assessment.")
        if judgment["assessment"]["id"] in seen_ids:
            raise CalibrationError("Each reference packet requires a distinct Judge assessment identifier.")
        seen_ids.add(judgment["assessment"]["id"])
        existing[packet_id] = {"origin": "fresh_reference_assessment", "judgment": raw}
    store.write_manifest("calibration_assessments", existing)
    store.append("calibration_judge_assessments_recorded", {"packets": list(assessments)}, stage="calibration")
    return {"recorded": len(assessments), "pending": len(items) - len(existing)}


def record_human_calibration(store, record: dict) -> dict:
    """Freeze a complete human review independently of Judge results."""
    presentation = store.read_manifest("calibration_presentation")
    if not presentation:
        raise CalibrationError("The experiment lacks a reference presentation. Use prepare-calibration before record-calibration.")
    if store.read_manifest("human_calibration"):
        raise CalibrationError("The experiment already contains human assessments. Prepare a new reference batch in a separate experiment.")
    _, rubric = _fixed_context(store, presentation)
    for reference in presentation["rendered_files"]:
        _file(reference, store.root)
    if not isinstance(record, dict) or record.get("version") != 1 or record.get("batch_id") != presentation["batch_id"]:
        raise CalibrationError("The human scores address a different reference batch.")
    if not _text(record.get("reviewer")) or record.get("blinded") is not True:
        raise CalibrationError("The human review requires a named reviewer and no exposure to Judge scores.")
    rows = record.get("records", [])
    expected = {item["packet_id"] for item in presentation["items"]}
    if not isinstance(rows, list) or any(not isinstance(row, dict) for row in rows) or len(rows) != len(expected) or {row.get("packet_id") for row in rows} != expected:
        raise CalibrationError("The human must assess every reference packet exactly once.")
    for row in rows:
        dimensions = row.get("dimensions", {})
        if not isinstance(dimensions, dict) or set(dimensions) != set(DIMENSIONS) or any(type(score) not in (int, float) or not math.isfinite(score) or not 0 <= score <= dimension_maximum(rubric, name)
                                                  for name, score in dimensions.items()):
            raise CalibrationError("Each human assessment requires all six finite dimension scores within their rubric ranges.")
        if type(row.get("critical_failure")) is not bool or not _text(row.get("reason")):
            raise CalibrationError("Each human assessment requires a critical failure determination and a reason.")
    reference = store.put_blob(record)
    store.write_manifest("human_calibration", {"record": reference, "batch_id": presentation["batch_id"]})
    store.append("human_calibration_recorded", {"record": reference}, stage="calibration")
    return {"recorded": len(rows)}


def export_calibration_records(store, output: Path) -> dict:
    """Join frozen human scores with their matching Judge results after the review."""
    presentation = store.read_manifest("calibration_presentation")
    human = store.read_manifest("human_calibration")
    judgments = store.read_manifest("calibration_assessments") or {}
    if not presentation or not human or len(judgments) != len(presentation["items"]):
        raise CalibrationError("Calibration export requires complete human and Judge assessments.")
    cases, rubric = _fixed_context(store, presentation)
    record = json.loads(store.read_blob(human["record"]))
    scores = {row["packet_id"]: row for row in record["records"]}
    datasets = {"record": [], "check_record": []}
    for item in presentation["items"]:
        rating = scores[item["packet_id"]]
        datasets[item["dataset"]].append({"packet_id": item["packet_id"], "case_id": item["case_id"], "group": item["group"],
                                           "reference_hash": case_hash(cases[item["case_id"]]["references"]),
                                           "assessment_context": presentation["assessment_context"],
                                           "score_maxima": {name: dimension_maximum(rubric, name) for name in DIMENSIONS},
                                           "packet": item["packet"], "human": {"reviewer": record["reviewer"],
                                           "dimensions": rating["dimensions"], "critical_failure": rating["critical_failure"],
                                           "reason": rating["reason"]},
                                           "judge": json.loads(store.read_blob(judgments[item["packet_id"]]["judgment"]))})
    output = Path(output).resolve()
    output.mkdir(parents=True, exist_ok=False)
    references = {}
    for name, values in datasets.items():
        path = output / (name + ".json")
        data = (json.dumps(values, indent=2, ensure_ascii=False) + "\n").encode()
        path.write_bytes(data)
        references[name] = {"path": str(path), "sha256": hashlib.sha256(data).hexdigest()}
    store.write_manifest("calibration_reference_records", references)
    store.append("calibration_records_exported", {"references": references}, stage="calibration")
    return references


def _review_page(template: dict, items: list[dict], rubric: dict, operator_context: str) -> str:
    payload = json.dumps({"template": template, "items": items,
                          "operator_context": operator_context,
                          "scope": rubric.get("scope", ""),
                          "assessment_guidance": rubric.get("assessment_guidance", []),
                          "dimension_guidance": {name: rubric["dimensions"][name].get("guidance", []) for name in DIMENSIONS},
                          "anchors": {name: rubric["dimensions"][name]["anchors"] for name in DIMENSIONS}}, ensure_ascii=False).replace("<", "\\u003c")
    return _PAGE.replace("__PAYLOAD__", payload)


_PAGE = '''<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Human reference review</title><style>
*{box-sizing:border-box}body{margin:0;background:#edf0f2;color:#15232d;font:15px/1.45 system-ui}header{padding:16px 24px;background:#fff;border-bottom:1px solid #ccd5da;display:flex;align-items:center;gap:18px;flex-wrap:wrap}h1{font-size:21px;margin:0}header p{margin:0;color:#4e6270}button,select,input,textarea{font:inherit}a.full{display:inline-block;padding:9px 14px;border:1px solid #a2b2be;border-radius:8px;background:#fff;color:#15232d;text-decoration:none}button{padding:9px 14px;border:1px solid #a2b2be;border-radius:8px;background:#fff;cursor:pointer}button:disabled{opacity:.45;cursor:default}.primary{background:#1d4ed8;color:white;border-color:#1d4ed8}main{display:grid;grid-template-columns:minmax(0,1fr) 410px;height:calc(100vh - 88px)}iframe{width:100%;height:100%;border:0;background:#fff}.panel{overflow:auto;padding:20px;border-left:1px solid #c8d1d8;background:#f9fbfc}label{display:block;font-weight:650;margin:14px 0 5px}input,select,textarea{width:100%;padding:8px;border:1px solid #bbc8d2;border-radius:6px;background:white}textarea{min-height:75px}details{background:white;border:1px solid #d5dee4;border-radius:8px;padding:10px;margin:12px 0}summary{cursor:pointer;font-weight:650}.anchor{color:#536879;font-size:13px;margin:5px 0}.controls{margin-left:auto;display:flex;align-items:center;gap:8px}dt{font-weight:650;text-transform:capitalize}dd{margin:4px 0 10px}ul{padding-left:18px}.notice{padding:10px;background:#e9effb;border-radius:7px}#message{color:#a12b20;margin-top:12px}.save{width:100%;margin-top:18px}@media(max-width:900px){main{display:block;height:auto}iframe{height:70vh}.panel{border-left:0}header{padding:14px}}
</style><header><div><h1>Human reference review</h1><p>Assess the packet. Keep Judge scores and the experiment viewer closed.</p></div><div class="controls"><a class="full" id="open" target="_blank" rel="noopener">Open packet at full width</a><button id="previous">← Previous</button><span id="position"></span><button id="next">Next →</button></div></header>
<main><iframe id="packet" title="Reference packet"></iframe><aside class="panel"><p class="notice">Open the packet at full width to assess its layout. Use the case facts to check accuracy. Rate the explanation through the six dimensions.</p><details><summary>Assessment instructions</summary><h2>Operator context</h2><p id="operator-context"></p><h2>Rubric scope</h2><p id="rubric-scope"></p><h2>Assessment guidance</h2><div id="assessment-guidance"></div></details><details><summary>Authoritative case facts</summary><div id="facts"></div></details><details><summary>Evidence limits</summary><div id="limits"></div></details><div id="scores"></div><label for="critical">Does the packet contain a critical failure?</label><select id="critical"><option value="">Select a determination</option><option value="false">No</option><option value="true">Yes</option></select><p class="anchor">A material factual error, invented verification, omitted condition, incorrect cause, or broken resource contract can constitute a critical failure.</p><label for="reason">Reason for these assessments</label><textarea id="reason" placeholder="Identify the packet content and case facts that support your assessment."></textarea><label for="reviewer">Human reviewer</label><input id="reviewer" autocomplete="off"><label><input type="checkbox" id="blinded" style="width:auto"> I did not inspect Judge scores, variant labels, or expected errors.</label><button id="save" class="primary save">Download completed review</button><div id="message" role="status"></div><p id="complete" class="anchor"></p></aside></main>
<script>const data=__PAYLOAD__;let review=structuredClone(data.template),index=0;const key='culler-reference-review:'+review.batch_id;try{const saved=JSON.parse(localStorage.getItem(key));if(saved?.batch_id===review.batch_id)review=saved;}catch{}const names=Object.keys(review.records[0].dimensions),el=id=>document.getElementById(id),labels={comprehension:'Comprehension',causal_explanation:'Causal explanation',decision_support:'Decision support',visual_explanation:'Visual explanation',clarity:'Clarity',packet_length:'Packet length'};el('operator-context').textContent=data.operator_context;el('rubric-scope').textContent=data.scope;el('assessment-guidance').innerHTML=guidance(data.assessment_guidance);el('scores').innerHTML=names.map(name=>'<label for="score-'+name+'">'+labels[name]+'</label><details><summary>Dimension guidance</summary>'+guidance(data.dimension_guidance[name])+'</details><select id="score-'+name+'"><option value="">Select a score</option>'+Object.keys(data.anchors[name]).map(Number).sort((a,b)=>a-b).map(score=>'<option value="'+score+'">'+score+'</option>').join('')+'</select><p class="anchor" id="anchor-'+name+'"></p><details><summary>Score anchors</summary>'+Object.keys(data.anchors[name]).map(Number).sort((a,b)=>a-b).map(score=>'<p class="anchor"><b>'+score+'</b> · '+escape(data.anchors[name][score])+'</p>').join('')+'</details>').join('');function escape(text){return String(text).replace(/[&<>"']/g,ch=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[ch]));}function guidance(lines){return '<ul>'+lines.map(line=>'<li>'+escape(line)+'</li>').join('')+'</ul>';}function finished(row){return names.every(name=>typeof row.dimensions[name]==='number')&&typeof row.critical_failure==='boolean'&&row.reason?.trim();}function complete(){return review.reviewer?.trim()&&review.blinded===true&&review.records.every(finished);}function persist(){const row=review.records[index];for(const name of names){const value=el('score-'+name).value;row.dimensions[name]=value===''?null:Number(value);el('anchor-'+name).textContent=value===''?'':data.anchors[name][value];}row.critical_failure=el('critical').value===''?null:el('critical').value==='true';row.reason=el('reason').value;review.reviewer=el('reviewer').value;review.blinded=el('blinded').checked;try{localStorage.setItem(key,JSON.stringify(review));}catch{}el('complete').textContent=review.records.filter(finished).length+' of '+review.records.length+' packets assessed.';if(complete())el('message').textContent='';}function show(){const row=review.records[index],item=data.items.find(item=>item.packet_id===row.packet_id);el('packet').src=item.page;el('open').href=item.page;document.querySelector('.panel').scrollTop=0;el('facts').innerHTML=item.facts_html;el('limits').innerHTML=item.limits_html;for(const name of names){el('score-'+name).value=row.dimensions[name]??'';el('anchor-'+name).textContent=row.dimensions[name]===null?'':data.anchors[name][row.dimensions[name]];}el('critical').value=row.critical_failure===null?'':String(row.critical_failure);el('reason').value=row.reason??'';el('reviewer').value=review.reviewer??'';el('blinded').checked=review.blinded===true;el('position').textContent='Packet '+(index+1)+' / '+review.records.length;el('previous').disabled=index===0;el('next').disabled=index===review.records.length-1;el('complete').textContent=review.records.filter(finished).length+' of '+review.records.length+' packets assessed.';}for(const element of document.querySelectorAll('select,input,textarea'))element.addEventListener('input',persist);el('previous').onclick=()=>{persist();index--;show();};el('next').onclick=()=>{persist();index++;show();};el('save').onclick=()=>{persist();if(!complete()){el('message').textContent='Complete each assessment, name the reviewer, and confirm the absence of Judge scores.';return;}el('message').textContent='';const url=URL.createObjectURL(new Blob([JSON.stringify(review,null,2)+'\\n'],{type:'application/json'})),a=document.createElement('a');a.href=url;a.download='human-reference-scores.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);};show();</script></html>'''
