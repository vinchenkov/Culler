# DEV-BASELINE（v0.1 · 执行索引 / 本地工单图）

- 性质：把契约义务转换为**工程工单、依赖关系、执行状态与证据要求**的活文档（tracker=本地，未来可迁 GitHub Issues 作执行界面）。
- **权威边界（防二套 AM 条款，先于一切）**：AM=冻结状态/权威哈希/RR 接受/阶段 Exit 的唯一登记处；各契约=规范语义与验收要求的唯一来源；**本文=执行索引，引用不覆盖**；Git=变更载体；TM §7.1=证据格式。本文状态变化不产生任何治理含义；本文与契约冲突时**以契约为准并立即修本文**。
- 授权基线（2026-11-19 部署所有者裁定）：批准本文+工单映射+worktree 模型设计。**首批实施授权已发出**（同日 19:26，IMPL-AUTH-DRAFT-001 v2，登记=AM v0.1.23）：首批=A08/A15/B03/A20，启动次序按授权文件 §5a（A20 串行先行，已激活 19:29:17，AM v0.1.24）。**A20 检查器已成立并集入 shz/dev@6f8a332（三绿+§7.1 记录 A20-V-001）**——A08/A15/B03 的 READY 判定与开工自此经检查器通道执行（不再需要单独激活）。**仍未授权**：其余 F0a 工单、DT 起草、merge main/push、正式冻结与风险接受。工单系统的建立不构成上述任何一项的授权。

## 1. 工单模型

**字段**：`ID | 标题 | 状态 | Owner(角色占位) | depends-on | 契约锚（文件§条款，精确到不变量/测试名） | 验收映射（§8/§9/A-* 向量） | 证据要求（§7.1 记录+环境+digest+reviewed_by） | 基线 commit | worktree`
**状态枚举（工单层）**：`BACKLOG → READY(依赖齐) → IN_PROGRESS → CODED(实现完成) → EVIDENCED(证据登记) → AM-REFERRAL(移交验收判定)`；旁态 `BLOCKED(依赖/门未解)`。
**关闭规则（冻结）**：**工单关闭≠契约义务完成**。CODED/EVIDENCED 只是执行事实；义务标为 `IMPLEMENTATION_VERIFIED` 唯一路径=证据满足契约验收条款**且按治理规则在 AM 登记**（治理动作非本文动作）。本文永不出现"已验证/已冻结/已接受"字样指向义务——只指向工单自身状态。
**验证记录**：每工单的验收证据=TM §7.1 全字段记录（schema 唯一来源，本文不重列字段）；批次规则按 AM P2-4（每工单一运行批次）。
**变更规则**：契约重开仅限三情况（真实矛盾/不可实现/实测证伪）→ 发生时：受影响工单全部回退 READY 并重绑新契约锚与验收映射；identity-only 清扫不触发回退。
**机器检查**：§3 图由 tools/blcheck/ 按 AM §4d EXEC-1 五规则检查；表格须六列（ID|标题|depends|契约锚|验收映射|备注/门），depends 用 `—` 或逗号分隔工单 ID；状态记录在 tools/blcheck/status.json（缺省=BACKLOG）；契约漂移重绑凭 tools/blcheck/anchors.bind.json。

## 2. 工作区与集成纪律

- 集成分支：本地 `shz/dev`（长期，无 push）；工单分支 `shz/T<id>`；**每工单开工前确认 worktree 基点=当时 `shz/dev` tip 并记录基线 commit**；完成时 merge `shz/dev` tip 回自己分支验证后才报 CODED。
- worktree：`/private/tmp/culler-worktrees/<T-id>/`（AGENTS.md 现行路径；R2（A09）落地时随品牌一次性更名 `shorizon-worktrees`，更名本身是一行工单）。
- 工具链：一律 `mise exec --`；每工单跑其验收锚对应的 check lane（`mc/check.sh` 全链为合并前置）；**工单间禁隐式共享未登记改动**（一工单一 worktree 一分支）。
- 权限边界：合并进 `main`、提交、push、launchd 相关脚本执行（redeploy/dev-reset/uninstall）——全部等部署所有者点名（AGENTS.md）。
- 环境事实缺口（工单不得自行推测）：O1/O2/O3/O9 + V-1/3/5/6/7 建立绑定表（TM §7.3）；届时由部署所有者供给。

