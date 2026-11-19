# S-Horizon 迭代路线：自持内核的企业软件工厂（v2 · SUPERSEDED）

> **本文已被取代**：权威决策载体为 [docs/contracts/MASTER-ARCHITECTURE.md](docs/contracts/MASTER-ARCHITECTURE.md)（v3 冻结候选，含 D21–D30 与四项销账）。保留本文作决策历史，不再更新。

- 日期：2026-11-19（v2 同日：并入 grilling 第 5–6 轮结论）。基线：Culler `b22df40`；对照取证：LoopForge `451d38c`。
- 前置：`ANALYSIS-2026-11-19.md`（Culler 深度评审）、`ANALYSIS-2026-11-19-LoopForge-vs-Culler.md`（对比）。本文为二者之上的**决策记录 + 阶段规格**，grilling 会话（Q1–Q28；决策 D1–D20）的收敛产物。
- 证据级别沿用：[E1]=源码直证。

## 0. 决策记录（不可再默认更改，除非新一轮 ADR）

| # | 决策 |
|---|---|
| D1 | 基座 = **Culler**（Q22，C2）；LoopForge 为设计捐赠者，模式重写为主，仅自包含无状态件可移植（Q3） |
| D2 | 定位改写：从"单操作员个人工厂"演进为 **企业级、同时支持单人与多人团队协同的自托管软件工厂**（Q25-B：大客户私有化 + 采购合规姿态）；两条身份红线不变——VISION 驱动的主动产线不被工单化稀释；"拥有工厂"= 自托管+审计+主权数据。**多租户 SaaS 为显式非目标** |
| D3 | 部署主形态 = **x86_64 Linux 服务器**（headless、7×24）；arm64 Mac/Linux 保持兼容（Q23） |
| D4 | 模型接入 = 商业 API（GPT/Claude/Gemini/Qwen/Kimi/GLM/DeepSeek/MiniMax…）+ 私有推理（vLLM/SGLang/Ollama）经 **三层 provider registry**（§3）；agent 永不控制目的地——这条既有防线不动 |
| D5 | 数据库 = **SQLite spine 保留**；MongoDB 否决；PostgreSQL 仅在"多写者协调/HA 成为已证实需求"时经 ADR 重启讨论（用户诉求由服务器移植+身份层满足，不由换库满足） |
| D6 | 远程 runner / 多机执行 = **移出路线，ADR 冻结**（Q9=A；算力在模型侧，编排器单机足够） |
| D7 | 不变量保持：spine 唯一权威、mc 唯一写者、DB 层状态机纪律、凭证代理模型（真凭据不进容器）、Decide 纯函数 |
| D8 | `Decide` wire shape：成本闸门走 **2b（authorize 时下发 per-token 额度）**，wire 16→17 的 2a 方案缓行（Q18） |
| D9 | 迁移器：账本表进 v1 基线；`OnboardSpine` 内嵌执行；journal `verify` 窗口内迁移；四态检查点防误销毁（Q10–Q13） |
| D10 | v1 基线极简（只加 `schema_history` + 账本自身带 `actor`/`tenant_id` 占位）；**usage 表 = 第一个真迁移 v1→v2**，含 blob→表回填（Q14/Q17） |
| D11 | push 走宿主直跑（token 不出宿主、零容器出网面）；远端权威四规则：本地权威/自证 pushed_sha/只快进+lease/PR 状态先只进展示层（Q15/Q16） |
| D12 | 用量未知记 `unavailable`，不估算兜底；预算闸门只认已确认量（Q19=A） |
| D13 | Q15–Q19 其余按推荐记录（异议窗口见 §6） |
| D14 | **二开改名 S-Horizon**（O6 已确认拼写）：产品/品牌/dashboard 全用户面更名，成为独立演进的二开产品；执行分 R1 文档品牌层 / R2 基础设施标识符层（§2R） |
| D15 | **派生身份合规**：MIT 义务履行——保留上游 LICENSE 与版权声明、新增本 fork 版权行、README 显式"derived from Culler"致谢段落；`mc`/`spine` 等工程代号保留为内部术语（用户面与代理面契约的最小改动原则，见 §2R） |
| D16 | 企业形态 = **Q25-B**：私有化单组织边界 + 采购合规姿态（OIDC、SIEM 导出、SBOM/签名工件、暴露面收敛）；跨组织共享实例（SaaS 多租户）**非目标** |
| D17 | 数据库 = **SQLite 现役、PostgreSQL 目标、书面晋升门**（Q26-A；门条件 §4B.0）；双方言抽象层否决；三条 substrate 纪律入 §5 第 7 条 |
| D18 | 签署层（Q27）：VISION/产线配置归 owner；签署权可委派、SoD **可配置非默认**、超时升级、签署防篡改；**批准永不产生新需求**（结构性红线） |
| D19 | 企业功能第一批序（Q28）：签署(b)→准入配额(c)→审计防篡改(e)→平台 metrics(f)→OIDC(a)；chargeback(d) 搭 P2b；SBOM/签名(g) 与 Worksources(h) 入 P6 |
| D20 | 命名终选：产品 **S-Horizon**、CLI **`shz`**；版本谱系 **v0.1.0 重启本 fork 序列**，旧 Culler 部署一次性卸载重装，不建兼容层（O7/O8） |

