# 车道承载者勘查 #1（`docker_e2e` / `update_docker` / `docker_boundary` / `nightly` 有无自动承载者）

性质=**决策支持**（Q8 宣布的运行域、N-1 的修复回归面、F0a 的验收面共同依赖此事实）。执行者=执行侧，只读勘查 + 一次定向机探测，未改任何文件、未装任何工具。

## 1. 结论（先给）
**这四条道今天在任何地方都没有自动承载者：CI 只编译它们、从不执行；本机 Docker Desktop 是已定性的坏运行域；定向机没有可跑该道的工具链。** 因此 Q8 所说"验收域=有效 Docker 的定向机/CI"目前**两侧皆不成立**——这不是"我们没去跑"，而是**没有跑的地方**。残余风险的准确表述应从"本机缺有效 e2e 信号"改写为"**缺有效 e2e 承载者**"。

## 2. 证据（逐条行级）
| 面 | 事实 | 出处 |
|---|---|---|
| CI 编译面 | `mc/check.sh:23-24` 对 `nightly docker_e2e docker_boundary update_docker test_fake_routing test_scratch_spine` 逐个 `go vet -tags` ——注释自述目的="a tagged suite that no longer compiles rots invisibly while the fast lane stays green" | 读源 |
| CI 执行面 | `.github/workflows/release.yml` 全文**无** `docker_e2e`/`docker_boundary`/`update_docker` 执行步骤（`grep` 零命中）；`security-nightly.yml` 的 "nightly" 是**安全扫描**道，非属性道（同名不同物，易误读） | 实测 grep |
| CI runner | 全部为 GitHub 托管：`macos-15` / `ubuntu-24.04` / `ubuntu-24.04-arm`——**Linux runner 自带 Docker 引擎**，且本道用 **fake harness**（`e2e_test.go:812-814` "routes every role to the fake harness"、`:5/:10` 生命周期注记、`runner/image/build.sh:6-8` 产 `mc-fake-e2e` 带 `test_fake_routing`）⇒ 零模型 token、零 provider 凭据 ⇒ 与"PR CI 无凭据无发布权限"不变量**不冲突** | 读源 + AGENTS |
| 定向机 218 | `mise`/`go`/`bun` **全部 MISSING**（docker 29.4.0、git 2.34.1、node 有）；`/` 已用 89%（可用 ~50 GB）；culler 单元 0；宿主上另有**第三方租户容器**在跑（nexusflow-*，未触） | 实测探测（只读命令） |
| 定向机能否绕过工具链 | **不能**：`e2e_test.go:632` 与被测镜像构建 `runner/image/build.sh:31-32` 都在**驱动机**上调 `mise exec -- go build`；`e2e_test.go:1085` 更在驱动机上以 `mise exec -- bun src/main.ts` 起**真 resident 进程**（resident 不落容器）⇒ 需宿主级 Go+Bun+被 pin 的工具链，非"拷一个测试二进制"可代 | 读源 |
| 本机 Docker Desktop | 已定性：pre-A16 父提交 `8dce77c` 上同样红的骨架空转（AM v0.1.64 二分在册），锁域项因 virtiofs 呈现已由 Q7 重钉为拒绝性质断言 | 在册事实 |

## 3. 若要给出承载者，三条候选（成本如实，全部**需所有者点名授权**，执行侧不动）
- **C-A（最低成本，建议）**：CI 新增一个 Linux job 跑 `docker_e2e`+`docker_boundary`+`update_docker`（fake harness ⇒ 无凭据；runner 自带 Docker；mise-action 已在用）。
  代价/风险：① 触 `.github/workflows/release.yml`=**workflow 文件族须授权时点名**（003 附三 + Q10 双重在册）；② 三守卫测试转写义务（`dashboard/src/release-workflow.test.ts`/`security-gate.test.ts`/`smoke-gate.test.ts`，AGENTS"改 release.yml"条目）；③ 发布 job ID 被 `release/publish.py`/`mc/distribution/record.go`/`install.sh` 引用，新增 job 须核对其枚举假设；④ CI 时长与 arm runner 排队；⑤ **前置阻塞=N-1 的 DEFECT-003**：`update_docker` 今天必红，把它接进 CI 之前必须先修（否则新增 job 首跑即红，或被迫放宽——两支都不可接受）。
- **C-B**：定向机装 mise+go+bun 后在 218 跑。**不推荐**：属生产宿主持久变更（需专门指令），且该宿主为他人共用、`/` 已 89%；本 fork 的隔离原则也不支持为跑测试在共享生产机上铺工具链。
- **C-C**：维持现状=**每次真跑由执行侧手工组织**（本轮 update_docker/nightly 即此形）。代价=不可复现、易再变哑（正是 DEFECT-002/003 的病灶）。

## 4. 本轮已按 C-C 交付的部分（不需授权，已完成）
`update_docker`（红+根因）、`nightly`（绿）两条已由执行侧真跑，见 `evidence/LANES/lanes-first-run-001.md`。`docker_boundary` 此前已绿（A16-V-003）。**`docker_e2e` 在四条道里唯一仍未在任何有效域跑过**——它比另三条更吃工具链（bun+真 resident+镜像构建），且本机域已被否证；故其有效域首跑事实上取决于 C-A 或 C-B 的授权。

## 5. 轮九 Q2 裁定（2026-11-22，登记于此使本件与决定同步）
- **C-A=终态承载者路线获原则批准**；依赖序钉死为 **A27 落地 → update_docker 本地转绿 → workflow 文件族单独点名授权 → CI job 落地**。C-A 正式实施票必含：三守卫测试转写（release-workflow/security-gate/smoke-gate——**转写=适配形态，不是为让新 job 变绿而放宽守卫，原安全性质保留**）+ publish.py/record.go/install.sh 发布 job ID 引用面核对 + 新 job 与既有发布流程的权限边界检查。
- **C-B 不采用**（生产宿主持久变更+共用+根分区 89%）。
- **C-C 可作过渡状态保留，但不得登记为最终验收承载者**——本件 §3/§4 的"C-C 已交付"字样按此定性读取；任何以"C-C 已覆盖"为由关闭承载者问题的表述无效。
