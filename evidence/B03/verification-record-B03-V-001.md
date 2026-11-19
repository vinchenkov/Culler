# B03 验证记录（TM §7.1 全字段 · 执行事实，不构成义务级判定）

- verification_id: B03-V-001
- target_component: F0b-1 序列化设计件——docs/design/f0b-serializer.md + docs/design/vectors/{vectors.json, reference_encoder.py}（30 向量+推导器）
- tested_version: shz/TB03@762a032（merge shz/dev=Already up to date）；集入后 dev tip e2ca459；无镜像
- environment: MacBookPro darwin 24.6.0 arm64；python3（stdlib-only，`-I` 隔离运行）；无网络；blcheck go1.25.12 构建件
- attack_scenario（可复现）: ① `python3 -I reference_encoder.py` ×2 于 B03 worktree + 第 3 次于 dev tip——三次输出同一套件摘要 `1a582fba5ce4b25db7894b8613692010d32f270f9b90a5382bbfa44b6526a5dc`，git status 无漂移（推导确定性）② 套内语义断言（脚本内嵌 assert，任一破坏即非零退出）：V1a/b/c 三摘要互异（词法存活）；V2a≠V2b（缺失≠null）；V3a=V3b 且输入原文相异（NFC）；V4a=V4b（声明序非书写序）；V4c 字节钉死非字典序；V5a≠V5b（版本入哈希域）；V6a/b+V7a-e 全为 expect_error（⑥与文法守卫）；F-anthropic NULL≠ABSENT ③ blcheck 门禁：tickets=54 violations=0 exit=0（B03 无依赖，EVIDENCED 态合法）
- expected_result: ①-③ 如上；AM §4b 五类最小集**齐备**（⑧"未定形"解除的前提=本件获 F0b-3 批准——批准前不得宣称摘要算法已定形）
- actual_result: ①②③ 全部达成；执行中发现并修复 1 个自身缺陷：ENVELOPE/SCHEMAS 第三列语义倒置（若未测出=必需字段缺失被静默 ABSENT、required-NULL 混入）——由 V1 关系断言在生成期当场抓住，修正后全断言重过。**该缺陷本身即本向量套件价值的自证**
- evidence_ref: evidence/B03/{blcheck-b03.ndjson, verification-record-B03-V-001.md}；套件本体在版本库内（vectors.json 即证据产物，摘要在上）
- evidence_digest: vectors.json=1a582fba5ce4b25db7894b8613692010d32f270f9b90a5382bbfa44b6526a5dc（内容摘要，重跑可复现）
- test_script_digest: reference_encoder.py 随套件提交，其字节由所在 commit 762a032 锚定
- execution_id: B03-run-01
- supersedes_verification_id: 无
- verdict: PASS（作用域=本 commit+本环境；**declared-pending 家族**（lifecycle/transition/lease/migration/aggregate_open/activity/signoffs）不在本 PASS 覆盖内——它们的字段表未注册、无向量，AM §4b 语义下这些族=declared 非 verified；跨实现符合性（第二实现比对）在 B07/C03 实施时由 vectors.json 承担验收）
- reviewed_by: host-agent（执行者兼任复核者，显式声明；Integrity Reviewer 独立复核未做；F0b-3 批准确认待治理主体）
- verified_at: 2026-11-19 20:26 (+0800)
- 边界: 工单 EVIDENCED ≠ 义务完成。本件的规范效力待 F0b-3 批准；"DC §3.4 未被修改"由 anchors.bind 漂移检查机械保证（DC 哈希与 §1.3 登记一致，blcheck 零违规即含此证明）。
