# Linux 宿主代理可达性——契约事实包（呈所有者裁"add-host 小票"之前提；零裁决）

背景：轮十三审查意见三定"是否立票以 **Linux 部署契约是否明确要求该能力**为前提，不能仅凭源码缺 `--add-host` 定性"。本件收集库内**已有的契约表态**（皆本代理亲验 file:line），把前提问题变成可读材料。归属与立票权=所有者。

## F1 产品自己的两处表态（互相矛盾的那对）
- **判定"容器需要该映射"**：`mc/deployment/runtimeauth_verifier.go:448-449`——onboarding 的 runtime-auth 验证/登录路径（生产入口 `mc onboard runtime --acquire`，`cmd/mc/onboard_runtime_prod.go:19-20` 挂载）**在注入 provider-access 挂载的同时显式追加** `--add-host host.docker.internal:host-gateway`。产品在此路径上**自己回答了"需要"**。
- **生产运行路径无此映射**：`resident/src/effects.ts` 全文无 `--add-host`/`host.docker.internal`（三处 provider-access 注入点 :734-735/:1417/:1539-1540 皆然）——agent/collie 的正式 birth 只带 `-e MC_PROVIDER_ACCESS_FILE` 与挂载。

## F2 容器侧 URL 与 bind 的解耦（加重疑点）
`resident/src/provider-proxy.ts:785` 容器拿到的 url 恒 `https://host.docker.internal:<port>`（**与 bind 选值无关**；bind 只决定宿主监听面，:435 egress 亲验）。故 `mc/config/config.go:403-406` 注释承诺的"显式 IP bind 让 agent 容器经 host gateway 地址可达（Linux）"，在**容器仍被要求解析 host.docker.internal** 的前提下并不自洽——除非宿主 DNS/引擎另行提供该名。

## F3 该能力的验证覆盖现状（为何从未暴露）
定向机 218 的全部真跑（A03/A04/A16/A26/A27/A28/A29 各道）中，provider 往返面**从未被非 Mac 容器测过**：e2e 只在 macOS；boundary/update_docker 触达的挂载与网络形态不含 host-gateway 解析题。C-A（Linux CI job）落地后 e2e 将在 Linux 引擎上跑真 harness——**但 fake harness `--network none` 不发 provider 请求，故 C-A 也不会暴露此题**；唯一会暴露它的是"Linux 上的真 harness 端到端"，现无此道（A14 V-1b/c 门控在册）。

## F4 供裁定的问题形（不问"是不是缺陷"，问契约句取舍）
1. 若裁定"**Linux 容器必须能达宿主代理**"（=采纳 verifier:448 为契约表态）⇒ effects 三注入点需补映射或等效物=独立小产品票（A31 候选），正清单可预判为 `resident/src/effects.ts` 三处+行为测（Linux 引擎上解析断言）；A30 与其零耦合。
2. 若裁定"**Linux 代理可达=未承诺能力**"（=config.go:403-406 注释降格为 Mac 便利描述）⇒ 修**文档**一句+把该面挂 F0a 残余（"Linux+真 harness 未验证"），无码。
3. 若裁定"以 explicit-bind 为 Linux 正形"（config 注释原意）⇒ 需先解 F2 的 url 解耦矛盾（容器侧 URL 应随 bind/网关形变=又一码面决定）。
执行侧倾向：材料上 1 与 3 互斥、2 与 verifier 现文冲突——**三条都需所有者先落一句契约话**，本件只呈不裁。
