"""Real harness sessions in isolated containers. No model call uses the host workspace."""

from __future__ import annotations

from contextlib import contextmanager
import atexit
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import threading
import uuid

from .browser import BROWSER_TOOLS, BrowserServer, PacketBrowser
from .commands import CommandAdapter, CommandServer
from .credentials import CredentialPool
from .product_sources import (ProductSourceError, create_product_snapshot, product_source_hash,
                              product_source_manifest, verify_product_sources)
from .protocol import digest, effort_choices, fast_mode_error
from .runtime_sources import RuntimeSourceError, create_runtime_snapshot, verify_runtime_sources


class RuntimeErrorWithEvidence(RuntimeError):
    def __init__(self, message: str, artifacts: list[dict] | None = None):
        super().__init__(message)
        self.artifact_paths = artifacts or []


def control_cancelled() -> bool:
    path = os.environ.get("CULLER_EVAL_CONTROL_CANCEL_FILE")
    return bool(path and Path(path).exists())


class ProductContracts:
    """The bridge compiles against a disposable copy of the selected product module."""

    def __init__(self, source_root: Path, cache_root: Path | None = None, *, source_manifest: dict | None = None):
        self.source_root = Path(source_root).resolve()
        self.cache_root = Path(cache_root or tempfile.mkdtemp(prefix="culler-eval-contracts-"))
        self.source_manifest = json.loads(json.dumps(source_manifest or product_source_manifest(self.source_root)))
        self.source_hash = product_source_hash(self.source_manifest)
        self.snapshot_root = self.cache_root / self.source_hash / "source"
        self.binary: Path | None = None
        self.binary_sha256: str | None = None
        self.module: Path | None = None
        self.lock = threading.Lock()

    def execution_sources(self) -> Path:
        with self.lock:
            try:
                return create_product_snapshot(self.source_root, self.snapshot_root, self.source_manifest)
            except (ProductSourceError, OSError) as error:
                raise RuntimeErrorWithEvidence(str(error)) from error

    def build(self) -> Path:
        with self.lock:
            return self._build()

    def _build(self) -> Path:
        try:
            sources = create_product_snapshot(self.source_root, self.snapshot_root, self.source_manifest)
        except (ProductSourceError, OSError) as error:
            raise RuntimeErrorWithEvidence(str(error)) from error
        if self.binary is not None:
            if self.binary.is_symlink() or hashlib.sha256(self.binary.read_bytes()).hexdigest() != self.binary_sha256:
                raise RuntimeErrorWithEvidence("The product contract executable differs from its recorded build. Rebuild the contract helper from the recorded source.")
            return self.binary
        build_root = Path(tempfile.mkdtemp(prefix="build-", dir=self.snapshot_root.parent))
        module = build_root / "mc"
        shutil.copytree(sources / "mc", module)
        for directory, _names, _files in os.walk(module):
            Path(directory).chmod(0o755)
        shutil.copyfile(sources / ".mise.toml", build_root / ".mise.toml")
        bridge = sources / "evals/bridge"
        shutil.copyfile(bridge / "contracts.go", module / "verbs/eval-contracts.go")
        shutil.copyfile(bridge / "spine-parity_test.go", module / "verbs/eval-spine-parity_test.go")
        main = module / "eval-contracts"
        main.mkdir(exist_ok=True)
        shutil.copyfile(bridge / "contracts-main.go", main / "main.go")
        binary = build_root / "product-contracts"
        env = os.environ.copy()
        env["GOCACHE"] = str(self.cache_root / "go-cache")
        env.update(self.source_manifest["compile_recipe"]["environment"])
        env["MISE_TRUSTED_CONFIG_PATHS"] = str(build_root / ".mise.toml")
        version = subprocess.run(["mise", "exec", "--", "go", "env", "GOVERSION"],
                                 cwd=build_root, capture_output=True, text=True, env=env)
        if version.returncode or version.stdout.strip() != self.source_manifest["compile_recipe"]["go_version"]:
            raise RuntimeErrorWithEvidence("The product contract toolchain differs from its fixed Go version. Restore the recorded Go toolchain before continuation.")
        verified = subprocess.run(["mise", "exec", "--", "go", "-C", str(module), "mod", "verify"],
                                  cwd=build_root, capture_output=True, text=True, env=env)
        if verified.returncode:
            raise RuntimeErrorWithEvidence("The product contract dependencies differ from their module checksums. Restore verified module dependencies before continuation.")
        result = subprocess.run(["mise", "exec", "--", "go", "-C", str(module), "build", "-o", str(binary), "./eval-contracts"], cwd=build_root, capture_output=True, text=True, env=env)
        if result.returncode:
            raise RuntimeErrorWithEvidence("The product contract bridge failed to compile: " + result.stderr.strip())
        verify_product_sources(self.source_root, self.source_manifest)
        verify_product_sources(sources, self.source_manifest)
        self.binary = binary
        self.binary_sha256 = hashlib.sha256(binary.read_bytes()).hexdigest()
        self.module = module
        return binary

    def call(self, operation: str, **fields) -> dict:
        env = os.environ.copy()
        if operation == "artifact":
            env["MC_ARTIFACTS_ROOT"] = fields.pop("artifacts_root")
        result = subprocess.run([str(self.build())], input=json.dumps({"operation": operation, **fields}),
                                capture_output=True, text=True, env=env)
        if result.returncode:
            raise RuntimeErrorWithEvidence("The product contract bridge refused its input: " + result.stderr.strip())
        return json.loads(result.stdout)

    def check_spine(self) -> dict:
        self.build()
        env = os.environ.copy()
        env["GOCACHE"] = str(self.cache_root / "go-cache")
        env.update(self.source_manifest["compile_recipe"]["environment"])
        env["MISE_TRUSTED_CONFIG_PATHS"] = str(self.module.parent / ".mise.toml")
        result = subprocess.run(["./test.sh", "-count=1", "-run", "^TestEvalPackagerSpineParity$", "./verbs"],
                                cwd=self.module, capture_output=True, text=True, env=env)
        return {"accepted": result.returncode == 0, "output": result.stdout + result.stderr}


