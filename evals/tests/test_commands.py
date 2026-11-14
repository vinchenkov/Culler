import json
from pathlib import Path
import subprocess

import pytest

from culler_evals.commands import CommandAdapter
from culler_evals.runtime import ProductContracts


SOURCE = Path(__file__).resolve().parents[2]


@pytest.fixture
def command_case(tmp_path):
    repository = tmp_path / "repository"
    repository.mkdir()
    def git(*args):
        return subprocess.run(["git", "-C", str(repository), *args], check=True, capture_output=True, text=True).stdout.strip()
    git("init", "--quiet")
    git("config", "user.name", "Evaluation fixture")
    git("config", "user.email", "fixture@example.invalid")
    (repository / "example.txt").write_text("before\n")
    git("add", ".")
    git("commit", "--quiet", "-m", "Before")
    base = git("rev-parse", "HEAD")
    (repository / "example.txt").write_text("after\n")
    git("commit", "--quiet", "-am", "After")
    reviewed = git("rev-parse", "HEAD")
    artifacts = tmp_path / "artifacts"
    packet = artifacts / "eval/701/packet/packet.html"
    packet.parent.mkdir(parents=True)
    packet.write_text("<!doctype html><h1>Summary</h1>")
    case = {"task_id": "701", "base_commit": base, "reviewed_commit": reviewed,
            "command_responses": {"task get 701": {"id": 701, "title": "Fixture"}, "packet list": []}}
    adapter = CommandAdapter(case, repository, artifacts, "eval-run", tmp_path / "state")
    return adapter, packet, repository, git


def submit(adapter, **changes):
    values = {"--run": "eval-run", "--status": "packaged", "--outputs": "/mc/artifacts/eval/701/packet/packet.html", "--thesis": "The fixture changes."}
    values.update(changes)
    args = ["complete", "701"]
    for key, value in values.items():
        args.extend([key, value])
    return adapter.invoke(args)


def test_refusal_repairs_then_one_terminal(command_case):
    adapter, packet, *_ = command_case
    packet.write_text("")
    assert submit(adapter)["exit_code"] == 1
    assert adapter.terminal is None
    packet.write_text("<h1>Summary</h1>")
    assert submit(adapter)["exit_code"] == 0
    assert adapter.terminal["kind"] == "packaged"
    assert submit(adapter)["exit_code"] == 1
    records = [json.loads(line) for line in adapter.log_path.read_text().splitlines()]
    assert [record["exit_code"] for record in records] == [1, 0, 1]


def test_terminal_retains_the_accepted_packet(command_case):
    adapter, packet, *_ = command_case
    accepted = packet.read_bytes()
    assert submit(adapter)["exit_code"] == 0
    packet.write_text("The role changed its submitted file afterward.")
    assert Path(adapter.terminal["packet_path"]).read_bytes() == accepted


@pytest.mark.parametrize("change", [
    {"--run": "other-run"}, {"--thesis": ""}, {"--thesis": "x" * 4097},
    {"--thesis": "A\x1bB"}, {"--outputs": "/tmp/packet.html"},
    {"--outputs": "/mc/artifacts/fabricated/701/packet/packet.html"},
])
def test_identity_summary_and_plane_refusals(command_case, change):
    adapter, *_ = command_case
    assert submit(adapter, **change)["exit_code"] != 0
    assert adapter.terminal is None


def test_repository_dirty_refusal_can_repair(command_case):
    adapter, _, repository, _ = command_case
    (repository / "untracked.txt").write_text("extra")
    assert submit(adapter)["exit_code"] == 1
    (repository / "untracked.txt").unlink()
    assert submit(adapter)["exit_code"] == 0


def test_repository_changed_head_refused(command_case):
    adapter, _, _, git = command_case
    git("commit", "--quiet", "--allow-empty", "-m", "Unexpected")
    assert submit(adapter)["exit_code"] == 1


@pytest.mark.parametrize("detached", [False, True])
def test_repository_branch_change_at_same_commit_requires_restore(command_case, detached):
    adapter, _, repository, git = command_case
    if detached:
        git("checkout", "--quiet", "--detach", "HEAD")
        adapter = CommandAdapter(adapter.case, repository, adapter.artifacts, adapter.run_id, adapter.root / "detached")
    original = git("rev-parse", "--symbolic-full-name", "HEAD")
    commit = git("rev-parse", "HEAD")
    git("switch", "--quiet", "-c", "unexpected-branch")
    assert git("rev-parse", "HEAD") == commit
    assert submit(adapter)["exit_code"] == 1
    assert adapter.terminal is None
    if detached:
        git("checkout", "--quiet", "--detach", commit)
    else:
        git("switch", "--quiet", original.removeprefix("refs/heads/"))
    assert submit(adapter)["exit_code"] == 0


def test_reads_use_fixed_responses(command_case):
    adapter, *_ = command_case
    assert adapter.invoke(["task", "get", "701"])["stdout"]["id"] == 701
    assert adapter.invoke(["packet", "list"])["stdout"] == []
    assert adapter.invoke(["task", "get", "702"])["exit_code"] == 1
    assert adapter.invoke(["dispatch"])["exit_code"] == 2


def test_symlink_packet_refused(command_case):
    adapter, packet, *_ = command_case
    packet.unlink()
    outside = packet.parents[4] / "outside.html"
    outside.write_text("<h1>Outside</h1>")
    packet.symlink_to(outside)
    assert submit(adapter)["exit_code"] == 1


def test_infrastructure_result_has_no_packet(command_case):
    adapter, *_ = command_case
    response = adapter.invoke(["complete", "701", "--run", "eval-run", "--infra", "--reason", "The evidence file is absent."])
    assert response["exit_code"] == 0
    assert adapter.terminal["kind"] == "infra"
    assert "packet_path" not in adapter.terminal


@pytest.fixture(scope="module")
def product_contracts(tmp_path_factory):
    return ProductContracts(SOURCE, tmp_path_factory.mktemp("product-contracts"))


@pytest.mark.parametrize("thesis", ["The fixture changes.", "", "x" * 4097, "A\x1bB", "A\nB", "é" * 2049])
def test_thesis_acceptance_matches_product(command_case, product_contracts, thesis):
    adapter, *_ = command_case
    actual = product_contracts.call("thesis", thesis=thesis)
    assert (submit(adapter, **{"--thesis": thesis})["exit_code"] == 0) == actual["accepted"]


def test_artifact_and_repository_acceptance_match_product(command_case, product_contracts):
    adapter, packet, repository, _ = command_case
    actual = product_contracts.call("artifact", path=str(packet), planes=[str(adapter.artifacts / "eval")], artifacts_root=str(adapter.artifacts))
    assert actual["accepted"]
    assert product_contracts.call("repository", repository=str(repository), start_commit=adapter.start_commit)["accepted"]
    packet.write_text("")
    assert not product_contracts.call("artifact", path=str(packet), planes=[str(adapter.artifacts / "eval")], artifacts_root=str(adapter.artifacts))["accepted"]
    assert submit(adapter)["exit_code"] != 0
    (repository / "unexpected.txt").write_text("extra")
    assert not product_contracts.call("repository", repository=str(repository), start_commit=adapter.start_commit)["accepted"]


def test_packager_terminal_matches_the_disposable_product_spine(product_contracts):
    result = product_contracts.check_spine()
    assert result["accepted"], result["output"]
