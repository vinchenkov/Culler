# S-Horizon · MASTER ARCHITECTURE（v3.3 · 冻结候选 · 待 ARCHITECTURE_FROZEN 批准）

- 状态：**v3.3（v3.1 ⑥④裁决；v3.2 D31；v3.3 D31② 扩充 Degraded Operation Authorization——THREAT-MODEL v0.2.3 复审引入）· Architecture Frozen 待批准**（三档冻结定义见 §5）。本文取代 `../../ITERATION-ROADMAP-2026-11-19.md`（v2，已标 superseded）成为唯一权威决策载体。
- 证据基线：Culler `b22df40`（E1 取证见附录与两份 ANALYSIS 文档）；LoopForge `451d38c`（仅作设计参照，其结论按"未复核其实现质量"对待）。
- **立身原则（防审查过度）**：v3 的全部工作是**把 Culler 已验证的内核契约化、把缺失的治理与恢复能力规格化**——不是重建。任何"看起来更企业级"的新概念若与既有 E1 机制重叠，以既有机制为基准做增量声明；重写已验证机制需 ADR（§9 护栏 3）。

## 1. 最高不变量（Six Invariants，任何文档/代码/ADR 不得与其冲突）

1. **Spine Authority**——spine（SQLite）是唯一权威事实源；mc 是唯一写入入口。
2. **Single Writer Semantics**——所有写遵守唯一写者事务契约（flock/BEGIN IMMEDIATE/WAL/CAS 围栏/幂等收据），契约文本见 `DATA-CONSISTENCY.md`。
3. **Autonomy Preservation**——Control Plane（VISION/Strategist/Arbiter/Decide/执行/验证/进化）的决策不被 Governance Plane 替代；治理面只能**约束与拒绝**，不得创造生产需求或篡改调度语义。
4. **Evidence First**——重要状态变化必须有可追溯证据（事实、判卷、签署、结算）；无证据不得宣称完成；派生事实与原始证据永远可区分。
5. **Least Privilege**——人、Agent、服务、管理员均无隐式扩张权；授权是显式、有界、可撤销、可审计的。
6. **Recoverability**——一切重要持久化操作有书面故障语义：崩溃点、恢复输入、收敛条件；不可逆变换配快照路径。

## 2. 产品身份与边界

**是什么**：企业级、单人与多人团队协同皆可用的**自治软件工厂**——自托管私有化部署；从 VISION 出发的自主选题/执行/验证/签署/交付产线，叠加企业一致性、安全、治理、证据与恢复能力。
**差异化一句话**：在具备企业级安全/权限/审计/可恢复性的前提下，**仍然保留一个能自主规划、执行、验证并受控进化的软件生产内核**（不是又一个工单驱动的工作流平台）。

**双平面（词汇表冻结）**
- **Control Plane**：VISION、任务规划、角色调度、Decide、Agent 执行、质量验证、受控进化。
- **Governance Plane**：身份、授权、签署、预算、配额、审计、合规、运维。
- 交互契约：有版本、有身份、有证据的授权与事件；治理面**拒绝权完整、创造权为零**（不变量 3）。

**软件生产生命周期（七阶段 × 三终态）**

| 阶段 | 归属 |
|---|---|
| 1 Requirement Intake（来源/版本/可信度/归属；VISION 与 Worksources 双入口） | Control |
| 2 Planning & VISION（分解/依赖/优先级/预算约束输入） | Control |
| 3 Execution（Agent/角色/模型/沙箱/工具/上下文冻结） | Control |
| 4 Verification（测试/审卷/质量门/独立验证/证据归档） | Control |
| 5 Acceptance（签署/拒绝/返工/重验） | **Governance 授权、Control 记录** |
| 6 Delivery（push/PR/merge；事务契约见 DELIVERY-TRANSACTION） | Control 执行、Governance 约束 |
| 7 Post-Delivery（**Production Outcome Observed**，见下） + Evolution（受控进化） | 信号治理 / Control |

- **三终态严格分离**：`Run Completed`（执行完成）≠ `Delivery Accepted`（签署+交付成功）≠ **`Production Outcome Observed`**。
- 第三态是**外部信号的观察结果，不是 S-Horizon 自己执行的状态迁移**：信号摄入（CI failure/revert/issue 复发/外部部署结果/运行时健康）→ Production Outcome Evidence → 喂 Worksources 与 Evolution。**无信号时状态恒为 `UNKNOWN`/`NOT_OBSERVED`，永不默认成功。**

