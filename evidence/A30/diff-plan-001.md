# A30 diff-plan #2（登记票面 v2：Collie native-proxy 子界红——**失败层=provider 传输边界**；wake/birth 框架已作废，v1 全文经 git 史保留）

**状态=立票登记，实施未授权**（轮十二 Q1："仅批准立票，不授权实施"；进码需明示令，A26/A27/003A 先例）。v1→v2=预勘+独立复验推翻 wake/birth 框架（全程=`evidence/A03/DEFECT-REGISTER-005.md` 更正段 #1，含**违犯"感知远程效果≠不存在"纪律的自案**与"探针位置错"两处入册教训）。
症状（修正版）：`TestCollieNativeProxyDockerBoundary/{codex,claude-sdk}` 超时 `no native draft`——容器**正常 birth**（`runner.started` 实证于宿主侧保留树）→ 适配器 provider 请求**未抵达 fixture 传输**（`native-provider/requests.jsonl` 不存在=零请求；预勘侧 401 证据涉已删卷，待 §2 复验）→ harness `exit=1, replied=false` → 会话 `resume_owed` 停摆。wake/birth/消息持久化**皆正常**。

## 1. 五项保留证据（按所有者指定形更新）
1. **失败签名逐字**（两次独立跑）：`collie_native_proxy_test.go:88 timed out (1m30s) … no native draft` + 保留树 `home/logs/runs/h-22c5523c973ad663/runner.ndjson` 逐字 `"exit":1,"replied":false`。~~`No such container`~~ **作废为 birth 证据**（collie 容器 `--rm`，事后 docker logs 必然落空，effects.ts:686 注释自明）——保留为**误导向量证据**。
2. **父提交同签名**：`04cd6b2`（无 A29 改动）同红 `PARENT_EXIT=1`（308.24s）⇒ 非 A29 引入。
3. **夹逼边界**：三枚通过 collie 测（Conversation/Resume/PacketArtifacts）与失败测之差=resident 晚启+preload 补 `globalThis.fetch`+测配置 `surfaces.provider_proxy bind=""`；**通过测是否走宿主代理路径待 §2 证实此差**。
4. **时序/层级证据（已定层）**：birth 与死因链全在宿主侧可复验；**唯一未终判=请求实际 egress 路径**（§2）。
5. **行为判据（替代已作废的 (a) birth / (b) fail-closed 对）**：归属判定后二形——**(夹具形)** fixture 布线补全使请求可抵 preload 传输，两子界转绿+三通过测不回归；**(产品形)** 若 egress 在 `bind=""` 下语义未定义/旁路宿主代理=产品缺陷，判据=修正 egress 路由+附**独立于该测**的行为证。**择一与正清单=§2 追证后呈，裁决权归所有者。**

## 2. v2 前置追证（只读，本票下一动作；预勘 E3 留口）
codex/claude 适配器的 provider egress 走 `globalThis.fetch`（preload 可拦）还是 SDK 自带传输（绕过之）：追 `runner/agent-runner/adapters/` egress 面、`tests/provider-proxy/collie-resident-preload.ts` 拦截面、宿主代理 `bind=""` 时 provider-access 落点，并复跑一子界取证 requests 是否出现（可携轻量插桩于**测支内**，不入产品码）。**结果决定 §1-5 归属；不预设。**

## 3. 边界
不触 L02（双残余/T-2 与本票无关）；不并 D-C；005 §1/§2/§5 旧 wake 框架作废留痕于更正段（原文不删）；实施授权前 collie/adapters/resident 零动。

## 4. §2 追证结果（轮十二续；只读追证子代理+本代理亲验承重行）
**归属材料=H-夹具（置信高），结构链亲验**：容器 egress 目标=宿主代理（`provider-access.json` 的 url 恒 `https://host.docker.internal:<port>`，`credential-projector.ts:159`+`provider-proxy.ts:785`；bind 只选宿主监听面，`config.go:403-406` 空=loopback，Mac 下 host.docker.internal 可达）；代理上游 egress 读 `dependencies.fetch ?? providerTransport`（`provider-proxy.ts:435`，本代理亲验），生产 `main.ts:318` 起投影器**不传 fetch**（行范围无 "fetch"，亲验）⇒ 实际走 `provider-transport.ts`（node:https）；而 fixture preload 只补 `globalThis.fetch`（`collie-resident-preload.ts:15`，亲验）——**被补引用在生产调用链上从未被读，拦截在最后一跳结构性失效**⇒代理持合成 key 打真实上游=401（预勘侧 diagnostic 证据，与机制一致）。**注入缝已存在**：`credential-projector.ts:127 fetch?: typeof fetch` → `:136` 下传（bun 侧 native-harness/collie-stream/failure-repair 套件即走此缝且**绿**——真容器化 codex/claude CLI 到宿主代理端到端已证，产品 egress 设计无缺口）。三枚通过 collie 测=假 harness（fakeRouting；`credential-projector.ts:166-167` fake→null projection 无挂载 `--network none`），**从不发真 provider 请求**——失败测是道内唯一期待真形往返者。
**UNKNOWN 保留（不掩）**：a) 观测轮请求实际到没到代理（macOS loopback 可达性；requests.jsonl 从未存在故不可判，保留树 `runs/provider-access/` 空=清理或未写不可判）；b) Linux 形 `bind=""`+host.docker.internal 无 `--add-host`（effects 全文无该参）=契约问题（若产品声明支持 Linux 容器达代理，此为**另一独立产品缺口**，属 A03/A04 部署面非本票 wake 题——单列呈报）。
**判据二形（供所有者择一，即 §1-5 的落地）**：**(夹具形·主)** e2e 测改用既有 `fetch` 注入缝（或等效测试钩子）使 fixture 传输真正在链上，判据=两子界转绿+三通过测不回归+**native-harness 绿套不动**（避免双权威注入）；**(收缩形·副)** 承认 bun 级 native-harness 套为该拦截的既备证据，删除/降级 e2e 侧重复断言（转写≠放宽的边界在此=不得把两形都留成假绿面）。b 项若裁"Linux 契约应撑"，另立小票不并 A30。
