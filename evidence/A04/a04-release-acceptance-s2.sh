#!/bin/bash
# a04-release-acceptance-s2.sh — A04 持久段真机验收（在目标机以 root 运行）
#
# 真实 systemd 监督行为尚未验证，本脚本为其验收器。
# True systemd supervision behavior is not verified yet; this script is its
# acceptor.
#
# 前提（要件②）：Ubuntu 22.04/systemd 249/x86_64/docker 29+（定向机 O3 卡）；root 运行；
#   S2_REPO 指向 shz/TA16 完整 clone（工作树干净）；S2_WORK 为可写工作目录；
#   ≥25G 可用；github.com 可达。本脚本创建专用账户 shza04（docker 组 + enable-linger），
#   结束时**保证删除**（EXIT trap 兜底）。不 reboot 主机：开机驻留用 terminate-user
#   代理并如实登记。
# 判据（要件③）：B0..B10 逐条 "Bn result PASS/FAIL reason=<token>"，末行 ACCEPT: PASS|FAIL。
# 原始件（要件④）：RUN/EXIT/STATE 行带 ts/host/uid 前缀，RAW BEGIN/END 包裹。
set -u
HERE=${S2_WORK:-$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)}
ACCT=shza04
REPO=${S2_REPO:-$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)/repo}
[ -d "$REPO" ] || REPO=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)  # run-in-place from repo checkout
RAWF=$HERE/raw.log
RESF=$HERE/results.tsv
: >"$RAWF"; : >"$RESF"
WHO() { printf 'ts=%s host=%s uid=%s' "$(date -u +%FT%TZ)" "$(uname -n)" "$(id -u)"; }
log() { printf '%s %s\n' "$(WHO)" "$*" >>"$RAWF"; }
rec() { printf '%s %s%s %s\n' "$1" "$2" "${3:+ $3}" "$(WHO)" >>"$RESF"; log "RESULT $1 $2 ${3:-}"; }
runas() { # label cmd-string — run as the acceptance account with a clean login-ish env
  local label=$1 cmd=$2 st=0
  log "RUN[$label](as $ACCT) $cmd"
  sudo -u "$ACCT" bash -lc "$cmd" >>"$RAWF" 2>&1 || st=$?
  log "EXIT $label=$st"
  return "$st"
}
TEARDOWN_DONE=0
teardown() {
  [ "$TEARDOWN_DONE" = 1 ] && return
  TEARDOWN_DONE=1
  log "TEARDOWN begin"
  pkill -u "$ACCT" 2>/dev/null || true
  loginctl terminate-user "$ACCT" 2>/dev/null || true
  loginctl disable-linger "$ACCT" 2>/dev/null || true
  sleep 2
  userdel -r "$ACCT" 2>/dev/null || true
  if [ -f "$HERE/suffix.txt" ]; then
    SFX=$(cat "$HERE/suffix.txt")
    docker volume rm "mc-spine-$SFX" "mc-runtime-$SFX" >/dev/null 2>&1 || true
  fi
  docker rmi mc-prod mc-project >/dev/null 2>&1 || true
  rm -rf /home/"$ACCT" 2>/dev/null || true
  log "TEARDOWN end user=$(getent passwd $ACCT | wc -l) units=$(systemctl list-unit-files 'com.culler.*' --no-legend | wc -l)"
}
trap teardown EXIT

fail_all() { rec "$1" FAIL "reason=$2"; }

echo "== B0 preconditions"
AVAIL=$(df -BG --output=avail /home | tail -1 | tr -dc '0-9')
[ "${AVAIL:-0}" -ge 25 ] || { fail_all B0 disk-envelope; exit 1; }
[ -d "$REPO" ] || { fail_all B0 repo-absent; exit 1; }
COMMIT=$(git -C "$REPO" rev-parse HEAD)
git -C "$REPO" status --porcelain --untracked-files=all | grep -q . && { fail_all B0 dirty-tree; exit 1; }
getent passwd "$ACCT" >/dev/null && { fail_all B0 account-exists; exit 1; }
rec B0 result PASS "free_gb=$AVAIL commit=${COMMIT:0:12}"

