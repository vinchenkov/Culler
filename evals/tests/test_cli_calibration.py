import json
from pathlib import Path

import pytest

from culler_evals import cli
from culler_evals.calibration import calibration_assessment_requests
from culler_evals.protocol import source_revision
from culler_evals.records import RecordStore
from test_calibration import judgment, setup


def test_cli_prepares_assesses_and_records_independent_calibration(setup, tmp_path, monkeypatch):
    store, batch, output, _, rubric = setup
    protocol = store.read_manifest('protocol') | {
        'source_revision': source_revision(cli.SOURCE),
        'retry': {'assessment_attempts': 2},
    }
    store.write_manifest('protocol', protocol)
    batch_path = tmp_path / 'batch.json'
    batch_path.write_text(json.dumps(batch))
    experiment = store.root
    store.close()
    prepared = cli.execute(cli.parser().parse_args([
        'prepare-calibration', str(experiment), '--batch', str(batch_path), '--output', str(output)]))
    assert prepared['packets'] == 10
    with RecordStore(experiment, read_only=True) as view:
        requests = {request['packet_path']: request for request in calibration_assessment_requests(view)}
    closed, calls = [], {}

    class ReferenceRuntime:
        def assess_packet(self, case, packet, root, settings, actual_rubric):
            request = requests[packet]
            assert 'kind' not in case and 'conditions' not in case and 'human' not in case
            assert actual_rubric['operator_context'] == protocol['operator_context']
            calls[packet] = calls.get(packet, 0) + 1
            result = judgment(request['packet'], request['case'], rubric, 'temporary')
            context = request['assessment_context']
            judge = context['judge_runtime']
            result['_runtime'] = {
                'assessment_contract_hash': context['assessment_contract_hash'],
                'metadata': {'requested_model': judge['model'], 'effective_models': [judge['model']],
                             'harness': judge['harness'], 'image': judge['image']},
                'browser': {'wrapper_hashes': context['wrapper_hashes'], 'viewport': judge['viewport'],
                            'visible_headings': []},
            }
            if calls[packet] == 1:
                result['dimensions'].pop('clarity')
            return result

        def close(self):
            closed.append(True)

    class ReferenceEvaluator:
        runtime = ReferenceRuntime()

        def settings(self, role):
            assert role == 'judge'
            return {'model': 'fixture-only'}

        def _runtime_artifacts(self, result):
            return {}

        def _failure_artifacts(self, error, result):
            return {}

    monkeypatch.setattr(cli, '_runtime', lambda *args: ReferenceEvaluator())
    monkeypatch.setattr(cli, 'prepare_case', lambda case, *args, **kwargs: dict(case))
    assessed = cli.execute(cli.parser().parse_args([
        'assess-calibration', str(experiment), '--cache', str(tmp_path / 'cache')]))
    assert assessed['recorded'] == 5 and assessed['pending'] == 0
    assert closed == [True]
    assert set(calls.values()) == {2}
    human = json.loads((output / 'review.json').read_text())
    human['reviewer'] = 'Fixture human reviewer'
    human['blinded'] = True
    for row in human['records']:
        row['dimensions'] = {name: 3 for name in rubric['dimensions']}
        row['critical_failure'] = False
        row['reason'] = 'Fixture assessment only.'
    human_path = output / 'complete.json'
    human_path.write_text(json.dumps(human))
    assert cli.execute(cli.parser().parse_args([
        'record-calibration', str(experiment), '--record', str(human_path)]))['recorded'] == 10
    exported = cli.execute(cli.parser().parse_args([
        'export-calibration', str(experiment), '--output', str(tmp_path / 'export')]))
    datasets = {name: json.loads(Path(value['path']).read_text()) for name, value in exported.items()}
    assert len(datasets['record']) == 6 and len(datasets['check_record']) == 4
    assert not {row['group'] for row in datasets['record']} & {row['group'] for row in datasets['check_record']}
    with RecordStore(experiment, read_only=True) as view:
        failures = [event for event in view.events() if event['kind'] == 'operation_completed'
                    and event['data'].get('outcome') == 'failed']
        assert len(failures) == 5
        assert all(event['data']['result']['judgment'] for event in failures)
        assert not view.completeness()['unfinished_operations']


