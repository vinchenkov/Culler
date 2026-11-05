"""Token-free container acceptance for paths, command state, and adapter execution."""

import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile

from culler_evals.runtime import Runtime
from culler_evals.runtime_sources import runtime_source_manifest, runtime_source_hash


parser = argparse.ArgumentParser()
parser.add_argument("--image", required=True)
parser.add_argument("--output", required=True)
args = parser.parse_args()
source = Path(__file__).resolve().parents[2]
root = Path(args.output).resolve()
root.mkdir(parents=True, exist_ok=False)
repository = root / "case/repository"
repository.mkdir(parents=True)


def git(*argv):
    return subprocess.run(["git", "-C", str(repository), *argv], check=True, capture_output=True, text=True).stdout.strip()


git("init", "--quiet")
git("config", "user.name", "Evaluation fixture")
git("config", "user.email", "fixture@example.invalid")
(repository / "example.txt").write_text("Before\n")
git("add", ".")
git("commit", "--quiet", "-m", "Before")
base = git("rev-parse", "HEAD")
(repository / "example.txt").write_text("After\n")
git("commit", "--quiet", "-am", "After")
reviewed = git("rev-parse", "HEAD")
artifacts = root / "case/artifacts"
(artifacts / "eval/701").mkdir(parents=True)
(artifacts / "eval/701/evidence.txt").write_text("Fixed upstream evidence fixture.\n")
case = {"case_id": "runtime-contract-fixture", "task_id": "701", "repository_path": str(repository), "artifacts_path": str(artifacts),
        "base_commit": base, "reviewed_commit": reviewed, "command_responses": {}, "project_skills": [], "readiness": {"ready": True},
        "brief_parts": [{"heading": "Run", "explanation": "This is the current run.",
                         "facts": [{"label": "Run identifier", "value": "{{run_id}}"}]}]}
scope = subprocess.run(["docker", "run", "--rm", "--user", "10002:10002", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
                        "--mount", f"type=bind,src={source / 'evals/bridge'},dst=/app/eval,readonly",
                        "--mount", f"type=bind,src={root / 'case'},dst=/evidence,readonly", args.image,
                        "bun", "/app/eval/tool-scope-smoke.ts"], capture_output=True, text=True, check=True)
(root / "tool-scope.json").write_text(scope.stdout)
manifest = runtime_source_manifest(source)
from culler_evals.product_sources import product_source_manifest

class SyntheticRuntime(Runtime):
    def _docker_execute(self, request, settings, execution_root, mounts, env):
        return super()._docker_execute(request, settings, execution_root, mounts,
                                       {**env, "MC_CODEX_BIN": "/app/eval/test-double-codex"})


with SyntheticRuntime(source, source_manifest=manifest, product_manifest=product_source_manifest(source),
             snapshot_root=root / "runtime-source") as runtime:
    result = runtime.execute_packager(case, "Preserve the repository and submit the fixture packet.", root / "trial",
                                      {"harness": "codex", "model": "fixture-model", "effort": "high", "fast_mode": True, "image": args.image, "timeout_seconds": 60,
                                       "channel": "codex", "base_url": "",
                                       "credentials": {"provider_access": {"version": 1, "harness": "codex", "authMode": "account",
                                         "url": "https://host.docker.internal:12345", "token": "culler_proxy_" + "x" * 43,
                                         "ca": "-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----"}}})
if result["terminal"]["status"] != "packaged" or not Path(result["packet_path"]).is_file():
    raise RuntimeError("The token-free container failed its packet contract.")
if result["metadata"]["evidence_changed"]:
    raise RuntimeError("The token-free container changed its supplied evidence.")
if result["metadata"]["repository_changed"] is not False:
    raise RuntimeError("The token-free container lacks the final repository check.")
if result["metadata"].get("runtime_source_hash") != runtime_source_hash(manifest):
    raise RuntimeError("The token-free container lacks its frozen source identity.")
if (result["metadata"].get("requested_effort") != "high" or result["metadata"].get("configured_effort") != "high"
        or result["metadata"].get("observed_efforts") != ["high"]):
    raise RuntimeError("The token-free container lacks its explicit effort identity.")
if (result["metadata"].get("requested_fast_mode") is not True
        or result["metadata"].get("configured_fast_mode") is not True
        or result["metadata"].get("configured_service_tier") != "fast"):
    raise RuntimeError("The token-free container lacks its fast mode configuration record.")
received = [json.loads(line) for line in Path(result["trace_path"]).read_text().splitlines()
            if json.loads(line).get("type") == "evaluation.prompt_received"]
prompt = (root / "trial/packager/execution/prompt.txt").read_bytes()
if len(received) != 1 or received[0]["sha256"] != hashlib.sha256(prompt).hexdigest() or received[0]["bytes"] != len(prompt):
    raise RuntimeError("The container harness received a different or incomplete role input.")
command_environment = [json.loads(line) for line in Path(result["trace_path"]).read_text().splitlines()
                       if json.loads(line).get("type") == "evaluation.command_environment"]
if len(command_environment) != 1 or command_environment[0]["token_present"] or not command_environment[0]["private_file_present"]:
    raise RuntimeError("The command adapter lacks its private capability after the shell environment excludes tokens.")
if any(Path(item["path"]).name in ("command.json", "eval-command.json", "container.env", "auth.json") for item in result["_runtime"]["artifact_paths"]):
    raise RuntimeError("The runtime exported a private authentication file.")
(root / "result.json").write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps({"check": "container_runtime_contract", "passed": True, "model_calls": 0,
                  "image": args.image, "evidence": str(root / "result.json"), "terminal": result["terminal"]["status"]}))
