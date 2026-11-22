# L02 设计件 #1（在线升级语义：drain/handover 评估——计划内重启与崩溃的协议区分）

状态：**设计评估件（PROPOSED）**。L02 票面=设计先行（Q10：dep A03 满足≠自动获实施权）；本文一切触 dispatch/wire/发布形状的选项**只列不裁**（§6）。零生产码改动。
锚：ANALYSIS-2026-11-19-LoopForge-vs-Culler.md §4-8/§8-5/§9-6；IMPL-AUTH-003 附五（真机验收脚本五要件）；A13（envelope 全等绑定，dev 已 EVIDENCED）。

## 1. 现状事实（E1 读源，行级锚点）
**升级链**：`culler update`（culler_prod.go:281/:639）→ 恢复优先（runRetainedUpdate，setup_update_transaction_prod.go:449）→ 下载/校验（install.sh→release/installer.sh）→ 记账事务：journal 相位机 `stop→prepare→checkpoint→replace→verify→committed→complete`（+`restore/restored`）（deployment/update_journal.go:19，落盘 `home/updates/transaction.json` 0600 原子写 :125-144）→ 中断确认闸 `--interrupt-runs`（:562-564）+面板语"This update interrupts active runs."（:547）→ 停（update_stop.go:194-295：维护门先行→supervisor stop 三单元→宿主进程 SIGTERM→10s SIGKILL→容器 `docker stop --time 10`→kill；30s 硬限）→ 换二进制（os.Rename 原子）→ 段落重放+服务核验 60s/单元 → 提交/清理。
**恢复语义**：committed 前失败=retry/restore 面板（"A committed update never restores its old checkpoint over current data."UPDATES.md:67）；未完成 journal 保持工作闸关（"An incomplete update keeps Culler stopped…"update_journal.go:152）；update_docker lane 已有**中断升级检查点测试**（TestUpdateCheckpointDocker，openwiki/tests/checks-and-test-lanes.md:429-433：scratch spine+可选运行时恢复，不激活宿主服务）。
**生命周期的既有耐受面（本案的支点）**：
- **agent run 租约跨 resident 重启存活**——runner 在容器内心跳（verbs/runner.go:10-30，僵尸租约被 fencing），resident 死亡期间容器照常推进并可**直接向脊写终态**（`mc complete`，README.md:234-237）；回收只在任一 resident 回归后由 tick 记 `reported-outcome`（dispatch.go:264-288）。
- 计划/崩溃的协议区分**已存在一半**：`ReapVMRestart`（BootID 换=系统事件，不扣额 dispatch.go:272-274/:291-293）、操作者取消=`interrupted` 不扣额（:279-287）、安装暂停请求=resident 停 tick（README.md:228-230；repository-lane.ts:196 none/drain/confirmed）。
- **缺的另一半**：控制面"计划内重启"无专用标记——仓库操作被 resident 重启打断记 `interrupted`（owner 变更），**第二次连续 owner 变更起扣任务的账**（README.md:447-448）：一次例行升级撞上一次故障=双 owner 变更=把计划内事件计入失败预算。
- dashboard 无状态+轮询（main.ts:4-6，"browser reconnects"），resident 无监听 fd 交接需求；**LoopForge 的 SIGHUP listener fd handover 在 Culler 拓扑里无对应物**（NOT FOUND=handover 概念全仓零命中）。

## 2. 结论（先给）
§9-6 的两半在 Culler 不对称：
- **fd handover：不需要**。单写者=短命 `mc` 进程+flock+BEGIN IMMEDIATE（dispatchverb.go:24-26/:322-326），无常驻监听面可交接；照抄=为先进而先进（AGENTS 禁则）。**建议从 L02 范围正式剔除**（D-A 裁此项）。
- **drain：真缺口，但缺口比 LOOPFORGE 叙事窄**。Culler 缺的不是"运行不能跨重启"（agent run 已经能），而是：①升级**主动放弃**了这份耐受性——stop 段无差别 docker-stop 全部部署容器；②"计划内重启"无协议身份，仓库操作计费与 journal 恢复叙事都按崩溃处理；③操作者只有"现在中断"一个选项，无"排空到期限再中断"。
- **"让旧 run 在新宿主下活完"不是免费热升级**：A13 已裁定混版装载 fail-closed（envelope release_build_id 全等拒绝）——镜像内 agent mc 是旧构建，新 resident 签发的信封会被其正确拒绝。故真·在线交接需要**按 run 版本偏斜的容忍策略=wire/治理决定**，且与 A13 的既定方向相反。L02 的价值定性=**把"排空后替换"做成制度**（旧 run 在旧契约下自然终态，新 run 在新契约下出生），**不是**把混版运行做成制度。

