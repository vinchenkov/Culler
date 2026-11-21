#!/bin/bash
# a14-v1d-ordering.sh — V-1d 启动顺序实证（定向机 root 运行）
#
# 安全配置（tmpfs/剥离/白名单）先于任何不可信配置加载生效，尚未从首进程视角
# 实证，本脚本为其记录器。
#
# 命题：容器内**第一个进程**（=真实 runner 所居的 exec 域）观察到的装配状态即
# 时序证明——docker 在 exec 之前完成挂载/帽/降权（namespaces/cgroup 语义），
# 因此"任何仓库文件被读之前"这些防线已存在。首进程一次性 dump：
#   T1 /proc/mounts 的 tmpfs 行 = HOME 掩码目录（生产 projectHomeArguments 形）；
#   T2 CapEff=0…0（--cap-drop ALL 实值）+ NoNewPrivs=1；T3 uid/gid=10002；
#   T4 /.dockerenv 在场 + /mc/run.json 缺席（H1）/在场（H2 agent 形）；
#   T5 运行树可写性实况；T6 PATH 无 harness CLI（与事实卡一致）。
# 每行 PASS/FAIL 明确 token。全字面命令行，无变量拼命令。
set -u
HERE=${A14_WORK:?pass A14_WORK (absolute scratch dir)}
case "$HERE" in /*) ;; *) exit 2;; esac
[ "$HERE" = "/" ] && exit 2
RAWF=$HERE/v1d-raw.log; RESF=$HERE/v1d-results.tsv
: >"$RAWF"; : >"$RESF"
WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u +%FT%TZ)" "$(uname -n)" "$(id -u)"; }
log() { printf '%s %s\n' "$(WHO)" "$*" >>"$RAWF"; }
rec() { printf '%s %s %s %s\n' "$1" "$2" "$3" "$(WHO)" >>"$RESF"; log "RESULT $1 $2 $3"; }
teardown() { rm -rf "$HERE/home" "$HERE/ws" "$HERE/run.json" 2>/dev/null || true; log "TEARDOWN end"; }
trap teardown EXIT
install -d -m 777 "$HERE/home" "$HERE/ws"
printf '{"run_id":"v1d","role":"worker","tier":"pipeline","project_id":"a14","task_id":"t","boot_id":"b","attempt":1,"deadline_s":60}\n' > "$HERE/run.json"

echo "== H1 pipeline-shape first process"
out=$(docker run --rm --user 10002:10002 --cap-drop ALL --security-opt no-new-privileges=true \
    --pids-limit=2048 --network none \
    -v "$HERE/home:/home/agent" \
    --tmpfs /home/agent/.agents:rw,uid=10002,gid=10002,mode=0700 \
    --tmpfs /home/agent/.claude:rw,uid=10002,gid=10002,mode=0700 \
    --tmpfs /home/agent/.codex:rw,uid=10002,gid=10002,mode=0700 \
    -e HOME=/home/agent -v "$HERE/ws:/workspace/source" -w /workspace/source \
    mc-prod /bin/sh -c 'echo ==T1==; grep -E " /home/agent/\.(agents|claude|codex) " /proc/mounts | awk "{print \$2, \$3}"; echo ==T2==; grep -E "^CapEff|^NoNewPrivs" /proc/self/status; echo ==T3==; id -u; id -g; echo ==T4==; [ -f /.dockerenv ] && echo dockerenv=yes || echo dockerenv=no; [ -e /mc/run.json ] && echo runjson=yes || echo runjson=no; echo ==T5==; (touch /workspace/source/.wtest && echo ws-writable && rm -f /workspace/source/.wtest) 2>/dev/null || echo ws-ro; echo ==T6==; command -v claude codex opencode || echo path-none' 2>&1); st=$?
log "OUT[H1 exit=$st] $out"
t1=$(printf '%s\n' "$out" | awk '/^==T1/{s=1;next}/^==/{s=0}s&&/tmpfs/{c++}END{print c+0}')
[ "$t1" = 3 ] && rec T1.tmpfs-before-exec result PASS "3-mask-mounts" || rec T1.tmpfs-before-exec result FAIL "reason=mount-table=$t1"
printf '%s' "$out" | grep -qE "^CapEff[[:space:]]+0000000000000000" && rec T2.caps result PASS "cap-eff-zero" || rec T2.caps result FAIL "reason=cap-effective"
printf '%s' "$out" | grep -qE "^NoNewPrivs[[:space:]]+1" && rec T2.nnp result PASS || rec T2.nnp result FAIL "reason=no-new-privs-off"
u=$(printf '%s\n' "$out" | awk '/^==T3/{getline a; getline b; print a":"b}')
[ "$u" = "10002:10002" ] && rec T3.uid result PASS || rec T3.uid result FAIL "reason=$u"
printf '%s' "$out" | grep -q "dockerenv=yes" && rec T4.dockerenv result PASS || rec T4.dockerenv result FAIL "reason=missing"
printf '%s' "$out" | grep -q "runjson=no" && rec T4b.runjson-absent-pipeline result PASS "helper-shape-clean" || rec T4b.runjson-absent-pipeline result FAIL "reason=envelope-visible-where-should-not-be"
printf '%s' "$out" | grep -q "ws-writable" && rec T5.workspace result OBSERVED "repo-code CAN write workspace (by design)" || rec T5.workspace result FAIL "reason=unexpected-ro"
printf '%s' "$out" | grep -q "path-none" && rec T6.no-path-cli result PASS "matches fact card" || rec T6.no-path-cli result FAIL "reason=cli-on-path"

echo "== H2 agent-shape: run.json present at first exec"
out=$(docker run --rm --user 10002:10002 --cap-drop ALL --security-opt no-new-privileges=true \
    --network none -v "$HERE/home:/home/agent" \
    --tmpfs /home/agent/.claude:rw,uid=10002,gid=10002,mode=0700 \
    -e HOME=/home/agent -v "$HERE/run.json:/mc/run.json:ro" -v "$HERE/ws:/workspace/source" \
    mc-prod /bin/sh -c '[ -e /mc/run.json ] && echo runjson=yes; grep -cE " /home/agent/\.claude " /proc/mounts' 2>&1); st=$?
log "OUT[H2 exit=$st] $out"
printf '%s' "$out" | grep -q "runjson=yes" && rec T7.runjson-at-exec result PASS "identity-envelope-present-before-any-code" || rec T7.runjson-at-exec result FAIL "reason=absent-at-first-look"

emit() {
  echo "== RAW BEGIN"; sed 's/^/| /' "$RAWF"; echo "== RAW END"; cat "$RESF"
  failing=$(awk '($2=="FAIL" || $3=="FAIL"){printf "%s,", $1}' "$RESF")
  [ -z "$failing" ] && { echo "ACCEPT: PASS"; exit 0; }
  echo "ACCEPT: FAIL failing=${failing%,}"; exit 1
}
emit
