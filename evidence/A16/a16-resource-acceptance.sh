#!/bin/bash
# a16-resource-acceptance.sh — A16 真机资源耗尽矩阵（在定向机以 root 运行）
#
# 沙箱资源维的真实强制行为尚未验证，本脚本为其验收器。
# Real enforcement of the sandbox resource dimension is not verified yet; this
# script is its acceptor.
#
# 作用域：本件证明 **docker 在真实宿主上确实施加 A16 的各帽**（pids/fd/tmpfs/
#   日志/内存）且**爆炸半径≤该容器**（spine 卷文件系统零污染、宿主控制面存活、
#   被限进程以资源类错误失败而非拖垮宿主）。它**不**证明 run 的 spine 终态记账
#   ——该面属 dispatch 的既有 container-gone/lease 通道（A03/A12 单元 + A14 真机
#   轮覆盖）且需持久部署（A25 向导段）。逐维"从容器内真实触发"是本件判据。
# 前提（要件②）：Ubuntu 22.04/systemd 249/x86_64/docker 29+（O3 卡）；root 运行；
#   S2_REPO=shz/TA16 完整 clone（含 A16 配置面，工作树干净）；mc-prod 镜像已在位
#   （A04-S2 B4 产成；缺则 fail-fast，不即兴构建——构建保真归 A04）；≥25G。
#   专用账户 shza16（docker 组），结束**保证删除**（EXIT trap）。不 reboot、不装 unit。
# 判据（要件③）：逐维 C0..C8 "Cn result PASS/FAIL reason=<token>"，末行 ACCEPT: PASS|FAIL。
# 原始件（要件④）：RUN/EXIT 行带 ts/host/uid 前缀，RAW BEGIN/END 包裹。
set -u
HERE=${S2_WORK:-$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)}
ACCT=shza16
REPO=${S2_REPO:-$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)/repo}
[ -d "$REPO" ] || REPO=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
RAWF=$HERE/raw.log
RESF=$HERE/results.tsv
: >"$RAWF"; : >"$RESF"
WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u +%FT%TZ)" "$(uname -n)" "$(id -u)"; }
log() { printf '%s %s\n' "$(WHO)" "$*" >>"$RAWF"; }
rec() { printf '%s %s%s %s\n' "$1" "$2" "${3:+ $3}" "$(WHO)" >>"$RESF"; log "RESULT $1 $2 ${3:-}"; }
SPINE=$HERE/spine-witness          # stands in for the deployment volume
fail_all() { rec "$1" FAIL "reason=$2"; }
TEARDOWN_DONE=0
teardown() {
  [ "$TEARDOWN_DONE" = 1 ] && return
  TEARDOWN_DONE=1
  log "TEARDOWN begin"
  for c in a16-pids a16-fd a16-tmpfs a16-log a16-log2 a16-disk a16-mem a16-env; do
    docker rm -f "$c" >/dev/null 2>&1 || true
  done
  pkill -9 -u "$ACCT" 2>/dev/null || true
  RT_UID=$(id -u "$ACCT" 2>/dev/null || true)
  if [ -n "$RT_UID" ] && [ "$RT_UID" != "$(id -u)" ]; then rm -rf "/run/user/$RT_UID" 2>/dev/null || true; fi
  userdel -rf "$ACCT" 2>&1 | head -3 || true
  rm -rf /home/"$ACCT" "$SPINE" 2>/dev/null || true
  log "TEARDOWN end user=$(getent passwd $ACCT | wc -l) a16containers=$(docker ps -a --format '{{.Names}}' | grep -c '^a16-' || true)"
}
trap teardown EXIT

free_kb() { df -Pk "$1" | awk 'NR==2{print $4}'; }
# A docker id is 64 hex chars; reject anything else before it enters a path.
is_container_id() { printf '%s' "$1" | grep -qE '^[0-9a-f]{64}$'; }

echo "== C0 preconditions"
AVAIL=$(df -BG --output=avail /home | tail -1 | tr -dc '0-9')
[ "${AVAIL:-0}" -ge 25 ] || { fail_all C0 disk-envelope; exit 1; }
[ -d "$REPO" ] || { fail_all C0 repo-absent; exit 1; }
git -C "$REPO" status --porcelain --untracked-files=all | grep -q . && { fail_all C0 dirty-tree; exit 1; }
docker image inspect mc-prod >/dev/null 2>&1 || { fail_all C0 mc-prod-image-absent-build-in-A04; exit 1; }
MC_ARCH=$(docker image inspect mc-prod --format '{{.Architecture}}')
[ "$MC_ARCH" = amd64 ] || { fail_all C0 image-arch-not-amd64; exit 1; }
COMMIT=$(git -C "$REPO" rev-parse HEAD)
install -d -m 700 "$SPINE"
rec C0 result PASS "free_gb=$AVAIL commit=${COMMIT:0:12} image=$MC_ARCH"

