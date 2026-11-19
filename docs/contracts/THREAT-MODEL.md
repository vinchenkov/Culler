# CONTRACT · THREAT-MODEL（v0.2.3 · RECOMMENDED · 待批准）

- 状态：**RECOMMENDED v0.2.3**（限域复审 9.2/10：四项 P0+四项 P1 全部落实，审方裁定"TM 文档迭代结束，不启动第四轮内容审查"；三项非阻断执行要求以 X-1/2/3 落于 AM）。评审正文=a3de0a9e…；本行状态修正为 identity-only（v0.1.18 清扫），登记哈希见 AM。四轮修订映射 §11。
- 归属：F0b；执行验证挂 F0a/P2a Exit Criteria。

## 0. 设计解释原则（非最高不变量，全文适用解释基准）

> S-Horizon 的安全目标不是让 Agent 不能做任何事情，而是让 Agent 能够自主完成被允许的事情，同时无法突破明确的权限、资源和信任边界。
> 两面一体：自治能力不能被治理架构取代（Autonomy Preservation）；安全边界不能被自治能力绕过。

安全声明三命题分离（本轮裁定确立）：**机制存在 ≠ 配置正确 ≠ 真实攻击下有效**——分别对应 `[固化]` / 参数表 / V-list+证据结构，任何条款不得混用三者互相背书。

## 1. 资产（AS1–AS12）

| # | 资产 | 失陷后果 | 主防护/归属 |
|---|---|---|---|
| AS1 | spine（事实/状态/审计/授权） | 工厂现实被改写 | 唯一写者+触发器+链/锚 [DC/D-G3]；静态明文=RR-5a/5b |
| AS2 | 模型 provider 凭据 | 资金+账号接管 | Keychain+临时 token+liveness [固化] |
| AS3 | Git/GitHub 凭据 | 仓库投毒/供应链 | 宿主持钥（D11）[P3] |
| AS4 | 操作员 checkout/源码 | 代码主权 | mount 司法权 [固化] |
| AS5 | Project secrets | 项目侧服务 | stdin 注入不落 env [固化]+egress（RR-3）；运行期明文=RR-5b/V-7 |
| AS6 | 证据链（签署/判卷/usage/锚点） | 审计可信性 | append-only+链+锚 [D22]；运行期明文=RR-5b/V-7 |
| AS7 | 宿主 | 全部 | 容器边界（RR-1） |
| **AS8** | **锚定签名私钥及操作授权** | 伪造可信检查点=审计体系整体失效 | 密钥域独立（D22-1）；**签名操作本身需授权边界**（IDENTITY-GOVERNANCE 裁决，防"只谈存储不谈使用权"） |
| **AS9** | **Docker daemon/宿主特权控制面** | 隔离边界失效≈宿主控制 | **socket 不可达=T2 硬性验收条件（非加固建议）**；daemon 归 Z0a 管理域 |
| **AS10** | 身份/Capability 签发材料（session secret/token mint） | 伪造主体、授权与会话 | O1 后端+签发面最小化 [IG] |
| **AS11** | 备份加密密钥 | 历史不可恢复或备份泄露 | 与备份分域 [DC §7.4] |
| **AS12** | 更新发布信任根 | 恶意版本进生产 | digest 链 [固化]+签名 [RR-4→P6] |

## 2. 信任域（细化）

- **Z0a 宿主 OS / Docker daemon 管理域**（AS9；运维即 root——A4 内部者主场）。
- **Z0b S-Horizon 控制面服务域**（resident/mc/dashboard/proxy）。
- **Z0c 凭据与签名密钥域**（AS2/3/8/10/11/12；Linux 后端 O1；密钥只从环境/密钥服务读取，源码/示例/测试零可用凭据字面量——全局纪律）。**Z0c 是密钥管理逻辑域，不推导出任何两类密钥共享存储/读取权限/签发权/轮换周期**：provider 凭据、git 凭据、锚定签名私钥、Capability 签发材料、备份加密密钥、发布信任根各按对应契约定义读取主体/使用主体/签发主体/撤销与恢复语义（与 D22"签名密钥与 spine 不共享安全域"一致；"同属 Z0c"≠"可共用安全边界"）。
- **Z0d 操作员工作站与管理访问域**。
- **Z1 Agent 执行域**（T0/T1/T2 参数档，§5）。
- **Z2 管理访问路径**：SSH 隧道**提供网络可达性收敛，不构成客户端身份与设备可信性的等价证明**——SSH 凭据、隧道端口绑定（默认仅 loopback-local）、客户端设备失陷、浏览器会话泄露为独立风险面，逐项归 IDENTITY-GOVERNANCE 会话模型。
- **Z3a 商业 Provider 域**（外部 SaaS 信任与数据处理边界）／**Z3b 私有推理端点域**（组织基础设施/网络路由/端点管理边界）——**可信等级不互借**：Z3b 的"内网"不等于可信（它同时是 DestinationPolicy 的 allow_cidrs 主张与 A5 的靶面）。
- **Z4 外部制品域**：默认敌对 [固化第一性]。
- **Z5 独立信任域**：备份异地副本+锚点 checkpoint（D22 四要求）。
- Z6 团队/用户边界（P4.0 后）。

## 3. 对手（七类保留，A1 一等对手不因容器隔离降级——容器限制执行边界，不限制决策可信性）

