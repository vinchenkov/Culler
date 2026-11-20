# A16 diff 计划预登记 #1（003 附三：先列后改；改动超出本清单=STOP-1）

票：A16 资源限制实现 disk/fd/log + T1/T2 强制参数表（爆炸半径≤沙箱）
基线：shz/dev@028c00c（A04 EVIDENCED 态）；blcheck 60 票 0 违规；dep A03✓A04✓
CODED 准入事实（TM §5.2:94 自证 + 本轮盘点 E1）：pids 2048 [固化]；cpu/mem 机制在、默认 0=不限 [GAP]；**disk/fd 零机制**、docker json-file 日志无 max-size、supervision 单元无资源键、**envelope 摘要不含新资源字段**（见证缺口）；目标机事实：/home 与 /var/lib/docker=**ext4 未带 prjquota 挂载选项**，docker=overlayfs/containerd（`--storage-opt size=` 与 xfs 项目配额两条常规路**都不现成可用**）；runner.ndjson 已有 4MiB/run 硬顶+留痕 [固化]；run 树=计数修剪（500）非字节预算。
所有者裁定输入：grill 轮 2026-11-20 Q1(a)=触发开工；本文 D-1..D-5 为**新 frontier**（Q1 不覆盖机制与数值）。

## 政策不变量（全程有效，违者=STOP）
1. **爆炸半径≤该沙箱**（TM §5.2:99）：任何资源耗尽不得连带控制面/spine 可用性；超限行为逐维=写失败/新调用失败/run 以资源类终态终止，**不污染 spine 卷所在文件系统**。
2. **截断必留痕**（Evidence First）：日志配额触顶要留下可见记录，静默丢证据违 TM:99。
3. **fail-closed 默认**：新参数表缺项=拒启动该档，而非静默不限（TM:99 "配置即拒启动" 分支）。
4. **envelope 见证一致性**：新资源标志必须进 digest 构建（collie-envelope 摘要面扩展），否则"设了但没证"——比不设更糟。
5. darwin/现有 T0 语义零回归；`--security-opt no-new-privileges` 在 pipeline 类的**刻意缺席**（boundarydocker landing_envelope_test:198-200 钉死的 setuid 门约束）不得顺手"补严"。

## 设计分叉（D-1..D-5 = frontier，所有者裁定后生效；裁定前本票不动生产代码）
- **D-1 磁盘配额机制**（最难，环境依赖）：
  - **a) ext4 项目配额**：宿主 provisioning 前提（mkfs project 特性 + `prjquota` 挂载选项 + root 配置）=**改宿主环境**，越出部署动作边界；且与"部署不上升权限"姿态冲突。作用域可全覆盖（工作区/日志/临时）。
  - **b) tmpfs 化短生命周期目录**（scratch/tmpfs home 补 size=、logs 的**容器内**写入落 tmpfs）：零宿主前提、ENOSPC 语义天然；但吃宿主 RAM（与容器 mem 限额此消彼长）、workspace 仓库 bind 无法 tmpfs（那是项目数据本体）。
  - **c) 度量看门狗 + 既有字节会计扩展**（resident 对 runs/scratch/cache 目录周期 du，超软限→提前终止 run 为资源类终态；硬限由 b 的 tmpfs 兜底）：无宿主前提、语义="终止换污染为零"；非真 ENOSPC（写者视角不失败而是被终止）——TM:99 要求"写失败**且**资源类终态终止"，c 只满足后半，需裁定 TM 措辞是否接受等价实现（等价性裁定=所有者/契约动作，非执行侧自裁）。
  - **执行侧倾向（非裁定）**：b+c 组合（tmpfs 管日志/临时/短命，看门狗管工作区增长，workspace 本体的"灌满磁盘"面诚实登记为残余——**爆炸半径=宿主共享 FS** 这一条 b/c 不闭合，a 才闭合但需宿主改造）。