## 1. 阶段总览与排序理由

```
P0 Linux-amd64 服务器移植 + 凭据后端     （零 schema；本周期可用，一切的地基）
P1 版本化迁移器 + v1 基线冻结            （之后每片的 schema 变更都骑在它上）
P2a Provider registry 层2/3 + 模型能力注册 （端点放开，绑 P0 的密钥后端）
P2b 成本线 phase1-2（usage 解析/报表；v1→v2 首迁移=usage 表）
P3 远程交付（push→PR→远端观察）          （v2→v3）
P4 多人/暴露治理（身份、actor 归因、签署层、团队准入、审计防篡改、metrics、TLS/反代）（schema 最重一片；规格见 §4B）
P5 成本线 phase3 预算闸门（2b authorize 额度下发）+ chargeback 报表 + 受控进化（runeval 移植，可选）
P6 合规与生态层（OIDC 收尾、SBOM+工件签名、Worksources 进产线、共享定义治理）
──
R1 品牌文档层改名 S-Horizon（立即，与 P0 并行，零代码语义）
R2 标识符层迁移（搭 P0：安装/服务/资产/密钥路径，CLI=`shz`；schema 文本搭 P1 v1 基线冻结，零迁移成本）
```
排序原则：**先解锁用户日常（P0/P2a），再付基建债（P1），一切 schema 变更后置到迁移器之后（D10），多人化晚于"单人+远端交付"闭环跑通（Q22 连带结论）**。P2a/P2b 可并行（共享 proxy，不共享 schema）。P4 内部按 D19 顺序展开：签署→准入→审计→metrics→OIDC。

## 2. P0：x86_64 Linux 服务器移植

