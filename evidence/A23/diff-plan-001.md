# A23 diff-plan #1（实施轮：launchd 停止循环改 list-驱——含执行顺序如实申报）

**⚑ 顺序如实申报（在册"先列后改"义务）**：本票 W1 期首版改动**先于**本计划文件落地（上一轮直接修+重钉测试到半途收口），本文件在**合并前**补齐；未提前列的原因=按 DEFECT-REGISTER-001 的登记即蓝图（修复形已在票行/登记件写死"launchctl list 为准，与 Linux 支对齐"）。此偏差如实登记；若波门判"补列=不合规"，回撤=1 提交。

## 1. 改动（正清单）
- `redeploy.sh` Darwin 停止支：plist-glob → **`read_units` + `launchctl list` 第 3 列 label** 驱动 bootout；exit-113 "already stopped" 分支保留（竞态容错：读表与 bootout 之间单元消失）。注释写明与 Linux 支/wait 循环同源的理由。
- `redeploy_test.sh` 转写与建模（**重钉非放宽**逐条）：
  - 假 `launchctl` 的 list **如实报两在载 label**（真 launchd 有俩即报俩；旧 fake 只报 resident 一枚配合 plist-glob 掩盖了双源分裂）。
  - 假 `launchctl` bootout=3（absent 语义）时**同步移除 loaded 哨兵**（服务已不在⇒后续 list 不再报它——旧 fake 无此模型，因为旧码从不在 absent 态再查 list）。
  - happy/rerun：**补 `LOADED_FILE`=以"真在载"建模**（旧用"plist 文件存在"盲 boot——文件在≠单元在载，正是缺陷本体在测试面的镜像）；期望序按实际重钉（stop-branch 多一次 list、bootout 序=resident,dashboard）。
  - bootout-race：期望按实际重钉（busy 计数位形变了，"等清零才 build"语义保留）。
  - 错误矩阵 6 场景：`list_fail` 按新调用基数重标定（poll 2→3、image 3→4、database/preflight 不变）；bootout/bootout-check/absent 三场景补 loaded 哨兵（旧 glob 场景不需要，list-驱必须给在载源）。
  - reset 家族期望：旧期望含一次盲 bootout（plist 在、单元不在载——新码正确不 boot）⇒ 按实际重钉（去 bootout、+1 list）。**此条重钉中我犯过一次 printf 参数错位（删了 %s 没删 `"$(id -u)"`），红一次，已修**——记录在案。
  - **新用例**（票面 F0b 本体）："plist removed by hand but the units are loaded"⇒两 bootout+完成部署（旧码此态必楔死）。
- 负控：`evidence/A23/negative-control.txt`（存档脚本+执行结果 `OLD_STATUS=1 / bootout_calls=0 / WEDGE MESSAGE PRESENT`，对被删的 pre-fix worktree 跑）。

## 2. 证据
- `redeploy_test.sh` 全套 `RT_EXIT=0`（含新用例）；负控如上（两侧闭合=DEFECT-001 修复的判别性证明）。
- `mc/check.sh` 全道：`A23_MC_EXIT=0`（26 包 ok/0 FAIL/0 UNVERIFIED；重跑较慢系负载非异常，进程与 `verbs.test` 存活经亲验）。
- update_docker/boundary/docker_e2e 不涉（纯 Darwin shell 路径）；Go/TS 零触碰。
- **扫描门如实定性=范围外（不是"0 落行干净"）**：seal `0fb23cda008d9f8a3ec7812de715a255ebe2e2433484068b6a5daa0077df00c`（485 条，`partial/inconclusive`）。coverage 1527 文件里**不含 `redeploy.sh`、全树零 `.sh`**⇒扫描器不解析 shell——"落 A23 行 0"**不携带干净信号**（阳性对照失效：changed-file 集内本就无 finding，0/0 平凡成立）。历轮 Go/TS 的 blame-join 方法在 shell 票上**不可套用**，此结论入纪律面：**扫描门按语言覆盖分域；shell 票的既有防线=其自身测试族+负控，不假称扫描背书**。本票实际防线=`RT_EXIT=0` 全套+新用例+负控（OLD wedge 复现）。

## 3. 边界
- fail-closed 姿态不变（错误场景全部仍拒部署、不动脊/二进制——矩阵断言逐条原样，无一放宽）。
- Linux 支零触碰（A03 遗产，本就 list-驱）。
- `dev-reset.sh`/`uninstall.sh` 的同型停止循环**未查**——登记为后续观察项（若同病=独立小票；本票不扩面）。