## 3. 工单图

### Track A · F0a 地基（Linux 服务器/品牌/门禁/验证）
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| A01 | ADR-0 凭据后端选型（age+口令/systemd-creds/KMS 接口，经 R1 拆分实为 C0/C1/C2a/C2b/C3 五候选） | — | IG §5.1 表B〔待:O1 ADR〕；MASTER §10 O1；DC §7.4 | F0a：O1 ADR 入档+表B 引用格补源 | O1-a 事实基线**已冻结**（o1-credential-facts-001.md §7，AM v0.1.46）；O1-b 比较待 U-6/U-7 要求值；ADR 决定本身仍未发——A01 保持 BLOCKED-on-decision |
| A02 | Linux 凭据后端实现（host_secret_linux + 迁移工具） | A01 | IG §5.1 矩阵行；DC §7.4 分域 | F0a：IG §5.1 矩阵逐行测试 | |
| A03 | systemd 监督替换 launchd（supervision/services/uninstall/redeploy/dev-reset 平台面） | — | MASTER §2 P0 清单③；DC-FACTS #5 | F0a：Linux 安装→运行→停复→卸载实测 | amd64 主目标 |
| A04 | 安装/发布资产双架构（install.sh 矩阵+arch 闸 host-native-arch 政策+opencode x64 包链+CI 矩阵） | A03 | MASTER §2 移植清单（B4 行：`containerruntime_linux.go:115`） | F0a：CI 双架构矩阵全绿+amd64 净机安装 | |
| A05 | proxy bind 可配置（默认 loopback）+host-gateway 语义 | — | TM §4 B5；MASTER §2 网络行 | F0a：bind/loopback 配置矩阵测试 | |
| A06 | 备份/恢复能力+**自动恢复演练**（七要素/三层证据/异地 Z5 通道预留） | A01 (密钥面) | DC §7.1-7.7（含 7.4 双事件、7.7 P1-1 三层证据） | F0a/F1：三层演练证据（DC §7.7） | 门:O3 |
| A07 | 发布完整性基线：digest 验证器**自测** | — | MASTER R10/§4 F0a Exit；TM §4 B9-T | F0a：digest 负例集全拒 | |
| A08 | R1 品牌文档层（S-Horizon/shz/白名单枚举/LICENSE+derived-from） | — | ROADMAP §2R R1；D14/D15 | ROADMAP 行 91：全仓 grep 残留恰∈白名单 | 无代码语义，首批 |
| A09 | R2 标识符迁移（shz 二进制/home/systemd 名/`shz_proxy_*`+掩码正则/`shz migrate-home`/**schema 文本先于 v1 基线**） | A02,A03,A04 | ROADMAP §2R R2 表 | R2 验收：净机端到端无 Culler 残留 | →B01 前置 |
| A10 | G-A 普通写事务 live-schema 门禁（范围声明贴码：SchemaMatch⇏BuildID） | — | DC §5 G-A；AM §4b | F0a/F1：逐入口拒绝测试正反例（AM §4b G-A） | |
| A11 | G-D/G-E 收编（附 G-A 覆盖证明，禁预宣布自动闭合） | A10 | DC §5 合并规则 | F0a：reset/onboard 路径测试+覆盖证明（AM §4b） | |
| A12 | G-F 逐入口覆盖测试出口（二选一取测试臂，不空转）：11 入口×强制层矩阵（E 帧等值／R ensure 对账／S schema 门，docs/facts/g-f-coverage-matrix.md）；唯一实然缺口=`__system-state` 无 ensure→已加门+seam；F1 P2 路由漂移闸（81 条=spine 写 24/读 19/host-local 38）+P3 RPC 边界正反例 | — | DC §5 G-F；IG §5.3 四分支同构；docs/facts/g-f-coverage-matrix.md | F0a/F1：SystemState 门拒绝+源守卫×2+RPC 边界四例+DoctorRuntime 五分量否定+operatorcmd 漂移闸；残余 R1-R4 声明于矩阵 §4；**非 RR 出口，无 TM §8 映射义务**（RR 臂未走） | 覆盖=机制验证层；"混合版本已排除"仍归 F0a Exit+复测包络（DC §6） |
| A13 | G-B agent 构建身份设计件（PROPOSED）：现状=字段已在 schema、collie 写（带 fail-open 回退缺陷）/pipeline **不写**→agent 写路径零比对；提案=生产端必填+消费端装载咽喉全等拒绝，**不新增字段**（拒 envelope_version=第二权威）；实施待批准另发 | A12 同域 | DC §5 G-B；IG §4；docs/facts/g-b-envelope-design.md | F0a/F1：实施票将含 envelope 字段正反例+混版拒绝+漂移守卫+更新演练（设计件 §3） | 设计件=本票交付；wire 形状批准归所有者 |
| A14 | V 任务组建+执行（V-1a–d/2/3/5(+X-1 三场景)/6/7） | A04,A05,A16 | TM §7.2/§7.3 绑定表 | F0a：V-list §7.1 全记录（含 X-1） | 门:O3+owner 环境事实；grill 六裁 Q7 排程注记：V 清单**组建（纸面）可先行**，**执行**待 A16 EVIDENCED（图边 A14←A16 维持，串行浪费由此消解）；A19 随 A14 合流 |
| A15 | 存量锁域全量盘点（人工跨层持锁扫描，非 grep；K1-K4 表二候选→强制） | — | DC §1.3 表二 5 | F1 前置：盘点表闭合+表二 1-3 强制化测试 | 首批候选 |
| A16 | 资源限制实现 disk/fd/log + T1/T2 强制参数表（爆炸半径≤沙箱） | A03,A04 | TM §5.2 资源行+统一资源纪律；RR-7 证据 | F0a：资源限制实测矩阵 | **所有者 2026-11-21 grill 六裁 Q1–Q4（全案按建议）**：D-1=**b+c 组合**为票内闭合集（tmpfs 短生命周期目录真 ENOSPC + 工作区 du 看门狗越软限→资源类终态终止），**工作区真配额登记为宿主契约/F0a 残余**（不藏）＋(a) ext4-prjquota 宿主变更工单草案备用（日后 218 挂上则增量切换并撤销残余）；(d) 拒。D-2/D-3 数值=nofile 65536:262144（两档同值）、docker json-file max-size=10m max-file=3、run 树 500 计数外叠加 2GiB 字节预算；runner.ndjson 4MiB [固化] 不动。D-4 数值表按建议草案采纳：T1 cpu 8.0/mem 16GiB/T2 2.0/4GiB（memory-swap 等值封 swap 旁路）、tmpfs 4GiB/1GiB、看门狗软限 20GiB/8GiB、pids 2048/deadline 45min [固化]。D-5=**混合**：T1 非零默认、T2 未显式配置拒 spawn。新资源标志一并进 collie-envelope 摘要面。实现按 shz/TA16 diff-plan-001 正清单进码 |
| A17 | BlocksWork C-i 边界 ADR（C-ii 走 RR 提交） | — | DC §5 G-C；AM §4b | F0a：ADR 记录；C-ii 仅 Authority 签署 | |
| A18 | immutable=1 处置（mode=ro 默认 或 不可变证明） | — | DC §2；AM P2-6 | F0a：代码审计项闭合（AM P2-6） | |
| A19 | 沙箱边界实测提交（与 A14/V-5 合流：socket/API/host-gw/loopback 鉴权+X-1 三场景） | A14 | TM B4-S/RR-1 补偿取证 | F0a：边界实测记录+取证归档 | 取证≠接受（RR 归治理） |
| A20 | 依赖图确定性检查器（EXEC-1 落地件）：检出不存在 ID／循环依赖／依赖未满足进 READY／缺契约锚或验收映射／锚变更未重绑；路径 tools/blcheck/ | — | AM §4d EXEC-1+A20-EX；IMPL-AUTH §5a | §5a 三绿：自测+五规则 fixture+真实快照零违规；完整输出 §7.1 留痕 | 首批·串行先行·已激活 19:29:17（AM v0.1.24）；三条件=持续验收；畸形输入显式 FAIL/UNKNOWN |
| A21 | 容器 spine 挂载布局：共享 sidecar 可写 + db 文件本体非可写（sidecar 必须与写者共享——WAL 协议约束，per-container sidecar=分裂视图，禁止）；最小权限反向断言 | — | docs/facts/a18-immutable-split-decision.md；DC §1.3 表二 3；TM §5.2 | F0a/F1：docker_boundary 三挂载态矩阵（只读无 sidecar/新布局/权限异常）+容器读成功+db 本体不可写反向断言；与 A18 联合验收 | 批二·与 A18 同批合并；docker_boundary/e2e=验收硬前置；立票授权=AM v0.1.29 |
| A22 | operator 围栏委托面核验（A15-F1 P1 执行）：运行时实验改判"委托不生效"为初稿误读——`__operator-forward` 交接在子进程复验收据并重包裹；事实记录修订+回归用例钉死交接形态；生产代码零改动 | — | docs/facts/f1-operator-fence-001.md；DC §1.2 不变量；TM §4 B5 | F0a：live 收据正控+审计行、`uncertain` 收据拒绝+零副作用、伪造 id 拒绝、委托 argv 形态双向断言；U1 关闭（docker 往返传输面归 A21 类证明） | 常驻授权 v0.1.33；原"修复需再授权"分支未触发 |
| A23 | redeploy.sh launchd 停止循环真相源分裂修复（DEFECT-REGISTER-001 登记项：launchd 分支按 ~/Library/LaunchAgents 的 plist 文件枚举停手，而等待/数据库面按 launchctl list 判在载——plist 被手删但单元在载时该单元永不被 bootout、等待耗尽 fail-closed 楔死；改 launchd 停止循环亦以 launchctl list 为准，与 Linux 分支 list 驱动对齐；fail-closed 姿态不变） | A03 | evidence/A03/DEFECT-REGISTER-001.md；003 附五状态链 | F0b：bootout-race + plist-缺失-但-在载两情形用例（既有 absent/服务错误转写须重钉，非放宽）；净机 redeploy 往返 | 立票=所有者 2026-11-20 grill 裁定 Q3；A03 票内只登记不修的残余转此票；P2 可用性楔死非安全边界 |
| A24 | Linux 宿主操作者视野门（`canInspectProjectVision()` 现返回 `runtime.GOOS=="darwin"`，门住 __release status、project list/delete、environment、delivery、dashboard upload 等全部宿主操作者动词）：A04 真机 S2-B3 暴露——Linux 生产形态下 `culler status` 等直接拒。评估安全地窄化到"非 agent/helper 容器"（保留 RequireHostScope + /.dockerenv 双检，helper/agent 仍拒），或明确判为产品边界（Linux 无宿主操作者 CLI，运维仅经 mc 私有动词+dashboard） | A04 | runtime_scope_prod.go:41 + culler_prod.go:222 + private_release.go:121（A04-S2 取证：evidence/A04/s2-interim-status.md）；TM §4 B5；RR-7 | F0a：三域矩阵（宿主=放行、helper 容器=拒、agent 容器=拒）逐动词转写 + 无 run.json 伪装的 /mc/run.json 门重验 | **所有者 2026-11-21 grill 六裁 Q5=(b) 定向放开**：宿主=放行、agent/helper 容器=拒（RequireHostScope(run.json)+/.dockerenv 双检为真边界，darwin 判据系历史产物）；验收硬前置=三域矩阵逐动词转写+run.json 伪装重验；**实现票另走 003 附三专门正清单批准，批准前本门一字不松**；A04 已按当前拒绝如实断言（validate_package 平台感知），未弱化；立票=执行侧 2026-11-21，方向裁定=同日所有者 |
| A25 | Linux 终端设置向导移植（`runTerminalSetup` 现以"Culler setup requires a Mac."拒绝非 darwin，install.sh 手递 setup 阶段止步于此）：A04 真机 S2-B3 第二轮暴露——Linux 部署的前台设置入口整体缺失，持久安装只能经宿主私有 `__onboard` 动词链接力（S2-B5 已如实记录 gap 并逐段驱动）。范围=向导各 stage 的平台语料（"Prepare your Mac"等）、Linux 凭据面（与 A02 的 hostSecretStore 缺口纠缠）、以及 AGENTS 对终端设置文本的对抗性评审门（80/40 列、有色/无色实拍）；纯产品移植而非安全边界变更，但涉 A02 未决段，须所有者定序 | A04, A02 | setup_terminal_prod.go:99（A04-S2 取证：evidence/A04/s2-interim-status.md）；AGENTS"When you change these things"终端设置条 | F1：Linux 真机 `mc __setup` 全程绿（含凭据段按 A02 决议形态）+ 双平台 80/40 列对抗评审记录；validate_package linux 断言随移植改钉（双向，非放宽） | **所有者 2026-11-21 grill 六裁 Q6=(b)**：**Linux 前台权威面=dashboard/Collie 配置路径**（产品裁定在册）；向导移植降级 backlog，挂 A02 决议之后，仅当所有者仍要终端体验时升回；追加义务=Linux 安装前台语义（install.sh 之后做什么）随本票一次文档转写；A04 已按当前拒绝如实断言，未弱化；立票=执行侧 2026-11-21（S2 取证派生），处置裁定=同日所有者 |

### Track B · F1 数据与一致性基座
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| B01 | 迁移器（schema_history 进基线、OnboardSpine 内嵌、verify 窗执行、四态检查点、Q10-A 旧二进制快照时序） | A09 | DC §5+§4 迁移四态；MASTER D9/Q10-Q13 决议 | F1：四态检查点+verify 窗证据 | 本文最大件 |
| B02 | **v1 基线冻结+AM 哈希登记**（identity 清扫类改动全部前置完成） | B01 | DC §5；AM §1.2 登记规约 | F1：登记完成=AM 事件 | 治理动作伴随 |
| B03 | F0b-1 serializer 字段表+**跨实现测试向量**（数值词法存活；五类最小集+PS §4 家族扩展） | — | DC §3.4 语义⑧条；AM §4b F0b 行 | F0b：五类+家族向量齐备（AM §4b 不齐=未定形） | 首批候选（纸面件） |
| B04 | F0b-2 根算法+`algorithm_id` 冻结 | B03 | DC §3.4 验证器输出 | F0b：algorithm_id 登记+验证器输出测试 | |
| B05 | 六维错误语义登记表生成+CI 门（缺登记=拒绝项） | B02 | DC §1.1② | F1：CI 门拒绝测试（缺登记必红） | |
| B06 | 多进程写压测 lane+容量包络基线（G 闭合后复测钩子） | B02,A04 | DC §1.1①③/§6；MASTER F1 Exit | F1：压测报告+复测钩子挂钩 | 门:O3 硬件 |
| B07 | 链实现（prev_hash 触发器+aggregate_open+序列化器按 B03）+`shz verify-chain` | B02,B03,B04 | DC §3.4 全节 | F1：链测试全套；建成前链连续性=Planned（DC A6/§7.5） | |
| B08 | activity/存量域入链迁移（v_n→v_n+1 实战第二迁移） | B07,B03 | DC §3.4 层规则；IG §7 历史零改写互验 | P4.0：第二迁移实战+零改写比对 | |
| B09 | **usage 表 v1→v2 首迁移+blob 回填+回填证明**（计数/digest/双向核验，失败原文不丢） | B02,C03 | MASTER D10；DC §3.2 回填不丢弃 | F1：回填证明（计数/digest/双向核验） | 审方指定首战 |

### Track C · P2 Provider 与预算
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| C01 | L2 registry 配置链（config.go 双闸改 custom 路径、三 https 闸条目化、字段闭集+port_allowlist、命名空间隔离） | A02 | PS §1 | P2：配置链闭集测试（缺省拒） | O1 |
| C02 | DestinationPolicy 执行面（canonical host 规范化/connect 钩子/v4-mapped/重定向/pooling=off 默认） | A05 | PS §2.1-2.5；MASTER §6.3(A1 比对) | P2：执行面向量集（A2 二分含覆盖证明） | 实现=resident/src/provider-transport.ts（fetch 形单点：§2.1 规范形判定+§2.2 逐尝试 connect/字面量过滤+§2.3 SNI 钉真+agent:false+永不自动跟随重定向）；provider-proxy 默认出口已替换；A2 静态半=provider-* 原始出口枚举守卫；**运行时审计半+allow_cidrs 配置面=C09/C01（§2.7③ 不继承声明）** |
| C03 | usage 家族解析+observation 身份生成+持久序（origin=local/去重键/version） | C02,B01 | PS §4/§4.1；PS-F1① 崩溃窗 | P2b：PS-F1① 崩溃窗恢复不双重记账 | 持久序需新建 observation 表=schema.sql 变更→必须先有 B01 迁移器基线（spine 不兼容不变量）；此边为 2026-11-20 追链补登（图上原缺） |
| C04 | breaker 状态×事件同事务+持久 lease+fencing token | B02,C02 | PS §6/§6-PS5；IG §5.3 同构 | P2：过期 token 迟到提交=必红 | |
| C05 | BL 核心（budget/reservation/period 表、四动词+reconcile/writeoff、I2 规范式+方向性用例⑩、版本 fence） | B02,C03 | BL §1-§4/§6；A12 防回潮 | P5：I2 方向性⑩+fence 竞争+投影重算 | |
| C06 | authorize 准入闸接线+proxy `maxTokens_effective` 授权线（域 2 按 BL-V1 结果启用/退化） | C05,C07 | BL §5(a)(b)；AM §4c BL-V1 | P5：准入矩阵测试（域 2 按 BL-V1 态声明） | BL-V1 前置核对 |
| C07 | CapabilityRecord+阶梯+探针（bootstrap grant、三态 provability、A4 联动） | C01,A02 | PS §5；BL §5(b) 接口 | P2：三态 provability+无 grant 拒启动（A9） | O1 |
| C08 | 逐家族向量验收（A5：解析器存在≠口径正确） | C03 | PS §8/§9 | P2b：逐家族向量全绿（A5） | |
| C09 | RR-3 关闭五项包（V-6+环境登记+config↔执行一致+反证+变更守卫） | C02,A14 | TM §8 RR-3 | RR-3：五项包+Authority 接受（治理） | 门:V-6 执行 |
| C10 | chargeback 三列报表+价格快照版本表（unsettled-cost 态） | C05 | BL §5(c)/§6/§7；D-G7.2 | P5：对账报表测试+unsettled 态 | |

### Track D · P4 身份与治理
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| D01 | 身份基础（user/session/grant/team 表、guard 单平面、可信上下文注入+不可伪造性） | B02 | IG §1-§3；IG-T1 前置 | P4.0：IG-T1 四组攻击全绿 | |
| D02 | 撤销矩阵+零缓存证明+派生即失效（epoch fence 预备） | D01 | IG §6；IG-T2/T8 | P4.0：矩阵逐行实测+零缓存证明 | |
| D03 | legacy:operator 迁移+映射表+封存 | D01 | IG §7；IG-T5 | P4.0：零改写比对+封存拒写测试 | |
| D04 | break-glass 机制+**紧急单人操作清单冻结成文** | D01 | IG §3；D23；IG-T7 | P4.0：清单成文+单人紧急例演练 | |
| D05 | degraded_grants 五要件表（per-operation 拒语义） | D01 | IG §3；TM §8.1；MASTER D31② | P4.0：五要件缺项=拒 测试 | |
| D06 | 签名请求状态机+节点+五证（REQUESTED/SIGNED/RECORDED+fencing） | A02,D01 | IG §5.2/§5.3；IG-T3/T9 | P4.1：IG-T9 五证含取消×签名并发+重启迟到 | 门:O11 裁决 |
| D07 | 锚定发布（链头集→Merkle root→Z5 复制；四要求） | B07,D06 | D22；DC §3.4 输出 | P4.1：锚定四要求离线验证 | |
| D08 | signoffs 表+SoD 可配+审批防伪造+批准永不创建需求（结构红线测试） | D06,B02 | D18；IG；MASTER §2 生命周期 5 | P4.0/4.1：结构红线测试 | |
| D09 | OIDC 联合（issuer 精确/PKCE/JIT 拒绝策略） | D01 | IG §2；IG-T6 | P4.1：协议验证集全项（IG-T6 扩项） | 门:O9；P4.1 尾 |
| D10 | metrics/OTel+审计导出（哈希链可验证）+retention 声明制 | B08,D02 | MASTER §4 P4.1；TM §6 行；DC §3.5 | P4.1：导出可验证+retention 缺省拒 | |

### Track E · P3 交付 —— **全部 blocked-by：DT 契约封闭**（v0.1 草案已提交评审 AM v0.1.36；封闭=评审+正文事件，起草本身不解除 BLOCKED）
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| E01 | push 宿主直跑+journal/receipt+pushed_sha 自证 | — | DT §1 INV-D1/D3、§2 R2/R3、§3；MASTER D11；TM §4 B8 | P3：交付故障矩阵（AM §2） | blocked-by DT 封闭；实施另发授权（DT §0） |
| E02 | PR 创建（compare 直查键） | — | DT §4 | P3：故障矩阵 | blocked-by DT 封闭；API 认证面=DT §4 Planned |
| E03 | 远端观察器+自激环封堵（delivery.go:1059 字符串相等缺陷） | — | DT §6 修复规格、§5-S8 | P3：故障矩阵 | blocked-by DT 封闭；READY 前置=result 字段全表入 B03/F0b 注册（DT §6 末） |
| E04 | 故障矩阵八场景 | — | DT §5（S1–S8，规范源=该节） | P3 Exit 全绿（AM §2） | blocked-by DT 封闭 |
| E05 | hook 隔离强制配置（V-3 承接） | — | DT §6 清单 1-8；TM §7.2 V-3 | P3：强制配置清单实测 | blocked-by DT 封闭 |

### Track L · LoopForge 能力互补承接（2026-11-20 所有者目标校准指令立轨：逐条核对 ANALYSIS §9 采纳清单后的补缺口票；主体性=模式提取入契约/语义，零代码并入；§9-4 runner 远程执行机=所有者已裁 non-goal，不入）
| ID | 标题 | depends | 契约锚 | 验收映射 | 备注/门 |
|---|---|---|---|---|---|
| L01 | SOP 式"脚本节点+产物契约"进 Worker/Verifier 验收语义（确定性检查不靠模型；runeval 式受控改进边界） | — | ANALYSIS-2026-11-19-LoopForge-vs-Culler.md §9-5/§8-5/§3.4 | F1：设计件（节点语义×task 验收映射×agent 输入面变更清单）+实施后确定性检查执行测试 | **设计件先行**——触任务验收/dispatch 语义=wire-adjacent，形状决定归所有者 |
| L02 | 在线升级语义：drain/handover（补"必须 stop-the-world"短板；systemd SIGTERM/Restart 语义先行，listener fd 交接在评估内） | A03 | ANALYSIS §4-8/§9-6 | F1：升级演练绿（与 update_docker lane 合流）+ journal/停摆语义不回归断言 | Wave A 后续票；不新造更新协议权威（复用 A17/journal 面） |
| L03 | 防内部信息回流 CI（内部域名/私网段/AKID/人名哈希扫描 + gitleaks 版本+SHA 双固定） | — | ANALYSIS §4-4 | F1：红绿样例（植入→必红；清除→绿）入 CI 门 | 低成本高收益；挂发布流程，触 workflow 面=按 003 负清单逐票正清单申请 |

### 治理指针（非工单，状态只在 AM 演进）
AM §3 队列 1-6（Authority 指定、5×CONTRACT_FROZEN+Integrity 复核、ARCHITECTURE_FROZEN、D-IG×5+D-PS×5+D-BL×5 裁决批准、RR-1..7 逐条、F0b-3 摘要关系批准）；BL-V1 原文核对；DT 起草启动令。

### Backlog（P6/演进，不排期）
SBOM+cosign（RR-4 关闭路径）、Worksources、共享定义治理、OBS-1 双层摘要登记协议、容量预测、故障演练自动化扩展、gVisor/Kata（仅按 RR-1 四触发条件重启）。

## 4. 首批 frontier（一旦实施授权生效即 READY 排序）
发出后次序（v0.1.23）：**A20 串行先行**（IMPL-AUTH §5a：检查器三绿前，A08/A15/B03 不判 READY、不开工）；随后纸面先行：A08、A15、B03（无 O 门）；再后 A01/A05/A07/A10/A17 并行；A02←O1、A03/A04 依授权批次。依赖正确性说明：A10/G-A 不等 P1（校验用常量身份，与迁移器正交）；B09 故意压在 C03 后——usage 先以 blob 积累（DC 观测通道），首迁移带回填实战。

## 5. 迁移 GitHub Issues 的条件（部署所有者已定，不前置）
多人跨团队分工出现／工单审查与 PR 关联需平台化／本地维护成本>外部治理成本。迁移后 Issues **仅是执行界面**：义务权威仍在契约，状态权威仍在 AM，本文降级为镜像或归档。