**范围**
- 架构闸改造：`containerruntime_linux.go:115` 的"arm64-only"改为 **host-native-arch 策略**（amd64|arm64 二选一，禁模拟）；全链 `--platform linux/arm64` 硬编码改参数化（清单已取证：`runner/image/build*.sh`、`environment-lane.ts`、`opencode_models.go`/`opencode_source.go`、CI `release.yml:82,158,391-403` 等，[E1]）。
- opencode CLI 包链：按架构选包（上游有 linux-x64 [外部知识，P0 第一周验证]）；`@opencode/cli-linux-arm64` 硬路径消除。
- 监督：launchd→**systemd user/system unit**（`supervision.go` 平台面同形替换 + `culler_services_prod.go:40` 去 launchctl 硬编码 + `uninstall`/`redeploy`/`dev-reset` 对称改）。
- **凭据后端（唯一无现成替代的高阻力项）**：新增 Linux 实现——age 加密文件（0600、属主钉死、nlink==1、O_NOFOLLOW，复用 `distribution.Credential` 已有的文件卫生校验模式 [E1] `client.go:314-338`）+ 解锁路径（systemd-creds 或操作员口令）；Keychain 后端保留为 darwin 路径；接口不变（`host_secret.go` 的 kind/reference 泛型今天已支持）。（此条 = 你的 GPU 服务器 + 一切 server 形态的硬前提）
- 网络语义：proxy 监听从 `127.0.0.1` 改可配置 bind + 与 `host-gateway` 的匹配（[E1] `provider-proxy.ts:449,774`），默认仍 loopback；服务器形态暴露治理留 P4，P0 交付物是"SSH 隧道访问 dashboard 的无头服务器"——**这是故意保守**。
- 安装资产：`install.sh` Darwin 硬拒（`:74`）改平台矩阵（linux-{amd64,arm64}/darwin-arm64）；发布管线产出 linux tarball + systemd 单元；GH Actions 双架构 job。
- dashboard/resident Bun 面：低阻力（libc 分支已存在；osascript 降级已有 [E1]）。

**验证**：`./mc/check.sh` Linux 全量 + docker_boundary/docker_e2e（Linux 原生跑，UNVERIFIED 面积反而缩小——darwin 专属测试显式化）+ 新增"amd64 服务器 smoke"验收（干净 Ubuntu/Debian、systemd、无桌面、SSH 隧道、跑一个 fake-harness 全流水线）。**性能红利记录为正面证据**：Linux 上 mc 直开 spine，macOS 的每调用 docker-exec 委托整体消失（`runtime_scope_prod.go:12` [E1]）。
**回滚**：纯部署形态变更，无 schema，可退回 Mac。
**工作量**：~25–40 文件 + CI 矩阵；P0 内最大单项风险 = 凭据后端设计评审（L3，建议独立 ADR-0）。

## 2R. 品牌迁移：Culler → S-Horizon（D14/D15/D20）

**原则**：用户面全改、工程内核代号不动、schema 文本蹭 P1 基线免费换、合规致谢不可少。

**R1 · 文档与品牌层（立即执行，零代码语义变更）**
- `README.md`/`README*`：产品名、tagline（建议保留"软件工厂"叙事，主权措辞按 D2 改写为自托管团队工厂）、安装指向本 fork 仓库；新增 **"Derived from Culler"** 致谢段（作者 Vincent Santos、上游仓库、MIT 义务声明）。
- `LICENSE`：**保留原版权行**，追加本 fork 版权行（MIT 标准派生实践）；`THIRD_PARTY_NOTICES`（如新增）承接第三方许可。
- `AGENTS.md`：全量更名改写（项目段落、`mc`/spine 术语保留并注明"内部工程代号，不改"）、`INSTALL.md`/`GIT-DELIVERY.md`/`SECURITY.md`/`CHANGELOG.md`（新开 fork 身份段，注明版本谱系决定 O8）、`config.example.toml` 注释文案。
- Dashboard：标题/主题命名（"Culler blue"→新命名）、logo/hero 资产替换、`PAGES.md` 文案。
- `openwiki/`：**不由人工改**——按本仓自身规则由 Scribe 从 accepted commits 重新生成基线；R1 合入后触发一次全量 wiki 重写（Scribe 跑通本身就是 dogfooding 验收）。
- 上游痕迹清理：badges（workflow 状态、star-history、trendshift）、社区二维码/群、`origin` 重命名。

