#!/bin/bash
# a24-vision-acceptance.sh v2 — A24 操作者视野门三域矩阵（定向机 root 运行）
#
# 操作者视野门的三域行为尚未验证，本脚本为其验收器。
# The three-domain behavior of the operator-vision gate is not verified yet;
# this script is its acceptor.
#
# 设计（v2，按 pre 实测重写——v1 的"六动词通用门 token"假设被 pre 轮证伪：
# 门为 false 时多数命令根本不到 broker 站点，落到结构/用法拒）。**数据优先+
# 基线比对**：pre 轮只记表（唯一断言=AGENT 域零 exit-0 的实测不变量），产物
# a24-baseline.tsv；post 轮带 A24_BASELINE 断言四条：
#   V1 H.release-status 门 token 必须消失；
#   V2 H.delivery-start / H.env-save 行必须离开基线（进入后端语义）；
#   V3 AGENT 全行必须逐字等于基线（放宽渗漏硬闸）；
#   V4 HELPER 全行如实记录、不预设——helper 域 post 语义是批准包待裁项。
# 每个探针是一行字面 argv（无 eval、无变量拼命令）。前提（要件②）：mc-prod
# 在位；MC_BIN=linux/amd64 宿主 mc（生产构建）；临时容器 trap 删除；一次性
# HOME/spine。判据（要件③）与原始件（要件④）同规；verdict 双列扫描（A16 教训）。
set -u
HERE=${A24_WORK:?pass A24_WORK (an absolute scratch dir)}
STATE=${A24_STATE:?set A24_STATE=pre|post}
MC=${MC_BIN:?pass MC_BIN (absolute host mc binary path)}
BASE=${A24_BASELINE:-}
case "$HERE" in /*) ;; *) printf 'A24_WORK must be absolute\n' >&2; exit 2;; esac
[ "$HERE" = "/" ] && { printf 'refusing root\n' >&2; exit 2; }
[ "$STATE" = post ] && [ -z "$BASE" ] && { printf 'post needs A24_BASELINE\n' >&2; exit 2; }
RAWF=$HERE/raw.log; RESF=$HERE/results.tsv; DATA=$HERE/measured.tsv
: >"$RAWF"; : >"$RESF"; : >"$DATA"
WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u +%FT%TZ)" "$(uname -n)" "$(id -u)"; }
log() { printf '%s %s\n' "$(WHO)" "$*" >>"$RAWF"; }
rec() { printf '%s %s%s %s\n' "$1" "$2" "${3:+ $3}" "$(WHO)" >>"$RESF"; log "RESULT $1 $2 ${3:-}"; }
teardown() {
  docker rm -f a24-helper >/dev/null 2>&1 || true
  docker rm -f a24-agent  >/dev/null 2>&1 || true
  # Only paths under the guarded absolute non-root HERE are removed, each one
  # a literal child name this script created.
  rm -rf "$HERE/spine.db" "$HERE/run.json" "$HERE/home" 2>/dev/null || true
  log "TEARDOWN end containers=$(docker ps -a --format '{{.Names}}' | grep -c '^a24-' || true)"
}
trap teardown EXIT

# probe <domain> <verb> <stdin-body> — fixed literal argv per case; records a
# DATA row (domain.verb <TAB> exit <TAB> first output line) and a RAW line.
probe() {
  local d=$1 v=$2 body=$3 out st=0
  case "$d/$v" in
    H/project-list)     out=$(printf '%s' "$body" | "$MC" project list 2>&1) || st=$? ;;
    H/release-status)   out=$(printf '%s' "$body" | "$MC" __release status 2>&1) || st=$? ;;
    H/delivery-start)   out=$(printf '%s' "$body" | "$MC" delivery start --request - 2>&1) || st=$? ;;
    H/env-save)         out=$(printf '%s' "$body" | "$MC" environment save 2>&1) || st=$? ;;
    H/upload)           out=$(printf '%s' "$body" | "$MC" dashboard upload 2>&1) || st=$? ;;
    H/task-list)        out=$(printf '%s' "$body" | "$MC" task list 2>&1) || st=$? ;;
    HELPER/project-list)    out=$(printf '%s' "$body" | docker exec -i a24-helper mc project list 2>&1) || st=$? ;;
    HELPER/release-status)  out=$(printf '%s' "$body" | docker exec -i a24-helper mc __release status 2>&1) || st=$? ;;
    HELPER/delivery-start)  out=$(printf '%s' "$body" | docker exec -i a24-helper mc delivery start --request - 2>&1) || st=$? ;;
    HELPER/env-save)        out=$(printf '%s' "$body" | docker exec -i a24-helper mc environment save 2>&1) || st=$? ;;
    HELPER/upload)          out=$(printf '%s' "$body" | docker exec -i a24-helper mc dashboard upload 2>&1) || st=$? ;;
    HELPER/task-list)       out=$(printf '%s' "$body" | docker exec -i a24-helper mc task list 2>&1) || st=$? ;;
    AGENT/project-list)     out=$(printf '%s' "$body" | docker exec -i a24-agent mc project list 2>&1) || st=$? ;;
    AGENT/release-status)   out=$(printf '%s' "$body" | docker exec -i a24-agent mc __release status 2>&1) || st=$? ;;
    AGENT/delivery-start)   out=$(printf '%s' "$body" | docker exec -i a24-agent mc delivery start --request - 2>&1) || st=$? ;;
    AGENT/env-save)         out=$(printf '%s' "$body" | docker exec -i a24-agent mc environment save 2>&1) || st=$? ;;
    AGENT/upload)           out=$(printf '%s' "$body" | docker exec -i a24-agent mc dashboard upload 2>&1) || st=$? ;;
    AGENT/task-list)        out=$(printf '%s' "$body" | docker exec -i a24-agent mc task list 2>&1) || st=$? ;;
    *) log "probe: unknown shape $d/$v"; return 99 ;;
  esac
  printf '%s\t%s\t%s\n' "$d.$v" "$st" "$(printf '%s' "$out" | head -1 | tr '\t' ' ')" >>"$DATA"
  log "OUT[$d.$v exit=$st] $out"
  return 0
}

echo "== D0 preconditions"
docker image inspect mc-prod >/dev/null 2>&1 || { rec D0 result FAIL "reason=mc-prod-absent"; exit 1; }
[ -x "$MC" ] || { rec D0 result FAIL "reason=mc-bin-missing"; exit 1; }
install -d -m 700 "$HERE/home"
printf '{"run_id":"a24-probe","role":"worker","tier":"pipeline","project_id":"a24","task_id":"t","boot_id":"b","attempt":1,"deadline_s":60}\n' > "$HERE/run.json"
export MC_HOME=$HERE/home MC_SPINE=$HERE/spine.db   # throwaway; post host rows are business errors at worst
rec D0 result PASS "state=$STATE baseline=${BASE:+set}"

# Containers: the agent binds the pre-written envelope file.
docker run -d --name a24-helper --entrypoint /bin/sleep mc-prod infinity >>"$RAWF" 2>&1
docker run -d --name a24-agent -v "$HERE/run.json:/mc/run.json:ro" --entrypoint /bin/sleep mc-prod infinity >>"$RAWF" 2>&1

for v in project-list release-status delivery-start env-save upload task-list; do
  probe H "$v" '{}'
  probe HELPER "$v" '{}'
  probe AGENT "$v" '{}'
done

log "DATA BEGIN"; sed 's/^/| /' "$DATA" >>"$RAWF"; log "DATA END"

if [ "$STATE" = pre ]; then
  # The one invariant the pre round actually observed: the agent domain never
  # succeeds on a host verb (identity refusal or structural refusal).
  zero=$(awk -F'\t' '$1 ~ /^AGENT\./ && $2=="0"' "$DATA" | wc -l | tr -d ' ')
  [ "$zero" = 0 ] && rec I.agent-zero-exit result PASS || rec I.agent-zero-exit result FAIL "reason=agent-host-verb-succeeded"
  cp "$DATA" "$HERE/a24-baseline.tsv"
  rec P.baseline result PASS "file=$HERE/a24-baseline.tsv"
  echo "== BASELINE (hand to the post round as A24_BASELINE)"; cat "$HERE/a24-baseline.tsv"
else
  host_rel=$(awk -F'\t' '$1=="H.release-status"{print $3}' "$DATA")
  printf '%s' "$host_rel" | grep -q "require the Culler host" \
    && rec V1.host-release-opened result FAIL "reason=gate-still-refuses-host" \
    || rec V1.host-release-opened result PASS
  moved=0
  for k in H.delivery-start H.env-save; do
    now=$(awk -F'\t' -v k="$k" '$1==k{print $2 FS $3}' "$DATA")
    was=$(awk -F'\t' -v k="$k" '$1==k{print $2 FS $3}' "$BASE")
    [ "$now" != "$was" ] && moved=$((moved+1))
  done
  [ "$moved" -eq 2 ] && rec V2.host-rows-moved result PASS || rec V2.host-rows-moved result FAIL "reason=moved=$moved"
  diff <(grep -E '^AGENT\.' "$DATA") <(grep -E '^AGENT\.' "$BASE") >/dev/null 2>&1 \
    && rec V3.agent-invariant result PASS || rec V3.agent-invariant result FAIL "reason=agent-rows-changed"
  rec V4.helper-recorded result PASS "compare-baseline-per-approval-wording"
fi

emit() {
  echo "== RAW BEGIN"; sed 's/^/| /' "$RAWF"; echo "== RAW END"; cat "$RESF"
  failing=$(awk '($2=="FAIL" || $3=="FAIL"){printf "%s,", $1}' "$RESF")
  rows=$(awk 'END{print NR}' "$RESF")
  if [ -z "$failing" ] && [ "$rows" -ge 3 ]; then echo "ACCEPT: PASS"; exit 0; fi
  echo "ACCEPT: FAIL failing=${failing%,} rows=$rows"; exit 1
}
emit
