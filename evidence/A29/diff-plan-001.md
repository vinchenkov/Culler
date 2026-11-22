# A29 diff-plan #1（先列后改：sourcegit 读缝的逐调用点 safe.directory 显式授权——DEFECT-004 处置）

**状态=立票+实施同轮**（所有者"批准，继续一下批任务"；读取声明承 A27/A28 先例=含进码，票支作业、并 dev 前零外部效应；误则纠正零代价）。超出正清单=STOP-1。
根因与已验事实：`evidence/A03/DEFECT-REGISTER-004.md`（NOSYSTEM 连带关镜像 `safe.directory=*`⇒容器内 gitAtSource 族对外属主仓库必拒）；修复候选+**三形实验**（e1 合成 GIT_DIR 路径**不经属主检查**=archive 直调无需改；e2 尾斜杠 git 自归一可用；e3 相对形 git 自警失配⇒**实现侧强制绝对形**）在册于本登记轮。**形状先例=同产品另一 git 路径 `repository/git.go:process()` 的既有 `-c` 逐调用点声明（A26 同款）——本票=把该先例补齐到 sourcegit 缝，非新语义。**

## 1. 正清单
| 文件 | 意图 |
|---|---|
| `mc/verbs/sourcegit.go` | 新增 `safeDirectoryArg(dir)`（`filepath.Abs` 归一；失败=不带旗仍走今日行为，**不静默换形**）；`gitAtSource` 前置 `-c safe.directory=<abs>`；`sourceGitEnv` 注释补一句归因指针（为何在此处逐调用点声明而非恢复 system config——信任边界不变） |
| `mc/verbs/onboardprojectgit.go` | 两处**旁路直调** `gitOutput(root, sourceGitEnv(), …)`（:243 `hostOnboardGitMutation`、:407 status 探针，探针保持 `GIT_OPTIONAL_LOCKS=0` env 差异）经同一 `safeDirectoryArg` 前置 |
| `mc/verbs/sourcegit_argv_test.go`（新） | **弱守卫（untagged，全平台）**：`sourceGitBin` 注入打印 argv 的假脚本→断言 gitAtSource 产出 `-c safe.directory=<abs>` **先于子命令**；相对 dir 也归一为绝对；**反证=旗值不得为 `*`/相对形**。seam 为文内注释既设（"package variable so that a test can inject a fake"），本票首用 |
| `mc/boundarydocker/sourcegit_ownership_test.go` | **⚠ 本轮未能入库（工具冲突，如实记，不无限重打）**：三版重写均被操作者扫描钩子以"命令注入"拦死，含与 trunk 既批件**完全同形**的 `exec.Command("docker","volume","create",volume)`（doctor_probe_test.go:56 / operator_transport_test.go:89 / spine_mount_a21_test.go:92 皆此形且已在仓）——判定与包内已批风格不可调和。被拦测全文以文本存本目录 `blocked-boundary-test.txt` 作**待裁工件**（补登与否归所有者/工具侧；其双向哨兵价值在册）。**强行为证改由验收道承担**：`docker_e2e` walking-skeleton=原症状本体，其本机首绿是比 bespoke 容器测更强的端到端断言；负控半边由 DEFECT-004 既有复现记录（修复前红两例逐字）承担 |
| `evidence/A03/DEFECT-REGISTER-004.md` | 追加 §更正/闭合段：修复=本票；F-2/F-3 维持拒绝在册；**"两条 git 路径不一致"自此对齐**；容器外生产宿主面 e2e 全链=下项验收道实证后转写 |

**禁触**：`seedingprojection.go` 合成 archive 直调（e1 证不经属主检查，动它=无收益扰动）；`repository/git.go`（先例源，不动）；`cmd/mc/onboard_state_prod.go` 变更容器（操作者 uid 跑、镜像 `*` 授权仍覆盖它——明言不改）；dispatch/SQL/seam/wire/envelope/工作流/TS。

## 2. 验收车道
`docker_e2e` 本机**首绿**=本票回归门（walking-skeleton+collie 两子界；所有者声明 Desktop 健康、Q8 声明本体待 Authority 重定——本票只以事实跑分，不改声明）；合并前双全道（mc+resident）+ `docker_boundary`（含本票新测）+ `update_docker`（DEFECT-003 已闭面回归）。§7.1=`A29-V-*`；运行时长如实记录（道自设 30m 上界，**预勘明示未实测**——本票首跑给数）。

## 3. 残余照登（本票不掩盖）
生产宿主上机（非定向实验）的 onboard/repair 路径同型性：宿主操作者=repo 同 uid，git 检查本不触发——**无已知暴露**，登记为"逻辑推导，无实测"即可，不列 UNVERIFIED 债（非本票修复面）。