**R2 · 基础设施标识符层（搭 P0，一次改对）**
| 面 | 现值 → 新值 | 迁移动作 |
|---|---|---|
| CLI 二进制 | `culler` → **`shz`**（D20） | 安装脚本、PATH、`culler_services`、skill 目录 `~/.agents/skills/culler` → `~/.agents/skills/shorizon` |
| 部署 home | `~/.culler`（`MC_HOME` 默认值） | **env 变量名 `MC_HOME` 与 `MC_*` 前缀保留**（内部代号，见原则）；默认路径改 `~/.shorizon`；老部署一次性搬迁步骤（文档化，非代码兼容层——用户基数=自己） |
| Keychain | 文件 `culler.keychain-db`、service `Culler-<sha256(MC_HOME)>` [E1 `host_secret.go:25-38`] | → `shorizon.keychain-db` / `S-Horizon-<sha256(home)>` + 一次性迁移（旧读→新写→删旧，P0 凭据后端 ADR-0 一并设计，**Linux 后端新起即无历史包袱**） |
| launchd/systemd | `com.culler.*`（build-prod 拒载守卫、supervision plist）[E1] | Linux 新 unit 直接 `shorizon-*` 命名；darwin 兼容路径换 `com.<域名根>.shorizon.*`（域名根见 O9）+ 守卫同步 |
| 容器/refs | `mc-*` 容器名、`refs/mc/applications`、`mc-land`/`mc-real` | **保留**（`mc` = Mission Control 内部代号，改动波及面大且零用户价值） |
| token 前缀 | `culler_proxy_*` | 改 `shz_proxy_*` 类新前缀（掩码正则 `trace-mask.ts` 同步 [E1]；与 P2b 有交集，改在 P0） |
| 发布/更新通道 | GitHub release 源（现指 vinchenkov/Culler）、资产名 `culler-darwin-arm64.tar.gz`、`distribution/record.go`、`update_policy.json` [E1] | 指向本 fork releases；新资产命名（含 linux-amd64，与 P0 共用改动）；`minimum_version`/版本谱系按 O8 |
| schema.sql 内品牌文案 | 触发器 RAISE 消息等含 "Culler" 处 | **搭 P1 v1 基线冻结一起改**（零迁移成本；过了 v1 再改就是付费迁移，顺序不可颠倒） |
| 角色输入文本 | directives/brief/skills 中的 "Culler token/Culler Project" 等表述 | 按 AGENTS.md"改角色输入三件套同步"义务执行；这是唯一**故意触及 agent 语义**的改名，放 R2 末尾单独 commit，跑角色指令检查 |

**验收**：R1——全仓 `grep -ri culler` 只剩 LICENSE 版权行、致谢段、`mc` 代号白名单；R2——干净服务器（amd64）与干净 Mac 各完成一次端到端安装→跑通 fake-harness 流水线→批准→合并，全程无 "Culler" 用户可见残留；`./mc/check.sh` 与发布闭包测试全绿。
**回滚**：R1/R2 均为普通分支变更；Keychain/数据搬迁提供旧路径只读 fallback 一个版本周期。

## 3. P2a：Provider Registry 三层（商业 + 私有）

```
L1 release catalog（现状保留）：测试过、版本钉死、目的地内嵌 —— 已含 Qwen/Kimi/GLM/DeepSeek/MiniMax/Gemini 等 209 厂商
L2 operator allowlist（新）：宿主配置的显式自定义 provider，全部既有防线复制（proxy 路由来自受信配置、agent 不可见目的地）
L3 模型能力注册（新，随 L2）：每条 model 声明 {usage_format 家族, tool_calling, structured_output, effort, context_window}，缺失字段默认取保守值
```
- L2 配置语义：`[[provider]]` 条目 = name、base_url（可 http，但需条目级 `insecure_explicit_ok = true` 显式认罪字段 + 启动日志警告；若服务器能上内部 CA/TLS 则建议不用该字段——见 §6 开放项 O2）、auth kind（bearer/x-api-key/x-goog 复用现有 keyPlacement [E1]）、model 来源（静态列表或 `/models` 实时发现+缓存审核）。
- 校验链改造点全部已定位：Parse/Validate 的 forbidden-base_url 拒绝改为"kind=custom 白名单路径"（`config.go:421-432,1079-1128`）、projector/proxy/access 三处 https 钉改为**条目策略驱动**（默认 https、`insecure` 显式降级）[E1]。
- L3 是"各种商业小模型私有模型都能当好 Worker"的真正防线：agent 体验由 harness 决定——L2 条目若 tool_calling 未声明/为假，**dispatcher 不得把需要工具调用的角色派发给它**（能力路由进 connection/profile 校验，不碰 Decide）。
- vLLM/SGLang/Ollama = openai-compatible 家族特例，无专门代码；Ollama 的 usage 可靠性归入 D12 `unavailable` 口径。

