# O3-ENVIRONMENT-PROBE-001 · 当前目标机环境事实采集卡（v0.4）

性质：**采集工具**。职责边界（所有者 2026-11-20 裁定）：**本卡只回答"当前开发/目标机真实具备什么"**；它**不是**未来用户部署环境的硬件契约——任何当刻硬件/配置事实（TPM、容量、KMS 在场性）不得外推为用户环境能力，也不得作为产品架构前提；用户环境能力判定归 **O1-b 适用范围评估 + 部署适配层的现场能力探测**。执行侧无法自采（O3=所有者的目标机）；两项合法回传：① 所有者运行探测块并贴回输出（证据级=owner-reported，非执行侧 E1）；② 口头给定（owner-stated）。**回传前相关票面与 U 项一律维持 UNKNOWN/等待，不预设答案。**

**v0.2 修订（依所有者 2026-11-20 安全审查）**：v0.1 的 `git config --list` 系**值输出**，可能携带 http.extraheader 认证头、URL userinfo/token、helper 参数、include 引入的未检配置——"回传前人工检查"不足以防。本版 Git 面改为**机制性采集**：只输出键名/计数/机制类型，**不产生任何配置值**；并修正两处证据越界（headless、日志上限）。采集范围未扩大。

## §1 探测块 v0.2（只读；无写操作；唯一外发=对 github.com 的匿名 GET，目的地固定非配置决定）

```sh
# —— 身份与内核 ——
uname -m                                   # P-arch → A04 / U-1
cat /etc/os-release                        # P-distro → U-1、A03 基线
# —— 监督与会话形态（证据边界见 §2 注记）——
systemctl --version | head -1              # P-systemd → A03 / U-1
systemctl get-default                      # P-default-target → U-3 辅助（graphical vs multi-user）
loginctl list-sessions --no-legend | head  # P-sessions → U-3 辅助（仅会话列表，见边界声明）
# —— 日志上限/保留/轮转（v0.4 三件套【P1-1 采集闭环】；值=容量数字无凭据风险）——
journalctl --show-config 2>/dev/null | grep -Ei 'SystemMaxUse|SystemKeepFree|RuntimeMaxUse|MaxFileSec|Seal|Config'   # ①解析后配置值与来源行
journalctl --disk-usage 2>/dev/null                                   # ②实际磁盘占用
ls /etc/systemd/journald.conf.d/ 2>/dev/null; grep -hE 'SystemMax|RuntimeMax|MaxFileSec|Seal' /etc/systemd/journald.conf /etc/systemd/journald.conf.d/*.conf 2>/dev/null   # ③主文件+drop-in 来源链
# 生效值纪律：①=systemd 解析视图，③=来源链；矛盾或有运行时覆盖可能→owner-stated 补一句裁决；仍不能确认→P-journald-limits 记 UNKNOWN，A16 保持未激活（不拖他票）
# —— 容器运行时 ——
docker version --format '{{.Server.Version}}' 2>/dev/null   # P-docker → A04/A14/A16
stat -fc %T /sys/fs/cgroup                 # P-cgroup → A16（v1/v2）
# （P-tpm 已依所有者 2026-11-20 最终裁定从必需采集项移除：当前机 TPM 存在与否不构成产品
#   架构前提、不阻塞 Linux 开发，亦不得外推为用户环境能力——TPM 归 O1-b 条件性候选与
#   部署适配层能力探测，见 §2 注记）
# —— 资源与容量 ——
df -h                                      # P-capacity → B06/A16
free -h
# —— Git 机制性采集（v0.2：只输出键名与计数，绝不输出值；任何情况下不回传值）——
git --version                              # P-gitver
for SCOPE in --system --global; do
  echo "== git-keys $SCOPE（仅键名） =="
  git config $SCOPE --name-only 2>/dev/null \
    | sed -E 's#^credential\..*#credential.<masked>#'   # 键名可内嵌 URL→统一掩码
done
echo "== git-mechanism-presence（计数，非值） =="
for SCOPE in --system --global; do
  N=$(git config $SCOPE --name-only 2>/dev/null | wc -l | tr -d ' ')
  C=$(git config $SCOPE --name-only 2>/dev/null | grep -c '^credential\.' || true)
  H=$(git config $SCOPE --name-only 2>/dev/null | grep -ci 'extraheader' || true)
  R=$(git config $SCOPE --name-only 2>/dev/null | grep -ci 'insteadof' || true)
  I=$(git config $SCOPE --name-only 2>/dev/null | grep -ci '^include' || true)
  echo "$SCOPE keys=$N credential=$C extraheader=$H urlrewrite=$R include=$I"
done
echo "== credential helper 机制类型（仅首词，无参数） =="
for SCOPE in --system --global; do
  git config $SCOPE --name-only 2>/dev/null \
    | grep '^credential\..*\.helper$\|^credential\.helper$' | while read -r K; do
        V=$(git config $SCOPE --get "$K" 2>/dev/null | awk '{print $1}')
        echo "$SCOPE $K -> $(echo "$V" | sed -E 's#^/.*#<abs-path>#')"   # 绝对路径→掩码
      done
done
# —— 目标仓库 local 面（把 <repo> 换为实际路径；同样仅键名）——
# git -C <repo> config --local --name-only | sed -E 's#^credential\..*#credential.<masked>#'
# —— 远端可达（唯一外发，匿名 GET，目的地硬编码）——
getent hosts github.com                    # P-gh-dns → E 系最低可达事实
curl -s -o /dev/null -w '%{http_code}\n' -m 8 https://github.com   # P-gh-https → E01/E03
```

