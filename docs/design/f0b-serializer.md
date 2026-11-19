# F0b-1 Canonical Serializer（shz-canon-v1 · 字段表+编码规范+跨实现向量）

- 状态：**REGISTERED 设计件**——本文件的冻结生效路径 = F0b-3 摘要关系批准确认（AM §3 队列步骤 4 三件套之一）+ AM 登记；在此之前本表是**待批文本**，不构成契约变更。DC §3.4 语义层 ①-⑧ 是唯一上位规范，本件只承载**字段表/编码规范/向量**，**禁改语义**（DC §3.4 原文边界）。
- 配套：`vectors/` 目录（`vectors.json` 30 例，套件摘要 `1a582fba5ce4b25db7894b8613692010d32f270f9b90a5382bbfa44b6526a5dc`；`reference_encoder.py` 为向量推导工具——**非生产代码**，重跑必须逐字节复现 vectors.json）。
- 消费者：B04（根算法+algorithm_id）、B07（链实现）、C03（usage 持久序）、验证器 `shz verify-chain`。生产实现按 vectors.json 做符合性测试（A5 教训：解析器存在≠口径正确）。

## 1. 摘要构造（复述 DC §3.4 已冻关系，不新设规则）

- `event_digest_i = SHA-256(CanonicalEvent_i)`；H 冻结为 **SHA-256（无参数、无截断）**。
- `chain_digest_i = SHA-256(chain_digest_{i-1}_bytes ‖ event_digest_i_bytes)`（原始 32 字节拼接）。
- `prev_hash_i := chain_digest_{i-1}`，存于事件行**且计入哈希域**（DC：不得排除；验证器双关系同核）。
- `GENESIS(aggregate_type) = SHA-256(UTF8("shz-genesis-v1:" ‖ NFC(aggregate_type)))`——域分隔常量按此推导，非任意字面量；首事件即存在证明（§3.5 协同）。
- 根算法（验证器整体根、`EMPTY_ROOT`）属 **B04**，不在本件冻结范围。

## 2. 规范化编码层（①-⑦ 的可执行形态）

| DC 语义 | 本件编码规则 |
|---|---|
| ① 缺失≠null 两态 | 每字段槽首字节 presence 标签：`0x00`=ABSENT（键不存在）、`0x01`=NULL、`0x02`=VALUE+载荷。两态均入摘要（V2a/b 钉死） |
| ② 数值精确十进制字符串，禁整浮互化 | decimal 字段载荷=存储的**词法原文字节**，文法 `^-?(0\|[1-9][0-9]*)(\.[0-9]+)?\$`；不匹配=NOT_CONFIRMED 错误（V7a-d）。`1`/`1.0`/`1.00` 三者摘要互异（V1a-c）；整数域（seq/版本/计数）文法无小数点。写入方负责存前规范（禁 `+`、前导零、指数、尾点）——序列化器**零算术变换** |
| ③ 时间定宽 | 本 serializer_id 冻结选项一：`RFC3339 UTC 定宽微秒` `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{6}Z\$`；非形（含无微秒段、非 Z、纳秒>6 位）=错误（V7e）。epoch-nano 被本 id 排除，禁混用 |
| ④ 字符串 NFC+转义规则 | 载荷=NFC(UTF-8) 原始字节；长度前缀分帧 ⇒ **无转义层**（转义规则=不存在，随版本表冻结）。V3a/b：预组合与分解序列 NFC 后同摘要 |
| ⑤ 对象键按声明序 | 位置化编码（按下表声明序），JSON/映射的书写序无关（V4a=b）；声明序≠字典序由 V4c 钉死字节；数组 `u64 计数+原序元素`，元素可 NULL 不可 ABSENT |
| ⑥ 未知字段=错误 | 输入含未声明字段（信封或载荷）→ `NOT_CONFIRMED` 上抛，不得跳过（V6a/b） |
| ⑦ 版本不可变 | `serializer_id` 是**哈希域首字段**（V5a≠V5b）；历史事件按其记录 id 复算；id 一经 AM 登记语义永久，新语义=v2，禁原地改 |

标量类型：`str`（NFC+u64 长度）、`dec`/`dec_int`（词法文法+u64 长度）、`time`（20 字符定宽）、`hex32`（64 小写十六进制字符，长度分帧同上）、`enum:a|b|...`（封闭词表字符串，表外=错误）、`obj:<schema>`、`arr:<type>`。所有 u64 大端。必需字段禁止显式 NULL（缺=错误，null=错误——两态只属于可选字段）。