def _replace_identity(value, run_id: str):
    if isinstance(value, str):
        return value.replace("{{run_id}}", run_id)
    if isinstance(value, list):
        return [_replace_identity(item, run_id) for item in value]
    if isinstance(value, dict):
        return {key: _replace_identity(item, run_id) for key, item in value.items()}
    return value


def _private_file(path: Path, value: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(value)
    path.chmod(0o600)


def _artifacts(root: Path) -> list[dict]:
    paths = [path for path in root.rglob("*") if not path.is_symlink() and path.is_file() and path.resolve().is_relative_to(root.resolve()) and "private-config" not in path.parts]
    return [{"type": path.suffix.lstrip(".") or "file", "path": str(path)} for path in sorted(paths)]


def _json_response(output: str, artifacts: list[dict] | None = None) -> dict:
    if not isinstance(output, str):
        raise RuntimeErrorWithEvidence("The harness returned no complete JSON object.", artifacts)
    value = output.strip()
    if value.startswith("```json") and value.endswith("```"):
        value = value[7:-3]
    try:
        response = json.loads(value)
    except json.JSONDecodeError:
        raise RuntimeErrorWithEvidence("The harness returned no complete JSON object.", artifacts) from None
    if not isinstance(response, dict):
        raise RuntimeErrorWithEvidence("The harness result must contain one JSON object.", artifacts)
    return response


def _render_failures(response: dict, report: dict) -> list[dict]:
    uninspected = {str(section).strip().casefold() for section in response.get("uninspected_content", [])}
    failures = []
    for kind in ("failed_images", "failed_media"):
        for item in report.get("render", {}).get(kind, []):
            if item.get("section", "").strip().casefold() in uninspected:
                failures.append({"kind": kind, **item})
    return failures


class Runtime:
    def __init__(self, source_root: Path, *, executor=None, contracts=None, credential_provider=None,
                 source_manifest: dict | None = None, snapshot_root: Path | None = None,
                 product_manifest: dict | None = None):
        self.source_root = Path(source_root).resolve()
        self.source_manifest = source_manifest
        self.product_manifest = product_manifest
        self.snapshot_root = Path(snapshot_root) if snapshot_root else None
        self.snapshot_lock = threading.Lock()
        self.control_run = os.environ.get("CULLER_EVAL_CONTROL_RUN")
        self.control_cancel_path = os.environ.get("CULLER_EVAL_CONTROL_CANCEL_FILE")
        control_action = " Clear CULLER_EVAL_CONTROL_RUN and CULLER_EVAL_CONTROL_CANCEL_FILE from the environment. Then rerun the command."
        if bool(self.control_run) != bool(self.control_cancel_path):
            raise RuntimeErrorWithEvidence("The browser run requires its identifier and stop file together." + control_action)
        if self.control_run and re.fullmatch(r"[a-zA-Z0-9-]{1,80}", self.control_run) is None:
            raise RuntimeErrorWithEvidence("The browser run identifier is invalid." + control_action)
        self.executor = executor or self._docker_execute
        self.contracts = contracts or ProductContracts(self.source_root, source_manifest=product_manifest)
        self.product_contracts = self.contracts if isinstance(self.contracts, ProductContracts) else None
        self.credential_provider = credential_provider
        self.credential_pool = CredentialPool(lambda: self.product_execution_sources(), checkout_root=self.source_root,
                                              bridge_root=lambda: self.execution_sources() / "evals/bridge")
        atexit.register(self.close)

    def execution_sources(self) -> Path:
        if self.source_manifest is None:
            raise RuntimeErrorWithEvidence("The real runtime requires a frozen runtime source manifest.")
        with self.snapshot_lock:
            try:
                verify_runtime_sources(self.source_root, self.source_manifest)
                if self.snapshot_root is None:
                    self.snapshot_root = Path(tempfile.mkdtemp(prefix="culler-eval-runtime-")) / "source"
                return create_runtime_snapshot(self.source_root, self.snapshot_root, self.source_manifest)
            except (RuntimeSourceError, OSError) as error:
                raise RuntimeErrorWithEvidence(str(error)) from error

    def close(self):
        self.credential_pool.close()

    def _check_control_stop(self):
        if self.control_cancel_path and Path(self.control_cancel_path).exists():
            raise KeyboardInterrupt("The browser server requested experiment interruption.")

    def product_execution_sources(self) -> Path:
        if self.product_manifest is None:
            raise RuntimeErrorWithEvidence("The real runtime requires a frozen product source manifest. Start a new experiment from the complete source checkout.")
        if self.product_contracts is None:
            self.product_contracts = ProductContracts(self.source_root, source_manifest=self.product_manifest)
        return self.product_contracts.execution_sources()

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        self.close()

    def assemble_input(self, case: dict, directive: str, run_id: str) -> dict:
        parts = case.get("brief_parts")
        if parts is None:
            raise RuntimeErrorWithEvidence("The case requires product-compatible brief parts.")
        skills = case.get("project_skills", [])
        if any(skill.get("name") == "writing" for skill in skills):
            raise RuntimeErrorWithEvidence("The separate writing skill duplicates the editable directive.")
        return self.contracts.call("input", run_id=run_id, directive=directive,
                                   parts=_replace_identity(parts, run_id), project_skills=skills)

    @contextmanager
    def _credentials(self, settings: dict):
        with tempfile.TemporaryDirectory(prefix="culler-eval-credentials-") as directory:
            private = Path(directory)
            private.chmod(0o700)
            projection = dict(settings.get("credentials", {}))
            handle = None
            if self.credential_provider is not None:
                handle = self.credential_provider(settings, private)
                projection = handle.__enter__()
            elif projection.get("profile"):
                projection = self.credential_pool.project(projection["profile"], projection["binding"], settings["model"],
                                                          settings.get("timeout_seconds", 1800))
            try:
                yield projection, private
            finally:
                if handle is not None:
                    handle.__exit__(None, None, None)
                elif settings.get("credentials", {}).get("profile"):
                    credentials = settings["credentials"]
                    projection["provider_requests"] = self.credential_pool.release(credentials["profile"], credentials["binding"], projection)

    def _docker_execute(self, request: dict, settings: dict, execution_root: Path, mounts: list[dict], env: dict) -> dict:
        self._check_control_stop()
        if not settings.get("image") or not settings.get("model"):
            raise RuntimeErrorWithEvidence("The real runtime requires an image and an exact model identifier.")
        duration = settings.get("timeout_seconds", 1800)
        if type(duration) is not int or not 1 <= duration <= 1800:
            raise RuntimeErrorWithEvidence("The evaluation session requires a timeout from 1 to 1800 seconds.")
        if request["harness"] not in ("codex", "claude"):
            raise RuntimeErrorWithEvidence("The runtime harness family is unsupported.")
        if settings.get("effort") not in effort_choices(request["harness"]):
            raise RuntimeErrorWithEvidence("The real runtime requires an explicit effort supported by its harness. Set the role's effort in the experiment configuration.")
        if request.get("effort") != settings["effort"]:
            raise RuntimeErrorWithEvidence("The requested effort differs from the fixed runtime configuration. Use the effort from the fixed experiment configuration.")
        if not isinstance(settings.get("channel"), str) or not isinstance(settings.get("base_url"), str):
            raise RuntimeErrorWithEvidence("The real runtime requires a declared credential channel and base URL.")
        error = fast_mode_error(settings, "runtime.fast_mode")
        if error:
            raise RuntimeErrorWithEvidence(error)
        if request.get("fast_mode", False) != settings.get("fast_mode", False):
            raise RuntimeErrorWithEvidence("The requested fast mode differs from the experiment configuration.")
        sources = self.execution_sources()
        source_hash = verify_runtime_sources(sources, self.source_manifest)
        product_sources = self.product_execution_sources()
        product_hash = verify_product_sources(product_sources, self.product_manifest)
        execution_root.mkdir(parents=True, exist_ok=False)
        execution_root.chmod(0o700)
        container_root = execution_root / "container"
        container_root.mkdir(mode=0o777)
        container_root.chmod(0o777)
        _private_file(execution_root / "prompt.txt", request["prompt"])
        trace_path = execution_root / "events.jsonl"
        stderr_path = execution_root / "stderr.txt"
        bridge_root = sources / "evals/bridge"
        tools_file = execution_root / "browser-tools.json"
        tools_file.write_text(json.dumps(BROWSER_TOOLS))
        (container_root / "request.json").touch()
        (container_root / "browser-tools.json").touch()
        with self._credentials(settings) as (projection, private):
            if projection.get("channel", settings["channel"]) != settings["channel"]:
                raise RuntimeErrorWithEvidence("The credential channel differs from the frozen runtime configuration.", _artifacts(execution_root))
            if projection.get("base_url", settings["base_url"]) != settings["base_url"]:
                raise RuntimeErrorWithEvidence("The credential base URL differs from the frozen runtime configuration.", _artifacts(execution_root))
            actual_request = {**request, "channel": projection.get("channel", request.get("channel")),
                              "base_url": projection.get("base_url", request.get("base_url", ""))}
            actual_request["channel"] = settings["channel"]
            actual_request["base_url"] = settings["base_url"]
            request_path = execution_root / "request.json"
            request_path.write_text(json.dumps({key: value for key, value in actual_request.items() if key != "prompt"}, indent=2) + "\n")
            request_path.chmod(0o644)
            if any(name in projection for name in ("env", "mounts", "auth_json", "authJson")):
                raise RuntimeErrorWithEvidence("Raw credential projection differs from the Culler proxy contract. Use a saved Culler connection.", _artifacts(execution_root))
            access = projection.get("provider_access")
            family = "claude-sdk" if request["harness"] == "claude" else "codex"
            if (not isinstance(access, dict) or set(access) != {"version", "url", "token", "ca", "harness", "authMode"}
                    or access.get("version") != 1 or access.get("harness") != family
                    or access.get("authMode") not in ("account", "api-key")
                    or not isinstance(access.get("token"), str) or re.fullmatch(r"culler_proxy_[A-Za-z0-9_-]{43}", access["token"]) is None
                    or not isinstance(access.get("ca"), str) or "-----BEGIN CERTIFICATE-----" not in access["ca"]
                    or "PRIVATE KEY" in access["ca"] or not isinstance(access.get("url"), str)
                    or not re.fullmatch(r"https://[^/@?#]+(?::[0-9]+)?/[^?#]*|https://[^/@?#]+(?::[0-9]+)?", access["url"])):
                raise RuntimeErrorWithEvidence("The evaluation requires scoped Culler provider access. Use a saved Culler connection.", _artifacts(execution_root))
            protected = re.compile(r"^(?:OPENAI_|ANTHROPIC_|CODEX_|CLAUDE_|MC_PROVIDER_ACCESS_FILE$|CULLER_PROXY_TOKEN$)")
            if any(protected.match(name) for name in env):
                raise RuntimeErrorWithEvidence("The container environment cannot replace provider access.", _artifacts(execution_root))
            access_file = private / "provider-access.json"
            access_file.write_text(json.dumps(access))
            access_file.chmod(0o444)
            all_env = {"HOME": "/home/mc-model", "MC_LOG_LEVEL": "error", **env,
                       "MC_PROVIDER_ACCESS_FILE": "/mc/provider-access.json"}
            auth_mounts = [{"source": str(access_file), "target": "/mc/provider-access.json", "read_only": True}]
            capability_mounts = []
            if all_env.get("CULLER_EVAL_COMMAND_URL") and all_env.get("CULLER_EVAL_COMMAND_TOKEN"):
                command_file = private / "command.json"
                command_file.write_text(json.dumps({"url": all_env.pop("CULLER_EVAL_COMMAND_URL"),
                                                    "token": all_env.pop("CULLER_EVAL_COMMAND_TOKEN")}))
                command_file.chmod(0o644)
                capability_mounts.append({"source": str(command_file), "target": "/mc/eval-command.json", "read_only": True})
            env_file = private / "container.env"
            for name, value in all_env.items():
                if not isinstance(value, str) or "\n" in value or "\r" in value or "=" in name:
                    raise RuntimeErrorWithEvidence("The projected container environment contains an invalid entry.")
            _private_file(env_file, "\n".join(f"{name}={value}" for name, value in all_env.items()) + "\n")
            if projection.get("harness") and projection["harness"] != request["harness"]:
                raise RuntimeErrorWithEvidence("The credential profile requires a different harness family.")
            name = settings.get("_container_name", "culler-eval-" + uuid.uuid4().hex)
            argv = ["docker", "run", "--interactive", "--rm", "--name", name, "--init", "--user", "10002:10002", "--cap-drop", "ALL",
                    "--security-opt", "no-new-privileges=true", "--add-host", "host.docker.internal:host-gateway", "--env-file", str(env_file),
                    "--mount", f"type=bind,src={sources / 'runner'},dst=/app/src,readonly",
                    "--mount", f"type=bind,src={bridge_root},dst=/app/eval,readonly",
                    "--mount", f"type=bind,src={container_root},dst=/eval",
                    "--mount", f"type=bind,src={request_path},dst=/eval/request.json,readonly",
                    "--mount", f"type=bind,src={tools_file},dst=/eval/browser-tools.json,readonly",
                    "--workdir", request["workspace"]]
            if self.control_run:
                argv += ["--label", "org.culler.evals.control-run=" + self.control_run]
            for mount in [*mounts, *auth_mounts, *capability_mounts]:
                source = str(Path(mount["source"]).resolve())
                target = mount["target"]
                if "," in source or "," in target:
                    raise RuntimeErrorWithEvidence("The runtime mount path contains an unsupported separator.")
                argv += ["--mount", f"type=bind,src={source},dst={target}" + (",readonly" if mount.get("read_only", True) else "")]
            argv += [settings["image"], "bun", "/app/eval/launch.ts"]
            try:
                self._check_control_stop()
                with trace_path.open("w") as stdout, stderr_path.open("w") as stderr:
                    result = subprocess.run(argv, input=request["prompt"], text=True, stdout=stdout, stderr=stderr,
                                            timeout=settings.get("timeout_seconds", 1800))
                self._check_control_stop()
            except KeyboardInterrupt:
                try:
                    subprocess.run(["docker", "rm", "--force", name], capture_output=True, timeout=10)
                except (subprocess.TimeoutExpired, OSError):
                    pass
                raise
            except (subprocess.TimeoutExpired, OSError) as error:
                subprocess.run(["docker", "rm", "--force", name], capture_output=True)
                raise RuntimeErrorWithEvidence("The isolated harness session failed: " + type(error).__name__, _artifacts(execution_root)) from error
            finally:
                token = access["token"]
                for retained in (trace_path, stderr_path, *(container_root / "session").rglob("*.jsonl")):
                    if retained.is_file() and not retained.is_symlink() and retained.resolve().is_relative_to(execution_root.resolve()):
                        body = retained.read_bytes()
                        retained.write_bytes(body.replace(token.encode(), b"*" * len(token)))
        if result.returncode:
            raise RuntimeErrorWithEvidence("The isolated harness returned an infrastructure failure.", _artifacts(execution_root))
        events = []
        for line in trace_path.read_text().splitlines():
            try:
                events.append(json.loads(line))
            except json.JSONDecodeError:
                continue
        completions = [event for event in events if event.get("event") == "turn-complete" and event.get("status") == "success"]
        outputs = [event.get("output") for event in completions]
        if not outputs:
            raise RuntimeErrorWithEvidence("The harness completed without a successful result record.", _artifacts(execution_root))
        usage = [event["usage"] for event in events if "usage" in event]
        observed_models = sorted({str(event["model"]) for event in events if event.get("model")})
        observed_efforts = {event["effort"] for event in events
                            if event.get("type") == "system" and event.get("subtype") == "init"
                            and isinstance(event.get("effort"), str)}
        for path in (container_root / "session").rglob("*.jsonl"):
            if path.is_symlink() or not path.resolve().is_relative_to((container_root / "session").resolve()):
                continue
            for line in path.read_text().splitlines():
                try:
                    native = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if native.get("type") == "turn_context" and native.get("payload", {}).get("model"):
                    observed_models.append(str(native["payload"]["model"]))
                if native.get("type") == "turn_context" and isinstance(native.get("payload", {}).get("effort"), str):
                    observed_efforts.add(native["payload"]["effort"])
        observed_models = sorted(set(observed_models))
        versions = sorted({str(event["harness_version"]) for event in events if event.get("harness_version")})
        configurations = [event for event in events if event.get("event") == "runtime-config"]
        if (len(configurations) != 1 or configurations[0].get("requested_effort") != settings["effort"]
                or configurations[0].get("configured_effort") != settings["effort"]):
            raise RuntimeErrorWithEvidence("The harness lacks a matching effort configuration record. Check the recorded trace and restore the selected evaluation bridge.", _artifacts(execution_root))
        if observed_efforts and observed_efforts != {settings["effort"]}:
            raise RuntimeErrorWithEvidence("The harness used a different effort. Use the effort from the fixed experiment configuration.", _artifacts(execution_root))
        if request.get("output_schema") is not None:
            configuration = configurations[0]
            if (configuration.get("configured_output_format") != "json_schema"
                    or configuration.get("configured_output_schema") != request["output_schema"]
                    or completions[-1].get("output_format") != "json_schema"):
                raise RuntimeErrorWithEvidence("The harness lacks its validated structured output record.", _artifacts(execution_root))
        if settings.get("fast_mode", False) or request["harness"] == "claude" and "fast_mode" in settings:
            configuration = configurations[0]
            enabled = settings.get("fast_mode", False)
            if (configuration.get("requested_fast_mode") is not enabled
                    or configuration.get("configured_fast_mode") is not enabled
                    or request["harness"] == "codex" and enabled and configuration.get("configured_service_tier") != "fast"):
                raise RuntimeErrorWithEvidence("The harness lacks its fast mode configuration record.", _artifacts(execution_root))
        fast_mode_observations = [{key: event[key] for key in ("fast_mode_state", "fast_mode_disabled_reason") if key in event}
                                  for event in events if event.get("type") in ("system", "result")
                                  and any(key in event for key in ("fast_mode_state", "fast_mode_disabled_reason"))]
        repository_states = [event["preserved"] for event in events if event.get("event") == "repository-state"]
        if request["role"] == "packager" and (not repository_states or type(repository_states[-1]) is not bool):
            raise RuntimeErrorWithEvidence("The Packager session lacks its final repository check.", _artifacts(execution_root))
        if observed_models and observed_models != [settings["model"]]:
            raise RuntimeErrorWithEvidence("The harness used a different model identifier.", _artifacts(execution_root))
        return {"output": outputs[-1], "trace_path": str(trace_path), "artifact_paths": _artifacts(execution_root),
                "metadata": {"requested_model": settings["model"], "effective_models": observed_models or None,
                             "requested_effort": settings["effort"], "configured_effort": configurations[0]["configured_effort"],
                             "observed_efforts": sorted(observed_efforts) or None,
                             "harness": request["harness"], "harness_versions": versions or None,
                             "requested_fast_mode": settings.get("fast_mode", False),
                             "configured_fast_mode": configurations[0].get("configured_fast_mode"),
                             "configured_service_tier": configurations[0].get("configured_service_tier"),
                             "fast_mode_observations": fast_mode_observations or None,
                             "output_format": configurations[0].get("configured_output_format", "text"),
                             "output_schema_hash": digest(request["output_schema"]) if request.get("output_schema") is not None else None,
                             "channel": actual_request.get("channel"), "base_url": actual_request.get("base_url", ""),
                             "runtime_source_hash": source_hash, "product_source_hash": product_hash,
                             "repository_changed": not repository_states[-1] if repository_states else None,
                             "usage": usage or None, "image": settings["image"], "provider_requests": projection.get("provider_requests")}}

    def _execute(self, role: str, prompt: str, trial_root: Path, settings: dict, *, skills=None, mounts=None, env=None,
                 workspace="/workspace/source", output_schema=None):
        self._check_control_stop()
        request = {"role": role, "harness": settings["harness"], "model": settings.get("model"),
                   "effort": settings.get("effort"), "fast_mode": settings.get("fast_mode", False),
                   "channel": settings.get("channel", settings.get("credentials", {}).get("channel")),
                   "base_url": settings.get("base_url", settings.get("credentials", {}).get("base_url", "")),
                   "workspace": workspace, "skills": skills or [], "prompt": prompt}
        if output_schema is not None and settings["harness"] == "claude":
            request["output_schema"] = output_schema
        result = self.executor(request, settings, Path(trial_root) / "execution", mounts or [], env or {})
        self._check_control_stop()
        return result

    def execute_packager(self, case: dict, directive: str, trial_root: Path, settings: dict) -> dict:
        self._check_control_stop()
        if not case.get("readiness", {}).get("ready", False) and self.executor == self._docker_execute:
            raise RuntimeErrorWithEvidence("The case remains unready for a real Packager trial.")
        sources = self.execution_sources() if self.executor == self._docker_execute else self.source_root
        root = Path(trial_root) / "packager"
        root.mkdir(parents=True, exist_ok=False)
        root.chmod(0o700)
        repository = root / "repository"
        artifacts = root / "artifacts"
        shutil.copytree(case["repository_path"], repository, symlinks=True)
        shutil.copytree(case["artifacts_path"], artifacts, symlinks=True)
        for directory in [repository, artifacts, *[path for path in [*repository.rglob("*"), *artifacts.rglob("*")] if not path.is_symlink() and path.is_dir()]]:
            directory.chmod(0o777)
        for file in repository.rglob("*"):
            if not file.is_symlink() and file.is_file():
                file.chmod((file.stat().st_mode & 0o111) | 0o666)
        before = {str(path.relative_to(artifacts)): hashlib.sha256(path.read_bytes()).hexdigest() for path in artifacts.rglob("*") if path.is_file()}
        run_id = "eval-" + uuid.uuid4().hex
        assembled = self.assemble_input(case, directive, run_id)
        adapter = CommandAdapter(case, repository, artifacts, run_id, root / "command-state")
        actual_settings = settings
        if self.executor == self._docker_execute:
            container_name = "culler-eval-" + uuid.uuid4().hex
            actual_settings = {**settings, "_container_name": container_name}
            adapter.git_runner = lambda args: subprocess.run(["docker", "exec", container_name, "git", "-C", "/workspace/source", *args], capture_output=True, text=True)
        envelope = {"run_id": run_id, "tier": "pipeline", "role": "packager", "subject": case["task_id"],
                    "mounts": {"allow_write": adapter.planes}, "skills": assembled["skills"], "synthetic": True}
        envelope_path = root / "run.json"
        envelope_path.write_text(json.dumps(envelope, indent=2) + "\n")
        envelope_path.chmod(0o644)
        with CommandServer(adapter) as server:
            result = self._execute("packager", assembled["input"], root, actual_settings, skills=assembled["skills"],
                                   mounts=[{"source": str(repository), "target": "/workspace/source", "read_only": False},
                                           {"source": str(envelope_path), "target": "/mc/run.json", "read_only": True},
                                           {"source": str(sources / "evals/bridge/mc"), "target": "/usr/local/bin/mc", "read_only": True},
                                           *[{"source": str(artifacts / Path(plane).name), "target": plane, "read_only": False} for plane in adapter.planes]],
                                   env={"CULLER_EVAL_COMMAND_URL": server.url, "CULLER_EVAL_COMMAND_TOKEN": server.token,
                                        "CULLER_EVAL_START_COMMIT": adapter.start_commit})
        terminal = adapter.terminal
        if terminal:
            terminal = {**terminal, "status": terminal["kind"]}
        altered = [path for path, digest in before.items() if (artifacts / path).is_symlink() or not (artifacts / path).is_file()
                   or not (artifacts / path).resolve().is_relative_to(artifacts.resolve()) or hashlib.sha256((artifacts / path).read_bytes()).hexdigest() != digest]
        return {"packet_path": terminal.get("packet_path") if terminal else None, "terminal": terminal,
                "commands_path": str(adapter.log_path), "trace_path": result.get("trace_path"),
                "metadata": {**result.get("metadata", {}), "input_schema": assembled["brief_schema"], "run_id": run_id,
                             "omitted_parts": assembled.get("omitted_parts", []),
                             "evidence_changed": altered},
                "limitations": ["The command adapter reproduces Packager commands without the production spine or dispatch lifecycle."],
                "_runtime": {"artifact_paths": [*result.get("artifact_paths", []), *_artifacts(adapter.root)]}}

    def repository_states(self, repositories: dict[str, str], settings: dict) -> dict:
        if not repositories or set(repositories) - {"authority", "packager"}:
            raise RuntimeErrorWithEvidence("The repository probe requires private trial repository roles.")
        sources = self.execution_sources()
        product_sources = self.product_execution_sources()
        argv = ["docker", "run", "--rm", "--network", "none", "--read-only", "--user", "10002:10002",
                "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true", "--tmpfs", "/tmp:rw,nosuid,nodev,mode=1777",
                "--env", "MISE_TRUSTED_CONFIG_PATHS=/app/eval/.mise.toml", "--workdir", "/app/eval",
                "--mount", f"type=bind,src={product_sources / '.mise.toml'},dst=/app/eval/.mise.toml,readonly",
                "--mount", f"type=bind,src={sources / 'evals/bridge/repository-state.ts'},dst=/app/eval/repository-state.ts,readonly"]
        for role, path in repositories.items():
            if "," in str(path):
                raise RuntimeErrorWithEvidence("The runtime mount path contains an unsupported separator.")
            argv += ["--mount", f"type=bind,src={Path(path)},dst=/evidence/{role},readonly"]
        argv += [settings["image"], "mise", "exec", "--", "bun", "/app/eval/repository-state.ts", *repositories]
        try:
            result = subprocess.run(argv, capture_output=True, text=True, timeout=60)
        except (subprocess.TimeoutExpired, OSError):
            return {"states": {}, "image": settings["image"], "verified": False}
        if result.returncode:
            return {"states": {}, "image": settings["image"], "verified": False}
        response = _json_response(result.stdout)
        return {"states": response.get("states", {}), "image": settings["image"],
                "runtime_source_hash": verify_runtime_sources(sources, self.source_manifest)}

    def assess_packet(self, case: dict, packet, trial_root: Path, settings: dict, rubric: dict) -> dict:
        self._check_control_stop()
        from .assessment import SECTIONS, SEMANTIC_CHECKS, SEMANTIC_CONTRACTS, JUDGE_INSTRUCTIONS, judge_output_schema
        from .protocol import assessment_contract, digest
        contract_hash = digest(assessment_contract())
        root = Path(trial_root) / ("judge-" + uuid.uuid4().hex)
        root.mkdir(parents=True, exist_ok=False)
        workspace = root / "workspace"
        workspace.mkdir()
        derived = root / "derived-evidence"
        derived.mkdir()
        diff = subprocess.run(["git", "-C", case["repository_path"], "diff", "--no-ext-diff", case["base_commit"], case["reviewed_commit"]],
                              capture_output=True, text=True)
        if diff.returncode:
            raise RuntimeErrorWithEvidence("The Judge evidence lacks the actual reviewed Git range.")
        (derived / "reviewed.diff").write_text(diff.stdout)
        prompt = (JUDGE_INSTRUCTIONS
                  + json.dumps({"case_id": case.get("case_id", case.get("id")), "references": case["references"], "rubric": rubric,
                                "required_semantic_checks": SEMANTIC_CHECKS, "semantic_contracts": SEMANTIC_CONTRACTS,
                                "section_order": SECTIONS, "submitted_summary": case.get("packet_summary"),
                                "evidence_paths": {"source": "/evidence/source", "upstream_artifacts": "/evidence/artifacts", "references": "/evidence/references",
                                                   "reviewed_diff": "/evidence/git/reviewed.diff"},
                                "reviewed_range": {"base_commit": case["base_commit"], "reviewed_commit": case["reviewed_commit"]}}, indent=2))
        viewport = settings.get("viewport", {})
        with PacketBrowser(self.source_root, Path(packet), root / "browser", viewport.get("width", 1280), viewport.get("height", 900), settings.get("browser_executable")) as browser:
            with BrowserServer(browser) as server:
                result = self._execute("judge", prompt, root, settings,
                                       mounts=[{"source": str(workspace), "target": "/workspace/source", "read_only": True},
                                               {"source": case["repository_path"], "target": "/evidence/source", "read_only": True},
                                               {"source": case["artifacts_path"], "target": "/evidence/artifacts", "read_only": True},
                                               {"source": case["references_path"], "target": "/evidence/references", "read_only": True},
                                               {"source": str(derived), "target": "/evidence/git", "read_only": True}],
                                       env={"CULLER_EVAL_BROWSER_URL": server.url, "CULLER_EVAL_BROWSER_TOKEN": server.token},
                                       output_schema=judge_output_schema(rubric))
            report = browser.report()
        report_path = root / "browser/report.json"
        report_path.write_text(json.dumps(report, indent=2) + "\n")
        artifact_paths = [*result.get("artifact_paths", []), *_artifacts(root / "browser")]
        response = _json_response(result.get("output"), artifact_paths)
        response["_runtime"] = {"metadata": result.get("metadata", {}), "browser": report,
                                "assessment_contract_hash": contract_hash,
                                "render_failures": _render_failures(response, report),
                                "artifact_paths": artifact_paths}
        return response

    def propose(self, parent: str, reflective_dataset, trial_root: Path, settings: dict, rubric: dict) -> dict:
        self._check_control_stop()
        root = Path(trial_root) / ("optimizer-" + uuid.uuid4().hex)
        root.mkdir(parents=True, exist_ok=False)
        workspace = root / "workspace"
        workspace.mkdir()
        workspace.chmod(0o777)
        prompt = ("You are the Optimizer. Propose one complete revised Packager directive from the permitted training feedback. "
                  "Preserve every protected requirement. Return JSON with directive and hypothesis. "
                  "The directive contains the complete text; the hypothesis explains the expected quality improvement. "
                  "Return only one valid JSON object. Start with { and end with }. "
                  "Use escaped newlines inside JSON strings. Include no markdown fences, preamble, or commentary.\n\n"
                  + json.dumps({"parent_directive": parent, "training_feedback": reflective_dataset, "rubric": rubric}, indent=2))
        result = self._execute("optimizer", prompt, root, settings,
                               mounts=[{"source": str(workspace), "target": "/workspace/source", "read_only": False}])
        response = _json_response(result.get("output"), result.get("artifact_paths", []))
        if not isinstance(response.get("directive"), str) or not response["directive"].strip() or not isinstance(response.get("hypothesis"), str):
            raise RuntimeErrorWithEvidence("The Optimizer result requires a complete directive and hypothesis.", result.get("artifact_paths", []))
        response["_runtime"] = {"metadata": result.get("metadata", {}), "artifact_paths": result.get("artifact_paths", [])}
        return response

    def assess_edit(self, parent: str, child: str, protected: dict, settings: dict) -> dict:
        from .assessment import scope_judge_output_schema
        root = Path(settings["trial_root"]) / ("scope-judge-" + uuid.uuid4().hex)
        root.mkdir(parents=True, exist_ok=False)
        workspace = root / "workspace"
        workspace.mkdir()
        prompt = ("You are the Judge. Assess whether the proposed directive preserves the protected requirements. "
                  "This assessment concerns edit scope, not packet quality. Return JSON with permitted and reasons. "
                  "The permitted value must be a Boolean. The reasons value must be a list of strings. "
                  "Return only one valid JSON object. Start with { and end with }. "
                  "Include no markdown fences, preamble, or commentary.\n\n"
                  + json.dumps({"parent": parent, "child": child, "protected": protected}, indent=2))
        result = self._execute("scope_judge", prompt, root, settings,
                               mounts=[{"source": str(workspace), "target": "/workspace/source", "read_only": True}],
                               output_schema=scope_judge_output_schema())
        response = _json_response(result.get("output"), result.get("artifact_paths", []))
        if type(response.get("permitted")) is not bool or not isinstance(response.get("reasons"), list) or not all(isinstance(reason, str) for reason in response["reasons"]):
            raise RuntimeErrorWithEvidence("The edit assessment requires a Boolean result and text reasons.", result.get("artifact_paths", []))
        response["_runtime"] = {"metadata": result.get("metadata", {}), "artifact_paths": result.get("artifact_paths", [])}
        return response
