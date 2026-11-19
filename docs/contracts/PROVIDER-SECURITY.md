# CONTRACT · PROVIDER-SECURITY（v0.1.4 · RECOMMENDED · 待批准）

- 状态：**RECOMMENDED v0.1.4**（审方 9.5/10 声明：两澄清落实后直接 RECOMMENDED，不再要求修订轮；**PS 文档工程停止**——重开仅限三情况：契约间真实矛盾/语义不可实现/实测推翻假设）。复审史：v0.1.3 限域复审 9.5/10——PS-8（provability/持久序/负能力）与 PS-9（fencing token）**通过**；PS-7 余两澄清（A 路径两层上界分离；B1 预算入 min 须自身可验证强制）已落实。与 §1/§3/§4/§6 及 TM/DC/IG/BL 冲突检查：审方未发现新结构性冲突。**PS 文档工程至此停止**（审方裁定：后续仅三类情况重开标准——契约间真实矛盾/语义不可实现/实测推翻假设）。
- 审查证据边界镜像：`[固化]`/`[E1]`=提交稿声明；`[参考实现]`=LoopForge gitegress 模式核读（connect 时 Control 钩子+禁重定向+禁 keep-alive，本轮亲读其源码 gitegress.go:20-60），借用的是**模式**非其验证结论。
- 消费不重述（沿 v0.1）：MASTER D-G5/§6.3 上位语义（A1 比对义务）；TM V-6 验证归属；IG §6 零缓存；BL §5(b) 接口（本稿 §5 为其唯一可证性源）；D12/D22/O1 关系。

## 0. 不变量
P1 目的地永不来自 agent/请求侧。P2 默认拒绝：未登记端点不可达；scheme 仅 https（**v0.1.1：`insecure` 字段删除**，§7-O2 处置）；loopback/私有/保留地址默认拒，放宽仅条目级显式 CIDR。P3 真凭据不入容器（custom 同）。P4 声明≠证实（declared/configured/verified 三态贯穿 §4/§5）。P5 授权位实时 spine 读（IG §6）。**P6：安全属性只声明在 enforcement 点上，不声明在意图上**——本契约所有"拒绝"必须落到 connect/parse/inject 的执行位，无执行位的句子降为 Planned。**（终审裁定：P6 为 PS 局部规范；升为跨契约原则须 MASTER/TM 侧正式治理变更，A10 抽查范围=本契约。）**

## 1. Registry 三层
（L1 不动；L2 字段闭集沿 v0.1，删 `insecure_ok`，增 `port_allowlist`（默认 {443}，条目级扩展须评审）与 `pooling ∈ {off(默认), bound}`（§2.4）。）
**条目生命周期（终审 PS-4，两维过期分离）**：
| 维度 | 语义 |
|---|---|
| 绝对有效期 | `expiry` 必填；到期=禁新授权。**时钟源=单调钟+墙钟双验；回拨/不可用按已验证最大历史时刻保守判定，禁倒退续命；重启同理** |
| 空闲过期 | `idle_ttl`；`last_used`=`成功授权判定`（非 TCP 活动），**持久化于 spine（重启不失忆）** |
| 撤销 | 新授权即拒（P5 实时读）；旧 token 不得用于任何新请求；连接池即刻不可用（§2.4） |
| 在途请求 | **已被 provider 接受的请求不承诺可逆撤销**——取消仅在 provider/传输层存在实证取消能力时成立（IG §5.3 fencing 同理）；否则自然终结、usage 照常对账。禁制造"瞬间消失"的不可证承诺 |

签发=具名流程（Class A 事件，IG §3 模式）。