## 3. 信封字段表（CanonicalEvent，声明序即哈希序）

| # | 字段 | 类型 | 可选 | 语义锚 |
|---|---|---|---|---|
| 1 | serializer_id | enum{shz-canon-v1, shz-canon-v0(仅历史复算)} | 否 | DC §3.4⑦ |
| 2 | event_id | hex32 | 否 | DC §3.1② 去重身份 |
| 3 | aggregate_id | str | 否 | DC §3.1 |
| 4 | aggregate_type | str | 否 | genesis 域盐输入（§3.4） |
| 5 | seq | dec_int | 否 | DC §3.1③ 链排序键 |
| 6 | event_type | str | 否 | §3.3 关系语义 |
| 7 | occurred_at | time | 否 | 双列之一（§3.3） |
| 8 | recorded_at | time | 否 | 双列之二——两列不得混用 |
| 9 | effective_at | time | 是 | §3.3 admission 业务时点 |
| 10 | supersedes_event_id | hex32 | 是 | §3.3 决策修订链（禁与 TM §7.1 字段互抄） |
| 11 | causation_id | str | 是 | §3.1 边界：仅因果关联，非顺序证明 |
| 12 | correlation_id | str | 是 | 同上 |
| 13 | prev_hash | hex32 | 否 | §3.4 冗余绑定，必入哈希域 |
| 14 | payload | obj:family | 否 | 载荷字段表按族注册 |

## 4. 载荷字段表（v1 注册集）

**已注册（有向量）**：`usage_observation`（PS §4.1 身份五要件+双维报告型+provability 三分+族名——声明序 output 先于 input，**故意非字典序**，V4c 同型钉死）、`usage_settle`（BL §4 来源身份四元组+payload_digest+supersedes 链+七效枚举含 reconciliation/written_off）、`admission`（§3.3：grant_id+reason+expires_at；去重键=grant_id×event_type 在写入侧）。完整声明序见 `reference_encoder.py` SCHEMAS——代码与本节同义，冲突以本文件为准并报缺陷。

**declared-pending（无字段表=无向量，禁标 verified）**：lifecycle、transition、lease grant/renew/revoke、migration、aggregate_open、activity、signoffs[P4 后]。补齐路径：从 DC §4 原语与写入面提取字段表 → 追加向量（新 serializer 版本表内**只增不改**属 v1 附录，改动既有表=v2）。AM §4b 五类最小集已由通用信封向量覆盖（V1-V5 不依赖具体族）——**⑧ 条"摘要算法未定形"状态就此解除的前提是 F0b-3 批准本件**，批准前一切链摘要仍视为未定形（PS A5/DC A6 同律）。

## 5. 错误与 UNKNOWN 语义（fail-closed，A20-EX 同源）

一切文法/词表/未知字段违规 = `NOT_CONFIRMED` 错误上抛（⑥）；跨实现复算中**输入不完整或形态不定 = UNKNOWN，不得由任何验收条款静默消费**（TM §7.1 P1-2 同律）。测试向量的 `expect_error` 字段是断言目标，不是可选提示。

## 6. 验收映射（本工单）

AM §4b 五类=V1-V5；PS 家族扩展=F-*（openai-chat/anthropic×3/google/openai-responses/unknown/local-origin）+S/A/G；grammar 守卫 V7；共 **30 例，套内关系断言全过、双跑逐字节确定**。本件 EVIDENCED=纸面登记完成；其**义务**（序列化契约冻结生效）待 F0b-3 批准与 AM 登记（治理动作，非本工单）。

## 7. 已知边界与风险（不静默）

- 事件内 payload_digest 字段（settle 表）承载的是**结算报告源数据**摘要，与链上 event_digest 是两个域——命名近似是历史事实（BL §4 原文），实现方不得合并两者。
- `shz-canon-v0` 仅为 V5 的"历史 id 复算"提供可测载体，**不代表存在 v0 部署**；若 F0b-3 决定删除该枚举项，vectors V5b 随之作废重制。
- 时间定宽取微秒：晚于 v1 的写入源若持纳秒数据，须在**存储层**先规范（本件禁 >6 位小数即为此设计）。
