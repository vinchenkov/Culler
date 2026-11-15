"""Capture the backup refusal without model calls."""
import contextlib
import io
import os
from pathlib import Path
import shutil
import tempfile

from culler_evals import cli, storage
from culler_evals.records import RecordError, RecordStore

parent = Path.home() / ".local/share/culler-evals-tests"
parent.mkdir(parents=True, exist_ok=True)
for width in (80, 40):
    for color in (True, False):
        os.environ.update(COLUMNS=str(width), LINES="24", TERM="xterm-256color" if color else "dumb")
        if color:
            os.environ.pop("NO_COLOR", None)
            os.environ["FORCE_COLOR"]="1"
        else:
            os.environ.pop("FORCE_COLOR", None)
            os.environ["NO_COLOR"]="1"
        with tempfile.TemporaryDirectory(prefix="backup-refusal-", dir=parent) as temporary:
            base=Path(temporary)
            storage.DATA_HOME=base
            primary=base / "experiments" / "experiment"
            with RecordStore(primary) as store:
                backup=store.enable_backup()
                store.append("test_record", {}, stage="preparation")
            original={str(path.relative_to(backup)):path.read_bytes() for path in backup.rglob("*") if path.is_file()}
            shutil.rmtree(primary)
            output=io.StringIO()
            with RecordStore(primary) as store:
                try:
                    store.enable_backup()
                except RecordError as error:
                    cli.message(str(error),stream=output)
                else:
                    raise AssertionError("The existing backup requires a refusal.")
            current={str(path.relative_to(backup)):path.read_bytes() for path in backup.rglob("*") if path.is_file()}
            assert current==original
            text=output.getvalue()
            assert "A backup already exists" in text
            assert "select a new experiment directory." in " ".join(text.split())
            assert "\x1b[" not in text
            target=Path(__file__).parent / f"backup-refusal-{width}-{'color' if color else 'plain'}.txt"
            target.write_text(text)
            print(target.name)
