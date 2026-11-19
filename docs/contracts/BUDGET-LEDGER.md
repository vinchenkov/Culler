# CONTRACT · BUDGET-LEDGER（v0.1.2 · RECOMMENDED · 待批准）

- 状态：**RECOMMENDED v0.1.2**（终审二 9.3/10：六审查项全过——I2 公式/聚合去重/模式分离/settle 身份/WRITTEN_OFF/I4 出处改判（设计通过，BL-V1 前域 2 退化运行=登记的诚实状态）；**契约正文封闭，无 v0.1.3**；七项架构原则+§12 十二项设计不再重开）。**RECOMMENDED≠CONTRACT_FROZEN≠IMPLEMENTATION_VERIFIED**——正式冻结待 Architecture Approval Authority+Integrity Reviewer 哈希核验；§8/§9 所列 F1 义务不因推荐状态推定完成。修订映射 §13。
- 审查证据边界镜像（沿 v0.1.1）：审查基于提交文本非独立读源；`[固化]`=提交稿声明，验证归 F1/P4.x 证据。
- 冻结输入（消费不重述）：DC §1-2/§3.2/§3.3/§4（含"效果同一性判定归 BL"授权）/§6；**D25=预留五态账本机器的唯一出处**；**request-local 失败分类链的出处=D13/Q18 裁定记录+[固化] `chargesAllowance` 机制，其是否显式覆盖 budget_exceeded 输入类待 BL-V1 原文核对（§11）**；D12/D8；IG §6 零缓存/§3/§5.3 同构源/§8.1 五要件；TM §8.0/8.1；D30/不变量 3；D17。

## 0. 术语纪律
"窗口"三限定（预留期限 T_r／预算周期／授权陈旧窗-已消灭禁复用）。四额度概念严禁互称：`confirmed usage`／`outstanding retention`（未决预留占用，含 UNKNOWN/PARTIAL 未收敛，**闸门算术成员**）／`OVERSPENT`（confirmed 超出原预留的**已证**部分；不可证者记 `OVERSPENT-UNPROVEN` 独立标识，不入"有界越界"口径）／`frozen`（数据语义=未决挂账标志，outstanding 的投影，展示与算术同源）。

## 1. 对象与不变量
对象：`budget`（层级×周期×维度）、`reservation`、`settlement`、`budget_period`。
- I1 authorize 同事务 reserve+launch intent（DC §3.2）。
- **I2（v0.1.2 重立，规范式）**：对每个适用层级 L——

  `Available_L = Limit_L − ConfirmedUsage_L − OutstandingRetention_L`

  （Limit_L=该层级当前周期有效上限；ConfirmedUsage_L=已确认实耗；OutstandingRetention_L=未收敛预留占用，含 UNKNOWN/PARTIAL 未决、未到期与已到期未收敛均计）；
  **准入条件：∀L∈适用层级，`Available_L ≥ RequestedReservation_L`**，任一不满足=整体拒绝；
  **聚合去重纪律**：同一 reservation 在各层级各有一条占用投影行（§6），**层级聚合按 reservation_id 唯一事实计，Team 层求和严禁把 Team 投影行与 Project 投影行重复相加**；预测性放大禁止。
- I3 只认 confirmed；UNKNOWN≠零耗；估算只入报表估算列。
- **I4 事件域表（v0.1.2 出处修正+不自行映射约束）**：

| 域 | 事件 | 允许语义 |
|---|---|---|
| authorize 准入闸 | `BUDGET_ADMISSION_DENIED` | 零预留/零 run/零任务状态/零 allowance 扣减；**绝不产生 blocked/stalled** |
| 执行中 proxy 守卫 | `REQUEST_BUDGET_EXHAUSTED` | 仅=拒供后续 provider 调用；任务级后果**完全由既有 request-local 失败分类链承载（出处=D13/Q18 裁定+[固化] chargesAllowance 机制）** |

  **约束句（终审§四入约）**：BL 不定义、不修改、不扩大该失败分类及其任务级后果；该事件仅作为分类链的**输入类**投稿——**若 BL-V1 核对确认出处未显式覆盖 budget 输入类，BL 不得自行映射至 allowance/stalled 链，§5(b) 守卫退化为"仅拒新请求、不做分类推断、零任务语义"**。stalled 若出现，只可能是既有链的下游效果，非预算治理动作。
- I5 越界是事实不是禁忌，**有界是容忍前提**（§5(b)：BOUNDED 模式定义）。

