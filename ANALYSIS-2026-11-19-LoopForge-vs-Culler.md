# LoopForge 设计评审 + Culler 对比分析

- 研究对象：Tencent/LoopForge，本地克隆 `/tmp/loopforge-research`，基线 `HEAD 451d38c`（2026-11-19，main 可见 50 commits 浅克隆窗口，PR 流 #778→#1068；v0.4.0 发布于 2026-11-17）。
- 对照物：Culler（见同目录 `ANALYSIS-2026-11-19.md`，基线 `b22df40` 0.1.0）。
- 方法：与 Culler 同一套四维一手源码取证（架构引擎 / 安全沙箱 / 工程成熟度 / 企业级能力），四个后台代理并行精读，未运行任何测试/服务；[代码]/[测试]/[文档]/[推断] 为证据级别，[测试] 指测试源码写了断言而非本次执行过。

---

## 1. 执行摘要

LoopForge 是腾讯开源的**自托管团队级 coding-agent 交付平台**（Go 单二进制 api/worker/all 三角色 + MySQL 8.4 + 内嵌 web/React chatbot + Docker 沙箱），双形态：**Chat**（Main Bot 按需开"对话工作区"沙箱、把项目角色物化为 CLI 原生 subagent、产出 PR）与 **SOP**（可视化 DAG 引擎：Agent/Script/Human-gate/Sub-SOP/Preview 五类节点、产物契约、返工策略），编排 10 种 agent CLI（CodeBuddy/Codex/Claude Code/OpenCode/Cursor/Antigravity/Kimi/Pi/DeepSeek Harness/ZCode），带需求→PR/MR→合并检测闭环、token/成本报表、定时任务与共享模板市场、MCP API 面。3.8k stars，~10 PR/天、7+ 作者，明显是腾讯内部平台（工蜂/TAPD/iOA/CodeBuddy 痕迹遍布 `internal/gongfeng/`、`User.StaffID`）的开源外溢。

**总评**：这是一个**产品面宽、工程带宽大、内核纪律中等**的系统——测试:产品 1.09:1（3,627 用例）、覆盖率 84.8%/85% 双门 + 路由命中 85% 门 + 门禁脚本自身有回归测试、发布前置证明链（tag 必须在 main 且 CI 绿、compose 双 pin、多架构按 digest 冒烟）、gitleaks/govulncheck/真实 CLI 钉版本进 CI、README 对 docker.sock 与"可信团队非多租户"的自述诚实得罕见。它的内核是**过程式的**：状态机只在应用层（VARCHAR 列、无 CHECK/FK/触发器）、无迁移器（5 组迁移前缀碰撞、手跑 SQL）、无 `-race`/无 fuzz/无属性测试、审批不留名、gate 结果读自 agent 可写的工作区（自我申报）。安全姿态与 Culler 恰好互为镜像：**LoopForge 假设用户可信、agent 内容部分可信但环境不可信地弱处理；Culler 假设 agent 完全不可信、操作员单独一人无需用户体系**。

**对比一句话**：Culler 是"深内核、窄界面"（单机单人，把正确性/凭证/审计做到同类最强）；LoopForge 是"宽界面、实用内核"（团队多人，把交付闭环/集成生态/成本报表做到同类最全）。互补性极高，且各自最缺的恰好是对方的强项。企业标准下两者都不合格但病因不同：Culler 缺"人和规模"，LoopForge 缺"归因和闸门"。

---

## 2. LoopForge 是什么（定位与解决的问题）

| 叙述声称 | 代码核实 | 判定 |
|---|---|---|
| "self-hosted workspace where your team ships software with coding agents"（README:37） | 多账号 + 项目成员 + 审批收件箱 + 报表属实 | 真，但"team"=受信内网小团队（deployment.md#Accounts：登录即全实例可读） |
| SOP 五类节点、产物契约、human gate | `domain/workflow.go:39-80`、`engine/node_execute.go`，与 README:154-161 一致 | 真 [代码] |
| "Observable and resumable"（README:68） | run_events/telemetry/artifact 按 attempt 不可变快照、租约接管续跑有测试 | 真，但"resumable"的边界诚实：节点从不中间 push，主机死+未推送=判丢失重跑（`run_recovery.go:50-64`） |
| "Token/cost accounting"（README:183） | 无模型代理，用量=沙箱内 CLI 自报（hooks/OTLP/stream）→ 平台差值 → 公开牌价折算（`observability/hooks.go:16-21`、`engine/observability.go:60-80`、`docs/token-pricing.md`） | 半真：是"投入比较测算"，文档自己明说"不是供应商结算账单"（token-pricing.md:14-16）——诚实但不可作 chargeback/闸门 |
| "Docker socket …for trusted teams, not untrusted multi-tenant isolation"（README:130） | `compose.yaml:57` 确实挂 socket | 真且坦率：主动声明自己不适合不可信多租户 |
| 审批内置（"review and approval built in"） | 服务端状态机强制 + 乐观 CAS 防双批属实；但 `Engine.Approve` 不记录审批人（`run_ops.go:140-179`），decision.md 写"Approved by a human gate"匿名 | 半真：门是真的，归因是缺的 |

