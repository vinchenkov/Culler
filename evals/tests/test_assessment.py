import json
import pytest
from pathlib import Path
import re

from culler_evals.assessment import (AssessmentError, JUDGE_INSTRUCTIONS, SEMANTIC_CHECKS, SEMANTIC_CONTRACTS, check_packet, trial_score,
                                    validate_judgment, require_inspection, SECTIONS, REQUIRED_SECTIONS, FIRST_SECTIONS,
                                    case_sections, first_section, required_sections)
from culler_evals.protocol import DIMENSIONS


def judgment():
    return {"dimensions": {name: {"score": 3, "reason": "The visible explanation preserves the fact.",
                                   "evidence": ["packet:summary", "reference:fact-1"]} for name in DIMENSIONS},
            "critical_failures": [], "semantic_checks": [{"id": name, "passed": True,
                                                            "reason": "The claims match the evidence.", "evidence": ["reference:fact-1"]} for name in SEMANTIC_CHECKS],
            "uninspected_content": [], "feedback": ["Explain the recovery condition."], "artifacts": ["screenshot:1"]}


def rubric():
    return {"dimensions": {name: {"weight": 1} for name in DIMENSIONS}}


def current_rubric():
    return json.loads((Path(__file__).resolve().parents[1] / "config/rubric.experiment.json").read_text())


def test_visual_five_preserves_equal_dimension_weights_and_total_range():
    result = judgment()
    for name in DIMENSIONS:
        result["dimensions"][name]["score"] = 4
    score = trial_score(result, current_rubric(), {"passed": True})
    assert score["quality"] == pytest.approx(100 * (5 + 4 / 5) / 6)
    result["dimensions"]["visual_explanation"]["score"] = 5
    assert trial_score(result, current_rubric(), {"passed": True})["contribution"] == 100
    result["critical_failures"] = [{"category": "false_claim", "reason": "The diagram changes the access boundary.",
                                    "evidence": ["packet:diagram", "reference:boundary"]}]
    score = trial_score(result, current_rubric(), {"passed": True})
    assert score["quality"] == 100 and score["contribution"] == 0


@pytest.mark.parametrize("name", DIMENSIONS)
def test_only_visual_explanation_accepts_five(name):
    result = judgment()
    result["dimensions"][name]["score"] = 5
    if name == "visual_explanation":
        validate_judgment(result, current_rubric())
        with pytest.raises(AssessmentError):
            validate_judgment(result, rubric())
        result["dimensions"][name]["score"] = 6
    with pytest.raises(AssessmentError):
        validate_judgment(result, current_rubric())


def test_critical_failure_keeps_raw_score_and_zeroes_contribution():
    result = judgment()
    result["critical_failures"] = [{"category": "false_claim", "reason": "The packet invents a successful check.",
                                    "evidence": ["reference:unverified"]}]
    score = trial_score(result, rubric(), {"passed": True})
    assert score["quality"] == 75
    assert score["contribution"] == 0


def test_uninspected_content_cannot_receive_complete_score():
    result = judgment()
    result["uninspected_content"] = ["Risk and rollback"]
    with pytest.raises(AssessmentError, match="inspect"):
        trial_score(result, rubric(), {"passed": True})


def test_render_failure_zeroes_packet_with_independent_browser_evidence():
    result = judgment()
    result["uninspected_content"] = ["Risk and rollback"]
    result["_runtime"] = {"render_failures": [{"kind": "failed_images", "section": "Risk and rollback"}]}
    assert trial_score(result, rubric(), {"passed": True})["contribution"] == 0


def test_one_render_failure_does_not_excuse_other_uninspected_sections():
    result = judgment()
    result["_runtime"] = {"render_failures": [{"kind": "failed_images", "section": "Technical"}],
                          "browser": {"visible_headings": ["Summary", "Technical"]}}
    with pytest.raises(AssessmentError, match="Glossary, Risk and rollback"):
        require_inspection(result, {"sections": ["Summary", "Technical", "Risk and rollback", "Glossary"]})
    result["uninspected_content"] = ["Technical", "Glossary"]
    with pytest.raises(AssessmentError, match="inspect"):
        validate_judgment(result)


