# A17 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A17-V-001
- target_component: C-i BlocksWork 边界 ADR（**PROPOSED**，批准权在部署所有者）+ C-ii RR 候选材料包（**备料**，无接受无签署，禁打包条款按 §4b 原文写入）
- tested_version: shz/TA17（两提交：docs 交付 + 台账 EVIDENCED）；合并 dev@fae11e2
- environment: MacBookPro darwin 24.6.0 arm64；文档单——无编译面；证据面=引用代码坐标全部亲读（BlocksWork 8 入口点 grep+读、A15 N18/D31 交叉引用、DC §5 G-C 原文）
- attack_scenario: ① `./mc/check.sh` 合并前置全链（文档改动零红=确认无意外耦合，exit 0/0 FAIL）② blcheck 门禁（A17 无依赖、EVIDENCED 态合法）exit 0 ③ 材料事实核验：三条暴露路径逐条对 file:line（引用自 A15 盘点/DC-FACTS，本单二次核对 RPC 白名单与 journal 竞态两处的原文行）
- expected_result: C-i ADR 不写成既成契约；C-ii 不含任何接受语义；两文件自缚边界条款齐备
- actual_result: 达成。RR 候选拆分含一条**反向建议**（候选 2 不建议走接受——有低成本闭合路径，先实施后评估），供 Authority 参考不约束其决定
- evidence_ref: evidence/A17/{mc-check.log, verification-record-A17-V-001.md}；正文 docs/facts/a17-blockswork-boundary-adr.md + a17-rr-c-ii-material.md（dev@fae11e2）
- evidence_digest: 两份正文由所在提交 git 锚定；lane 日志文件字节
- test_script_digest: 不适用（文档单；快线为仓库既有）
- execution_id: A17-run-01
- supersedes_verification_id: 无
- verdict: PASS（作用域=材料完成度本身。**C-i 未批准、C-ii 未接受、G-C 义务未闭合**——三者均为待办治理动作，本记录不构成任何一项）
- reviewed_by: host-agent（执行兼任复核，显式声明）
- verified_at: 2026-11-20 03:10 (+0800)