它解决的核心问题与 Culler 同源（一次性 agent 运行不可复现、不合规——README:63），但答案不同：**Culler 用"确定性内核 + 唯一人工审批点"回答，LoopForge 用"可复用的组织流程（SOP）+ 多人协作与交付集成"回答**。

---

## 3. 架构分析（对照 Culler 的四个层次）

### 3.1 状态权威与持久层

- **MySQL 8.4 是唯一生产权威**（`internal/repo/repo.go:1-4`，"no second in-memory implementation"——对照 Culler 的"mc 唯一开库者 + darwin 每次 docker exec 委托"，这是**真多进程共享状态**，扩展性天花板高得多）。GORM + `FOR UPDATE` 行锁。[代码]
- run 是聚合根文档：header 标量列 + `snapshot/repositories/usage_json` JSON 大列 + 子表 `run_nodes/run_events/run_telemetry/run_logs`（append-only、`(run_id,seq)`）。一次迁移=重写整行 JSON。[代码]
- **与 Culler 的本质分歧**：Culler 把状态机沉进数据库（~130 触发器 + CHECK + append-only 强制 + schema SHA-256 身份握手）；LoopForge 的 `runs.status` 是裸 `VARCHAR(32)`，无 CHECK/外键，合法迁移散在 30+ 个 Go 调用点靠围栏函数兜底（`repo/models.go:339,378`）。**Culler 被绕过应用层仍正确；LoopForge 被绕过应用层就自由落体。**
- 迁移治理双方都不及格但病因相反：Culler=不兼容即拒启+清空（安全但破坏性）；LoopForge=AutoMigrate 只加列不改 + 数据迁移 SQL 靠运维手跑、无 schema_migrations 表、无锁、无排序执行，5 组版本号碰撞（007/016/018/024/036）证明前缀无人校验。升级=stop-the-world compose 重建；**无备份/回滚文档**（全库 grep mysqldump 零命中）。[代码]

### 3.2 编排内核

- 每 run 一个调度协程：每圈从 MySQL 重读权威、检查 cancel/pause/generation、就绪节点各起 goroutine 真并行（"no wave waits for its slowest member"，`node_execute.go:58-60`）、收敛后 settle。跨进程用**租约 + fencing generation**（`FOR UPDATE` 下 `generation++`，续租失败立即 cancel 整个 run ctx，结果提交前统一过 `shouldCommitNodeOutcome` 栅栏，`repo/runs.go:1112-1177`、`node_outcome.go:37-48`）——这套围栏是**教科书级的正确设计**且有密集测试（旧 owner 迟到写全灭、双批 CAS 后者收 `ErrRunDecisionStale`、rework 不双提交、取消重跑恢复兄弟分支）。[代码+测试]
- 控制标志（cancel/pause）刻意排除出 `PutRun` 列集防 worker clobber API 意图（`runs.go:1003-1009` + `TestPutRunPreservesControlFlags`）——好设计。
- **但与 Culler 的 Decide 纯函数 + prepare/attest/commit 接缝相比缺三样**：(a) 决策不可复算/不可审计——没有"同一输入必得同一迁移"的验证面，无 canonical JSON 冻结；(b) **外部副作用无 outbox/receipt**——push/MR 由沙箱内 agent 自己完成，平台只做事后证据核验（MR-tip 相等校验 + `GET_LOCK` 合入锁），Culler 对每个 merge 用 journal-before/receipt-after 包裹；(c) 快照粒度错位——图冻结了，**角色 prompt 不冻结**（每次启动现解 catalog，`node_execute.go:189-224`），重跑历史 run 拿到的是今天的角色定义，审计语义有洞。[代码+推断]
- 失败语义比 Culler 的固定流水线灵活得多：`onFailure ∈ {fail,reject,human,loop}`、rejectTo 任意回跳、返工预算耗尽转人工不失败、verification gates 四型（command/mr/manual/submit）、Agent 必须经节点作用域 MCP 提交四态裁决（缺提交先复用 session 追问再转人工）。逃生门有类型限制（`Pass` 只能人工放行 script 节点不能放行 Agent 节点，`run_ops.go:246-248`）。[代码]
- 弱点细节：`claimJob` 调度领取非原子（进程 mutex 内读判写，两 API 实例并发 tick 有双触发窗口，`schedule/service.go:244-277`）；`updateRun` 返回 false 多数调用点 `_ =` 忽略（静默丢失窗口，靠租约 reconcile 收敛）；全实例写经进程级 `stateMu` 串行；轮询密度高（2s claim、500ms revision、SSE 实为 DB 轮询投影、5min MR sync——无 webhook）。[代码]

