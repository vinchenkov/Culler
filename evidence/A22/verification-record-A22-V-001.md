# A22 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A22-V-001
- target_component: operator 围栏跨进程委托交接——docs/facts/f1-operator-fence-001.md 的 P1 剧本执行与 §2/U1 修订
- tested_version: shz/TA22（基线 shz/dev@7cd86be；生产代码零改动，交付=验证件+事实修订）
- environment: macOS arm64；`go test -tags 'test_fake_routing test_cli_faults'`；无 docker、无模型 token
- attack_scenario: 批准→提交竞态——请求在 decide 之后、写提交之前失去 active 态（真实撤销路径产出的 `uncertain`），委托子进程形态（宿主身份 + `MC_SPINE` 直开 + `__operator-forward` 全等代码路径）仍执行写入
- expected_result: 若围栏在委托形态失效→写落库（证实不生效）；若存活→提交前拒 "The operator request has no active execution." 且零副作用
- actual_result: **存活（改判）**。live 收据：写落库 + `activity` operator.request 审计行 ≥1；`uncertain` 收据：exit 1、消息匹配、projects.status 保持 active（零副作用断言）；伪造 id：拒且零副作用。委托 argv 交接形态单测双向钉死（置位全等重写为 `__operator-forward <id> -- …`；未置位不发明收据）。全道 `./mc/check.sh` 退出 0（gofmt/vet 各 tag/Linux arm64/shell 测试/全部 Go 测试）；`mc/cmd/mc` 全量包另跑一次显式留证：ok 323.6s。目标根扫描：scanId `scan-2026-11-20T01-43-46.862Z-e55f148ee28a`，seal `sha256:b9df43bc…19d8`，findings 0（扫描根=两新测试文件，与工作树字节一致 sha256 `59cbc16c…`/`9532e62c…`）
- evidence_ref: mc/cmd/mc/operator_fence_a22_cli_test.go + mc/cmd/mc/operator_fence_a22_test.go + docs/facts/f1-operator-fence-001.md（§2/§4-U1/§5-P1 修订）
- evidence_digest: 以 TA22 提交内测试文件 git 锚为准
- test_script_digest: operator_fence_a22_cli_test.go / operator_fence_a22_test.go @TA22
- execution_id: A22-run-01
- verdict: PASS（作用域声明：fast-lane 证明的是子进程**同形**代码路径与 argv 交接形态；真实 `docker exec` 往返传输面未在本件证明，属 A21 类边界证明范围。fact-rec §2 原"委托不生效"为初稿误读 verbs 包变量所致，本次以运行时证据改判；attachment RPC 旁路面与 U2/U3 矩阵归 A12，不在本件覆盖声明内）
- reviewed_by: host-agent（兼任声明；本记录不构成交付完整性通过，亦不构成 RR 接受）
- verified_at: 2026-11-20 (Asia/Shanghai)