A1 提示注入/恶意仓库（一等）｜A2 供应链｜A3 认证用户越权（P4+）｜A4 内部者/管理员｜A5 网络对手｜A6 被注入 Agent 的逃逸（A1 升级态）｜A7 更新链篡改。

## 4. STRIDE 边界矩阵（B1–B10 × 六判定全格；N/A 附理由）

**B1 User→Dashboard/API**
S：单操作员 token [固化]→RR-6；P4.0 HMAC session+可撤销 [新→IG]。T：Origin/Host 钉+release 头防陈旧 UI [固化]。R：board_operations 前后值 [固化]+actor 占位转正 [IG]。I：单人期无暴露面；Z6 隔离 [IG]；日志掩码 [固化]。D：注册恒关 [F0a 验收]；写串行容量 [F1 包络]。E：verb scope [固化]+admin 矩阵 D23 [IG]。

**B2 控制面→spine**
S：mc 身份=run.json 存在性+setuid 忽略 env [固化]；hello 四元组 [固化]。T：格网+IMMEDIATE+CAS [固化]；**文件系统级整体重建=in-DB 不可防→链+外部锚定（V-链演练）** [D22]。R：activity append-only+dispatch 收据 [固化]；签署=独立业务证据 [IG]。I：spine 运行期可读面=**RR-5b/V-7**；离线介质明文=RR-5a；只读 verb 最小面 [固化]。D：WAL 压力监测+busy 语义逐 verb 声明 [F1]；库损→恢复七要素 [DC §7]。E：private/helper scope 探针 [固化]+V-5 重验。

**B3 控制面→沙箱创建**
S：launch envelope 由 spine 冻结、双 recheck [固化]——创建参数不可被中间人/竞争者篡改。T：mount plan TOCTOU 双复验+镜像字节审计+env 白名单 [固化]。R：launch 记录+launchId+boot_id [固化]。I：provider-access 单文件 ro+跨项目禁挂 [固化]。D：**gap**：cpu/mem 默认无限→T 档强制；disk/fd 无配额=**gap→§5 新增**；pids 2048 [固化]。E：setuid 面=唯一 mc-real [固化]+V-5。

**B4 沙箱→宿主（A6）**
S：**风险格（复审撤销 N/A）**——沙箱进程不得经伪造/复用/污染 run 身份上下文、容器身份或宿主服务信任前提获得其他 run 或宿主服务的权限；宿主服务不得把"来自受控容器"当作"来自合法 Agent"。身份真实性引用 run.json/setuid 机制 [固化]，授权边界与宿主服务鉴权实况引用 **V-5**。本格仅为责任定义，不表示机制已通过验证。T：namespaces/cap-drop/只读面 [固化]；宿主文件经 VM 可见性=RR-1 范围。R：容器生命周期 spine+NDJSON 双留 [固化]；逃逸行为的宿主侧证据=**gap→gVisor/Kata ADR 触发条件之一**。I：宿主 loopback 可达（README 自认）→**V-5 实测+鉴权复验**。D：共享 VM 资源（RR-1 补偿=pids+deadline+§5 资源新行）。E：内核漏洞逃逸=RR-1（接受条件：V-5 闭合+补偿三件在线；升级不自动=保留 5）。

**B5 Agent→Provider proxy（A1 升权+AS2）**
S：token digest 校验+过期+per-request liveness [固化]；跨网期防重放 [P2a，随 RR-3 关闭]。T：routes 只来自宿主配置 [固化]；L2 条目→**DestinationPolicy**（V-6）[新→PS]。R：provider_request observation+proxy-request-id 交叉收据 [固化]。I：SecretFilter 跨 chunk+trace 掩码 [固化]。D：速率/并发限额 [固化]；拒绝码三表同步（防无限重派烧钱，D25）[P2b]；proxy 重启全 token 失效=已声明可用性代价 [固化]。E：模型白名单 [固化]；额度下发（maxTokens 接现有 maxRequests 线）[P2b]。

**B6 外部制品→Agent（A1/A2 主战场）**
S：N/A（**限定理由：制品本身不是执行主体**——本 N/A 仅覆盖"主体身份伪造"命题；制品携带的伪造身份声明/伪造工具调用/伪造上下文属 B6-T、B7-S 处理范围）。T：brief 标签数据边界 [固化]；**仓库级可执行配置加载=V-1a–d（未闭合前本格记 gap，T2 按"会被加载"设防）**。R：证据可溯（task_versions 不可变+input_commit）[固化]。I：per-run fresh workspace [固化]；**持久 /home/agent+/cache 的跨 run 残留=V-2 实测项**。D：恶意仓库资源陷阱→deadline [固化]+§5 资源行 [新]。E：注入→越权主链=硬边界三件套（scope/spine/proxy）[固化]；evals 注入语料回归门 [固化]。

**B7 工具/MCP→平台**
S：run.json 不可伪造 [固化]；MCP server **显式声明制**（禁名字启发式——LoopForge 反面教材 [E1]）[新→PS]。T：collie verb_allowlist 冻结缺失拒启+附件 canary 四元 uid [固化]。R：工具调用入 telemetry（actor=run；自然人归因 P4.0）[固化+IG]。I：他任务工件空目录遮蔽 [固化]+V-2。D：工具挂起由 run hard-deadline 兜底 [固化]。E：**Authority/Task Delegation 裁决（D30）为本格宪法；证据与风险状态语义循 D31**（职责区分：D30 定义权柄委派边界，D31 定义三命题/状态词汇——MASTER v3.3 正文核对一致，见 §11-d 跨文件核对记录）；skills 角色隔离 e2e [固化]。