## 2. Destination Binding and Connection Reuse（P0-1 全链）
**2.1 判定对象**：`Destination = (scheme, canonical_host, port, resolved_IP)` **完整对象判定**，不是 host 或 IP 单因子。canonical_host 规范化规则（冻结语义，实现表归 F0b）：拒 userinfo；IPv4 仅点分十进制规范形（**拒 0x7f/八进制/十进制异常形**）、v4-mapped 一律解出按 v4 策略判、拒 zone id；IDN→A 标签小写、尾点剥离、大小写归一；URL 解析唯一规范形（非规范形=拒，禁宽松解析器差异利用）；scheme×port 匹配（https→port_allowlist）。
**2.2 解析与拨号（[参考实现] gitegress 模式提升为规范）**：验证发生在 **connect 时的实际地址**（socket 层 Control 钩子类机制）——**每一次连接尝试**（A/AAAA、多地址客户端选择、Happy Eyeballs 竞速、重试、重定向后的新连接）都在其自身 connect 时过策略：由此**结构性消灭**"验一个地址、连另一个地址"族攻击，无需枚举时序；CNAME 链终点的实际 IP 天然被同钩子覆盖；**连接建立后 remote IP==已验证地址**为其平凡推论。`allow_cidrs` 命中=条目内放宽，禁全局豁免。
**2.3 TLS 身份**：证书校验+hostname 绑定至 canonical_host（SNI/verify 同名）；`pooling=off` 时每请求重走 2.1-2.3 全链。
**2.4 连接复用（P0-1 补全）**：**默认禁 keep-alive（[参考实现] 同构，附带性质：条目撤销/变更即时生效——每次调用都服从当前 allowlist）**；`pooling=bound` 仅限同 `(entry_id, entry_version, canonical_host, verified_ip set)` 全等复用，任何不等=新连接走全链；**跨条目 coalescing 禁止**；条目撤销/版本变更⇒池内连接即刻不可用。
**2.5 重定向**：永不自动跟随 [参考实现+固化既有]；返回调用方，每一跳作为新请求全链复验。
**2.6 责任切分（不重开）**：模型流量通道由此节全控；provider 响应体内的 URL/工具调用引发的沙箱侧网络=T 层（TM §5.2），本契约不假接管，接口测试见 §8。

**2.7 Enforcement Coverage（终审 PS-1）**：① 全部 provider 出站必经**同一策略执行点**（统一 transport：dialer+connect 钩子+TLS+池）；**禁继承进程/环境级 HTTP(S)/SOCKS 代理**（`Proxy=nil` [参考实现] 同构）——钩子面对代理时验到的是代理 IP，不证明目的地合规；确需代理则该代理**注册为受 DestinationPolicy 约束的显式执行主体**（其出口再过同规则）；② 禁自定义 dialer/Unix socket/裸 socket/替代协议栈接触 provider；**新增 transport 类型=本契约修订**；③ 无法纳入覆盖的传输路径必须标 `Planned/禁产`，**不得继承"出站无旁路"结论**；④ **A2 二分**：攻击向量负例=证明"测过的没穿"；覆盖证明=静态枚举全部出站构造点+lint 禁旁路+运行时连接审计，证明"不存在没测的出口"——互不可替代，齐备方可作"无旁路"平台安全状态声明（P6/X-2）。

## 3. 凭据与授权（P1-4 纪律补全）
临时 token 机制 [固化] 沿用，v0.1.1 增六条：**① secret_ref 解析失败=fail-closed**（禁回退字面量/默认环境变量/他条目凭据）；② token 绑定**条目身份+版本**+route/model/operation（错条目使用=鉴权失败，非降级）；③ **撤销/版本变更的四平面效果（终审 PS-6）**：新授权=即拒（P5 实时读 [固化] liveness 通道）；旧 token=任何新请求即拒；连接池=即刻不可复用（§2.4）；**在途已被 provider 接受的请求不承诺可逆**——按真实语义记账与对账（与 §1 在途行同规），fail-closed 不弱化、也不虚构瞬时消歧；④ 凭据值禁入日志/trace/异常/metrics 标签/调试输出（SecretFilter 域 [固化] 扩展至本契约新路径）；⑤ 跨条目不可互换；⑥ 轮换=版本推进，旧版本即时不可用（与 IG §5.1 密钥环区分：此处是条目版本不是密钥密码学轮换）。`auth_placement→精确 header` 冻结映射：bearer→`Authorization`、x-api-key→`x-api-key`、x-goog-api-key→`x-goog-api-key`；**调用方不得指定或覆盖宿主注入的认证头**；PS-D-3 落定=封闭白名单。

