# CONTRACT · DATA-CONSISTENCY（v0.3.2.1 · RECOMMENDED · 待批准）

- 状态：**RECOMMENDED v0.3.2.1**（短终审裁决：五项补正全部通过，9.3/10，无新增事实 P0，正文不再修改）。本行为末次登记清扫轮（短终审 §2 第 2/3 项：标题版本统一+G-F 范围统一，**零语义变化**——审查正文绑定 v0.3.2 哈希 `53c91445…`，diff 核验仅标题/状态行/事实底座句三处，§11-f）。**批准未发生**：待 Architecture Approval Authority 批准 CONTRACT_FROZEN + Integrity Reviewer 独立字节复核；冻结≠实现验证，短终审 §4"不得宣称七项"由 AM 登记执行。修订映射 §11-d/e/f。
- Scope/立约方式/全局解读注：同 v0.2（`[固化]`=机制存在于 b22df40 的证据声明，非有效性声明；`[新增]`=本契约引入；每条不变量映射到可执行验证）。
- **事实底座**：写入口×门禁审计（b22df40 控制流级，CONFIRMED/NOT_CONFIRMED/NOT_FOUND 三分，检索范围=mc/resident/dashboard/runner 生产码，排除测试）——本契约 §1.3/§5 与之逐项对应；审计结论中"未覆盖组合"如实登记为实施义务 **G-A..G-F**（v0.3.2.1 统一，此前 bullet 漏 G-F 属登记不一致——实施义务集合以 §5 为准），**不补写全覆盖声明**。

## 1. Writer Contract（写者契约）

**1.1 定义**（三层串行/围栏同 v0.2；`[新增]` 三件套中②由 v0.3 按 P1-4 重写如下）：
- ② **写 verb 错误语义六维登记表**（v0.3.1 按复审 6.3 补严）：每个写 verb 落库路径必须登记六维——(a) SQLite busy 行为（等待至 timeout 后的返回形态）；(b) CAS 败者的业务结果码；(c) retryability（**按副作用阶段登记**——同一错误类在副作用发生前/后可有不同安全重试条件，允许多值，禁止单一笼统 true/false）；(d) side-effect 是否可能不确定（→对账义务）；(e) exit code（0/1/2）；(f) 稳定机器可读 JSON error class。**两个同 exit code 的 verb 必须可从 (b)(c)(d)(f) 区分**。登记表随 F1 生成并入 AM；缺登记=review 拒绝项。
- ①③ 同 v0.2（多进程压测 lane；锁等待直方图）。

**1.2 Invariants**：同 v0.2（原子性可见、围栏不可绕、参数绑定禁拼接、持锁不做宿主 I/O）。

**1.3 互斥机制与顺序约束（v0.3 按 P0-3 拆双表）**

*表一：互斥机制登记（按实际机制类型，不称"锁"者名实相符）*
| # | 域 | 实际机制 | 层位 | 保护对象 | 证据 |
|---|---|---|---|---|---|
| K1 | dispatch 轮次 | flock（`mc.dispatch.lock` 文件锁） | DB 事务**外** | 轮次串行（flock→BEGIN IMMEDIATE 顺序 [CONFIRMED `dispatchverb.go:199-209,326-359`]） | ✅ |
| K2 | onboarding | flock（`<spine>.onboard.lock`） | DB 事务外 | 建库/迁移序列化 [CONFIRMED `onboard_spine.go:149-183`] | ✅ |
| K3 | admission "桶" | **逻辑认领**：事务内认领行 CAS/UNIQUE 冲突，**非 OS/DB 行锁**（SQLite 无行锁） | DB 事务内 | 配额竞争确定性（桶行先于目标行的**事务内操作序**）| ✅机制/◐命名（v0.3 起弃用"桶锁"称谓，改"桶认领行"） |
| K4 | 库级写序列化 | SQLite WAL 引擎互斥（BEGIN IMMEDIATE 取得） | 写事务期间 | 单写者 | ✅ |

