# O3-ENVIRONMENT-PROBE-001 · O3 生产目标环境事实采集卡（所有者触发"启动 O3 环境事实"的执行件）

性质：**采集工具**。执行侧无法自采（O3=所有者的目标机）；本卡列出每项事实的**用途绑定**（哪些票/哪些 U 项消费它）与**获取方式**。两种合法回传：① 所有者在目标机运行探测块并贴回原始输出（执行侧按"所有者提供之工具输出"登记，证据级=owner-reported，非执行侧 E1）；② 所有者口头给定（登记为 owner-stated）。**在回传之前，相关票面与 U 项一律维持 UNKNOWN/等待，不预设任何答案。**

## §1 探测块（一次性粘贴执行；只读，无写操作，无外发）

```sh
# —— 身份与内核 ——
uname -a                                   # P-arch → A04 双架构闸 / U-1
cat /etc/os-release                        # P-distro → U-1（发行版+版本），A03 systemd 语义基线
# —— 监督与登录态 ——
systemctl --version | head -2              # P-systemd → A03（systemd 监督替换的可行性与版本语义）
loginctl list-sessions --no-legend | head  # P-sessions → U-3（headless 程度：有无 GUI 登录会话）
systemd-analyze get-log-target 2>/dev/null # P-logtarget → A16（日志上限强制面）
# —— 容器运行时 ——
docker version --format '{{.Server.Version}}' 2>/dev/null   # P-docker → A04/A14/A16（容器边界/Linux lane）
stat -fc %T /sys/fs/cgroup                 # P-cgroup → A16（v1/v2=资源限制实现面）
ls -l /dev/tpm* 2>/dev/null                # P-tpm → U-9（C2b 可行性；无输出=无设备，也是答案）
# —— 资源与容量 ——
df -h; free -h                             # P-capacity → B06 容量包络 / A16 配额基线
# —— Git 面 ——
git --version                              # P-gitver → F14b 对象②基线
git config --system --list 2>/dev/null     # P-git-system → U-5（系统级凭据配置）
git config --global --list 2>/dev/null     # P-git-global → U-5（credential.helper 实态；输出可能含用户名——回传前人工检查，属个人配置面）
# —— 网络与凭设施 ——
getent hosts github.com                    # P-gh-dns → E 系（远端可达性最低事实）
curl -s -o /dev/null -w '%{http_code}' -m 8 https://github.com  # P-gh-https → E01/E03；此为本卡唯一外发（只读 GET，无凭据）
# KMS/HSM：U-2 为形态问题，无通用命令——所有者按实作答（无=合法答案）
```

## §2 事实项 → 消费方绑定表

| 项 | 消费方 | 回传前状态 |
|---|---|---|
| P-distro / P-systemd / P-arch | A03、A04 的**启动前置**；U-1 | UNKNOWN → 不推断（"不从候选材料反推环境"，所有者令在案） |
| P-sessions | U-3；C0 存续与 C1 口令通道结论的可行性判定 | UNKNOWN |
| P-tpm | U-9；C2b 的可行性结论（无 TPM=结论"不可行"也是有效产出） | UNKNOWN |
| P-docker / P-cgroup | A14/A16/A19 与 Linux docker lane；B06 | UNKNOWN |
| P-capacity | B06 包络基线；A16 配额定值 | UNKNOWN |
| P-git-* | U-5；F14b 对象①②（③目的地约束仍走 DT/V-3 验收面） | UNKNOWN |
| P-gh-* | E01-E05 的环境半证（远端可达+无凭据可达）；**不改变 E 系 BLOCKED**（其解锁位=DT 封闭+触发） | UNKNOWN |
| U-2（KMS）/ U-4（宿主数与迁移频率）/ U-6、U-7（要求值） | O1-b 前置 | owner-stated 待答 |

## §3 回传后的登记动作（预告，不预执行）
① 各项按 owner-reported/owner-stated 入本卡 §4（新增节）；② 与之关联的 U 项在 o1-credential-facts-001.md §4 表内改状态并复核阻塞声明（**冻结集的变更纪律=§7 重绑条款**：新证据与冻结集冲突时定向修订+AM 同轮）；③ A03/A04 的 READY 判定过 blcheck；④ DT/E 系状态**不因 O3 数据自动变化**（独立转换原则）。