**非目标清单（改动需 ADR，§9 同族）**：多租户 SaaS（单组织私有化边界 D16）；双方言抽象层（D17）；远程 runner/多机执行（D6）；批准产生需求（D18）；估算值参与预算闸门（D12）；部署/发布编排——工厂止于合并，CD 是别人的（D24）。

## 3. 决策台账（D1–D31，含 v2→v3 修订标注；D30/D31 为表后正文段）

| # | 决策 | 修订 |
|---|---|---|
| D1 | 基座=Culler（C2）；LoopForge 为设计捐赠者；模式重写为主、自包含件白名单移植（gitegress 类） | |
| D2 | 定位=企业级单组织自托管软件工厂（单人/多人皆可）；两条身份红线（产线不工单化；"拥有工厂"=自托管+审计+主权数据） | v3 按 Q25-B 精化 |
| D3 | 部署主形态 x86_64 Linux headless；arm64/Mac 兼容保留 | |
| D4 | 模型接入=商业 API+私有推理（vLLM/SGLang/Ollama）三层 Provider Registry；agent 永不控制目的地 | v3 加 DestinationPolicy 强制（D-G5） |
| D5 | 数据库=SQLite spine 保留；Mongo 否决 | 措辞并入 D17 统一表述 |
| D6 | 远程 runner 移出路线，ADR 冻结（形态 C 为唯一可接受设计；多部署=算力替代方案） | |
| D7 | 不变量保持：spine/mc/DB 状态机/凭证代理/Decide 纯函数 | 唯一写者升级为可验证契约（→不变量 2 + DATA-CONSISTENCY） |
| D8 | Decide wire 冻结：成本闸门走 2b（authorize 下发额度）；16→17 缓行 | **authorize = 同事务原子预留**（D25 细化） |
| D9 | 迁移器：账本表进 v1 基线；OnboardSpine 内嵌；journal verify 窗口；四态检查点 | |
| D10 | v1 基线极简；usage 表=首迁移 v1→v2（含 blob 回填） | 回填须数据正确性证明（D-G6.3） |
| D11 | push 宿主直跑；远端权威四规则（本地权威/自证 pushed_sha/只快进+lease/PR 先只进展示） | 补 Delivery 故障矩阵（DELIVERY-TRANSACTION 契约）；宿主 git 面验证入 T2 清单（D26） |
| D12 | 未知用量=`unavailable`，不估算；闸门只认已确认 | +`UNKNOWN/RECONCILIATION` 预算态（D25）；+价格快照（D-G7.2） |
| D13 | Q15/16/18/19 沉默生效 | |
| D14 | 改名 **S-Horizon**；CLI **`shz`**；R1/R2 双层迁移（§2R 见 v2，白名单枚举为验收条件） | |
| D15 | MIT 派生合规：保留上游版权+LICENSE、README derived-from 致谢；`mc`/`spine`/`MC_*` 内部代号不改 | |
| D16 | 企业形态=Q25-B：私有化单组织+采购合规姿态；多租户 SaaS 显式非目标 | |
| D17 | **统一表述**：SQLite 是当前唯一生产实现；PostgreSQL 是满足晋升门后**允许启动 ADR 的候选目标，不构成预先承诺的迁移计划**。晋升门四条件+量化指标（CAPACITY ENVELOPE，DATA-CONSISTENCY §6）。量化指标一律为 **Provisional Observational Baseline**：必须绑定测试工作负载定义，记录硬件/数据规模/并发数/测量窗口，区分持续性退化与偶发尖峰，由实测数据触发；**达阈仅启动 ADR，不自动批准迁移** | 措辞销账（矛盾 R-2）；v3.1 阈值裁定 |
| D18 | 签署层骨架=所有权分层：VISION/产线配置归 owner；签署权可委派；SoD 可配置非默认；超时升级；批准永不产生新需求 | 签署证据模型见 D-G3（不变量 4 的具体化） |
| D19 | 企业功能序：签署→准入→审计→metrics→OIDC；chargeback 搭 P2b；SBOM/Worksources 入 P6 | 拆入 P4.0/P4.1（D-G4，顺序不变：身份基础先行） |
| D20 | 版本谱系 v0.1.0 重启；旧部署一次性迁移 `shz migrate-home`（路径+Keychain 搬迁工具，**无**版本窗口兼容层） | fallback 矛盾销账（R-5）：迁移工具取代 fallback |
| **D21** | 契约体系=**7 份**+Acceptance Matrix；BACKUP-RECOVERY 为 DATA-CONSISTENCY 一级章节（不增第八份） | 本轮销账（矛盾 C-1） |
| **D22** | 锚定协议四要求：签名密钥与 spine **不共享安全域**；checkpoint 序号单调；新 checkpoint 链于前者（防分叉/回滚）；定期复制至独立信任域支持离线验证。sigstore 可选路径**只发布签名 Merkle root+验证元数据**，永不发布签署内容/项目名/内部事件 | 本轮新增 |
| **D23** | 管理员默认权限矩阵（健康指标=允许/治理元数据=按策略/Team 业务内容=拒绝/敏感日志=显式授权/临时接管=Break-glass/改授权策略=独立管理权限）+ **Break-glass 协议**（目的申明、最小数据范围、限时、强制审计、可配双人批准、到期自动撤销） | 本轮新增 |
| **D24** | 第三终态=Production Outcome Observed（外部信号观察，无信号恒 UNKNOWN/NOT_OBSERVED）；工厂边界=合并为止 | 本轮新增（Q32 定名） |
| **D25** | 预算双阶段账本状态机：`RESERVED`（原子预留，绑 launch intent）→`DISPATCHED`（run 实际启动）→`SETTLED`（实际量结算）/`RELEASED`（确认未耗释放）+ `UNKNOWN/RECONCILIATION`（用量不可得≠零耗）。预留成功、启动前崩溃=经持久化 launch intent+lease recovery 确定性收敛。token 预算与货币成本分维管理；P2b 建账本与预留语义，P5 启用硬闸门 | 本轮新增（G02+用户状态机） |
| **D26** | **全容器化≠已证明 T2 隔离**：T2 默认身份继承既有 enforcement（双 uid/挂载司法权/凭证代理/pids 限），但 F0/P2a 必须**验证**网络、mount、凭据、docker socket、宿主敏感路径等实际边界并出验收记录（沙箱信任分级=T0/T1/T2 参数化，非新建体系） | 本轮新增（G07 修正） |
| **D27** | 三档冻结：`Architecture Frozen`（决策冻结，新需求走 ADR）→`Contract Frozen`（实现必须循约）→`Implementation Verified`（阶段 Exit Criteria 达成才可进下一档）；每阶段 Exit Criteria 书面化（§4） | 本轮新增 |
| **D28** | 最高不变量六条（§1）进 MASTER-ARCH 顶格；其中 **Autonomy Preservation** 为企业化演进的防反噬条款 | 本轮新增 |
| **D29** | 执行顺序：①v3 Master ②DATA-CONSISTENCY 草稿 ③其余六契约草稿 ④Acceptance Matrix ⑤F0a 启动包 ⑥F0b 逐份契约批准（无隐式冻结）；O1/O2/O3/O9 保持开工事实位不因文档整理而改变状态 | 本轮新增 |

