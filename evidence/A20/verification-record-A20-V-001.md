# A20 验证记录（TM §7.1 全字段 · 执行事实，不构成义务级判定）

- verification_id: A20-V-001
- target_component: tools/blcheck 依赖图检查器 + DEV-BASELINE §3 六列表格化改造
- tested_version: commit a287c2169254d84ccf68bfe88123315dcf6910ae（shz/TA20；merge shz/dev=Already up to date，测试提交即合并后提交）；无镜像（本地工具，非容器件）；go 1.25.12（mise 钉版）
- environment: MacBookPro / darwin 24.6.0 / arm64；无网络需求（stdlib-only：文件读写、sha256、JSON、regexp——无 exec/网络/SQL/凭据）；测试配置：`go test -count=1 ./...` + blcheck bind/check 全命令序列
- attack_scenario（可复现脚本引用）: §5a 三判据——① `cd tools/blcheck && mise exec -- go test -count=1 ./...`（自测+五规则 fixture：dep-unknown/cycle/ready-unmet/anchor+mapping-missing/rebind-required 各含正反例）② 同套件内 A20-EX 畸形输入组（5 列行/§3 散文声明/坏 depends token/空图/缺 ledger/未知状态——全部须显式 FAIL/VIOLATION/exit≠0）③ 真实快照：`/tmp/blcheck -graph docs/contracts/DEV-BASELINE.md -status tools/blcheck/status.json -bind tools/blcheck/anchors.bind.json -docs docs/contracts -root .`
- expected_result: 判据①②全绿；判据③ tickets=54 violations=0（修复前该图须被拒绝：tickets=0、54×VIOLATION、exit 2）；畸形输入零静默
- actual_result: 判据① 7/7 PASS（fresh -count=1）；判据② 含于套件全过；判据③ tickets=54 violations=0 warnings=2（A06 `(密钥面)`、A13 `同域` 边注释按规则保留并显式 WARN，边按真依赖处理）exit=0；bind 哈希与 AM §1.3 清扫登记互证（TM 950dbacc…、PS adf30f71…）
- evidence_ref: /Users/raistlin/Downloads/git/Ai/Culler/evidence/A20/{blcheck-before.ndjson, blcheck-after.ndjson, blcheck-final.ndjson, blcheck-before.stderr.txt, blcheck-final.stderr.txt, gotest-full-merged.txt, env-context.txt}
- evidence_digest: before=655802681822d803042d402205a620b4d7d63a273aea60989e5b7f045b6731b0; after/final=063baef0be21e918d51d68c47321e45b8ad48b5cf07db472cc579a8598e4a2c7; gotest=4f394ba2a7aaa267eb998b6d014628d27d712cfa76fe6f1daa9ec0d26e9ad7b9; env=fd4bf3b554e90d61ba067016d117059a260cb64862ea148cefcd3916cc1e2c68
- test_script_digest: adbe9df3706f8e5ff6533075db41c271be87bd6acf119c9dfd5831a729ee6821
- execution_id: A20-run-01
- supersedes_verification_id: 无
- verdict: PASS（作用域限定 §7.1 P1-2：仅对本 commit+本环境+本配置有效；DEV-BASELINE 或契约文件字节变化→rebind 流程即触发复测；UNKNOWN≠PASS——本记录不含 UNKNOWN 项）
- reviewed_by: host-agent（执行者兼任复核者——按 §7.1 显式声明兼任，无双人假象）；独立完整性复核（Integrity Reviewer）未做，不宣称
- verified_at: 2026-11-19 19:41 (+0800)
- 安全扫描状态（如实）: mimosa scan-2026-11-19T11-37-36.249Z-94d7b4119317（seal sha256:bb42e843…）completeness=partial、runStatus=inconclusive、blcheck 文件未入覆盖亦未入 findings——**不宣称新代码已通过安全扫描**；stdlib-only 属性为人工阅读观察（E1 人工），不替代扫描器
- 边界声明: 本记录=工单 CODED/EVIDENCED 执行事实；A20 对应义务（EXEC-1 落地）之 IMPLEMENTATION_VERIFIED 判定归 AM 治理动作（DEV-BASELINE §1 关闭规则），本记录不构成该判定
