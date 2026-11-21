# A16 diff 计划登记 #3（附三增补：chore 宿主与看门狗落点；含阶段③追认）

## 追认（已发生的越名文件）
阶段③（b5d152b run 树字节预算）把 `du` 依赖接在 **`resident/src/main.ts`** 的 chore 装配处。001/002 未列 main.ts；本条如实追认：chore 的宿主就是 main.ts（run-log-prune 既有装配点），改动限于给该装配传参（纯接线，无新权威）。**后续任何 main.ts 改动均以本条为正清单依据。**

## 看门狗（D-1 裁定 c 的落地件）
TM:99 语义="写失败且 run 以资源类终态终止"；b+c 组合下工作区维由看门狗兑现（tmpfs 维真 ENOSPC 已在阶段②）。**终止权威不搬**：dispatch 的容器缺席 reap（ReapContainerGone）与租约/截止阈值仍是唯一终态机器；看门狗只做宿主侧观测+对超软限容器执行 `docker stop`，并以 `sweep.resource` 留痕资源类因由（run id、软限、实测字节）。**不新增 ReapReason 词**——新状态词=dispatch wire 形状决定，越出本票正清单，若要 spine 内可查的资源类专因须另呈所有者（随 §7.1 呈报项）。

增补文件族：
- `resident/src/environment-lane.ts`：`createWorkspaceWatchdogChore`（与限额读取同族；复用 agentContainerLimits/容器清单惯式；永不抛出，与 prune 同款护栏：测量失败只跳过、不终止）。
- `resident/src/main.ts`：chore 装配一行（接线，承上追认口径）。
- `resident/src/environment-lane.test.ts`：看门狗用例（t0 禁用；t1/t2 超软限→stop+留痕；等于软限不动作；测量失败不动作；setup+pipeline 两标签都在作用域、collie 只读不在）。

作用域依据：写工作区的只有 pipeline（/workspace/source rw）、setup（build 输入）与环境操作容器；collie 全部挂载为 :ro（effects.ts 装配可见），不入看门狗。du 失败/超时目录：保留并 warn（与 prune 的 fence 同律）。

## 追加（守卫驱动，实现前登记）
`runner/shared/log.ts` 的 **EVENTS 注册表一行**（"sweep.resource" 加入 sweep 组）：事件登记表守卫（TestEveryEmittedEventIsInTheRegistry）要求每个 emit 事件先入册——TM §10-A1 三元组语义下这正是"新留痕须有主"的正途，非绕行。渲染器与 golden 夹具零改动（mclog 无事件名单镜像，注册表为 TS 单侧）。