**Provider/安全侧细化（源自外部审查、经 E1 裁定，统称 D-G 系列，落点见 §6/§7 与对应契约）**：G01 写者=契约化+压测非重设计；G02→D25；G03→D22；G04→P4.0/4.1 拆分+词汇冻结（D30 见下）；G05 DestinationPolicy：解析后 IP 校验+重定向复验+per-entry `allow_cidrs`（私有端点显式声明内网目的地）+代理绕行禁止，P2a 强制验收；G06→DATA-CONSISTENCY §7；G09 事件契约统一规范、明确不引入 event sourcing/Kafka；Model Compatibility Profile（声明≠表现：capability_declaration vs verification + 时效 + known_limitations，验证非永久）；Provider 故障治理（超时/退避/熔断/限流/健康评分；fallback 受能力注册表约束；可重试与不可重试调用分类）；Pricing Snapshot（usage↔price_version；evidence/pricing/settlement 三层+unsettled 态）。

**D30 身份词汇冻结（P4.0 地基，v3.1 裁决修订）**：Organization=隐式唯一（`tenant_id` 恒=部署 UUID，**不建 org 表**）；Team=权限/共享/配额/chargeback 最小单元（单组织≠单团队）；Project 归属唯一 Team；Actor=统一主体 ID 空间（sub-type：user/service-principal/agent-run）；Principal/Session/Capability 按六元组（Principal/Actor/Subject/Action/Context/Decision）。授权三问冻结（v3.1）——**核心裁决：禁止的是 Authority Delegation，不是 Task Delegation**：① Agent 不得将自身持有的**身份、Capability 或授权权柄**再委派给另一 Agent（防权限链失控与主体冒用）；② Agent 将**任务**交给其他 Agent 执行、子任务拆分、角色调度及 Agent 间协作**允许**——属 Control Plane 自主范围（Autonomy Preservation）；③ 接收方必须以**自身 Actor 身份**执行并独立通过授权检查；任务委派**不得扩大**接收方权限、不得继承委派方未被明确授权转移的权柄（防间接权限提升）。撤权生效模型："启动时身份上下文冻结 + 执行时按调用点复验 + 敏感动作重授权"（provider-proxy per-request liveness 是既有先例 [E1]，人类 session 撤销走服务端会话表）；run 权限生命周期按该模型定义于 IDENTITY-GOVERNANCE。本裁决同时保护：Autonomy Preservation、Least Privilege、Swarm 协作、Capability 独立验证、撤销与审计——作为 D30 正式补充，供身份契约/威胁模型/Provider 安全共同引用。