### 3.3 执行面与交付闭环（Culler 完全没有的两块）

- **三种执行面**：平台 worker（compose 容器内 docker 沙箱）/ 个人执行机 runner（只出站长轮询 + WSS，`desired_state` 落 DB 权威）/ native（bwrap/seatbelt）。工作区绑定宿主（first-writer-wins bind + 心跳存活则他机不得接管）。[代码]
- **需求→PR/MR→合并检测**：每需求独立工作分支；PR 发现是"两层目录扫描 + 作者/时间/tip/未关闭四条件启发式匹配"；`mode=mr` 门禁服务端核验 MR head==已推送 tip（"复用分支上早前已合并的 MR 不算数"，`engine/gate_mr.go:33-56` + 测试）；5min 轮询合并状态回写需求。`gitegress` 对自定义 Git 主机做了 DNS 重绑定安全的 SSRF 围栏（解析后校验真实 IP、默认禁内网、不跟随重定向）。[代码+测试]
- 沙箱加固参数：`--init --cap-drop=ALL --security-opt no-new-privileges`（NNP 有！Culler 的 agent 容器因 setuid 反而没有）——但**默认无 `--user`（平台 root → 沙箱 root）、无资源限制（全仓 grep 无 memory/cpus/pids-limit——Culler 有 pids 2048 + 可选 cpu/mem）、无 egress 控制（Culler 同样没有）**。bwrap 驱动把整个宿主 FS `--ro-bind` 进沙箱（可读）、宿主网络全通；macOS seatbelt 同理全盘可读。[代码]

### 3.4 多智能体形态

- 没有 Strategist/Arbiter 式"自主选题"层：**LoopForge 的工作来源是人（需求/聊天/SOP 触发/定时任务）**，Culler 的核心独创是"从 VISION 主动提出高杠杆任务 + 独立裁判选一个"。反向地，LoopForge 的 **runeval**（run 复盘→蒸馏有界事实→生成 SOP/角色改进建议→人工 apply 才落目录，`runeval/apply.go:10-36`）是 Culler 完全没有的受控进化闭环雏形。[代码]
- 角色模型两边相似（prompt/executor/model/tools 绑定，跨 SOP 节点与 chat subagent 复用），LoopForge 多了 Skills/MCP 工具挂载与 SkillHub 代理市场、共享定时任务模板市场（不可变发布版本、发布者不得借共享定义取得订阅方写权限——这条不变量有代码纪律，`server.go:577-578`）。

---

## 4. LoopForge 亮点（值得借鉴）

