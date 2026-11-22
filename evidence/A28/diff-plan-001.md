# A28 diff-plan #1（先列后改：安装/维护门的停写脊读径——site-2 处置）

**状态=立票+实施同轮**（所有者"批准，继续下一票"；读取声明见 AM 登记轮。超出正清单=STOP-1）。
缺陷形（A27 的 §7.1 追证 + 本轮 Agent 勘察，全部 file:line）：安装/维护/候选四处 `p.database()` 在生产以 **readonly 挂载 + 容器内 `mc __release-database`（活读形 mode=ro）** 读取脊卷；而**服务停后/两 verb 之间的脊是闭合 WAL 文件（无 -shm）——活读形对其必 `CANTOPEN(14)`**（DEFECT-003-v2 四态表），即 site-2 与 site-1 同型红，今日被单测 fake 直喂 JSON 完全掩盖。

## 0. 静置性逐点定类（所有者裁"先追证再定类"的答复）
| 点 | 现行 | 定类 | 证据 |
|---|---|---|---|
| `stopAndRecheck` 内读（setup_published_prod.go:499） | 活读 | **live（维持；本行初判 settled 已被同批追证推翻）** | 该 `stop()` 只停**监督单元**；**agent/Collie 容器不在其停止范围**（容器停止只发生在更新流的 `StopUpdateWork`，update_stop.go:194-295）⇒ 静置**不可证**，且此处的忙答复正是检查目的。登记为**具名残余**（见 §0b） |
| `prepare` update 支读（:638） | 活读 | **settled iff `p.update != nil`** | update 支在 `updatePhase("stop")`/`stopForUpdate` 之后（setup_update_transaction_prod.go:149-153，含容器停止）；install 支无脊卷走 absent 短路（:746-750）不触读 |
| `drain` 内读（:813） | 活读 | **live（维持；初判 settled 同被推翻）** | idle ack 只证 **resident 无 tick**，不证容器侧写入停止 ⇒ 静置不可证。同记 §0b 残余 |
| `checkCandidateProjectImages` 读（setup_update_candidate_prod.go:159） | 活读 | **settled iff `p.update != nil`** | 唯一生产调用点在 beforeUpdate 的 stop 相后（setup_update_transaction_prod.go:167），StopUpdateWork 连容器一起停 |

## 0b. 具名残余（本票**不**处理，理由=静置不可证，非工作量）
安装/维护停止路径（:499 与 :813）在生产仍以活读形打"可能已闭合"的脊 ⇒ **同型红仍在**。其闭合需要一个**新的确定性事实**：先证明"这些路径上无人写脊"（例如把容器停止纳入该 stop，或以 lease/ack 证明无 in-flight verb），属**更新/停止时序设计**，与 **L02 排空闸同源**（site-3 亦归该处）。因此 A28 的实际闭合面=**能证静置的两点**（update 流 :638/:159），其余两点作为在册残余随票行转写。

**新论证（须随票呈报，因它改变 busy 检测语义）**：快照读对闭合文件看到**最后提交态**——run/lease/collie 活跃**行**完整可见 ⇒ `Idle()` 忙判据**保持有效**；守卫拒绝只发生在"有 verb 正写"的瞬窗（本就该 abort 的场景）。回退面=瞬窗内操作者看到的是拒绝而非计数 → 门内把 `snapshot precondition unmet` 映射为既有指导句（"Culler work began before the services stopped…"家族），**不裸抛、不吞因**。

## 1. 正清单（候选文件）
| 文件 | 意图 |
|---|---|
| `mc/verbs/onboard.go` | 新增 `inspectSpineSettled`（**独立复制前导段**，体内逐字含 `substrate.OpenSnapshotReadOnly(spine)`）；`inspectSpineReadOnly` **字节不动**（源守卫 :26/:35/:47 钉其体内必须含 `substrate.OpenReadOnly(` 带括号，函数值传递会破——A27 实证）。全包禁 `mode=ro` 字串（:63-69）→ 新注释不得含之 |
| `mc/verbs/release_database.go` | 新增 `InspectSettledReleaseDatabase`（前导走 inspectSpineSettled，计数连接逐字 `substrate.OpenSnapshotReadOnly`）；`InspectReleaseDatabase` 字节不动 |
| `mc/cmd/mc/setup_published_prod.go` | `database(ctx, settled bool)` 分流 + `settledDatabase`（经 `deployment.StageSpineCopy` 暂存→进程内停读；复用 2a68849 已验形状）；4 调用点按 §0 表传类；瞬窗拒绝映射指导句（§0 末段） |
| `mc/cmd/mc/setup_update_candidate_prod.go` | :159 传 `p.update != nil` + 事由注 |
| `mc/cmd/mc/setup_published_prod_test.go` | 既有 readonly-target 测试传 false 不动其余；迁移 `TestPublishedMaintenanceRequiresMatchingReceiptAndIdleDatabase` fake（staging 支+种子含 active run 行，busy 语义保持；schema→CurrentSchemaID）；重引入 2a68849 两枚 settled 测试（正/残留反控，helper 改可组合不 fatal 他支） |
| `mc/cmd/mc/culler_prod_test.go` | **不改动**（§0b：`stopAndRecheck` 路径维持活读 ⇒ 其 canned fake 语义仍成立；若改 settled 需把五类活工作计数器映射为真实行，属本票范围外） |
| `mc/cmd/mc/setup_update_candidate_prod_test.go` | 迁移 `TestCandidateChecksArchivedImagesWithIsolatedRuntime` fake（staging 支+projects/project_environments 种子行；保留 `check` 脚本计数与 runtime 安全断言原语义） |
| `mc/verbs/spine_read_entry_guard_test.go` | 白名单扩入新落点（onboard/release_database/cmd-mc 各计数以守卫实跑定值）；删"they must not appear here"过时注；植入负控保留 |
| 只读复核不动码 | `mc/substrate/substrate.go`（A27 守卫已在 HEAD）、`mc/deployment/update_checkpoint.go`（StageSpineCopy 已在 HEAD） |

**禁触**：dispatch/SQL/seam/envelope/wire/工作流/`update_checkpoint.go` 语义；`inspectSpineReadOnly`/`InspectReleaseDatabase`/`TestReadOnlyInspectionsOpenThroughTheSharedPath` 与被破 5 测试的**断言语义**（只迁移 fake 的应答面=适配形态，不放宽断言——轮九 Q2 原句约束）。

## 2. 测试义务
- 被破 5 测试**原断言全数保留**、经种子化真读绿（证 settled 路径给出与今日 canned 同等的事实面）；
- **附带收益（计划外但真实）**：迁移后的候选测试其 `ProjectImages` 与 `Empty` 由**真实脊行**经停读门导出（而非 canned JSON）⇒ **该段计数/联表 SQL 首次被测试覆盖**；
- busy 语义：`stopAndRecheck`/`drain` 仍走活读，其既有 busy 断言**原样不动**（§0b 残余的代价如实记）；
- 瞬窗映射测：残留 -wal → 拒绝且操作者句含因；
- 守卫白名单实跑定数 + 植入必红负控维持；
- **车道**：合并前 `mc/check.sh`+`resident/check.sh` 全道 + `update_docker` 复跑（在册必跑族）+ `docker_boundary`。

## 3. 与在册队列的挂点
A27 §7.1 边界段的具名跟进项由本票闭合（site-2）；site-3 仍按所有者裁**归 L02 票面**（本票不碰 redeploy_check_prod.go）；C-A workflow 授权不在本票范围。
