# A10 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A10-V-001
- target_component: G-A live-schema 写门禁（verbs.OpenSpine 咽喉接入完整 CheckSchema）+ collie 故障注入迁移（schema trigger → 编译期 test_cli_faults 钩子）
- tested_version: shz/TA10@882aae6 → 合并 dev@a7f7f7c（EXEC-2：门禁与 collie 测试复验于合并 tip）
- environment: MacBookPro darwin 24.6.0 arm64；go 1.25.12（mise）；无网络无 Docker（快线本体）；docker_boundary/e2e **未跑**（Docker MCP 断连——见边界）
- attack_scenario（可复现）: ① `./test.sh ./verbs/ -run TestOpenSpineGate -count=1`——四例：装配 spine 写成功（正控）；**ALTER TABLE 加列而 meta.schema_id 保持诚实 → 仍拒**（owner 要求的"运行时实际加载≠静态声明"反证）；无 meta 空库拒；foreign schema_id 拒；拒绝后数据零变动 ② `go test -tags test_fake_routing,test_cli_faults ./cmd/mc/ -run TestCollie -count=1`——四个原子性用例失败注入门锁在目标 INSERT 前（事务内），错误串专属钩子（早失败不可能产生该串=位置证明），回滚断言原样保留，**新增**每例后续干净写成功 ③ 全链 `mc/check.sh`（分支态 exit 0；合并态以 ①②+门禁复验覆盖）④ 双向编译：无标签生产构建走 no-op 钩子文件、linux/arm64 交叉过
- expected_result: 门禁不弱化（对象+meta 双检保留）；注入不碰 schema；生产构建无故障开关
- actual_result: 全达成。首跑快线正确拦下 trigger×门禁冲突（3 测试红）——按裁定 A 处理测试手段而非门禁语义；钩子经 `cliTestBuildTags` 既有通道，零新增基建；hermeticEnv 通道核验（per-call env 后置生效）
- evidence_ref: evidence/A10/{verification-record-A10-V-001.md, g-a-coverage-matrix 已入库 docs/facts/g-a-coverage-matrix.md}
- evidence_digest: 提交 882aae6（实现+测试）与 a7f7f7c（合并）git 锚定；扫描（门槛义务）：mc/verbs 根 288/288 文件 parsed、37 发现**全部位于未改动存量文件**、A10 改动文件（collie.go/init.go/fault_hook_*）**零命中**；seal sha256:b4eaba984c6e899d30c6da171b75e5ed9979e567c0eb629125cd8ec37c29e543（runStatus 仍 inconclusive=工具保守性，同 SCAN-001 口径）
- test_script_digest: schema_gate_test.go + cli_test.go 改动由 882aae6 锚定
- execution_id: A10-run-01
- supersedes_verification_id: 无
- verdict: PASS（作用域=本提交+darwin 本环境；**显式 UNKNOWN/UNVERIFIED**：① attachment RPC 与 __system-state 的端到端容器行为依赖部署形态，docker_boundary/docker_e2e 未跑（环境断连，非通过）② G-D/G-E 覆盖合并判定归 A11（矩阵如实标 UNCOVERED）③ 37 条存量发现待分诊（非本单义务，上游事实面））
- reviewed_by: host-agent（执行兼任复核，显式声明；Integrity Reviewer 未做，不宣称）
- verified_at: 2026-11-19 23:12 (+0800)
- 边界: 工单 EVIDENCED ≠ G-A 义务 IMPLEMENTATION_VERIFIED；AM §4b G-A 行的验收判定归治理动作。R-01 声明已贴码（SchemaMatch⇏构建身份）。
