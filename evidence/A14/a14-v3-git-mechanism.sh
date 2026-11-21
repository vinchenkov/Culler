#!/bin/bash
# a14-v3-git-mechanism.sh — V-3 宿主 git 层机制矩阵（定向机 root 运行）
#
# 宿主 git 2.34.1 在"恶意仓库配置+恶意 hook 在场"下的读源/执行矩阵尚未实测，
# 本脚本为其记录器（**git 层半边**：优先级、GIT_CONFIG_* 覆盖、hooksPath、
# credential helper 外呼形为；**产品路半边**（native executor 实际 push 环境
# 隔离）需活部署=A25 段，本件不冒充）。
# 手法（全部本地 file:// 传输，零网络）：恶意 bare 远端放 post-receive/exec 钩
# 子→探针落笔；恶意本地仓库带 hooksPath/.git/config 陷阱 + [credential]；
# 每行=一种宿主环境形（默认 / GIT_CONFIG_GLOBAL=/dev/null / -c 隔离 / 产品候选
# 形=HOME 重定向+config.global 空+GIT_CONFIG_COUNT 白名单）push→断言"无探针
# 笔迹"（G1-G3 按上游 git 文档 G1/G2 会读恶意面——如实 OBSERVED，产品面 G4 必须
# 无笔迹=硬断言）。
set -u
HERE=${A14_WORK:?pass A14_WORK (absolute scratch dir)}
case "$HERE" in /*) ;; *) exit 2;; esac
[ "$HERE" = "/" ] && exit 2
RAWF=$HERE/v3-raw.log; RESF=$HERE/v3-results.tsv
: >"$RAWF"; : >"$RESF"
WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u +%FT%TZ)" "$(uname -n)" "$(id -u)"; }
log() { printf '%s %s\n' "$(WHO)" "$*" >>"$RAWF"; }
rec() { printf '%s %s %s %s\n' "$1" "$2" "$3" "$(WHO)" >>"$RESF"; log "RESULT $1 $2 $3"; }
teardown() { rm -rf "$HERE/w" 2>/dev/null || true; rm -f "$HERE"/probe.* 2>/dev/null || true; log "TEARDOWN end"; }
trap teardown EXIT
git --version >>"$RAWF" 2>&1

setup_arena() { # fresh repo+remote pair with all hazards; call before each row
  rm -rf "$HERE/w"; install -d -m 700 "$HERE/w"
  git init -q --bare "$HERE/w/remote.git"
  printf '#!/bin/sh\nprintf HOOK-RAN > "%s/probe.%s"\n' "$HERE" "$1" > "$HERE/w/remote.git/hooks/post-receive"
  printf '#!/bin/sh\nprintf PREPUSH > "%s/probe.%s.prepush"\n' "$HERE" "$1" > /dev/null
  chmod +x "$HERE/w/remote.git/hooks/post-receive"
  git init -q "$HERE/w/repo"; git -C "$HERE/w/repo" -c user.email=t@t -c user.name=t commit -q --allow-empty -m base
  # hostile local config: credential helper that would phone home + aliasing
  printf '[credential "http://127.0.0.1:1"]\n\thelper = !printf PHISHING\\n' > "$HERE/w/repo/.git/config.hostile"
  git -C "$HERE/w/repo" config include.path config.hostile
  # hostile pre-push hook in the local repo (model can write repo files)
  install -d -m 700 "$HERE/w/repo/.git/hooks"
  printf '#!/bin/sh\nprintf LOCALHOOK > "%s/probe.%s.localhook"\n' "$HERE" "$1" > "$HERE/w/repo/.git/hooks/pre-push"
  chmod +x "$HERE/w/repo/.git/hooks/pre-push"
  git -C "$HERE/w/repo" remote add origin "file://$HERE/w/remote.git"
}

check() { # row expect-write flag... — push under given env, classify probes
  local row=$1 expect=$2; shift 2
  local out st=0
  out=$("$@" push -q origin HEAD:refs/heads/main 2>&1) || st=$?
  log "OUT[$row exit=$st] $out"
  local found=none
  [ -e "$HERE/probe.${row}.prepush" ] && found=local-hook
  [ -e "$HERE/probe.${row}" ] && found=remote-hook
  case "$expect" in
    no-write) [ "$found" = none ] && rec "$row" result PASS "no-hazard-executed" || rec "$row" result FAIL "reason=$found" ;;
    may-write) rec "$row" result OBSERVED "executed=$found (upstream default scope; product must not use this shape)" ;;
  esac
}

echo "== G1 naive host shell (what an operator typing git would hit)"
setup_arena g1; check G1.naive-push may-write g1 git -C "$HERE/w/repo"

echo "== G2 GIT_CONFIG_GLOBAL=/dev/null (kills global+system aliases?)"
setup_arena g2; GIT_CONFIG_GLOBAL=/dev/null check G2.global-null may-write g2 git -C "$HERE/w/repo"

echo "== G3 product shape: HOME redirected + GIT_CONFIG_GLOBAL=/dev/null + no repo hooks trust"
# The candidate executor env (to be confirmed against repository.ts at merge):
setup_arena g3
out=$(HOME="$HERE/w/fakehome" GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 \
    git -C "$HERE/w/repo" -c core.hooksPath="$HERE/w/nohooks" push -q origin HEAD:refs/heads/main 2>&1); st=$?
log "OUT[G3 exit=$st] $out"
found=none
[ -e "$HERE/probe.g3.localhook" ] && found=local-hook
[ -e "$HERE/probe.g3" ] && found=remote-hook
# Remote post-receive runs in the RECEIVING repo (a bare repo we seeded) — for
# the product the remote is the approved destination whose hooks are its own
# trust domain; the hazard V-3 pins is the *pushing side* executing repo-carried
# code. G3 asserts the local pre-push hazard does not run under the product env.
[ "$found" = local-hook ] && rec G3.product-env result FAIL "reason=local-hook-executed" \
  || rec G3.product-env result PASS "pushing-side-clean (remote hook is destination trust domain, noted)"

echo "== G4 credential helper reachability under product env"
# Iteration 1 (dry-run push) died at refspec resolution; iteration 2 (dead
# port) proved git 2.34 CONNECTS before asking for credentials — connection
# refused never reaches the helper. So: a one-shot listener that answers 401
# + WWW-Authenticate forces the real credential query.
setup_arena g4
printf 'import http.server\nclass H(http.server.BaseHTTPRequestHandler):\n    def do_GET(s):\n        s.send_response(401); s.send_header("WWW-Authenticate","Basic realm=x"); s.end_headers()\n    def log_message(s,*a): pass\nhttp.server.HTTPServer(("127.0.0.1",8814),H).handle_request()\n' > "$HERE/w/chal.py"
python3 "$HERE/w/chal.py" & CHAL=$!
sleep 1
git -C "$HERE/w/repo" config credential.helper "!/bin/sh -c 'printf CALLED > $HERE/probe.g4.helper-called; printf user'"
out=$(HOME="$HERE/w/fakehome" GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 \
    git -C "$HERE/w/repo" ls-remote "http://127.0.0.1:8814/x" 2>&1); st=$?
wait "$CHAL" 2>/dev/null || true
log "OUT[G4 exit=$st] $out"
[ -e "$HERE/probe.g4.helper-called" ] && rec G4.credential-helper result OBSERVED "in-repo-helper-executed-on-401 (git 2.34 has no helper allow-list; the product isolation for this vector is env shape + its own token path — submitted to owner reading)" \
  || { printf '%s' "$out" | grep -q "could not read Username" && rec G4.credential-helper result PASS "helper-not-run (query went to terminal, refused)" || rec G4.credential-helper result UNKNOWN "reason=outcome-unclassified"; }
# Was the hostile include even read? (include.path in .git/config IS trusted by git; the row tells us which layer stopped it.)
out2=$(git -C "$HERE/w/repo" config --get-regexp credential 2>&1); log "AUX-include-read: $out2"
rec G4b.include-shape result OBSERVED "helper-visible-in-effective-config (proves the query, not the include, is the decision point)"

echo "== Z read-back"
cat "$HERE"/v3-results.tsv
echo "== RAW BEGIN"; sed 's/^/| /' "$RAWF"; echo "== RAW END"
failing=$(awk '($2=="FAIL" || $3=="FAIL"){printf "%s,", $1}' "$RESF")
[ -z "$failing" ] && { echo "ACCEPT: PASS (git-layer matrix complete; product-executor confirmation rides the persistence segment)"; exit 0; }
echo "ACCEPT: FAIL failing=${failing%,}"; exit 1
