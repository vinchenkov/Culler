# L02 diff-plan #1（实施轮：升级排空闸=stop 相流程内步骤——复用既有维护门/ack/Idle/容器核验，零新相位词零 wire）

状态=CODED 中（report 文案对抗审查在途=AGENTS 义务，其结论回填 §5）。授权=轮十六 D6（T-2=**10 分钟**+到期强制中断并登记之批准行为形）。
锚：`docs/design/l02-drain-handover-001.md`（D-B 案+§5 验收映射+轮九 Q6 各 T 裁）；残余继承句见本票行 F1。

## 1. 改动（4 文件，全在 update 编排层）
- `mc/cmd/mc/setup_published_prod.go`：publishedSetup +2 字段（`drainWindow`、`drainIdle` 采样缝；**零值=关**⇒restore 路径与既有全部调用点行为逐字不变）。
- `mc/cmd/mc/setup_update_stop_prod.go`：`updateDrainWindow=10*time.Minute` 常数 + `waitForDrain`（循环：`workQuiesced`→成=报告"无中断"继续 stop；窗闭=报告"按批准继续中断"继续 stop——**到期永不中止更新**，设计 D-B 语义）+ `workQuiesced`（=既有 `p.database(ctx,false).Idle()` + `requireIdleContainers`——A28 已核的活读门+Collie 容器核验，**零新状态词**）。插入位=maintenance 请求 rename 之后、`StopUpdateWork` 之前（resident 先见门停 spawn，再等 run 自然终态=设计时序）。
- `mc/cmd/mc/setup_update_transaction_prod.go`：主更新流 stop 相前 `p.drainWindow = updateDrainWindow`（**唯一启用点**；恢复/维修路径不设）。
- `mc/cmd/mc/setup_update_stop_prod_test.go`：新测 ×3——①常数机械钉（10min=D6 批准值，改动必先经所有者）②自然终态序（busy,busy,idle→stop→reconcile；断言形态=前缀序+stop 后无 drain 采样，robust 于 stub 轮询频度）③窗闭回退（持续 busy+推进钟→仍 stop+reconcile+到期报告语——**中断路径不回归含 __update-reconcile 记账**）。既有测**零改动**（零值守卫兑现）。

## 2. 双残余承接（设计在册两句的执行认定）
- **site-3（A27 让渡）与 A28 §0b（:499/:813 维持活读之让渡）**：两处代码**保持活读不动**；其"窗口"语义由排空闸收缩——闸内 spine+容器已静默才进 stop/checkpoint，update 路径上的活读不再发生于"容器正写"瞬窗（非闸路径如 stopAndRecheck 维持原论证）。票行以指针句登记此承接；两处原句不改写（append-only）。
- D-C（计划内重启计费豁免）=轮九 T-3 缓行**维持**，本票零触 `domain/execution.go`/Decide 输入。

## 3. 形状偏离申报（如实）
- 设计 D-B 原句"期限参数=配置面"——本实施用**编译期常数+机械钉测**，未开 config 字段。理由：批准值是安全/运维参数（所有者裁定的数）而非操作者偏好；配置面反把参数暴露为可误配。若波门判必须可配，增 config 字段=后续加法（本票 F1 不动其余面）。**此为对批准案文的收缩执行，主动呈报。**
- "等待=流程内步骤"（T-4 裁文）逐字兑现：journal 九相位词表零触碰；`install-maintenance.json`/ack/`ready` 字段全为既有件。

## 4. 证据
- cmd/mc 焦点组 `TEST_EXIT=0`（新 3+既有 stop/restore 全绿）。
- 待跑（合并前全道硬规则）：`mc/check.sh` 全道 + `update_docker`（本票核心域，含既有一致性用例族回归）+ `docker_boundary` + 扫描门。
- e2e 道内"活跃 run 真自然终态"端到端=既有 walking-skeleton 族不覆盖 update 场景——**验收映射①的真实半=update_docker 内扩一用例（真 spine 活跃行+短窗不可注入⇒改以"窗闭回退"实证+快道双分支覆盖）**；此差距如实挂票行 UNVERIFIED 样（若波门要求真端到端排空用例，另立实施项）。

## 5. report 文案（对抗审查毕=三行 ACCEPT）
首审**拦下真缺陷**： 入口的 30s  包死 10 分钟窗⇒到期报告语不可达、静默到点（AGENTS 义务的实证价值）。修复：预算移至 drain 之后（stop 相自 StopUpdateWork 起算，其内另有 30s——结构同旧、drain 移出；复审亲验调用链 30min 包裹>窗、可达性成立）。三行定稿=L1 "Waiting up to %d minutes…"（**数字自含于常数**）/L2 "Active work finished before the stop."（时态scoped，复审确认与无条件 reconcile 步线不矛盾）/L3 "Active work remains. Interrupted runs can require a retry."（呼应既有面板句）。复审二核：80/40 列有/无色全 ≤2 行、无 verb-map 叠词、无"跑 mc"句式——**三行 ACCEPT 入册**。非阻塞注两则登记：(i) 分数窗注入会印"up to 0 minutes"——生产常数钉 10 无现实路径，若日后可配再加 max(1,…)；(ii) stop+reconcile 共享 post-drain 30s=与旧入口包同形非回退，stop 逼近 30s 时另视。断言同步重跑 +gofmt 0 差。