def test_cli_rejects_missing_pilot_without_a_model_call(tmp_path):
    import pytest
    with pytest.raises(ValueError, match='completed pilot'):
        cli.execute(cli.parser().parse_args([
            'assess-calibration', str(tmp_path / 'empty'), '--cache', str(tmp_path / 'cache')]))
    with RecordStore(tmp_path / 'empty', read_only=True) as view:
        assert len(view.events()) == 2
        assert view.events()[-1]['data']['outcome'] == 'failed'
        assert not view.completeness()['unfinished_operations']


def prepare_cli_reference_fixture(setup, tmp_path):
    store, batch, output, _, rubric = setup
    store.write_manifest("protocol", store.read_manifest("protocol") | {"retry": {"assessment_attempts": 2}})
    batch_path = tmp_path / "batch.json"
    batch_path.write_text(json.dumps(batch))
    experiment = store.root
    store.close()
    cli.execute(cli.parser().parse_args(["prepare-calibration", str(experiment),
                "--batch", str(batch_path), "--output", str(output)]))
    return experiment, rubric


def test_reference_context_change_blocks_model_calls(setup, tmp_path, monkeypatch):
    experiment, _ = prepare_cli_reference_fixture(setup, tmp_path)
    original = cli.freeze_protocol

    def changed_wrapper(protocol, source):
        frozen = original(protocol, source)
        frozen["product_wrapper_hashes"] = {path: "f" * 64 for path in frozen["product_wrapper_hashes"]}
        return frozen

    monkeypatch.setattr(cli, "freeze_protocol", changed_wrapper)
    calls = []
    monkeypatch.setattr(cli, "_runtime", lambda *args: calls.append(args))
    with pytest.raises(ValueError, match="selected Judge context"):
        cli.execute(cli.parser().parse_args(["assess-calibration", str(experiment),
                    "--cache", str(tmp_path / "cache")]))
    assert calls == []


def test_reference_assessments_can_resume_after_failed_attempts(setup, tmp_path, monkeypatch):
    experiment, rubric = prepare_cli_reference_fixture(setup, tmp_path)
    with RecordStore(experiment, read_only=True) as store:
        requests = {request["packet_path"]: request for request in calibration_assessment_requests(store)}
    paths, closed = [], []
    fail = True

    class ReferenceRuntime:
        def assess_packet(self, case, packet, root, settings, actual_rubric):
            root.mkdir(parents=True, exist_ok=False)
            paths.append(root)
            request = requests[packet]
            result = judgment(request["packet"], request["case"], rubric, "temporary")
            if fail:
                result["dimensions"].pop("clarity")
            return result

        def close(self):
            closed.append(True)

    class ReferenceEvaluator:
        runtime = ReferenceRuntime()

        def settings(self, role):
            return {}

        def _runtime_artifacts(self, result):
            return {}

        def _failure_artifacts(self, error, result):
            return {}

    monkeypatch.setattr(cli, "_runtime", lambda *args: ReferenceEvaluator())
    monkeypatch.setattr(cli, "prepare_case", lambda case, *args, **kwargs: dict(case))
    arguments = cli.parser().parse_args(["assess-calibration", str(experiment), "--cache", str(tmp_path / "cache")])
    with pytest.raises(RuntimeError, match="remains incomplete"):
        cli.execute(arguments)
    assert len(paths) == 2
    fail = False
    result = cli.execute(arguments)
    assert result["recorded"] == 5 and result["pending"] == 0
    assert len(paths) == len(set(paths)) == 7
    assert len(closed) == 2
    with RecordStore(experiment, read_only=True) as store:
        assert not store.completeness()["unfinished_operations"]