**B8 交付→远端**
S：push 身份=宿主 keychain（无容器面）[D11]；远端 URL 只来自宿主配置（destination 复用语义）[新]。T：只快进+lease+自证 pushed_sha [P3]；**hook/credential-helper 执行面=V-3 实测，强制配置入 DELIVERY-TRANSACTION**；**宿主配置≠可信配置——"最终 push 目的地=批准目标"为强制一致项（V-3 测机制，DT 验收绑定，正反皆测）**。R：push journal/receipt [P3]；批准消费记录 [固化]。I：token 不出宿主 [D11]+输出脱敏 [固化]。D：push 超时歧义→对账语义 [DT 契约]；origin 高频漂移→quiet [固化]。E：PR 状态只进展示层、不驱动调度（防远端注入调度）[D11-R4]。

**B9 更新→运行时**
S：release record+platform pin [固化]；工件签名=RR-4→P6。T：sha256 不可变输入链+digest 唯一 [固化]；验证器自测 [F0a]。R：journal 9 阶段+committed 收据 [固化]。I：test-release 泄露面=已治理 [固化]。D：中断更新→同 candidate 重试/--restore [固化]。E：升级能力漂移→hello 四元组+清单闭包测试 [固化]。

**B10 备份/锚点→Z5**
S：签署者密钥身份=AS8（Z0c，签名操作授权边界→IG）。T：checkpoint 单调+链式（防分叉/回滚）[D22]；备份加密与完整性保护**目标及验收约束** [DC]（当前实施状态以 DC 验证记录为准，**未完成前不得宣称备份加密已生效**）。R：备份发布五重闸收据 [固化]；演练记录（§7 证据结构）[DC §7.7]。I：备份明文=**gap→DC §7.4 加密落地前记 gap**；异地通道 TLS/age。D：空间预检 fail-closed [DC §7.6]；密钥丢失=宣告设计事实 [DC]。E：**降级重放**（用旧备份把系统整体回卷）→恢复前 schema_history+链/锚比对检测 [DC §7.5+新]。

## 5. T0/T1/T2 参数表（七维；网络行三态分离修正）

**5.1 网络维度状态声明（P0-1 销账）**
- Target Policy：T2=默认拒绝、provider 仅经 proxy（=目标强制要求，非现状）。
- Current Enforcement：**待 V-6 实测**；当前基线=开放 bridge（[E1 README 自认]）。
- Residual Exposure：**RR-3（未关闭缺口，临时风险，有失效条件）**。
- **纪律**：egress enforcement 完成并经 V-6 关闭前，**任何平台安全状态声明**（UI/报告/API/客户材料/自动交付说明，定义见 §10-A7）不得宣称 T2 网络隔离达标；**零凭据是补偿控制，不是网络隔离的替代**（无凭据仍可外传仓库内容与 AS5）。

**5.2 参数表**

| 维度 | T0 自有 | T1 受审核外部 | T2 不可信（默认档） | 现有 enforcement |
|---|---|---|---|---|
| 网络 | provider+声明域 | egress 白名单（DestinationPolicy 复用） | 默认拒绝（5.1 三态） | [固化] 无→[P2a] |
| 挂载 | 司法权内 | 只读源+隔离工作区 | 全最小化+宿主敏感路径地板（只增不减） | [固化 boundary] |
| 凭据 | 项目 secrets（stdin） | 短 TTL+声明 | 零凭据或仅 proxy 临时 token | [固化 projector] |
| 资源 | 操作员设定（可不限） | **强制 cpu/mem+deadline+pids** | 同左+**新增：磁盘配额、fd 上限、日志量上限**（现状 gap→F0a；**参数与 enforcement 定案前本维保持 GAP——列入表格≠完成**） | pids 2048 [固化]；cpu/mem 默认不限 [GAP]；disk/fd/log 无证据 [GAP] |
| 镜像 | release 目录 | 字节审计 | 同 T1+禁 lifecycle scripts（npm --ignore-scripts+**禁 rebuild**） | [固化 image_contents/字节比对] |
| 配置/hooks 加载 | 允许 | 声明式审核清单 | **不加载任何未审核仓库级可执行配置；不可信仓库不能修改 harness 安全策略、不能自行注册带平台权限的 MCP**——闭合判据=V-1a–d，未闭合前 T2 验证态不得标完成 | tmpfs home（规划）→**HOME 隔离≠仓库本地配置隔离**（审查修正：cwd/父目录/项目目录/显式路径四个加载源分别验证） |
| **执行身份与权限（新增维度）** | 按项目+角色授所需 capability | 最小权限；外部工具/服务独立授权 | **独立低权限身份；不继承宿主/操作员权限；平台能力一律经受控代理按请求授权**（把双 uid/Capability/proxy/MCP 声明制统一入本行——防"容器隔离了但继承身份仍可越界"） | [固化 run.json+setuid+proxy liveness] 的组合声明，有效性=V-1/V-5 |

