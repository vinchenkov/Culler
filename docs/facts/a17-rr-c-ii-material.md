# RR 材料包：更新期间直达 helper 写入（G-C C-ii）

- 性质：**候选材料准备**（A17 交付）。**非风险接受、非签署、非提交**——接受通道=Acceptance Authority 按 TM §8 七字段逐条处置；Authority 尚未指定（AM §3 队列步骤 1），本材料先备料不催签。
- 事实底座（全部有 file:line，DC-FACTS-001/A15 盘点）：更新机制在入口层阻断（ADR a17-blockswork-boundary）；下列路径不受 BlocksWork 约束，可在 update journal 活跃窗口内写 spine。

## 暴露面清单（按可利用性排序）

| # | 路径 | 证据 | 现状防线 | 缺口 |
|---|---|---|---|---|
| 1 | attachment spine RPC 手写事务 | attachment_spine_rpc.go:144-259；broker epoch-SH 跨容器持开事务（A15 N18/D31） | 语句白名单+profile 边界+wall-clock 兜底+K4 串行 | 不经 inTx ⇒ 无 authorizeOperatorTransaction（A15-F1）；无构建身份（G-F/A12 域）；更新窗口内可直达写 |
| 2 | 直接 docker exec helper `mc` | A15 表二 4 分析；runtimeauth_verifier argv 构造 | 容器边界（spine 仅 helper 可开）、锁域守卫 | 能进容器=能写；宿主失陷面归 RR-1/V-5 分层（TM §7.2 V-7 类③纪律），不重复计 |
| 3 | 与更新并行的正常 verb 写（窗口竞态） | update journal 与 dispatch 停摆握手未端到端证死（A15 G3） | 阶段序+tick 单飞 | 竞态窗口未闭合=UNKNOWN（不写成已排除） |

## 建议的接受单元拆分（供 Authority 逐条处置，禁止打包）

- **RR-candidate-1**：路径 2——限定形态"单宿主、matched-pair 部署、容器逃逸本身已由 RR-1 体系处置"下的直达写残余。暴露面/操作范围/有效期/复审触发三要素草案在提交接受时由 Authority 定稿，本材料不代填。
- **RR-candidate-2**：路径 1——**不建议走接受**：缺口有明确低成本闭合（G-F/A12 覆盖 + A15-F1 钩住 operator 复验），先实施后评估残余，避免"接受一个本可修的洞"。
- **RR-candidate-3**：路径 3——**当前不可接受也不可谓已排除**：竞态未证死=UNKNOWN；处置前提是 A15 G3 端到端追认或 B06 压测证据。

## 与既有 RR 册关系
不并条：RR-1（失陷面）/RR-3（egress）语义不同；路径 2 若接受应作独立条并互引。

## 边界声明（本材料自我约束）
本文任何内容不构成风险接受、不预设 Authority 结论、不改动 DC/TM 文本；A17 工单关闭≠C-ii 闭合（AM §4b 映射纪律原样适用：接受必须落 TM §8 具体行+三要素+具名签署）。