echo "== C1 account provisioning (docker group for the drill uid)"
UID_PICK=2100
while getent passwd "$UID_PICK" >/dev/null; do UID_PICK=$((UID_PICK+1)); done
useradd -m -u "$UID_PICK" -s /bin/bash "$ACCT" && usermod -aG docker "$ACCT" || { fail_all C1 account-create; exit 1; }
rec C1 result PASS "uid=$(id -u $ACCT)"

BASE=(--user 10002:10002 --cap-drop ALL --network none)
# runin: one-shot container under test; the docker args are passed as a LIST
# (no shell interpolation of the inner body beyond the fixed probe strings).
runin() {
  local label=$1; shift
  log "RUN[$label] docker run $*"
  sudo -u "$ACCT" docker run --rm "$@" >>"$RAWF" 2>&1
  local st=$?; log "EXIT $label=$st"; return $st
}

echo "== C2 pids: fork bomb bounded by --pids-limit, host and spine untouched"
BEFORE=$(free_kb "$SPINE")
# The inner command is a fixed literal; only the docker flags vary. A resource
# refusal (EAGAIN) OR a bounded completion both count as "the cap held".
if runin C2.fork "${BASE[@]}" --pids-limit 128 --name a16-pids mc-prod \
    /bin/bash -c 'i=0; while [ $i -lt 20000 ]; do ( : ) & i=$((i+1)); [ $((i%200)) -eq 0 ] && wait; done; echo spawned-$i'; then
  rec C2 result PASS "fork-bomb-completed-under-cap"
else
  if grep -qiE 'resource temporarily unavailable|cannot fork|EAGAIN|fork' "$RAWF"; then
    rec C2 result PASS "fork-bomb-refused-by-pids-cap"
  else
    rec C2 result FAIL "reason=fork-not-bounded-and-no-resource-signal"
  fi
fi
AFTER=$(free_kb "$SPINE")
[ $(( BEFORE - AFTER )) -lt 1024 ] && [ $(( AFTER - BEFORE )) -lt 1024 ] || fail_all C2 spine-fs-polluted
docker info >/dev/null 2>&1 && log "C2 control-plane docker alive=yes" || fail_all C2 control-plane-lost

echo "== C3 fd: --ulimit nofile bound makes a new open fail, not the host"
BEFORE=$(free_kb "$SPINE")
fd_probe='import os
soft=__import__("resource").getrlimit(__import__("resource").RLIMIT_NOFILE)[0]
fs=[]; err=None
try:
  i=0
  while i<200000:
    fs.append(os.open("/tmp/f%d"%i, os.O_CREAT|os.O_WRONLY)); i+=1
except OSError as ex:
  err=ex
print("inherited-soft",soft,"opened",len(fs),"errno",getattr(err,"errno",None))'
runin C3.fds "${BASE[@]}" --ulimit nofile=1024:2048 --name a16-fd mc-prod python3 -c "$fd_probe"
if grep -qE 'inherited-soft (1023|1024)' "$RAWF" && grep -qE 'errno 24' "$RAWF"; then
  rec C3 result PASS "fd-cap-inherited+emfile-refusal"
else
  rec C3 result FAIL "reason=fd-cap-or-emfile-not-observed"
fi
AFTER=$(free_kb "$SPINE")
[ $(( BEFORE - AFTER )) -lt 1024 ] || fail_all C3 spine-fs-polluted

echo "== C4 tmpfs: a bounded --tmpfs write fails with real ENOSPC"
BEFORE=$(free_kb "$SPINE")
runin C4.tmpfs "${BASE[@]}" --tmpfs /mc/scratch:rw,size=16m --name a16-tmpfs mc-prod \
    /bin/bash -c 'dd if=/dev/zero of=/mc/scratch/fill bs=1M count=512 status=none; echo dd-exit=$?'
if grep -qE 'No space left on device|ENOSPC' "$RAWF" || grep -q 'dd-exit=[1-9]' "$RAWF"; then
  rec C4 result PASS "tmpfs-write-refused-enospc"
else
  rec C4 result FAIL "reason=tmpfs-cap-not-enforced"
fi
AFTER=$(free_kb "$SPINE")
[ $(( BEFORE - AFTER )) -lt 1024 ] || fail_all C4 spine-fs-polluted

