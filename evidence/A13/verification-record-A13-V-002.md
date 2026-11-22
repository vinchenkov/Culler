# A13 验证记录 #2（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A13-V-002（G-B 实施轮；V-001=设计件 A13-DEL）
- target_component: 运行信封构建身份绑定（IMPL-AUTH-003 主款六-b/附七五条件全满足后 CODED，绑 g-b-envelope-design.md `94a96a0a…8e4a`，所有者 2026-11-22"授权 A13 进码"）。**双端最小面，无 schema.sql 触面，无新增 envelope_version 键（条件 4：不超获批形状）**：
  - 生产端 `resident/src/effects.ts`：pipeline run.json 新增 `release_build_id`、collie 去 `?? "development"` 回退，统一经 `requiredReleaseBuildId`——缺失=effect 抛错不 spawn（fail-closed）。
  - 消费端 `mc/verbs/verbs.go LoadIdentity`：agent 分支强制 `id.ReleaseBuildID == selfReleaseBuildID`，先于任何 spine 打开域级拒绝；host 分支（无 run.json）与 operator-forward 分支逐字不受影响；self 空=包测试态跳过（非生产态）。`mc/cmd/mc/release_bind.go`（无 tag，prod+test_fake 两构建皆注册 self）。
- tested_version: shz/TA13impl；合并态以 AM 同轮 dev 提交锚定。
- environment: darwin 24.6.0 arm64（go 1.25.12 / bun 1.3.12）。
- attack_scenario（执行清单）：
  1. `./mc/check.sh`：exit 0（gofmt/vet 各 tag、全 Go 测试含新 release_bind loader 用例 + drift 守卫 + verbs/cmd-mc/deployment 全量）。**过程如实**：cmd/mc 全量在并发下耗时显著，等待其终判并入本轮。
  2. `resident/check.sh`（**两轮，如实记录完整链**）：首轮窄测 effects 152/0 绿后即并 dev——**合并态全 resident 道暴 5 例 runner exit 2**（split-brain 脚本 Worker 边界×2 + wiki-crossing 等×3）：真因=**我自己的不一致**——我给共享 `testConfig` 加的默认 `releaseBuildId="test-release"`，而测试构建的真 mc 二进制无 ldflags 覆盖 self="development"，producer 按 config 写 "test-release"→loader 判混版**正确拒绝**（窄 effects 系进程内不 exec 真子进程故测不到）。**处置**：先 reset 撤 A13 出 dev（never-merge-red，dev 保绿），临时诊断捕获 stderr 定根因，再把 testConfig 默认/脚本 worker 伪造信封/fixed-schema golden 三处统一对齐 "development"（=二进制实身），重并后**全 resident 道 774/0 exit 0 + 全 mc/check.sh exit 0**。教训入册=**第 4 次窄测绿≠全道绿（前三=A16 字节臂、R1 假阳性、Q7 冒烟），且本次含我自纠"先并后验"的违规：此后并 dev 前必跑全 lane**。
  3. 消费端正反（`mc/verbs/release_bind_test.go`）：matched-pair 放行 / 混版拒 / 缺字段拒 / self 空跳过 / host scope (nil,nil) 不受影响。
  4. 漂移守卫（`mc/deployment/release_bind_test.go`，随 log-event registry 同族源守卫）：effects.ts 禁 `release_build_id: config.releaseBuildId ??` 复活 + 两处 `requiredReleaseBuildId` 齐 + choke 在位。
  5. 扫描门：seal `8401348b…`（485 该 worktree 基线量），程序化核对**落 A13 改动生产行 0 条**（静态边界，不宣称安全）。
- expected_result: 旧信封/混版 run 容器以新二进制写脊=显式 blocked（非静默腐化）；matched-pair 正常态永不触发。
- actual_result: 双端机制 + 单元正反例 + 漂移守卫达成；**且混合构建拒绝在 split-brain 真实 subprocess 场景被意外实测触发（以我自造的不一致为燃料）——拒绝行为、文案、传导链全部与设计的"显式 blocked"一致，事后看是该 bind 在本会话收到的最真实的一次端到端演练**。
- 残余（不吞）：**更新演练面**（升级窗口旧 run 重启=显式 blocked）随 F0a / update_docker lane（设计 §3.4 明示与既有停摆机制合拍，本票不新造窗口）；e2e 真容器（test_fake_routing 构建的 agent mc，self=ldflags releaseBuildID）由匹配夹具满足，本机 e2e 运行域=定向机/CI（Q8 声明）。
- evidence_ref: 本记录 + 上述测试文件；设计件 A13-V-001；批准事件=AM v0.1.65 Q9 + 进码授权 AM v0.1.66；合并链=dev 413880a→dcdd82b(修复)→791bdd4
- reviewed_by: host-agent（执行兼任复核声明沿用；Integrity 未做，不宣称）；verified_at: 2026-11-22 (+0800)
- 边界: 合并态双全道（mc/check.sh exit0、resident 774/0）+ 扫描 0-on-lines → **A13 台账转 EVIDENCED**（附七 cond1–3 清+cond4 实施在获批形状内；义务完成判定仍随 F0a Exit+Authority——Q3 任命后 Authority=所有者本人，其逐项接受仍须明示）。
