# DC-FACTS-001 · Spine 写入入口的版本/身份门禁审计（控制流级）

- 对象：Culler b22df40，只读；执行时间 2026-11-19；结论三分=**CONFIRMED**（读到调用路径与控制流）/ **NOT_CONFIRMED**（机制存在但路径约束未证）/ **NOT_FOUND**（声明范围内未发现）。
- **检索范围声明**：`mc/**/*.go`、`resident/src/*.ts`、`dashboard/src/*.ts`、`runner/{agent-runner,collie-runner,setup-runner,shared,image}/**`、`release/*.sh`、`mc/deployment/*.go`；排除 `*_test.go`、`*.test.ts` 与测试车道目录（`mc/{e2e,property,testenv,boundary,boundarydocker,evalcase,wikitest}`）。
- 用途：DC v0.3 §5/§1.3 的事实底座；复审方可对下列行号独立读源复核（DC A5）。本报告为代理取证记录，结论解释以 DC v0.3 §5 为契约侧唯一权威。

## 关键发现摘要（详表见下）

1. `substrate.Open` 本体=GuardLockDomain+pragma DSN，**无任何 schema/版本读取**（substrate.go:47-65；lockdomain_other.go:23 darwin 放行）。
2. **普通写 verb ≈90 入口（`withSpine`，cmd/mc/main.go:1229 族）落库前无 schema 身份、无构建比对**（verbs/init.go:166-169 仅路径非空检查）——CONFIRMED。
3. `CheckSchema` 调用者全集仅 onboard/doctor/spine_transfer(backup/restore)/update/inspect 家族——**不在任何普通写 verb 路径**——CONFIRMED；读 live `meta.schema_id`/活体哈希的 SQL 只存在于 CheckSchema 内部——NOT_FOUND（普通路径）。
4. agent 容器写：run.json 提供身份（tier/role/own-run+setuid 防伪，verbs.go:294-368,242-250）但 **pipeline run.json 不含 release_build_id 字段**（effects.ts:1340-1367；仅 collie envelope 写 :625）；容器无 journal 可见性，不受 BlocksWork——构建交叉验证 NOT_FOUND。
5. `BlocksWork`（update_journal.go:44）消费者全清单：operatorDeploymentGuard（operator_guard.go:26←operator_bridge.go:171,467）、culler CLI（culler_services_prod.go:109）、安装/更新状态机（setup_install_prod.go:111 等）、onboarding（onboard_home_prod.go:52）、opencode 供给（opencode_source.go:86,109）、**resident tick 上游阻断**（tick-loop.ts:80-83→main.ts:777→install-maintenance.ts:7-33）、repository/environment/operator lane（repository-lane.ts:401 等）、**dashboard mutation 503**（server.ts:82-97,212-214）。**mc 自身写路径不读 journal**——直接 exec helper 不受更新阻断（NOT_FOUND+架构事实）。
6. matched-pair 运行时强制点=per-crossing 四族：dispatch hello 五字段双向（resident_control.go:126-157；resident-control.ts:14-20 现算 .sql 哈希,303-310）+private dispatch 三 crossing 四元组（private_dispatch.go:109,132,160→181-187 及应答侧 :232,493,544）、operator 链（operator_bridge.go:175,383；operator_requests.go:83；operator_guard.go:19-51）、transfer/onboard/doctor/update 私有帧（spine_transfer.go:88-99；onboard_state.go:205,374；ops.go:311,507；update_reconcile.go:21-30）、部署期一次性配对（build.sh:62-65；runtime_volume.go:84；helper_manager.go:258-318）。**agent↔宿主/helper 无交叉校验**（NOT_FOUND）。
7. darwin 委托普通 verb/`__system-state`：argv 透传不附版本字段（main.go:471-533；systemstatus.go:62-92 closed schema 无 release 字段），helper 接收侧 host-scope 恒真（无 run.json）——**完全无版本门禁的写通道**（含 proxy-observe/probe/verified 等可写 action，systemstatus.go:402-416）。
8. 例外登记：`mc __reset` 无 schema 门（ops.go:217-248）；onboard sections（project/tunables/surfaces/smoke，onboard.go:1342,1690,1766；onboardsmoke.go:113）裸写依赖 `__onboard-spine` 程序性先行。

## 覆盖矩阵（v0.3.1 按 R-12 分列：互斥类型不再混装；✅CONFIRMED｜◐NOT_CONFIRMED｜✖NOT_FOUND）

| 写入入口 | 文件互斥(flock) | DB 写串行化(WAL/IMMEDIATE) | 业务竞争(CAS/UNIQUE/lease fence) | live schema 校验 | 构建身份校验 | 更新阻断(BlocksWork) | 残余无门禁→义务 |
|---|---|---|---|---|---|---|---|
| resident→mc dispatch→helper 三 crossing | ✅flock(dispatch) | ✅ | ✅CAS+fence+收据 | ✖（仅 deployment_uuid 镜像比对，dispatchseam.go:1114-1118） | ✅五字段双向 | ✅上游 tick（mc 内✖） | 混版⇒tick 停摆而非静默写坏；schema 面→G-A |
| 宿主 mc→helper 普通 verb（含 __system-state、attachment RPC） | ✖（无专属文件锁） | ✅ | ◐（verb 各异，随六维登记） | ✖ | ✖ | ✖ | **构建身份+schema 双无→G-A(schema)/G-F(build)** |
| agent 容器 mc 写 | ✖ | ✅ | ✅lease fencing | ✖ | ✖（run.json 无版本字段，effects.ts:1340-1367） | ✖（无 journal 可见） | scope/tier/role ✅[CONFIRMED，另类控制]；**构建身份/版本来源未逐写验证→G-B/G-A** |
| dashboard→mc | ✖（经入口2） | ✅ | ◐ | ✖ | ◐（x-culler-release 仅管浏览器↔UI，server.ts:209） | ✅UI 层(82-97,212-214) | 过 UI 门后=入口 2 残余 |
| collie 容器写 | ✖ | ✅ | ✅allowlist+代次 | ✖ | ✅三点（operator_bridge.go:175,383；仅 operator 命令路径）| ✖（id==nil 分支 :170） | 普通 collie 写行=入口 2/3 残余 |
| backup/restore | ✖ | ✅ | ✅五重闸 | ✅CheckSchema 前后(241/456) | ✅帧全比对(88-90) | ◐（chore 挂 tick 内） | 窄；`mc backup` 写打开仅 VACUUM INTO |
| onboard/reset | ✅onboard flock | ✅ | ◐ | ◐（spine 创建/探针✅；sections/reset ✖） | ◐（section 帧✅；sections 经委托✖） | ✖ | sections/reset 裸写面→G-D/G-E |
| update 路径 | ✅installer flock | ✅ | ✅ | ✅ | ✅镜像=installed 记录链 | ✅本体 | — |

## 一句话结论（v0.3.1 按复审 1.2 精确化，v0.3 §5 据此定形）

**版本/身份门禁当前是 per-crossing 的（五类私有帧），不是 per-write 的；共享 spine 上的普通写动词——含 agent 容器的全部写——其"调用方构建身份/版本来源"未被逐写入口验证（NOT_FOUND），此为 G-A..G-F 的实施义务来源。精确性声明：这些路径并非全无控制——tier/role/own-run scope、setuid 防伪与租约 fencing 均 CONFIRMED 存在（属"业务竞争/scope"类，另栏登记）；本结论只否定"构建身份与版本来源"的逐写验证，不抹除既有 scope 控制。** 一句话版：**门禁真空的是"版本/构建身份"维，不是"授权"维。**
