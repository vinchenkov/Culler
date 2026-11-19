# SCAN-BASELINE-001（第二批门槛·首次基线扫描记录）

- 门槛条款：IMPL-AUTH-DRAFT-002 §4（**随②正式登记生效**；本记录为②建议下的首次履行尝试，无论结果形态先成真实记录）
- 扫描对象绑定：shz/dev@`fa8c07b75a47`（含首批全部新代码：tools/blcheck 5 文件、docs/design、docs/facts、docs/contracts）；worktree=/private/tmp/culler-worktrees/dev
- 工具与执行：mimosa security plugin **v0.1.0**，depth=deep，focusFiles=[blcheck×4, reference_encoder.py]；scanId=`scan-2026-11-19T12-42-03.851Z-93f777d24f41`；seal=`sha256:9107b04985528be58017316209b312c6a6e64a81145891901e8e4ecb660921a1`；scanDir=`~/.mimosa/security-scans/project-0f2111a0377592931de28132/scan-2026-11-19T12-42-03.851Z-93f777d24f41/`（原始产物持久于用户目录，非 /tmp）
- 原始产物摘要（字节绑定，非路径绑定）：coverage.json=`c7d3565d…578e`；findings.json=`5899bb37…3983`；report.md=`ffaf10ad…a92f`；scan-manifest.json=`b1529299…373f7`；seal.json=`6fa3ec7d…a109`

## 结果四分

1. **findings**：486 条（high 373 / medium 109 / low 4）——分布 dashboard 161 / mc 149 / runner 78 / resident 62 / evals 19 / performance 10 / release 7，**全部位于上游既有代码目录**。这是新暴露的事实面：上游代码存在一揽子待分诊告警（含此前已实证的误报族——runner"SQL 注入"三例即同类），是否立项分诊归所有者。
2. **未覆盖项（本门槛的核心问题）**：新代码 tools/blcheck、docs/* **在全部工件中零出现**。coverage.json 仅计数（selectedFiles=1472、parsed=1472、truncated=false、read/parseFailures=0），**无每文件清单**——因此**无法证明**新代码进入选择集，也无法证明未进入。verdictEffect=none。
3. **工具错误/不完整原因**：gaps 原文="调用图部分不完整：部分调用为动态派发或超出分析规模，跨文件可达性可能不完整" → completeness=partial，runStatus=**inconclusive**。
4. **焦点参数无效性（重复证实）**：本扫描与 20:37 那次（不同 project root）签名一致——focusFiles 在工件中无任何可见效果。

## 门槛判定

**未满足**。inconclusive≠PASS（②原文）；仓库门禁全绿、人工阅读（E1）均不替代扫描覆盖证据（②原文）。**登记为工具缺陷**：mimosa v0.1.0 深扫工件缺每文件覆盖清单，新代码覆盖不可证、焦点参数无效——建议②采纳时把本缺陷一并入 AM 待决面；门槛的满足路径=修复/更换扫描工具后重跑，或由所有者对"覆盖证明缺失"显式处置（豁免需登记，不得静默）。

## 追加：尝试 #2（目标根拆分法，20:50）

用**只含新代码的项目根**做定向深扫，绕过全仓规模导致的覆盖不可证：
- `tools/blcheck` 根：scanId `scan-2026-11-19T12-50-06.893Z-ce0ec31b85fa`，seal `sha256:5ddecea6…`——**selectedFiles=4/parsed=4**（=目录内全部 4 个 .go 源文件，与人工清单逐一对账），0 findings，0 读/解析失败。
- `docs/design` 根：scanId `scan-2026-11-19T12-50-12.018Z-f882e7f98184`，seal `sha256:08d8cc82…`——selectedFiles=1/parsed=1（=唯一的 reference_encoder.py），0 findings。
- **但两次的 runStatus 仍= inconclusive**（gaps："部分分析阶段未能完整覆盖"+"调用图部分不完整"——该工具对其认为存在跨文件动态可达的根一律降级；对 blcheck 这种 CLI+纯函数包，此保留属工具保守性而非已知缺陷）。

**门槛判定（更新）**：新代码的**文件级覆盖证明已达成**（5/5 源文件 selected+parsed+零发现，计数与清单对账可复核）；工具仍拒绝给出 conclusive 结论。**这是 mimosa v0.1.0 的能力上限**。按②规则 inconclusive≠PASS——门槛正式关闭路径只剩二选一：(a) 所有者显式处置：接受文件级覆盖+零发现为本门槛的满足形态，把"调用图可达性不完整"登记为已声明残余（可复核、可日后换工具补扫）；(b) 更换/修复扫描器再跑。静默豁免禁止。

## 复现

对所有列出的摘要重跑同一命令即可核对；扫描工件本体按 scanDir 路径+seal 定位（外部存储定位条款首次实例）。

- execution_id: SCAN-001；扫描时刻 ≈2026-11-19 20:42 (+0800)；环境：MacBookPro darwin 24.6.0 arm64