1. **真多用户骨架**：bcrypt+哑哈希防枚举、登录限速、HMAC 会话 cookie（HttpOnly/Lax/条件 Secure、TTL 同源注释）、API token 哈希化+过期+撤销+禁止 token 自举管理、runner 一次性绑定码。从 Culler 视角这是它整层缺失的。[代码]
2. **租约/fencing/CAS 的完整纪律**：generation 单调、续租失败即自杀式取消、迟到写全灭、控制标志与文档分列——重并发引擎该有的样子，且每层有测试名可查。[代码+测试]
3. **发布即验证**：tag→main 祖先+CI 绿+compose 双 pin→多架构→按 digest 起容器 curl 健康→自动生成含 digest 的 Release；CHANGELOG 兼任运维文档（手跑迁移清单、密钥轮换、体积代价）。[代码]
4. **防内部信息回流的 CI**：`check-public.py` 扫描内部域名/AKID/私网段/哈希化员工名 + gitleaks 版本+SHA 双固定——开源外溢项目的必备卫生，很多大厂做不到。[代码]
5. **真实依赖验收**：CI 装 pin 版本真实 codex/claude CLI 跑协议测试；e2e 不 mock 认证；"authentication is never mocked"。[代码]
6. **交付闭环产品化**：需求→分支→PR/MR→合并回写、Needs-review 单一收件箱、等待陈旧检测+企微通知、SOP 设计器+版本发布+子 SOP、Preview build 产物归档。[代码]
7. **成本口径的诚实**：自报与牌价重算分离声明、"未知就是未知，不拿适配器低价兜底"、`report-workflow-usage` skill 连非 LoopForge 执行的用量也显式上报（禁估算）。[代码+文档]
8. **平滑升级**：SIGHUP listener fd handover + SIGTERM Drain park（"计划内重启"与"崩溃"在协议上区分开）——Culler 只有 journal，没有在线交接。[代码]

## 5. LoopForge 弱点（按严重度）

**S1**
1. **模型真凭据进沙箱 + docker.sock 直挂平台容器**：API key 以 env 给到运行不可信生成代码的 CLI 进程（`runner/daemon.go:1107-1133`），CLI 登录目录（`~/.claude/.codex/.codebuddy`）rw bind——被投毒仓库可改写登录态/窃取 OAuth；管理员共享 CodeBuddy key 时泄露半径=全实例。Git 侧恰好做了全仓最漂亮的凭据代理（会话网关 helper、内存 ssh-agent、askpass 钉主机）——**证明团队有能力，只是没对模型凭据做**。对照：Culler 的 provider-proxy + 每请求向 spine 复核。[代码]
2. **默认部署即暴露**：`0.0.0.0:8090` + 注册默认开 + 任意登录用户全实例可读 + 无 TLS 终结 + 凭据明文列（`user_configs` 六类 key 明文存 MySQL）+ 密码最低 6 位。`release-readiness.md:9` 还写着"默认绑 localhost"（与 compose 矛盾——文档漂移）。信任团队≠默认配置不设防。[代码]
3. **审批不可归责**：approve 的 run 账本无操作者（`run_ops.go:140-179`），且任何项目 writer 可批自己实现的门（无 SoD）；无 append-only/平台配置变更审计。企业合规硬否决项。[代码]

**S2**
4. **验证自申报**：gate 结果/产物全在 agent 可写的工作区内、`readGateResult` 直接读文件（`gate_submit.go:40-42`）——对诚实 agent 有效，对沦陷 agent 是自我认证；只有 `mode:"mr"` 在服务端对托管平台核验。对照 Culler：提交=capture 进 spine 的不可变 task_version，verdict 绑版本。
5. **无 `-race`/无 fuzz/无属性测试**：重并发 lease/heartbeat 系统缺 race 检测是测试体系最实质空洞；无任何"决策可复算"类性质验证。
6. **沙箱零资源限制 + 无 egress + 无 run-run 仓库互斥**（靠每需求分支+GET_LOCK 合入串行缓解）；`mcpCallMayWrite` 工具名启发式缺 delete/create/update（`agent_tool_mcp.go:531-549`）；会话网关 token 注入被批准 CLI 环境（turn 存续期可绕 approval 钩子直调网关）；Cursor/Antigravity 沙箱内 `curl|bash`、`npm rebuild` 执行生命周期脚本、bind-cli 允许 HTTPS→HTTP 降级。
7. **状态机仅应用层 + 全量 JSON RMW + 进程级写锁**；调度领取非原子；角色 prompt 不入 run 快照（审计缝隙）；`claimJob` 双实例窗口。

**S3**：前端零静态检查（web 34k 行 JS + chatbot JSX，无 TS/eslint——与 Culler 同款病）；无 /metrics/Prometheus（观测只覆盖被编排的 run，平台自身 SLO 不可监控）；大量中文文案硬编码（i18n 债）；`AGENTS.md`/`docs/architecture.md` 仅 21 行（后端叙述薄，事实主要得读码——对照 Culler 的 33 页 openwiki 是反面极值）。

## 6. LoopForge 企业级能力（速览，详见代理报告矩阵）

