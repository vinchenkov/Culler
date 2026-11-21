#!/bin/bash
# a14-v7-uid-matrix.sh — V-7 类①（跨低权限 UID 读取矩阵）记录器（定向机 root 运行）
#
# 不同低权限 UID 进程对平台敏感文件的读取可达性尚未实测，本脚本为其记录器。
# 作用域纪律（TM RR-5b 分层，越层声明禁止）：只测**类①**（uid 10002=agent 真
# 实身份 + 一个无关低权 uid 3100）对**一次性模拟件**（模式/属主按产品实况复制）
# 的读可达；类②（同 UID 进程隔离）/类③（root/daemon）不在此面；对真实部署文件
# 零触碰。判读语义如实：同属主 0600=可读（这正说明防线的真身=agent 从不被挂
# spine 卷，而非文件模式）。
set -u
HERE=${A14_WORK:?pass A14_WORK (absolute scratch dir)}
ACCT_UID=${A14_OTHER_UID:-3100}
case "$HERE" in /*) ;; *) exit 2;; esac
[ "$HERE" = "/" ] && exit 2
RAWF=$HERE/v7-raw.log; RESF=$HERE/v7-results.tsv
: >"$RAWF"; : >"$RESF"
WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u +%FT%TZ)" "$(uname -n)" "$(id -u)"; }
log() { printf '%s %s\n' "$(WHO)" "$*" >>"$RAWF"; }
rec() { printf '%s %s %s %s\n' "$1" "$2" "$3" "$(WHO)" >>"$RESF"; log "RESULT $1 $2 $3"; }
teardown() {
  [ -d "$HERE/arena" ] && rm -rf "$HERE/arena"
  log "TEARDOWN end"
}
trap teardown EXIT

A=$HERE/arena; install -d -m 755 "$A"
printf 'SECRET-SPINE-VALUE\n' > "$A/spine.db"; chmod 600 "$A/spine.db"; chown 10002:10002 "$A/spine.db"
printf 'SECRET-PROVIDER-ACCESS\n' > "$A/provider-access.json"; chmod 644 "$A/provider-access.json"
printf 'SECRET-RUNLOG\n' > "$A/run.ndjson"; chmod 640 "$A/run.ndjson"; chown 10002:10002 "$A/run.ndjson"
install -d -m 700 "$A/private-out"; printf 'SECRET-ATTACH-OUT\n' > "$A/private-out/att.bin"; chmod 600 "$A/private-out/att.bin"
printf 'SECRET-CONFIG\n' > "$A/config.toml"; chmod 600 "$A/config.toml"
install -d -m 755 "$A/open-share"; printf 'PUBLIC-OK\n' > "$A/open-share/notes.md"

readprobe() { # uid label filename expected(see|refuse) — path passed positionally
  local uid=$1 label=$2 file=$3 expect=$4 out st=0
  out=$(docker run --rm --user "$uid:$uid" --network none -v "$A:/arena:ro" mc-prod \
      /bin/sh -c 'cat "/arena/$1" 2>&1; echo rc=$?' sh "$file" 2>&1); st=$?
  log "OUT[$label uid=$uid file=$file] $out"
  if printf '%s' "$out" | grep -q 'SECRET\|PUBLIC'; then got=see; else got=refuse; fi
  [ "$got" = "$expect" ] && rec "$label" result PASS "got=$got" || rec "$label" result FAIL "reason=got-$got-expected-$expect"
}

echo "== reader uid 10002 (the agent identity)"
readprobe 10002 R1.spine-same-owner spine.db see        # by-design readable AT THE FILE; real defense=never mounted into agent
readprobe 10002 R2.provider-0644 provider-access.json see  # the mode finding row (0644 world-readable per effects.ts)
readprobe 10002 R3.runlog-0640 run.ndjson see
readprobe 10002 R4.private-0700dir private-out/att.bin refuse
readprobe 10002 R5.config-0600 config.toml refuse
readprobe 10002 R6.open-share open-share/notes.md see

echo "== reader uid $ACCT_UID (an unrelated low uid — separates same-uid from mode)"
readprobe "$ACCT_UID" R7.spine-other-uid spine.db refuse
readprobe "$ACCT_UID" R8.provider-other-uid provider-access.json see   # 0644 = readable by ANY uid — key exposure row
readprobe "$ACCT_UID" R9.runlog-other-uid run.ndjson refuse
readprobe "$ACCT_UID" R10.config-other-uid config.toml refuse

echo "== Z table"; cat "$HERE"/v7-results.tsv
echo "== RAW BEGIN"; sed 's/^/| /' "$RAWF"; echo "== RAW END"
failing=$(awk '($2=="FAIL" || $3=="FAIL"){printf "%s,", $1}' "$RESF")
[ -z "$failing" ] && { echo "ACCEPT: PASS (class-one matrix as designed; exposure rows read as MODE facts about replicas — R2/R8 are the provider-access 0644 pattern to submit for owner reading; no claim about classes two/three or real files)"; exit 0; }
echo "ACCEPT: FAIL failing=${failing%,}"; exit 1