*表二：获取顺序约束*
1. 文件锁（K1/K2）必须先于 DB 事务开启；**禁止持有 DB 事务再等待文件锁**（现存路径合规 [CONFIRMED]）。
2. 事务内逻辑认领（K3 类）必须声明确定操作序（认领行→目标行→账本追加）。
3. 新互斥域登记时必须声明与 K1–K4 的关系，否则 review 拒绝。
4. **单写者≠无死锁**：flock 等待×进程间等待×docker-exec(120s)×helper 内事务×tick 单飞可构成跨层等待环——F0a 反例扫描**不能只做 grep 锁 API，必须人工检查持锁范围与跨层调用路径**（登记为 F0a 任务）。
5. 表二 1-3 为候选规则，F0a 盘点闭合前不得写成"已验证全局锁序"。

## 2. Transaction Semantics（事务语义）

- 同 v0.2（聚合根+子账本同事务模式、读路径、超时登记、失败分类学），另按 AM P2-6 收紧一处：`[固化→义务]` **`immutable=1` 的使用前提是"该库确无并发写者"的部署承诺**，观察不到 `-wal` 文件不构成充分条件；F0a 代码审计项：要么以只读事务模式（mode=ro）为默认、immutable 仅限离线快照，要么补显式不可变证明（登记 AM）。

## 3. Event Contract（事件契约；v0.3.1 按 R-03/04/05/07/08/10/11 闭合链协议最低契约）

**3.1 序列模型（[新增] 设计声明——本条全部为新设计，非 b22df40 推导的事实）**：主键 `(aggregate_id, seq)`，四概念分离（R-04）：① **序列分配规则**=聚合根内 `max(seq)+1`，由单写者事务（BEGIN IMMEDIATE）内读取并分配，确定性由此保证；② **唯一性约束**=UNIQUE(aggregate_id, seq)+event_id 去重；③ **链排序依据**=seq——**rowid/其他三选一方案作废**（复审 §10.3 方向采纳；v0.3 "链头=最大 seq 行"与"排序键待裁"的自相矛盾就此消除）；④ **链头定义**=该聚合根最后一个成功追加的链记录（3.4）。边界声明保留 v0.3 四条：聚合根内有确定性顺序；跨聚合根无 seq 保证的全局总序；causation/correlation 仅表因果关联、不构成顺序证明；跨根严格先后须另立可验证依据，不得从局部 seq 推导。

**3.2 链保护对象与原子审计不变量（Class A/B 分层经复审批准；补 R-03）**：Class A=append-only 证据账本（activity、dispatch 收据、signoffs[P4 后]、usage 结算事件、admission 授权/撤销/过期事件、schema **迁移事件**）入链；Class B=可变状态表，行不入链。
`[新增·R-03]` **原子审计不变量**：凡声明由 Class A 承担审计的 Class B 状态转换，**状态变更与其对应事件追加必须在同一 spine 写事务内原子提交，任一失败整体回滚**；未建立映射与原子保证的状态转换，**不得宣称已被 Class A 账本完整审计**（否则链验证器无法发现"根本没有写入的事件"）。映射表（仅覆盖纳入 Class A 审计责任者，telemetry/logs 不拉入）：run 状态↔lifecycle 事件；task 状态↔transition 事件；lease↔grant/renew/revoke 事件；usage 余额↔五态账本事件；admission 状态↔grant/revoke/expiry 事件；schema 当前值（`meta.schema_id`，Class B）↔迁移事件（Class A）。
`[新增·v0.3.2/R-03 补]` **迁移原子性契约**：迁移导致的 schema 状态变更、迁移步骤登记（schema_history 行）与对应 Class A 迁移事件，必须在**同一可恢复的事务边界**内形成一致结果；确不能单事务完成的步骤，必须定义**可确定性恢复的中间状态**（由 §5 四态检查点恢复），**不得暴露"schema 身份已更新而对应迁移证据缺失"的分裂态**——不要求巨型事务，只禁止无法解释的状态/证据分裂。高频记录（R-10 收紧）：**不入审计链；其 payload_digest 用于数据一致性检查与后续外部锚定材料，不构成独立的防篡改、防整段重建或对外完整性证明**。
`[固化→缺口]` 链在 b22df40 不存在（NOT_FOUND 已证）——本节全部为 `[新增]`；§7.5/A6 的"链连续性"在机制建成并验证前仅为目标验收要求。