## 4. usage 解析家族与事实身份生成（P0-3 补全）
**PS 职责**：把 provider 原始流事件转换为**有身份、有顺序、有类型**的 observation 序列；**BL 职责**：收敛为每请求唯一 confirmed 效果（v0.1.2 §4 消费）——越权禁止句入约。身份生成规范规则（冻结语义）：`provider_request_id` 关联真实 provider 请求，**不可得时显式登记 unverifiable，禁伪造**；`source_report_id` 请求内稳定唯一不可复用；`source_version` 确定性单调（同来源重复到达的同一事件**不生成新身份**）；`payload_digest` 对规范化 usage 事实计算；同身份异内容→UNKNOWN/RECONCILIATION（禁 LWW）；partial→final 的 supersedes 关系在生成侧保持。
**逐家族生成规则（终审验收面）**：openai-chat：缺最终 usage chunk→UNKNOWN 态非零值；final chunk 身份=该请求唯一末次累计快照。openai-responses：completed 缺失/重复/迟到→按版本规则收敛，重复不重账。anthropic：**input 于 message_start、output 于 message_delta 的组合语义显式化**——单请求收敛为一条累计快照（input 固定+output 取末次 delta），事件级中间态=interim 报告按版本 supersedes。google：usageMetadata 字段完整性检查，缺字段=partial 事实非估算。unknown 家族：**不得猜测/估算入 confirmed**（D12），只产 interim 不可判观测。F0b 向量（AM §4b 五类+本表逐家族）验收前，家族状态=declared 非 verified（A5 不变）。

**4.1 Observation 身份生成与去重（终审 PS-3：从目标语义到可执行算法）**：
① **身份来源序**：provider 原生事件/响应 ID 优先；不存在时=**本地持久化请求身份**（proxy 发请求前生成 `local_request_id` 并落 observation，跨重启稳定）+**确定性事件序号**（请求内单调、随收随赋、持久化）。**本地生成身份永远带 `origin=local` 标记，不得伪装 provider 原生**（终审红线入约）。
② **去重键**=`(provider_request_id | local_request_id, event_seq)` 或 provider 原生事件 ID；**禁以 payload 相同推断事件相同**（同内容不同来源=两个事件，各自入账/对账）。
③ **版本规则**：`source_version` 生成者=PS（非 provider）；累计快照按 `(来源身份, version 递增)` 单调 supersedes；增量事件各以唯一身份入账；序号状态持久化边界=observation 存储，重启从持久态续、不回卷。
④ **unverifiable 的 BL 消费行为（接口补全）**：`provider_request_id` 不可得→字段记 `unverifiable`+身份由 `local_request_id` 承载，**BL 四元组以 local 身份顶格消费，但该类观测只进 UNKNOWN/RECONCILIATION 通道，禁入 confirmed 直结**——与 provider 侧对账对齐后方可 SETTLED；禁伪造/占位假 ID。与 BL §4"同身份异内容→UNKNOWN"闭环。
⑤ **event_identity_provability（终审 PS-8 三分诚实规则）**：

| 输入条件 | 处理（provability 值） |
|---|---|
| provider 原生事件 ID 存在 | 原生 ID 去重（`native`） |
| 无原生 ID，但上游有稳定可跨重启恢复的事件序号/cursor | 本地请求身份+**上游 cursor** 去重（`cursor`） |
| 两者皆无 | **事件同一性不可证：可能重复的增量事实直入 UNKNOWN/RECONCILIATION，禁入 confirmed**（`none`——不要求 provider 提供不存在的东西，要求的是 PS 不假装能区分它区分不了的输入） |

**禁以 payload_digest 补此洞**（终审原文入约：同数值≠同事件；同事件可因传输封装不同 payload 不同）。
⑥ **持久化与位点顺序（冻结）**：收到事件→**先持久化原始事件（身份来源+内容）**→持久化 observation→**最后推进可恢复消费位点**；重启按持久化状态恢复，**对重复投递的同一输入不重新赋号**（序号只随"首次持久化"前进，不随"接收"前进）。
⑦ **负能力声明（入正文，A11）**：cursor=none 且接收-持久-位点间不保证可靠交付时，**PS 不声称具备跨重启 exactly-once 去重能力**——这是契约的一部分，不是实现备注。
分工重申：PS 产"一请求→多带身份有序观测"，BL 收敛"唯一 confirmed 效果"。