**D31 跨契约证据与风险语义（v3.2，THREAT-MODEL 复审引入）**：① 一切契约与验收条款遵循三命题分离——**机制存在 ≠ 配置正确 ≠ 攻击有效性已证**；"配置存在"不得作为"有效性"的证据，验收不得以"未验证但机制存在"判过（UNKNOWN≠PASS）；② 风险状态词汇统一为四值枚举 `PENDING_ACCEPTANCE / ACCEPTED_RESIDUAL / REVOKED / CLOSED`，**风险状态↔运行语义映射以 THREAT-MODEL §8.0 为跨契约共享词汇**——后续契约（IDENTITY-GOVERNANCE / PROVIDER-SECURITY / DELIVERY-TRANSACTION / BUDGET-LEDGER）涉风险或暴露面的运行策略时**引用 §8.0 并指明本契约的 fail-closed/降级授权落点，不得重定义**；**Degraded Operation Authorization 是独立于 Risk Acceptance 的授权（五要件绑定：具体风险×范围×补偿×有效期×撤销条件；补偿失效/过期/触发即自动作废并 fail-closed；审查意见/AI 判断/历史事实不构成授权），定义见 THREAT-MODEL §8.1，其授权主体与签署/撤销机制由 IDENTITY-GOVERNANCE 裁决**；Contract Frozen 冻结治理规则、不构成风险接受或运行许可；③ Acceptance Authority=部署所有者指定的具名自然人/组织角色，审查意见与 AI 过程不构成签署（与 D23/§8 前言同源）；④ 平台安全状态声明（UI/报告/API/客户材料/自动交付说明）的证据绑定规则=THREAT-MODEL §10-A7，跨文档通用。

## 4. 阶段依赖图与 Exit Criteria（D27）

```
F0a(代码)  Linux/systemd/凭据后端/备份能力落地/发布完整性基线(shasum 验证器自测)/R1-R2 品牌
F0b(契约)  MASTER（待 ARCHITECTURE_FROZEN）→ 七契约逐份批准(从 DATA-CONSISTENCY 起) → THREAT-MODEL → 其余   ── 与 F0a/F1 并行
F1         迁移器+Writer/Event/Idempotency 契约落地+容量包络基线测量+兼容矩阵机制
P2         P2a-L2(config-only，可与 F1 并行) → L3 能力注册+P2b 账本/预留(依赖 F1)
P3         push→PR→对账（DELIVERY-TRANSACTION 契约冻结后开工）
P4.0       身份基础（主体/会话/capability/membership/撤销/break-glass 协议）
P4.1       治理功能（签署+锚定、SoD、准入配额、审计链+导出、metrics/OTel、OIDC 收尾）
P5         预算硬闸 + chargeback + 受控进化（canary/rollback 语义）
P6         SBOM/attestation、Worksources、共享定义治理、容量预测、故障演练自动化
```

