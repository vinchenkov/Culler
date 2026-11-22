# Defect register 004 — `sourceGitEnv` 关掉 system config 时连带关掉镜像自带的 `safe.directory` 授权，容器内读外属主仓库必拒（docker_e2e 真因；**非 Docker Desktop**）

状态：**REGISTERED, NOT FIXED**。`mc/verbs/sourcegit.go` 不在任何现行正清单内（A28 正清单不含它），修复=新授权（建议 A29，见 §5）。本文件不宣称任何代码已修复。

## 1. 症状与因果链（e2e 道）
`mc/deployment/e2e` 的 walking-skeleton 用例等 Strategist run 产出，**超时**：
> `e2e_test.go:256: timed out (1m0s) waiting for a Strategist run spawns and terminates via empty batch; last: no new completed strategist run yet`

现场链（子代理保现场取证 + 本代理独立复现）：容器内 `mc`（`cmd/mc/main.go:878-882` 的 seeding projection 入口，`verbs/seedingprojection.go:75 → resolveBaseOID → gitAtSource`）报
> `mc: could not resolve target ref "refs/heads/culler": git rev-parse … fatal: detected dubious ownership in repository at '/repo/source'`

⇒ 环境播种失败 ⇒ run 起不来 ⇒ 脊内无 run ⇒ dispatch 恒空转。**旧结论"本机 Docker Desktop 既有环境问题/运行域不满足"被此机制证伪**（所有者亦明言本机 Docker 无问题）。

## 2. 机制（单变量隔离矩阵，本机 mc-prod 镜像，git 2.48.1）
根因在 `verbs/sourcegit.go:60-68 sourceGitEnv()`：它置 `GIT_CONFIG_NOSYSTEM=1`（隔离操作者/系统配置，属**正确的信任边界设计**），但**部署镜像的 system gitconfig 内容恰是 `safe.directory=*`**（实测 `git config --system --list` 输出）⇒ 关系统配置时**连带关掉了镜像自己给出的授权**。矩阵（同一 root 属主卷、进程 uid 10002、只读挂载）：

| env | 结果 |
|---|---|
| 基线（无覆盖） | **OK**（读到 OID） |
| 仅 `GIT_CONFIG_NOSYSTEM=1` | **REFUSE：detected dubious ownership** ← 翻转点 |
| 仅 `GIT_CONFIG_GLOBAL=/dev/null` | OK |
| 仅 `HOME=/tmp` | OK |
| 三件套（=产品 `sourceGitEnv` 形） | **REFUSE** |

⇒ 翻拒的**唯一必要变量**就是产品自己设的 NOSYSTEM。子代理在真实 e2e 容器内的两次逐字复现与此一致；其修复候选（同 env 下加 `-c safe.directory=/repo/source`）成功返回 wiki 导出的同一 OID。

## 3. 影响面（读源，不外推）
经 `gitAtSource` 的全部源码读路径：`verbs/seedingprojection.go`（环境播种，本案触发点）、`verbs/sourcegit.go`（`sourceCommonDirAbs`/`checkSourceExtractable`/`resolveBaseOID`）、`verbs/onboardprojectgit.go`（项目登记与分支校验的 8 处调用）。**同型的属主失配在生产形态同样成立**（宿主建 workspace、容器以 10002 跑；非 macOS 特有）——但**我未在生产宿主实测该路径**，此项标 UNVERIFIED，不外推为"生产已坏"。
对照事实：交付执行器 **`mc/repository/git.go:process()` 已显式携带 `-c safe.directory=<root>`** ⇒ 产品在一处承认该风险并处理，在另一条路径未处理＝**同产品内部不一致**，非环境问题、非新语义要求。

## 4. 为何活了这么久
`docker_e2e` 在 CI 中**从不执行**（仅 `go vet -tags` 编译，`mc/check.sh:23-24` + `release.yml` 无该道步骤，载道勘查 `evidence/LANES/lanes-carrier-001.md` 已证）；历轮本机偶跑时把红归因给了"Desktop 环境"。**"挂账变哑"的又一完整实例：无承载者的道，其红会被误归因消解。**