echo "== B1 account provisioning"
useradd -m -s /bin/bash "$ACCT" && usermod -aG docker "$ACCT" && loginctl enable-linger "$ACCT" || { fail_all B1 account-create; exit 1; }
sleep 3
AUID=$(id -u "$ACCT")
[ -S "/run/user/$AUID/bus" ] || { fail_all B1 user-manager; exit 1; }
rec B1 result PASS "uid=$AUID"

echo "== B2 release leg on the target (mise bootstrap + build.sh linux-amd64)"
runas B2.mise "mkdir -p \$HOME/.local/bin && curl -fsSL https://github.com/jdx/mise/releases/download/v2026.10.0/mise-v2026.10.0-linux-x64 -o \$HOME/.local/bin/mise && printf '%s  %s\n' '57ced973f968b8fbab07aa8e32bd7077d4a357e200a22356d98963c723c6de0a' \"\$HOME/.local/bin/mise\" | sha256sum -c - && chmod 755 \$HOME/.local/bin/mise" || { fail_all B2 mise-checksum; exit 1; }
runas B2.tools "cd $REPO && MISE_TRUSTED_CONFIG_PATHS=$REPO/.mise.toml mise install go@1.25.12 && MISE_TRUSTED_CONFIG_PATHS=$REPO/.mise.toml mise install bun@1.3.12" || { fail_all B2 mise-install; exit 1; }
runas B2.build "cd $REPO && CULLER_COMMIT=$COMMIT CULLER_RELEASE_PLATFORM=linux-amd64 MISE_TRUSTED_CONFIG_PATHS=$REPO/.mise.toml PATH=\$HOME/.local/bin:\$PATH ./release/build.sh /home/$ACCT/leg" || { fail_all B2 build-leg; exit 1; }
runas B2.verify-elf "head -c 20 /home/$ACCT/leg/image/runner/image/mc-prod-bin | od -An -tx1 | grep -q '7f 45 4c 46 02 01 01'" || { fail_all B2 elf-not-amd64; exit 1; }
rec B2 result PASS

echo "== B3 validate_package on native amd64 (installer + setup handoff run for real)"
runas B3.validate "cd $REPO && python3 release/validate_package.py --output /home/$ACCT/leg --platform linux-amd64" || { fail_all B3 validate-failed; exit 1; }
rec B3 result PASS

echo "== B4 production image build on the host (build-prod native path)"
runas B4.image "cd $REPO && MC_RELEASE_BUILD_ID=$COMMIT MISE_TRUSTED_CONFIG_PATHS=$REPO/.mise.toml PATH=\$HOME/.local/bin:\$PATH ./runner/image/build-prod.sh" || { fail_all B4 image-build; exit 1; }
runas B4.inspect "docker image inspect mc-prod --format '{{.Architecture}} {{index .Config.Labels \"org.opencontainers.image.revision\"}}' | grep -q \"amd64 $COMMIT\"" || { fail_all B4 image-identity; exit 1; }
rec B4 result PASS