多租户缺失（无 org，登录即全读）；身份部分（有真实账号体系/OAuth/SSO 遗留——Culler 的"缺失"到这里是"部分"）；授权部分（服务端 guard 收敛一个平面、MCP 复用同一谓词，但无 RBAC 细粒度/无策略引擎/无 token 预算闸门，`AGENTFLOW_AUTH_REQUIRED=false` 可整体关）；扩展部分（MySQL 共享 + api/worker 分离 + engine_instances 心跳 + runner 队列 FOR UPDATE——真分布式原语，但工作区文件态绑宿主、跨实例审批被拒、无备份/HA 证据）；模型治理部分（10 执行器注册表+effort 白名单，但计量自报、无 fallback）；审计缺失（决策不留名）；集成强（GitHub/GitLab/Gitea/工蜂/TAPD/企微/MCP/OpenAPI——双方差距最大的一维）；数据保护缺失（明文存储、无 TLS）；SRE 部分（health/通知/陈旧检测好，无 metrics/备份/runbook）。**结论：企业骨架的雏形已具（身份/配额/审批状态机/多进程协调），但归因、闸门、密钥治理三处是企业评审硬否决。**

---

## 7. 逐维对比：Culler vs LoopForge

| 维度 | Culler | LoopForge | 胜者 |
|---|---|---|---|
| 产品定位 | 单操作员个人软件工厂，Mac 单机 | 受信团队自托管平台，服务器部署 | 不同问题 |
| 状态权威 | SQLite spine，mc 单写者，**DB 层 130 触发器 + schema 哈希握手** | MySQL，**应用层状态机 + 行锁/租约** | **Culler**（正确性下沉更深） |
| 决策内核 | 纯函数 Decide + prepare/attest/commit（可复算、可审计、防伪造帧） | per-run 调度协程 + 围栏（租约 generation/CAS/attempt 栅栏），决策不可复算 | **Culler**（范式领先一档） |
| 并发/分布式原语 | 单库单写者 + VM 双时钟域 + 两阶段停 + 六类崩溃检测（单机极致） | MySQL 租约/fencing + 多 worker claim + 跨进程控制通道 + 在线 drain/handover | **LoopForge**（真分布式） |
| 副作用防重 | journal/receipt/outbox/dispatch_key 收据，merge 崩溃边界属性测试证明 | push/MR 无 outbox；MR-tip 校验+GET_LOCK；工作区丢失=判重跑 | **Culler** |
| 工作来源与智能 | **Strategist 主动选题 + Arbiter 独立裁决 + VISION 驱动**（独有） | 人驱动需求 + SOP 模板 + 定时任务；runeval 受控改进建议 | 各有所长（Culler 更前瞻，LoopForge 更可控） |
| 交付闭环 | 本机 `culler` 分支 + review packet（批准绑 packet+version+理由，防陈旧批准） | **PR/MR 创建 + 合并检测 + direct merge + tracker 同步** | **LoopForge**（企业流程必需）；审批严谨性 Culler 更高 |
| 凭证治理 | **真凭据不进容器：Keychain 分片 + 每请求向 spine 复核的临时代理 token** | 模型 key env 直通沙箱 + 登录目录 rw bind；Git 侧却有全仓最佳代理 | **Culler**（模型）/ **LoopForge**（git）——合并即满分 |
| 沙箱隔离 | 非 root 双 uid + setuid 门 + spine 物理不可读 + 镜像字节审计；但无 NNP、无 egress、网络全开 | NNP + cap-drop + 环境消毒 + 审批钩子 fail-closed；但默认 root 沙箱 + docker.sock + **零资源限制** + 无 egress | **Culler**（对 agent 的敌意假设更强） |
| 验证证据链 | Verifier 对不可变版本判卷、UNVERIFIED 显式化、证据入 spine | attempt 级产物快照+sha256，但 gate 结果**agent 自报** | **Culler** |
| 测试体系 | 比 >1.2，属性+变异体+浏览器 smoke+跨语言日志字节 fixture+慢线每提交 vet | 比 1.09，3,627 用例+三重 85% 门+真实 CLI CI+路由清单门；**无 race/fuzz/property** | **Culler**（深度）/ LoopForge（广度与 CI 自动化率） |
| CI/发布 | 13 作业、扫描器自验收、sha256 不可变链；无签名 | 4 条流水线、发布前置证明链、多架构 digest 冒烟、check-public；无签名 | 平手（风格不同，纪律都在第一梯队） |
| schema 演进 | 哈希身份+拒启+`--reset-database`（破坏性但安全） | ADD-only AutoMigrate+手跑 SQL+版本碰撞（宽容但漂移） | 都差；**Culler 失败模式更安全**，LoopForge 升级代价更低 |
| 可观测性 | NDJSON 字节一致 + AgentsView 代理 + append-only 审计；无 metrics | 自建 run telemetry（OTLP 入站+生成列）+ SSE + 报表；无 metrics、审计不留名 | **LoopForge**（run 过程）/ **Culler**（决策审计） |
| 成本治理 | 零 | 自报+牌价（诚实声明）+外部用量上报 skill；无闸门 | **LoopForge**（从 0 到 1） |
| 身份/授权 | 单操作员 + verb 硬作用域（run.json、setuid、scope 先于开库） | 多账号多角色 guard 收敛平面；但粗粒度 + 可整体关闭 + 审批匿名 | **Culler**（对 agent 的授权是真边界）/ **LoopForge**（对人有身份） |
| 多租户/扩展 | 架构性缺失（设计声明） | 模型无租户但真实用户/项目/配额准入；MySQL 可多实例 | **LoopForge**（更近，但仍不达） |
| 集成生态 | 本地 git + Discord；无远端/CI/tracker | **GitHub/GitLab/工蜂/TAPD/企微/MCP/OpenAPI/模板市场** | **LoopForge**（差距悬殊） |
| 文档即合同 | AGENTS.md + 33 页 wiki + release 清单闭包测试 | AGENTS.md 分层规矩 + PR 模板 Validation 栏；后端架构文档 21 行 | **Culler** |
| 迭代速度/社区 | 0.1.0，单线发布 | v0.4.0、~10 PR/天、3.8k stars、双语、模板市场 | **LoopForge** |

