#!/bin/bash
# a14-v1a-claude.sh — V-1a 配置发现实测（claude 族；定向机 root 运行）
#
# claude 族的配置发现源与产品迁移（CLAUDE_CONFIG_DIR / settingSources）的实测
# 行为尚未验证，本脚本为其记录器。定性同 codex 轮：发现表 UNKNOWN→KNOWN，非
# T2 政策判定（政策层=SDK 选项 settingSources:["user"]，其强制属适配器单元面，
# 本件只测 CLI 在各源形状下的读写事实）。每行为完整字面命令行，无变量拼命令。
# 判据（要件③）：C0 正控必须 read（否则 fail fast）；其余行 OBSERVED 入表。
set -u
HERE=${A14_WORK:?pass A14_WORK (absolute scratch dir)}
case "$HERE" in /*) ;; *) exit 2;; esac
[ "$HERE" = "/" ] && exit 2
RAWF=$HERE/claude-raw.log; DATA=$HERE/claude-measured.tsv
: >"$RAWF"; : >"$DATA"
WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u +%FT%TZ)" "$(uname -n)" "$(id -u)"; }
log() { printf '%s %s\n' "$(WHO)" "$*" >>"$RAWF"; }
teardown() { rm -rf "$HERE/cinj" "$HERE/empty" 2>/dev/null || true; log "TEARDOWN end"; }
trap teardown EXIT

install -d -m 777 "$HERE/cinj/.claude" "$HERE/empty"
printf '%s\n' '[[definitely-broken' > "$HERE/cinj/.claude.json"
printf '%s\n' '{"broken": [[[}'     > "$HERE/cinj/.claude/settings.json"
chmod -R a+rwX "$HERE/cinj" "$HERE/empty"

record() { local row=$1 st=$2 out=$3 tok
  if printf '%s' "$out" | grep -qiE 'corrupted|Parse error'; then tok=read-config
  elif printf '%s' "$out" | grep -qiE 'not logged|No value set'; then tok=clean
  else tok=indeterminate; fi
  printf '%s\t%s\t%s\n' "$row" "$st" "$tok" >>"$DATA"; log "TOKEN[$row]=$tok"; }

echo "== C0 instrument: corrupted HOME/.claude.json, plain"
out=$(docker run --rm --user 10002:10002 --network none -e HOME=/c -v "$HERE/cinj:/c:rw" \
    mc-prod /bin/sh -c '/opt/culler-runtime/app/node_modules/@anthropic-ai/claude-agent-sdk-linux-x64/claude config get theme' 2>&1); st=$?
log "OUT[C0 exit=$st] $out"; record C0 "$st" "$out"
[ "$(awk -F'\t' '/^C0/{print $3}' "$DATA")" = read-config ] || { echo "ACCEPT: FAIL failing=C0"; exit 1; }

echo "== C1 product relocation: CLAUDE_CONFIG_DIR at an empty dir"
out=$(docker run --rm --user 10002:10002 --network none -e HOME=/c -e CLAUDE_CONFIG_DIR=/e \
    -v "$HERE/cinj:/c:rw" -v "$HERE/empty:/e:rw" \
    mc-prod /bin/sh -c '/opt/culler-runtime/app/node_modules/@anthropic-ai/claude-agent-sdk-linux-x64/claude config get theme' 2>&1); st=$?
log "OUT[C1 exit=$st] $out"; record C1 "$st" "$out"

echo "== C2 project shape: corrupted cwd/.claude/settings.json, clean HOME"
out=$(docker run --rm --user 10002:10002 --network none -e HOME=/tmp -w /p -v "$HERE/cinj:/p:rw" \
    mc-prod /bin/sh -c '/opt/culler-runtime/app/node_modules/@anthropic-ai/claude-agent-sdk-linux-x64/claude config get theme' 2>&1); st=$?
log "OUT[C2 exit=$st] $out"; record C2 "$st" "$out"

echo "== C3 bare control: nothing mounted"
out=$(docker run --rm --user 10002:10002 --network none -e HOME=/tmp \
    mc-prod /bin/sh -c '/opt/culler-runtime/app/node_modules/@anthropic-ai/claude-agent-sdk-linux-x64/claude config get theme' 2>&1); st=$?
log "OUT[C3 exit=$st] $out"; record C3 "$st" "$out"

echo "== TABLE"; cat "$DATA"
echo "== RAW BEGIN"; sed 's/^/| /' "$RAWF"; echo "== RAW END"
echo "ACCEPT: OBSERVED (C1 answers whether the relocation shields HOME leakage; C2 answers CLI-default project reading; product policy enforcement is the SDK option, recorded separately)"
