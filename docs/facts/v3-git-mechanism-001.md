# V-3 宿主 git 层机制事实卡 #1（2026-11-21，a14-v3-git-mechanism.sh @218，v3-results/raw-218 入档）

范围=§7.3 V-3 的 **git 层半边**（git 2.34.1 行为矩阵，file:// 本地竞技场，零网络）；**产品执行路半边**（native executor 实际 push 环境）确认仍随持久段。**三次仪器迭代留痕**（每步失败都有日志，未洗）：dry-run refspec 空跑→死端口"先连接后鉴权"（2.34 真序）→一次性 401 监听（终态仪器）。

## 矩阵（G1/G2=上游默认事实，G3/G4=产品环境形）
| 行 | 结果 | 事实 |
|---|---|---|
| G1 天真宿主 push | **仓库 pre-push hook 执行**（OBSERVED） | 操作者手敲 git 在恶意仓库上=中招（这正是 A06/culler 面存在的理由，登记） |
| G2 GIT_CONFIG_GLOBAL=/dev/null | 仍执行（OBSERVED） | 全局隔离**不挡 repo 信任源**（预期，钉死） |
| G3 产品环境形（HOME 重定向+GLOBAL=/dev/null+NOSYSTEM+`-c core.hooksPath=空`） | **push 侧零执行**（PASS） | hooksPath 显式重置=对 repo hooks 的有效防线 |
| G4 同环境 + repo-local credential.helper + 401 服务 | **helper 被真实执行**（OBSERVED） | **credential 向量活在 repo config，G3 四件套挡不住它**（repo=git 信任层）|
| G4b | include 在 effective config 可见 | 决策点在查询/注入层，非 include 读取 |

## 呈报（事实+缺口，判定归所有者）
1. **产品代码追链（grep E1）**：全仓无 `credential.helper` 重置、无 `GIT_ASKPASS` 处置——G4 事实 ⇒ **产品 push 环境若沿用 repo 携带 config 的场景，恶意 helper 可在 auth 协商时执行并可读到注入凭据**（网络面缓解=T2 默认拒出/声明域，P2a 未完；darwin 面同样暴露与否取决于 executor env，=持久段确认项）。
2. 标准 git 层解法=命令行 `-c credential.helper=`（**空重置先于平台注入**）+ 可选 `GIT_ASKPASS=/dev/null`——属**交付/Repository 域产品改动**（GIT-DELIVERY.md 域），**不在 A14/A16/A24 任何正清单内，执行侧不顺手改**；立票候选（含 G4 判据自动化）呈所有者处置。
3. V-3 状态=**git 层 OBSERVED/部分闭合；产品路半边+X-1 型活部署确认仍 NOT_STARTED**（持久段）。