**3.3 事件形状与关系语义（R-07/R-08/R-11）**：事件 schema 的**定义权在本契约**；TM §7.1 是**验证记录** schema 的唯一来源，两者仅字段名借鉴语义（occurred/recorded 双列、digest 族），**不构成事件对验证记录结构的继承，反之亦然**。关系修正：**决策修订链用本契约定义的 `supersedes_event_id`**（通用事件取代关系）；TM §7.1 的 `supersedes_verification_id` 仅适用验证记录替代——命名习惯同源，语义不得互抄。**admission 过期时序**：grant 有效性判定依据其自身 `expires_at` 业务规则，**不依赖 expiry 事件是否已写入**；expiry 事件记 `effective_at`（业务失效时点=expires_at）与 `recorded_at`（观察时点）两列且不得混用；事件幂等（去重键=grant_id×event_type），重复观察不产生第二条。

**3.4 链结构最低契约（R-05；[新增]）**：
- **摘要关系**（复审基准采纳，算法实现细节留 F0b 配套设计件）：`event_digest_i = H(CanonicalEvent_i)`；`chain_digest_i = H(chain_digest_{i-1} ‖ event_digest_i)`；`prev_hash_i := chain_digest_{i-1}`，**存于事件行列**且**计入 CanonicalEvent 哈希域字段集**（防记录替换）——两者关系统一由本条确定，不设第二条规则。
- **genesis**：聚合根首事件 `prev_hash = GENESIS(aggregate_type)`（域分隔常量=零值+类型盐，防跨聚合根移链）；不设独立 genesis 登记表，首事件即存在证明（与 3.5 `aggregate_open` 协同）。
- **规范化序列化——语义层冻结在 DC（v0.3.2/P0-A），F0b 只承载字段表/编码规范/跨实现测试向量，且 F0b 不得改变以下语义**：① **缺失字段 ≠ null**（两态，均入摘要计算）；② 数值以精确十进制字符串规范化，**禁止整数/浮点互化**（`1` 与 `1.0` 属不同摘要输入=serializer 语义错误，报 FAIL 而非静默统一）；③ 时间统一 RFC3339 UTC 定宽（或 epoch-nano 整数——由 serializer_id 冻结其一，禁混用）；④ 字符串 NFC 规范化 UTF-8，转义规则随 serializer 冻结版本表；⑤ **对象键按声明序**（schema 声明序，非字典序；嵌套递归同规则），数组保持原序；⑥ 未知字段=错误处理（NOT_CONFIRMED 上抛，不得跳过）；⑦ **serializer 版本不可变**：id 一经登记语义永久，历史事件按其记录的 serializer_id 复算；⑧ **跨实现一致性测试向量为 F0b 验收前置**——无向量集，摘要算法视为未定形。
- **检测义务（v0.3.2/P0-B 两层界定）**：**层一（本地验证器，链建成即有）**——断链（prev_hash≠前序 chain_digest）、重复（UNIQUE 拒+复验）、分叉（同一 (aggregate_id,seq) 双摘要——单写者下=损坏信号报 ERROR）、聚合根内事件缺失、链头回退的**检出**。**层二（整聚合根消失）**：`aggregate_open` 只登记生命周期——**不得同时充当被删对象与其消失的唯一检测依据**（整根连同 open 事件被删后本地无任何痕迹）；整根消失检测**必须依赖独立于该根链的预期依据**：已锚定的聚合根链头集合（D22/IG 产物）或独立保全登记。**无锚定/独立登记在位时，不得宣称具备整根消失检测能力**（设计能力≠当前能力，D31①）。归档保留记录；物理删除仅 project_deletion 登记路径且删除本身入事件账本。
- **prev_hash 冗余绑定的显式承认（v0.3.2/三.1）**：prev_hash 为事件行**规范字段**且参与链递推=有意的冗余绑定（无循环依赖）；验证器**同时核验两种关系**（字段值=前序 chain_digest，且递推式成立）；任何实现**不得**把 prev_hash 排除出 CanonicalEvent 哈希域字段集。
- **验证器输出（协议形状）**：输入=spine 文件/快照；输出=**per-aggregate 链头集 `{(aggregate_id, seq_max, chain_digest)}` + 整体根**（按 canonical 序 aggregate_id 升序对链头集做 Merkle 或有序 fold）；输出绑定 §7.1 验证记录（evidence_digest=链头集文件摘要）。**根算法版本化（v0.3.2/三.2）**：DC 冻结输入集合、排序规则与空集语义（空集=`EMPTY_ROOT(域常量)`）；**F0b 冻结实际算法及其 `algorithm_id`**；输出必须携带 algorithm_id；算法更换**不得静默重释旧摘要**（历史根按旧 id 解释）——IG 锚定/离线验证器等跨系统互操作消费前，该字段不得留空。
- **外部完整性分工（不变）**：内部链不自证对外可信；锚定/撤销/存证=D22（IG 消费本条输出），DC 不引入签名机制。

