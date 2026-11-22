# A23 验证记录 #1（TM §7.1 · launchd 停止循环改 list-驱——DEFECT-REGISTER-001 闭合）

- verification_id: **A23-V-001**
- target_component: `redeploy.sh` Darwin 停止段（旧按 `~/Library/LaunchAgents` plist 文件枚举 bootout，而等待/数据库面按 `launchctl list`——plist 被手删但单元在载⇒永不 bootout⇒等待耗尽 fail-closed 楔死，P2 可用性面）
- tested_version: 票支 `shz/TA23` @de3a644（两提交 dbd3645+de3a644），基线 `7c6524e`；改动=`redeploy.sh`（+24 行面，list-驱+注释）+`redeploy_test.sh`（+51 行面）+本票证据件
- 判据（票行 F0b）：两情形用例（bootout-race + plist-缺失-但在载）、错误矩阵转写**重钉非放宽**、净机往返

## 结果（逐件真实读取）
1. **正控全套**：`sh redeploy_test.sh` `RT_EXIT=0`——含**新用例**"plist removed by hand but the units are loaded"（两 bootout 皆发+部署完成+哨兵清除；**旧码此态必楔死**）；race/bootout-stuck/错误矩阵 6 场景全绿。
2. **负控**（`evidence/A23/negative-control.txt`，对被删的 pre-fix worktree `7c6524e:redeploy.sh` 实跑）：`OLD_STATUS=1 / bootout_calls=0 / WEDGE MESSAGE PRESENT`——缺陷在旧码复现、新码消失=**判别性双向证明**（marker-needs-negative-proof 纪律兑现）。
3. **转写重钉逐条可审计**（diff-plan §1）：假 launchctl 如实报两 label+absent 语义补模型；happy/rerun 以 LOADED_FILE 建"真在载"（替代旧 plist-存在盲 boot=缺陷在测试面的镜像）；`list_fail` 按新调用基数重标（poll 2→3、image 3→4）；**矩阵断言本体零放宽**（错误仍全拒、脊/二进制仍不动、"already stopped" 判别仍红绿两侧钉死）。
4. **合并前全道**：`mc/check.sh` `A23_MC_EXIT=0`（26 包/0 FAIL/0 UNVERIFIED；shell 家族含于其内）。
5. **扫描门=范围外（如实，不假称背书）**：seal `0fb23cda…f00c` coverage 1527 文件不含 `redeploy.sh`、全树零 `.sh`⇒"落行 0"平凡成立无干净含义；**新纪律入册：扫描门按语言覆盖分域，shell 票防线=其测试族+负控**。历轮我未查此覆盖边界（Go/TS 票 changed-file 有 finding 可对照，掩盖了问题）——首次触 shell 票即暴露，纠正于本件。
6. **观察项销账**：同型 plist-glob 全仓不存在（`dev-reset.sh:214` 本就 list-驱；`uninstall.sh` 无停止循环——仅查同病，不扩面）。
7. **自案两条入册**（diff-plan §1/§0）：先改后列（顺序偏差申报）；重钉中 printf 参数错位红一次（自纠）。

- reviewed_by: host-agent（agent E1；非独立复验）
- verdict: **DEFECT-001=FIXED（闭合指针）；A23=EVIDENCED**。回滚=revert 两提交（纯 Darwin shell 路径，无连带）。
