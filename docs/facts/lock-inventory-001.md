# LOCK-INVENTORY-001（A15 交付 · 存量互斥域全量盘点）

- 对象：shz/dev@e2ca459（代码=上游 b22df40 未迁移态）。方法：DC §1.3 表二 4 的命令——**人工跨层持锁路径检查，非 grep 锁 API**。执行：主代理委派 Explore 子代理全量枚举（93 次读检，file:line 证据）+ 主代理对最高风险 4 域的引用行**原文抽查复核**（[SP] 标记）。
- 分级：**E1-sp**=主代理亲读原文证实；**E1-ag**=子代理读码定位（证据行存在、语义为其报告口径，未逐行亲审——Integrity Reviewer 可按行复核）；**E2**=推断。**诚实边界（表二 5）**：本盘点闭合"F0a 反例扫描"任务面；规则 1-3 由候选转强制是**治理采纳动作**（AM/所有者），本文件不自行升格；"已验证全局锁序"仍不得宣称——残留缺口逐项列于 §4。
- 上位锚：DC §1.3 表一 K1-K4、表二 1-5；DC §1.2 不变量（持锁不做宿主 I/O）。

## 1. 域登记表（表二 3 关系声明义务一并履行）

**K1-K4 对照**：K1=dispatch 轮次 flock、K2=onboard flock、K3=admission 桶认领行、K4=WAL 写序列化。新域按表二 3 声明与四者关系（包裹/被包裹/无关/平行）。

