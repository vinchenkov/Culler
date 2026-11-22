# A28 diff-plan #1（先列后改：安装/维护门的停写脊读径——site-2 处置）

**状态=立票+实施同轮**（所有者"批准，继续下一票"；读取声明见 AM 登记轮。超出正清单=STOP-1）。
缺陷形（A27 的 §7.1 追证 + 本轮 Agent 勘察，全部 file:line）：安装/维护/候选四处 `p.database()` 在生产以 **readonly 挂载 + 容器内 `mc __release-database`（活读形 mode=ro）** 读取脊卷；而**服务停后/两 verb 之间的脊是闭合 WAL 文件（无 -shm）——活读形对其必 `CANTOPEN(14)`**（DEFECT-003-v2 四态表），即 site-2 与 site-1 同型红，今日被单测 fake 直喂 JSON 完全掩盖。

## 0. 静置性逐点定类（所有者裁"先追证再定类"的答复）
| 点 | 现行 | 定类 | 证据 |
|---|---|---|---|
| `stopAndRecheck` 内读（setup_published_prod.go:499） | 活读 | **settled** | 仅在 `stop(p.home)` 返回成功后触达（:497-499）；install 路径 wrapper `p.update != nil 即返回`（:306-308）不冒充此路；maintenance 入口仅当 resident 曾运行（culler_services_prod.go:286-288） |
| `prepare` update 支读（:638） | 活读 | **settled iff `p.update != nil`** | update 支在 `updatePhase("stop")`/`stopForUpdate` 之后（setup_update_transaction_prod.go:149-153）；install 支无脊卷走 absent 短路（:746-750）不触读 |
| `drain` 内读（:813） | 活读 | **settled** | 在 `ack.Ready`（resident 自证 idle 且 90s 窗内，:809-812）之后 |
| `checkCandidateProjectImages` 读（setup_update_candidate_prod.go:159） | 活读 | **settled iff `p.update != nil`** | 唯一生产调用点在 beforeUpdate 的 stop 段后（setup_update_transaction_prod.go:167） |

**新论证（须随票呈报，因它改变 busy 检测语义）**：快照读对闭合文件看到**最后提交态**——run/lease/collie 活跃**行**完整可见 ⇒ `Idle()` 忙判据**保持有效**；守卫拒绝只发生在"有 verb 正写"的瞬窗（本就该 abort 的场景）。回退面=瞬窗内操作者看到的是拒绝而非计数 → 门内把 `snapshot precondition unmet` 映射为既有指导句（"Culler work began before the services stopped…"家族），**不裸抛、不吞因**。

## 1. 正清单（候选文件）
| 文件 | 意图 |
|---|---|
| `mc/verbs/onboard.go` | 新增 `inspectSpineSettled`（**独立复制前导段**，体内逐字含 `substrate.OpenSnapshotReadOnly(spine)`）；`inspectSpineReadOnly` **字节不动**（源守卫 :26/:35/:47 钉其体内必须含 `substrate.OpenReadOnly(` 带括号，函数值传递会破——A27 实证）。全包禁 `mode=ro` 字串（:63-69）→ 新注释不得含之 |
| `mc/verbs/release_database.go` | 新增 `InspectSettledReleaseDatabase`（前导走 inspectSpineSettled，计数连接逐字 `substrate.OpenSnapshotReadOnly`）；`InspectReleaseDatabase` 字节不动 |
| `mc/cmd/mc/setup_published_prod.go` | `database(ctx, settled bool)` 分流 + `settledDatabase`（经 `deployment.StageSpineCopy` 暂存→进程内停读；复用 2a68849 已验形状）；4 调用点按 §0 表传类；瞬窗拒绝映射指导句（§0 末段） |
| `mc/cmd/mc/setup_update_candidate_prod.go` | :159 传 `p.update != nil` + 事由注 |
| `mc/cmd/mc/setup_published_prod_test.go` | 既有 readonly-target 测试传 false 不动其余；迁移 `TestPublishedMaintenanceRequiresMatchingReceiptAndIdleDatabase` fake（staging 支+种子含 active run 行，busy 语义保持；schema→CurrentSchemaID）；重引入 2a68849 两枚 settled 测试（正/残留反控，helper 改可组合不 fatal 他支） |
| `mc/cmd/mc/culler_prod_test.go` | 迁移 `TestPublishedCancellationRestoresServicesAfterTheStop` 的 fixture fake（同法：staging 支+closed spine 种子+schema 翻正） |
| `mc/cmd/mc/setup_update_candidate_prod_test.go` | 迁移 `TestCandidateChecksArchivedImagesWithIsolatedRuntime` fake（staging 支+projects/project_environments 种子行；保留 `check` 脚本计数与 runtime 安全断言原语义） |
| `mc/verbs/spine_read_entry_guard_test.go` | 白名单扩入新落点（onboard/release_database/cmd-mc 各计数以守卫实跑定值）；删"they must not appear here"过时注；植入负控保留 |
| 只读复核不动码 | `mc/substrate/substrate.go`（A27 守卫已在 HEAD）、`mc/deployment/update_checkpoint.go`（StageSpineCopy 已在 HEAD） |

**禁触**：dispatch/SQL/seam/envelope/wire/工作流/`update_checkpoint.go` 语义；`inspectSpineReadOnly`/`InspectReleaseDatabase`/`TestReadOnlyInspectionsOpenThroughTheSharedPath` 与被破 5 测试的**断言语义**（只迁移 fake 的应答面=适配形态，不放宽断言——轮九 Q2 原句约束）。

## 2. 测试义务
- 被破 5 测试**原断言全数保留**、经种子化真读绿（证 settled 路径给出与今日 canned 同等的事实面）；
- busy 语义回归测：种子含活跃 run 行 → `Idle()=false` 指导句路径可达（防"快照只读空表"假绿）；
- 瞬窗映射测：残留 -wal → 拒绝且操作者句含因；
- 守卫白名单实跑定数 + 植入必红负控维持；
- **车道**：合并前 `mc/check.sh`+`resident/check.sh` 全道 + `update_docker` 复跑（在册必跑族）+ `docker_boundary`。

## 3. 与在册队列的挂点
A27 §7.1 边界段的具名跟进项由本票闭合（site-2）；site-3 仍按所有者裁**归 L02 票面**（本票不碰 redeploy_check_prod.go）；C-A workflow 授权不在本票范围。