## 2. 预算状态机
`RESERVED → DISPATCHED → SETTLED | RELEASED | PARTIAL_SETTLED`；`进行中态 → UNKNOWN → RECONCILIATION → {SETTLED | RELEASED | PARTIAL_SETTLED}`；`RESERVED → EXPIRED → RELEASED`（EXPIRED=可见中间态；额度回归只由 RELEASED 事件同事务完成）；`UNKNOWN | PARTIAL_SETTLED →(RECONCILIATION)→ WRITTEN_OFF`。
**WRITTEN_OFF（v0.1.2 授权收紧）**：唯一人工治理出口，**独立账本终态——非 RELEASED 变体、非账务修正**；其本质=对"未决资源风险"的治理接受，故批准事件六要素强制绑定：① reservation_id+原 period_id；② 核销的具体未决额度；③ 证据永久不可得的依据（指针）；④ 原因+关联风险；⑤ **具名 Acceptance Authority**；⑥ 批准时间/范围/不可逆审计事件。**凡涉及未接受之残余风险者，须同时满足 TM §8.1 与 RR 登记流程——单个 Authority 字段不构成风险接受**；核销不改 confirmed、不伪装 RELEASED，报表永久保留其历史事实；UNKNOWN 无任何自动清零路径。
禁止转换：终态原地改写（纠错=反向冲抵事件）；`RELEASED→SETTLED`（再耗=新预留）；UNKNOWN 无证据入 SETTLED；**T_r 到期释放 UNKNOWN/PARTIAL 未决 retention**（到期扫描只作用 RESERVED）；DELETE。终态竞争一律 reservation 单调版本 fence（胜者唯一，后到 stale 零效果）。

## 3. 预留生命周期与 launch fencing
（同 v0.1.1，未变项不重述：fence 化启动资格、双序规则、UNKNOWN 保守占用、IG §5.3 同构+域内简化声明、原则句"本地超时≠远端资格失效"、T_r 有期强制、续期=supersedes。）场景 B 措辞更新：partial 已证→PARTIAL_SETTLED；**未决差额=retention 占用延续（非 OVERSPENT——OVERSPENT 仅指已证超留，不可证者带 UNPROVEN 标识待对账）**；核销路径唯一经 §2 WRITTEN_OFF。

## 4. 原语与幂等
四动词+reconcile/writeoff 治理动词，全 mc 单写事务、零外部账本。reserve 同键同快照=原行、异快照=冲突拒（调额=supersedes/冲抵）。**settle（v0.1.2 身份补全）**：
- **来源身份四元组**：`(authorization_id, provider_request_id, source_report_id, source_version)`+`payload_digest`+`supersedes_report_id`——**report_kind 修正为正交两维**：`{cumulative | incremental} × {interim | final}`（final 是终局性标记，非第三类报告型）。
- 规则：累计报告=同源**更高 version supersedes 低 version，禁相加**；增量报告=每事件独立不可复用 source_report_id，重复身份不重账；final 必须经 supersedes 链收敛既有 interim（无链=协议错误→UNKNOWN）；**同源同 version 异 payload_digest=冲突→UNKNOWN/RECONCILIATION，禁 last-write-wins**；**类型不可判定 ≠ 按"无增量"入账——直接入 RECONCILIATION**（v0.1"最保守处理"含混处按终审§五修正：不得以选择某种解释静默丢失可能消耗）。
release：(reservation_id, cause) 键防同意图重放 + 版本 fence 防异意图重复回归（双防御）。
**错误语义规范六条（冻结；码位留 F1）**：冲突重放不覆盖；stale 零效果；UNKNOWN 非成功非失败；busy/锁、CAS-fence 败、业务拒绝三分离；重试不改意图快照；对账失败不静默释放。

## 5. 闸门位置
- (a) authorize 唯一硬闸：实时 spine 读；I2 全层级判定；不足=整体拒绝零记录增长（§1 I4 域 1）。
- **(b) 执行中守卫——两种运行模式，例外不是 BOUNDED（v0.1.2）**：

| 模式 | 语义 | 进入正常预算强制配置 |
|---|---|---|
| **BOUNDED** | completion 上界可证（① PS 能力注册表可靠值 → ② 平台可强制 local 值） | **允许**——"有界越界+OVERSPENT 事后入账"仅适用此模式 |
| **UNBOUNDED_EXCEPTION** | completion 上界不可证，经治理授权承担明示风险 | **否——独立受限例外态**。绑定七要素：具体模型×provider、Team/Project/budget_period、例外额度或明确风险范围、具名 Authority 批准事件、生效/失效/撤销条件、账本 `OVERSPENT-UNPROVEN` 独立标识、**报表与 A4 一律记"例外，不计为有界越界验收通过"** |

  ①②皆缺且无有效例外授权=**该模型不得进入预算强制生产派发**；**无法给出可接受范围与补偿控制时，维持默认拒绝——Authority 事件不得成为绕开预算上界的通道**（本例外即 TM §8.1 降级授权的一个实例，五要件语义全适用）。
- **量纲**：maxTokens=completion tokens 上界（provider 语义可强制量）；总/计费 token 不由此保证；总界估计不完整→该请求上界记"未证"，归入上表模式判定。
- (c) chargeback 非闸门；三列永分；frozen 为数据语义标志。
- 负面清单（A6）：预算域对 tasks/dispatch/priority 零写零读（I4 域表+BL-V1 前置约束下成立）。

