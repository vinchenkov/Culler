# C-A 点名授权准备件（轮十二 Q3"批准进入准备"的执行面；本文不授权任何修改）

状态=**待点名材料**。所有者轮十二已列五项要件，本件逐件填实（读源锚点全部 file:line 实测于 dev `2e86177`）；**workflow 文件族在收到对本文（或本文修订版）的点名授权前零改动**。

## 1. 精确文件与位置（唯一必改文件）
- `.github/workflows/release.yml`（仓内唯一 PR/push CI 文件；`name: Release`；触发 push[main]+pull_request[main]；顶层 `permissions: contents: read`）。
- 插入位置：现 job 序 `security-acceptance(:23) → release-checks(:51) → fast-suite-macos(:69) → application-package(:142) → application-package-linux(:221) → deployment-linux(:301) → deployment-linux-amd64(:348) → dashboard-browser(:395) → dashboard-browser-amd64(:466) → release-images(:537) → …`；新 job 建议置于 `fast-suite-macos` 之后同级（PR 门族）。
- **转述纠正（如实）**：轮九我按 AGENTS 措辞预告"发布 job ID 引用面核对"——实测其真形不是 GitHub job-id 语法，而是**三处发布检查名枚举 + 一处清理正则**：`release/publish.py:21` `CHECKS`、`release/validate_package.py:68` `REQUIRED_CHECKS`、`mc/distribution/record.go:74` `RequiredChecks`（三处同列表 `[fast-suite-macos, deployment-linux, application-package, product-image, release-downloads]`）、`release/prune_go_cache.py:14`（按 job 名前缀组的清理正则）。`install.sh` 未搜到该列表的直接引用（AGENTS 点名了它——实施授权时须复核此差额，防漏引用面）。

## 2. 新增 job（拟名 `opt-in-lanes`）规格
- 内容：`docker_e2e` + `docker_boundary` + `update_docker`（+`nightly` 属性道，秒级）各一 go test 步骤，Linux runner（`ubuntu-24.04`，自带 Docker；fake harness 零凭据——与"PR CI 无凭据无发布权限"不变量相符）。
- 前置核对（实施授权时先跑事实确认）：**GitHub 托管 Linux runner 上 `mise exec -- go build`+真 resident（bun）+镜像构建的可行时延**——本道曾在 218 与本机跑过，托管 runner 未实测（如实挂账）；若时延超发布 CI 容忍，退路=只接 `docker_boundary`+`update_docker`（分钟级），`docker_e2e` 仍走"定向机+本机"双域，此取舍归点名授权决定。
- needs 边：独立同级 job，**不入** `release-checks` 的 9 项 needs（见 §3 形状选择）。

## 3. 形状选择（点名授权须二选一，执行侧不代定）
- **P-1（建议）=PR/push 门，不触发布检查集**：新 job 只挡合并/推送 CI；三处 `CHECKS/REQUIRED_CHECKS/RequiredChecks` 枚举**零改动**、prune 正则零改动（job 名避开其匹配组即无需改）。理由=验收道价值在 PR 门已兑现；发布检查集扩容=另案治理决定。
- **P-2=发布门亦含新道**：三枚举同步 +1、prune 正则复核、`install.sh` 差额复核——改动面×4 文件，发布路径触碰加深。除非要把"道未绿"直接挡在发布外，不建议本轮走此形。

## 4. 三守卫测试转写义务（轮九 Q2 原约束重申）
`dashboard/src/release-workflow.test.ts` / `security-gate.test.ts` / `smoke-gate.test.ts` 按 AGENTS 解析本 workflow——实施时先跑三守卫，转写**只适配形态（如 job 计数/名单新增项），不放宽任何原安全性质**；若守卫语义与新 job 冲突（例如"CI 无凭据"断言的枚举式写法），冲突呈所有者裁，不得静默改判据。

## 5. 明确不改清单（写死在授权文本里）
release 触发（push/PR 分支不变）· 发布 job 及其权限（`release-images*`/`release-downloads` 与 publication 环境）· 凭据注入（零新增 secrets；道全走 fake harness）· 环境保护规则 · `security-nightly.yml` · 发布检查枚举三处（P-1 下）· `install.sh`（P-1 下）。

## 6. 就绪判定
本文 = 轮十二 Q3 要求的"精确清单"本体。**发出授权的动作=所有者对本文（或含修订的本文）点名**；授权文本建议覆盖：文件=release.yml 一处、job 名、P-1/P-2 择一、时延核对前置、§4-§5 义务随附。授权前，本票（C-A 实施票=**A32**〔轮十六票号裁定：PENDING-DECISIONS 曾写 A31，与 Linux 票冲突，最终 A31=Linux、A32=C-A，WAVE-PLAN-001 在册〕；轮十六"批准整条议程"=点名授权已发：文件=release.yml、job=opt-in-lanes、形=P-1）转入实施。