**脱敏契约（v0.2 新增）**：本块设计为**不生成任何配置值**；若因运行时差异导致输出中出现疑似 token/密码/URL userinfo，**该行不得回传**——宁缺勿泄。KMS/HSM（U-2）无通用探测：owner-stated 回答（"无设施"是合法答案）。

## §2 事实项 → 消费方绑定表（含证据边界声明）

| 项 | 消费方 | 证据边界（v0.2 明示） | 状态 |
|---|---|---|---|
| P-distro/P-systemd/P-arch | A03、A04 启动前置；U-1 | 单次快照=当刻事实；升级后需重采 | 待采 |
| P-default-target + P-sessions | U-3（headless） | **两者皆辅助信号**：get-default=开机形态，sessions=当刻会话；"服务运行主体是否依赖 GUI 登录会话"仍须由**服务 unit 的实际 User=/会话归属**确认——本卡不闭合 U-3，只收窄；剩余由 owner-stated 补 | 待采 |
| P-journald-limits | A16 日志上限面（**CODED 独立准入**，003 附二） | v0.4 三件套=解析配置①+实际占用②+来源链③；三者矛盾或运行时覆盖→owner-stated 裁决；不可确认→UNKNOWN 且 A16 不激活（分组原则） | 待采 |
| P-docker/P-cgroup/P-capacity | A04/A14/A16/A19/B06 | 版本/形态事实；性能包络另测 | 待采 |
| P-git-*（机制性） | U-5；F14b 对象①②的**部分** | **机制存在性≠对象闭合**：实际读取主体、运行 uid、环境变量面、目标仓库 local 全键链、helper 真实调用链仍 UNKNOWN（F14b ①②不因本卡判"已证"）；**对象③目的地约束归 DT/V-3，不由本卡代替**（所有者裁定入册） | 待采 |
| P-gh-* | E 系环境半证 | 可达性事实；**不改变 E01-E05 BLOCKED**（解锁=DT 封闭+触发） | 待采 |
| ~~P-tpm~~（v0.3 移除） | ~~U-9/C2b~~ → **不再由本卡承载** | 所有者最终裁定：TPM 不作当前开发准入；产品核心架构不依赖 TPM；C2b=条件性部署候选，其能力判定归 **O1-b（适用范围评估）+ 部署适配层（真实用户环境能力探测）**；"环境探测用于选择适配方案，不用于降低安全保证"（无 TPM 环境不得自动退化为未经安全批准的明文方案） | 移除 |
| U-2 / U-4 / U-6 / U-7 | O1-b 前置 | owner-stated；可与本卡一并回传 | 待答 |

## §3 回传后的登记动作（预告，不预执行）
① 各项以 owner-reported/owner-stated 级入本卡 §4（新增节）；② 关联 U 项在 o1-credential-facts-001.md §4 改状态并复核阻塞声明（与冻结集冲突时走 §7 重绑条款：定向修订+AM 同轮）；③ A03/A04 的 READY 判定过 blcheck；④ DT/E 系状态不因 O3 数据自动变化（四转换独立）。

## §3.5 实地采集结果（2026-11-20；**执行侧直读 E1**——经所有者定向授权"218.84.111.20 可以做为未来部署的环境，你可以去采集"，执行侧 ssh 实跑只读探测；证据级**高于** owner-reported，归因=agent-run on owner-designated host）