**3.5 追加纪律与保留**：seq 单调、append-only 触发器、retention 声明制同 v0.2。

## 4. Idempotency（幂等矩阵）

- 表同 v0.2，两处修订：**迁移步骤行展开为四态语义**（P1-2，入契约不靠测试兜底）：step 不存在→执行；存在且 checksum 相同→跳过；**存在但 checksum 不同→fail-closed 停止迁移**（防历史迁移文件被静默改动）；step 序列缺失/异常→停止并人工诊断。
- **usage 结算行改确定性原语**（P1-3，业务状态机归 BL）：幂等键必须识别**同一结算效果**；partial 与 final 不得双重结算；provider 效果不可判定时进入 UNKNOWN/对账，禁止以时间戳造新键绕重放保护；（authorization_id, provider_request_id）仅为候选键位，且须处理"重试换新 request_id"场景的效果同一性判定（归 BL 裁决）。
- 键推导规则同 v0.2。

## 5. Schema Compatibility（三层版本矩阵；v0.3 按 P0-1 重写混合版本节）

- 三层版本、兼容矩阵登记制、expand-contract、四态检查点、`--reset-database` 语义：同 v0.2。
- **混合版本组件（v0.3 定形，声明与证据强度逐一对应）**：
  **真实防线（全部控制流证实 [CONFIRMED]）**：① **dispatch 路径**构建身份五字段/四元组双向相等（resident↔宿主 mc↔helper；`resident-control.ts:303-310`、`resident_control.go:126-157`、`private_dispatch.go:109-187,232-234,493-494,544-545`）——混版后果是**每 tick 拒绝（停摆）**，非带病写入；② operator 命令链三处 release 相等（`operator_bridge.go:175,383`、`operator_guard.go:19-51`、`operator_requests.go:83`；**collie 容器提交不触 operatorDeploymentGuard** [CONFIRMED `operator_bridge.go:170`]）；③ backup/restore/onboard-spine/doctor/update 私有帧携带并比对 releaseBuildID+CurrentSchemaID 且写前活体 CheckSchema（`spine_transfer.go:88-90,241,496`、`onboard_spine.go:125-128`、`ops.go:311,507`、`update_reconcile.go:21-30`）；④ 部署期配对（同 commit 双二进制、helper 精确重建、runtime 卷 release-id）——**仅在 onboard/update 时执行一次** [CONFIRMED]。
  **门禁形态结论**：版本/身份门禁是 **per-crossing 的，不是 per-write 的**。普通写 verb 全家族（withSpine≈90 入口、`__system-state` 写 action、attachment spine RPC）**不校验 live schema 与调用方同源 [NOT_FOUND，检索范围如上]**；agent 容器 mc 写与宿主/helper 构建无交叉验证（pipeline run.json 无版本字段 [CONFIRMED `effects.ts:1340-1367`]）；BlocksWork 阻断全部在 spawn/入口层（tick/dashboard/operator/culler CLI），**mc 写路径不读 journal，直接 exec helper 绕过** [CONFIRMED]。
  **实施义务登记（v0.3.1：G-A..G-F，按 R-01/R-02/复审 2.3/2.4）**：
  - **G-A** 普通写事务 live-schema 门禁：写事务开库校验 `meta.schema_id==CurrentSchemaID`（覆盖普通 verb/`__system-state`/attachment RPC/sections/reset 路径的 schema 面）。**闭合范围限定（R-01）：SchemaMatch ⇏ BuildIdentityMatch**——不证明调用方与 helper 同源、不证明异构建同 schema 语义一致、不证明 helper 协议版本符合预期；本任务不承担且任何文档不得宣称其承担构建身份。
  - **G-B** agent 容器写路径构建身份（run.json 增版本字段+容器侧比对；触 launch envelope schema 版本，F0a/F1 设计任务）。
  - **G-C**（复审 2.3 拆分）：**C-i 架构边界决策**——BlocksWork 定位为入口层阻断（tick/spawn/UI/operator），非数据库写事务门禁；此定位本身走 ADR。**C-ii 残余风险接受**——"更新期间直接 exec helper 的写入仍可能发生"的接受，**只能由 Acceptance Authority 按 RR 流程逐条签署，架构审查意见与实施任务关闭状态均不得替代**。
  - **G-D** reset 写入门禁（G-A 覆盖证明后可并实施，见合并规则）。
  - **G-E** onboard sections 程序性顺序→门禁化（ditto）。
  - **G-F**（R-02 新增）**宿主 mc→helper 普通写路径的构建身份门禁**：覆盖 withSpine 委托 verb、`__system-state` 写 action、attachment spine RPC 及其他直接 exec 写入口；必须**证明**调用方/执行方构建身份如何验证，**或**由 Authority 签署接受未验证的残余风险。机制不设前提（逐写校验或同等保证的架构控制均可）。**不得并入 G-A**（证明性质不同，R-01）。
  **合并规则（复审 2.4）**：G-D/G-E 在证据证明被 G-A 完整覆盖后方可合并实施工作，**不得预先宣布自动闭合**；G-F 与 G-A 不合并；全部挂 F0a/F1 与 AM §4b 任务登记。
  **限定结论（措辞不变，义务集更新）**：*当前单宿主、matched-pair 部署形态中，构建身份协商、schema 检查与更新停摆机制分别约束特定的进程交互、数据库访问与更新窗口，覆盖范围及组合边界以上述 E1 为准；G-A..G-F 闭合前，未被逐一覆盖的运行时组合（普通 verb、agent 容器写、直接 exec）**不得宣称已排除混合版本写入风险**。* 多实例/滚动升级/组件独立部署不在本条任何结论范围内（另行 ADR）。

