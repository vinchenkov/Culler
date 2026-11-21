#!/bin/bash
# a14-v2-residue.sh — V-2 跨 run 残留实测（定向机 root 运行）
#
# run 间共享面的持久化行为尚未验证，本脚本为其记录器。
# The persistence of surfaces shared between runs is not verified yet; this
# script is its recorder.
#
# 形状（对齐生产装配，environment-lane.projectHomeArguments/CacheArguments）：
#   HOME bind = 项目 home 持久目录；其下 .agents/.claude/.codex 为 per-run tmpfs
#   掩码（runState=true）；/cache = 命名卷（跨 run 共享是**设计**，有字节帽）。
# 探针：run#1 向【HOME 直层】【被掩码目录内】【cache 卷】各写一枚标记；run#2
#   以同 bind/同卷、全新 tmpfs 起，读三处。预期（=被测命题，逐行如实记 OBSERVED
#   而非预设 PASS）：直层=持久（V-2 的真残留面，策略：谁可写它=项目 home 由平台
#   seed，仓库不可写 home——run 内模型可写！这才是本件要呈报的核心事实）；
#   掩码目录=不持久；cache=持久+字节帽属设计。
# 五要件：头句+前提+逐行 token+ts 原始块+trap 自清。
set -u
HERE=${A14_WORK:?pass A14_WORK (absolute scratch dir)}
case "$HERE" in /*) ;; *) exit 2;; esac
[ "$HERE" = "/" ] && exit 2
RAWF=$HERE/v2-raw.log; RESF=$HERE/v2-results.tsv
: >"$RAWF"; : >"$RESF"
WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u +%FT%TZ)" "$(uname -n)" "$(id -u)"; }
log() { printf '%s %s\n' "$(WHO)" "$*" >>"$RAWF"; }
rec() { printf '%s %s %s %s\n' "$1" "$2" "$3" "$(WHO)" >>"$RESF"; log "RESULT $1 $2 $3"; }
VOL=a14v2-cache
teardown() { docker rm -f a14v2-r1 a14v2-r2 >/dev/null 2>&1; docker volume rm "$VOL" >/dev/null 2>&1 || true; rm -rf "$HERE/home" 2>/dev/null || true; log "TEARDOWN end"; }
trap teardown EXIT

install -d -m 777 "$HERE/home"
docker volume rm "$VOL" >/dev/null 2>&1 || true
docker volume create "$VOL" >/dev/null

MASK="--tmpfs /home/agent/.agents:rw,uid=10002,gid=10002,mode=0700 --tmpfs /home/agent/.claude:rw,uid=10002,gid=10002,mode=0700 --tmpfs /home/agent/.codex:rw,uid=10002,gid=10002,mode=0700"

echo "== R1 write marks"
out=$(docker run --rm --name a14v2-r1 --user 10002:10002 --network none \
    -v "$HERE/home:/home/agent" $MASK -e HOME=/home/agent \
    -v "$VOL:/cache" \
    mc-prod /bin/sh -c '
      echo mark-direct > /home/agent/v2-direct.txt
      mkdir -p /home/agent/.claude && echo mark-inside-mask > /home/agent/.claude/v2-settings.json
      echo mark-cache > /cache/v2-cache.txt
      echo wrote' 2>&1); st=$?
log "OUT[R1 exit=$st] $out"
[ "$st" = 0 ] && rec R1.writes result PASS || rec R1.writes result FAIL "reason=writer-run-failed"

echo "== R2 read marks (fresh masks, same bind+volume)"
out=$(docker run --rm --name a14v2-r2 --user 10002:10002 --network none \
    -v "$HERE/home:/home/agent" $MASK -e HOME=/home/agent \
    -v "$VOL:/cache" \
    mc-prod /bin/sh -c '
      for p in /home/agent/v2-direct.txt /home/agent/.claude/v2-settings.json /cache/v2-cache.txt; do
        if [ -f "$p" ]; then echo "PRESENT $p $(cat "$p")"; else echo "ABSENT  $p"; fi
      done' 2>&1); st=$?
log "OUT[R2 exit=$st] $out"
if [ "$st" != 0 ]; then
  rec R2.reads result FAIL "reason=reader-run-failed"
else
  printf '%s' "$out" | grep -q 'PRESENT /home/agent/v2-direct.txt' \
    && rec R2.home-direct-persists result OBSERVED "fact=model-writable-persistent-layer" \
    || rec R2.home-direct-persists result FAIL "reason=expected-persistence-absent"
  printf '%s' "$out" | grep -q 'ABSENT  /home/agent/.claude/v2-settings.json' \
    && rec R2.masked-dirs-clear result PASS "per-run-tmpfs" \
    || rec R2.masked-dirs-clear result FAIL "reason=mask-leaked-across-runs"
  printf '%s' "$out" | grep -q 'PRESENT /cache/v2-cache.txt' \
    && rec R2.cache-persists result OBSERVED "by-design-shared-volume" \
    || rec R2.cache-persists result FAIL "reason=cache-did-not-persist"
fi

echo "== Z read-back"
cat "$HERE/v2-results.tsv"
echo "== RAW BEGIN"; sed 's/^/| /' "$RAWF"; echo "== RAW END"
failing=$(awk '($2=="FAIL" || $3=="FAIL"){printf "%s,", $1}' "$RESF")
[ -z "$failing" ] && { echo "ACCEPT: OBSERVED (V-2 fact table complete; home-direct layer is the policy-bearing persistence surface, submit to owner reading)"; exit 0; }
echo "ACCEPT: FAIL failing=${failing%,}"; exit 1