## 5. CapabilityRecord 与可证性（P0-2 重写：证据阶梯）
| 等级 | 语义 | BL 可否据此 BOUNDED |
|---|---|---|
| `config_asserted` | 运营声明 | **否** |
| `probe_observed` | 探针观察到特定行为（如返回未超 100） | **单独否**——观察≠强制语义证明 |
| `verified_by_probe` | **provider 执行语义被证明** | **可，六前提全满足**：① 上游语义依据（文档/契约行为）；② 实发上限参数及其生效位置确认；③ 边界/截断/refusal 测试组；④ 不可忽略/静默变更的证伪测试；⑤ 绑定 endpoint×model revision×API version×配置版本×测试环境；⑥ **上述任一变更→旧验证自动失效回落 probe_observed 以下** |
**计费口径约束（首审判词入约）**：BOUNDED 证明的上界必须是**约束 BL 所依赖的计费 completion usage 的上界**——**本地停止读流不构成证明**（provider 可持续生成计费）。因此 ①级=provider 侧强制上界；②级"本地强制上限"仅当平台能证明**计费口径受此上界约束**（如同时约束请求参数并验证 provider 服从）方可 BOUNDED，否则记 `local-capped-unproven-billing`=UNBOUNDED_EXCEPTION 轨道。
**`max_tokens_effective`（新增）**：记录**本请求实际生效的 completion 上限**（模型理论界∩条目配置∩BL per-launch 界），BL §5(b) 消费的是 effective 值非能力名义值。
探针（PS-D-5+终审 PS-2/PS-7）：走 DestinationPolicy/凭据/observation/预算全链（自食其粮）；**bootstrap grant 双组件**——(a) **运行额度**：请求数硬上限+本地读字节硬上限；(b) **损失边界（终审 PS-7 语义三分）**：三条非蕴含 `本地读字节上限 ⇏ provider 计费上限`、`停止派发 ⇏ 停止已接受请求的计费`、`预留金额 ⇏ provider 侧计费封顶`（§1 在途不可逆对探针**自反适用**，禁隐含复活已被否定的能力）：

