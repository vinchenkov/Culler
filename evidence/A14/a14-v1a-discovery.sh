#!/bin/bash
# a14-v1a-discovery.sh — V-1a 配置发现实测（codex 族；定向机 root 运行）
#
# codex 族的配置发现源与生产掩码的有效性尚未验证，本脚本为其记录器。
# The discovery sources and the production mask for the codex family are not
# verified yet; this script is its recorder.
#
# 定性=**发现表（UNKNOWN→KNOWN），非 T2 政策判定**：V-1b/c（hook/MCP）需要真实
# T2 活会话，结构性继承向导段（A25）与凭据段（A02）；本脚本不冒充该判定，只把
# "每个源是否被读"变成事实。唯一硬断言=**产品掩码正控**（S2）：生产 pipeline 形
# 容器（HOME/.codex 为 tmpfs 掩码）内注入的项目级配置必须读不到——今天可判。
# 正控 S0：显式 CODEX_HOME 指向注入目录必须读到；读不到=仪器失效，fail fast。
# 每个探针是字面 docker 命令行（无通用脚本拼接、无 eval）。
# 前提（要件②）：mc-prod 在位；一次性 scratch；容器全 --rm，trap 兜底。
# 判据（要件③）：measured.tsv 行 + rec；原始件（要件④）ts/host/uid+RAW 包裹。
set -u
HERE=${A14_WORK:?pass A14_WORK (absolute scratch dir)}
case "$HERE" in /*) ;; *) exit 2;; esac
[ "$HERE" = "/" ] && exit 2
RAWF=$HERE/raw.log; RESF=$HERE/results.tsv; DATA=$HERE/measured.tsv
: >"$RAWF"; : >"$RESF"; : >"$DATA"
WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u +%FT%TZ)" "$(uname -n)" "$(id -u)"; }
log() { printf '%s %s\n' "$(WHO)" "$*" >>"$RAWF"; }
rec() { printf '%s %s %s %s\n' "$1" "$2" "$3" "$(WHO)" >>"$RESF"; log "RESULT $1 $2 $3"; }
teardown() {
  docker ps -a --format '{{.Names}}' | grep -q '^a14-cx$' && docker rm -f a14-cx >/dev/null 2>&1
  rm -rf "$HERE/inject" "$HERE/home" 2>/dev/null || true
  log "TEARDOWN end containers=$(docker ps -a --format '{{.Names}}' | grep -c '^a14-' || true)"
}
trap teardown EXIT

install -d -m 755 "$HERE/inject/.codex" "$HERE/home"
printf '%s\n' '[[definitely-broken' > "$HERE/inject/.codex/config.toml"
printf '%s\n' '[[definitely-broken' > "$HERE/inject/config.toml"
chmod -R a+rX "$HERE/inject"

record() { # row exit output — classify whether the injected parse error surfaced
  local row=$1 st=$2 out=$3 tok
  if printf '%s' "$out" | grep -qiE 'definitely-broken|expected|invalid (table|config)|parse'; then tok=read-config
  elif printf '%s' "$out" | grep -qiE 'not logged|logout|login'; then tok=clean
  else tok=indeterminate; fi
  printf '%s\t%s\t%s\n' "$row" "$st" "$tok" >>"$DATA"
  log "TOKEN[$row]=$tok"
}

echo "== S0 instrument positive control (CODEX_HOME explicit)"
out=$(docker run --rm --user 10002:10002 --network none -e CODEX_HOME=/probe \
    -v "$HERE/inject:/probe:ro" mc-prod /bin/sh -c \
    'node /opt/culler-runtime/app/node_modules/@openai/codex/bin/codex.js login status' 2>&1); st=$?
log "OUT[S0 exit=$st] $out"
record S0 "$st" "$out"
[ "$(awk -F'\t' '/^S0/{print $3}' "$DATA")" = read-config ] \
  && rec S0.instrument-check result PASS "control=read" \
  || rec S0.instrument-check result FAIL "reason=control-did-not-read;table-meaningless"

if awk '/S0.instrument-check result FAIL/{found=1} END{exit !found}' "$RESF"; then
  echo "== RAW BEGIN"; sed 's/^/| /' "$RAWF"; echo "== RAW END"; cat "$RESF"
  echo "ACCEPT: FAIL failing=S0"; exit 1
fi

echo "== S1 discovery rows (literal argv each)"
out=$(docker run --rm --user 10002:10002 --network none -e HOME=/home/probe \
    -v "$HERE/inject:/home/probe:ro" mc-prod /bin/sh -c \
    'node /opt/culler-runtime/app/node_modules/@openai/codex/bin/codex.js login status' 2>&1); st=$?
log "OUT[R.home exit=$st] $out"; record R.home "$st" "$out"

out=$(docker run --rm --user 10002:10002 --network none -e HOME=/tmp \
    -v "$HERE/inject:/work:ro" -w /work mc-prod /bin/sh -c \
    'node /opt/culler-runtime/app/node_modules/@openai/codex/bin/codex.js login status' 2>&1); st=$?
log "OUT[R.cwd exit=$st] $out"; record R.cwd "$st" "$out"

out=$(docker run --rm --user 10002:10002 --network none -e HOME=/tmp \
    -v "$HERE/inject:/work:ro" -w /work mc-prod /bin/sh -c \
    'mkdir -p /tmp/sub && cd /tmp/sub && node /opt/culler-runtime/app/node_modules/@openai/codex/bin/codex.js login status' 2>&1); st=$?
log "OUT[R.upscan exit=$st] $out"; record R.upscan "$st" "$out"

out=$(docker run --rm --user 10002:10002 --network none -e HOME=/tmp mc-prod /bin/sh -c \
    'node /opt/culler-runtime/app/node_modules/@openai/codex/bin/codex.js login status' 2>&1); st=$?
log "OUT[R.bare exit=$st] $out"; record R.bare "$st" "$out"

echo "== S2 production mask (hard assertion)"
out=$(docker run --rm --user 10002:10002 --network none \
    -v "$HERE/inject:/home/agent" \
    --tmpfs /home/agent/.codex:rw,uid=10002,gid=10002,mode=0700 \
    -e HOME=/home/agent mc-prod /bin/sh -c \
    'node /opt/culler-runtime/app/node_modules/@openai/codex/bin/codex.js login status' 2>&1); st=$?
log "OUT[S2 exit=$st] $out"
if printf '%s' "$out" | grep -qiE 'definitely-broken|invalid (table|config)|parse'; then
  rec S2.production-mask result FAIL "reason=injected-config-reached-under-mask"
else
  rec S2.production-mask result PASS "mask-holds"
fi

echo "== Z table"
while IFS=$'\t' read -r row st tok; do rec "$row" OBSERVED "exit=$st $tok"; done < "$DATA"
emit() {
  echo "== RAW BEGIN"; sed 's/^/| /' "$RAWF"; echo "== RAW END"; cat "$RESF"
  # Both-column scan (A16 lesson); the single FAIL channel is the mask leak or
  # the dead instrument.
  failing=$(awk '($2=="FAIL" || $3=="FAIL"){printf "%s,", $1}' "$RESF")
  [ -z "$failing" ] && { echo "ACCEPT: OBSERVED mask-holds; failing-none"; exit 0; }
  echo "ACCEPT: FAIL failing=${failing%,}"; exit 1
}
emit