**统一资源纪律（复审新增）**：T1/T2 的资源耗尽**不得连带控制面或 spine 失可用**（爆炸半径 ≤ 该沙箱）。落地定义（F0a 参数表，定案前资源维保持 GAP）：Disk quota——作用域=工作区/临时目录/**日志目录**/宿主共享卷，超限行为=写失败且 run 以资源类终态终止（不污染 spine 卷所在文件系统）；FD limit——位置（容器 rlimit）与继承规则、超限=新调用失败；Log quota——单 run 上限+轮转/截断策略+写满行为（截断必须留痕，Evidence First）；CPU/mem——T1/T2 非零默认值或"配置即拒启动"规则；deadline/pids——既有机制 [固化] 的实测证据与超限终止语义。

## 6. 缺口→归属契约×实现阶段

DestinationPolicy+MCP 声明制+T2 网络执行→**PROVIDER-SECURITY**×P2a；hooks/配置加载（V-1a–d）+资源 disk/fd/log+T2 强制档位→**F0a**（实现）/本契约（验收）；锚点操作授权+break-glass+session 模型+AS8-12 密钥边界→**IDENTITY-GOVERNANCE**×P4.0；链/锚存储/备份加密/演练七要素→**DATA-CONSISTENCY**（已含，F0a/F1）；git hook 强制配置+对账语义→**DELIVERY-TRANSACTION**×P3；预算拒绝码/熔断→**BUDGET-LEDGER**×P2b/P5；工件签名+SBOM→Acceptance Matrix P6 行。

## 7. V-list（验证记录结构 + 拆分/扩充）

**7.1 统一验证记录（P0-4 销账；v0.2.3 证据完整性扩充）**：每次验证产出一条记录——`verification_id / target_component / tested_version（commit+镜像 digest）/ environment（硬件/OS/Docker 版本/网络拓扑/测试配置）/ attack_scenario（可复现脚本引用）/ expected_result / actual_result / evidence_ref（产物路径）/ `**`evidence_digest（证据产物内容摘要）/ test_script_digest（实际执行脚本或套件摘要）/ execution_id（同一验证任务的不同执行批次）/ supersedes_verification_id（如替代旧验证须建链）**（v0.2.3，P1-1：证据身份按内容摘要而非路径——复审者须能确认所见证据即当时判 PASS 的那份**）/ verdict ∈ PASS/FAIL/UNKNOWN/NOT_APPLICABLE / reviewed_by（**复核主体**——可与执行者同一人，但必须显式记录兼任，字段不得制造双人复核假象）/ verified_at`。**PASS 的作用域限定（P1-2）**：PASS 仅对记录中 tested_version/environment/attack_scenario/测试配置的组合有效；版本、配置、网络拓扑、运行身份或相关执行链变化时按对应契约触发复测——**旧 PASS 不自动继承到新环境**。UNKNOWN ≠ PASS，且 UNKNOWN 不得被任何验收条款静默消费；关闭=verdict=PASS 且 **evidence_digest 可复核**，禁止"文档改状态"式关闭。
**7.2 验证项**
- **V-1a 配置发现**：CLI 实际读取哪些位置——**四个源分别测**（HOME/tmpfs、cwd 及父目录上溯、项目配置目录 `.claude/.codex/.mcp.json` 类、显式 env 指定路径），逐 harness family、真实 CLI 钉版本，模拟 harness 无效。
- **V-1b Hook 执行**：哪些仓库文件可触发代码执行（生命周期 hook/命令 hook/脚本）；测"恶意 hook 在场时 T2 容器不执行之"。
- **V-1c MCP 注册**：仓库能否自行注册带平台权限的 MCP；期望=注册只来自审核声明（B7 声明制）。
- **V-1d 启动顺序**：安全配置（tmpfs/剥离/白名单）是否**先于**任何不可信配置加载生效；测时序证明（探针落笔点）。
- V-2 跨 run 残留：/home/agent 与 /cache 的投毒持久化面（装—换 run—读取攻击链）。
- V-3 宿主 git 路径（P1-3 扩充）：系统/全局/仓库级配置优先级、`GIT_CONFIG_*` 覆盖性、credential helper 外呼程序、模板目录与 hooksPath、push 用配置与常规操作隔离性——**必测"恶意仓库配置+恶意 hook 在场，宿主 push 不执行之"**；**必测"最终 push 目的地=批准目标"（正反一致）**；强制配置清单落 DT。
- V-4 Collie 附件渲染路径注入可达性。
- V-5 沙箱→宿主可达性：docker socket、`host.docker.internal` 环回各服务实测+鉴权实况（loopback≠免鉴权）。
- V-6 egress 实况：**六个独立用例**——代理绕行、直连 IP（绕 DNS）、域名→环回/私有解析、DNS rebinding、IPv6 与 IPv4-mapped、重定向改道。**验证边界**：测试对象=实际 Agent 网络命名空间+代理+宿主转发链（三者整体，非任一局部）；对私有端点 `allow_cidrs` **按解析后 IP 与重定向目标复验**；失败用例必须经**出口观测点**证明"请求未从任何其他路径发出"（仅代理返回拒绝不构成证明）；允许目标与禁止目标**正反皆测**。**语义引用 MASTER DestinationPolicy（已冻结约束），本契约不重定义**，只承担覆盖其边界的验证义务。
- **V-7 运行时明文暴露面（RR-5b 专属，v0.2.2 能力分层）**：按 RR-5b 能力表分级执行并**逐类结论**——类①不同低权限 UID 进程读取（文件权限矩阵有效性）；类②同 UID 进程读取（mode 检查不算过，需进程隔离/权限边界证据）；类③特权/root 与 daemon 路径**不作为 RR-5b 补偿的验证对象**，其可达性证据归 RR-1/V-5 与 Z0a 失陷面登记。另测：备份过程临时文件、诊断产物（trace/log 副本）暴露面。**单次权限矩阵检查 PASS 禁止推广为"运行时明文风险已控"**；不得挂 V-5 代验。
**7.3 任务建立纪律**：V-1/3/5/6/7 于 F0a 建为可执行验证任务。**任务状态与验证 verdict 是两个维度**：任务态 ∈ `NOT_STARTED / RUNNING|UNKNOWN / PASS|FAIL`——**"任务已建立"不得被误读为"验证已通过"**。建立时必须绑定环境事实（**由部署所有者提供，审查模型不得推测**）：

