# V 清单组建稿 #1（A14-F1 纸面件；执行硬前置=A16 EVIDENCED + 所有者环境事实批准轮）

依据：TM §7.2 V-1a–d/2/3/5(+X-1)/6/7、§7.3 绑定表（任务态与 verdict 两维——本组建稿只把"任务已建立"记账，零"验证已通过"含义）。环境事实来源：docs/facts/o3-environment-probe-001.md（执行侧 E1）；§7.3 标注"由部署所有者提供"的格子=本单 OPEN 队列，非执行侧可自采。

## 1. 逐格建立卡（任务→脚本形态→绑定事实→缺口）
| V | 建立形态（acceptor 五要件同规） | 已可得事实 | 所有者/其他缺口 |
|---|---|---|---|
| V-1a 配置发现 | 容器内四源探针套件：HOME(tmpfs)/cwd 上溯/项目 `.claude/.codex/.mcp.json`/显式 env；逐 harness family（claude/codex/opencode/fake）×镜像内 CLI 钉版本；真实 CLI 在镜像=/mc-prod 已含（A04②），非模拟 | 镜像工具版本=project-manifest（实效快照已入册） | 恶意配置样本=本稿 §2 附件清单（可纸面备妥） |
| V-1b Hook 执行 | 恶意 hook 样本入 repo 夹具→T2 容器 run→断言未执行（探针落笔法） | 同 V-1a | T2 档激活=依赖 A16 t2 表配置（已具备机制）|
| V-1c MCP 注册 | 仓库自带 .mcp.json→运行→平台权限 MCP 注册不发生断言（B7 声明制）| run.json 投影面（A15 F1 事实卡） | — |
| V-1d 启动顺序 | tmpfs/剥离/白名单生效 先于 不可信配置加载：容器内首进程即探针（时序=entrypoint 链证据） | Dockerfile 装配次序（A04 票内可引用） | — |
| V-2 跨 run 残留 | 投毒→换 run→读取攻击链于 /home/agent(tmpfs size 后=更强) 与 /cache | cache 限额=既有 CULLER_CACHE_LIMIT_BYTES | 无 |
| V-3 宿主 git | 恶意仓库 config+hooksPath+credential helper 在场→宿主 push 不执行；push 用配置隔离矩阵 | O3 git=system+global 零配置（探针事实） | 批准 remote=所有者（P1-3 面） |
| V-4 Collie 附件渲染 | 注入样本→dashboard/附件浏览器渲染路径断言（dashboard smoke 家族扩展，浏览器真渲染） | smoke 基建在位 | 无 |
| V-5 沙箱→宿主 | socket/host.docker.internal/loopback 各服务实测+鉴权实况（loopback≠免鉴权）；与 A19 合流，X-1 三场景 | docker 29.8/containerd/网络=O3 卡；服务清单=部署后实测 | 部署实跑=持久段（A25 移交段）——**V-5 真实执行顺位在其后** |
| V-6 egress 六用例 | 代理绕行/直连 IP/域名→私网/DNS rebinding/IPv6+mapped/重定向改道，整体命名空间+代理+宿主转发链 | provider-proxy 装配在码 | **P2a=egress enforcement 未完成**（TM §5.1 三态+[P2a] 标记）——V-6 完整六例现在必红于"直连"维：任务建立但按 §7.3 状态=NOT_STARTED→执行需与 PS/P2a 合流，本票不假装 |
| V-7 明文暴露面 | RR-5b 三级分层：类①跨 UID 读矩阵（容器 uid 10002 vs 宿主服务 uid）；类②同 UID=需进程隔离证据（mode 不算过）；类③ root/daemon 不作补偿验证 | 双 uid 设计事实（run.json+setuid） | 采样 run+诊断清单+备份路径=A16 资源轮可同场采集 |

## 2. 样本与夹具清单（组建产物，不含执行结论）
恶意配置样本目录规格：`evidence/A14/fixtures/`（.claude/settings.json 含 hook 命令=落笔探针、.mcp.json 声明平台权限 MCP、gitconfig+hooksPath+fake credential helper、cwd 上溯样本 .codex/config.toml）。探针=写 `/mc/probe/<name>.was-executed` 断言缺在。

## 3. 排程绑定
执行=218 真实往返（每格一 acceptor 脚本，五要件+磁盘预检同 A04 模式）⇒ **所有者点名轮**；建议与 A16 资源耗尽矩阵（四维+spine 零污染对照+envelope 活证）**同一轮**跑（共享部署实态，减少生产机往返数）；V-5/V-2 依赖持久部署态=与 A25 段合流或 A16 轮内用私有动词+helper 正规路径（届时按向导可用的形态定，不再复刻第二权威）。
**X-1 三场景**（§7.2 注）随 V-5 附件：同镜像异 release_build_id 的 tick fail-closed 已 A04 面覆盖，余两场景列入 V-5 卡。

## 4. 状态词汇（§7.3）
本稿后 A14 状态=组建完成（纸面），执行=NOT_STARTED；A16 EVIDENCED + 所有者点名轮 = 进入 RUNNING 的前置。任何格 PASS 判定只经 §7.1 记录+独立复核路径，不因本稿存在而预支。