## 4. P1 / P2b / P3 / P4 / P5 摘要（规格细节见 grilling 记录 Q10–Q19）

- **P1 迁移器**：`schema_history` 进 v1 基线（[E1] 自举悖论对你不存在：0.1.0 史无 schema 变更、无存量 spine）；`meta.schema_id` 同事务更新；每步 DDL/数据变换单事务（BEGIN IMMEDIATE）；`DeployedDatabase` 扩四态 `absent|current|migratable(step-k)|unknown` 堵死"differs→误 reset"通道 [E1]；迁移在 update journal `replace→verify` 窗口、`committed` 前；redeploy.sh 补"旧二进制先快照"；三 SQL 文件哈希纪律与 hello 协商**零改动**。
- **P2b 成本**：proxy `ProviderEventEvidence` 已有 SSE 逐事件 JSON 解析位——usage 按家族分派顺路提取（零新架构 [E1]）；先落 `diagnostic_events` blob（零 schema，并行于 P1）；**v1→v2 首迁移 = `usage_events` 表（带 actor/tenant 占位）+ blob 回填**（让迁移器首个实战考验数据变换）；`end_reason` 加 'budget' 搭车 v2；报表 = mc 只读 verb + dashboard 页（`host_release.go`/`UI_MODULES` 清单纪律）。
- **P3 交付**：`push_operations`/`project_remotes` = v2→v3；push 走 repository-lane 宿主直跑 + Keychain 同源 github secret（kind 字符串零格式变更 [E1]）+ `protocol.https.allow` 仅 push 路径开；自激环防护 = 记录 `pushed_sha` 自证 [E1]（quiet 判定现为纯字符串相等 `delivery.go:1059`，已取证）；PR 状态只进展示与归档门，**不进 Decide wire**。
- **P4 多人**：users/sessions（bcrypt+HMAC cookie 的基线卫生参考 LoopForge 但按其标准加强：服务端会话可吊销——LoopForge 没做到的）、`actor` 占位转正、**审批事件带自然人**（Culler 的 append-only activity + `operator_requests.origin` 有现成落点 [E1]）、按人 verb scope（复用"身份只来自不可伪造载体"范式：session→capability，对齐 run.json 纪律）、owner 一票否决、SoD 选项（approver≠implementer）；**dashboard 允许离开 loopback 的前提 = 本阶段全部完成 + 反代 TLS 文档**；Collie 从"操作员权柄代理"改为"具名成员权柄代理"。
- **P5 预算闸门（2b）+ 受控进化（可选）**：authorize 时从 spine 读已知名额→per-token `maxTokens`（容器路径现传 undefined 的那根线接上 [E1]）；预算拒绝码进**三张码表**（proxy/runner diagnostics/mc provideraccess [E1]，否则被归为 connection 不扣额度→无限重派烧钱——本计划最凶险的坑，修复在 P2b 就做防御性归类，P5 做完整）；runeval 移植（run 复盘→角色改进候选→人工 apply）。

## 4B. 企业层设计规格（Q25-B / Q26-A / Q27 / Q28 的产物）