## 6. Capacity Envelope

同 v0.2（Provisional 基准+绑定纪律 (a)-(e)+WAL 边界声明）；补充：G-A..G-F 闭合后需复测一轮包络（门禁本身是写路径成本；v0.3.2 修正 §6 漏 G-F 的文档内不一致）。

## 7. Backup & Recovery

- 7.1/7.2/7.3/7.6 同 v0.2（RPO/RTO=Provisional，depends_on O3）。
- **7.4 密钥轮换契约责任（v0.3.1 按 R-09 改为双事件协议）**：① 旧备份**永远用其生成时的密钥**（密钥环：key_id×覆盖时段×指纹，Class A 事件）；② **轮换生效=双事件绑定**：`rotation_prepared`（登记拟用新 key_id，此前备份继续旧钥）→ 新钥**完成一次成功备份并通过完整性检查** → `rotation_effective`（绑定 backup_id+备份摘要+key_id）；**生效时间=rotation_effective 的 recorded_at**——杜绝"已宣布新钥生效但无一备份证明其可用"的中间态；③ **历史可恢复性=轮换的强制验收**：每次轮换触发旧钥抽样恢复演练（§7.7 关联），未通过则轮换回退（`rotation_reverted` 事件）；④ 旧钥销毁=无存活备份引用+Authority 批准（销毁本身入事件账本）；⑤ **轮换失败不阻断备份**（双钥可读续存），阻断的是"轮换完成"声明。机制实现归 O1 ADR；本节冻结以上五条验收边界（P2-3 历史恢复测试与本条合并考虑，但**不取消独立的旧钥恢复证据**）。
- **7.5 恢复验证（v0.3/P1-6）**：integrity_check+聚合根抽样 [固化/新增同前]；**"链连续性"在 §3 终形裁决前仅为目标验收要求，不是现存恢复能力**——恢复程序不得依赖未裁决的链生成/验证规则（A6 呼应）。
- **7.7 恢复演练（v0.3/P1-1 修）**：三层证据结构同 v0.2，**字段集不再在本文列举**——验证记录 schema 唯一来源=**TM §7.1**，所有必需字段完整存在；场景不适用字段按 TM 的空值/适用性规则处置，**禁止删除字段列**（防各消费者各自实现记录变体）。演练 PASS 作用域限定同 v0.2；`diagnostic_events` 仅为索引。