## 3. 选项（只列不裁）
| 案 | 内容 | 触面 | 代价/风险 |
|---|---|---|---|
| **D-A** handover 剔除 | §9-6 承接范围改写为"仅 drain 语义"，fd handover 登记为不适用+理由（§1/§2 事实为据） | 票面文字（本设计件+DEV-BASELINE L02 行） | 无；防后人照抄 |
| **D-B** 排空闸（票内核心候选） | 升级在 `--interrupt-runs` 前加"drain 模式"：置**既有**安装暂停/维护门→dispatch 停 spawn（run 不新增）→等活跃 run 自然终态至**期限 N**→到期回退现状中断路径（含既有记账/恢复链） | 复用 install-pause+maintenance gate 两既有机制；**零新状态词、零 wire**；期限参数=配置面 | 长 run 可把升级拖到期限；期限后体验≈现状 |
| **D-C** 计划重启标记 | 控制面计划内重启入协议：owner 变更遇升级 journal=不记崩溃性 `interrupted` 计费（第二次连续变更豁免），journal 相位入证据 | 触 `mc/domain/execution.go` 判定+Decide 输入=**wire-adjacent** | 语义要防"崩溃伪装计划"——标记源只能是 journal 事务自身（有相位+摘要），不可新造权威 |
| **D-D** 版本偏斜窗口（远期，默认拒绝） | 让在途 run 跨一个发布存活=信封按出生版本全等（改"全等"为"出生时钉死+跨重启保持"）+旧镜像驻留至 run 终态 | **翻 A13 已批形状**=违反附七 cond5 精神 | 混版窗口=审计/安全面回退；除非真机数据证明排空成本不可接受，不呈 |

## 4. ×A03/A17/更新面接缝
- 监督语义不改（Restart=always/RestartSec=10 与 KeepAlive 对镜 supervision_systemd.go:74/supervision_darwin.go:62-69）；drain 是**升级编排层**（setup_update_transaction）语义，不是单元语义。
- journal 相位机是天然承载点：D-B 在 `stop` 前插"等待"属**流程内步骤**还是**新相位词**=形状决定（新相位词触 update_journal.go 值集+UPDATES.md 操作者面 8 相位表=登记同步义务）。
- "计划内 vs 崩溃"的证据要求与 §7.1/附五一致：演练必须在 update_docker lane（已有检查点用例家族）+定向机持久段出原始块，**本机 Docker Desktop=运行域不满足（Q8 口径），不得出 PASS**。

## 5. 验收映射预告（F1 实施票时细化）
①update_docker lane 新用例：drain 期限内活跃 run 自然终态→零 `interrupted` 记录；到期→现状中断路径行为不回归（含 __update-reconcile 记账）。②journal 崩溃点矩阵在 drain 态重跑（既有用例族）。③D-C 若批：仓库操作计费断言（计划内双变更不扣额/崩溃双变更扣额）。④红绿样例入 fast suite（drain 闸的开关态）。⑤A13 不回归：混版拒绝在升级前后各验一次。

## 6. 待所有者形状决定
- **T-1** 范围裁定：D-A 剔除 handover、L02=drain 语义票（执行侧建议：是）。
- **T-2** D-B 排空闸采纳与期限数值（数值=安全/运维参数，归所有者）。
- **T-3** D-C 计划标记是否立项（wire-adjacent；若立=独立小票+正清单，不并入 D-B）。
- **T-4** 新相位词 vs 流程内步骤（若 T-2 采纳则需答）。
- **T-5** 实施票授权时机（现在/挂 F0a 后；update_docker 与 A25 持久段同域，捆绑跑可摊薄真机成本）。

## 7. 状态
设计评估件=本文件；L02 转"评估件已交、待 T-1..T-5"。L03 另票另件。实施零授权状态不变。