每阶段 Exit Criteria（摘要，全文进 Acceptance Matrix）：**F0a**=干净 amd64 服务器端到端（安装→fake-harness 全流水线→批准→合并）+ `shz` 用户面零 Culler 残留（白名单除外）+ 恢复演练自动化跑通一次；**F0b**=七契约 Contract-Frozen + Acceptance Matrix v1；**F1**=v1 基线冻结 + 首个真迁移（v1→v2）在带回填证明的实战完成 + 多进程写压测达容量包络声明；**P2**=L2 端点经 DestinationPolicy 验收 + usage/预留账本对平测试全绿；**P3**=故障矩阵全绿（含 push 超时歧义、PR 记录丢失幂等恢复、hook 隔离验证）；**P4.x**=身份→签署→锚定离线验证闭环演示 + break-glass 审计演练；**P5**=预算在并发 authorize 压测下零超支承诺成立（或书面声明允许越界一次请求的精确语义）；**P6**=工件可证签名链 + Worksources 不污染 priority 规则。
**规则**：代码完成/测试通过≠阶段完成；Exit Criteria 未书面核验，不得宣布 Implementation Verified。

## 5. 冻结等级与生效（D27）

- 本文当前=**v3 冻结候选**。你对 §1–§3 批准 → **Architecture Frozen**（此后新需求走 ADR）。
- 单契约批准 → 该契约 **Contract Frozen**（无隐式冻结，D29-⑥）。
- 阶段 Exit Criteria 达成 → 该阶段 **Implementation Verified**。
- 三级独立推进，**禁止**用"文档冻结"冒充"实现达标"（成熟度等级：Designed→…→Production Verified，不得跨级宣称）。

## 6. 安全基线条款（跨契约统一，细节归 THREAT-MODEL/PROVIDER-SECURITY/IDENTITY-GOVERNANCE）

1. STRIDE 威胁模型为 F0b 产物；边界至少覆盖：Agent→Provider、Agent→Sandbox、Sandbox→Host、Agent→Credentials、Repo→Host（恶意配置/hooks）、User→API、MCP→Platform、Admin→Database、Update→Runtime。
2. **T0/T1/T2 执行信任参数化**（D26）：T2 为一切外部来源仓库/未知脚本/第三方 MCP 的默认档（无网络或目的地白名单、无凭据或最小临时凭据、独立沙箱、宿主敏感路径禁达）；**任何一档的隔离声明必须有验证记录**，继承机制不等于继承证明。
3. DestinationPolicy 链（L2 条目）：配置存 allowlist → 解析 → **解析后 IP 校验** → 拨号固定到已验证 IP（防 rebinding）→ 重定向复验 → per-entry 允许网段声明；gitegress 为移植母本 [E1]。
4. 签署锚定协议（D22 四要求）；审计哈希链 + `prev_hash`（activity/signoffs）。
5. 凭据纪律（全局，含代码层）：凭据只从环境/密钥服务读取；**源码、示例、测试不得写入可用凭据字面量**；一切外部输入 SQL 参数绑定，禁止拼接组装（Review+CI 门）。
6. break-glass（D23）为管理员接触业务数据的唯一旁路，且其使用本身是高审计密度事件。

## 7. 一致性基线条款（细节归 DATA-CONSISTENCY）

