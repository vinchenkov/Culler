# F0b-3 评审材料 · B03（shz-canon-v1 + 30 向量）— 一页版

供设计审查；**非批准文书**。正式 F0b-3 批准=具名 Architecture Approval Authority 签署+AM 登记（权限现状：该身份未指定，见决策卡 Q7）。绑定对象：docs/design/f0b-serializer.md + docs/design/vectors/{vectors.json, reference_encoder.py}@dev tip fa8c07b；套件摘要 `1a582fba5ce4b25db7894b8613692010d32f270f9b90a5382bbfa44b6526a5dc`（vectors.json 内容字节摘要，重跑可复现）。

## 1. 字段定义与类型约束

信封 14 字段声明序（哈希序）+载荷 4 族已注册表（usage_observation 12 字段/usage_settle 7/admission 3/probe_order 2），全在 f0b-serializer.md §3-4 与 reference_encoder.py ENVELOPE/SCHEMAS——**两处同源，代码为表的可执行形态**。类型系统六标量：str(NFC)/dec(文法 `^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)/dec_int/time(RFC3339 定宽微秒 Z)/hex32/enum(封闭词表)+obj/arr 复合。必需字段禁显式 NULL；可选字段三态 presence 标签（0x00/01/02）。

## 2. 编码规则与摘要参数

presence-tagged 长度分帧位置化编码（无转义层——规则=不存在，随 id 冻结）；u64 大端计数。`event_digest=SHA-256(canonical_bytes)`；`chain_digest_i=SHA-256(prev32‖event32)`；`GENESIS(t)=SHA-256("shz-genesis-v1:"+NFC(t))`；prev_hash 必入哈希域（DC §3.4 冗余绑定条款）。H 参数：SHA-256 无截断无加盐。根算法/EMPTY_ROOT 明确不在此件（B04 范围）。

## 3. 30 向量覆盖矩阵

| DC §3.4 语义 | 向量 | 断言关系 |
|---|---|---|
| ①缺失≠null | V2a/b（信封层）+F-anthropic-interim/-absent（载荷层） | 摘要互异 |
| ②数值词法存活 | V1a/b/c（1/1.0/1.00）+V7a-d 文法守卫 | 三摘要两两互异；非法形必错 |
| ③时间定宽 | V7e | 非微秒定宽=错误 |
| ④NFC | V3a/b（U+00E9 vs e+U+0301，chr() 构造防编辑器归一化） | 输入原文异、摘要同 |
| ⑤声明序 | V4a/b（书写序无关）+V4c（字节钉死非字典序）+载荷表 output先于input 的设计性反序 | a=b 且 c 的 hex 可断言 |
| ⑥未知字段 | V6a/b | 必 NOT_CONFIRMED 错误 |
| ⑦版本不可变 | V5a/b（v0/v1 同逻辑事件） | 摘要互异+复算规则文 |
| ⑧向量前置 | 本套件全体 | 五类最小集齐备 |
| PS §4 家族 | F-openai-chat-final/anthropic×3/google-partial/responses-supersedes/unknown-family/local-origin（7 例） | 各族收敛规则的字节形态 |
| BL §4 身份 | S-settle-confirmed | 四元组+digest+supersedes |
| DC §3.3 | A-admission-expiry | effective/recorded 双列不混用 |
| genesis 域分隔 | G-genesis-run | 类型盐可复算 |

## 4. 已知缺陷史与修正证据（您点名第 4 项）

生成期发现：**ENVELOPE/SCHEMAS 第三列语义倒置**（必需⇄可选）——若逃逸=必需字段缺失被静默 ABSENT、required 字段可被 NULL 伪装。被 V1 套内关系断言当场抓住（三摘要相等→断言失败）；修正=统一"第三列=OPTIONAL"语义+新增 required-NULL 拒绝+encode_event 同规则镜像；修正后全套断言重过（gotest 7/7、V1 三元互异恢复）。该缺陷**由向量的存在而暴露**，作为套件有效性的反向证明记录在案（B03 §7.1 actual_result 原文）。

## 5. 确定性与绑定

三次重跑（B03 worktree×2 + dev tip×1）逐字节同摘要 `1a582fba…`；提交链 a287c21→e2ca459→fa8c07b 期间 vectors.json 未漂移。

## 6. declared-pending（禁读成 verified）

lifecycle/transition/lease/migration/aggregate_open/activity/signoffs 七族：无字段表、无向量、状态=declared；补齐通道=serializer v1 附录或 v2（B03 §4 原文）。跨实现符合性=第二实现（B07/C03）对 vectors.json 跑通时才成立——**本材料证明的是"定义确定"，不是"已被多实现同意"**（AM §4b 语义）。

## 7. 与 DC §3.4 / PS §4 逐项对应

§3.4 ①-⑧ 逐条映射见本文件第 3 节表头列；PS §4.1 ①-⑦（身份序/去重键/版本/④local 红线/provability 三分/持久序/负能力）→ usage_observation 字段+ F 系向量逐例注记；BL §4 四元组/report_kind 正交两维→S 向量+枚举编码。**未发现与已冻语义的偏离项**；两处**主动收紧**（超出 DC 文字但不与其冲突）：时间纳秒拒绝、required-NULL 拒绝——请审查者特别过目这两格是否可接受。
