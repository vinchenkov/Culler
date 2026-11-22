# Defect register 005 — Collie 原生代理子界：collie 容器从未 birth（wake 不 fires）；A29 修复后 e2e 道内**唯一剩余红**，与 A29 改动面零交集

状态：**REGISTERED, IN PROGRESS（本登记=事实半，隔离取证在途）**。属登记不修：`collie` 族/`resident` wake 路径不在任何现行正清单。

## 1. 症状（首次可见性=DEFECT-004 修复的副产品）
`docker_e2e`（A29 票支，本机，Desktop 健康）：14 用例中 13 用例过/半过，唯一整测红=
`TestCollieNativeProxyDockerBoundary/{codex,claude-sdk}` 双双
> `collie_native_proxy_test.go:88: timed out (1m30s) waiting for the native Collie draft before provider completion; last: The actual Collie history has no native draft yet.`

此前该测在坏域叙事下"从未有干净运行"（v0.1.64 三红之一；v0.1.80 更正后其"wake 依赖骨架产活"的旧注**部分成立**：骨架活了它仍红 ⇒ 内层第二缺陷被外层缺陷掩盖——**洋葱结构，剥一层见一层**）。

## 2. 已有证据
- `docker logs mc-collie-h-…` = **`No such container`** ⇒ collie 容器从未创建 ⇒ 断言等待的 draft（由容器内 runner 写）不可能出现；红在 **wake/birth 层**，不在 provider/代理层。
- resident 日志（同 run）：boot 全绿、`projector_live`、`credential.acceptance_armed`、interval 500ms、**149 idle ticks elided**（=collie send 之后 tick 无动作）；sweep 仅有 run-log prune 的 `unrecorded=1（h-…）` 告警（A16 面，独立挂账，非本因）。
- A29 改动面=`verbs/sourcegit.go` 族（seeding/onboard/uninstall 的 git 读）；collie 启动链（`mc collie start/send`→resident adopt/launch）**不经该族**（grep 零交集，本票评审时验证）⇒ **非 A29 引入**——已由父提交对照实证（§3，同红同签名）。
- 同道的 `TestCollieConversationDockerBoundary`/`CollieResume`/`ColliePacketArtifacts`（三枚 collie 测）**皆 PASS** ⇒ collie 基础 birth 路径整体可用；红被收窄到**"native proxy 启动时序"特有面**——该测的次序独异：`collie send` **在前**、`startNativeProxyResident`（带 `CULLER_COLLIE_PROXY_FIXTURE` preload 的 resident）**在后**；其余 collie 测 resident 先起。

## 3. 取证进展（隔离跑+父对照，如实分层）
- **隔离复现成立**：单测直跑（无并行负载）两子界同红同签名（`no native draft` + `No such container`，`COLLIE_EXIT=1`，292.8s）；`MC_E2E_KEEP=1` 现场树=`/private/tmp/mc-e2e-{4205225568,3994404516}`。
- **行级取证作废（自我纠正，二次）**：曾以两份 spine 卷的 `collie_sessions=0` 作"teardown 残留态"解读——**卷身份未证实**：事后核挂载，`582032337/1673169925` 属**并发中的父对照**、`140360502/2687160510` 为昨日旧泄漏卷 ⇒ 我的读取**与保现场树无对应关系**，那次"0 行"**不构成任何证据**（连"teardown 残留"都不是——根本没读到当事卷）。教训并入 AM 更正链：**行取证前先证卷身份**（home↔volume 映射须从被检树本身读出，不从列表猜）。当前 spine 行状态=**UNKNOWN，未取证**。
- 可靠事实层（不依赖行取证）：该测内 `collie start/send` 经 `f.mcOK` 全成功（否则更早红）⇒ **会话建立成立**；红在 wake/birth 层（该测自身 resident 日志零 collie 行——此读自保现场树的 resident.log，树身份有 `MC_E2E_KEEP` 行明示=可信 + 容器不存在 daemon 报）。
- **父提交对照=独立性定论（决定性）**：`04cd6b2`（无 A29 改动）单跑同测 **同红同签名**——两子界皆 `collie_native_proxy_test.go:88 … no native draft` + `No such container`，`PARENT_EXIT=1`，308.24s。⇒ **DEFECT-005 先于 A29 存在于 trunk，非本修复引入**；A29 使 e2e 道其余全链首次可跑，才让此内层红第一次获得干净可见性。

## 4. 通道预算观察（独立于本缺陷，一并登记）
包级 `-timeout 30m`（AGENTS 文本即此值）对现 14 用例**不足**：已执行用例合计 ≈1556s + 剩余 4 测 ≈600s+ 下界 ⇒ 全过亦须 ~40m。`TestRebootDrill` 在 12s 处被 panic 打断（非其自身失败）。**连带实证一型残留机制**：包超时 panic 跳过 `t.Cleanup` ⇒ **夹具命名卷泄漏**（本机实测 8 只 `mc-e2e-spine-*/culler-e2e-runtime-*`；已清）。**道时预算/AGENTS 数值更新属测试域决定**（呈报，不擅改）。

## 5. 修复归属建议
wake/birth 时序属 **collie 产品面**（非票面现有：A14 V-1b/c 系 harness 配置面、A25 系向导）。建议**新立票承载**（A30 候选）：collie native-proxy 子界红 + resident wake 语义追证；或并入 L02 的"计划内/崩溃区分"家族（resident 晚启=计划内时序，恰与其同源）。**归所有者裁**，执行侧不建票不猜修。
