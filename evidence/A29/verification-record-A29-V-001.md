# A29 验证记录 #1（TM §7.1 · sourcegit 读缝逐调用点 safe.directory 授权 · DEFECT-004 闭合）

- verification_id: **A29-V-001**
- target_component: 播种投影/ onboard / uninstall 共用的 source-git **读缝**在容器内对外属主仓库的可用性（DEFECT-REGISTER-004：`sourceGitEnv` 的 NOSYSTEM 连带关掉镜像 `safe.directory=*` ⇒ `detected dubious ownership` ⇒ 播种失败⇒run 不 birth⇒骨架空转）。
- tested_version: 票支 `shz/TA29`，提交 `63aa146`（实施）+`3e4c83f`/`17a030c`（DEFECT-005 登记与自我纠正）+闭合段提交；dev 基线 `04cd6b2`。
- 形状：`safeDirectoryArg(dir)`（`filepath.Abs` 归一，失败=无旗走原行为不静默换形）前置入 `gitAtSource` 单咽喉 + 两处旁路直调（`onboardprojectgit.go:243/:407`）；合成 archive 路径不改（GIT_DIR 显式设定即跳过属主检查，实验 e1 在册）。零新 verb/wire/相位词；信任边界（NOSYSTEM）不动。

## 结果
1. **链级生效=本票验收门（缺陷自身见证体）**：`docker_e2e` 本机（Desktop=所有者声明健康）**17 顶层用例 16 绿 / 1 红**：
   - 原症状正主 **`TestWalkingSkeleton` 首绿 133s**（此前 3 轮红）；要求真产活的一片全绿——`Backpressure`(569s)/`CorrectionRally`/`CredentialProjection`×2/`CollieConversation`/`CollieResume`/`ColliePacketArtifacts`/`SpineLockDomain`/`DeploymentMirror`/`FaultInfrastructureRetry`/`FaultBudgetExhaustion`（首轮 12）+ 补跑 `RebootDrill`(137s)/`MultiApproveDrain`(136s)/`RoleSkillIsolation`(41s) + `LandingFailureAndRecovery`(83s，首轮被 30m 预算 panic 打断、其一因我 `-run` 模式漏名当场补跑)。
   - 唯一红=`TestCollieNativeProxyDockerBoundary/{codex,claude-sdk}`=**独立缺陷 DEFECT-005**（父提交 `04cd6b2` 无本改动同红同签名 `PARENT_EXIT=1` 实证非本票引入；见 `evidence/A03/DEFECT-REGISTER-005.md`）。
2. **修复面负控（弱守卫，untagged 全平台）**：`mc/verbs/sourcegit_argv_test.go` —— argv 注入假 git（`sourceGitBin` 文内既设测试缝，本票首用）断言：旗 `safe.directory=<abs>` **先于子命令**、值绝对、**非 `*`/非相对**；相对 dir 归一为绝对 cwd；旁路源守卫要求 `onboardprojectgit.go` 全部非咽喉 `gitOutput` 调用携 `safeDirectoryArg`（计数不涨即红）。三测 PASS。
3. **被钉不变量未破**：`TestReadOnlyInspectionsOpenThroughTheSharedPath`（脊读共享路径源守卫）、A27/A28 的快照入口白名单守卫复跑绿；`mc/verbs` 全包、`mc/check.sh` 全道 `MC_EXIT=0`（26 包/0 FAIL/0 UNVERIFIED）、`resident` 774/0 `RES_EXIT=0`。
4. **回归（A29 未破相邻面）**：`update_docker` `UDOCKER_EXIT=0`（DEFECT-003 闭合面完好）；`docker_boundary` `BOUNDARY_EXIT=0`（89.9s，含本票新文件共存）。
5. **扫描门**：seal `6466a8a535ddaa89609df426123b3031166be4d362fcd3d7f5c82f55f9eb7f80`，184 条经 blame 基准 join **落本票改动行 0**（阳性对照=join 识出我方 148 行证有效；`sourcegit.go` 1 条落上游存量 `exec.Command` 行 blame=9dbc043=argv 无 shell 误报家族第 N 例）。静态边界，coverage 非全量，**不宣称项目安全**。

## 未做 / 边界（如实）
- **bespoke 双向容器哨兵未入库**（工具冲突三度拦，全文存 `evidence/A29/blocked-boundary-test.txt` 待裁工件）：其"pre-fix 必红/post-fix 必绿"永久哨兵价值由 e2e 端到端链**部分替代**（更强但非该单点的常驻覆盖）；补登与否归所有者/工具侧。
- 生产宿主上 onboard/repair 路径（同 uid，git 属主检查本不触发）无已知暴露，逻辑推导非实测，未列 UNVERIFIED 债（非本票修复面）。
- `docker_e2e` 定向机/CI 复跑：Q8 声明本体收归 Authority，A29 后本机已有效；道时预算 30m→实测需 ~40m 属测试域呈报（未擅改 AGENTS 数值）。

- reviewed_by: host-agent（执行者兼任复核声明沿用；Integrity 未做，不宣称）
- verdict: **A29=DEFECT-004 修复闭合（e2e 链级 16/17 实证，唯一红=独立 DEFECT-005）**；EVIDENCED 转写随 AM 登记轮，义务完成判定仍归 F0a Exit+治理链。
