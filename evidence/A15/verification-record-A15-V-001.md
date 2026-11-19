# A15 验证记录（TM §7.1 全字段 · 执行事实，不构成义务级判定）

- verification_id: A15-V-001
- target_component: docs/facts/lock-inventory-001.md（跨层互斥域盘点：K1-K4 对照 + N1-N18 新域 + TS 进程内串行 + 表二判定 + F1-F3 新发现 + G1-G5 缺口）
- tested_version: shz/TA15@b5978a5（merge shz/dev=Already up to date）；集入后 dev tip 11a8aed；盘点对象代码=b22df40
- environment: MacBookPro darwin 24.6.0 arm64；只读扫描（Explore 子代理 93 次读检 + 主代理 4 处原文抽查）；blcheck 门禁 go1.25.12 构建件
- attack_scenario: 表二 4 反例扫描=人工跨层持锁路径检查（禁 grep-only）；复现=按 §1 表逐 file:line 开卷核对，抽查锚 [SP] 四处（dispatchverb.go:199-209 / attachment_spine_rpc.go:175-235 / project_delete.go:310-341 / connectionowner.go:26-42）
- expected_result: 全互斥域登记+K 关系声明（表二 3）+表二 1/2/4 判定+缺口 UNKNOWN 明列（禁静默）
- actual_result: 达成。判定：表二-1/-2 observed-PASS（非全局证明，G5 声明）；表二-4 无闭合死锁环 observed，N18 活性=UNKNOWN（不洗白）；新发现 A15-F1（RPC 旁路绕过 authorizeOperatorTransaction，主代理亲证[SP]）、F2（onboard/restore 依赖运维时序）、F3（flock 跨无界外部活动=设计选择）。blcheck 门禁 0 违规 exit 0
- evidence_ref: evidence/A15/{blcheck-a15.ndjson, verification-record-A15-V-001.md}；正文 docs/facts/lock-inventory-001.md（已入库，内容摘要见下）
- evidence_digest: lock-inventory-001.md 内容摘要由所在提交 b5978a5 锚定（git 版本权威=§1.2 完整性前提条款）
- test_script_digest: 无独立脚本（人工读检件；方法=表二 4 命令本身）
- execution_id: A15-run-01
- supersedes_verification_id: 无
- verdict: PASS（作用域=b22df40 代码态+b5978a5 文本；G1-G5 关闭前**禁**升格为"锁序已验证"或"无跨层风险"；代码迁移后（A09 等）本盘点须复跑）
- reviewed_by: host-agent（执行者兼任复核者，显式声明；子代理报告经 4 点抽查未全量复审——Integrity Reviewer 可按 E1-ag 行逐条复核）
- verified_at: 2026-11-19 20:38 (+0800)
- 边界: 扫描义务闭合≠规则生效。表二 1-3 转强制、F1 并入 A12/G-F 声明、F2/G1-G4 的 RR 处置——全部为治理动作，归所有者/AM。
