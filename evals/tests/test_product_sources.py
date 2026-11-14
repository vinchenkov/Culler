import hashlib
from pathlib import Path
import subprocess

import pytest

from culler_evals.product_sources import (FIXED_FILES, ROOTS, ProductSourceError, create_product_snapshot,
                                         product_source_hash, product_source_manifest, verify_product_sources)
from culler_evals.runtime import ProductContracts, RuntimeErrorWithEvidence


def source_files(source: Path):
    for root in ROOTS:
        directory = source / root
        directory.mkdir(parents=True, exist_ok=True)
        (directory / "fixture.txt").write_text("The exact product source.\n")
    for name in FIXED_FILES:
        path = source / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("[tools]\ngo = '1.25.12'\nbun = '1.3.9'\n" if name == ".mise.toml" else "The exact bridge input.\n")
    for name in ("mc/verbs/brief.go", "mc/verbs/skills/packager/mc-packager/SKILL.md"):
        path = source / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("The fixed input.\n")


def test_product_snapshot_binds_module_host_helpers_toolchain_and_bridge(tmp_path):
    source = tmp_path / "checkout"
    source_files(source)
    manifest = product_source_manifest(source)
    snapshot = create_product_snapshot(source, tmp_path / "snapshot", manifest)
    assert verify_product_sources(snapshot, manifest) == product_source_hash(manifest)
    assert manifest["compile_recipe"]["environment"]["GOWORK"] == "off"
    assert manifest["compile_recipe"]["environment"]["GOTOOLCHAIN"] == "local"
    assert manifest["compile_recipe"]["environment"]["GOFLAGS"].startswith("-mod=readonly")
    for row in manifest["files"]:
        assert hashlib.sha256((snapshot / row["path"]).read_bytes()).hexdigest() == row["sha256"]
        assert not (snapshot / row["path"]).stat().st_mode & 0o222


@pytest.mark.parametrize("name", ["mc/verbs/brief.go", "mc/verbs/skills/packager/mc-packager/SKILL.md",
                                  ".mise.toml", "resident/fixture.txt", "runner/shared/fixture.txt",
                                  "evals/bridge/contracts.go"])
def test_product_drift_prevents_initial_and_existing_snapshot_use(tmp_path, name):
    source = tmp_path / "checkout"
    source_files(source)
    manifest = product_source_manifest(source)
    snapshot = create_product_snapshot(source, tmp_path / "existing", manifest)
    target = source / name
    target.write_text(target.read_text() + "A later source edit.\n")
    for destination in (tmp_path / "new", snapshot):
        with pytest.raises(ProductSourceError, match="changed after"):
            create_product_snapshot(source, destination, manifest)
    assert verify_product_sources(snapshot, manifest)


@pytest.mark.parametrize("change", ["add", "remove", "mode", "link"])
def test_product_source_inventory_drift_is_rejected(tmp_path, change):
    source = tmp_path / "checkout"
    source_files(source)
    manifest = product_source_manifest(source)
    target = source / "mc/verbs/brief.go"
    if change == "add":
        (source / "mc/new.go").write_text("A new compile input.")
    elif change == "remove":
        target.unlink()
    elif change == "mode":
        target.chmod(0o755)
    else:
        target.unlink()
        target.symlink_to(tmp_path / "private")
    with pytest.raises(ProductSourceError):
        verify_product_sources(source, manifest)


def test_cached_contract_executable_cannot_survive_a_skill_edit(tmp_path, monkeypatch):
    source = tmp_path / "checkout"
    source_files(source)
    calls = []

    def execute(argv, **settings):
        calls.append(argv)
        assert Path(settings["cwd"]) != source
        assert settings["env"]["GOWORK"] == "off"
        if argv[-2:] == ["env", "GOVERSION"]:
            return subprocess.CompletedProcess(argv, 0, "go1.25.12\n", "")
        if "build" in argv:
            module = Path(argv[argv.index("-C") + 1])
            assert (module / "verbs/brief.go").read_text() == "The fixed input.\n"
            Path(argv[argv.index("-o") + 1]).write_bytes(b"The contract executable fixture.")
        return subprocess.CompletedProcess(argv, 0, "", "")

    monkeypatch.setattr(subprocess, "run", execute)
    contracts = ProductContracts(source, tmp_path / "cache", source_manifest=product_source_manifest(source))
    original = contracts.build()
    assert original.exists()
    assert contracts.build() == original
    count = len(calls)
    (source / "mc/verbs/skills/packager/mc-packager/SKILL.md").write_text("A different input skill.")
    with pytest.raises(RuntimeErrorWithEvidence, match="changed after"):
        contracts.build()
    assert len(calls) == count


def test_product_snapshot_rejects_a_changed_host_credential_helper(tmp_path):
    source = tmp_path / "checkout"
    source_files(source)
    manifest = product_source_manifest(source)
    snapshot = create_product_snapshot(source, tmp_path / "snapshot", manifest)
    target = snapshot / "resident/fixture.txt"
    target.chmod(0o644)
    target.write_text("A changed credential helper.")
    with pytest.raises(ProductSourceError, match="changed after"):
        create_product_snapshot(source, snapshot, manifest)