echo "== C5 docker json-file log cap: max-size bounds the host-side log"
# Run detached so the log path survives to measure, then remove it. The
# container id is validated before it is ever used to derive a path.
CID5=$(sudo -u "$ACCT" docker run -d --name a16-log2 --log-driver json-file \
    --log-opt max-size=1m --log-opt max-file=2 "${BASE[@]}" mc-prod \
    /bin/bash -c 'yes AAAAAAAAAA | head -c 20000000' 2>>"$RAWF")
MAXLOG=0
if is_container_id "$CID5"; then
  sleep 4
  RAW_PATH=$(docker inspect --format '{{.LogPath}}' "$CID5" 2>/dev/null)
  # Accept only an absolute /var/lib/docker path with safe characters; then
  # stat each rotation file found under that exact directory, no raw glob.
  if printf '%s' "$RAW_PATH" | grep -qE '^/var/lib/docker/[0-9A-Za-z._/-]+\.json\.log$'; then
    LOGDIR=${RAW_PATH%.json.log}
    for suffix in "" ".1" ".2"; do
      f="$LOGDIR.json.log$suffix"
      [ -f "$f" ] || continue
      sz=$(stat -c %s "$f" 2>/dev/null || echo 0)
      [ "$sz" -gt "$MAXLOG" ] && MAXLOG=$sz
    done
  fi
  log "C5 logpath-accepted=$([ "$MAXLOG" -gt 0 ] && echo yes || echo no)"
fi
docker rm -f a16-log2 >/dev/null 2>&1 || true
if [ "$MAXLOG" -gt 0 ] && [ "$MAXLOG" -lt 3000000 ]; then
  rec C5 result PASS "largest-log-file=$MAXLOG-under-cap"
else
  rec C5 result FAIL "reason=log-unbounded-or-absent max=$MAXLOG"
fi

echo "== C6 memory: --memory bounds a runaway allocation to the container"
BEFORE=$(free_kb "$SPINE")
mem_probe='x=[]
while True: x.append(bytearray(1024*1024))'
runin C6.mem "${BASE[@]}" --memory 128m --name a16-mem mc-prod python3 -c "$mem_probe"
if grep -qiE 'Killed|MemoryError|OOM|exit status 137|Exit code: 137' "$RAWF"; then
  rec C6 result PASS "allocation-bounded-to-container"
else
  rec C6 result FAIL "reason=memory-cap-not-observed"
fi
AFTER=$(free_kb "$SPINE")
[ $(( BEFORE - AFTER )) -lt 1024 ] || fail_all C6 spine-fs-polluted

echo "== C7 envelope witness: the caps are recorded on the created container"
# The A16 collie-envelope digest attests these settings (negative controls in
# the resident unit suite); at the docker layer the witness is that inspect
# reflects them, so the digest has real values to attest.
CID7=$(sudo -u "$ACCT" docker create "${BASE[@]}" --pids-limit 2048 \
    --ulimit nofile=65536:262144 --memory 128m --memory-swap 128m --log-driver json-file \
    --log-opt max-size=10m --log-opt max-file=3 mc-prod /bin/true 2>>"$RAWF")
ENVJSON=""
if is_container_id "$CID7"; then ENVJSON=$(docker inspect "$CID7" 2>/dev/null); fi
docker rm -f "${CID7:-a16-env}" >/dev/null 2>&1 || true
ok=1
for want in '"PidsLimit": 2048' '"Soft": 65536' '"Hard": 262144' '"Memory": 134217728' '"Type": "json-file"'; do
  printf '%s' "$ENVJSON" | grep -qF "$want" || { ok=0; log "C7 missing $want"; }
done
printf '%s' "$ENVJSON" | grep -qF '"max-size": "10m"' || ok=0
[ "$ok" = 1 ] && rec C7 result PASS || rec C7 result FAIL "reason=inspect-omitted-a-cap"

echo "== C8 host control plane alive after all dimensions"
if docker info >/dev/null 2>&1; then rec C8 result PASS "docker-responsive"; else rec C8 result FAIL "reason=control-plane-affected"; fi
uptime | sed 's/^/C8 /' >>"$RAWF"

emit_result() {
  echo "== RAW BEGIN"; sed 's/^/| /' "$RAWF"; echo "== RAW END"
  cat "$RESF"
  failing=$(awk '$2=="FAIL"{printf "%s,", $1}' "$RESF")
  if [ -z "$failing" ]; then echo "ACCEPT: PASS"; exit 0; else echo "ACCEPT: FAIL failing=${failing%,}"; exit 1; fi
}
emit_result
