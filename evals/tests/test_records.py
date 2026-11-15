import builtins
import fcntl
import json
import threading
from concurrent.futures import ThreadPoolExecutor

import pytest

from culler_evals.records import RecordError, RecordStore


def test_concurrent_operations_have_durable_order_and_artifacts(tmp_path):
    with RecordStore(tmp_path, "test") as store:
        def run(index):
            operation = store.begin_operation("trial", trial_id=f"trial-{index}", dependencies=[])
            blob = store.put_blob({"output": index})
            store.end_operation(operation, "success", result={"artifact": blob})
        with ThreadPoolExecutor(max_workers=4) as executor:
            list(executor.map(run, range(12)))
        assert store.completeness()["complete"]
        assert [event["sequence"] for event in store.events()] == list(range(1, 25))
    with RecordStore(tmp_path, "test") as continued:
        assert len(continued.events()) == 24
        assert continued.completeness()["complete"]


def test_incomplete_attempt_stays_explicit_after_continuation(tmp_path):
    with RecordStore(tmp_path) as store:
        operation = store.begin_operation("judge", trial_id="t1")
        assert store.completeness()["unfinished_operations"] == [operation]
        assert store.interrupt_unfinished("The process stopped.") == [operation]
        assert store.completeness()["complete"]
        outcome = store.events()[-1]["data"]
        assert outcome["outcome"] == "interrupted"
        assert outcome["absence_reasons"]["completion"] == "The process stopped."


def test_missing_or_corrupt_artifact_blocks_completeness(tmp_path):
    with RecordStore(tmp_path) as store:
        artifact = store.put_blob("an output")
        store.append("result", {"artifact": artifact})
        (tmp_path / artifact["path"]).write_text("other output")
        assert not store.completeness()["complete"]
        assert store.completeness()["artifact_errors"]


def test_journal_rejects_truncated_event_and_changed_history(tmp_path):
    with RecordStore(tmp_path) as store:
        store.append("test", {"value": 1})
    journal = tmp_path / "events.jsonl"
    original = journal.read_bytes()
    journal.write_bytes(original[:-1])
    with pytest.raises(RecordError, match="incomplete"):
        RecordStore(tmp_path)
    changed = json.loads(original)
    changed["data"]["value"] = 2
    journal.write_text(json.dumps(changed) + "\n")
    with pytest.raises(RecordError, match="hash chain"):
        RecordStore(tmp_path)


def test_failed_write_prevents_subsequent_operations(tmp_path, monkeypatch):
    with RecordStore(tmp_path) as store:
        monkeypatch.setattr("culler_evals.records.os.fsync", lambda descriptor: (_ for _ in ()).throw(OSError("disk failure")))
        with pytest.raises(RecordError, match="required experiment event"):
            store.begin_operation("trial")
        with pytest.raises(RecordError, match="Dependent work"):
            store.begin_operation("dependent")


def test_live_read_only_view_does_not_lock_or_write(tmp_path):
    with RecordStore(tmp_path) as writer, RecordStore(tmp_path, read_only=True) as reader:
        writer.append("first")
        assert [event["kind"] for event in reader.events()] == ["first"]
        writer.append("second")
        assert len(reader.events()) == 2
        with pytest.raises(RecordError, match="only reads"):
            reader.append("third")


def test_second_writer_and_duplicate_completion_are_refused(tmp_path):
    with RecordStore(tmp_path) as store:
        with pytest.raises(RecordError, match="Another process"):
            RecordStore(tmp_path)
        operation = store.begin_operation("trial")
        store.append("diagnostic", operation_id=operation)
        store.end_operation(operation, "success")
        with pytest.raises(RecordError, match="already complete"):
            store.end_operation(operation, "success")


def test_live_reader_waits_for_a_complete_journal_write(tmp_path, monkeypatch):
    partial = threading.Event()
    release = threading.Event()
    reader_started = threading.Event()
    original_open = builtins.open
    original_flock = fcntl.flock

    class PartialWrite:
        def __init__(self, output):
            self.output = output

        def __getattr__(self, name):
            return getattr(self.output, name)

        def __enter__(self):
            return self

        def __exit__(self, *args):
            return self.output.__exit__(*args)

        def write(self, data):
            position = len(data) // 2
            self.output.write(data[:position])
            self.output.flush()
            partial.set()
            assert release.wait(2)
            self.output.write(data[position:])
            return len(data)

    def open_file(path, mode, *args, **kwargs):
        output = original_open(path, mode, *args, **kwargs)
        return PartialWrite(output) if mode == "ab" else output

    def lock_file(descriptor, operation):
        if operation == fcntl.LOCK_SH:
            reader_started.set()
        return original_flock(descriptor, operation)

    def read_events():
        with RecordStore(tmp_path, read_only=True) as reader:
            return reader.events()

    with RecordStore(tmp_path) as writer:
        writer.append("first")
        monkeypatch.setattr("culler_evals.records.open", open_file, raising=False)
        monkeypatch.setattr("culler_evals.records.fcntl.flock", lock_file)
        with ThreadPoolExecutor(max_workers=2) as executor:
            write = executor.submit(writer.append, "second")
            assert partial.wait(2)
            read = executor.submit(read_events)
            assert reader_started.wait(2)
            try:
                assert not read.done()
            finally:
                release.set()
            write.result(timeout=2)
            assert [event["kind"] for event in read.result(timeout=2)] == ["first", "second"]
