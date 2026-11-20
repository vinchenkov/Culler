# A13 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A13-V-001
- target_component: G-B 设计件——docs/facts/g-b-envelope-design.md（PROPOSED）+ DEV-BASELINE A13 行同步
- tested_version: shz/TA13（基线 shz/dev@4c64eef）；**docs-only 波次：无生产代码改动，代码扫描门不触发（如实声明）**
- environment: 静态追链（E1 直读：mc/verbs/verbs.go:152-160、resident/src/effects.ts:625/:1341-1360、operator_bridge.go:175、verbs/operator_requests.go:83）+ AM/DC-FACTS 既有登记行核对
- attack_scenario: 设计件的事实正确性=反向核查"提案声称的缺口存在"：pipeline run.json 构造确无 release_build_id（effects.ts:1341-1376 全键集逐行读至写点，确认缺失非视野疏漏）；agent 消费点确无比对（grep 全消费者面）；collie 回退缺陷确为 fail-open（`?? "development"` 语义直读；同族宽松式另见 evaluation 记录 `?? ""`，非脊写面，注记不改提案）
- expected_result: 设计件所依据的每条现状陈述可指到行号；提案不新增权威、不触 schema.sql、与 matched-pair/更新停摆既有语义合拍；wire 批准权归所有者声明在文
- actual_result: 三条现状事实全部行级复核成立（并新发现次生缺陷=collie 生产者 fail-open 回退，已登记）；提案=必填生产+装载咽喉全等+漂移守卫+更新演练四件套，拒绝 envelope_version 新键并给出理由（第二权威无新增信息）；§3 验收映射可直接作为实施票测试面
- evidence_ref: docs/facts/g-b-envelope-design.md §1（每条带 file:line）
- evidence_digest: 设计件 @TA13（git 锚）
- test_script_digest: 不适用（无代码执行面；实施票将建 §3 所列测试）
- execution_id: A13-run-01
- verdict: PASS（作用域=设计件的事实正确与内部一致性；**G-B 义务完成与实施就绪均不属本件**——待 wire 批准+实施票+AM §4b G-B 验收）
- reviewed_by: host-agent（兼任声明；提案不自批）
- verified_at: 2026-11-20 (Asia/Shanghai)
