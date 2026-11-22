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