## 8. 互补性分析

两个项目像是对"agent 交付治理"这道题从**相反的两端**给出的答案，缺的恰好都是对方最强的部分：

**Culler 从内核向外缺（LoopForge 已给出现成答案）**
1. 主体模型：LoopForge 的 cookie 会话/API token/项目成员模型可移植，且 Culler 的 append-only activity 表 + `operator_requests.origin` 一旦接上真实身份，归因立刻完整（LoopForge 反过来要把事件补 actor——它的账本模型没这层，Culler 有位置放）。
2. 成本闸门：LoopForge 的"自报+牌价"虽弱于代理计量，但它的 `run_admission`（DB 桶锁+排队状态）是 Culler 完全没有的准入骨架，token 预算只需在 proxy 扣减（Culler 的 provider-proxy 是所有流量的必经点——**在这里做硬预算闸门，比 LoopForge 补代理容易得多**）。
3. 远程交付：LoopForge 的 tracker/`mr_delivery_states`/gitegress 三件套是 Culler v1 路线图（Worksources）的现成实现。
4. 多执行面与水平扩展：runner 出站长轮询 + desired_state 落库 + engine_instances 心跳，证明 Culler 的"mc 唯一写者"缝隙之外还有可复制的协调模式。
5. 在线升级：SIGHUP handover/Drain park 可补 Culler 更新 journal 的"必须 stop-the-world"短板。

**LoopForge 从界面向内缺（Culler 已给出范式答案）**
1. **决策可复算**：把 readyNodes/settle 抽成纯函数 + canonical JSON 冻结 + 属性测试/fuzz，是 Culler `Decide`+`dispatchseam` 直接可移植的方法论——LoopForge 的 3,627 个测试测的都是围栏函数，没有一个是"同输入必同迁移"的全局性质，且无 race。
2. **副作用收据**：push/MR 前落意图 journal、后落不可变 receipt + replay fence——Culler 对 merge 的"journal-before/receipt-after"是 LoopForge 防重复交付的模板（它现在靠 git 快进语义兜底，`GET_LOCK` 只串行不幂等）。
3. **不变量下沉**：run/node 状态加 CHECK/生成列/触发器、给 append-only 事件加 actor 列并禁止 UPDATE——Culler 用 ~130 个触发器证明了这条路走得通且可测试。
4. **凭证代理**：LoopForge 的 git 会话网关已经证明它会做；把同一形态用到模型 API（按 run 临时 token + 每请求复核 + 顺带解决计量不可信——代理侧记账天然是账级精确）是 Culler 架构的直接移植。
5. **批准绑版本**：Culler 的"批准必须点名 live packet+version、旧包即拒（obsolete）"堵住 LoopForge 两个洞：prompt 不入快照的审计缝隙、以及"批 A 跑 B"的 TOCTOU。
6. **镜像派生审计**：Culler 的 project_image 字节级 setuid/cap digest 比对，恰好补 LoopForge "只绑定你信任的仓库"这条纸面边界（自定义 Dockerfile 沙箱现在无审查）。
7. **验证证据链**：attempt 结果进 DB（不可变 task_version 式 capture）而非读工作区文件——把"gate 自我申报"变成"平台取证"。

