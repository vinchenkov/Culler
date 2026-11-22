# A13 验证记录 #2（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A13-V-002（G-B 实施轮；V-001=设计件 A13-DEL）
- target_component: 运行信封构建身份绑定（IMPL-AUTH-003 主款六-b/附七五条件全满足后 CODED，绑 g-b-envelope-design.md `94a96a0a…8e4a`，所有者 2026-11-22"授权 A13 进码"）。**双端最小面，无 schema.sql 触面，无新增 envelope_version 键（条件 4：不超获批形状）**：
  - 生产端 `resident/src/effects.ts`：pipeline run.json 新增 `release_build_id`、collie 去 `?? "development"` 回退，统一经 `requiredReleaseBuildId`——缺失=effect 抛错不 spawn（fail-closed）。
  - 消费端 `mc/verbs/verbs.go LoadIdentity`：agent 分支强制 `id.ReleaseBuildID == selfReleaseBuildID`，先于任何 spine 打开域级拒绝；host 分支（无 run.json）与 operator-forward 分支逐字不受影响；self 空=包测试态跳过（非生产态）。`mc/cmd/mc/release_bind.go`（无 tag，prod+test_fake 两构建皆注册 self）。
- tested_version: shz/TA13impl；合并态以 AM 同轮 dev 提交锚定。
- environment: darwin 24.6.0 arm64（go 1.25.12 / bun 1.3.12）。
- attack_scenario（执行清单）：
  1. `./mc/check.sh`：exit 0（gofmt/vet 各 tag、全 Go 测试含新 release_bind loader 用例 + drift 守卫 + verbs/cmd-mc/deployment 全量）。**过程如实**：cmd/mc 全量在并发下耗时显著，等待其终判并入本轮。
  2. `resident/check.sh`：exit 0，effects 152/0——含新"the G-B producer bind"拒绝例（releaseBuildId 缺失→spawn 拒、无 run.json 落盘、无 docker create）；**首轮 77 失败=测试 harness `testConfig` 从未设 releaseBuildId（types 里可选⇒运行时 undefined），新必填如设计抛错**；补 harness 默认 "test-release"（模拟真实已配置部署）+ 固定 schema golden 加 `release_build_id` 行（=获批 wire 形状变更的正例转写，非放宽）。
  3. 消费端正反（`mc/verbs/release_bind_test.go`）：matched-pair 放行 / 混版拒 / 缺字段拒 / self 空跳过 / host scope (nil,nil) 不受影响。
  4. 漂移守卫（`mc/deployment/release_bind_test.go`，随 log-event registry 同族源守卫）：effects.ts 禁 `release_build_id: config.releaseBuildId ??` 复活 + 两处 `requiredReleaseBuildId` 齐 + choke 在位。
- expected_result: 旧信封/混版 run 容器以新二进制写脊=显式 blocked（非静默腐化）；matched-pair 正常态永不触发。
- actual_result: 双端机制 + 单元正反例 + 漂移守卫达成。
- 残余（不吞）：**更新演练面**（升级窗口旧 run 重启=显式 blocked）随 F0a / update_docker lane（设计 §3.4 明示与既有停摆机制合拍，本票不新造窗口）；e2e 真容器（test_fake_routing 构建的 agent mc，self=ldflags releaseBuildID）由匹配夹具满足，本机 e2e 运行域=定向机/CI（Q8 声明）。
- evidence_ref: 本记录 + 上述测试文件；设计件 A13-V-001；批准事件=AM v0.1.65 Q9 + 进码授权 AM 本批
- reviewed_by: host-agent（执行兼任复核声明沿用；Integrity 未做，不宣称）；verified_at: 2026-11-22 (+0800)
- 边界: CODED→EVIDENCED 随扫描门 + cmd/mc 全量终判入本记录尾注；A13 票状态待登记轮转 EVIDENCED（附七已激活，非未激活）。