## 6. 层级、聚合与周期
- **投影模型（v0.1.2 明文化）**：一个 reservation 对每个适用层级各生成一条占用行（同一 reservation_id 贯通）；**各层级 outstanding=其名下按 reservation_id 去重的占用行之和**；authorize=单事务内对全部层级行原子写入，任一 Available 不足整体回滚（无部分预留；异步二道扣减禁止）。Team outstanding(Team)=Σ_projects 占用投影，**不与 Team 层行重复相加**（I2 聚合去重纪律的实施形）。
- 周期五规则（绑定终身/UTC 确定性/换期不迁移不释放/新期独立核算不掩旧期未决/迟到 usage 永记原期）：同 v0.1.1。
- 双维度/牌价快照/不限须显式批准绑四要素/子≤父配置约束：同 v0.1.1。

## 7. 失败语义
同 v0.1.1（proxy 计数随 grant 作废=既定；对账证据不可用=UNKNOWN 占用延续+health+超期升级 Authority；预算表损坏走 DC §7 恢复链；牌价缺失=unsettled-cost 事实保留）。

## 8. Test Strategy
v0.1.1 九组全保留，新增：⑩ **I2 算式方向性用例**（confirmed 增加→Available 单调减；retention 增加→Available 减；Limit 变更事件→重算正确——防本公式回退）；⑪ UNBOUNDED_EXCEPTION：A4 判"例外"断言+例外到期/撤销后派发拒绝；⑫ settle 四元组矩阵（同 id 同 version 异 digest→UNKNOWN；类型不可判→RECONCILIATION 非入账）；⑬ WRITTEN_OFF 六要素缺任一=拒绝执行（非普通释放的负例：绕过 TM §8.1 流程=测试必须红）。

## 9. Acceptance Criteria
A1-A3、A5-A7 同前；**A4（v0.1.2）**：(b) 越界上界=可证 completion 界，数值成文；**UNBOUNDED_EXCEPTION 模型 A4 记"例外不计通过"，且其禁以"有界超支"名义出现在任何平台安全状态声明（X-2）**；A9-A11 同前；**A12**：I2 规范式实现与本文逐符号对照+⑩方向性用例全绿（**公式错误曾是本契约最重缺陷，以测试锁定防回潮**）；**A13**：BL-V1 通过为 §1 I4 域 2 全语义生效前提——未通过前守卫按约束句退化运行（仅拒新请求），此为**当前诚实状态**。

## 10. 未决与依赖
F0b 参数：T_r 系数、BOUNDED 各默认上限、per-team 收紧面。**BL-V1（新增挂账）**：核对 D13/Q18 裁定原文与 `chargesAllowance` 分类链文本是否显式覆盖 budget 输入类——核对前 I4 域 2 按退化语义运行。PS 接口（与审查方互认）：BL §5(b)①级以 PS 能力声明的**可靠性**为前提，PS 定义"max_tokens 可证性"的语义与证据，BL 只消费——**PS 不担预算正确性，BL 不担 provider 能力真实性**；单 proxy 实例假设不变。

## 11. 裁决处置记录
BL-D-1 ✔（token-only）；BL-D-2 ✔（四要素绑定）；BL-D-3 ✔（确定性版本+收紧 only）；**BL-D-4 有条件接受→v0.1.2 落实为模式分离**（例外≠BOUNDED，不得借例外过 A4）；BL-D-5 ✔（PARTIAL_SETTLED 独立非终态+转换组）；WRITTEN_OFF 接受为唯一人工出口+六要素收紧；三级阶梯保留但第③级拆为"默认拒绝/例外受限态"两轨。

## 12. 已通过不再重议（沿审§七）
fence 化启动资格、EXPIRED 可见中间态、UNKNOWN/PARTIAL 跨期占用、同事务多层原子预留、UTC 周期终身绑定、货币不做闸门、单发上限收紧 only、supersedes 收敛、双防御 release、无 DELETE 反向冲抵、四概念分离、零外部账本——**接受，不重开**。

## 13. 修订映射（v0.1.1→v0.1.2，终审四项必修+核验）
①必修-公式：§1 I2 重立规范式（Available=Limit−Confirmed−Retention+全层级准入式+聚合去重）+§6 投影模型+§8⑩/§9 A12 防回潮；②必修-例外边界：§5(b) BOUNDED/UNBOUNDED_EXCEPTION 模式表+七要素绑定+"不得绕界"+A4 改写；③必修-报告身份：§4 四元组+digest+supersedes 链+report_kind 正交两维+"不可判定入对账非入账"；④必修-WRITTEN_OFF：§2 六要素+独立终态+TM §8.1 联动+§8⑬；⑤核验挂账：I4 出处由 D25 改判为 D13/Q18+chargesAllowance，**BL-V1 原文核对前域 2 按退化语义运行**（A13 记录当前诚实状态）；⑥镜像登记：审查§六对 OVERSPENT 与未决占用的概念混用修正入 §0/§3。
