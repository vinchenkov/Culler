# G-A COVERAGE MATRIX（A10 交付 · 写入口 × live-schema 门禁）

方法：DC-FACTS-001 同型（控制流级，file:line 亲读；b22df40+fork 改动态）。门禁位置：`verbs.OpenSpine`（mc/verbs/init.go，G-A 注释含 R-01 范围声明）。判定口径：GATED=开库时执行 `substrate.CheckSchema`（比对内嵌 schema 对象 vs 真实 sqlite_master + meta.schema_id==CurrentSchemaID + CHECK 形状断言），不过则拒绝且**零语句执行**。

| 入口族 | 路由证据 | 门禁状态 |
|---|---|---|
| 普通写 verb（~90 处 withSpine） | cmd/mc/main.go:1228-1235 → verbs.OpenSpine | **GATED**（本单） |
| `__system-state` 写动作（ProviderAccessObserve/ConnectionVerified/lifetimes 等） | systemstatus.go:382+ 各 case → withSpine | **GATED**（同上路由） |
| attachment spine RPC 服务器 | attachment_spine_rpc.go:98 → verbs.OpenSpine | **GATED**（进程生命周期开一次；长寿服务的中途 schema 变更由部署停摆机制约束——update 窗口 K4/停摆事实，A15 在案） |
| onboard sections / project onboard 直写 | onboard.go:1342/1690/1766 substrate.Open 直连 | **UNCOVERED** → **G-E 保持挂起**（不预宣布闭合） |
| reset（dev-reset 写面） | 同上直连族 | **UNCOVERED** → **G-D 保持挂起** |
| spine 创建/替换流 | onboard_spine.go:251（自带 inspect+CheckSchema 于流程内）、init.go:56/169 | 不适用（先于 schema 存在；其自身检查在位） |
| doctor/update-reconcile/backup 直开 | ops.go:200/231/499/540/608、update_reconcile.go:31 | **有意不门**（诊断/恢复路径必须能在 mismatch 态工作；ops:507 已显式调用 CheckSchema 作诊断而非拦截） |

**边界声明（R-01，贴码同步）**：以上 GATED 仅证 schema 匹配；构建身份（调用方=执行方同源）不在此证明范围——G-F/A12 独立义务，且其范围声明须并入 A15-F1 发现的 RPC 手写事务面。
**合并规则兑付**：G-D/G-E 未被本咽喉覆盖=矩阵如实呈现；A11 依本表决定并实施或继续挂起。
