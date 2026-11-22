# A30 diff-plan #2（实施轮；序列上为票面第三版——v1 立票/v2 追证重写均在本文件 001 的 git 史内）

状态=**已实施，验收绿在途收口**（轮十六"批准整条议程"含 D1 夹具形选定+进码）。
锚：`evidence/A03/DEFECT-REGISTER-005.md` 更正段 #1 + 本票 diff-plan v2（§2 追证=本实施的依据）。

## 1. ⚑ 与批准描述的差异（如实标注，波门复核位）
PENDING-DECISIONS D1 描述写"只改 `mc/e2e/collie_native_proxy_test.go`（测试面单文件）"——**实际最小正确修复点不是测面**：断链在 `resident/src/main.ts:318`（起投影器不传 `fetch`），测面的 preload/断言本就正确（preload 拦的正是代理→上游那一跳的 header 形）。故**测试零改动，生产文件 1 处 +6 行**（见 §2）。按"夹具形=让夹具经产品既有注入边界生效"的批准语义执行；若波门判此越出"测试面单文件"字面，回撤成本=revert 一提交，零连带。
（v2 §3 的预计触面里"collie/resident/dispatch 解除嫌疑"一句系**追证前**的旧 wake 框架遗留——v2 自身已被 §4 更新推翻，此处不再重复该句。）

## 2. 改动（唯一一处）
`resident/src/main.ts`：`startProjector` 内构造投影器 deps 时，
`fetch: process.env.CULLER_COLLIE_PROXY_FIXTURE ? globalThis.fetch : undefined`
+5 行注释。语义：
- **变量未设（生产/dev/CI 全部真实运行）⇒ 选项 undefined ⇒ `provider-proxy.ts:435` 走 `providerTransport` 逐字不变**（`??` 语义，与改动前同一求值路径）。
- **变量已设（=该 e2e 测的 preload 环境，preload 自身强制要求该变量否则拒绝启动）⇒ 经既有注入缝（`credential-projector.ts:127/:136` deps.fetch→proxy）把 preload 替换过的 `globalThis.fetch` 送进代理 egress 决策点**。拦截语义与原测试意图逐字一致（"host proxy forwards → fixture serves synthetic stream"）。
- 双注入权威检查（批准判据"native-harness 不动"）：bun 级套件经 **deps.fetch 直注**（不读 env）；env 分支只在 preload+fixture 目录存在时生效，两者不同时命中同一进程——**无双重注入态**。

## 3. 证据
- **验收门（判据后半照跑）**：`TestCollieNativeProxyDockerBoundary` `A30_E2E_EXIT=0`——codex 120.07s / claude-sdk 42.62s 双 PASS；绿即证 `requests.jsonl` 恰一条（`:105`，改动前永不可能存在）、draft 先于 release（`:88`）、完成后再无第二条请求且凭据不露（`:127-130`）。**该 e2e 直接以被检树的 main.ts 起 resident**（`startNativeProxyResident` 从 `../..` 跑工作树源）⇒ 测的绿即本改动的行为证。
- **resident 全道**：`RES_EXIT=0`，774 pass/0 fail（与基线同数=无测增删）。
- **三枚通过 collie 测不回归**：`REGRESS_EXIT=0`（Conversation/Resume/PacketArtifacts，包级 148.5s ok）。
- 测试面零改动 ⇒ 转写≠放宽义务自然满足。

## 4. 边界
零新文件（产清单无需变更）；零新 verb/wire/相位词；`tests/provider-proxy/*`、测文件、preload 一字未动；DEFECT-005 登记件待随 §7.1 记"已修"指针。