| 任务 | 建立时必须确定 |
|---|---|
| V-1 | harness family、CLI 精确版本、启动命令、配置发现探针、恶意配置样本 |
| V-3 | Git 版本、push 用配置环境、credential helper、hooksPath、批准 remote |
| V-5 | Docker Engine 版本、宿主网络拓扑、容器网络模式、宿主服务清单、鉴权测试账号 |
| V-6 | 网络命名空间、代理配置、DNS 解析路径、IPv4/IPv6 环境、出口观测点 |
| V-7 | 权限矩阵采样 run、诊断产物清单、备份临时文件路径 |

契约冻结**不要求**全闭合，但矩阵相应格必须保持 gap 标记与 Exit Criteria 挂钩（复审检查点）。

## 8. 残余风险登记册（七字段；v0.2.2 治理修订）

**8.0 风险状态 ↔ 运行语义全局映射（v0.2.2，跨契约共享治理词汇）**

| 风险状态 | 运行语义 |
|---|---|
| `PENDING_ACCEPTANCE` | **不代表获得运行授权。** 依赖该暴露面执行的操作：有明示降级授权者必须标记 `Degraded/Gap` 并满足预定义补偿控制与范围限制；无明示降级授权者 **fail-closed**（由对应组件拒绝启动或拒绝派发——本条的实装点在 IG/PS/DT 各自的准入处）。**禁止**以"系统已在运行/任务已派发/历史上曾运行"推定风险已获接受。 |
| `ACCEPTED_RESIDUAL` | Acceptance Condition、补偿控制证据、Authority 签署三者齐备时，方许在**限定范围**内运行；仍受 Expiry/Review Trigger 约束——**不是永久放行**。 |
| `REVOKED` | 原接受作废；按对应控制定义触发拒绝、暂停或重新审批。 |
| `CLOSED` | 风险源已消除且有可复核 Closure Evidence——不是"没有风险"的同义词。 |

应用示例（本表直接推论）：**RR-3 未关闭或未获正式接受前，平台不得把网络开放的 T2 沙箱标记为"符合 T2 目标策略"**；可否以降级模式运行由本表明示决定，不由历史惯例决定。治理面无权创造生产需求，但**必须有权拒绝违反安全边界的执行**——与 Autonomy Preservation 不冲突（§0 两面一体的第二面）。

**8.1 Degraded Operation Authorization（v0.2.3，P0-2：与 Risk Acceptance 分离的独立授权）**
- 两种授权不得互替：**Risk Acceptance**（§8 各条，Authority 签署）回答"持续残余风险是否接受"；**Degraded Operation Authorization** 回答"风险未关闭/未正式接受时，某次限定操作可否运行"。已 `ACCEPTED_RESIDUAL` 的风险，若其补偿控制**当前不成立**，运行准入仍然拒绝——接受不能替代补偿控制的持续有效性。
- 签发要件：由**部署所有者指定的具名授权主体**作出（授权主体机制、签署载体与撤销通道归 IDENTITY-GOVERNANCE 裁决，本契约只冻结要求）；必须绑定五要件——**具体风险 × 操作范围（项目/任务/Agent/能力清单）× 补偿控制 × 有效期 × 撤销条件**；不得扩大到未列明项。
- **自动失效**：补偿控制失效、有效期届满或撤销条件命中→授权自动作废并 fail-closed。审查意见、AI 判断、历史运行事实**均不构成授权**。
- **Contract Frozen 性质声明（复审裁定收录）**：冻结冻结的是**风险治理规则**——不是风险接受，更不是生产运行许可；冻结时 RR 条目合法保持 PENDING_ACCEPTANCE，其运行语义由 §8.0/§8.1 继续约束。

**Authority 治理定义（全局，复审裁定）**：Acceptance Authority = **由部署所有者明确指定的具名自然人或组织角色**。架构审查意见与 AI 审查过程**不构成风险接受签署**；**未指定 Authority 时，任何风险状态不得进入 ACCEPTED_RESIDUAL**。单人期允许 Owner 与 Authority 同一人兼任（不强制 SoD），但**必须逐条记录兼任事实及其自批风险**。
**Status 统一枚举**（Acceptance Matrix 只消费这四个值）：`PENDING_ACCEPTANCE`（责任人未指定或未签署）｜`ACCEPTED_RESIDUAL`（有权主体明示接受残余，补偿控制有证据）｜`REVOKED`（触发条件命中，接受作废）｜`CLOSED`（风险源已消除且有 Closure Evidence）。长期残余以 `ACCEPTED_RESIDUAL`+复审循环表达，**禁止**以 CLOSED 伪装。

