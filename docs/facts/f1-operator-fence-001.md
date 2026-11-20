# F1-OPERATOR-FENCE-001 · operator 会话围栏影响记录

状态：REGISTERED 事实记录（①立项经所有者 2026-11-19 20:54 发出令采纳，登记=AM v0.1.27）。范围声明：本文件=事实与覆盖范围记录，**不含修复方案、不含 RR 接受、不改任何契约**。

## 1. 机制事实（全部 file:line 直读，主代理 E1）

- 围栏=`authorizeOperatorTransaction`（mc/verbs/operator_requests.go:518-536）：三件套=①请求态提交时刻复验（`State=="active"`）②`authorizeOperatorRequest` 复授权 ③`activity` operator.request 审计行。空上下文第一行放行（对非 operator 写是正确设计，不算缺陷）。
- 载体=`currentOperatorRequest` **进程内变量**（:473），唯一置入者=operator_bridge.go:389/:423/:472 三包装器。
- 生效路径条件（operator_bridge.go:415-417 原文）：`!d.Host && !shouldDelegateToHelper() && request.Identity != nil && slices.Equal(args, request.Command)` → 才进 :423 的 WithOperatorCaller 包裹；**否则 dispatchArgs 无包裹**。

## 2. 不生效面（2026-11-20 修订：委托面经 P1 实验改判；修订标记保留于此）

- ~~委托 fallback~~ **改判为生效**：`__operator-execute`（operator_bridge.go:485-490）与 `__operator-forward`（:386-389）在进入 fallback 之前已置 `activeOperatorRequest` 并经 `WithOperatorRequest` 置 `currentOperatorRequest`；委托 argv 由 `operatorHelperArgs`（:50-55）全等重写为 `__operator-forward <id> -- <args>`，子进程 `runOperatorForward` 先经 `authorize` 收据复验（要求 state='active'）再行包裹。初稿把"verbs 包进程变量不跨界"误读为"围栏不跨界"，漏看了 main 包交接变量这条设计内通道。运行时证据见 §5-P1 执行结果。
- **attachment spine RPC**（mc/cmd/mc/attachment_spine_rpc.go:144-259）：手写 BEGIN IMMEDIATE，不经 inTx → 不跑该函数（此即 A15 V-001 之 F1）。operator 命令的两个入口不经该 broker（broker 服务 attachment publisher/terminal），其围栏义务归 A12/G-F 逐入口面（P3）。
- 结构性结论（修订）：围栏覆盖**宿主进程内全部 operator 写**与**经 `__operator-forward` 交接的委托写**（提交时刻复验在子进程事务内恢复）；不覆盖的是不经该交接的旁路写面（attachment RPC 直写），属 A12 覆盖声明范围。

## 3. 仍存防线（防扩大化声明）

收据生命周期状态机与 CAS、身份/参数全等匹配、release+schema 三处相等（DC-FACTS-001 [CONFIRMED] 列）、spine triggers/CHECK、lease+fence CAS、锁域守卫、RPC 语句白名单与 profile 边界。已知代价限于：批准→提交竞态窗口的时刻复验 + 对应审计痕迹。

## 4. 保留的 UNKNOWN（不以推测填补）

- U1：**已闭合（2026-11-20，P1 执行）**——fast-lane 以 helper 子进程同形（宿主身份 + `MC_SPINE` 直开 + `__operator-forward` 全等代码路径）执行：live 收据→写落+审计行；`uncertain` 收据→提交前拒 "no active execution" 且零副作用；伪造 id→拒。委托形态围栏存活。残余边界：真 docker exec 往返未跑（argv 交接形态已由单测钉死，docker 传输面属 A21 类证明）。
- U2：委托模式下 :416 fallback 覆盖 operator 写的**比例**（是否全部，或仅命令路由不匹配者）——路由表未全量核对（归 A12 P2）。
- U3：dashboard 组件链（MC_OPERATOR_ORIGIN=component/dashboard 路径，operator_bridge.go:190-191）是否构成第三种上下文（归 A12 P2 矩阵）。

## 5. 关闭剧本（V 级验证义务，随①立项进 G-F/A12 测试映射）

P1（针对 U1/委托面）：**已执行（2026-11-20）→ 围栏存活，按本剧本改判分支落定。** 载体=mc/cmd/mc/operator_fence_a22_cli_test.go（live 收据正控+审计行断言、`uncertain` 收据拒绝断言+零副作用断言、伪造 id 拒绝断言）+ operator_fence_a22_test.go（委托 argv 全等交接形态双向断言）。无需修复授权：委托面不存在已证实的不生效，原"修复=新授权事项"分支未被触发。
P2（U2）：枚举 operatorcmd 路由表 ×d.Host×Identity×args 全等，输出矩阵（A12）。
P3：RPC profile 白名单边界用例（含 mutation-无-BEGIN 拒绝已存在路径的正反例）（A12）。
产出=§7.1 记录 + 更新本文件 + G-F/A12 覆盖声明补丁（A12 工单文案的"逐入口"清单须含 :416 fallback 与 RPC 两入口）。

## 6. 处置边界（复述以自缚）

修复（若 P1 证实后需要）=新的实施授权事项；RR 登记=具名 Acceptance Authority 按 TM §8 程序；本记录入库不改变任何契约状态。
