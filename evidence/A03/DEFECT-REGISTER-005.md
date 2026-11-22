# Defect register 005 — Collie 原生代理子界红（~~collie 容器从未 birth（wake 不 fires）~~ **更正：birth/wake 正常，红在 native-provider 传输层**，见更正段 #1）；A29 修复后 e2e 道内**唯一剩余红**，与 A29 改动面零交集

状态：**REGISTERED, IN PROGRESS（取证进行中；§1/§2 原定性已作废——更正段 #1）**。属登记不修：collie native-proxy 传输面不在任何现行正清单。

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

---

## 更正段 #1（2026-11-22 轮十二追加；**§1/§2 核心定性作废**，原文保留不删；§5 的 wake 框架与 (a)/(b) 判据随之作废）

**追加者=原撰写者本人；触发=轮十二 A30 只读预勘带回否证，经本代理独立复验成立。原句"collie 容器从未创建 / wake 不 fires / 红在 wake/birth 层"全部推翻。**

### 复验证据（全部宿主侧保留树+源码，不依赖已删卷）
1. **Birth 实证**：树 `/private/tmp/mc-e2e-4238657310/home/logs/runs/h-22c5523c973ad663/runner.ndjson` 含 `runner.started` → `runner.harness_failed`，逐字 `"exit":1,"replied":false`。会话目录+runner 日志存在=容器创建并运行过。
2. **`No such container` 是设计保证的事后假象**：`resident/src/effects.ts:686` 注释自明 collie 容器 **`--rm`**（"so the container is gone"）——测内 cleanup（`collie_native_proxy_test.go:84-87`）在容器退出后 `docker logs` **必然**拿不到对象。该信号与"从未创建"和"出生后即死"**两个假设皆相容**，我取了前者。
3. **反证曾在自己引用的行里**：首轮我逐字引用过 `sweep.skipped unrecorded=1 … log(s) name no run row … h-1e6f4bd057c36d94`——h- 前缀=collie 会话 runner 日志存在=**birth 已发生的直接反证**，当时未解读。**违犯在册纪律"本地感知远程效果≠不存在（超时/关闭/dispatch 停摆皆不证无）"的现行实例。**
4. **wake 路径实核**（预勘 file:line+本代理复核其设计文本）：wake=dispatch 驱动自脊态（`collieselect.go:19-21`"wake 无需 pending inbound turn"；`collie.go:203-204` 容器可先于首消息备好；`CollieSend` 为纯脊事务 INSERT 消息——**无 socket 队列可丢**）。我"resident.log 零 collie 行⇒wake 未发"是**探针位置错**：wake 结果落 `mc.ndjson`/spine activity，不落 resident.log（该树实测 resident.log 计数=0 与 wake 正常并存，即证）。
5. **卷身份纪律已执行**：树→卷映射自被检树读出（`e2e_test.go:567 f.volume="mc-e2e-spine-"+sfx`；预勘所引会话 id 与树内目录名全等）——§3 那次"0 行"误读的教训未重犯。

### 修正后的定性（红仍在、独立仍在，层位移动）
- **失败层=native-provider 传输边界**：容器内适配器请求**未抵达测试 fixture 传输**（树内 `native-provider/` 下 requests.jsonl **不存在**=零请求；预勘另见 diagnostic_events `invalid_api_key 401`，涉已删卷 spine 行，本代理未独立复验=降为"预勘证据"）⇒ harness 无回复 exit=1 ⇒ 会话 `resume_owed=1` 停摆 ⇒ 测等 draft 超时。wake/birth/持久消息**皆正常**。
- **产品 vs 夹具归属=未定（E3）**：线索=测配置 `surfaces.provider_proxy bind=""` + preload 仅补 `globalThis.fetch`——若 codex SDK 自带传输绕过之，401 来自真实上游（合成 key）⇒ **夹具布线缺陷**；若产品 egress 本应经宿主代理而 bind="" 语义未定义 ⇒ **产品面**。**此判定需一次适配器 egress 只读追读，A30 diff-plan v2 的前置；不预设。**
- 不受影响的在册结论：父提交独立性（红确在 04cd6b2）；"A29 使内层红首次可见"的洋葱定性（成立，内层=传输非 birth）；DEFECT-004 闭合；A29 16/17。