| 域 | 机制 | 位置（file:line） | 与 K1-K4 关系 | 关键持锁窗口 | 级别 |
|---|---|---|---|---|---|
| K1 dispatch 轮次 | flock `mc.dispatch.lock`(BLOCKING) **包** BEGIN IMMEDIATE | verbs/dispatchverb.go:199-209,326-360 | **=K1**；flock→inTx 序 [SP] | prepare/commit 两段各自取放；attest（宿主文件读）在两段**之外**；容器启动在 mc 返回后由 TS 执行——flock 内无子进程/网络 | E1-sp |
| K2 onboard | flock `<spine>.onboard.lock`(BLOCKING) 包 inTx+文件替换 | verbs/onboard_spine.go:159-280 | **=K2**；lock→txn | **运行时写路径不查此锁**——与非空拒绝(:460-465)+部署时序共同保证（缺口 §4-G2） | E1-ag |
| K3 admission 桶 | 事务内认领行 CAS（非 OS 锁） | domain/lease.go:46-110 | **=K3**，在 K4 事务内 | 认领→runs 插入同事务；跨 Project 互不等待（:39-45） | E1-ag |
| K4 库级写 | BEGIN IMMEDIATE+busy_timeout(5000) | verbs/verbs.go:68-91；substrate/substrate.go:47-56 | **=K4** | 全部 ~90 withSpine 经 `verbs.*→inTx`；锁域守卫前置（lockdomain.go:202-231） | E1-ag |
| N1 project admission | flock `<project>.admission`(NB, RW) | cmd/mc/project_delete.go:32-64,314-332 | K4 **外**，先于 work/txn | 删除：admission EX→**跨 docker rm 子进程**→work EX→spine 写 [SP] | E1-sp |
| N2 project work | flock `<project>.work`(NB, RW) | project_delete.go:150-238 | K4 外；与 N1 嵌套(声明序 admission→work) | **work SH 跨 docker create/run/build 子进程边界持有**（:169-224）[SP]；build 期 100ms tick 探 N1（撤销感知） | E1-sp |
| N3 attachment 健康租约 | 双文件 flock（resident.lock EX 心跳 + resident-epoch.lock SH/EX 排水） | cmd/mc/attachment_guard.go:28-227 | K4 外；epoch SH 先于后续一切 | **broker 持 epoch SH 跨容器边界**：ensureHelper+callHelper→helper 内独立开 spine 事务（见 N18） | E1-ag |
| N4 canary scratch | flock `<canary>.lock`(NB EX)+崩溃 journal | attachment_canary_prod.go:373-397 | 独立域；docker 在其窗口内(:744) | — | E1-ag |
| N5 dashboard 上传 | flock(会话散列, NB) | dashboard_upload.go:85-130 | K4 外，**包** spine 事务 | lock→epoch 租约→run(verb)→inTx；委托模式下 flock 跨 docker exec（宿主侧持有）——两态运行模式未定 [见 §4-G4] | E1-ag |
| N6 project setup | flock `project-setup.lock`(NB) | project_setup.go:160-180 | K4 外，包事务 | check-then-act（Mkdir+onboard）由它串行 | E1-ag |
| N7 config | flock `.config.lock`(BLOCKING) | config/config.go:968-988 | 无 DB——与 K1-K4 无关 | — | E1-ag |
| N8 routing | flock `routing.lock`(BLOCKING)+期望值 CAS | verbs/routingshow.go:44-79 | 无 DB | — | E1-ag |
| N9 install/维护 | flock `install.lock`(NB EX) | setup_published_prod.go:163-198 | 部署级门；**与 K1 的静默协调未端到端追实**（§4-G3） | update 用 journal 文件非 flock | E1-ag |
| N10 agent-profile 操作 | flock(25ms 轮询 ctx)×`<id>.lock/.worker.lock/owner-<conn>.lock` | agentprofile_testop.go:68-142,360-373；opencode.go:103-123；connectionowner.go:26-42 | K4 外 | **worker 锁跨 20min 网络下载**；**owner 锁=子进程生命期**（stdin 信号）[SP]，TS 侧跨 **OAuth HTTPS 刷新×3 试 + mc 子进程写 spine**（provider-credentials.ts:142-179）——flock 跨网络+事务 | E1-sp(SP)/E1-ag(网络窗) |
| N11 provider acceptance claim | flock claim.json(NB EX) | acceptance_provider_prod.go:74-86；provider_acceptance_state.go:201,344 | K4 外 | **跨真实模型轮次持有**（花钱操作的全局声明锁） | E1-ag |
| N12 wiki export | flock `.lock`(NB EX) | wiki/export.go:43,509-525 | K4 外 | **跨全部项目 git 子进程循环持有** | E1-ag |
| N13 eval 案例 | flock(案级, BLOCKING) | evalcase/store.go:326-347 | 无 DB | 测试基座 | E1-ag |
| N14 git index.lock | **O_EXCL 存在性预约**（非 flock）+journal inode 所有权 | repository/apply.go:158-171；journal.go:106-140 | K4 外（仓库域） | 崩溃持有者下弱于 flock——journal 认领+人工移除文本为缓解 | E1-ag |
| N15 log 轮转 | O_EXCL lockfile | mclog/rotate.go:49-66 | 无 DB | — | E1-ag |
| N16 lease/fence CAS | 行 CAS+Fence+boot-id | domain/lease.go:71-183 | K3/K4 内 | 正确性围栏（flock 仅为省耗优化——dispatchverb.go:321-325 原注） | E1-ag |
| N17 交付 owner epoch 号 | spine 内 owner 号+stale 拒→resident 自退 | resident/src/repository-lane.ts:2-3,254-261 | K4 内(CAS)+进程监督 | SQL 体未亲读 [E2 尾] | E1-ag |
| N18 **attachment spine RPC** | **手写 BEGIN IMMEDIATE 帧协议，事务跨容器帧边界驻留** | attachment_spine_rpc.go:144-259[SP]；proxy:95-141,339-342；broker 持 epoch SH 跨之 | **K4 旁路**：不经 `verbs.inTx` ⇒ **不跑 authorizeOperatorTransaction**[SP]；wall-clock os.Exit+defer ROLLBACK 兜底 | **表二 4 的具体化身**：broker-epoch-SH(flock)×docker-exec×helper 持开事务×WAL 写锁——空闲驻留饿死他写者=**定时属性，静态不可判**（§4-G1） | E1-sp(结构)/UNKNOWN(饿死) |
| TS 进程内串行 | tick 单飞/链锁/全局分配锁/single-flight | tick-loop.ts:15-73；repository-lane.ts:207-215；repository.ts:103-111；wiki/trace/operator-lane；agent-profile-runtime.ts:40-46 | 全在 K1 外围（TS 永不开 spine） | 事件循环级，无 OS 锁 | E1-ag |

