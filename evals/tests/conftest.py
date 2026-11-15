"""Keep model-free experiment fixtures outside temporary filesystem roots."""

from pathlib import Path
import tempfile

import pytest


@pytest.fixture
def tmp_path(monkeypatch):
    root = Path.home() / ".local/share/culler-evals-tests"
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    with tempfile.TemporaryDirectory(prefix="case-", dir=root) as directory:
        from culler_evals import storage
        monkeypatch.setattr(storage, "DATA_HOME", Path(directory) / "data")
        yield Path(directory)
