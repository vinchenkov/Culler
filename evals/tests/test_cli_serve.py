import pytest

from culler_evals import cli


def serve_args(tmp_path, *extra):
    return cli.parser().parse_args([
        "serve", "--protocol", str(tmp_path / "protocol.json"),
        "--output-root", str(tmp_path / "experiments"),
        "--cache", str(tmp_path / "cache"), *extra,
    ])


def test_serve_parser_retains_private_host_paths(tmp_path):
    args = serve_args(tmp_path, "--credentials", str(tmp_path / "private.json"), "--port", "8123")
    assert args.source == cli.SOURCE
    assert args.output_root == tmp_path / "experiments"
    assert args.credentials == tmp_path / "private.json"
    assert args.port == 8123
    assert not args.no_browser
    assert args.preset is None


def test_serve_opens_selected_preset_without_starting_an_experiment(tmp_path, monkeypatch):
    from culler_evals import control_server
    options = []

    class Server:
        url = "http://127.0.0.1:8123/private-address/"

        def __init__(self, *args, **kwargs):
            options.append(kwargs)

        def __enter__(self):
            return self

        def __exit__(self, *_):
            pass

        def wait(self):
            pass

    monkeypatch.setattr(control_server, "ControlServer", Server)
    monkeypatch.setattr(cli, "_execute_run", lambda _: pytest.fail("Preset selection must make no model call."))
    args = serve_args(tmp_path, "--preset", "test", "--no-browser")
    assert cli.execute(args) == {"status": "experiment browser closed"}
    assert options == [{"port": 0, "preset": "test"}]


@pytest.mark.parametrize("automatic_browser", [False, True])
def test_serve_opens_controls_without_a_model_call(tmp_path, monkeypatch, capsys, automatic_browser):
    from culler_evals import control_server
    calls = []

    class Server:
        url = "http://127.0.0.1:8123/private-address/"

        def __init__(self, protocol, source, credentials, cache, output_root, *, port):
            calls.append((protocol, source, credentials, cache, output_root, port))

        def __enter__(self):
            return self

        def __exit__(self, *_):
            calls.append("closed")

        def wait(self):
            calls.append("wait")

    monkeypatch.setattr(control_server, "ControlServer", Server)
    monkeypatch.setattr(cli.webbrowser, "open", lambda url: calls.append(("browser", url)) or False)
    monkeypatch.setattr(cli, "_execute_run", lambda _: pytest.fail("The browser command must await Start experiment."))
    args = serve_args(tmp_path, *([] if automatic_browser else ["--no-browser"]))
    assert cli.execute(args) == {"status": "experiment browser closed"}
    assert calls[0] == (args.protocol, cli.SOURCE, None, args.cache, args.output_root, 0)
    assert calls[-2:] == ["wait", "closed"]
    assert any(isinstance(call, tuple) and call[0] == "browser" for call in calls) == automatic_browser
    output = " ".join(capsys.readouterr().err.split())
    assert "Start experiment" in output
    assert "interrupt an active" in output
    assert "records remain intact" in output
    assert "private.json" not in output


def test_serve_defaults_to_persistent_host_storage(tmp_path):
    args = cli.parser().parse_args(["serve", "--protocol", str(tmp_path / "protocol.json")])
    assert args.output_root == cli.DEFAULT_EXPERIMENTS
    assert args.cache == cli.DEFAULT_CACHE
