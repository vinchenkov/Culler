"""Partition the pinned scheduler change into actual fixture commits.

The final tree stays equal to the upstream tree. The fixture records no Culler run.
"""

from __future__ import annotations

import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT.parent))
from culler_evals.cases import content_hash, load_case

UPSTREAM = "5828f34aae3bd91cc60c420ee4ce9760f7239ff8"
NAME = "memos-scheduler-plugin"


def main():
    parser = argparse.ArgumentParser(
        description="Create actual scheduler fixture commits. Preserve the pinned upstream source tree.",
        formatter_class=lambda prog: argparse.HelpFormatter(prog, width=max(40, shutil.get_terminal_size().columns), max_help_position=18),
    )
    parser.add_argument("--cache", type=Path, required=True, metavar="EXTERNAL_MIRROR", help="Path to the external bare Memos repository.")
    args = parser.parse_args()
    if not args.cache.is_dir():
        parser.error("The external mirror is absent. Set --cache to an existing bare Memos repository.")
    manifest = ROOT / "memos/corpus" / f"{NAME}.json"
    case = json.loads(manifest.read_text())
    if case["source"]["change_origin"] == "authored":
        load_case(manifest)
        bundle = manifest.parent / case["source"]["bundle"]["path"]
        if hashlib.sha256(bundle.read_bytes()).hexdigest() != case["source"]["bundle"]["sha256"]:
            parser.error("The fixed Git bundle differs. Restore the bundle before you use this fixture.")
        print("Scheduler fixture preserved.")
        print("The manifest, local artifacts, and bundle content hashes match.")
        print("This check executes no application test.")
        return
    parent_dir = Path(tempfile.mkdtemp(prefix="culler-scheduler-fixture-", dir="/private/tmp"))
    repository = parent_dir / "source.git"
    stamp = datetime.datetime.now(datetime.timezone.utc).isoformat()
    env = {key:value for key,value in os.environ.items() if not key.startswith("GIT_")}
    env.update(GIT_AUTHOR_NAME="Culler evaluation fixture", GIT_AUTHOR_EMAIL="evaluation@example.invalid", GIT_COMMITTER_NAME="Culler evaluation fixture", GIT_COMMITTER_EMAIL="evaluation@example.invalid", GIT_AUTHOR_DATE=stamp, GIT_COMMITTER_DATE=stamp)

    def git(*argv, capture=True):
        return subprocess.check_output(["git", f"--git-dir={repository}", *argv], env=env)

    def write(path, value):
        data = (json.dumps(value, sort_keys=True, ensure_ascii=False, indent=2) + "\n").encode()
        path.write_bytes(data)
        return hashlib.sha256(data).hexdigest()

    try:
        subprocess.run(["git", "clone", "--bare", "--no-hardlinks", str(args.cache), str(repository)], env=env, capture_output=True, check=True)
        base = git("rev-parse", f"{UPSTREAM}^").decode().strip()
        upstream_tree = git("rev-parse", f"{UPSTREAM}^{{tree}}").decode().strip()
        git("read-tree", base)
        partitions = [
            ("Parse cron schedules", ["parser.go", "parser_test.go"]),
            ("Validate jobs and compose middleware", ["job.go", "job_test.go", "middleware.go", "middleware_test.go"]),
            ("Execute and stop scheduled jobs", ["scheduler.go", "scheduler_test.go", "integration_test.go", "example_test.go"]),
            ("Document the scheduler package", ["README.md", "doc.go"]),
        ]
        commits = []
        previous = base
        for title, files in partitions:
            paths = ["plugin/scheduler/"+name for name in files]
            entries = git("ls-tree", UPSTREAM, "--", *paths)
            subprocess.run(["git", f"--git-dir={repository}", "update-index", "--index-info"], input=entries, env=env, capture_output=True, check=True)
            tree = git("write-tree").decode().strip()
            commit = git("commit-tree", tree, "-p", previous, "-m", "Evaluation fixture: "+title).decode().strip()
            commits.append(dict(title=title, paths=paths, input_commit=previous, result_commit=commit, result_tree=tree))
            previous = commit
        assert commits[-1]["result_tree"] == upstream_tree
        git("update-ref", "refs/heads/culler-eval-scheduler-fixture", previous)
        folder = manifest.parent / NAME
        bundle = folder / "source.bundle"
        git("bundle", "create", str(bundle), "refs/heads/culler-eval-scheduler-fixture", "^"+base)
        git("bundle", "verify", str(bundle))
        proof = dict(schema_version=1, provenance="authored_for_experiment", author="/root/case_corpus", created_at=stamp, scope="Actual fixture Git history derived from one pinned upstream change. No production Culler role executed these tasks.", upstream_commit=UPSTREAM, upstream_tree=upstream_tree, base_commit=base, partitions=commits, final_tree_matches_upstream=True, model_calls=0, application_tests_executed=False)
        proof_hash = write(folder / "authored-history.json", proof)
        case["source"].update(base_commit=base, reviewed_commit=previous, reviewed_tree=upstream_tree, change_origin="authored", bundle=dict(kind="file", path=NAME+"/source.bundle", sha256=hashlib.sha256(bundle.read_bytes()).hexdigest()), upstream_commit=UPSTREAM, upstream_commit_url=f"https://github.com/usememos/memos/commit/{UPSTREAM}")
        case["source"].pop("commit_url", None)
        task_id = case["role_inputs"]["task"]["id"]
        task = case["role_inputs"]["task"]
        task.update(scope="task", title="Provide an in-process scheduler package")
        role = case["role_inputs"]
        role["command_responses"] = {f"task get {task_id}":task | dict(status="packaging", project_id="memos-evaluation"), "task list":[task | dict(status="packaging", project_id="memos-evaluation")], "packet list":[], "project list":[dict(id="memos-evaluation", name="Memos", repo_path="/workspace/source")]}
        role["brief_parts"] = [part for part in role["brief_parts"] if part["heading"] != "Scribe report"]
        subject = next(part for part in role["brief_parts"] if part["heading"] == "Task")
        subject["explanation"] = "This is the subject of your review packet. The packet covers each criterion in the description."
        state = next(part for part in role["brief_parts"] if part["heading"] == "Git state")
        updates = {"Start commit":previous, "Base commit":base, "Submitted commit":previous, "Verified source commit":commits[2]["result_commit"], "Scribe input commit":commits[2]["result_commit"], "Scribe result commit":previous}
        for item in state["facts"]:
            if item["label"] in updates:
                item["value"] = updates[item["label"]]
        paths = case["paths"]
        artifacts = case["artifacts"]
        for artifact in artifacts:
            if artifact["id"] == "reviewed-diff":
                artifact["sha256"] = hashlib.sha256(git("diff", "--binary", "--no-ext-diff", "--no-textconv", base, previous)).hexdigest()
            elif artifact["kind"] == "report":
                path = manifest.parent / artifact["source"]["path"]
                report = json.loads(path.read_text())
                if artifact["id"] == "worker-report":
                    report["role"] = "Worker"
                    report["scope"] = "The case author composed this task report from actual source commits. No Culler Worker executed this task."
                    report["verified_source_commit"] = commits[2]["result_commit"]
                    report["checks"][0].update(base_commit=base, reviewed_commit=previous, reviewed_tree=upstream_tree)
                elif artifact["id"] == "verifier-evidence":
                    report["execution_evidence"]["source_observation"].update(base_commit=base, reviewed_commit=previous, reviewed_tree=upstream_tree)
                    report["verified_source_commit"] = commits[2]["result_commit"]
                    report["scope"] = "The case author inspected the implementation commits and final tree. This is an authored task verdict. No Culler run produced this verdict."
                elif artifact["id"] == "scribe-report":
                    report.update(source_commit=commits[2]["result_commit"], result_commit=previous, changed_documentation=commits[3]["paths"], scope="The case author created an actual documentation-only fixture commit. No Culler Scribe executed this task.")
                artifact["sha256"] = write(path, report)
        artifacts.append(dict(id="authored-history", kind="source_record", visibility="judge", path="authored-history.json", source=dict(kind="file", path=NAME+"/authored-history.json"), sha256=proof_hash, provenance="captured_git_commands"))
        role["upstream_reports"] = [dict(artifact=a["id"], provenance=a["provenance"]) for a in artifacts if a["kind"] == "report"]
        references = case["references"]
        references["facts"].append(dict(id="fixture-history", before="The upstream change exists as one implementation commit.", after="The authored fixture partitions that change into three actual source commits and one documentation commit.", mechanism="The final fixture tree equals the pinned upstream tree. The Git bundle supplies every new fixture commit.", conditions=["The source test files and documentation come from upstream; no fixture report claims executed tests.", "The Scribe range changes only the package README and doc.go.", "The upstream commit defines the source behavior. The fixture records no production Culler execution."], sources=[dict(revision=previous, path="plugin/scheduler/README.md", symbol="The complete package README", evidence_artifact="authored-history")]))
        references["limits"].append("The fixture supplies actual source commits. It records no production Culler execution.")
        for key in ["role_inputs", "references", "project_skills", "environment", "visual_evidence"]:
            case["content_hashes"][key] = content_hash(case[key])
        write(manifest, case)
        load_case(manifest)
        print("Scheduler fixture created: three child commits and one documentation commit.")
        print("The final source tree matches the pinned upstream tree.")
        print("The fixture needs independent source review before live trials.")
    finally:
        shutil.rmtree(parent_dir)


if __name__ == "__main__":
    main()
