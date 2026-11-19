# F1-OPERATOR-FENCE-001 · operator 会话围栏影响记录

状态：REGISTERED 事实记录（①立项经所有者 2026-11-19 20:54 发出令采纳，登记=AM v0.1.27）。范围声明：本文件=事实与覆盖范围记录，**不含修复方案、不含 RR 接受、不改任何契约**。

## 1. 机制事实（全部 file:line 直读，主代理 E1）

- 围栏=`authorizeOperatorTransaction`（mc/verbs/operator_requests.go:518-536）：三件套=①请求态提交时刻复验（`State=="active"`）②`authorizeOperatorRequest` 复授权 ③`activity` operator.request 审计行。空上下文第一行放行（对非 operator 写是正确设计，不算缺陷）。
- 载体=`currentOperatorRequest` **进程内变量**（:473），唯一置入者=operator_bridge.go:389/:423/:472 三包装器。
- 生效路径条件（operator_bridge.go:415-417 原文）：`!d.Host && !shouldDelegateToHelper() && request.Identity != nil && slices.Equal(args, request.Command)` → 才进 :423 的 WithOperatorCaller 包裹；**否则 dispatchArgs 无包裹**。

## 2. 不生效面（已证实部分）

- **委托 fallback**：`shouldDelegateToHelper()` 为真（Linux 生产目标形态）时，operator 批准的写命令走 :416-417 无包裹分支；写入在 helper 子进程执行，子进程变量恒空 → 三件套在提交时刻不生效。
- **attachment spine RPC**（mc/cmd/mc/attachment_spine_rpc.go:144-259）：手写 BEGIN IMMEDIATE，不经 inTx → 不跑该函数（此即 A15 V-001 之 F1，现已归并为下列结构性事实的实例）。
- 结构性结论（取代 A15 初稿口径）：**围栏是"宿主进程内、逐命令全等匹配"的窄面机制**，任何跨进程边界（委托/子 RPC）的 operator 写天然在其覆盖之外。

## 3. 仍存防线（防扩大化声明）

收据生命周期状态机与 CAS、身份/参数全等匹配、release+schema 三处相等（DC-FACTS-001 [CONFIRMED] 列）、spine triggers/CHECK、lease+fence CAS、锁域守卫、RPC 语句白名单与 profile 边界。已知代价限于：批准→提交竞态窗口的时刻复验 + 对应审计痕迹。

## 4. 保留的 UNKNOWN（不以推测填补）

- U1：生产宿主 withSpine 经代理驱动开事务时（变量在宿主、SQL 在 helper），:423 包裹路径的围栏是否实际存活——未追死。
- U2：委托模式下 :416 fallback 覆盖 operator 写的**比例**（是否全部，或仅命令路由不匹配者）——路由表未全量核对。
- U3：dashboard 组件链（MC_OPERATOR_ORIGIN=component/dashboard 路径，operator_bridge.go:190-191）是否构成第三种上下文。

## 5. 关闭剧本（V 级验证义务，随①立项进 G-F/A12 测试映射）

P1（针对 U1/委托面）：委托模式启用下——submit→decide 批准命令 C→**在 C 执行前**使请求失效（expire/revoke 路径）→执行 C。断言：若见 "The operator request has no active execution." → 围栏在该形态存活（结论改判）；若命令成功 → U1 证实为不生效。
P2（U2）：枚举 operatorcmd 路由表 ×d.Host×Identity×args 全等，输出矩阵。
P3：RPC profile 白名单边界用例（含 mutation-无-BEGIN 拒绝已存在路径的正反例）。
产出=§7.1 记录 + 更新本文件 + G-F/A12 覆盖声明补丁（A12 工单文案的"逐入口"清单须含 :416 fallback 与 RPC 两入口）。

## 6. 处置边界（复述以自缚）

修复（若 P1 证实后需要）=新的实施授权事项；RR 登记=具名 Acceptance Authority 按 TM §8 程序；本记录入库不改变任何契约状态。
