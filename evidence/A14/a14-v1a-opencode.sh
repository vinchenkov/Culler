#!/bin/bash
# a14-v1a-opencode.sh — V-1a 配置发现实测（opencode 族；定向机 root 运行）
#
# opencode 族的配置发现源与产品 XDG 迁移/掩码的实测行为尚未验证，本脚本为
# 其记录器。定性同 codex/claude 轮：发现表 UNKNOWN→KNOWN，非 T2 政策判定。
# 事实基线（本会话实地探得，脚本固化复核）：种卷走**产品安装脚本逐字复刻**
# （openCodeInstallScript@mc/deployment/opencode.go，非再发明；diff-plan-004
# 撤回自我更正版在案）；探针子命令=`opencode debug config`（输出带 path 的
# 装载序；判据=MARKER 值被读出，损坏 JSON 不报错而合法标记必显）。
# 行集：O0 种卷（仪器正控，fail-fast）；O1 XDG_CONFIG_HOME 迁移位；O2 HOME 直层；
# O3 项目 .opencode/（cwd=模型可写工作区）；O4 AGENTS.md 指令源；O5 产品
# /etc/opencode tmpfs 掩码形。每行完整字面命令行，无变量拼命令。
set -u
HERE=${A14_WORK:?pass A14_WORK (absolute scratch dir)}
LEG_ARCHIVE=${LEG_ARCHIVE:?opencode archive from the release leg}
A14_RELEASE=${A14_RELEASE:?commit the leg was built at (becomes release-id)}
LEG_SHA=f76b059e2062949486927c470fb329e7f85b1ef766fa5da396eedea4febf02aa
OCV=a14-oc-probe
case "$HERE" in /*) ;; *) exit 2;; esac
[ "$HERE" = "/" ] && exit 2
RAWF=$HERE/oc-raw.log; DATA=$HERE/oc-measured.tsv
: >"$RAWF"; : >"$DATA"
WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u +%FT%TZ)" "$(uname -n)" "$(id -u)"; }
log() { printf '%s %s\n' "$(WHO)" "$*" >>"$RAWF"; }
teardown() { docker volume rm "$OCV" >/dev/null 2>&1 || true; rm -rf "$HERE/ocinj" 2>/dev/null || true; log "TEARDOWN end"; }
trap teardown EXIT

install -d -m 777 "$HERE/ocinj"
printf '{"username":"MARK-XDG"}' > "$HERE/ocinj/opencode.json"
mkdir -p "$HERE/ocinj/xdg/opencode"
printf '{"username":"MARK-XDG"}' > "$HERE/ocinj/xdg/opencode/opencode.json"
mkdir -p "$HERE/ocinj/homeshape/.config/opencode"
printf '{"username":"MARK-HOME"}' > "$HERE/ocinj/homeshape/.config/opencode/opencode.json"
mkdir -p "$HERE/ocinj/proj/.opencode"
printf '{"username":"MARK-PROJ"}' > "$HERE/ocinj/proj/.opencode/opencode.json"
printf 'INSTR-MARK\n' > "$HERE/ocinj/proj/AGENTS.md"
mkdir -p "$HERE/ocinj/etcopencode"
printf '{"username":"MARK-ETC"}' > "$HERE/ocinj/etcopencode/opencode.json"
chmod -R a+rwX "$HERE/ocinj"

echo "== O0 seed the probe volume with the product install script (verbatim)"
docker volume rm "$OCV" >/dev/null 2>&1
docker volume create "$OCV" >/dev/null
out=$(docker run --rm --pids-limit=2048 --network none --user 0:0 \
    --mount type=volume,src=$OCV,dst=/output,volume-nocopy \
    --mount type=bind,src="$LEG_ARCHIVE",dst=/archive.tar.gz,readonly \
    mc-prod /bin/sh -ec 'set -eu
 tar -xzf /archive.tar.gz --no-same-owner -C /output
 chmod -R a+rX /output
 count=$(find /output/node_modules/@opencode -mindepth 3 -maxdepth 3 -path "*/bin/opencode" | wc -l | tr -d "[:space:]")
 [ "$count" = 1 ] || { printf "The archive does not hold exactly one OpenCode program.\n" >&2; exit 1; }
 program=$(find /output/node_modules/@opencode -mindepth 3 -maxdepth 3 -path "*/bin/opencode")
 version=$(XDG_DATA_HOME=/tmp/data XDG_CONFIG_HOME=/tmp/config XDG_CACHE_HOME=/tmp/cache XDG_STATE_HOME=/tmp/state "$program" --version)
 [ "$version" = "opencode v$3" ] || { printf "The archive contains %s. This release requires OpenCode %s.\n" "$version" "$3" >&2; exit 1; }
 printf "%s\n" "$1" > /output/release-id
 printf "%s\n" "$2" > /output/archive-sha256
 chmod a+r /output/release-id /output/archive-sha256' install "$A14_RELEASE" "$LEG_SHA" 2.2.7 2>&1); st=$?
log "OUT[O0 exit=$st] $out"
[ "$st" = 0 ] || { echo "ACCEPT: FAIL failing=O0-seed"; exit 1; }

