# A08 验证记录（TM §7.1 全字段 · 执行事实，不构成义务级判定）

- verification_id: A08-V-001
- target_component: R1 品牌文档层——README/LICENSE/AGENTS/SECURITY/INSTALL/GIT-DELIVERY/CHANGELOG/failure-handling-spec prose 改名 + docs/BRAND-WHITELIST.md 枚举
- tested_version: shz/TA08@1e51ed6（含 9dc1059 改名 + 回退提交；merge shz/dev=Already up to date，即合并后提交）
- environment: MacBookPro darwin 24.6.0 arm64；mise go 1.25.12；无网络需求；配置=仓库默认（docs-only 变更 + 门禁套件）
- attack_scenario（可复现）: ① `grep -ri culler` 全仓清点→逐命中对 docs/BRAND-WHITELIST.md 分类（5374 命中全部落表：A 永久/B R2 清除/C R2 末尾/D 改写规则；根文档残留终态 README 5/AGENTS 7/SECURITY 2/INSTALL 38/GIT-DELIVERY 21/CHANGELOG 10/toml 1/spec 1，逐条=B/C 或致谢历史 A）② `./release/check.sh`（CHANGELOG 结构约束——首跑红：`## Fork identity` 违反"首标题须为版本"，按约束改正文段后 116/116 绿）③ `mc ./test.sh ./config/ -run '^TestTrackedAndEmbeddedExamplesAreIdenticalAndValid$' -count=1`（config.example.toml 与内嵌副本逐字节配对——首改触红，**回退**，全文件归 B 类至 A09）④ `./mc/check.sh` 全快线（exit 0）⑤ blcheck 门禁 tickets=54 violations=0 exit 0
- expected_result: 文档 prose=S-Horizon；一切描述当前真实行为的标识符零改动；全部门禁绿
- actual_result: 如上全绿；两处首跑红均为**门禁正确拦截我的越界编辑**，按"生产约束优先"处置（一修文档形、一全回退）；dashboard/logo/hero 资产替换未做（需设计输入+越授权禁面），挂白名单 B 类交 A09/资产单——已知余项，非静默遗漏
- evidence_ref: evidence/A08/{blcheck-a08.ndjson, residual-inventory.txt, release-check.txt, mc-check.txt(首跑含红), mc-check-final.txt(绿), verification-record-A08-V-001.md}
- evidence_digest: README=b68e31ce505bc4e94f420f28a1e519ee36a588b80d73436c6122b5d5221f569c; BRAND-WHITELIST=b9b40eb732b6f3f5965953c816f553378905b60829c904fd174030166ec37f96（关键产物内容摘要；全套文件摘要同法）
- test_script_digest: 门禁脚本均为仓库既有（release/version_test.py 等，未改一字）；变更清单见 git TA08 三提交
- execution_id: A08-run-01
- supersedes_verification_id: 无
- verdict: PASS（作用域=本 commit+本环境；A09 落地后须按白名单 B/C 表复跑 grep 判定"仅剩 A 类"）
- reviewed_by: host-agent（执行者兼任复核者，显式声明；Integrity Reviewer 独立复核未做，不宣称）
- verified_at: 2026-11-19 20:12 (+0800)
- 边界: 本记录=工单 EVIDENCED 执行事实。R1 文档面义务之 IMPLEMENTATION_VERIFIED 归 AM 治理判定；且**R1 整体（含资产面）未闭合**——A08 工单覆盖=授权 §3 允许面的文档改名+白名单，资产替换余项在案。
