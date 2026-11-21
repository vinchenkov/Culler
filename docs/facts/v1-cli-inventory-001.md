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

## 追加：claude 族发现轮实测（2026-11-21，a14-v1a-claude.sh @218，claude-measured/claude-raw 两 218 件入档；探针形状=`claude config get theme` 离线子命令，版本实读 2.1.322）
- **C0 正控 read**：损坏 `~/.claude.json` 触发 `corrupted: JSON Parse error`（消息点名文件=理想发现探针）。
- **C1 = clean（本轮最重要一格）**：`CLAUDE_CONFIG_DIR` 指空目录（**产品 adapter 实况：claude.ts:61 每 run 私有 tmpdir + :110 settingSources:["user"]**）时损坏 HOME 文件**不再被读**——产品的配置迁移**实测有效**，HOME 泄漏路被切断。
- **C2 = clean（带探针限度声明）**：cwd/.claude/settings.json 损坏在该子命令下无 marker——**"该探针形状未见项目设置读取"≠"claude 全阶段不读项目设置"**（settings/hooks 的读取阶段可能仅 session 启动，属 T2 活会话门控面）；settingSources:["user"] 的 SDK 级强制=适配器单元既有事实（claude.ts 注释明言 project 源=模型自写配置 hazard，"keep it user"）。
- **C3 = clean** 基线控制。
- 综合（codex+claude 两轮）：两族的**活发现源均被产品机制对准**（codex=HOME tmpfs 掩码罩住唯一活源【S2 硬断言 PASS】；claude=CONFIG_DIR 迁移切断 HOME【C1】）；项目目录源=SDK 选项层（单元面在册）+V-1b/c 活会话判据（继承 A25/A02）。opencode 族=tmpfs 掩码 /etc/opencode 已在装配（V-1d 时序锚），其发现轮随 T2 会话轮。**（下节把 opencode 行从"随 T2"提前完成了——发现轮不需要活会话。）**

## 追加：opencode 族发现轮实测（2026-11-21，a14-v1a-opencode.sh @218，opencode-measured/raw-218 入档；种卷=产品 openCodeInstallScript 逐字复刻+版本正控 v2.2.7【diff-plan-004 撤回自我更正版】；探针=`debug config` 带 path 装载序，MARKER 值判据——损坏 JSON 不报错但合法值必显，判据先于使用经实地校准）
| 行 | 结果 | 事实 |
|---|---|---|
| O1 XDG_CONFIG_HOME 迁移位 | **读** | 活源；产品 adapter 指每-run 私有目录=迁移隔离（同 claude 模式） |
| O2 HOME/.config 形（XDG 别指） | 不读 | 无暗通道 |
| O3 项目 `.opencode/`（cwd=模型可写工作区） | **读（CLI 原形）** | 策略级事实：opencode 默认吃 cwd 项目配置 |
| O3b 产品旗标 OPENCODE_DISABLE_PROJECT_CONFIG=1 | **不读** | **adapter 旗标实测挡效**（adapters/opencode.ts:61；"配置了"升"生效了"，同 C1 之于 claude） |
| O4 AGENTS.md 经 debug config | 不可见 | **仪器限度如实**：指令装载在会话阶段=仍 UNKNOWN，非"不读" |
| O5a/O5b /etc/opencode 读性/掩码 | **读 / tmpfs 挡** | **产品掩码两侧正控齐备=实测有效**（教科书形） |
- **三族合读**：每条活源都有产品机制且**现在全部持有真机行为证据**（codex=tmpfs 掩码 S2；claude=CONFIG_DIR 迁移 C1；opencode=XDG 迁移+disable 旗标 O3b+/etc 掩码 O5a/b 双侧）；残余=O4 指令源（会话阶段=T2 门控）+版本漂移重测纪律。另记：本轮两处探针自纠（/w 不可写≠发现结果、O2 形状错、O5 加 5a 正控才成立）+首轮 O0 位置参数序错（产品调用形 $0..$3 追读源文修正）——**仪器错误的每条都留了日志证据再修，无一洗白**。