- **D-2 fd 上限**：`--ulimit nofile=SOFT:HARD` 全 docker 环境可用、无 FS 依赖（本维无分叉，只有数值）；继承规则=容器默认，harness 子进程随容器。
- **D-3 日志配额**：容器 `--log-driver json-file --log-opt max-size=Nm --log-opt max-file=k`（补 docker 侧无顶缺口）+ runner.ndjson 既有 4MiB 保持 + run 树**字节预算**（计数 500 之外加总字节上限，超限删最旧带留痕）。
- **D-4 T1/T2 强制参数表数值**（TM 刻意留白给 F0a）：每维=cpu(m/nano)/mem/disk-workspace/fd/log-run/log-tree/pids/deadline × {T1,T2} 两组数值。执行侧可呈**建议值表**（基于 503G RAM/多项目并发包络），批准权所有者。
- **D-5 默认策略**：(a) 非零默认（未配置=T2 默认值生效）vs (b) 配置即拒启动（未配置=不可信档禁止 spawn）。TM:99 两分支都列；(b) 更硬但把现网"默认不限"变成"默认不可用"——行为变更面大。执行侧倾向 (a) 默认表 + (b) 用于**显式 T2 部署标记**时。裁定归所有者。

## 允许文件族（裁定后启用；现仅预登记）
`resident/src/environment-lane.ts`（argv 构建+限额读取）、`resident/src/effects.ts`（pipeline/collie spawn+envelope 期望重建）、`resident/src/collie-envelope.ts`（摘要面扩展）、`resident/src/repository.ts`+`resident/src/run-log-prune.ts`（合并容器限额+树字节预算）、`runner/shared/log-sink.ts`（配额交互留痕，如需）、`mc/config/config.go`+`mc/verbs/configsave.go`+`config.example.toml`（参数表键面+校验+**D-5 策略**）、`mc/substrate/dispatch_mount_projection.go`（仅当 D-1 需要计划字段）、`resident/src/*.test.ts`+`mc/config/config_test.go`+`mc/verbs/configsave_test.go`+`mc/boundarydocker/*`（新增断言与转写；**既有默认断言"create 不含 --cpus/--memory"在 D-4/D-5 裁定后按新默认重钉——重钉=契约变更呈报，非为绿而改**）、`evidence/A16/`。
**禁触区**：substrate SQL、dispatch wire、directives/skills、`.github/workflows/*`（A16 无 CI 需求；T1/T2 全矩阵实测属 F0a 验收非 CI）、supervision 渲染器的资源键（**主机进程限额不在本票**——TM 资源行主语=沙箱容器；监督栈自身限额另票另锚，防范围静默扩张）。

## 真机验收（五要件义务）
F0a 判据=TM:177 **资源耗尽场景组逐维**（fork bomb→pids 重验 / 磁盘灌满→新配额 ENOSPC 或资源类终态 / 日志膨胀→上限+留痕 / fd 耗尽→新调用失败）在 218（或等价 ext4 宿主）**从容器内真实触发**并验证：①spine 卷文件系统零污染（前后 statfs/对比）②控制面（resident/dashboard）存活③run 终态与留痕正确。脚本五要件+磁盘预检同 A04 模式；**envelope 见证用例**（标志变更→digest 变化→spawn 拒绝）随行为测试。

## 风险预判与 STOP 映射
- D-1 的 (a) 需宿主改造（越界）；若裁定选 a 系方案 → 宿主动作单独呈 owner，本票只实现**机制适配层**。
- D-4 数值过大=形同虚设，过小=T2 正常任务失败率升——需与 RR-7 Acceptance Condition 的"逐维验证通过"对表；实测矩阵是唯一仲裁。
- envelope 摘要扩展会**使旧 plan 全部失效**（版本化迁移=登记残差：旧在飞 run 的 plan 与新 digest 不匹配 → 终止语义要明说）。
- 14 日时效（v0.1.53 起算）：D 裁定一日未回，链上 A14/A19 顺延一日——已如实回传压力，不制造抢跑理由。