## 2. 表二逐条判定（候选规则 → 盘点证据）

- **表二-1（文件锁先于事务；禁持事务等文件锁）**：观测域**零违例**——所有"锁包事务"域（K1/K2/N5/N6）为 lock→inTx 序 [SP:K1]；反序形态（事务内取 flock）在 `mc/verbs` 中不存在（该包 flock 仅 dispatchverb=包裹式、routingshow=无 DB）。**判：observed-PASS，非全局证明**（~90 动词体未逐一通读，flock 缺席证据为包级 grep+抽样）。
- **表二-2（事务内逻辑认领确定操作序）**：K3/N16/N24 类均"认领/条件写→追加"同事务 [SP:删除序另证 N1→N2→写]。判：observed-PASS。
- **表二-4（跨层等待环）**：本盘点即反例扫描结果——**已证成环原料**：N18（flock SH 跨容器×开事务×WAL）与 N10（flock 跨网络×子进程×事务）是两条真实跨层持有链；**未发现**闭合死锁环所需的反序共持（无"持 DB 事务再等文件锁"实例，无"持 work 等 dispatch-flock 且反向"实例）；阻塞型 flock（K1/K2/N7/N8）+ docker-exec(120s) + busy_timeout(5000) + 20min 下载窗组合出的**活性风险**（非死锁，starvation/超时雪崩）在 N18 挂 UNKNOWN。判：**无环 observed；活性=UNKNOWN，不得洗白为 PASS**（建议按 RR 语义登记残余，归治理）。
- **表二-5**：本文件履行"盘点闭合前不得称已验证全局锁序"的兑付动作：闭合的是**扫描义务**，不闭合**证明义务**。

## 3. 新发现（进 G-F/A12 与后续契约的弹药）

- **A15-F1（安全语义，高）**：N18 RPC 旁路**绕过 `authorizeOperatorTransaction`**（D25 会话围栏不在该路径）——与 DC-FACTS-001 已登记的"attachment RPC 不验构建身份"同族**且更进一步**；A12（G-F）范围声明须覆盖此入口。
- **A15-F2**：onboard（K2）与运行时写、restore（无锁文件替换 spine_transfer.go:447-521）与 dispatch 的互斥**依赖运维时序**而非共享锁——非空拒绝是唯一代码防线；恢复/演练窗口内派发未停的次序责任在部署规程（A03/A06 关联）。
- **A15-F3**：flock 跨无界外部活动（N10 下载 20min、N11 模型轮次、N12 全项目 git）为**设计选择**（同键互斥/防双花），可用性耦合如实登记；无违例。

## 4. 未闭合缺口（UNKNOWN 清单，禁静默）

- **G1**：N18 空闲事务驻留 vs WAL busy_timeout(5000) 的真实交错——运行时属性，F0a 压测（B06 lane）前不可判。
- **G2**：onboard vs 运行时写：未发现运行侧任何点查 onboard 锁；依赖链=install.lock+helper 生命周期，未端到端证死。
- **G3**：update journal（transaction.json）↔ dispatch 静默握手未逐行追实。
- **G4**：N5 的 run() 宿主/委托两态运行时择路未钉死（影响"锁跨容器"是否常开）。
- **G5**：子代理未穷读全部 ~90 动词体与 `runProjectDocker` 之外的一切 spawn 路径；E1-ag 行的复核深度=行级引用存在性，语义由报告口径承载。

## 5. 验收映射兑付

DC §1.3 表二 5 要求（候选→强制的输入）+ DEV-BASELINE A15（人工跨层扫描，非 grep）：**扫描义务履行完毕；强制化建议随本报告提交所有者**（表二 1-3 建议转强制、F1 建议进 A12/G-F 范围声明、F2/G1-G4 建议按 RR-7/新 RR 候选走治理）。工单 EVIDENCED ≠ 规则生效。