## 5. 修复形状（呈报，未执行）
- **F-1（建议，最小且与既有先例同形）**：`gitAtSource` 的 argv 前置 `-c safe.directory=<该仓库绝对路径>`（与 `repository/git.go:process()` 同式，逐调用点显式声明，**不用 `*`**、不动 `GIT_CONFIG_NOSYSTEM` 信任边界）。测试=**行为哨兵**（真实属主失配容器下 rev-parse 由拒转变现成功，非字符串断言；A26 双控纪律）。
- **F-2（不采）**：去掉 `GIT_CONFIG_NOSYSTEM`——为通过而削弱既有信任边界，禁则；且把行为外包给镜像配置。
- **F-3（不采）**：镜像里改 `safe.directory=*` 之外再放宽——扩大授权面。
- 验收域：修后 `docker_e2e` 在本机（所有者声明其健康）与定向机/CI 各跑一次；**此前所有把它记为"运行域不满足"的在册句须按更正纪律改写**（AM 变更史只增不改、§7.1 追加更正段、DEV-BASELINE/设计件可就地转写）。
- 立票建议：**A29**（承载 F-1 + 三处调用族的负控 + e2e 道本机首绿），与 A28 独立（不同文件族，可并行）。
- **【轮十二注】执行差与状态**：F-1 落地（单咽喉+两旁路）；验收域实际=**本机半边兑现**（首绿+16/17），**定向机/CI 半边未跑**（如实挂账，"各跑一次"原句未完成）；道内唯一残留红 `TestCollieNativeProxy*` 经 A30 预勘+本代理复验**定性=传输层独立缺陷**（DEFECT-005 更正段 #1），与 sourcegit 族零交集再证 ⇒ **不影响本件闭合**（本缺陷症状 seeding-dubious-ownership 已被端到端否证）。

## 6. 闭合段（2026-11-22 轮十一，A29 实施；登记不修→授权后闭合）
- **修复=F-1 落地**：`mc/verbs/sourcegit.go` 新增 `safeDirectoryArg(dir)`（`filepath.Abs` 归一；Abs 失败=无旗走今日行为，不静默换形），前置入 `gitAtSource` 单咽喉与两处旁路直调（`onboardprojectgit.go:243/:407`，探针保留 `GIT_OPTIONAL_LOCKS=0` env 差异）；`seedingprojection.go` 合成 archive 直调**不改**（实验证 GIT_DIR 显式设定时 git 跳过发现与属主检查，无需旗）。零新 verb/wire/相位词。
- **链级生效实证（本机，Desktop=所有者声明健康；完整状态表经补跑凑齐，含一次 `-run` 模式漏项的当场纠正）**：`docker_e2e` 17 个顶层用例 **16 绿 / 1 红**——本缺陷原症状正主 `TestWalkingSkeleton` **首绿 133s**；此前要求真产活的整片（`TestBackpressure` 569s、`CorrectionRally`、`CredentialProjection×2`、`RebootDrill`、`MultiApproveDrain`、`RoleSkillIsolation`、`LandingFailureAndRecovery` 等）全绿。⇒ 播种投影不再被 `dubious ownership` 挡死，run birth→dispatch→回收全链通。
- **修复面负控**：argv 注入弱守卫（旗先于子命令/值绝对/非 `*`）+ 旁路源守卫（两处非咽喉直调必带 `safeDirectoryArg`，计数不涨）；`TestReadOnlyInspectionsOpenThroughTheSharedPath` 等被钉不变量**未破**。bespoke 双向容器哨兵（root 属主仓库×10002×pre/post 双跑）因操作者扫描钩子与其包内已批同形件冲突、三度拦下 ⇒ 存 `evidence/A29/blocked-boundary-test.txt` 待裁工件，**强证由上列 e2e 端到端链承担**（断言体即原症状，较容器单点更强）。
- **唯一残留红定性**：`TestCollieNativeProxyDockerBoundary/{codex,claude-sdk}` = **独立缺陷 DEFECT-REGISTER-005**（wake/birth 层；父提交 `04cd6b2` 无本改动**同红同签名** `PARENT_EXIT=1` 实证非我引入；其余三枚 collie 测全绿把红收窄到"native proxy resident 晚启"特有面）。**本缺陷（004）就此闭合**，005 另呈（A30 候选或并入 L02 计划内时序家族，归所有者裁）。
- **状态转写**：REGISTERED, NOT FIXED → **FIXED via A29**（CODED；e2e 链级 16/17 实证；EVIDENCED 转写随 AM 登记轮，义务完成判定仍归 F0a Exit+治理链）。通道预算注：包级 30m 对 17 测不足（实测全跑 ≈40m+），本批补跑用 45m；数值更新属测试域呈报项，未擅改。
