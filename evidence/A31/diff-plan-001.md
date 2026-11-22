# A31 diff-plan #1（实施轮：两处生产 launch 注入点补宿主网关映射——含票面"三处"引用的更正）

状态=CODED+EVIDENCED（判据见 §3；与 A32 runner 首跑合流的**跨拓扑验收**挂波门，见 §4）。授权=轮十六 D3①（契约=需要映射）+进码。
锚：`evidence/A30/linux-host-gateway-contract-facts-001.md`（F1 矛盾对）；产品自身先例=`mc/deployment/runtimeauth_verifier.go:448`（同旗同值逐字仿写）。

## 0. ⚑ 引用更正（票面曾写"三处"）
v2 所引"effects 三处注入点（:734-735/:1417/:1539-1540）"实为**两处挂载点+一处 write 行**：`providerAccessPath` 消费仅两处 argv spread（collie :733-735、agent :1538-1540），:1415-1418 是文件写（不产生 docker 参数）。本实施覆盖**全部真实挂载点（两处，`replace_all` 一次到位）**；票行"三处"随转写更正——引用错误在**我自己的 v2 证据件**，源头=追证时对 grep 行号的粗读。教训入 §7.1：**argv 注入点计数以"产生 docker 参数的表达式"为准，不以路径字符串命中数为准**。

## 1. 改动
`resident/src/effects.ts` 两处同形 spread 各加 `--add-host host.docker.internal:host-gateway`（+3 行注释；providerAccessPath 存在才加=fake/无投影容器不受扰，负断言在册）。
`resident/src/effects.test.ts` 正断言 ×3（claude/codex/collie 各站点旗-值成对）+ 负断言 ×1（fake 全 argv 钉测后 `not.toContain("--add-host")`）；零用例增删（断言入既有用例）。

## 2. 决策边界遵守（所有者轮十四）
- 范围=仅 effects 两挂载点+行为测（批准原文范围）。
- **不改**：`provider-proxy.ts:785` 的 URL 形（F2 explicit-bind 矛盾留 F0a 残余观察，非 A31）；`runtimeauth_verifier.go`（先例侧零动）；不并 A30。
- 零新 verb/wire/相位词；零配置面。

## 3. 证据
- **resident 全道** `RES_EXIT=0`（774/0 与基线同数=断言零增删）；**docker_boundary** `BOUNDARY_EXIT=0`（79.6s）。
- **真 Docker 端到端**：collie native-proxy e2e（该测走 effects 真实 launch 含 provider-access）`A31_E2E2_EXIT=0`（114.1s，树=dev@69d250e+A31=含 A30 修复）。**首跑红为我的拓扑错误**（支基 990e3b4 缺 A30 修复 ⇒ 两子界必红；`docker run --add-host …:host-gateway` 本机直测 `RC=0` 排除旗因）——如实记为"控制实验必须单变量"的**第 N+1 例自案**。
- Mac 可达性事实：host-gateway 关键字在本机 Docker Desktop 可用（直测）；Linux 引擎行为=A32 job 首跑载体（§4）。

## 4. 延期验收（具名，不静默）
"Linux 真容器→宿主代理端到端"（票面 F1③）=需 Linux 引擎跑 collie e2e——本地无 Linux Docker 域持续可用，**载体=A32 `opt-in-lanes` 首跑**（push 后）；届时回填 §7.1。218 宿主（amd64 Linux+Docker）可作为推送前的人工验证域——属生产宿主操作，候波门请示（不擅自动 218）。

## 5. 风险
`--add-host` 在 Docker Desktop 旧版（<20.10 引擎）可能拒旗——本机实测通过=当代版本事实；无既有部署使用旧引擎的证据（A04 矩阵=当代双架构）。若现场爆出，回退=同两点撤旗（2 行）。