def test_confirmed_render_failure_excuses_only_its_own_section():
    result = judgment()
    result["uninspected_content"] = ["Technical"]
    result["_runtime"] = {"render_failures": [{"kind": "failed_images", "section": "Technical"}],
                          "browser": {"visible_headings": ["Summary", "Risk and rollback", "Glossary"]}}
    require_inspection(result, {"sections": ["Summary", "Technical", "Risk and rollback", "Glossary"]})
    assert trial_score(result, rubric(), {"passed": True})["contribution"] == 0


@pytest.mark.parametrize("bad", [float("nan"), float("inf"), True, 5, -1, "3"])
def test_invalid_judge_scores_fail_as_infrastructure(bad):
    result = judgment()
    result["dimensions"]["clarity"]["score"] = bad
    with pytest.raises(AssessmentError):
        validate_judgment(result)


def test_packet_checks_external_css_and_section_order(tmp_path):
    packet = tmp_path / "packet.html"
    packet.write_text('''<style>p{background:url(https://example.org/image.png)}</style>
    <a href="#summary">Summary</a><h2 id="summary">Summary</h2>
    <h2>Glossary</h2><h2>Risk and rollback</h2>''')
    result = check_packet(packet)
    assert not result["passed"]
    assert any("resource" in failure for failure in result["failures"])
    assert any("order" in failure for failure in result["failures"])


def test_self_contained_packet_with_internal_links(tmp_path):
    packet = tmp_path / "packet.html"
    packet.write_text('''<a href="#summary">Summary</a><h2 id="summary">Summary</h2>
    <h2>Risk and rollback</h2><h2>Glossary</h2>''')
    assert check_packet(packet)["passed"]


def test_packet_sections_match_the_current_product_directive():
    source = Path(__file__).resolve().parents[2]
    directive = (source / "mc/verbs/directives/packager.md").read_text()
    items = re.findall(r"^\d+\. (.+?) —", directive, re.M)
    sections = [name for item in items for name in re.findall(r"\*\*([^*]+)\*\*", item)]
    assert tuple(sections) == SECTIONS
    assert items[0] == "**Strategic rationale** or **Summary**"
    assert FIRST_SECTIONS == ("Strategic rationale", "Summary")
    assert REQUIRED_SECTIONS == ("Summary", "Risk and rollback")


STRATEGIST_CASE = {"role_inputs": {"brief_parts": [
    {"heading": "Task"}, {"heading": "Strategic rationale", "text": "Observed: the export fails."}]}}
OPERATOR_CASE = {"role_inputs": {"brief_parts": [{"heading": "Task"}]}}


def packet_with(tmp_path, *headings):
    packet = tmp_path / "packet.html"
    packet.write_text('<a href="#first">Contents</a>' + "".join(
        f'<h2 id="{"first" if index == 0 else "s%d" % index}">{heading}</h2><p>Text.</p>'
        for index, heading in enumerate(headings)))
    return packet


def test_the_task_origin_selects_the_first_section_and_the_section_order():
    assert first_section(STRATEGIST_CASE) == "Strategic rationale"
    assert first_section(OPERATOR_CASE) == "Summary"
    assert first_section(STRATEGIST_CASE["role_inputs"]) == "Strategic rationale"
    assert required_sections(STRATEGIST_CASE) == ("Strategic rationale", "Risk and rollback")
    assert required_sections(OPERATOR_CASE) == REQUIRED_SECTIONS
    assert required_sections(OPERATOR_CASE | {"required_sections": ["Risk and rollback"]}) == ("Risk and rollback",)
    assert case_sections(STRATEGIST_CASE) == ("Strategic rationale",) + SECTIONS[2:]
    assert case_sections(OPERATOR_CASE) == SECTIONS[1:]


