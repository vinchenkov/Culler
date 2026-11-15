import copy
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from culler_evals.cases import CaseError, canonical_json, case_readiness, content_hash, load_case, prepare_case, validate_cases


def git(repository, *args):
    return subprocess.check_output(["git", "-C", str(repository), *args], stderr=subprocess.DEVNULL)


def freeze(case):
    case["content_hashes"] = {
        name: content_hash(case[name])
        for name in ("role_inputs", "references", "project_skills", "environment", "visual_evidence")
    }
    return case


class CaseTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.upstream = self.root / "upstream"
        self.upstream.mkdir()
        git(self.upstream, "init", "-q")
        git(self.upstream, "config", "user.name", "Case fixture")
        git(self.upstream, "config", "user.email", "fixture@example.invalid")
        (self.upstream / "LICENSE").write_text("MIT fixture license\n")
        (self.upstream / "source.txt").write_text("before\n")
        (self.upstream / "lock.txt").write_text("fixed dependency\n")
        git(self.upstream, "add", ".")
        git(self.upstream, "commit", "-qm", "Base")
        base = git(self.upstream, "rev-parse", "HEAD").decode().strip()
        (self.upstream / "source.txt").write_text("after\n")
        git(self.upstream, "commit", "-qam", "Reviewed change")
        reviewed = git(self.upstream, "rev-parse", "HEAD").decode().strip()
        self.case_dir = self.root / "case"
        self.case_dir.mkdir()
        (self.case_dir / "evidence.txt").write_text("Captured fixture report.\n")
        review = canonical_json({"reviewer": "Fixture test author", "scope": "Reconstruction fixture", "cases": [{"id": "fixture", "base_commit": base,
                                 "reviewed_commit": reviewed, "reviewed_tree": git(self.upstream, "rev-parse", "HEAD^{tree}").decode().strip(),
                                 "result": "source_facts_accepted", "reference_facts_hash": content_hash([{"after": "after"}]),
                                 "reference_limits_hash": content_hash(["The fixture proves reconstruction only."])}]})
        (self.case_dir / "review.json").write_bytes(review)
        artifact = {
            "id": "evidence", "kind": "report", "visibility": "role",
            "path": "eval/1/evidence.txt", "source": {"kind": "file", "path": "evidence.txt"},
            "sha256": hashlib.sha256((self.case_dir / "evidence.txt").read_bytes()).hexdigest(),
        }
        self.case = freeze({
            "schema_version": 1, "id": "fixture", "group": "fixture-change", "types": ["internal_refactor"],
            "source": {
                "url": str(self.upstream), "base_commit": base, "reviewed_commit": reviewed,
                "reviewed_tree": git(self.upstream, "rev-parse", "HEAD^{tree}").decode().strip(),
                "change_origin": "upstream",
                "license": {"spdx": "MIT", "path": "LICENSE", "sha256": hashlib.sha256((self.upstream / "LICENSE").read_bytes()).hexdigest()},
            },
            "project_skills": [],
            "role_inputs": {"task": {"id": 1, "title": "Fixture", "acceptance_criteria": ["The text changes."]}, "brief": "Fixed brief. {{run_id}}", "command_responses": {"task get 1": {"id": 1}}},
            "references": {"review_status": "reviewed", "review_evidence": {"path": "review.json", "sha256": hashlib.sha256(review).hexdigest()}, "facts": [{"after": "after"}], "limits": ["The fixture proves reconstruction only."]},
            "artifacts": [artifact],
            "visual_evidence": {"required": False, "status": "not_applicable", "capture_instructions": []},
            "environment": {"trial": {"application_instance_required": False}, "capture": None, "dependency_locks": [{"path": "lock.txt", "sha256": hashlib.sha256((self.upstream / "lock.txt").read_bytes()).hexdigest()}]},
            "paths": {"/workspace/source": "repository", "/mc/artifacts/eval/1/evidence.txt": "evidence"},
            "expected_terminal": "packaged",
        })
        self.manifest = self.case_dir / "case.json"
        self.save()

    def save(self):
        self.manifest.write_bytes(canonical_json(self.case))

    def prepare(self, name="prepared"):
        return prepare_case(self.manifest, self.root / name, self.root / "cache", require_ready=True)

    def test_reconstruction_preserves_commits_and_isolates_trial_files(self):
        first, second = self.prepare("first"), self.prepare("second")
        first_repo, second_repo = Path(first["repository_path"]), Path(second["repository_path"])
        self.assertEqual(git(first_repo, "rev-parse", "HEAD").decode().strip(), self.case["source"]["reviewed_commit"])
        self.assertEqual(git(first_repo, "rev-parse", "refs/mc/destination").decode().strip(), self.case["source"]["base_commit"])
        self.assertEqual(git(first_repo, "show", self.case["source"]["base_commit"] + ":source.txt"), b"before\n")
        self.assertEqual(git(first_repo, "status", "--porcelain=v1"), b"")
        self.assertEqual(git(first_repo, "remote"), b"")
        self.assertEqual((first_repo / "LICENSE").read_text(), "MIT fixture license\n")
        (first_repo / "source.txt").write_text("changed by first trial\n")
        self.assertEqual((second_repo / "source.txt").read_text(), "after\n")
        self.assertEqual((self.upstream / "source.txt").read_text(), "after\n")
        self.assertEqual(Path(first["artifacts_path"]).joinpath("eval/1/evidence.txt").read_text(), "Captured fixture report.\n")
        self.assertFalse(Path(first["artifacts_path"]).joinpath("facts.json").exists())
        self.assertTrue(Path(first["references_path"]).joinpath("facts.json").is_file())

    def test_fixed_inputs_reject_a_changed_brief(self):
        self.case["role_inputs"]["brief"] = "Changed brief"
        self.save()
        with self.assertRaisesRegex(CaseError, "content hash"):
            self.prepare()

    def test_artifact_mismatch_leaves_no_prepared_case(self):
        (self.case_dir / "evidence.txt").write_text("Altered evidence")
        with self.assertRaisesRegex(CaseError, "artifact content hash"):
            self.prepare()
        self.assertFalse((self.root / "prepared").exists())

    def test_source_tree_mismatch_is_an_infrastructure_error(self):
        self.case["source"]["reviewed_tree"] = "0" * 40
        review = json.loads((self.case_dir / "review.json").read_text())
        review["cases"][0]["reviewed_tree"] = "0" * 40
        data = canonical_json(review)
        (self.case_dir / "review.json").write_bytes(data)
        self.case["references"]["review_evidence"]["sha256"] = hashlib.sha256(data).hexdigest()
        freeze(self.case)
        self.save()
        with self.assertRaisesRegex(CaseError, "source tree"):
            self.prepare()

    def test_dependency_mismatch_fails_before_role_execution(self):
        self.case["environment"]["dependency_locks"][0]["sha256"] = "0" * 64
        freeze(self.case)
        self.save()
        with self.assertRaisesRegex(CaseError, "dependency content hash"):
            self.prepare()

    def test_pending_review_and_missing_media_block_live_preparation(self):
        self.case["references"]["review_status"] = "pending"
        self.case["visual_evidence"] = {"required": True, "status": "missing", "capture_instructions": ["Capture the actual application."]}
        freeze(self.case)
        self.save()
        with self.assertRaisesRegex(CaseError, "not ready"):
            self.prepare()
        result = prepare_case(self.manifest, self.root / "development", self.root / "cache")
        self.assertFalse(result["readiness"]["ready"])
        self.assertEqual(len(result["readiness"]["problems"]), 2)

    def test_expected_infrastructure_case_restores_an_intentionally_absent_file(self):
        virtual = "/mc/artifacts/eval/1/missing-verdict.txt"
        self.case["expected_terminal"] = "infra"
        self.case["absent_paths"] = [virtual]
        self.case["paths"][virtual] = "absent"
        self.case["role_inputs"]["brief"] = "The required evidence file is " + virtual
        self.case["references"]["limits"] = ["The absent file requires an infrastructure report. No packet quality score applies."]
        review = json.loads((self.case_dir / "review.json").read_text())
        review["cases"][0]["reference_limits_hash"] = content_hash(self.case["references"]["limits"])
        data = canonical_json(review)
        (self.case_dir / "review.json").write_bytes(data)
        self.case["references"]["review_evidence"]["sha256"] = hashlib.sha256(data).hexdigest()
        freeze(self.case)
        self.save()
        prepared = self.prepare()
        self.assertEqual(prepared["expected_terminal"], "infra")
        self.assertFalse(Path(prepared["paths"][virtual]).exists())
        self.case["expected_terminal"] = "packaged"
        self.save()
        with self.assertRaisesRegex(CaseError, "infrastructure contract"):
            load_case(self.manifest)

    def test_a_mutable_review_record_cannot_enable_live_trials(self):
        (self.case_dir / "review.json").write_text("Altered review")
        with self.assertRaisesRegex(CaseError, "case review content hash"):
            self.prepare()

    def test_new_facts_cannot_inherit_a_previous_case_review(self):
        self.case["references"]["facts"][0]["after"] = "An unreviewed new claim"
        freeze(self.case)
        self.save()
        with self.assertRaisesRegex(CaseError, "independently reviewed facts"):
            self.prepare()

    def test_correction_history_and_requirement_versions_remain_fixed_inputs(self):
        rows = [{"outcome": "correct", "requirements_version": 1, "current_requirements": False,
                 "evidence_path": "/mc/artifacts/eval/1/evidence.txt", "correction_path": "/mc/artifacts/eval/1/evidence.txt"},
                {"outcome": "pass", "requirements_version": 2, "current_requirements": True,
                 "evidence_path": "/mc/artifacts/eval/1/evidence.txt", "correction_path": ""}]
        self.case["role_inputs"]["brief_parts"] = [{"heading": "Verdict history", "rows": rows}]
        self.case["role_inputs"]["task"]["requirements_version"] = 2
        freeze(self.case)
        self.save()
        first, second = self.prepare("first"), self.prepare("second")
        self.assertEqual(first["brief_parts"], self.case["role_inputs"]["brief_parts"])
        first["brief_parts"][0]["rows"][0]["outcome"] = "pass"
        self.assertEqual(second["brief_parts"][0]["rows"][0]["outcome"], "correct")

    def test_supplied_media_requires_a_real_recorded_artifact(self):
        self.case["visual_evidence"] = {"required": True, "status": "supplied", "capture_instructions": []}
        freeze(self.case)
        self.save()
        with self.assertRaisesRegex(CaseError, "actual media artifact"):
            load_case(self.manifest)

    def test_role_paths_exclude_judge_references(self):
        self.case["artifacts"][0]["visibility"] = "judge"
        self.save()
        with self.assertRaisesRegex(CaseError, "role-visible"):
            self.prepare()

    def test_paths_reject_traversal_and_source_symlinks(self):
        for path in ("../outside.txt", "/outside.txt", "a/../../outside.txt", "a\\outside.txt"):
            with self.subTest(path=path):
                changed = copy.deepcopy(self.case)
                changed["artifacts"][0]["path"] = path
                with self.assertRaises(CaseError):
                    validate_cases([changed])
        (self.case_dir / "evidence.txt").unlink()
        outside = self.root / "outside.txt"
        outside.write_text("Captured fixture report.\n")
        (self.case_dir / "evidence.txt").symlink_to(outside)
        with self.assertRaisesRegex(CaseError, "leaves its case directory"):
            self.prepare()

    def test_case_identifiers_are_unique_but_related_groups_can_repeat(self):
        other = copy.deepcopy(self.case)
        other["id"] = "fixture-variant"
        self.assertEqual(len(validate_cases([self.case, other])), 2)
        with self.assertRaisesRegex(CaseError, "occurs twice"):
            validate_cases([self.case, self.case])

    def test_git_artifacts_are_verified_and_judge_only(self):
        diff = git(self.upstream, "diff", "--no-ext-diff", "--no-textconv", "--binary", self.case["source"]["base_commit"], self.case["source"]["reviewed_commit"])
        self.case["artifacts"].append({"id": "diff", "kind": "source_diff", "visibility": "judge", "path": "diff.txt", "source": {"kind": "git_diff"}, "sha256": hashlib.sha256(diff).hexdigest()})
        self.save()
        result = self.prepare()
        self.assertEqual(Path(result["references_path"]).joinpath("diff.txt").read_bytes(), diff)
        self.assertFalse(Path(result["artifacts_path"]).joinpath("diff.txt").exists())

    def test_manifest_requires_full_commit_identifiers_and_safe_source(self):
        for value in ("main", self.case["source"]["base_commit"][:7]):
            changed = copy.deepcopy(self.case)
            changed["source"]["base_commit"] = value
            with self.assertRaisesRegex(CaseError, "full Git identifier"):
                validate_cases([changed])
        changed = copy.deepcopy(self.case)
        changed["source"]["url"] = "ext::untrusted command"
        with self.assertRaisesRegex(CaseError, "HTTPS"):
            validate_cases([changed])

    def test_memos_seed_retains_seven_types_and_ready_assignments(self):
        directory = Path(__file__).resolve().parents[1] / "cases" / "memos"
        cases = validate_cases(directory.glob("*.json"))
        self.assertEqual(len(cases), 7)
        self.assertEqual(len({case["group"] for case in cases}), 7)
        self.assertEqual(len({kind for case in cases for kind in case["types"]}), 7)
        for case in cases:
            self.assertEqual(case["project_skills"], [])
            expected = "valset" if case["id"] == "memos-account-attachment-usage" else "trainset"
            self.assertEqual(case["dataset"], expected)
            self.assertTrue(case_readiness(case)["ready"])
        self.assertEqual(len(validate_cases(cases, require_ready=True)), 7)

    def test_full_corpus_is_ready_covers_all_types_and_remains_disjoint(self):
        directory = Path(__file__).resolve().parents[1] / "cases"
        inventory = json.loads((directory / "corpus.json").read_text())
        assignments = inventory["assignments"]
        cases = validate_cases([directory / item["manifest"] for item in assignments], require_ready=True)
        self.assertEqual(len(cases), 35)
        self.assertEqual(len({case["group"] for case in cases}), 35)
        kinds = {kind for case in cases for kind in case["types"]}
        self.assertEqual(len(kinds), 7)
        for case, assignment in zip(cases, assignments, strict=True):
            for field in ("id", "group", "dataset", "types"):
                self.assertEqual(case[field], assignment[field])
        for dataset, total in (("trainset", 13), ("valset", 13), ("final", 9)):
            partition = [case for case in cases if case["dataset"] == dataset]
            self.assertEqual(len(partition), total)
            for kind in kinds:
                per_type = (1 if kind == "feature_across_components" else 2) if dataset == "trainset" else (
                    1 if kind == "performance_change" else 2) if dataset == "valset" else (
                    2 if kind in {"feature_across_components", "performance_change"} else 1)
                self.assertEqual(sum(kind in case["types"] for case in partition), per_type)
        media_cases = [case for case in cases if case["visual_evidence"]["required"]]
        self.assertEqual(len(media_cases), 6)
        self.assertEqual(sum(artifact["kind"] == "media" for case in media_cases for artifact in case["artifacts"]), 29)

    def test_protocol_matches_inventory_and_retains_the_pilot_groups(self):
        directory = Path(__file__).resolve().parents[1]
        inventory = json.loads((directory / "cases/corpus.json").read_text())
        protocol = json.loads((directory / "config/packager.json").read_text())
        actual = {}
        for dataset, paths in protocol["datasets"].items():
            for path in paths:
                case = load_case(directory / "config" / path)
                self.assertNotIn(case["id"], actual)
                self.assertEqual(case["dataset"], dataset)
                actual[case["id"]] = dataset
        self.assertEqual(actual, {item["id"]: item["dataset"] for item in inventory["assignments"]})
        pilot = json.loads((directory / "config/packager.pilot.json").read_text())
        for path in pilot["pilot"]["cases"]:
            case = load_case(directory / "config" / path)
            self.assertEqual(actual[case["id"]], "trainset")

    def test_current_assignments_preserve_historical_inputs_and_reviewed_amendments(self):
        directory = Path(__file__).resolve().parents[1] / "cases"
        proof_path = directory / "verification/corpus-assignments.json"
        proof = json.loads(proof_path.read_text())
        amendments = json.loads((directory / "verification/corpus-amendments.json").read_text())
        self.assertEqual(amendments["assignment_proof"]["path"], "verification/corpus-assignments.json")
        self.assertEqual(amendments["assignment_proof"]["sha256"], hashlib.sha256(proof_path.read_bytes()).hexdigest())
        review_path = directory / amendments["review_evidence"]["path"]
        self.assertEqual(amendments["review_evidence"]["sha256"], hashlib.sha256(review_path.read_bytes()).hexdigest())
        review = json.loads(review_path.read_text())
        reviewed = {item["id"]: item for item in review["cases"]}
        recorded = {item["case_id"]: item for item in amendments["records"]}
        self.assertEqual(len(recorded), len(amendments["records"]))
        self.assertEqual(len(recorded), 24)
        self.assertEqual(set(recorded), set(reviewed))
        self.assertIn(amendments["review"], reviewed["memos-scheduler-plugin"]["amendment_reviews"])
        self.assertEqual(amendments["review"]["result"], "accepted")
        historical = directory / "verification/corpus-reconstruction.json"
        self.assertEqual(proof["historical_reconstruction_sha256"], hashlib.sha256(historical.read_bytes()).hexdigest())
        original = {item["case_id"]: item for item in json.loads(historical.read_text())["records"]}
        self.assertEqual(len(proof["records"]), 35)
        self.assertEqual(sum(item["previous_dataset"] != item["dataset"] for item in proof["records"]), 6)
        changed = set()
        for item in proof["records"]:
            with self.subTest(case=item["case_id"]):
                manifest = directory / item["manifest"]
                current = manifest.read_bytes()
                case = json.loads(current)
                current_hash = hashlib.sha256(current).hexdigest()
                prior = original[item["case_id"]]
                self.assertEqual(case["id"], item["case_id"])
                self.assertEqual(case["dataset"], item["dataset"])
                self.assertEqual(item["previous_dataset"], prior["dataset"])
                self.assertEqual(item["previous_manifest_sha256"], prior["manifest_sha256"])
                for field in ("base_commit", "reviewed_commit", "reviewed_tree"):
                    self.assertEqual(case["source"][field], prior[field])
                if current_hash != item["manifest_sha256"]:
                    changed.add(item["case_id"])
                    self.assertIn(item["case_id"], recorded)
                    amendment = recorded[item["case_id"]]
                    self.assertEqual(amendment["manifest"], item["manifest"])
                    self.assertEqual(amendment["previous_manifest_sha256"], item["manifest_sha256"])
                    self.assertEqual(amendment["manifest_sha256"], current_hash)
                    self.assertEqual(amendment["source_sha256"], content_hash(case["source"]))
                    evidence = case["references"]["review_evidence"]
                    self.assertEqual((manifest.parent / evidence["path"]).resolve(), review_path.resolve())
                    self.assertEqual(evidence["sha256"], amendments["review_evidence"]["sha256"])
                else:
                    previous = current.replace(f'"dataset": "{item["dataset"]}"'.encode(),
                                               f'"dataset": "{item["previous_dataset"]}"'.encode())
                    self.assertEqual(hashlib.sha256(previous).hexdigest(), prior["manifest_sha256"])
        self.assertEqual(changed, set(recorded))
        self.assertEqual(proof["inventory_sha256"], hashlib.sha256((directory / "corpus.json").read_bytes()).hexdigest())


if __name__ == "__main__":
    unittest.main()
