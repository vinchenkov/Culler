# A28 验证记录 #1（TM §7.1 · 更新流两处门的停写脊读径 · 含本票范围内的一次自我更正）

- verification_id: **A28-V-001**
- target_component: 安装/更新事务中**能证明脊已静置**的两处数据库检查（`prepare` 更新支 :638、`checkCandidateProjectImages` :159）从活读门改走**停读门**（暂存副本→当前二进制进程内 `OpenSnapshotReadOnly`）；另两处（`stopAndRecheck` :499、`drain` :813）**维持活读并登记残余**（§0b）。
- tested_version: 票支 `shz/TA28`，提交 `d1042b3`（立票）→ `29fe8bf`（实施+同批更正）；dev 基线 6bd69a0。
- 形状依据（为何不再"参数化共享前导"）：源守卫 `mc/verbs/onboard_lockdomain_test.go:24-49` 机械要求 `inspectSpineReadOnly` **体内逐字含** `substrate.OpenReadOnly(` 且体内禁 `sql.Open(`，并禁全包出现 `mode=ro`（:58-69）。A27 教训=把 opener 作值传递会令该逐字要求失配。故 `inspectSpineReadOnly` **字节不动**，新增**独立复制前导**的 `inspectSpineSettled`（体内逐字 `substrate.OpenSnapshotReadOnly(spine)`），`InspectReleaseDatabase` 保持薄壳、新增 `InspectSettledReleaseDatabase`。

## 结果
1. **正控** `TestPublishedDatabaseSettledReadUsesTheStagedCopy`：真实闭合脊（`substrate.Open+Init+meta+Close`，无残留）经暂存→停读门读回 `uuid/tables`；断言**整个调用序列不含 `__release-database`**（证明确实换了门，而非两条都走）。
2. **反控** `TestPublishedDatabaseSettledReadRefusesWriteAheadResidue`：放入 `spine.db-wal` 后停读门**必拒**；且拒绝以 `setupFailure` 形态呈现——`cause` = 既有已评审操作者句（"work began before the services stopped…"），`details` 携技术成因 `snapshot precondition unmet`（**成因不被吞**，A27 R3 同律）。文案复用而非新增，规避终端文本对抗评审门口径已在票行声明。
3. **迁移一枚在制 fake**：`TestCandidateChecksArchivedImagesWithIsolatedRuntime`（compatible/incompatible 两态）改由真实脊行导出 `ProjectImages/Empty`——**该段计数联表 SQL 首次被测试覆盖**（此前 canned JSON 从不验它）；其 `check` 脚本安全断言与 `image inspect` 次数断言**原样保留**（未放宽）。为防误判，暂存识别需同时匹配 `dst=/source,readonly,volume-nocopy` 与 `dst=/destination`（候选门另有含 `readonly,volume-nocopy` 的运行）。
4. **未迁移面（诚实收缩，非疏漏）**：`TestPublishedCancellationRestoresServicesAfterTheStop`、`TestPublishedMaintenanceRequiresMatchingReceiptAndIdleDatabase`（idle/busy）、`TestCullerResidentStopWaitsForEveryWorkType`（五类活工作计数器）、`TestCullerStopRechecksWorkAndRestoresAllServices` 所经路径**仍走活读门**——因追证发现 `stop()` 只停监督单元、**容器不停**，静置不可证；若强改停读，需把五类工作计数器映射成真实行（`repository_executions`/`environment_operations` 等表列多、FK 深），属另一票面。这些测试的断言与 fake **零改动**。
5. **入口守卫扩点**：`mc/verbs/spine_read_entry_guard_test.go` 白名单加入 `verbs/onboard.go:1 / verbs/release_database.go:2 / cmd/mc/setup_published_prod.go:1`（**一次命中实跑数**）；`verbs/onboard_spine.go`（redeploy 前门=site 3）仍显式禁止进白名单；植入三类误路由的负控维持。**守卫的牙齿由 A27 的一次真实红保证**（其计数漂移曾令其红）。
6. **车道**（合并闸，退出码自取）：`update_docker` 与 `docker_boundary` 复跑、`mc/check.sh` 全道、`resident/check.sh` 全道——结果值随 AM 登记轮写入（任一红则不并 dev）。§7.1 撰写时三道在跑。
7. **零触面声明**：未改 `dispatch/`、SQL 文件、`dispatchseam.go`、envelope/wire 键、任何 `.github/workflows`、任何 TS；`mc/cmd/mc/redeploy_check_prod.go` 未改（site 3 归 L02）。

## 同批连带发现（改变另一在册结论，独立登记）
`evidence/A03/DEFECT-REGISTER-004.md`：`docker_e2e` 的真因是 **`sourceGitEnv()` 的 `GIT_CONFIG_NOSYSTEM=1` 连带关掉了部署镜像自带的 `safe.directory=*` 系统授权**，容器内读外属主仓库必报 `detected dubious ownership`（单变量隔离矩阵 + 两次复现 + 修复候选验证）。**"本机 Docker Desktop 坏域"的旧定性由此被证伪**——受影响登记面 19 处及其可更正性分类在同件 §5 与 AM 后续条目处理；修复属 `verbs/sourcegit.go`，非本票正清单 ⇒ **A29 候选**，待所有者授权。

- reviewed_by: host-agent（执行者兼任复核声明沿用；Integrity 未做，不宣称）
- verdict: **A28=更新流两点停读径闭合 + 两点具名残余 + 一次范围自我更正**；EVIDENCED 转写随 AM 登记轮，义务完成判定仍归 F0a Exit 与治理链。