def test_strategist_task_packet_starts_with_strategic_rationale(tmp_path):
    required = required_sections(STRATEGIST_CASE)
    passed = check_packet(packet_with(tmp_path, "Strategic rationale", "Background", "Risk and rollback"),
                          required_sections=required)
    assert passed["passed"]
    assert passed["sections"][0] == "Strategic rationale"
    summary = check_packet(packet_with(tmp_path, "Summary", "Risk and rollback"), required_sections=required)
    assert "The required section is absent: Strategic rationale." in summary["failures"]
    both = check_packet(packet_with(tmp_path, "Strategic rationale", "Summary", "Risk and rollback"),
                        required_sections=required)
    assert "The packet holds two first sections." in both["failures"]
    late = check_packet(packet_with(tmp_path, "Background", "Strategic rationale", "Risk and rollback"),
                        required_sections=required)
    assert "The packet section order is invalid." in late["failures"]


def test_operator_task_packet_starts_with_summary(tmp_path):
    required = required_sections(OPERATOR_CASE)
    passed = check_packet(packet_with(tmp_path, "Summary", "Background", "Risk and rollback"), required_sections=required)
    assert passed["passed"]
    assert passed["sections"][0] == "Summary"
    rationale = check_packet(packet_with(tmp_path, "Strategic rationale", "Risk and rollback"), required_sections=required)
    assert "The required section is absent: Summary." in rationale["failures"]
    both = check_packet(packet_with(tmp_path, "Strategic rationale", "Summary", "Risk and rollback"),
                        required_sections=required)
    assert "The packet holds two first sections." in both["failures"]


def test_judge_contracts_match_the_current_reader_and_term_authority():
    source = Path(__file__).resolve().parents[2]
    directive = " ".join((source / "mc/verbs/directives/packager.md").read_text().split())
    reader = directive.split("## Reader ", 1)[1].split(" ## Files changed", 1)[0]
    assert "The operator uses the Project and knows its core terms." in reader
    assert "The operator uses the Project and knows its core terms." in SEMANTIC_CONTRACTS["reader_context"]
    assert "The code, documents, the wiki, and the user interface text" in directive
    for authority in ("code", "documents", "wiki", "interface text"):
        assert authority in SEMANTIC_CONTRACTS["glossary"]
    assert "before the reviewed change" in SEMANTIC_CONTRACTS["glossary"]
    assert "The prose can define a new term" in directive
    assert "can define a central new term" in SEMANTIC_CONTRACTS["glossary"]
    assert "Each use of the term in the prose links to the entry." in directive
    assert "every prose use links to it" in SEMANTIC_CONTRACTS["glossary"]

    retired = ("pipeline roles", "lacks context", "omits Culler", "never appear",
               "without a Verification history section or artifact citations",
               "where it first appears", "in the Technical section")
    contracts = " ".join(SEMANTIC_CONTRACTS.values())
    for phrase in retired:
        assert phrase not in contracts
    assert "Historical reports establish authority" in JUDGE_INSTRUCTIONS

    contexts = [json.loads((source / "evals/config" / name).read_text())["operator_context"]
                for name in ("packager.json", "packager.pilot.json")]
    for context in contexts:
        assert "understands general software concepts" in context
        assert "uses the Project, and knows its core terms" in context
        assert "pipeline roles" not in context
        assert "lacks context" not in context


def test_packet_without_applicable_glossary_can_pass_structural_checks(tmp_path):
    packet = tmp_path / "packet.html"
    packet.write_text('<a href="#summary">Summary</a><h2 id="summary">Summary</h2>'
                      '<h2>Risk and rollback</h2><p>The records establish no additional risk.</p>')
    assert check_packet(packet)["passed"]


def test_judge_rejects_the_obsolete_history_contract():
    result = judgment()
    next(check for check in result["semantic_checks"] if check["id"] == "criterion_evidence")["id"] = "verification_history"
    with pytest.raises(AssessmentError, match="each required semantic packet contract"):
        validate_judgment(result)