## 8. Test Strategy

同 v0.2，另按 AM P2-4：§7.1 记录按**验证活动/测试批次**生成（批次规则=一次 lane 运行或一个演练任务=一条记录，聚合断言入 payload；每单元测试一条=自毁，排除）；六维错误语义登记表生成测试（F1）；G-A 闭合后混版拒绝路径进 boundary lane（F0a 任务：模拟宿主/helper 异构建，断言普通 verb 行为按 G-A 设计定性）。

## 9. Acceptance Criteria

v0.2 的 A1–A7 保留，修订/新增三条：A5 锚点复核挂靠 AM（含本文 §5 全部新增 CONFIRMED 行号，冻结前由复审方**独立读源**复核——grep 不算）；**A8（v0.3）**：§5 混合版本节的一切声明与审计矩阵逐行对应（无矩阵行=无声明）；**A9（v0.3.1 更新）**：G-A..G-F 在 AM §4b 各有实施任务行与验收阶段（已登记，R-06 闭合），且在任何一项闭合前，UI/文档不得出现"混合版本写入已被阻止"式声明（§10-A7 应用）；**DC 的 CONTRACT_FROZEN 不代表任何 G 任务完成**。

## 10. 未决与依赖

裁决收敛（v0.3.2）：F3 保护对象/序列/排序键已定；**残余裁决点=F0b 设计件三件套**（serializer 字段表+跨实现测试向量、algorithm_id 根算法冻结、§3.4 摘要关系的复审批准确认——语义层⑧条已在 DC，F0b 不得改语义）。O3 入场点=§7.3/7.4/7.7 验收；O1=§7.4 机制+G-B/G-F 容器侧材料；BL 依赖 §4 原语（DC 只冻结存储与幂等原语，**效果同一性判定归 BL**——复审 6.2 边界确认）；DT 依赖 §4 PR 行。F0a 前置：§1.3 持锁范围人工扫描、G-A..G-F 任务行（AM §4b，已登记）。

## 11. 修订映射

**a（v0.1→v0.1.1）**：④阈值裁决落 §6/§7.3。**b（v0.1.1→v0.2）**：自查八项（见 v0.2 文本原注）。**c（v0.2→v0.3）**：外部复审 P0×3/P1×6 首轮（§5 收敛+G-A..G-E、§3 提案化、§1.3 双表、P1 六项）。**d（v0.3→v0.3.1，复审 R-01..R-12 逐项）**：R-01/02→§5 G-A 范围限定+新增 G-F+合并规则；复审 2.3→G-C 拆 C-i 架构决策/C-ii Authority 专属接受；R-03→§3.2 原子审计不变量+映射表；R-04→§3.1 四概念分离、**seq 定为链排序键**（策略槽三选一作废）；R-05→§3.4 摘要关系/genesis/序列化五要求/检测义务/验证器协议形状；R-06→A9+AM §4b 任务登记；R-07→§3.3 supersedes 分治；R-08→admission effective_at/recorded_at；R-09→§7.4 双事件轮换协议；R-10→telemetry digest 声明收紧；R-11→§3.3 事件 schema 定义权与 TM §7.1 边界；R-12→FACTS-001 矩阵分列修正。**e（v0.3.2，复审 §5 五项+三.1/三.2）**：§3.2 迁移原子性契约（可恢复事务边界/确定性中间态，禁"状态更新而证据缺失"分裂）；§3.4 规范化序列化语义层 ⑧ 条入 DC（F0b 只管字段表/编码/向量且禁改语义）；整根消失检测两层界定（aggregate_open 不自证消失，层二依赖锚定/独立登记，未就位禁称具备能力）；根算法 algorithm_id 版本化+跨互操作前不得留空；prev_hash 冗余绑定显式承认+双关系核验+禁排除出哈希域；§6 G-F 补漏；§10 残余点收敛为 F0b 三件套。**f（v0.3.2.1，短终审登记清扫，零语义）**：标题版本号统一；事实底座 bullet G-A..G-E→G-A..G-F；状态行更新 RECOMMENDED。
