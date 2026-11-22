# A27 STOP-1 呈报：已停写读径的"意图如何跨容器边界传达"是设计决定，未擅改

依据：所有者 Q1 亲自加的守卫（"不能只证明三处调用已改成快照读；还须证明活脊不能误走快照入口；反向测试能在错误路由发生时失败"）+ 003 纪律"未证明不得当已证明""超出正清单=停"。实施到读径选择这一步，证据推翻了 v1/v2 "三处同一处理"的前提，必须回呈，不得在三个文件里猜一个 verb 形状。

## 一、已交付且零设计争议的一片（已在票分支，未并 dev）
提交 `73f3909`：`checkUpdateSpine` 不再把四类成因（容器跑不起来 / 返回非预期记录 / schema 不符 / 空表）压成一句 "integrity or schema check"，改为分支各自带因。`update_docker` 道重跑现如实报 `unable to open database file (14)`（CANTOPEN）而非伪装 schema 失败——**缺陷仍红，但从"哑红"变"诚实红"**，正是 R3 的目的。untagged `mc/deployment` 40.9s 绿；无在册测试钉旧字符串（仅我方新注释含之）；未改 `verbs/release_database.go:57` 那条真·integrity 消息（非我范围）。

## 二、三处并非同一风险类（证据）
| site | 调用形 | 读取对象 | 是否有并发写者 | 快照读是否"适用" |
|---|---|---|---|---|
| 1 更新检查点 `update_checkpoint.go:635 checkUpdateSpine` | 容器内 `mc __release-database`，挂**私有备份卷** `name-update-<id>`（cp -a 产物） | 隔离副本，**结构上无第二写者** | 否（update 流 stop 段先于 checkpoint） | **是**——唯一"必然可证稳"的一处 |
| 2 安装/更新门 `setup_published_prod.go:758` | 容器内 `mc __release-database`，挂**活脊卷**只读 | p.database() 有**三个**调用点：update 路径(638，stopForUpdate 之后=静置)、499/813(新装/修复，**静置性未逐点证明**) | 混合 | **分路径**——638 与 site1 同类；499/813 需先证明或维持活读+absent 短路 |
| 3 redeploy 前门 `redeploy_check_prod.go:100` | 容器内 `mc __database-schema`，函数自述"while the deployment runs" | **活脊，可能真有并发写者** | 是（tick 间隙可能无 sidecar，写时又有） | **否**——immutable 读可能在写者活跃时静默漏帧，正是守卫禁的错误路由 |

**关键反证（用本 fork 自己的 G 案，无需新测）**：DEFECT-003-v2 §G 已证"sidecar 在场 + 只读挂载 + mode=ro = exit 0 成功"。⇒ site 3 在"部署真在跑（写者持文件）"态本就能读；它 v2 标的"红 D 案"是**合成的无-sidecar 夹具**，不代表真运行态。把 site 3 翻成快照读 = 制造守卫明令禁止的"活脊误走快照"。site 3 的真实残余是**tick 静置窗口可能无 sidecar→门偶发红**，但那是"检查前须确保静置"的**编排/时序**问题（L02 域），不是"换 immutable"能安全解的。

## 三、site 1 转正 green 需要一个设计选择（未擅定）
site 1 的读发生在**发布镜像内的 mc 进程**里（容器边界内侧），而仓库内**已证明正确的稳态读者** `verbs/spine_transfer.go:snapshotInspection→substrate.OpenSnapshotReadOnly` 是**进程内**、对 host 侧 stagePath 操作——跨不过容器。要让"稳态"意图进到容器内那次读，三条路各有代价：
- **V-α 新私有动词** `__verify-spine-snapshot`（只读、不写脊、不触 dispatch/wire）→ site 1 调它走 snapshotInspection。**致命伤**：`checkUpdateSpine` 以 `j.Previous.Image`（**上一发布**镜像，:255）起容器——镜像内 mc 是**旧二进制**，预知不了任何新动词；混合版更新时 site 1 正是主场景 → **V-α 在真实更新路径上直接不可用**（不只是"新增面"）。
- **V-β `__release-database` 加 flag**：同一致命伤——旧镜像 mc 不认新 flag；且混合版下"新宿主 mc 传 flag、旧镜像 mc 收到未知参数"行为不可控。（更正：`setup_published_prod_test.go:117` 是 `Contains(...mc __release-database)` 子串匹配，加尾 flag 本身不破该断言——我先前把它当拦路虎，判据找错了；真正的否决在版本偏斜侧。）
- **V-γ host 侧落暂存文件后进程内快照读**（复用 site-restore 已证的 stagePath 形）：checkUpdateSpine 改为先把备份卷内容取到 host 暂存、再由**当前新代码的进程内** snapshotInspection 校验。**不受镜像版本偏斜影响**（读在 host 新 mc 里做，不经旧镜像）、**零新 verb、零 wire、复用已证原语**。代价=多一次卷→host 暂存的 I/O（与既有卷→卷拷贝量级相近）。

**执行侧建议 = V-γ，且近乎唯一正确解**：V-α/V-β 因"site 1 跑旧镜像 mc"这一**版本偏斜硬事实**在真实更新路径上根本不可用；V-γ 把稳态读移到宿主新进程，天然规避。此判断由 `j.Previous.Image` 的取证（:255）驱动，非偏好。

**推广检验（自我否证用）**：site 2 的 `p.database()` 用 `p.record.Image`（当前发布镜像，:760）——无版本偏斜，故 V-α/V-β 在 site 2 不因偏斜否决；但为最小命令面仍宜统一走 V-γ 思路。**site 3** 同理用当前镜像，但它是活读、根本不进快照类（见 §二）。

## 四、请裁（未动 site 2/3 与 R1 转正，等此答）
1. site 1 转正取 **V-γ / V-α / 其他**？
2. site 2 的 499/813 两调用点：授权我**追证其静置性**（读 update/install 全链定序）再逐点定类，还是先只转 638（与 site 1 同 stop 段）？
3. site 3：确认**维持活读 mode=ro 不翻快照**，其"偶发静置窗口红"作为编排时序问题移交 **L02**（与排空闸同源）——本票不擅自以 immutable 掩盖之？
4. R3 切片（73f3909）是否即并 dev（它独立、诚实化、全 untagged 绿、无钉死断言冲突），还是随 A27 主体一并？

票分支 `shz/TA27i`（基于 dev tip 06ebe48）；dev 主干未受影响、未合并、未 push、未触禁触区（dispatch/SQL/wire/envelope/工作流）。