| 事实 ID | 值 | 注记 |
|---|---|---|
| P-arch | **x86_64**，kernel 5.15.0-94-generic | A04 双架构目标坐实（非推定） |
| P-distro | **Ubuntu 22.04.4 LTS**（ID=ubuntu） | U-1 闭合 |
| P-systemd | **systemd 249**(249.11-0ubuntu3.21)；系统态=**degraded** | creds 语义在 249 可用；degraded 归因见下 |
| P-default-target | **graphical.target**（存在 uid1000 GUI 会话 + root 会话） | U-3 当刻形态 |
| P-journald-limits | **无显式限制**：SystemMaxUse/RuntimeMaxUse/MaxFileSec 全为注释默认态，无 drop-in；实际占用 **3.9G**。**槽位法回填完成【签发轮】**：①解析视图=**方法不适配 249**（`journalctl --show-config` 为 systemd 251+ 选项，249 上直接报错——首测空输出系命令失败非"无值"，教训：三件套①须按版本回退到③+默认语义）；②实际占用=3.9G ✓；③来源链=主文件全注释+`/etc/systemd/journald.conf.d` 不存在+`DropInPaths=`空 ✓。①缺席无矛盾（②③一致）→ 判定=**默认策略生效、零显式配置，A16 CODED 准入事实成立** | A16 CODED 准入 ✓（其激活仍等链上 A03/A04——分组门，非事实门） |
| P-docker | **29.4.0** | A04/A16 ✓ |
| P-cgroup | **cgroup2fs**（v2） | A16 实现面 ✓ |
| P-tpm | 无设备 | 按 v0.3 裁定仅当刻事实，**不 gate 任何开发** |
| P-capacity | **磁盘 437G 总/53G 余（88% 已用）；RAM 503Gi** | **A16/B06 容量包络输入=53G 可用，非 437G 总量** |
| P-gitver | git 2.34.1 | — |
| P-git-system / P-git-global | **均全零**：credential=0 extraheader=0 urlrewrite=0 include=0，无 helper | U-5 推进至"OS 级配置为空" |
| P-gh-dns / P-gh-https | 解析 20.6.7.17 / **HTTP 200** | E 系环境半证（BLOCKED 不变） |
| 失败单元 | systemd-networkd-wait-online=failed | degraded 归因；运维卫生事实（与产品无关，照录） |

**诚实边界注记**：① Git 面仅覆盖 system/global——**root 的 env 面与目标仓库 local 面未采**，F14b 对象①②不因此闭合（U-5 仅部分推进）；② graphical.target 不推翻"系统级服务与登录会话无关"的 Linux 语义，但 C1 类口令方案若将来在此机实施，口令通道与 GUI 会话关系=**实施时实测项**（附五族真机项）；③ 磁盘 88% 与失败单元建议所有者顺手处置（非产品义务，仅容量/卫生事实登记）。

**U 项状态推进（O1 册 §4 同步）**：U-1=**KNOWN**；U-3=**KNOWN（当刻形态，重成像不影响系统级结论）**；U-5=**部分 KNOWN**（OS 级空；env/local/实调链保持 UNKNOWN）；U-2/U-4/U-6/U-7 不变（待 owner-stated）。

## §4 修订登记
- v0.1：首版（含 `git config --list` 值输出）。
- v0.2：依所有者安全审查——Git 面机制性化（键名/计数/首词+双重掩码，零值输出）+脱敏契约；headless 两项降级为辅助信号并给出剩余确认路径；补 journald 上限采集（纯容量数字面）；F14b 对象①②的"不闭合"声明与对象③排除入绑定表。范围未扩大。
- **v0.5（签发轮）**：journald 三件套按槽位正式回填（§3.5 行更新）——**关键方法事实：`journalctl --show-config` 为 systemd 251+ 选项，目标机 249 不支持**；槽①按版本回退为"来源链+默认语义"判定，②③一致无矛盾→**默认策略生效=显式零限制**，A16 CODED 准入事实成立（其 READY 仍等链上前置）。CLOSURE-MAP 未解决项 1 就此关闭。
- v0.3：依所有者最终裁定——**P-tpm 移出必需采集**（当前机硬件不是用户环境硬件契约；TPM 不作开发准入、不阻塞 A03/A04/A14/A16/B06、其能力判定归 O1-b+部署适配层）；§2 记录"探测选择适配、不降低保证"原则行；U-9 在 O1 册中同步重定义（部署环境 TPM2 要求/覆盖范围，非当刻设备事实）。
- **v0.4（本态）**：依 003 审查【P1-1】——P-journald-limits 升为 A16 独立 CODED 准入的采集闭环：三件套（解析配置视图①+实际占用②+主文件/drop-in 来源链③）+ 矛盾/运行时覆盖→owner-stated 裁决 + 不可确认→UNKNOWN 且仅冻结 A16（分组不受累原则）。**新增 §3.5：218.84.111.20 实地采集结果（执行侧直读 E1，经所有者定向授权）——U-1/U-3 转 KNOWN、U-5 部分 KNOWN；A03/A04/A16 的 CODED 准入事实组经此判定齐备（明细见 §3.5 注记）。**
