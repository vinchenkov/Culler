# A32 diff-plan #1（实施轮：C-A P-1 = release.yml 新增 `opt-in-lanes`，两车道起步+一车道呈裁）

状态=CODED（本地义务毕；**runner 实跑证据=推送后首条 CI**，EVIDENCED 转写挂波门，不提前）。授权=轮十六"批准整条议程"（D2 P-1；唯一必改文件、零 secrets、发布枚举三处零触碰——C-A 准备件 §5 不改清单逐项遵守）。

## 1. 改动（恰一文件）
`.github/workflows/release.yml`：`fast-suite-macos` 与 `application-package` 之间新增 job `opt-in-lanes`（ubuntu-24.04，`timeout-minutes: 60`）：
- setup 配方逐字仿 fast-suite（同 pinned action SHA）；依赖装 npm openwiki+patch+agent-runner bun（**不装 dashboard 依赖**——两道均不需，减面）。
- 车道 1=`docker_boundary`；车道 2=`docker_e2e`（`-timeout 45m`=D4 数值，AGENTS 文本同步随 D4 步）。
- **不入任何 needs 链**（`release-checks` 九项逐字不动、`go-cache-cleanup.needs` 逐字不动——新 job 不存 go-build 缓存以保此零触碰）；零新 secrets。

## 2. 三车道→两车道的票面条款执行记录（波门呈裁件）
票面 F1① 原列三道。`update_docker` **本 job 不纳入**，依据=其前置资产唯一来源 `runner/image/build-prod.sh` 在 runner 环境**存在功能性阻断证据**：该脚本 `:74-79` 令 `systemctl --user list-units` 非零退出即 abort（exit 3），GitHub hosted runner 无用户 systemd 总线（该命令典型 `Failed to connect to bus` 失败）⇒ 依票面 F1③"runner 未实测=先试跑再定去留"条款，**证据先于试跑到达**，采"缓纳"。证据级别=**E2 推断（未实测 runner，无日志）**；如波门判应赌实测，恢复成本=job 加 2 步（build-prod + env 两行）。
boundary/e2e 不受此影响：其镜像由测试自身经 `runner/image/build.sh` 构建（`mc/e2e/e2e_test.go:61-65` imageCommand 亲验，不触 build-prod 守卫）。

## 3. 本地证据
- 三守卫 `GUARDS_EXIT=0`（release-workflow/security-gate/smoke-gate，16 pass/0 fail——新 job YAML 解析成功且既有断言零转写，守卫形态为存在性检查之预判经实测成立）。
- 待补：合并前 `dashboard` 全道跑一次（本 job 文件属 dashboard 测试读取面）。

## 4. 边界与延期项（如实）
- **runner 首跑结果=A32 的真正验收**（时延、宿主网关连带验证 A31、fake-harness 在 Linux 的端到端）——推送 D5 后方可得，挂波门后账。
- Linux 上 collie e2e 子界同时是 **A31 的端到端载体**（A31 行 F1③ 合流句）。
- 不改：三发布枚举、prune 正则、install.sh（P-1 承诺）；security-nightly.yml。
