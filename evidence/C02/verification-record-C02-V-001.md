# C02 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: C02-V-001
- target_component: DestinationPolicy 执行面——resident/src/provider-transport.ts（新）+ provider-proxy.ts 默认出口替换 + mc/deployment/host_release.go 清单登记 + 测试 9 例（provider-transport.test.ts）
- tested_version: shz/TA-C02@e49d0da（基线 shz/dev@e0112c3）
- environment: bun 1.3.12（mise）；本地回环 TLS 服务器+隔离 CA（测试注入 ca，生产路径 rejectUnauthorized:true 恒定）；macOS arm64
- attack_scenario: PS §2 靶族——非规范地址形利用解析器差异（0x7f/八进制/十进制/短计数/前导零）、"验一个地址连另一个"（解析后换地址/Happy-Eyeballs 多地址）、keep-alive 池绕过复验、自动跟随重定向换目的地、环境代理继承劫持出口、截断流伪装完整（本次实测发现 Bun 将提前断流报 end——适配器以 complete+close 双条件判完整，反例入测）
- expected_result: §2.1 完整对象判定且唯一解析器；§2.2 每次连接尝试自验（含字面量目的地入口过滤）；§2.3 SNI=canonical host 且证书必验；§2.4 无复用（每请求全链）；§2.5 重定向永不自动跟随；§2.7① 结构免疫环境代理（node:https 无代理继承）；A2 静态半=provider-* 原始出口枚举守卫
- actual_result: 9 例全绿（规范/拒绝向量 16+、connect 过滤正反例、无复用连接计数=2、302 原样返回、abort 截断必红、字面量 cidr 入口拒、method/url 形态拒）；**provider 全家回归 94/94、resident 全道 764/0、mc/deployment 包 ok（清单闭包含新文件）**；全道 `./mc/check.sh` EXIT=0（0 FAIL/0 UNVERIFIED/26 包 ok，归档 evidence/C02/full-lane.log）。开发期三处自纠如实留痕：v6-mapped 字节序、cidr 位段比较、IP 字面量绕过 lookup（入口过滤补位）——均由测试先行暴露
- evidence_ref: resident/src/provider-transport.test.ts + docs/facts/ps-e0-implementation.md（实现层注记）+ 扫描记录（下）
- evidence_digest: 全道 `./mc/check.sh` **EXIT=0：0 FAIL、0 UNVERIFIED、26 包 ok**（evidence/C02/full-lane.log）；扫描门——resident/src 根 13 findings 全落 main.ts/dependency-reuse.ts/provider-foreground.ts（未触碰上游文件，seal 818b441c…）；mc/deployment 根 20 findings 全落上游文件、host_release.go 零命中（seal e8880469…）；两改动文件（provider-proxy.ts 增量行/provider-transport.ts）零发现
- test_script_digest: provider-transport.test.ts @e49d0da（git 锚）
- execution_id: C02-run-01
- verdict: PASS（作用域=机制实现+测试钉死+回归无破损；**P2 Exit 判定不属本件**；§2.7② 的"运行时连接审计"半与 allow_cidrs 配置面=C09/C01 义务，**不继承"出站无旁路"结论**；discord/dependency 下载等非 provider 域出口如实注记于覆盖文档）
- reviewed_by: host-agent（兼任声明；非独立验收、非 P2 判定）
- verified_at: 2026-11-20 (Asia/Shanghai)