**合并图景**（若有人做"完整版"）：Culler 的内核（spine 纪律 + Decide 可复算 + provider proxy + 收据/outbox + 触发器状态机 + 镜像审计）+ LoopForge 的界面（身份/项目/配额 + SOP 设计器/脚本节点/子 SOP + PR/tracker 闭环 + 成本报表 + 远程 runner + 模板市场）。这不是功能拼接：两者在"谁批准了什么、证据在哪里、密钥经过谁"三个问题上的答案正好互补且不冲突。

## 9. 给 Culler 的采纳清单（按收益/成本排序）

1. actor 主体与多用户（LoopForge 的 auth 形态）——所有企业属性的根，Culler 已有 append-only 落点。
2. Git remote 交付 + PR/MR 状态回写——复用其 tracker/合并检测设计，Culler 的 culler 分支模型天然兼容"批准后再 push"。
3. provider-proxy 侧 token/费用入账 + 执行前预算闸门——改动面最小（流量已在手里），补 LoopForge 都做不到的账级精确。
4. runner 出站长轮询执行机模型——解除"一 Mac"天花板的第一块砖。
5. SOP 式"脚本节点 + 产物契约"进 Worker/Verifier 阶段（确定性检查不靠模型）——LoopForge 的 Script 节点与 artifact 契约值得抄进 task 验收语义。
6. 在线 drain/handover——补 stop-the-world 更新。

## 10. 给 LoopForge 的采纳清单

1. 模型凭据代理化（git 网关同形态）+ 代理侧计量——一次修复 S1-1 与成本可信度。
2. 审批事件带 actor + 批准绑快照/版本（含角色 prompt 快照）+ 默认配置收紧（绑 loopback、signup 默认关、注册首个 admin 时强制改密）。
3. 迁移器（schema_migrations + 锁 + 排序执行 + 回滚文档）——当前是其最大运维风险源。
4. `-race` 进 CI + 把 settle/readyNodes 抽纯函数做性质测试——并发正确性的最低补法。
5. gate 结果 capture 入 MySQL（而非读工作区文件）+ 自定义沙箱镜像字节审计（Culler project_image 模式）。
6. 资源限制（memory/pids）+ egress 白名单——两个项目共同的 S1 洞，LoopForge 的 root 沙箱使其更紧急。

## 11. 选型判断

| 场景 | 建议 |
|---|---|
| 个人/极小团队，代码主权与"代理不可信"是首要约束 | Culler（正确性与凭证治理目前同类最强） |
| 内网小团队，需要 PR 流程、多人协作、成本可见、多执行器混跑 | LoopForge（唯一务实选项，先按 §5-S1 加固：关注册、绑内网、上反代、独立 key） |
| 过企业安全/合规评审 | **都不行**：Culler 缺身份/规模/集成；LoopForge 缺审批归因/密钥治理/迁移治理/审计导出——且 LoopForge 的缺口（actor 归因、代理记账）是设计取舍问题，Culler 的缺口（租户、TLS、多用户）是范围问题 |
| 学术/借鉴价值 | 研究"agent 编排的正确性内核"读 Culler `dispatch`+`dispatchseam`+`property/`；研究"agent 交付的产品面与团队闭环"读 LoopForge `engine` 围栏 + tracker + 发布链 |

## 12. 证据边界

- 两案均为只读源码取证，未运行任何测试/服务/容器；所有 [测试] 指断言存在。LoopForge 浅克隆看不到完整历史（361 commits、分支保护实际启用、内部版与开源版差异均未取证）。
- LoopForge 未决：审批身份是否被 HTTP 中间件另行落日志、任务派发"需求责任人"可否第三方指定（决定凭据借用面的可利用性）、多 worker 真实负载验证程度。
- Culler 未决见前一份报告 §8。
- 本报告结论仅代表两棵 HEAD 树的状态；LoopForge 以 ~10 PR/天的速度演进，S1 类问题应在复核时重验。
