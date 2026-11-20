# PS-E0 · provider 出站执行面实现注记（C02）

## 出口拓扑（§2.7 静态枚举，resident/src）

- **provider 域唯一原始出口** = `provider-transport.ts` 的 `https.request`；`provider-proxy.ts` 默认 `requestProvider` = `providerTransport({portAllowlist:[443]})`，测试经依赖注入桩替换（生产形态不变）。守卫：`provider-transport.test.ts` 的 coverage describe（provider-*.ts 禁原始出口构造与 `await fetch(`，漂移即红）。
- **非 provider 域出口（不在本契约"无旁路"声明内，§2.7③ 不继承）**：`discord.ts`/`discord-config.ts`（通知面，操作者配置目的地）、`dependency-reuse.ts`（下载面）、`main.ts` 等对 **本机** proxy/gateway 的回环调用（回环≠出站目的地）。各自威胁域归属：git/宿主面=TM V-3/DT；下载面=供应链（T 层）。
- allow_cidrs 的**配置管道**未落地（config schema=C01）；机制端已就绪（`buildDestinationPolicy`），C09 的 config↔执行一致性验收覆盖该链。

## 运行时行为事实（实测，非文档推断）

- Bun 1.3.12 的 node:https 客户端在**响应截断时先报 `end` 再报 `aborted`**——body 完整性判定采用 `close + message.complete` 双条件（探针复现，反例入测）；这使"流截断伪装完整"族在本执行面被结构性堵死。
- node:https 客户端不继承 `HTTP(S)_PROXY` 环境代理（与 §2.7① `Proxy=nil` 同构，结构性而非配置性）。
- 自定义 `lookup` 钩子对 **IP 字面量目的地不被调用**——字面量在 transport 入口直接过滤（测试 `connect-time filter` 暴露此点后修正）。