**0. Substrate 晋升门（D17 的书面化）**——SQLite→PostgreSQL 的启动条件，满足其一即开 ADR，未满足不谈：① 报表聚合在实测负载下成为瓶颈（json/生成列扫描 P95 超阈值，性能 lane 出数说话）；② 需要多个无状态应用实例横向扩（API 面出现真实并发）；③ 首个客户合规条款点名（PITR/HA/PG 生态审计工具）；④ 备份恢复目标超出 `VACUUM INTO` 快照链能力。换库=一次有迁移器兜底的 L3 数据+约束格网移植（226 触发器语义逐表翻译），mc 唯一写者缝保证**消费者零改动**。PG 时代的对等设计已预留：RLS 按 `run_id/tenant_id` 过滤 = 今天"spine 卷 + setuid 双 uid"的企业翻译。

**按 D19 顺序的六个设计件**（全部落 P4，末两件落 P5/P6）：

1. **签署层（b）**：`signoffs` 为 spine 一等公民——`(run/version, signer_actor, role_at_signing, policy_snapshot, prev_hash)`，触发器保证签署记录不可变、版本绑定（沿用 packet 批准"点名 live packet+version、旧包即拒"的既有语义 [E1]）；签署权**可委派**（委派记录含有效期，杜绝无限转授）；**SoD 为项目级可配置策略**（默认关，`policies.sod_enforced`），开启后 approver≠implementer 由触发器硬拒；超时升级 = 催办沿委派链上行（借 LoopForge 等待陈旧语义：run 终态/attempt 变化即撤回过期提醒）；**批准永不产生新需求**（D18 结构红线：签署动作在 schema 层无创建 task 的路径）。
2. **团队准入配额（c）**：三层——并发 run（每 team/每 project）、日 token 预算（**只计已确认量，D12**）、容器资源；形态借 LoopForge `run_admission`（DB 桶锁+排队态+防死锁锁序 [E1]），落点为 spine 触发器 + Decide 的 **stored-fact 输入通道**（`WaitUntil` 同构 [E1]，零 wire 变更）。`tenant_id` 占位列在 team 表落地时迎来首个真实消费者。
3. **审计防篡改与导出（e）**：activity 表加 `prev_hash` 链式列 + 触发器强制（行改不得、序不得断）；导出 = mc 只读 verb（NDJSON + 链验证器工具）；保留策略入 `settings`；SIEM 格式适配是部署层 concern，核心承诺 = **可离线验证的导出**。
4. **平台自身观测（f）**：resident/dashboard 暴露 `/metrics`（OTel SDK）——两原生项目共同盲区、S-Horizon 的补位卖点；SLI 基线集：dispatch 延迟、队列滞留、run 成功率、proxy 时延、容器启动 P95/P99（顺手把 LoopForge 没做的"编排自身 SLO"补上）。
5. **SSO（a，P4 尾部）**：**OIDC 优先，不做 SAML**；本地密码保留为 breakglass；**session 服务端可撤销**（点名反 LoopForge：它只有全局 secret 轮换、无单会话吊销 [E1]）；JIT 建号 + 团队映射。
6. **chargeback（d，搭 P2b）**：`usage_events`（已带 run→task→project FK）× project→team 映射 × 牌价 = 团队成本报表；已确认/未确认两列**永远分开展示**（D12 口径进产品 UI，不做假精确）。
7. **P6 件**：SBOM+工件签名（cosign/sigstore；project_image 字节审计是现成种子 [E1]——从"检查镜像"升级到"证明镜像"）；Worksources（issue tracker → 需求进产线，Strategist 以之为证据、priority-2 规则不动 [E1]）；共享定义治理（**发布者不得借共享定义取得订阅方权限 + 版本不可变**——LoopForge 已验证的两条不变量 [E1]，模板共享前先行入触发器）；路由清单门 + OpenAPI 覆盖测试（LoopForge 三门禁直接移植，与 `test.sh` 零测试纪律同族）。

