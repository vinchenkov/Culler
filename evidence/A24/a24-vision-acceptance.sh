#!/bin/bash
# a24-vision-acceptance.sh — A24 操作者视野门三域矩阵（在定向机以 root 运行）
#
# 操作者视野门的三域行为尚未验证，本脚本为其验收器。
# The three-domain behavior of the operator-vision gate is not verified yet;
# this script is its acceptor.
#
# 用法：A24_STATE=pre（批准前现态钉：三域全部门拒）| post（批准后钉：宿主域
#   无门 token、容器域门 token 逐字保持——两态同一剧本，翻转由批准事件而非
#   改剧本触发）。前提（要件②）：mc-prod 在位（A16 轮产成）；MC_BIN=宿主 mc
#   二进制（绝对路径）；不触碰任何既有部署物：两个临时容器由 trap 删除，
#   HOME/spine 指向一次性目录。
# 判据（要件③）：D{H,HELPER,AGENT}.<verb> 逐行 "… result PASS/FAIL reason=<token>"；
#   ACCEPT: PASS|FAIL。原始件（要件④）：ts/host/uid 前缀 + RAW BEGIN/END。
set -u
HERE=${A24_WORK:-$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)}
STATE=${A24_STATE:?set A24_STATE=pre|post}
MC=${MC_BIN:?pass MC_BIN (an absolute host mc binary path)}
RAWF=$HERE/raw.log; RESF=$HERE/results.tsv
: >"$RAWF"; : >"$RESF"
WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u +%FT%TZ)" "$(uname -n)" "$(id -u)"; }
log() { printf '%s %s\n' "$(WHO)" "$*" >>"$RAWF"; }
rec() { printf '%s %s%s %s\n' "$1" "$2" "${3:+ $3}" "$(WHO)" >>"$RESF"; log "RESULT $1 $2 ${3:-}"; }
SPINE=$HERE/spine.db
teardown() {
  docker rm -f a24-helper >/dev/null 2>&1 || true
  docker rm -f a24-agent  >/dev/null 2>&1 || true
  # Guard the paths: HERE must be an absolute non-root directory this script
  # created artifacts under, or nothing is removed.
  case "$HERE" in /*) [ "$HERE" != "/" ] && rm -rf "$SPINE" "$HERE/run.json" "$HERE/home" 2>/dev/null || true ;; esac
  log "TEARDOWN end containers=$(docker ps -a --format '{{.Names}}' | grep -c '^a24-' || true)"
}
trap teardown EXIT

# Gate tokens observable today (E1 read of the refusal sites).
GATE_TOKENS='require the Culler host|require the Culler Mac host|requires the Culler host|requires the Culler Mac host'

# One probe per fixed verb shape; argv arrays are literals — no eval, no
# dynamic concatenation of paths into command words.
probe() { # domain verb stdin-mode: runs the fixed argv, echoes output
  local domain=$1 verb=$2 out st=0
  case "$domain/$verb" in
    H/project-list)    out=$("$MC" project list </dev/null 2>&1) || st=$? ;;
    H/release-status)  out=$("$MC" __release status </dev/null 2>&1) || st=$? ;;
    H/project-setup)   out=$("$MC" project add-demo </dev/null 2>&1) || st=$? ;;
    H/upload)          out=$("$MC" dashboard upload </dev/null 2>&1) || st=$? ;;
    H/delivery-start)  out=$("$MC" delivery start </dev/null 2>&1) || st=$? ;;
    H/env-save)        out=$("$MC" environment save </dev/null 2>&1) || st=$? ;;
    HELPER/*)          out=$(docker exec a24-helper mc "${ARGV[$VERB_IDX]}" ${ARGV_EXTRA} </dev/null 2>&1) || st=$? ;;
    AGENT/*)           out=$(docker exec a24-agent mc "${ARGV[$VERB_IDX]}" ${ARGV_EXTRA} </dev/null 2>&1) || st=$? ;;
    *) log "probe: unknown shape $domain/$verb"; return 99 ;;
  esac
  log "OUT[$domain.$verb exit=$st] $out"
  printf '%s' "$out"
  return 0
}

assert_gate() { # domain verb output expect-gated:yes|no
  local d=$1 v=$2 out=$3 want=$4
  if [ "$want" = yes ]; then
    printf '%s' "$out" | grep -qE "$GATE_TOKENS" \
      && rec "$d.$v" result PASS "gate-token-present" \
      || rec "$d.$v" result FAIL "reason=gate-token-absent"
  else
    printf '%s' "$out" | grep -qE "$GATE_TOKENS" \
      && rec "$d.$v" result FAIL "reason=gate-token-leaked-into-host" \
      || rec "$d.$v" result PASS "no-gate-token"
  fi
}

echo "== D0 preconditions"
docker image inspect mc-prod >/dev/null 2>&1 || { rec D0 result FAIL "reason=mc-prod-absent"; exit 1; }
[ -x "$MC" ] || { rec D0 result FAIL "reason=mc-bin-missing"; exit 1; }
case "$STATE" in pre|post) ;; *) rec D0 result FAIL "reason=state-invalid"; exit 1;; esac
install -d -m 700 "$HERE/home"
rec D0 result PASS "state=$STATE"
export MC_HOME=$HERE/home MC_SPINE=$SPINE        # throwaway; post host rows are business errors at worst

# The six fixed shapes (index-aligned arrays; the helper/agent exec mirrors the
# same words one at a time).
VERBS=(project-list release-status project-setup upload delivery-start env-save)
ARGV=(project __release project dashboard delivery environment)
ARGV_EXTRA=("list" "status" "add-demo" "upload" "start" "save")
want_for() { # verb -> expected gate presence in this state
  if [ "$STATE" = pre ]; then printf yes; else printf no; fi
}

for i in "${!VERBS[@]}"; do
  v=${VERBS[$i]}; export VERB_IDX=$i ARGV_EXTRA="${ARGV_EXTRA[$i]}"
  out=$(probe H "$v"); assert_gate H "$v" "$out" "$(want_for)"
done

echo "== D HELPER (/.dockerenv, no run.json)"
docker run -d --name a24-helper --entrypoint /bin/sleep mc-prod infinity >>"$RAWF" 2>&1
for i in "${!VERBS[@]}"; do
  v=${VERBS[$i]}; export VERB_IDX=$i ARGV_EXTRA="${ARGV_EXTRA[$i]}"
  out=$(probe HELPER "$v")
  # Container domain asserts the SAME gate truth in both states: it never widens.
  assert_gate HELPER "$v" "$out" yes
done

echo "== D AGENT (run.json present)"
printf '{"run_id":"a24-probe","role":"worker","tier":"pipeline","project_id":"a24","task_id":"t","boot_id":"b","attempt":1,"deadline_s":60}\n' > "$HERE/run.json"
docker run -d --name a24-agent -v "$HERE/run.json:/mc/run.json:ro" --entrypoint /bin/sleep mc-prod infinity >>"$RAWF" 2>&1
for i in "${!VERBS[@]}"; do
  v=${VERBS[$i]}; export VERB_IDX=$i ARGV_EXTRA="${ARGV_EXTRA[$i]}"
  out=$(probe AGENT "$v")
  assert_gate AGENT "$v" "$out" yes
done
# Second-layer witness: a run.json caller of a private host verb must be refused
# regardless of the gate (identity refusal, exit != 0). Asserted only as a
# refusal; the refusal text is recorded as data.
st=0
docker exec a24-agent mc collie start </dev/null >>"$RAWF" 2>&1 || st=$?
if [ "$st" -ne 0 ]; then rec AGENT.collie-start result PASS "refused-exit=$st"
else rec AGENT.collie-start result FAIL "reason=run.json-caller-succeeded"; fi

echo "== Z structural guard (post must not widen any container row)"
leak=$(awk '($1 ~ /^HELPER\./ || $1 ~ /^AGENT\./) && $2=="FAIL"' "$RESF" | wc -l | tr -d ' ')
[ "$leak" = 0 ] && rec Z.no-container-leak result PASS || rec Z.no-container-leak result FAIL "reason=$leak-container-rows-changed"

emit() {
  echo "== RAW BEGIN"; sed 's/^/| /' "$RAWF"; echo "== RAW END"; cat "$RESF"
  # Verdict scans BOTH columns (A16 lesson: a marker needs its own negative
  # proof; the first A16 acceptor passed over failures by reading one column).
  failing=$(awk '($2=="FAIL" || $3=="FAIL"){printf "%s,", $1}' "$RESF")
  rows=$(awk 'END{print NR}' "$RESF")
  if [ -z "$failing" ] && [ "$rows" -ge 23 ]; then echo "ACCEPT: PASS"; exit 0; fi
  echo "ACCEPT: FAIL failing=${failing%,} rows=$rows"; exit 1
}
emit
