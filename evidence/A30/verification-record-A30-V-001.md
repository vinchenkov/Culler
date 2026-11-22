# A30 验证记录 #1（TM §7.1 · collie native-proxy 夹具形修复=既有 fetch 注入缝由 env 守卫生效）

- verification_id: **A30-V-001**
- target_component: e2e Collie 原生代理子界的传输拦截链（DEFECT-005：preload 补 `globalThis.fetch` 而生产代理 egress 走 `providerTransport`，拦截结构性死）
- tested_version: 票支 `shz/TA30` @本记录提交；dev 基线 `990e3b4`；改动=`resident/src/main.ts` +6 行（唯一一处，测面零改）
- 判据（轮十六 D1 夹具形选定）：两子界转绿+三通过测不回归+全道；"native-harness 不动、无双重注入权威"（批准注记在册）

## 结果
1. **验收门绿**：`TestCollieNativeProxyDockerBoundary` `A30_E2E_EXIT=0`——codex 120.07s / claude-sdk 42.62s 双 PASS。绿体即行为证：draft 先于 release（`:88`）+ **`requests.jsonl` 恰一条（`:105`——改动前该文件永不可能生成）** + 完成后无第二条且凭据不露（`:127-130`）。e2e 从被检树源码起 resident（`startNativeProxyResident`）⇒ 测绿=本改动生效的直接证据。
2. **不回归**：三枚通过 collie 测 `REGRESS_EXIT=0`（Conversation/Resume/PacketArtifacts 148.5s ok）；**resident 全道 `RES_EXIT=0`（774/0，与基线同数——断言加于既有用例内，零增删）**。
3. **生产形态零扰动论证**：`CULLER_COLLIE_PROXY_FIXTURE` 未设（生产/dev/CI 真实运行）⇒ deps 选项 undefined ⇒ `provider-proxy.ts:435` 求值路径逐字同前（`??` 语义）；该变量仅被本 e2e 夹具设置（preload 自身强制要求它）。**bun 级 native-harness 经 deps.fetch 直注、不读 env ⇒ 无双注入命中态**（批准判据复核在案）。
4. **差异标注（波门复核位）**：批准件 D1 描述"只改测面单文件"——实际最小正确修复在 `main.ts`（断链点），测/preload 本就正确零动。diff-plan-002 §1 具名；回撤成本=1 提交 revert。

- reviewed_by: host-agent（agent E1；非独立复验，语义按轮十二 §G 在册）
- verdict: **EVIDENCED 就绪**。残留：Linux 引擎下 host.docker.internal 解析=**A31 域**（本改动与引擎无关，拦截在宿主进程内）；`docker_boundary`/`update_docker` 不涉 resident TS 求值路径，随波次统一回归，此处如实挂账。
