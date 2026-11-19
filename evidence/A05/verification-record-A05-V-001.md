# A05 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A05-V-001
- target_component: provider proxy 可配置监听地址（config `[surfaces.provider_proxy].bind` → proxy-bind 动词 → resident listen + 宿主 dialer），默认 loopback
- tested_version: shz/dev@d7357cb（EXEC-2 合并态；分支提交 5fb6ccd + d7357cb 修复；merge shz/dev=Already up to date）
- environment: MacBookPro darwin 24.6.0 arm64；mise go 1.25.12 / bun 1.3.12；无网络/无 Docker（快线本身）；实际 bind 探测经 `Bun.serve` 本机回环+网关 IP（192.168.50.170）验证
- attack_scenario: ① `go test ./config/`（TestProviderProxyBindIsLoopbackEmptyOrIPLiteral：默认/空/回环/网关/v6/通配 合法且解析正确；hostname/带端口/非法 IP/localhost 词 全拒）② `./test.sh ./deployment/ -run TestProviderAccessHTTPClient -count=1`（dialer 非 IP 字面量必拒；回环合法路径通过）③ `bun test` resident（新例：显式 hostname→listenHost 忠实回显 0.0.0.0；空→127.0.0.1；url 仍 host.docker.internal 不变）④ 手工构建 mc 对空 home 跑 `agent-profile proxy-bind`→`{"bind":"127.0.0.1"}` exit 0（未配置=回环，不报错）⑤ `./mc/check.sh` 合并态全链 exit 0、`bun test` 755/755
- expected_result: 默认实际监听 loopback；非 loopback 仅经显式合法 IP 配置生效；空/缺失=loopback 而非报错、非法值=fail-closed；容器侧 URL 契约不变
- actual_result: 全达成。两处真实回归由全链/单测暴露并修复：① foreground 启动桩未同步 proxy-bind→已加 case+更新严格请求集断言（含消息收窄为"不碰凭据"）② config.Load 对缺 config.toml 报错会误杀未就绪安装→新增 BindHost（缺失=loopback、存在但非法=报错，不放宽校验）。一次负载下 flake（TestTerminalSetupOpenCodeScreens/40-color-false-live-true）单跑两次绿、与 A05 无共享路径，列为测试卫生事项（不洗白为"重跑即无"）
- evidence_ref: evidence/{A05-mc-check.sh.out(首跑含两回归现场), A05-mc-check-rerun.out(合并态全绿 0 UNVERIFIED), A05-suite* 无(未触 shell lane)}
- evidence_digest: 首跑日志=（内容摘要由文件字节，提交 d7357cb 内可重算）；rerun 全绿、UNVERIFIED=0、FAIL=0
- test_script_digest: provider-proxy.test.ts / config_test.go / provider_access_client_test.go / provider_foreground_installed_test.go @d7357cb（git 锚定）
- execution_id: A05-run-01
- supersedes_verification_id: 无
- verdict: PASS（作用域=本提交+本机环境；Linux 真实网关绑定 + 容器 host.docker.internal 经 --add-host 的端到端可达性=**docker_e2e/boundary lane 未跑**（Docker MCP 断连），A05 网络暴露收紧的 Linux 实况待该 lane，标 UNVERIFIED 不并入本 PASS）
- reviewed_by: host-agent（执行兼任复核，显式声明；Integrity Reviewer 未做，不宣称）
- verified_at: 2026-11-19 21:40 (+0800)
- 边界: 工单 EVIDENCED ≠ 义务完成；不推导 TM B5/PS §2 冻结或 IMPLEMENTATION_VERIFIED；Linux 端到端为显式 UNKNOWN（Docker 不可用）