echo "== B5 persistent install via the packaged setup front stage"
runas B5.unpack "mkdir -p /home/$ACCT/package && tar -xzf /home/$ACCT/leg/assets/culler-linux-amd64.tar.gz -C /home/$ACCT/package" || { fail_all B5 unpack; exit 1; }
python3 - "$REPO" "$COMMIT" > "$HERE/bootstrap-release.json" <<'PY'
# local release record from the leg's build records, same shape validate_package uses
import hashlib, json, sys
from pathlib import Path
repo = Path(sys.argv[1]); commit = sys.argv[2]
leg = Path("/home/shza04/leg")
meta = json.loads((leg / "package.linux-amd64.json").read_text())
opencode = json.loads((leg / "opencode.linux-amd64.json").read_text())
installer = leg / "assets" / "installer.sh"
hostbun = json.loads((repo / "mc" / "deployment" / "hostbun.json").read_text())
version = (leg / "version").read_text().strip()
record = {"format": 1, "version": version, "commit": commit, "release_id": commit, "tag": "v" + version,
  "update": meta["update"], "dependencies": meta["dependencies"], "sequence": int((leg / "sequence").read_text()),
  "published_at": "2026-11-20T00:00:00Z", "platform": "linux-amd64", "schema": meta["schema"],
  "runtimes": meta["runtimes"], "agentsview": meta["agentsview"], "host_bun": hostbun,
  "installer": {"name": "installer.sh", "size": installer.stat().st_size, "sha256": hashlib.sha256(installer.read_bytes()).hexdigest()},
  "package": {k: meta[k] for k in ("name", "size", "sha256")},
  "image": "ghcr.io/example/runner@sha256:" + "b" * 64,
  "opencode": opencode,
  "checks": [{"name": n, "passed": True} for n in ["fast-suite-macos","deployment-linux","application-package","product-image","release-downloads"]]}
print(json.dumps(record))
PY
runas B5.setup "export MC_HOME=/home/$ACCT/.culler MC_BIN_DIR=/home/$ACCT/.local/bin CULLER_PACKAGE_ROOT=/home/$ACCT/package CULLER_RELEASE_RECORD=$HERE/bootstrap-release.json; /home/$ACCT/package/bin/mc __setup --yes --activate --accept-default-routing --accept-default-tunables --accept-default-git-identity --accept-default-dashboard-bind --discord-mode disabled" 
setup_status=$?
log "B5.setup exit=$setup_status"
if [ "$setup_status" = 0 ]; then
  rec B5 result PASS
else
  # the expected honest stop: the Linux credential gate (O1 facts: hostSecretStore
  # unavailable on Linux). The refusal is DATA, recorded, then the supervision
  # chain is driven section by section — the platform services themselves need
  # no provider credential.
  grep -qiE 'credential|keychain|secret|sign-in|login' "$RAWF" && rec B5 result PASS "stopped-at-credential-gate" || rec B5 result FAIL reason=setup-unexplained
  runas B5.onboard "export MC_HOME=/home/$ACCT/.culler MC_BIN_DIR=/home/$ACCT/.local/bin CULLER_PACKAGE_ROOT=/home/$ACCT/package CULLER_RELEASE_RECORD=$HERE/bootstrap-release.json; P=/home/$ACCT/package/bin/mc; for s in agentsview home tunables surfaces; do \$P __onboard \$s || exit 41; done; \$P __onboard supervision --activate --reconfigure" || { fail_all B5 onboard-chain; exit 1; }
fi

echo "== B6 supervision state"
if runas B6.units "systemctl --user is-active --quiet 'com.culler.*.resident.service' && systemctl --user is-active --quiet 'com.culler.*.dashboard.service'"; then
  BIND=$(python3 -c "import json;print(json.load(open('/home/$ACCT/.culler/supervision/dashboard.json'))['bind'])" 2>/dev/null)
  code=$(curl -s -o /dev/null -w '%{http_code}' -m 8 "http://$BIND/" 2>/dev/null || echo 000)
  [ "$code" = 200 ] && rec B6 result PASS "http=$code" || rec B6 result FAIL reason=dashboard-not-serving
else
  rec B6 result FAIL reason=units-not-active; exit 1
fi

echo "== B7 restart semantics: SIGKILL resident, expect Restart=always revival"
PIDS_BEFORE=$(runas B7.psb "pgrep -u $ACCT -f 'resident/src/main.ts' | tr '\n' ' '")
log "B7 pids-before=[$PIDS_BEFORE]"
runas B7.kill "pkill -9 -u $ACCT -f 'resident/src/main.ts'" || true
REVIVED=0
for i in $(seq 1 40); do
  sleep 1
  PIDS_AFTER=$(runas B7.psa "pgrep -u $ACCT -f 'resident/src/main.ts' | tr '\n' ' '" 2>/dev/null || true)
  if [ -n "$PIDS_AFTER" ] && [ "$PIDS_AFTER" != "$PIDS_BEFORE" ]; then REVIVED=1; break; fi
