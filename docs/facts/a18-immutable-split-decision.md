# A18 决策记录 · ACTIVE/SNAPSHOT 只读拆分（C1，已实施于 shz/TA18）

- 决定：C1（部署所有者裁定，2026-11-19 晚；C2 写者静默见证被否——未定义未验证的见证协议只是替换另一种推断；B 标量检查被否——改变 G-A 证明对象）。
- 语义：`substrate.OpenReadOnly`=ACTIVE（mode=ro，只读挂载无 sidecar **诚实失败**，永不退化 immutable——"现在没有 -wal"不证明"读取期间没有写者"）；`substrate.OpenSnapshotReadOnly`=唯一 immutable 入口，**前置条件归调用方**（快照已完成、读生命周期内无写者触碰文件与 sidecar；"传入了路径"不构成不可变性证明）。容器只读挂载的可用性=挂载布局义务（A16），不是驱动开关。
- 调用方分类（亲读）：ACTIVE=onboard.go inspectSpine、release_database.go（宿主可写挂载，不触回退）、init.go OpenSpineRead（全部读动词）；SNAPSHOT=spine_transfer.go snapshotInspection（已迁显式入口，三处 stage 调用均为复制件）。
- 测试矩阵（双向串线反证，含 owner 补的"可观察行为"检查）：ACTIVE 面对无 sidecar 只读脊→拒绝（substrate+verbs 两层，容器读动词 ActivityList/TaskList 亲测）；SNAPSHOT 面对同一文件→读成功+query_only 仍立；**ACTIVE 见活 WAL 提交**（原反陈旧测试原样保留——若 ACTIVE 再被路由到 immutable 即红）；两侧均不产生 sidecar；结构守卫更新为"两个具名共享入口"，手写 DSN/mode=ro 封禁不变。
- 波及面（快线实测）：3 处耦合（2 行为断言 + 1 源码守卫），分支全链 exit 0。
- **窗口定性（经 A21 docker 实测修正）**：生产布局（agent/helper 共享 rw spine 卷、同 uid 10002、db 文件 0600、DSN mode=ro+query_only）**不触发**该窗口——A21 联合测试 `TestA21AgentUserSpineReadsOnProductionLayout`（docker_boundary）实证：uid 10002 容器在无 sidecar 脊上 ACTIVE 读成功、db 字节与模式零变化；:ro 变体挂载则按 C1 设计**诚实失败**（同测试反向断言，非排除项）。窗口只存在于假设的 :ro 布局，非现行生产。e2e 全链路（真实 dispatch 起的 agent 容器）仍待 A03/A04 Linux 面落地后作为 F0a 验收执行。
- **引用勘误（本分支内自查）**：本文早期草稿及会话记录中"挂载布局义务=A16"为**错标**——A16 真实票面是资源限制（disk/fd/log + T1/T2 参数表）。sidecar 可写挂载义务当前**无工单承载**；承载裁决（新开工单或并入 A03/A04 Linux 移植面/G-B launch envelope 线）与相应授权归部署所有者。
- 合并排序：待部署所有者点名——架构侧建议为**移除与替代同批合并**（不单独让 dev 携带读可用性回归）；联合复核点=替代挂载布局有实际设计与测试证据，而非仅设计文档完成。