字段（7）：Owner｜Acceptance Authority｜Compensating Controls｜Acceptance Condition｜Expiry/Review Trigger｜Closure Evidence｜Status。

- **RR-1** 共享内核 runc-class｜Owner=运维负责人｜Authority=**待指定**｜补偿=双 uid+镜像字节审计+socket 不可达——三件**各自需证据**；V-5 范围=docker.sock、等价 Docker API 暴露、host.docker.internal、宿主 loopback 服务集及其鉴权（"不可达"禁只测一个挂载路径）｜条件=V-5 PASS（指定版本/环境/场景内）+三补偿有证据+Authority 签署；**V-5 PASS 只证明该测试条件下可达性结果，不自动证明共享内核风险已被充分补偿**（接受判断权在 Authority）｜**REVOKED 触发**（亦为 gVisor/Kata ADR 触发）：① 可复现容器逃逸或宿主侧未授权访问；② V-5 发现沙箱可达 Docker daemon/宿主敏感服务/其他越权入口；③ 补偿控制无法在目标威胁模型下成立；④ 内核漏洞或运行环境变化使接受依据失效——**基于威胁与证据触发，不基于验证项名称触发**｜Closure Evidence=逃逸面测试套件+ADR 记录｜Status=**PENDING_ACCEPTANCE**。
- **RR-2** 注入无内容级过滤｜Owner=平台负责人｜Authority=待指定｜补偿=结构性三件套+T2 收紧+evals 回归门｜条件（**接受与关闭分离，复审修正**）=指定版本下 T2 结构性控制验证通过并留记录 + Owner 与 Authority **明示接受内容级注入的剩余残余** + 每次注入类事故或控制面变更触发复审**；复审强制触发事件（v0.2.3，P1-3）：新型注入攻击导致权限边界突破 / 注入导致未授权对外数据传输 / 结构性控制变更 / 关键模型或 harness 行为变化——必要时 REVOKED**；**T2 全维绿只证明边界符合参数表，evals 只证明特定语料与版本——均不构成"注入风险消失"的关闭证明**，本风险永远以 ACCEPTED_RESIDUAL 存在（无 CLOSED 路径）｜Closure Evidence=不适用（长期残余），复审记录必附｜Status=**PENDING_ACCEPTANCE**（Authority 待指定）。
- **RR-3** 网络开放（临时缺口）｜Owner=平台负责人｜Authority=待指定｜补偿=零凭据（明示：补偿≠替代；运行语义按 §8.0——未关闭前 T2 网络维只能标 Degraded/Gap）｜Acceptance Condition（证据式）=零凭据验证记录 + §5.1 三态声明与 §10-A7 Gap 标注生效 + Authority 签署，三者齐备方可在**限定期**内接受｜**Expiry/关闭五项**：① V-6 六用例全 PASS；② 实际 Agent 网络命名空间/代理/宿主转发链的版本与环境登记完成（=§7.3 V-6 绑定项）；③ **DestinationPolicy 配置↔执行路径一致性核验**（生产与测试环境不同配置不得互推）；④ 正反测试与出口观测点反证可复核；⑤ **防静默旁路**——目标部署配置变更触发复测门（变更守卫），否则视为控制失效｜逾期=System health 红+升级 Owner｜Closure Evidence=五项记录集｜Status=**PENDING_ACCEPTANCE**。
- **RR-4** 无工件签名｜Owner=发布负责人｜Authority=待指定｜补偿=digest 链+TLS 源+验证器自测（F0a）｜Acceptance Condition（证据式，v0.2.2 修正——阶段描述不是接受证据）=F0a digest 验证器自测 PASS + 分发范围限定私有通道有配置证据 + 平台安全状态声明按 §10-A7 标 `Planned/Gap` 生效，三者齐备+Authority 签署方可接受｜Expiry=P6 行｜Closure Evidence=cosign 验证演练｜Status=**PENDING_ACCEPTANCE**。
- **RR-5a**（拆分）静态介质明文——离线介质获取｜Owner=基础设施负责人｜Authority=待指定｜条件=**O3 确认全盘加密 + 密钥管理 + 恢复可用性**（三者缺一不接受）｜范围声明=仅保护离线介质｜Closure Evidence=O3 记录+恢复演练关联｜Status=**PENDING_ACCEPTANCE**。
- **RR-5b**（拆分+v0.2.2 攻击者能力分层）运行时明文暴露面。**范围声明**：本风险覆盖**未取得宿主特权控制权**的同宿主进程读取面 + 运行时诊断/临时文件暴露面；已取得宿主 root 或 Docker daemon 管理权限者=**Z0a 完全失陷情形**（覆盖 AS1-12 全部），文件权限矩阵对其**不构成有效补偿、不得计入本风险的补偿证据**；该情形作为宿主控制域失陷风险由 RR-1 与 §4 B4 承载。

  | 攻击者能力 | 文件权限矩阵的补偿地位 |
  |---|---|
  | 同宿主、不同低权限 UID 进程 | 可验证补偿（V-7 类①） |
  | 同 UID 其他进程 | mode 检查**不充分**——需进程隔离与权限边界证据（V-7 类②） |
  | 宿主 root | 非补偿（Z0a 失陷面） |
  | Docker daemon 管理权 | 非补偿（AS9，Z0a 失陷面） |

  Owner=平台负责人｜Authority=待指定｜补偿=文件权限矩阵+诊断产物治理（V-7 待验）｜Acceptance Condition=**V-7 在指定版本/环境/攻击者权限模型下完成、类①②读取结果分别留证、root/daemon 越界明确列出不受补偿覆盖、Owner 与 Authority 对剩余风险范围明示接受**｜Expiry/Review Trigger=文件权限、运行身份、诊断产物路径、备份路径或宿主进程隔离策略变化即复审；宿主权限模型变化或发现越权读取→REVOKED｜Closure Evidence=V-7 分级记录+运行面评估｜Status=**PENDING_ACCEPTANCE**｜独立验证=**V-7，不得挂 V-5 代验**。
