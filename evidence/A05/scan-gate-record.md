# A05 扫描门槛履行记录

- 扫描：mimosa v0.1.0 deep，根=mc/config（A05 核心逻辑改动处），scanId `scan-2026-11-19T14-05-14.242Z-a3eefd51b30d`，seal `sha256:084ad3302a4f11d479d3f528bf2df54d29ea5cbe498596dd38b1db2a68572fc9`；selectedFiles=5/parsed=5，read/parseFailures=0，无截断——**本次改动文件确认在覆盖内**（对比 SCAN-BASELINE-001 的全仓不可证，小根策略有效）。
- 结果：3×high CWE-798 "硬编码凭据" @config.go:38/39/42 —— **逐条判为误报**：命中行是 `CredentialKind*` 类别名常量（值为 "codex-api-key" 等类型标识串，非任何凭据材料）；git blame 全部 = 上游提交 9dbc043（Culler 0.1.0），先于本 fork 一切改动。扫描器按"标识符含 APIKey + 字符串字面量赋值"模式匹配所致，与 SCAN-001 前 runner"SQL 注入"同族（第二实例，工具缺陷档案增厚）。
- **A05 新增代码区域（ProviderProxy/BindHost/validation/dialer/TS）：零发现**。
- 门槛判定：按处置 (a) 的口径，本次=**有效覆盖 + 新码零发现**（误报三例含逐条证据，不并入发现账）；runStatus 仍 inconclusive（调用图保守性，同前）——如实保留。
- 遗留：mc/deployment、mc/cmd/mc、resident 改动文件未做同型小根扫描（覆盖策略下一轮补；mc/deployment 根含大量上游文件，预期将复现误报族分诊成本——这本身就是"486 条上游告警待分诊"事项的缩影）。
- execution_id: A05-SCAN-001；时间 2026-11-19 22:05 (+0800)。