| 路径 | 条件 | 损失承诺 |
|---|---|---|
| **A** 可证 **completion 计费上界**（provider 侧强制已证） | 计费口径已证（限 completion 维度） | **仅可声明 completion 维度有界**；单请求**总损失上界**当且仅当**各计费组成**（input、多模态输入、工具调用计费等）均有可验证上界且计费口径/价格版本/重试行为皆入界时方可声明——**只证 completion 即称"单请求最大损失有硬上界"=违规表述**（PS-7 两层语义） |
| **B1** 可验证计费侧机制 | provider 计费侧硬机制存在且可验证（如预付额度耗尽即停止计费） | **硬上界=可验证机制实际保证的上界**；批准预算**仅当其自身对应可验证计费侧强制**（如预算即 provider 侧 prepaid cap）方可参与 `min(预算, 机制上界)`——**否则预算仅为暴露授权（B2 语义），禁借 min() 宣称更低硬界**；不产生 verified_by_probe、非 BOUNDED |
| **B2** `authorized_exposure_budget` | 上述皆不可验证时的兜底 | **不承诺损失≤批准金额**；五披露齐备：非硬上界/无 verified_by_probe/非 BOUNDED/预留金额不得表述为 provider 计费上限/**单请求最大成本=UNKNOWN** |

grant 七绑定：验证用途、provider×endpoint×model、最大请求数、**可证上界（A/B1）或批准预算（B2，授权性质非封顶性质）**、有效期、撤销条件、授权主体。探针不得引用未验证能力自放宽（循环依赖由 bootstrap grant 解开；无权限旁路；B2 下亦不伪装费用旁路已封——只承认为**受控暴露授权**）。

## 6. provider 故障治理（P1-5 状态语义冻结）
breaker：状态机 CLOSED→OPEN→HALF_OPEN→{CLOSED|OPEN}，转换触发条件成文；**半开探测单 lease/fence**（并发探测禁止，防探测风暴）；失败率成员资格=**仅 provider 侧故障类**（5xx/超时/网络断流），认证错误/配置错误/调用方业务错误**不得污染**状态；OPEN 期拒绝=域 1 语义（零任务状态，I4 同构）；HALF_OPEN 探测走正常授权+预算+凭据全链；**状态是条目级可用性事实，不进模型评分、不进调度优先级**；阈值/窗口/探测数=F0b 参数。可重试分类表与流式中断不可盲试规则：沿 v0.1（§4 两维 report_kind 联动）。health 入 metrics：沿 v0.1。
**状态持久化（终审 PS-5）**：breaker 状态行翻转与其 Class A 事件**同一 spine 写事务**（DC §3.2——禁"状态已 OPEN 事件未写"与反向分裂；重启后一致性由事件链重放校验）；**半开探测 lease=spine 持久 lease，带单调 fencing token（终审 PS-9）**：取得/重取 lease 时 token 严格递增；**状态转换必须携带取得时的 token，仅当前 token 持有者可提交**——过期持有者即使恢复也不得更新 breaker（A 停顿→过期→B 以新 token 取得→A 迟到提交=必须拒绝）；lease 取得/续期/过期与状态转换一律 spine 原子条件更新（DC §1.1 L3 围栏族，IG/TM fencing 同构，不创新机制）。跨进程/跨重启唯一性由此成立，"单探测"不再局限于单进程。

## 7. 相邻契约责任互认（O1/O2 生产限制更新）
BL↔PS（§5 表为唯一可证性接口）；TM↔PS（执行在此、V-6 验证在彼——**PS RECOMMENDED/FROZEN 均不推定 V-6 通过**，首审尾注镜像）；MASTER↔PS（A1）；IG/DC 消费不变；**O1 未交付：custom 条目凭据投放=Planned 禁生产**（语义可冻结，能力不可）；**O2 处置：`insecure` 字段删除**——未来确需无 TLS provider 时走**独立风险授权实例**（绑 endpoint+port+凭据投放限制+有效期+TM §8.1 记录），布尔开关不复存在；若治理主体终审改判保留字段，须满足首审§八五条件全束且默认 false。

## 8. Test Strategy
v0.1 全部保留，新增：URL 规范化攻击集（0x7f 十进制/八进制形、[::ffff:127.0.0.1]、zone id、大小写 IDN、尾点、userinfo、非规范形）；**多地址/Happy-Eyeballs/重试再解析负例**（含"合法+127.0.0.1 混发"域名）；pooling 绕过（跨条目 coalescing、撤销后旧连接、版本变更复用）；凭据六纪律各负例（secret_ref 缺失=拒、错条目 token=拒、泄漏面扫描）；breaker（半开单探测竞争、非 provider 类不触发、OPEN 零任务语义）；usage 逐家族矩阵（缺 final/重复/迟到/断流→可审计态，非静默值）；探针引导额度（无 bootstrap grant=拒启动验证；越额度=硬停）；§2.6 边界测试（响应体内 URL 触网=T 层观测断言）。**v0.1.2 新增**：代理旁路负例（继承环境代理=拒；显式代理主体过全链）；覆盖审计工具（构造点枚举+lint）纳入 CI；unverifiable 观测仅入 UNKNOWN 的通道测试+重启后序号/last_used 持久性测试；探针路径 B 损失边界强制（预留/停发/对账三件）负例；breaker lease 跨重启唯一性测试；撤销四平面分解断言（在途不可逆=如实记账而非虚假消歧断言）。**v0.1.4**：B1 min() 误用负例（预算无计费侧强制而参与 min=红）；"completion 有界"表述升格为"总损失有界"的措辞检查（A11 同族违规声明检测）。**v0.1.3 新增（终审）**：重启重复投递验收向量（cursor 有/无两态：有→不重账；无→增量入 UNKNOWN 非 confirmed）；持久序崩溃点测试（原始事件未持久化即崩、位点未推进即崩两例）；探针 B2 披露完备性负例（预留金额以"计费上限"名义出现=红）；breaker **过期 token 迟到提交=必红**+token 单调性断言。

## 9. Acceptance Criteria
A1 与 MASTER D-G5 逐字比对（不变）；**A2（终审二分）**：(i) 攻击向量十六类负例全绿（"测过的没穿"）；(ii) **覆盖证明**——全部 provider 出站构造点静态枚举+lint 禁旁路+运行时连接审计（"没有没测的出口"）；两者齐备方可作"出站无旁路"声明；**A3** 凭据六纪律+header 冻结映射测试；**A4（重写）** BOUNDED 判定仅认 §5 verified_by_probe 六前提+计费口径约束，`config_asserted`/`probe_observed`/`local-capped-unproven-billing` 作 BL 负例必红；**A5** 家族向量逐家族 verified（解析器存在≠口径正确）；A6 breaker 零任务语义+探测 fence（**v0.1.3：含过期 token 迟到提交拒绝**）；A7 三档冻结（V-6/F0a 不因本契约推定）；A8 O1/O2 未决格逐格 Planned 标注；**A9** 探针引导额度=唯一非 verified 特权且绑用途与硬上限（**v0.1.3：B2 五披露缺一即拒启动**）；**A10** P6 纪律抽查（本契约范围）；**A11（v0.1.3）** provability 三分通道逐态测试+⑥持久序两崩溃点+**负能力声明在验收/UI 中不得被弱化**（cursor=none 家族任何"已去重"表述=违规声明）。

## 10. 未决与依赖
O1（凭据后端，Planned 语义已在 §7）；O2 改判通道（§7）；F0b：breaker 阈值窗、per-launch 默认上限、探针引导额度数值、pooling=bound 的启用评审（默认全 off）。

## 11. 裁决处置记录（首审五项，全按建议落定）
PS-D-1 **全拒**（登记即授权）；PS-D-2 **快照+评审门**（新模型=新能力源，运行时发现不自动扩准入）；PS-D-3 **封闭白名单**（§3 冻结映射）；PS-D-4 **fallback 默认关**（开启须兼容集+policy 显式+Authority 记录）；PS-D-5 **复用 live-model 预算线+独立可强制探针额度**（bootstrap grant 解循环依赖，无特权旁路；终审两轮补充裁定已落实：**额度=运行额度+损失边界双组件，损失语义按 §5 A/B1/B2 三分**，B2 仅为受控暴露授权。）

## 13. 修订映射（v0.1→v0.1.4 全沿革）
P0-1→§2 重写（Destination 完整对象+connect 时逐尝试验证 [参考实现] gitegress 模式规范化+TLS 身份+pooling 默认 off/bound 全等证明+撤销即刻失效+**P6 执行位纪律**）；P0-2→§5 证据阶梯表+计费口径约束句+`max_tokens_effective`+探针引导额度；P0-3→§4 身份生成规范规则+逐家族生成表+PS/BL 职责切分句；P1-4→§3 六纪律+header 冻结映射；P1-5→§6 状态机四冻结；P1-6→§7 O1 禁产/O2 字段删除+独立风险实例通道；§8/§9 对应扩容（A2 十六类、A4 重写、A9/A10 新增）。**v0.1.2（终审 PS-1..6）**：§2.7 执行覆盖（统一执行点+代理显式主体化+禁旁路 transport+未覆盖=Planned 禁产+A2 二分）；§5 探针双组件额度+路径 A/B+七绑定（费用旁路封堵）；§4.1 身份算法（origin=local 红线、去重键、version 生成者与持久化边界、unverifiable→仅 UNKNOWN/RECONCILIATION 通道）；§1 生命周期四维表（绝对/idle/时钟保守判定/在途不可逆）；§6 状态×事件同事务+跨重启持久 lease；§3 撤销四平面；P6 限定为 PS 局部纪律（A10 范围=本契约）。**v0.1.3（终审 PS-7/8/9）**：§5 探针损失三分（A 可证硬上界/B1 可验证计费侧机制/B2 `authorized_exposure_budget` 五披露；三条非蕴含入文，§1 在途不可逆自反适用）；§4.1 ⑤provability 三分表+禁 payload_digest 补洞+⑥持久序（序号随首次持久化前进、非随接收）+⑦负能力声明入正文；§6 lease 单调 fencing token+过期持有者迟到提交拒绝；§8/§9 对应向量+A11（cursor=none 家族任何"已去重"表述=违规声明）。**v0.1.4（PS-7 末两处澄清，审方指定内容）**：A 路径两层上界分离（completion 维度界≠总损失界，各计费组成+价格版本+重试全入界才可称总界）；B1 min() 条件化（预算自身有可验证计费强制方可参与，否则 B2 语义）；§8 两负例。
