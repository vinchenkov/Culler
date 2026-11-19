# ADR：BlocksWork 定位为入口层阻断（G-C C-i）

- 状态：**PROPOSED**——本 ADR 是 A17 的 C-i 交付件，批准权在部署所有者；批准前 DC §5 G-C 的相应义务不闭合，任何文档不得按"已定"引用。
- 背景：更新/维护窗口必须阻断工作。b22df40 现实（全部 [CONFIRMED]，DC-FACTS-001 + A15 盘点）：`BlocksWork`（journal/update 态）在**入口层**执行——tick 跳过（dispatch 前置）、spawn 拒绝、setup/services 命令（setup_published_prod.go:189/231/367、setup_install_prod.go:111）、operator 闸（operator_guard.go:26）、onboard/服务面（onboard_home_prod.go:52、culler_services_prod.go:109/231）。数据库层**没有**"更新中拒写"门禁：能直达 spine 的写（直接 exec helper、attachment 手写事务 RPC）不受 BlocksWork 约束。

## 决策（提议）

BlocksWork 定位为**入口层阻断**：它约束"平台自己发起的工作"（派发、启动、UI/operator 命令），不承担、也不得宣称承担数据库写事务级的排他。写事务级的正确性归属：单写者语义（K4/WAL+BEGIN IMMEDIATE）、schema 门禁（G-A，A10 已挂 OpenSpine）、构建身份（G-F/A12，独立义务）、lease/fence（K3/N16）。更新窗口内直达写的残余风险**不由本 ADR 吸收**，走 C-ii RR 材料（a17-rr-c-ii-material.md），接受权仅 Acceptance Authority。

## 备选与拒绝理由

- **DB 级写锁（更新期 spine 拒写）**：需要跨进程协调原语（新互斥域，表二 3 登记+全套验证）；更新窗口本身已按阶段停摆派发，再加 DB 锁把入口层语义重复进第二权威面——违反"不引入平行机制"。拒绝。
- **把 BlocksWork 塞进 inTx**：入口标志进事务层=两个信任层耦合，且对不经 inTx 的旁路（RPC 手写事务）依然无效——成本高、覆盖假象。拒绝。

## 后果（本 ADR 若批准）

1. "更新期间无写入"这类声明在任何文档/UI 不得出现；允许出现的表述是"更新期间平台不再发起新工作；已准入与直达写的处置见 RR"。
2. G-A（A10，schema 面）与 G-F/A12（构建身份面）的覆盖矩阵须把"更新窗口直达写"作为交叉场景列入测试（A12 范围声明引用本 ADR）。
3. C-ii 残余在 RR 册闭合前，更新窗口的完整排除声明保持 UNVERIFIED——与本 ADR 批准与否无关的既有事实。
