# 车道首跑台账 #1（本 fork 从未跑过的两条 opt-in lane：update_docker / nightly）

背景：AGENTS 规定"改动所证明之物须跑其 opt-in lane"。本 fork 的 AM 条目自 v0.1.54 起反复记 `update_docker/docker_e2e/nightly 未跑`；本轮把其中**不依赖外部授权**的两条真跑一次，如实登记红绿与运行域边界。执行者=执行侧（agent-run E1，非审方验证）。

## 1. `nightly`（dispatch 属性道）— **首跑即绿**
```
cd mc && mise exec -- go test -tags nightly -v -count=1 ./property/...
```
- `ok mc/property 1.103s`，`EXIT=0`；`=== RUN` 27 行、顶层 `--- PASS` 6、**`--- SKIP`/`--- FAIL` = 0**。
- 六个属性测试名（`property/`）：`TestDispatchStateFuzzer` / `TestWikiDecisionFuzzer` / `TestMetamorphicIneligibleRowsAreInvisible` / `TestGeneratorHonesty` / `TestPlantedMutantsKilled` / `TestWikiDecisionStartsOnlyADueWikiUpdate`。
- 计量口径（不自夸也不低估）：`TestDispatchStateFuzzer` = **8 seeds × 2048 例**，每例做**三次**关键断言（Decide 合法性、双跑全等=确定性、输入零突变=纯函数）——即 16,384 样本 / 32,768 次 `Decide` 调用。这是该道的**默认预算，非长时程 soak**。
- 运行域：macOS 宿主（arm64），属性道为脊无关的纯内核性质（故不触 `test_scratch_spine` 文件系统闸）。**未**在 Linux 目标机跑该道（其性质与宿主 fs 无关，登记为边界而非缺口）。
- 意义：`dispatch.Decide` 的**纯函数/确定性/不可见性**不变量在 231 提交的当前态上首次有执行证据（v0.1.25 起该面只有 A20 检查器与快道单测）。

## 2. `update_docker`（升级检查点道）— **首跑即红，且红得有价值**
```
cd mc && mise exec -- go test -tags update_docker -count=1 -v ./deployment \
  -run '^TestUpdateCheckpointDocker' -timeout 12m
```
- 夹具=本 checkout 交叉构建的 Linux 二进制 + 宿主 `mc-prod` 镜像；`TestUpdateCheckpointDockerRestoresAfterInterruptedVolumeCopy` 于 `update_checkpoint_docker_test.go:92`（`CreateUpdateCheckpoint`）失败，`EXIT=1`。
- 该道**只有一个用例**（`grep -rln 'go:build update_docker'` 全仓唯一文件）⇒ 一次运行即该道全量。
- 根因与爆炸半径=`evidence/A03/DEFECT-REGISTER-003-v2.md`（含 v1 归因的自我否证、三处生产位点、四态实测表）。**未修**（不在任何现行正清单）。

## 3. 其余道的当前状态（照录，不粉饰）
| 道 | 状态 | 运行域 |
|---|---|---|
| fast suite（`mc/check.sh`+各 Bun lane） | 绿（v0.1.72 轮 dev `d22ed5d`：mc EXIT=0 零 UNVERIFIED；resident 774/0） | 本机 |
| `docker_e2e` | 三失败**已二分定性为非我方改动**（pre-A16 父提交同态复现）；Q8 裁定运行域=有效 Docker 的定向机/CI，本机记**运行域不满足/UNVERIFIED** ——**【2026-11-22 更正】归因证伪，真因=`evidence/A03/DEFECT-REGISTER-004.md`（容器内 git `safe.directory` 授权被 NOSYSTEM 连带关闭）；改记"产品缺陷在册未修（A29 候选）"，本机 Docker 由所有者声明健康** | 定向机/CI（未跑）；本机待 A29 |
| `docker_boundary` | 绿（A16-V-003 轮，ok/exit 0） | 本机 |
| `update_docker` | **红 + 根因在册**（本件 §2） | 本机与定向机均可跑 |
| `nightly` | **绿（本件 §1）** | 本机 |
| `dashboard-browser` 冒烟 | 绿（A16 轮完整单遍 313/0） | 本机 |

## 4. 方法学记录（本轮的自纠两条）
- **一条被弃用的仪器轮**：`def003-r2`（G/H/K 三案）因两个自家缺陷作废——sidecar 未随主档改名（`probe.db-shm` 配 `spine.db`=测的是无关杂散文件）、`prep` 以 `-f` 判形致单文件案误当 tar 解（卷为空）。K 案当时的 `exit=0` 因此**不可引用**。修正轮 `def003-r2b` 重跑并显式列出各卷文件清单后才用。原始件三份全部归档（含无效轮），无效性在其文件头注明。**纪律**：仪器自证 = 每案打印它自以为在测的东西（文件清单/头字节），否则"绿"可能只是空跑。
- 一次**归因自我否证**：v1 把触发因写成 readonly 挂载，被自己的可写挂载对照（F）与 sidecar 在场对照（G）推翻，v2 改判"能否取得 `-shm`"。v1 文本保留并在原位标注不作结论引用。
