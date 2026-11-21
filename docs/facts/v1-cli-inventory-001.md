# V-1 CLI 实体清单 #1（A14 执行前置事实卡 · 定向机 mc-prod 实测 E1）

日期 2026-11-21；源=218 上 mc-prod@9e0c54b（amd64）容器内只读探查（`--user 10002:10002 --network none`）。用途=TM §7.3 V-1 绑定表"CLI 精确版本/启动命令/探针落点"三格的**事实半边**；另半边（恶意样本设计）由 v-list-assembly-001 §2 规格承接。**修正组建稿 §1 的假设**：CLI 不在 PATH、不在 project 镜像层——生产镜像 PATH 无 claude/codex/opencode；harness 真身全部住在 **runtime 层**。

## 实体（逐条实测）
| 家族 | 实体路径（容器视角） | 形态/版本 | 启动链 | 配置发现源（V-1a 四源在本家的对应） |
|---|---|---|---|---|
| claude | `/opt/culler-runtime/app/node_modules/@anthropic-ai/claude-agent-sdk-linux-x64/claude` | SDK 内嵌 CLI，SDK 0.3.322（package.json 实读） | runner 经 `MC_CLAUDE_ADAPTER` 以 SDK in-process 拉起 | HOME=/home/agent(tmpfs 化，A16 后带 size) 下 `.claude*`；cwd/项目目录 `.claude/`；显式 env 路径；父目录上溯 |
| codex | `/opt/culler-runtime/app/node_modules/@openai/codex`（bin/codex.js）+ `codex-linux-x64` 平台包；`.bin/codex` 软链在 | 平台包形态（A04 D-a 双 manifest 对之 x64 腿——**跨架构一等公民又一实证**） | `MC_CODEX_ADAPTER` spawn | HOME 下 `.codex/`；项目 `.codex/config.toml`；`CODEX_*` env |
| opencode | **不在本镜像**——独立卷 `mc-opencode-<suffix>` 挂载 `/opt/culler-opencode`（openCodeArguments），配置掩码 tmpfs `/etc/opencode` | AgentsView/opencode 钉族产物 | `MC_HARNESS_CLI`/adapter 经卷内二进制 | tmpfs `/etc/opencode` 为**产品自带掩码**（ro,nosuid,noexec,mode=0555）——V-1d 时序论证的现成锚点 |
| fake | runtime 卷 `MC_HARNESS_CLI=.../fake-harness/cli.ts` | bun 脚本 | 直接 exec | 无真实 CLI 面（其"配置发现"=平台自身 run.json/env，已被 A12/A22 面覆盖） |

## 关键事实
1. **PATH 无 harness CLI 是设计**（runner 用绝对路径/adapter env）——V-1 探针不得经 PATH 找 CLI（会全 NOT_FOUND 假阴性）；按上表实体路径逐一测。
2. `/etc/opencode` tmpfs 掩码 = 启动顺序（V-1d）已有产品级证据点；其余三家**无等价格外掩码**——恶意样本注入面=各自 HOME/项目/cwd/env 四源，逐源探针。
3. HOME 现状：容器默认 `HOME=/home/mc-model`，pipeline 容器经 `projectHomeArguments` 改 `/home/agent` bind + `.agents/.claude/.codex` tmpfs（A16 后带 size 帽）——**tmpfs 帽只兜容量不兜内容**：tmpfs 初始为空=对"仓库带配置"是正面防御，但 HOME 首层 bind 的是**项目 home 持久目录**（跨 run 残留=V-2 作用域，装—换 run—读取链）。
4. 版本钉证据链：上表版本=镜像内实读；镜像=定向机 build-prod@9e0c54b；构建供给链=A04-S2/V-002 链。**"由部署所有者提供的 §7.3 环境事实"之 harness 行至此由 E1 填充**（版本随发布记录漂移，每轮验收器重读，不继承）。

## 下一执行件（待开）
`a14-v1-acceptance.sh`：逐家族×四源恶意样本（assembly §2 规格）+ `was-executed` 探针；T2 容器形（带 run.json）+ t2 参数表（A16 已提供配置面）。V-2/V-7 采样同场。**与 boundarydocker/e2e 道错峰**（同一定向机资源包络）。

## 追加：codex 族发现轮实测（2026-11-21，a14-v1a-discovery.sh @218，measured-218.tsv+raw 入档）
- **读源确认**：`$HOME/.codex/config.toml` 与显式 `CODEX_HOME` 两处**被读**（S0 正控+R.home 均 read-config）。
- **不读确认**：cwd 下 `.codex/`（R.cwd=clean，cwd 直挂注入目录仍未读）——本版本 codex 无 cwd 发现。
- **自纠**：R.upscan 探针形状缺陷（注入目录未落在 /tmp/sub 的父链上）=**父目录上溯未测**，clean 不作结论；修正探针（注入挂 /work、cwd=/work/sub）随 claude 族轮一并重跑。
- **S2 硬断言 PASS**：生产 pipeline 形（HOME bind + `.codex` tmpfs 掩码）下注入配置**读不到**——掩码恰好覆盖 codex 唯一的活读源=策略意义明确：**对 codex 族，产品掩码已闭合 HOME 源；仓库经 bind 进入的 HOME 持久目录（项目 home）由 V-2 跨 run 面接管；cwd/父链源该版本不存在**。
- claude/opencode 族：同表待跑（claude 活源=~/.claude.json+settings——`--version` 短路、需选择读配置的离线子命令，探针形状=下一件）；V-1b/c/d 政策判定仍门控于 T2 活会话（A25/A02）。