## 5. 工程纪律承接（防止迭代把 Culler 写成 LoopForge）

1. 每片自带迁移与回滚定义，P1 后一切 schema 变更走 `schema_history`（CHANGELOG "Database compatibility" 小节强制）。
2. **慢线进 CI**：P0 起 docker_e2e 与 nightly property 至少 cron 化（Culler 现存的自觉债；多人化后不可接受）。
3. `-race` 纳入 Linux 门（Culler/LoopForge 共同缺失，移植后 Linux 原生跑 race 检测成本最低）。
4. wire shape 变更（未来若上 2a）单开 ADR + golden + mutant + property 诚实性，不与功能片混装。
5. 前端静态面（TS typecheck）在 P4 dashboard 扩张前落地一次（两项目共同教训，越早越便宜）。
6. 文档同步义务按 AGENTS.md（directive/brief/skills 三件套、UI_MODULES/host_release 清单）——P4 改角色输入时最易违规。
7. **Substrate 纪律（D17 连带，即日生效）**：(a) 新表一律 RLS-ready——带 `actor`/`tenant_id` 列、主键用显式 INTEGER/TEXT 不依赖 rowid；(b) SQL 只存在于 mc 内——TS/dashboard/runner/报表零 SQL，全部经 mc verb；(c) **一切外部输入参数绑定，禁止拼接/format/f-string 组装 SQL**（含 mc 内部），作为 code review 与 CI 门。
8. **非目标清单（定位漂移护栏，改动需过 ADR）**：多租户 SaaS（D16）；双方言抽象层（D17）；远程 runner/多机执行（D6）；批准产生需求的任何工作流形态（D18）；估算值参与预算闸门（D12）。

## 6. 开放项与异议通道

**沉默确认已全部生效（2026-11-19，第 6 轮无异议）**：Q15（push 宿主直跑）、Q16（远端权威四规则）、Q18（预算三层含 2b）、Q19（unavailable 口径）→ 固化于 D11/D12/D13。

**开放项（仅剩开工时事实，非设计缺口）**：
- **O1**：P0 凭据后端最终选型（age 文件+口令 vs systemd-creds vs 预留 Vault 接口）——ADR-0，P0 开工前定。
- **O2**：你的 GPU 服务器能否上内部 TLS？能则 `insecure` 字段从 L2 语义整个删除（更干净）。
- **O3**：amd64 服务器硬件/发行版与 Docker Engine 版本（systemd unit 与 host-gateway 行为随版本变）。
- **O9（新）**：标识符域名根——launchd `com.<域名根>.shorizon.*`、OIDC issuer、发布仓库 URL、`shz` 更新通道地址共用一个域名决定（有自有域名即用，无则先以 GitHub org 名义占位，R2 前定）。
- **O4（已闭）**：否——服务器形态 dashboard 访问以 SSH 隧道顶到 P4 暴露治理完成；不做过渡性自签 TLS。
- **O5（已闭，维持缓议）**：harness family 扩展（Gemini 原生 CLI 等）独立维度，出现真实需求再议。

**已决记录**：O6 = **S-Horizon**（本文全量更名完成，R1 执行时同步代码/文档）；O7 = CLI **`shz`**；O8 = **fork 以 v0.1.0 重启版本序列**，旧部署一次性卸载重装。

## 7. 一句话风险声明

这条路线最大的风险不在任何单片技术，而在**节奏**：P0+P1 约两个月无用户可见功能；若中途忍不住从 P2a 或 P4 先做（多人界面很诱人、端点放开很急人），会在"无迁移器的生产 spine 上做多用户 schema 变更"处爆雷——那正是本路线要消灭的事故形态。第二风险是**定位漂移**："企业级"的引力会持续把产品拉向多租户 SaaS、策略引擎、双抽象层这类"看起来更 enterprise"的形态——§5 第 8 条非目标清单是护栏，任何越界必须走 ADR，不许借功能片夹带。
