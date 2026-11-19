# A18 决策记录 · ACTIVE/SNAPSHOT 只读拆分（C1，已实施于 shz/TA18）

- 决定：C1（部署所有者裁定，2026-11-19 晚；C2 写者静默见证被否——未定义未验证的见证协议只是替换另一种推断；B 标量检查被否——改变 G-A 证明对象）。
- 语义：`substrate.OpenReadOnly`=ACTIVE（mode=ro，只读挂载无 sidecar **诚实失败**，永不退化 immutable——"现在没有 -wal"不证明"读取期间没有写者"）；`substrate.OpenSnapshotReadOnly`=唯一 immutable 入口，**前置条件归调用方**（快照已完成、读生命周期内无写者触碰文件与 sidecar；"传入了路径"不构成不可变性证明）。容器只读挂载的可用性=挂载布局义务（A16），不是驱动开关。
- 调用方分类（亲读）：ACTIVE=onboard.go inspectSpine、release_database.go（宿主可写挂载，不触回退）、init.go OpenSpineRead（全部读动词）；SNAPSHOT=spine_transfer.go snapshotInspection（已迁显式入口，三处 stage 调用均为复制件）。
- 测试矩阵（双向串线反证，含 owner 补的"可观察行为"检查）：ACTIVE 面对无 sidecar 只读脊→拒绝（substrate+verbs 两层，容器读动词 ActivityList/TaskList 亲测）；SNAPSHOT 面对同一文件→读成功+query_only 仍立；**ACTIVE 见活 WAL 提交**（原反陈旧测试原样保留——若 ACTIVE 再被路由到 immutable 即红）；两侧均不产生 sidecar；结构守卫更新为"两个具名共享入口"，手写 DSN/mode=ro 封禁不变。
- 波及面（快线实测）：3 处耦合（2 行为断言 + 1 源码守卫），分支全链 exit 0。
- **显式窗口（不隐藏）**：A16 未落地前，agent 容器在只读挂载无 sidecar 状态下的 spine 读为**预期失败**——测试以断言承载，非排除项；容器边界真实验证（docker_boundary/e2e）因 Docker 环境断连为 **UNVERIFIED**，恢复后必须补跑，A18 在补跑前不得称容器面已验证。
- 合并排序：待部署所有者于 (a) 现在合入 dev / (b) 与 A16 同批合（host 推荐：修复与移除一起落地，主干任何时刻不携带裸回归）/ (c) 等 Docker 验证后合 之间点名。