1. Writer 契约三层：OS 串行（WAL 单写者+flock dispatch 域+BEGIN IMMEDIATE 全域）、业务围栏（CAS/lease/generation/replay 收据）、领域幂等键矩阵（已形式化：dispatch/attachment/outbox；待补：delivery/usage/migration/break-glass grants）。
2. 事件契约统一字段规范（event_id/type/version/aggregate/actor/causation/correlation/idempotency_key/occurred_at/recorded_at/payload_digest）；原始事件永久保留，派生事实可重建（回填不丢弃原则）。
3. 三层版本矩阵（binary/schema/**data-contract**）+ expand-contract 纪律 + **不可逆数据变换不提供 down，快照恢复是唯一退路**（明确写入迁移契约）。
4. 容量包络：写锁等待 P95、busy 重试率、WAL 尺寸/checkpoint 频率、mc spawn 延迟、spine 体积增速、per-launch 观测数——P1 建基线，D17 晋升门以数据说话。
5. 预算状态机（D25）；usage evidence 与 pricing snapshot 与 settlement 三层分离。
6. BACKUP-RECOVERY 为 DATA-CONSISTENCY 一级章节：在线一致备份（既有 backup API [E1]）、频率/WAL 归档策略、RPO/RTO 书面目标、备份加密与密钥生命周期（§6.5 同纪律）、恢复验证（restore+integrity_check+摘要比对）、**定期自动恢复演练**、迁移前保护（旧二进制快照时序 Q10-A）、复制至独立信任域（与 D22 锚定共用机制）。

## 8. 矛盾销账清单（v2 十条 + 本轮一条）

C1 七契约 vs BACKUP-RECOVERY → D21 解决；R1 §1 排序矛盾 → §4 拓扑（L2 config-only 并行声明）；R2 D5/D17 措辞 → D17 统一表述；R3 tenant 语义 → D30；R4 签署先于身份 → P4.0/4.1 拆分（顺序不变、依赖修正）；R5 D20 vs fallback → `shz migrate-home` 工具取代；R6 改名白名单 → R1/R2 验收枚举化（LICENSE/致谢/`mc`/`MC_*`/`spine`/容器名/refs）；R7 审计 vs DBA → D22 外部锚定；R8 writer 并发契约 → §7.1；R9 P2b/P5 边界 → D25；R10 供应链提前 → F0a 完整性基线（digest 验证器自测），cosign/SBOM 留 P6。

## 9. 护栏与风险声明

1. **节奏风险**（v2 起）：F0a+F1 仍是重投入前置期——以 F0b 契约与 R1 品牌交付物提供可见进展对冲，但**不得**为可见性砍契约深度。
2. **定位漂移**（v2 起）：非目标清单为护栏，越界走 ADR。
3. **审查过度风险（v3 新增，本轮共识的浓缩）**："不要把这轮架构审查变成重新建设 Culler"——外部审查与新增概念（Writer Service、事件总线、预算微服务之类）若与 E1 机制重叠，一律先证明既有机制不成立，才允许替换设计。
4. **治理反噬（企业化长期风险）**：为审批改任务状态、为配额改调度语义、为审计把 Agent 行为工单化=三重具体形态；防线=不变量 3 + D18"批准不产生需求" + D30"撤权不追溯篡改已冻结上下文"。

## 10. 开放项（开工事实位，D29 保护）

O1 凭据后端选型（age+口令/systemd-creds/Vault 接口，ADR-0）；O2 GPU 服务器内部 TLS 能力（决定 `insecure` 字段存废）；O3 服务器发行版与 Docker Engine 版本；O9 标识符域名根（launchd 前缀/OIDC issuer/更新通道共用）。新增：O11 锚定密钥宿主（独立签名节点 or 组织 KMS/HSM 选型——D22 的落地前提，随 F0b IDENTITY-GOVERNANCE 批准前定）。

## 附录 · 关键证据锚点索引（E1，供契约引用）

写者/事务：`mc/substrate/substrate.go:43-65`、`mc/verbs/dispatchverb.go:195-209`、schema.sql 头注；围栏/CAS：`mc/domain/lease.go:77-161`、`dispatchseam.go:911-936,234-247`；重放收据：`dispatchseam.go:530-536,1176-1201`、schema.sql:682-707；备份/恢复：`mc/verbs/spine_transfer.go`（backup API/五重可见性闸/restore 拒覆盖）、`broker_backup`/hourly chore；迁移落点：`mc/verbs/onboard_spine.go:210-219`、`mc/cmd/mc/redeploy_check_prod.go:71-132`、`mc/deployment/update_journal.go:19`；per-request liveness：`resident/src/provider-proxy.ts:584-586`；批准绑版本防陈旧：`mc/verbs/packet.go:51-82`、`delivery.sql:471-479`；quiet/merge_after：`delivery.sql`、`mc/dispatch/repository.go:243-248`；Keychain 卫生：`mc/deployment/host_secret*.go`、`mc/distribution/client.go:314-338`；gitegress（移植母本）：LoopForge `internal/gitegress/gitegress.go:36-47`；run 快照冻结指令：`mc/verbs/brief.go`（"Each run keeps the instructions it received"）。
