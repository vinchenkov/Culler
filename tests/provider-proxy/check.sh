#!/usr/bin/env bash
# Controlled host acceptance. This suite makes no real model calls.
set -euo pipefail
cd "$(dirname "$0")/../.."
source_root=$PWD
evidence=${1:-}
if [[ $# != 1 || "$evidence" != /* ]]; then
  echo 'Usage: tests/provider-proxy/check.sh <absolute-evidence-directory>' >&2
  exit 2
fi
if [[ $(uname -s) != Darwin ]]; then
  echo 'UNVERIFIED: the complete host suite requires macOS and Docker Desktop.' >&2
  exit 2
fi
mkdir -p "$evidence"
chmod 700 "$evidence"
evidence=$(cd "$evidence" && pwd -P)
for tool in mise docker python3 openssl git; do
  command -v "$tool" >/dev/null || { echo "UNVERIFIED: required host tool $tool is unavailable." >&2; exit 2; }
done
docker info >/dev/null
mise exec -- go version > "$evidence/toolchain.txt"
mise exec -- bun --version >> "$evidence/toolchain.txt"
unset OPENAI_API_KEY ANTHROPIC_API_KEY CODEX_API_KEY ANTHROPIC_AUTH_TOKEN CLAUDE_CODE_OAUTH_TOKEN
unset MC_HOME MC_HELPER MC_SPINE MC_RUN_JSON CULLER_TEST_HOST_SECRET_DIR
run_id=$(python3 -c 'import secrets; print(secrets.token_hex(6))')
real_image="culler-proxy-acceptance-${run_id}"
fake_image="culler-proxy-control-${run_id}"
cleanup() {
  docker image rm "$real_image" "${real_image}-project" "$fake_image" "${fake_image}-project" > "$evidence/image-cleanup.log" 2>&1 || true
}
trap cleanup EXIT
python3 - "$evidence/source.json" <<'PY'
import hashlib, json, os, pathlib, subprocess, sys
revision = subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
files = subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard','-z']).split(b'\0')
digest = hashlib.sha256()
for name in sorted(set(files)):
    if not name: continue
    path = pathlib.Path(os.fsdecode(name))
    if not path.is_file(): continue
    digest.update(name + b'\0')
    digest.update(path.read_bytes())
pathlib.Path(sys.argv[1]).write_text(json.dumps({'revision':revision,'base_revision':subprocess.check_output(['git','merge-base','HEAD','origin/main'],text=True).strip(),'source_sha256':digest.hexdigest(),'platform':sys.platform},indent=2)+'\n')
PY
run() {
  local name=$1
  shift
  python3 - "$evidence/commands.jsonl" "$name" "$@" <<'PYCOMMAND'
import json, sys
with open(sys.argv[1], 'a') as output:
    output.write(json.dumps({'phase':sys.argv[2],'argv':sys.argv[3:]})+'\n')
PYCOMMAND
  echo "START $name"
  if "$@" > "$evidence/$name.log" 2>&1; then
    echo "PASS $name"
  else
    echo "FAIL $name: $evidence/$name.log" >&2
    tail -30 "$evidence/$name.log" >&2
    return 1
  fi
}
prepare_dependencies() {
  (cd runner/openwiki && mise exec -- bun install --frozen-lockfile --no-save)
  (cd runner/agent-runner && mise exec -- bun install --frozen-lockfile)
  (cd dashboard && mise exec -- bun install --frozen-lockfile && mise exec -- bun x playwright install chromium)
}
run dependencies prepare_dependencies
run build-real env MC_TEST_IMAGE="$real_image" ./runner/image/build.sh --real
run build-control env MC_TEST_IMAGE="$fake_image" ./runner/image/build.sh
docker image inspect --format '{{.Id}}' "$real_image" "$fake_image" > "$evidence/images.txt"
for component in mc runner/fake-harness runner/agent-runner runner/homie-runner runner/setup-runner runner/shared runner/image resident dashboard; do
  run "${component//\//-}" "./$component/check.sh"
done
go_group() {
  local name=$1 tag=$2 package=$3 selector=$4
  (
    cd mc
    MC_BOUNDARY_IMAGE="$real_image" MC_BOUNDARY_FAKE_IMAGE="$fake_image" \
      MC_E2E_IMAGE="$fake_image" MC_E2E_PROJECT_IMAGE="${fake_image}-project" MC_E2E_REAL_IMAGE="$real_image" \
      mise exec -- go test -json -tags "$tag" -count=1 -timeout 30m "$package" -run "$selector" > "$evidence/$name.jsonl"
  )
  python3 - "$evidence/$name.jsonl" <<'PY'
import json, sys
rows=[json.loads(line) for line in open(sys.argv[1])]
passed=[row for row in rows if row.get('Action')=='pass' and row.get('Test')]
skipped=[row for row in rows if row.get('Action')=='skip' and row.get('Test')]
if not passed or skipped:
    raise SystemExit('Required tests are absent or unverified: '+', '.join(row['Test'] for row in skipped))
print('Passed',len(passed),'test results.')
PY
}
run docker-boundary go_group docker-boundary docker_boundary ./boundarydocker/... '.'
run scribe-native mise exec -- bun tests/provider-proxy/scribe-native.ts --image "$real_image"
run resident-container go_group resident-container docker_e2e ./e2e/... '^TestCredentialProjection|^TestHomieNativeProxyDockerBoundary$'
run provider-failure-repair env CULLER_PROXY_TEST_IMAGE="$real_image" MC_SMOKE_REPORT="$evidence/provider-failure-repair.xml" MC_SMOKE_EVIDENCE_DIR="$evidence/provider-failure-repair" ./dashboard/smoke.sh ../tests/provider-proxy/failure-repair.test.ts ../tests/provider-proxy/failure-origin.test.ts ../tests/provider-proxy/account-origin.test.ts ../tests/provider-proxy/homie-stream.test.ts
run dashboard-browser env MC_SMOKE_REPORT="$evidence/dashboard.xml" MC_SMOKE_EVIDENCE_DIR="$evidence/browser" ./dashboard/smoke.sh
printf '%s\n' 'The controlled host suite passed. Real provider checks use tests/live-model/.'
