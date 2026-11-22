# L01 设计件 #1（SOP 式"脚本节点+产物契约"进 Worker/Verifier 验收语义）

状态：**设计提案（PROPOSED）**。L01 票面=设计件先行；本文触任务验收/dispatch 语义的一切形状决定**只列不裁**（§6 S-1..S-5 归所有者）。零生产码改动；实施需批准后的独立票动作。
锚：ANALYSIS-2026-11-19-LoopForge-vs-Culler.md §9-5/§8-5/§3.4；IMPL-AUTH-003 附九词汇表（CODED/EVIDENCED 分界）；AM 决策解锁模式。

## 1. 问题（Culler 现状缺口，E1 读源）
- 验收标准现为 **tasks.description 内散文**（substrate/schema.sql:165-176 无结构化验收字段；brief.go:322 把"描述即验收标准"发给角色）。
- Verifier 指令（directives/verifier.md:7/19）：**由模型**"对每条验收标准取得自己的结果"——确定性可判的条目（测试过、lint 过、脚本退出码）也在靠模型"跑一遍再读输出"判定 = 不可复现的判定层，恰是 LoopForge 用 Script 节点消灭的那类不确定性（§3.4/§9-5"确定性检查不靠模型"）。
- **Culler 已有事实上的脚本节点+产物契约，只是未成制度**：`mc/check.sh`/`runner/*/check.sh` 家族（出口码+ok-marker 双检纪律）、blcheck、以及 003 附五**真机验收脚本五要件**（逐字头句+前提+机器判据 token+ts/host/uid 原始块+自清）——A03/A04/A16 全部 EVIDENCED 的脊柱就是这些脚本，不是模型叙述。L01 = 把这套已被验证的形态**升格为 task 验收语义的一等公民**，不新造哲学。

## 2. 节点语义（"确定性检查节点"= 三件套）
| 要素 | 定义 | 既有对应物 |
|---|---|---|
| **脚本** | 任务受理时冻结的可执行体：仓库内路径 + 解释器固定（`sh <path>`/`mise exec -- …`）+ 参数封闭（无模型注入面） | check.sh 家族；附五验收器 |
| **判定** | **退出码 + 声明式 ok-marker**（如行 `ACCEPT: PASS`）双条件=PASS；单条件不作数 | "绿=exit0 且 marker"纪律（R1 假阳性教训的制度来源） |
| **产物契约** | 每次执行留**原始块**（ts/host/uid 前缀输出+退出码逐行）入 run 证据面；模型只**引用**字节，不转写结论 | 附五要件④ + evidence/* 惯例；§7.1 记录绑定 |
非目标（守"抄语义不抄界面"）：不建 DAG 引擎、不做 SOP 设计器/子 SOP/模板市场——Culler 的线性流水线（Strategist→Arbiter→Worker→Verifier→Packager）已承载节点次序；L01 只把"每条可确定性化的验收标准=一个脚本节点"塞进现有阶段。

## 3. ×任务生命周期映射（现有角色面上，零新角色）
1. **Strategist/Operator 立任务**：可确定性化的标准写成检查脚本随任务提交（路径+预期 marker）；散文标准保留给主观判定。**谁能写脚本=信任边界，见 §5**。
2. **Worker**：交付前自跑检查（现状 check 家族的 task 化），把原始块随提交物带上（自证性质不变——自证≠验收，纪律在册）。
3. **Verifier**：判定拆两层——**机器层=重放脚本节点，判定权在退出码+marker，不在模型**；模型层=只对无脚本覆盖的主观标准判定，且必须引用重放原始块字节（cmp 一致）而非 Worker 转述。证据文件逐条 = {标准, 脚本或主观, 重放退出码, 原始块指纹}。
4. **Packager/操作者**：评审包首屏=机器判定汇总表（全绿/红+红因），模型叙述降为附注。
5. **dispatch 语义影响**：run 终态词汇**不新增**（PASS/FAIL 走现有 outcome 通道）；脚本失败=Verifier 报 concrete evidence 现状路径。**除非** S-3 裁"机器检查进 gates 前置"——那是 wire 形状决定。

## 4. 与"受控改进"（runeval 式）的边界（P3 方向，本票零授权）
LoopForge runeval=run 复盘→蒸馏**有界事实**→生成 SOP/角色改进建议→**人工 apply 才落目录**（apply.go:10-36）。Culler 对应物=AM 演进治理（候选→隔离评测→批准→晋升→回滚）。L01 只预留接缝：run 证据面的检查脚本失败模式可蒸馏为**检查候选**（如"该 task 类总漏检 X"→建议新脚本节点入任务模板），**候选不得自动生效**，走既有演进批准链。本票不实施任何自动蒸馏。

## 5. 信任边界（核心不变量，先于形状）
- **检查脚本属 requirements 面，不属实现面**：Worker（模型）不得在 run 内新增/修改自己的检查脚本或 marker（=自证通道，A13/V-1a "model authoring its own configuration" 同族 hazard）。脚本随任务 requirements_version 冻结；Worker 只能在评审包**建议**增改，采纳=新 requirements_version（人/Strategist 动作）。
- marker 字符串与脚本路径的声明也在冻结面内（防 marker 漂移造假）。
- 解释器封闭：脚本执行环境=现有 runner 容器（A16 资源帽+tmpfs 掩码下），不给检查节点新增权限面。

## 6. 待所有者形状决定（本票全部不动码的原因）
- **S-1 载体**：检查契约放哪——(a) tasks 新列=**schema 变更**，撞"spine 不兼容无转换器"硬闸（C03/B01 教训在册：迁移器基线未有）；(b) description 结构化小节（零 schema，靠解析纪律）；(c) **仓库侧清单文件**（`.culler/checks.json` 随 requirements 冻结，schema 零触面，但引入"仓库内容=需求面"的信任讨论）。**执行侧倾向 (c)+§5 冻结条款**，决定归所有者。
- **S-2 阶段面**：机器重放放 Verifier 内（改动小）还是独立检查步骤（新状态词=wire 决定）。倾向 Verifier 内，归所有者。
- **S-3 dispatch**：机器检查失败是否前移为 gates/派发前置（触 Decide 输入=wire 形状）。倾向不（Verifier 判定层足够），归所有者。
- **S-4 agent 输入面变更清单批准**（实施票时执行，AGENTS 规）：directives worker/verifier/packager.md、brief.go 任务段、roles skills 目录、GIT-DELIVERY 注记。**变更清单预览**：Worker+="按任务冻结面自跑检查并附原始块，不得改检查"；Verifier+="先重放机器判定并 cmp 字节，主观项才自评"；Packager="评审包首屏机器判定汇总表"。
- **S-5 范围裁可**：本设计件只裁"语义采纳"；实施票（L01-F1：确定性检查执行测试+红绿样例）是否现在授权，或挂 F0a 后。

## 7. 验收映射（F1 实施票预告）
红绿样例（植入必红/清除必绿）入 fast suite；requirements 冻结面防漂移守卫（源守卫家族同式）；Verifier 证据文件形状用例；（若 S-1 选 (a) 则整体挂 B01 迁移器后——依赖硬闸不是选项）。

## 8. 状态
设计件=本文件，L01 转"设计件已交、待所有者 S-1..S-5"。零码、零 wire 触面、零 agent 输入面改动。下一步不自动产生：实施需批准。
**2026-11-22 轮九 Q5：S-1..S-5 全裁（=c / Verifier 内 / 不前移 / 形状批准 / 实施挂 F0a 预清障后）——本文件 §6 就此闭合，§7 验收映射按 (c) 载体生效，实施仍待独立票与授权。**
