# A16 验证记录 #3（TM §7.1 · docker_e2e 本机轮 + 自我缺陷修复；执行事实，不构成义务级判定）

- verification_id: A16-V-003（补充 V-001 本机段；不替换，转写=把 V-001 中"docker_e2e 未跑"残余项按实况更新）
- target_component: A16 spawn/envelope 面在真 docker e2e/boundary 链的端到端表现；及本轮自我抓出的字节臂空闲成本缺陷。
- tested_version: shz/dev@1a2c9d8（含本票采样修复 1e815cf + A14 续轮）；夹具=本机 build.sh 产 mc-fake-e2e/mc-real-e2e；对照父提交=8dce77c（A16 生产码之**前**，同法跑同用例）。
- environment: darwin 24.6.0 arm64，Docker Desktop 29.8（data-root virtiofs 呈现）；本机外网对本 registry 层曾两试不稳（apt/npm）→ 重试策略=有界缓存重试（attempt-2 成）。

## 执行清单与实测退出码
1. **`boundarydocker` 道**：第三跑（夹具镜像齐备后）**`ok mc/boundarydocker 78.5s / exit 0`**——A16 创建面（ulimit/log-driver/表解析）真 docker 边界全绿。首轮 exit 1 已定责=**夹具镜像缺失窗口**（原文 `Unable to find image 'mc-fake-e2e'`），非产品。
2. **自我缺陷修复（本轮真收获）**：e2e 日志暴露 **A16-V-001 未见的我方回归**——字节预算臂使 prune 的"界内早退"消失 ⇒ 每 tick 全量读 run 表+`docker ps -a`+遍历 ended 体积，同条 unrecorded 告警刷 **91 次**。修复=`RUN_LOG_BYTE_ARM_EVERY_TICKS=8` 采样（计数臂每 tick 免费不变），回退守卫测试钉死"非采样 tick 不触预算读/run-list"；resident 全道 **773/0**。教训：新增强制面须算空闲态每-tick 成本，早退路径被关掉=空闲板被拖成重负载。**登记 V-001 §扫描/道段的"resident 772/0"为当时事实，本修复使其升 773/0 并消除空闲退化——非掩盖，是链上递进。**
3. **`docker_e2e` 道**：全树跑 exit 1，**三处失败全部二分定性为非 A16**：
   - `TestWalkingSkeleton`：235s FAIL（265 空转 tick，Strategist run 从未起，零 create/envelope/spawn 拒痕）。**决定性对照=父提交 8dce77c（A16 生产码之前）自重建夹具跑同用例 = 274s 同态 FAIL**；共有签名=boot 健康（canary_passed+projector_live）但 dispatch 产不出活。**结论=本机 Docker-Desktop 环境既有问题，非 A16 回归。**
   - `TestCollieNativeProxyDockerBoundary`（codex+claude-sdk 子界）：collie 容器从未创建，同上签名（wake 依赖 run 先被 dispatch，骨架不动它就不动）。
   - `TestSpineLockDomainGuardDockerBoundary`：**安全性质成立**（host-bind spine 真被拒），但消息报 `fstype=virtiofs source=virtiofs2`、测试钉 `fakeowner`——该测试注释明言钉"一条 Docker Desktop 生产发现（host bind 呈 fakeowner）"，本机 Desktop 已改呈 virtiofs。**=测试断言与宿主版本耦合的漂移，非守卫失效、非我方改动（未碰守卫/未碰该测试）。**
   - 定性总：docker_e2e 在本机=**既有环境不适**（与 S2 早期"darwin 无 mc-prod 镜像、boundary 受 launchd 守卫"同类挂账的历史延续）；**A16 资源/掩码/看门狗/envelope 强制的真实端到端证据 = 定向机矩阵（V-002 9/9）+ 本机 boundarydocker（ok）+ resident/dashboard 单元（绿）**，不以本机 e2e 红为反证，也不为其强行治本机环境（越票）。

## expected_result / actual_result
- 期望：本机 e2e 至少骨架绿，为 A16 端到端补一层。实际：本机 e2e **不能作 A16 的正/反证**（环境既有红，bisect 证明）；A16 自身缺陷（字节臂空闲退化）经此道**被真实抓出并修复**——这本身就是 e2e 的价值（它抓到了单元和 boundary 都漏的空闲态退化）。
- 净证据账（A16）：真机矩阵 9/9（V-002，含 spine 零污染+控制面存活+inspect 见证）+ boundarydocker ok（本轮）+ resident 773/0 + dashboard 983/0 + 冒烟 313/0 + 扫描落改动行 0 条 → **A16 EVIDENCED 基础更Solid，且字节臂缺陷在 EVIDENCED 后被发现即修即测**（非静默）。

## 呈所有者的两项待批测试维护（执行侧不擅改他人钉选断言）
1. **`TestSpineLockDomainGuard` 的 fakeowner 钉**：现宿主呈 virtiofs，钉选应改为"钉**任一**非块设备 fstype 触发拒绝"而非死钉 fakeowner 字面，方随 Docker Desktop 版本稳。属**交付/测试域独立维护票**。
2. **docker_e2e 本机环境**：若欲在本机跑通，需先解骨架 dispatch 在本 Docker-Desktop 的产活问题（pre-A16 即红，与本票无关）；否则维持"e2e=定向机/CI 面"的既有挂账口径。

- evidence_ref: evidence/A16/（本记录、verification-record-A16-V-001/002、a16-resource-raw-rounds.tgz；bisect 对照=/tmp/a16-walk-pre.log 摘要入本记录）
- supersedes_verification_id: 无（V-001 本机段补强）
- reviewed_by: host-agent（执行兼任复核，显式声明；Integrity 未做，不宣称）
- verified_at: 2026-11-22 (+0800)
- 边界: 执行事实零义务提升；bisect 是**归属**判定，不判 walking skeleton 的根因修复（那属环境/交付域，越 A16 票面）；EVIDENCED 完成判定仍随 F0a Exit+治理链。

---

## 更正段 #1（2026-11-22 轮十追加；原§3 结论的归因半边作废）

**原句"结论=本机 Docker-Desktop 环境既有问题"被证伪**（所有者明言本机 Docker 健康；`evidence/A03/DEFECT-REGISTER-004.md` 给出产品级真因：`verbs/sourcegit.go sourceGitEnv()` 的 `GIT_CONFIG_NOSYSTEM=1` 连带关掉镜像系统配置的 `safe.directory=*` ⇒ 容器内 git 对外属主仓库拒读 `detected dubious ownership` ⇒ 播种投影失败 ⇒ run 不 birth ⇒ 骨架空转）。**本记录其余判定不受影响且部分反而加强**：
- "非 A16 回归" **成立且升级**——父提交同态复现恰证该缺陷**先于 A16 存在于上游存量代码**（`sourcegit.go` 族），二分事实有效、仅**归因**（环境 vs 产品）错了。
- "待批 2（维持定向机/CI 口径）" **改判**：本机并非不适域；A29 修复后本机即有效 e2e 运行域（Desktop 健康为前提，所有者声明在册）。Q8 运行域**声明本体**是否收回归 Authority。
- 原§边界句"那属环境/交付域"中"环境"二字作废，改"产品缺陷域（DEFECT-004/A29）"。

**纪律入册**：把红归因给环境前必须先做**单变量隔离实验**；"父提交同态复现"只排除我方回归、**不构成环境归因的充分证据**。本件是该教训的原始案卷（教训全文=AM v0.1.80 ③）。