row() { # row-name marker exit output — MARKER visibility recorded, never guessed
  local r=$1 want=$2 st=$3 out=$4 seen
  if printf '%s' "$out" | grep -q "$want"; then seen=yes; else seen=no; fi
  printf '%s\t%s\t%s\tseen-%s\n' "$r" "$st" "$want" "$seen" >>"$DATA"
  log "ROW[$r exit=$st want=$want seen=$seen]"
}

echo "== O1 XDG_CONFIG_HOME relocation (production per-run shape: adapter points XDG_* at a private dir)"
# The private dir must be a writable bind: the container root filesystem is
# not writable by uid 10002 (first pass proved this with a cp failure, not a
# discovery result).
out=$(docker run --rm --user 10002:10002 --network none -v "$OCV":/oc:ro -v "$HERE/ocinj/xdg:/w:rw" -e HOME=/tmp \
    mc-prod /bin/sh -c 'XDG_DATA_HOME=/tmp/d XDG_CACHE_HOME=/tmp/c XDG_STATE_HOME=/tmp/s XDG_CONFIG_HOME=/w timeout 20 /oc/node_modules/@opencode/cli-linux-x64/bin/opencode debug config' 2>&1); st=$?
log "OUT[O1 exit=$st] $out"; row O1.xdg-live MARK-XDG "$st" "$out"

echo "== O2 HOME-shaped config (~/.config/opencode under HOME)"
out=$(docker run --rm --user 10002:10002 --network none -v "$OCV":/oc:ro \
    -v "$HERE/ocinj/homeshape:/h:ro" -e HOME=/h \
    mc-prod /bin/sh -c 'XDG_DATA_HOME=/tmp/d XDG_CACHE_HOME=/tmp/c XDG_STATE_HOME=/tmp/s XDG_CONFIG_HOME=/tmp/cf timeout 20 /oc/node_modules/@opencode/cli-linux-x64/bin/opencode debug config' 2>&1); st=$?
log "OUT[O2 exit=$st] $out"; row O2.home-source MARK-HOME "$st" "$out"

echo "== O3 project .opencode/ from cwd (model-writable workspace)"
out=$(docker run --rm --user 10002:10002 --network none -v "$OCV":/oc:ro -v "$HERE/ocinj:/inj:ro" -w /inj/proj -e HOME=/tmp \
    mc-prod /bin/sh -c 'XDG_DATA_HOME=/tmp/d XDG_CACHE_HOME=/tmp/c XDG_STATE_HOME=/tmp/s XDG_CONFIG_HOME=/tmp/cf timeout 20 /oc/node_modules/@opencode/cli-linux-x64/bin/opencode debug config' 2>&1); st=$?
log "OUT[O3 exit=$st] $out"; row O3.project-live MARK-PROJ "$st" "$out"

echo "== O4 AGENTS.md instruction source at cwd"
out=$(docker run --rm --user 10002:10002 --network none -v "$OCV":/oc:ro -v "$HERE/ocinj:/inj:ro" -w /inj/proj -e HOME=/tmp \
    mc-prod /bin/sh -c 'XDG_DATA_HOME=/tmp/d XDG_CACHE_HOME=/tmp/c XDG_STATE_HOME=/tmp/s XDG_CONFIG_HOME=/tmp/cf timeout 20 /oc/node_modules/@opencode/cli-linux-x64/bin/opencode debug config' 2>&1); st=$?
log "OUT[O4 exit=$st] $out"; row O4.agents-md INSTR-MARK "$st" "$out"

echo "== O5 product /etc/opencode mask shape"
# First prove the CLI would read the global /etc path at all (5a: bind the
# poisoned dir there, no mask). Only if 5a sees the mark does the mask row
# (5b) mean anything — an instrument check on each side of the curtain.
out=$(docker run --rm --user 10002:10002 --network none -v "$OCV":/oc:ro \
    -v "$HERE/ocinj/etcopencode:/etc/opencode:ro" -e HOME=/tmp \
    mc-prod /bin/sh -c 'XDG_DATA_HOME=/tmp/d XDG_CACHE_HOME=/tmp/c XDG_STATE_HOME=/tmp/s XDG_CONFIG_HOME=/tmp/cf timeout 20 /oc/node_modules/@opencode/cli-linux-x64/bin/opencode debug config' 2>&1); st=$?
log "OUT[O5a exit=$st] $out"; row O5a.global-reads MARK-ETC "$st" "$out"
out=$(docker run --rm --user 10002:10002 --network none -v "$OCV":/oc:ro \
    --tmpfs /etc/opencode:ro,nosuid,nodev,noexec,mode=0555 -e HOME=/tmp \
    mc-prod /bin/sh -c 'XDG_DATA_HOME=/tmp/d XDG_CACHE_HOME=/tmp/c XDG_STATE_HOME=/tmp/s XDG_CONFIG_HOME=/tmp/cf timeout 20 /oc/node_modules/@opencode/cli-linux-x64/bin/opencode debug config' 2>&1); st=$?
log "OUT[O5b exit=$st] $out"; row O5b.mask-covers MARK-ETC "$st" "$out"

echo "== TABLE"; cat "$DATA"
echo "== RAW BEGIN"; sed 's/^/| /' "$RAWF"; echo "== RAW END"
echo "ACCEPT: OBSERVED (opencode discovery table complete; policy reading of O3/O4 (live project-scope sources under model-writable cwd) submitted to the owner — the adapter isolation for this family is XDG relocation + volume readonly, project config disable is NOT established here)"
