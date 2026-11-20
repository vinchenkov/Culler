# F0b-2 根算法（shz-merkle-v1 · algorithm_id 冻结件）

- 状态：**REGISTERED 设计件**——批准路径=F0b 三件套批准确认+AM 登记（本件批准即 algorithm_id 生效）；上位规范=DC §3.4 验证器输出条（输入集合/排序规则/空集语义已冻，本件只冻结"实际算法"，禁改语义）。
- 配套：`vectors/root_vectors.json`（11 例，套件摘要 `9d1c3e9ccd1304f0d55da47857185fe775511d59ee3d6a303a490e87a582f0a5`）+ `reference_root_encoder.py`（推导器，非生产件，重跑必须逐字节复现）。

## 裁决：Merkle（非纯 fold）

DC 允二选一；选 Merkle 的决定性理由=D07/IG 锚定线需要 **per-aggregate 包含证明**（锚一个根、离线验证任意聚合根链头）——有序 fold 给不出一元证明。代价=规则面多三条（节点前缀/奇层复制/空根），全部由向量钉死，跨实现歧义面与 fold 相当。

## 冻结规则（与向量一一对应）

1. **叶**：`leaf = SHA-256(0x00 ‖ u64be(len(idNFC)) ‖ idNFC ‖ u64be(seq_max) ‖ chain_digest32)`——长度前缀消解拼接歧义；chain_digest 取 F0b-1 的 32 字节原值。
2. **序**：NFC(aggregate_id) 的 **UTF-8 原始字节**字典序升序（R6：`Z`(0x5A) 先于 `a`(0x61)——禁大小写折叠/禁 locale 比较；R6b：乱序输入同根）。
3. **奇层**：复制末节点上行（RFC-6962 §2.1，R4 钉死）——拒绝"carry-up"变体，因其与 Merkle 包含证明的索引约定不兼容。
4. **内部节点**：`node = SHA-256(0x01 ‖ left ‖ right)`——与叶前缀域分离（防第二原像类混淆）。
5. **空集**：`EMPTY_ROOT = SHA-256(UTF8("shz-root-empty-v1"))`（域常量按推导非字面魔数；R1）。
6. **输入卫生**：重复 aggregate_id=畸形（R7）；chain_digest 非 32 字节=畸形（R8）——均显式错误，不得静默去重/截断（A20-EX 同源纪律）。
7. **历史**：根输出必须携带 `algorithm_id`；规则任何变化=新 id（v2），历史根按旧 id 解释，禁止静默重释（DC 原文义务）；IG/离线验证器消费前该字段不得留空。
8. **验证器输出形状**：`{algorithm_id, leaf_count, root_hex}` + per-aggregate 链头集文件（evidence_digest=该文件摘要，绑定 §7.1 记录——DC 协议形状原文）。

## 验收映射

B07（链实现）与 `shz verify-chain` 必须对 root_vectors.json 跑通全部 11 例（含错误例的显式拒绝路径）；包含证明生成/验证的向量属 D07/IG 工作包，本件冻结其依赖的树形与哈希域。

## 已知边界（不静默）

单叶树 root=叶本身（R2，无额外包裹）——包含证明库若假设"根永远是 node"会在此分歧，已用向量钉死；深度上，2^k 级至千级聚合根线性无瓶颈（fold 亦同）。
