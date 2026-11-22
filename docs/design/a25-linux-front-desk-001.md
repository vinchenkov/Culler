# A25 前置文本：Linux 安装前台语义的一次转写（轮九 Q8 批准的零码义务）

性质=**语义转写**（产品裁定文本），非设计提案、非实施票。义务来源=轮六 Q6(b)"追加 Linux 安装前台语义一次文档转写"，轮九 Q8 批准在 A02 决议前执行。本文不触凭据存储/读取机制、不构成 A25 代码授权、不解锁任何票。
锚（E1 读源/在册登记）：`mc/cmd/mc/setup_terminal_prod.go:99-105`、`mc/cmd/mc/main.go:117`、`release/installer.sh:216`、AM v0.1.55（B3 真执行）、v0.1.59（第十二轮止步+B5–B10 移交）、v0.1.58 Q6(b)（权威面裁定）、v0.1.46（O1-a 冻结：Linux hostSecretStore=显式 unavailable）、A24 已并面（`runtime_scope_prod.go` 谓词对 {darwin,linux} 放行）。

## 1. 现状：Linux 操作者今天面对什么（逐段如实）
| 段 | Linux 现态 | 依据 |
|---|---|---|
| 发布安装 | **可走通**：installer 族已 Linux 一等（A04 双发布腿），打包 installer 在定向机**真实执行**过 | v0.1.55/v0.1.59（B2/B3） |
| 安装手递 setup | 包内 `installer.sh:216` exec 新 `mc __setup` → **止步**：`runTerminalSetup` 对非 darwin 打印 "Culler setup requires a Mac." 并 return 2；验证器按平台感知断言钉死该退出对 (2, 同句)——**断言现实而非弱化** | `setup_terminal_prod.go:99-101`；v0.1.57② |
| 容器内伪装 | 第二道拒语 "Culler setup requires the operator's Mac terminal."（/mc/run.json 在场即拒），防 agent 冒充操作者终端 | `:103-105` |
| 部署后配置 | **权威面=dashboard/Collie**（轮六 Q6(b) 产品裁定）：Configs 全局页（模型 profile 测试/部署检查/System health/Project configuration）、Collie 对话面；操作者经 `culler` + 界面完成配置与生命周期，**从不被指使跑 `mc`**（AGENTS 操作者文本规） | PAGES.md:71/331/346-348/378；v0.1.58③ |
| 宿主视野 | A24 后 operator-host（{darwin,linux}∧非容器）可走宿主视野/委托谓词——**但向导门不在 A24 正清单，仍 darwin-only**；两者不混淆 | AM v0.1.68；`setup_terminal_prod.go` 未触 |
| 持久化监督 | 单元渲染已在（systemd `Restart=always`/RestartSec=10 镜像 launchd 姿态），**真实监督行为的验证收窄句**（"持久化监督面仍未验证——结构依赖 A25 向导"）随票携带至 F0a | v0.1.59③；`supervision_systemd.go:74` |
| 演练路径 | A04-S2 曾用宿主私有动词链（`__onboard` 族）驱动持久段演练；第十二轮定性=**warm-helper 编排与 config/spine 持久化=向导单权威，宿主直驱=第二权威，违 003 不变量而止步** | v0.1.59② |

## 2. 权威面裁定（本文的核心转写）
1. **配置权威面**（凭据/profile/project/environment）= **dashboard + Collie**。Linux 上这是产品给操作者的唯一前台，A25 不移植向导**不改变**此裁定。
2. **设置持久化权威面**（spine/config 落盘、warm-helper 编排、首装编排）= **向导（`__setup`/`__onboard` 编排族）单权威**。任何第二驱动形态（宿主脚本直驱、dashboard 侧直接持久化）**不得产品化**；演练用私有动词链**不得被引用为产品路径的先例**。
3. Linux 前台的**实际缺口**因此只有一个：**向导首段（setup）在 Linux 上整体缺席**——不是配置模型缺席，是入口缺席。移植=把同一单权威换平台呈现，不是新建权威。

## 3. A25 开工时的接缝清单（只点名，不预裁）
- **凭据段**：向导的凭据录入语义与 A02（后端选型）纠缠——Linux hostSecretStore=显式 unavailable 为 O1-a 冻结事实。**本文只登记该依赖**：A25 的凭据段形状等 A02 决定；A25 其余段（项目/环境/健康检查前台）不因此等待。
- **平台语料**："Prepare your Mac" 等 stage 文案的 Linux 形态；触终端设置文本=**对抗性评审门**（AGENTS：80/40 列、有无色、真实输出、评审至接受）。
- **验收映射**：A04-S2 剧本 B5–B10+⑤ mask 红线数据已移交 A25（v0.1.59），本文不重复其内容、确认其效力；持久化监督收窄句由 A25 真机段闭合或再转写（规则=收窄不删）。
- **运行域**：遵 Q8——A25 真机验收域=定向机（持久外部效应逐项点名）。

## 4. 不变量（移植时不许漂移的既有语义）
两级拒语的存在与**分层语义**（OS 门 vs 容器门；exit 2=usage/environment）；验证器平台感知断言随平台集变化转写而非删除；`mc` 永不以动词形态出现在操作者指导里；A24 三域矩阵口径（host 放行/agent·helper 拒绝）在向导入口上同样成立——**操作者主机是唯一的 setup 域**。

## 5. 状态
义务=本文一次交付（轮九 Q8）。A25 仍 BLOCKED-on-decision（挂 A02 决议，形状=轮六 Q6(b)）；L02-T1 剔除 handover 的拓扑理由与本文 §2-2 同族（无对应物/无第二权威）。不产生任何票态变化。