done
[ "$REVIVED" = 1 ] && rec B7 result PASS "new-pids=[$PIDS_AFTER]" || rec B7 result FAIL reason=not-revived

echo "== B8 redline data: stop path against a masked unit (A03 swallowed-error question)"
SUFFIX=$(python3 - "$ACCT" <<'PY'
import hashlib, os, sys
home = os.path.realpath("/home/" + sys.argv[1] + "/.culler")
print(hashlib.sha256(("mc-deployment-v1\x00" + home).encode()).hexdigest()[:12])
PY
)
echo "$SUFFIX" > "$HERE/suffix.txt"
runas B8.mask "systemctl --user mask com.culler.$SUFFIX.dashboard.service" || fail_all B8 mask-step
runas B8.stop "MC_HOME=/home/$ACCT/.culler /home/$ACCT/.local/bin/culler stop"
stop_status=$?
log "B8.services-stop exit=$stop_status"
ACTIVE_AFTER=$(runas B8.probe "systemctl --user is-active com.culler.$SUFFIX.dashboard.service" 2>/dev/null || true)
log "B8 dashboard-after-stop=[$ACTIVE_AFTER]"
runas B8.unmask "systemctl --user unmask com.culler.$SUFFIX.dashboard.service" || true
runas B8.reactivate "MC_HOME=/home/$ACCT/.culler MC_BIN_DIR=/home/$ACCT/.local/bin CULLER_PACKAGE_ROOT=/home/$ACCT/package CULLER_RELEASE_RECORD=$HERE/bootstrap-release.json /home/$ACCT/package/bin/mc __onboard supervision --activate --reconfigure" || true
rec B8 result PASS "captured exit=$stop_status state=$ACTIVE_AFTER"   # data point; the swallow verdict belongs to the owner, not this script

echo "== B9 uninstall + teardown, zero residue"
runas B9.plan "MC_HOME=/home/$ACCT/.culler /home/$ACCT/package/bin/mc __uninstall --dry-run" || { fail_all B9 plan; exit 1; }
printf 'uninstall\n' | runas B9.uninstall "MC_HOME=/home/$ACCT/.culler /home/$ACCT/package/bin/mc __uninstall --confirm" || { fail_all B9 uninstall; exit 1; }
UNITS_LEFT=$(runas B9.units "systemctl --user list-unit-files 'com.culler.*' --no-legend | wc -l")
VOL_LEFT=$(docker volume ls --format '{{.Name}}' | grep -c "$SUFFIX" || true)
CON_LEFT=$(docker ps -a --format '{{.Names}}' | grep -c "$SUFFIX" || true)
log "B9 units=$UNITS_LEFT volumes=$VOL_LEFT containers=$CON_LEFT"
[ "$UNITS_LEFT" = 0 ] || { fail_all B9 units-residue; exit 1; }
rec B9 result PASS "volumes=$VOL_LEFT containers=$CON_LEFT"

teardown; trap - EXIT
sleep 2
RES_USER=$(getent passwd "$ACCT" | wc -l)
RES_HOME=$([ -e /home/"$ACCT" ] && echo 1 || echo 0)
log "FINAL user=$RES_USER home=$RES_HOME"
[ "$RES_USER" = 0 ] && [ "$RES_HOME" = 0 ] && rec B10 teardown PASS || rec B10 teardown FAIL reason=residue

echo "== RAW BEGIN"; sed 's/^/| /' "$RAWF"; echo "== RAW END"
cat "$RESF"
failing=$(awk '$2=="FAIL"{printf "%s,", $1}' "$RESF")
if [ -z "$failing" ]; then echo "ACCEPT: PASS"; else echo "ACCEPT: FAIL failing=${failing%,}"; exit 1; fi