- **RR-6** 单人无自然人归因｜Owner=平台负责人｜Authority=待指定｜补偿=token+actor 占位｜Acceptance Condition（证据式，v0.2.2 补）=dashboard 访问面收敛 loopback/SSH 隧道（§2-Z2 定义）有配置证据 + 全部写 actor 为占位值且平台安全状态声明按 §10-A7 标 `Target` 生效，两者验证+Authority 签署｜Expiry=P4.0 身份基础 Exit｜Closure Evidence=归因演练（每写可溯至具名主体）｜Status=**PENDING_ACCEPTANCE**。
- **RR-7**（v0.2.3 新增，P0-3）**沙箱资源耗尽与跨边界可用性影响**——无逃逸路径下经 CPU/mem/PID/disk/FD/日志争用波及宿主、控制面、spine 或其他 run；**与 RR-1 分立**（共享内核漏洞≠资源争用），pids 2048 [固化] **仅覆盖 PID 维，不构成资源隔离完成的证据**。｜Owner=平台负责人｜Authority=待指定｜补偿=pids 上限 [固化]；cpu/mem 配置机制 [固化，默认不限→GAP]；disk/fd/log [GAP→§5.2 F0a 参数表]｜Acceptance Condition（证据式）=§5.2 统一资源纪律（爆炸半径 ≤ 沙箱）逐维验证通过（cpu/mem/disk/fd/log/pids/deadline 各自记录）+ spine 卷与宿主关键路径空间隔离实证 + Authority 签署；任一维未取证则该维保持 GAP 且按 §8.0/§8.1 运行语义处理｜Expiry/Review Trigger=资源参数、宿主拓扑、容器运行时版本变化；任何跨边界资源耗尽事件→REVOKED｜Closure Evidence=§9 资源耗尽场景组逐维 PASS（按 §7.1 绑定版本/环境/摘要）｜Status=**PENDING_ACCEPTANCE**。

**当前全部 RR（含新增 RR-7）均不可被引用为"已接受"**——Authority 指定（§7.3 同一纪律：部署所有者提供，模型不推测）是本登记册生效的前置。**指定 Authority ≠ 各 RR 自动接受**——接受必须逐条满足 Acceptance Condition 并签署（复审裁定）。

## 9. Test Strategy（增补）

新增场景组：**资源耗尽**（fork bomb→pids [固化重验]、磁盘灌满→新配额、日志膨胀→新上限、fd 耗尽）；**Git hook/配置投毒**（V-3 攻击剧本双端：仓库侧+全局侧）；**配置加载投毒**（V-1a–d 剧本）；**网络绕行**（V-6 六路径）；A4 链重建攻击+锚发现性演练；红队清单按对手分派（A1 evals、A5 SSRF 复现、A6 逃逸面、A7 更新面）；V-list 记录结构与 `mc logs/health` 关联（证据可寻址）。

## 10. Acceptance Criteria（v0.2.1 修订）

A1（v0.2.3 强化，P0-1）**60 格逐格判定 + 结构化状态**：每格必须标注唯一状态 `[固化]/[新增]/[GAP]/[N/A]`（或等价且互斥的结构化状态）并回答三问——当前是什么状态／谁负责（Owner/契约）／在哪里验证或接受（V/RR/Exit 挂钩点）。**`[固化]` 须引用机制但不得因此自动判 PASS；`[新增]` 须有契约归属与阶段；`[GAP]` 须有 Owner+验证任务或 Exit Criteria；`[N/A]` 须有限定命题与理由（全矩阵仅 B6-S 一格）**。A1 验收的是"状态×责任×验证路径"三元组齐备，不是文字存在——防止矩阵退化为文档完整性检查；A2 T2 **七维**各有可执行验证或"GAP+V/RR 登记"，无静默格——**资源维在 enforcement 参数定案前保持 GAP，表格列名不构成完成**；A3 D30（Authority⊥Task Delegation）在 B7/B4-S/IG/PS 一致引用，**证据/风险语义按 D31 区分引用**（D30=权柄委派边界，D31=证据与状态词汇，职责不混）；A4 RR（1–7）七字段完整、Status 仅取四值枚举（§8 前言）、**全部 Authority=待指定 ⇒ 无风险可被引用为已接受**，Owner/Authority 兼任状态逐条记录；**Acceptance Matrix 中 RR-1/RR-5a/RR-5b/RR-7 的风险边界必须保持区分，禁止合并汇总为"明文/资源风险已处理"式笼统表述（P1-4）**；A5 [固化] 锚点全部经 b22df40 复核；A6 **UNKNOWN 禁令**：任何验收条款不得以"未验证但机制存在"判过（§0 三命题分离的验收投影）；A7（复审收敛版）**平台安全状态声明的证据绑定**——凡由平台产生、展示或对外发布的安全状态声明（UI、报告、API、客户材料、自动生成的交付说明），声称某边界/控制/防护**已生效**，必须可追溯至对应版本、环境和攻击场景的 PASS 验证记录；否则只能标记 `Target / Planned / Unknown / Gap`。**本条约束平台的安全状态声明，不约束设计讨论与审查文献的描述性文字。**

