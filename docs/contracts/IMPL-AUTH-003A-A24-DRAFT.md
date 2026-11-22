# IMPL-AUTH-003A — A24 追加令（**草案 DRAFT，未签发**；沿 003 签发协议：注册≠发出，发出=所有者明示决定事件 + 绑定本文件全值 SHA-256）

来源：所有者 2026-11-22 十项裁定 **Q1**——"批准 003 版，纳入 IMPL-AUTH-003 追加令，不改已签发正文及原正清单字节。" 本件承载 **A24 的代码实施授权范围**（设计范围批准 ≠ 代码授权；本轮仅把范围落成待签发文书，**执行侧不因本草案改 A24 生产码**）。

## 款一 唯一授权工单与正清单
A24 Linux 宿主操作者视野门（Track A 尾，depends A04✓）。**批准包权威对象 = `evidence/A24/diff-plan-003.md`**：
- git blob（TA24）：`2ca88cab1361087da3702d07248039dca39c96f2`
- **内容全值 SHA-256：`d557d5fb2a8b813a88fc79719df3ac633a163abbda2ffeed19fdca96567b3d4c`**（64 位全值；缩略值不作签发依据）

**正清单（超出 = STOP-1）**：
1. `mc/cmd/mc/runtime_scope_prod.go` — **两谓词同翻**：
   - `canInspectProjectVision()`：Linux 宿主（无 `/.dockerenv`）=true；其余域返回值与现状**逐字不变**；
   - `shouldDelegateToHelper()`：Linux 宿主=true（与 darwin 同构：宿主 broker→ensure warm helper→`docker exec` 委托进锁域）；其余域不变。
2. `mc/cmd/mc/*_test.go` — 委托/视野测试转写：既有 `brokerDoctor/bBackup/reset` 等 darwin 钉**加** linux 对应形（darwin 用例逐字保留，非替换）；视野谓词的容器域反证钉（agent/helper 仍 false）。
3. `evidence/A24/` — 三域矩阵 post 轮原始件 + A24-V-001 记录。
4. `tools/blcheck/status.json`、`docs/contracts/ACCEPTANCE-MATRIX.md`（收口轮）。

## 款二 禁触区（负清单优先，违者 STOP-1）
- `verbs.RequireHostScope`、run.json 身份装载、`dispatch.Decide`、prepare/attest/commit 冻结 canonical JSON、任何 wire 形状；
- `mc/substrate/*.sql`、supervision 渲染器、agent 输入面（directives/brief/skills）；
- 委托的 **helper 创建权威**（A03 监督栈/向导段所有）——本追加令只翻"宿主命令找自己 helper"的**委托谓词**，不新建第二编排权威。

## 款三 验收（附五真机义务 + Q1 冻结基线硬闸）
三域 post 矩阵在定向机（mc-prod@在位，剧本 `evidence/A24/a24-vision-acceptance.sh` v2，基线 `a24-pre-baseline-218.tsv` 为钉）：
1. **H 域**：`__release` 门 token 消失；`project-list/delivery/env-save` 从 `/mc/spine` 常量路径错**变为经 helper 的业务应答**（deployment-unavailable 族可，门 token 不可）；
2. **HELPER/AGENT 域**：**逐行等于 pre 基线**（放宽渗漏硬闸；AGENT 由身份+锁域双拒，与 OS 门无关）；
3. 判定 fail-closed（双列扫描，marker 需反证）。post 通过 = A24 EVIDENCED 证据；义务完成判定仍归 F0a Exit + 治理链。

## 款四 时效与撤销
- 实施时效：追加令发出日起 14 自然日无进展重审（沿 003 §6）；
- 撤销：所有者明示撤销 A24 即停，已交付票分支不自动并入；
- diff-plan-003 若被编辑，其 SHA-256 变 → 本绑定作废，重算重批（条件 5 精神）。

## 款五 状态（截至本草案）
- A24 **设计范围批准在册**（所有者 Q1）；**代码实施未授权**（待本追加令由所有者以完整 SHA-256 发出）；门一字未松，`shz/TA24` 未并 dev。
- 本草案全值 SHA-256 由所有者**发出时**记录为绑定对象（此处不预设）。
