# G-F-COVERAGE-MATRIX · 宿主 mc→helper 构建身份逐入口覆盖（A12 交付件）

状态：REGISTERED 事实+覆盖声明（执行=常驻授权 v0.1.33 下 A12；出口=**逐入口覆盖测试**，非 RR——DC §5 G-F"二选一不空转"取测试臂）。范围声明：本文件登记"构建身份如何被验证"的逐入口事实与测试锚点；**不构成 G-F 义务完成判定**（那属 F0a/F1 验收与独立复核），亦不与 G-A 合并（R-01）。

约定：**E**=per-write envelope 等值（子端把帧内 release/control/schema/config 与自身常量全等，不符即拒）；**R**=per-contact reconciliation（`ensure()`：镜像 sha256 内容寻址 + `helperExact` 全等重组，失配容器被销毁重建后才 exec）；**S**=A10 的 OpenSpine→CheckSchema 数据面门。测试锚未注明者=既有仓库测试（文件级引用见各行）。

## 1. 写入口 × 强制层

| # | 入口 | spawn 点 | E | R | S | 附加 |
|---|---|---|---|---|---|---|
| 1 | 通用 verb 自委托 | main.go:324-331 | —（argv） | ✓（ensure 先于 delegate） | ✓子端直开 | operator 命令经 #2 双套 |
| 2 | operator 传输（submit/execute/forward+spine call） | operator_bridge.go:239、delegateArgv 重写 | ✓ :175/:305/:383 + guard :19 + 复授权 :83 | ✓ :239 | ✓ | 围栏三件套=A22 套件钉死 |
| 3 | `__system-state`（写 action：proxy-observe 等） | systemstatus.go（docker exec） | —（诊断帧无身份字段） | **✓（A12 增设门，本票修复项）** | ✓withSpine | 修复前=G-F 实然缺口 |
| 4 | attachment spine RPC（publisher/terminal） | proxy:342 dial；broker 操作段 :511-:649 先 ensure | 帧 Version=1 双向锁 + profile 语句白名单 + 事务状态机 | ✓（每个 broker 操作段先 ensure，dial 点 :571/:661 均在覆盖内） | ✓子端 OpenSpine | 非 operator 面；写=语句白名单内 |
| 5 | `__onboard-spine` / `__onboard-state` | onboard_home_prod.go:75 / onboard_state_prod.go:157 | ✓ verbs/onboard_state.go:205 | ✓ | ✓ | onboard_spine_test / onboard_state_test:132 |
| 6 | `__backup-spine` / `__restore-spine` | spine_transfer_prod.go:17/:75 | ✓ verbs/spine_transfer.go:88 | ✓ | ✓ | spine_transfer_test.go |
| 7 | `__doctor-runtime` | doctor_prod.go:52（无 R——只读诊断） | ✓ verbs/ops.go:322（全组件+详错文案） | — | —（不触脊写） | 本票新增否定例（5 分量逐项） |
| 8 | `__acceptance-snapshot` | acceptance_s7_prod.go:48 起 | 快照读+home/release 参数入验收体 | ✓ | ✓ | deployment/s7_test.go |
| 9 | private dispatch 帧族（agent 网关、brokerDispatch 在 #1 ensure 之后） | private_dispatch.go 各点 | ✓ :182/:232/:493/:544 validatePrivateIdentity | ✓（main.go:327 段） | ✓ | resident_control_test / dispatchseam_test |
| 10 | host-local 写命令自委托（project delete/vision/environment/artifact expiry 等） | 各 spawn 前 ensure（project_delete.go:74、project_vision.go:18/:129、artifact_expiry.go:42、environment.go 族） | ✓（Prepare 帧走 :902 validatePrivateIdentity） | ✓ | ✓ | 既有族测试 |
| 11 | `__project-image-digests` | image_removal_prod.go:55 | — | — | —（只读） | **声明为读面**，非写入口 |

## 2. F1 P2：operatorcmd 路由全表（81 条）

分类计数：**spine 写 24 / spine 读 19 / host-local 38**（其中 host 写 17 经 #10 形态、host 读 21）。全表由 `mc/operatorcmd/a12_dump_test.go::TestOperatorRoutingTableMatchesGFCoverageMatrix` 逐行钉死，新增命令未归类即红（漂移闸）。表体=测试内嵌常量（唯一权威，不双写）。

## 3. F1 P3：RPC 边界正反例

正例=发布器全流程在闭合代理不变（既有）；反例=通用 SQL 拒（既有 :244）+ 本票新增 version-mismatch / commit-rollback-无-BEGIN / begin-带杂料 四例（gf_build_identity_a12_test.go）。`exec` 无事务=拒（状态机同族，服务器 :215-238 源证）。

## 4. 残余声明（非静默豁免）

- **R1 tag 内容配对**：运行时逐接触锚定的是"当前 `mc-prod` 标签镜像的内容 ID"；标签↔宿主二进制的配对在 provisioning 期由发布记录锁死（production_image.go:41/:70、setup_published_prod.go:732：镜像 label==记录 commit==releaseBuildID）。**手工重打 mc-prod 标签**属"更换部署本体"级操作，与手工替换宿主二进制同威胁等位，产品内不存在也不应存在对其的运行时防线——如实登记，不宣称已排除。
- **R2 ensure→exec 微窗**：ensure 后按确定性名字 exec，名字二次派生同值（与 #1 既有形态一致）；竞态替换容器由 `helperOwned`+下次 ensure 全等复检收敛。
- **R3 平台面**：docker 真实往返/Linux 原生矩阵=e2e 与 F0a 验收面（A03/A04/O3），不在 fast-lane 声明范围。
- **R4 wire 形态**：本票未动任何帧形状（DC/prepare-attest-commit 不变量）；#3 修复复用 ensure 机制而非新增 envelope 字段——加字段=设计决定，明确不做。

## 5. 判定边界

"逐入口覆盖测试"出口在**机制验证层**给出：每入口的强制层存在且被测试钉死。混合版本写入风险是否"已排除"属 F0a Exit 判定（待记），须连同 R1-R3 残余一并 weigh；AM A9 的措辞禁令在 G-F 复测包络（DC §6）完成前继续生效。