## 11. 修订映射（a：v0.1→v0.2；b：v0.2→v0.2.1 复审"冻结前必须完成"七项+P1 组）

**a**：P0-1→§5.1 三态+§8 RR-3 Expiry+§10 A7；P0-2→§7 V-1a–d+§5 hooks 行四加载源+§4 B6-T 保持 gap；P0-3→§4 全格补齐；P0-4→§7.1 记录结构+UNKNOWN≠PASS；P1-1→§1 AS8–12；P1-2→§2 Z0a–d/Z2/Z3a-b；P1-3→§7 V-3+DT；P1-4→§8 字段+Status；新维度→§5.2；测试→§9；验收→§10；五项保留→§0/§3/§8；设计解释原则→§0。

**b（v0.2.1）**：①B4-S 撤 N/A→§4；②RR-2 接受/关闭分离→§8；③RR-1 ADR 证据触发四条件+V-5 范围扩展→§8/§7；④RR-5 拆分→§1（AS1/5/6）/§4（B2-I/B10）/§7 V-7/§8（RR-5a/5b）；⑤Acceptance Authority 治理定义（自然人/组织角色、审查模型不任、未指定⇒禁入 ACCEPTED_RESIDUAL、兼任记录）→§8 前言/§10-A4；⑥A7 收敛为"平台安全状态声明"定义式→§10-A7/§5.1 纪律行；⑦跨文件版本：MASTER v3.1（D30 全文在行 79、D17 在行 63）与 DC v0.1.1（§6/§7.3 在行 61/68）为磁盘权威版；权威哈希**以 Contract Frozen 时刻 Acceptance Matrix 登记为准**（文内自指哈希每次编辑即过期——不在此维护，此为本契约自证纪律的应用）；复审库快照哈希不符须重取，**按内容哈希不按文件名取版本**；P1-1 资源五要素表→§5.2 统一资源纪律；P1-2 DestinationPolicy 引用+绑定表+任务态三值→§7 V-6/§7.3；P1-3 push 目的地=批准目标→§4 B8-T/§7 V-3；P1-4 Z0c 逻辑域限定→§2；Status 枚举统一→§8 前言/§10-A4。

**c（v0.2.2，v0.2.1 第二次复审"冻结前五项"）**：①RR-5b 攻击者能力分层+范围声明（root/daemon=Z0a 失陷面，文件权限不计补偿）→§8 RR-5b+§7 V-7 逐类结论；②风险状态↔运行语义全局映射（PENDING≠可运行、明示降级=Degraded/Gap、无授权=fail-closed、禁历史推定；ACCEPTED_RESIDUAL 非永久放行；CLOSED≠无风险）→**§8.0**（跨契约共享词汇，RR-3 的 T2 标记禁令为其直接推论）；③RR-5b 七字段补齐+RR-3/4/6 Acceptance Condition 改证据式→§8；④B10-T 目标/实施状态分离→§4；⑤RR-3 关闭五项绑定 DestinationPolicy 实际执行+防静默旁路复测门→§8。后续约束（复审通过项携带）：**所有后续契约遵循 §0 三命题证据语义与 §8.0 状态词汇，涉风险运行策略处引用 §8.0 不重定义**——已录 MASTER **D31**。

**d（v0.2.3，第三次复审三 P0+四 P1）**：P0-1 A1 从"60 格有内容"升级为"逐格 状态×责任×验证路径 三元组"（防验收退化为文档完整性检查）→§10-A1；P0-2 **Degraded Operation Authorization**（§8.1：与 Risk Acceptance 分离、五要件绑定、补偿失效即拒绝、主体机制归 IG、Contract Frozen≠风险接受≠运行许可声明）→§8.1+§8 尾注（"指定 Authority≠各 RR 自动接受"）；P0-3 **RR-7** 资源耗尽跨边界可用性（与 RR-1 分立；pids 固化≠资源隔离完成）→§8；P1-1 证据完整性四新字段+reviewed_by 兼任显式化→§7.1；P1-2 PASS 版本/环境/配置作用域限定+旧 PASS 不继承→§7.1；P1-3 RR-2 复审触发四事件扩充→§8；P1-4 Acceptance Matrix 禁合并汇总→§10-A4。**跨文件内容级核对记录（本轮完成，非摘要依赖）**：MASTER v3.3 D30 正文含 Authority⊥Task 三段式✔、D31② 含 Degraded Operation Authorization 引用与 Contract Frozen 性质声明✔（与本文 §8.0/§8.1 同口径）；B7-E 引用已按"D30 管委派、D31 管证据语义"拆分✔；DestinationPolicy 唯一定义源=MASTER §6-3/D-G5，本契约仅承担验证义务不重定义✔；备份语义唯一源=DC §7，B10/RR-5a 仅引用✔。权威哈希以 Contract Frozen 时刻 Acceptance Matrix 登记为准（§11-b⑦ 纪律不变）。
