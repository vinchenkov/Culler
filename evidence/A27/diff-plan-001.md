# A27 diff-plan #1（先列后改：更新校验读路径与诊断面修复——DEFECT-003 处置）

**状态=立票文本，代码未授权**（轮九 Q1："批准 A27 设计范围及立票，不授权代码实施"）。激活需所有者明示进码令（A26/003A 先例）。正清单超出=STOP-1。

## 0. 缺陷与已批形状（指针）
`evidence/A03/DEFECT-REGISTER-003-v2.md`：三处生产位点以 DSN `mode=ro`（无 `immutable=1`）打开"已停写的 WAL 脊**副本/静置脊**"，其 `-shm` 本不存在 ⇒ 只读连接需建 `-shm`=一次写 ⇒ `READONLY(1544)`/`CANTOPEN(14)`；`mode=ro&immutable=1` 两形皆通（四态表在其 §2）。已批=R1（按位点改适用读径）+R3（诊断面）+入口隔离守卫；R2 拒；不寄居 L02。

## 1. 正清单（候选文件；实施时逐文件按此表，超出即停）
| 文件 | 意图 |
|---|---|
| `mc/substrate/substrate.go` | 仅当需要：为"稳定快照 vs 活脊"提供**显式命名**的读入口（现有 `OpenReadOnly` / `OpenSnapshotReadOnly` 两入口已具形状，优先不加新码）。**零 schema、零 wire、零 envelope 键** |
| `mc/verbs/onboard.go`（`inspectSpineReadOnly:343`） | 读径选择**参数化/入口拆分**：调用方声明所读对象类（活脊 vs 稳定副本）；两形语义在函数注释钉死（活脊=mode=ro 带 sidecar 在场前提；副本=snapshot 形）。R3：`Domainf` 消息并入底层 err 的**可判别成因**（脱敏，不外泄路径外的值） |
| `mc/verbs/release_database.go`（`InspectReleaseDatabase`） | 同上拆分：检查点/恢复侧调用传"副本"类；安装侧传"静置活脊"类的语义逐字写明 |
| `mc/verbs/onboard_spine.go`（`InspectDeployedDatabase`） | 同上；redeploy 前门（部署**在跑**）保持活脊读形=行为不变量，须有测试钉住（见 §2-D3） |
| `mc/deployment/update_checkpoint.go`（`checkUpdateSpine:634`，含 `:640` 判定） | 走"副本"类读径；R3：`:640` 不再把 err/解析/schema 不等/empty 四因压成一句——消息带成因类标签 |
| `mc/cmd/mc/setup_published_prod.go`（`:758-760` 调用形） | 读径按 §1-verbs 的新类声明传递；挂载形**不改**（readonly 挂载被证明非触发因，动它=无收益扰动） |
| `mc/cmd/mc/redeploy_check_prod.go`（`:100-104`、`:112`） | 行为不变（活脊读），仅 R3 消息面；若诊断证明安装静置态亦需副本类，停并呈报（设计决定，不擅改） |
| 上列各文件的既有测试 + §2 新测试 | 见 §2 |
| `mc/deployment/update_checkpoint_docker_test.go` | 既有唯一用例转绿=本票必过；新增检查点副本读径的定向断言（不新增标签，仍 `update_docker`） |

**禁触区**：`mc/dispatch/`、`mc/substrate/schema.sql|delivery.sql|environments.sql`、`mc/verbs/dispatchseam.go`、任何 envelope/wire 形状、runner/resident/dashboard 侧、`openwiki/`、发布工作流文件。

## 2. 测试义务（所有者追加的守卫要求，逐条为硬验收）
- **D1 正控**：检查点/恢复副本（WAL 形、无 sidecar、只读挂载形）经"副本"类读径**可读**，schema/uuid/empty 判定如 v2 四态表 B/G/L 诸态。
- **D2 入口隔离（反向）**：**活脊误走 `OpenSnapshotReadOnly` 必须不可达**——漂移守卫（源守卫家族同式）枚举快照入口的全部调用点，白名单外即红；配一例植入错误调用→守卫必红（负控，R1 假阳性教训）。
- **D3 活脊路径不变量**：活脊（sidecar 在场）仍走 `mode=ro` 活读且成功；redeploy 前门用例断言其调用类=活脊（防"顺手全改快照"=静默漏已提交帧的风险面）。
- **D4 误路由可失败**：把副本类标错为活脊类（或反之）时，至少一个测试确定性失败（矩阵两侧都要有牙）。
- **D5 R3 判别性**：构造两类不同成因（CANTOPEN vs schema 不等）同点位失败，断言消息**不同类标签**（只断言类别，不断言整句，防脆）。
- **车道**：`update_docker` 为本票**必跑验收道**（轮九 Q1 批准入票面）；合并前跑全道（mc+resident，在册硬规则）；`-tags docker_boundary` 复跑；四态回归表随 §7.1 记录（值表形如 v2 §2，两宿主）。

## 3. 证据与登记
§7.1 记录 ID=`A27-V-*`；AM 同轮登记（双面哈希）；目标机复跑按 Q8 运行域口径（定向机或 C-A 后的 CI，本机 Desktop 不出 PASS）。
