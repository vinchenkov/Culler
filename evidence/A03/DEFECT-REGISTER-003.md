# Defect register 003 — 三处以只读连接校验"已停写的 WAL 脊副本"必失败（update_docker lane 首跑抓出；v2 已修正机制归因）

状态：**REGISTERED, NOT FIXED**。`mc/deployment/` 的更新检查点代码不在任何现行正清单内（003 主款二的 Wave A 五票与 003A 的 A24 清单一律不含），修复=新授权，本件仅登记。本文件不宣称任何代码已被修复。

发现事件=本 fork **首次**运行 `update_docker` 这条 opt-in lane（此前从 A03 到 A16 各轮一律记 UNVERIFIED 挂账）。用例 `TestUpdateCheckpointDockerRestoresAfterInterruptedVolumeCopy` 在 `update_checkpoint_docker_test.go:92`（即 `CreateUpdateCheckpoint`）失败，报 `The spine checkpoint failed its integrity or schema check.`。

## 位置与机制

- 校验点：`mc/deployment/update_checkpoint.go:634-644` `checkUpdateSpine`，被 `:255` 在拷贝脊卷之后调用。命令形态（`:635` 原样）：
  `docker run --rm <pids-limit> --network none --cap-drop ALL --user 10002:10002 --mount type=volume,src=<backup>,dst=/mc/spine,readonly <image> mc __release-database`
- 容器内：`mc/cmd/mc/private_release_database.go:13` → `verbs.InspectReleaseDatabase`（`verbs/release_database.go:47`）→ **`substrate.OpenReadOnly`**（`substrate/substrate.go:83`）。
- Culler 的脊**永远是 WAL**：`substrate.Open` 设 WAL 模式（`substrate.go:39-40` 附近的连接纪律；脊文件头 18/19 字节=`02 02` 实测在案，取自真实运行留下的 `mc-e2e-spine-140360502` 卷）。
- SQLite 打开一个 WAL 库需要能建立 `-shm`；卷以 **readonly** 挂载 ⇒ 无法建立 ⇒ `SQLITE_CANTOPEN (14)`。删除模式（`01 01`）的同一文件同一命令则成功。
  - **本条为 v1 归因，已被 v2 实测部分否证**：readonly 挂载**不是**充分也不是必要触发因——可写挂载同样失败（v1 轮自己的 F 案），而 `-shm` 在场时**连 readonly 挂载都能读**（v2 轮 G 案）。真正触发因见文末 §v2。v1 文本保留为发现过程记录，不作结论引用。

## 证据（两侧对照 × 两宿主 × 三脊）

| 对象 | 脊来源 | 头字节 | 结果 |
|---|---|---|---|
| 宿主=本机 Docker Desktop（arm64，镜像 `mc-prod:latest` 本机件） | 本 checkout SQL 建的**删除模式**夹具 | `01 01` | **EXIT=0**，JSON schema 全等、`empty:false` |
| 同宿主 | 同夹具的 **WAL 模式**版（仅 journal_mode 不同，其余字节与放置方式一致） | `02 02` | **EXIT=1** `unable to open database file (14)` |
| 同宿主 | **真实**运行脊卷 `mc-e2e-spine-140360502` | `02 02` | **EXIT=1** 同上 |
| 定向机 218（x86_64，docker 29.4.0，镜像 `mc-prod f77ab1459d89`） | WAL 夹具 | `02 02` | **EXIT=1** 同上 |
| 同定向机 | 删除模式夹具 | `01 01` | **EXIT=0** |

原始件=`evidence/A03/l03p-218-raw-20261122T041544Z.log`（sha256 `22ff1146cd07ffeb6105d1cecda4ce7c8a7d96bf5de573a97d623de8db89ad55`，与 218 上的原件逐字节一致后已删宿主副本；218 残留复核=文件 0、容器 0）。被测二进制=本 checkout `d1cae2e` 交叉构建（arm64/amd64 各一，`GOOS=linux`）。

## 影响边界（不自扩）

- 该检查位于 **checkpoint 阶段、替换二进制之前**，失败即整笔更新中止：属**可用性**影响（升级被挡在检查点），不是数据损失，也不是"带着坏脊继续跑"。方向是 fail-closed。
- 因此本件的严重性主张限于：**现行更新事务在真实（WAL）脊上无法通过自身检查点校验**。我**没有**跑通完整操作者前门序列（那需要一个可运行的检查点），结论取自产品自己执行的那一行命令在三个真实/仿真脊上的表现，加上 lane 本身恰好在该步失败这一独立见证。
- 未验证面：真实部署中 `-wal/-shm` 边车文件在拷贝时是否在场及其组合行为（本实验在边车不存在的稳态下做，与 resident 已停的更新时序一致）；`j.Previous.Image` 为**旧版镜像内 mc** 时的行为（该处用上一版二进制校验上一版脊，其 `CurrentSchemaID` 与被测实现同源，不影响 WAL 结论）。

## 修复方向（呈所有者，本票未执行）

仓库里**已有正确的读法**：`substrate.OpenSnapshotReadOnly`（`substrate.go:90-97`，`immutable=1`，其文档前条件正是"调用方已确立为完成且稳定的快照——一份拷贝出的备份"），并且 `verbs/spine_transfer.go:229-230` 已经在快照读取上用它。故最小形状=检查点校验改走快照读法（~~或改可写挂载~~——该替代项已被 v2 实测否证，可写挂载同样失败），二者皆为设计决定（"检查点拷贝的权威读法"归哪一层）。

## 附属登记项：诊断面缺陷（同族教训）

`:640` 的判定把 `err`、反序列化失败、schema 不等、`empty` 四种成因压成同一句话，真因（CANTOPEN）只存在于被丢弃的 `err` 里。本 fork 已多次因"判定丢失原因"付出诊断成本（A16 的 marker 假阳性、A13 的 exit-2），登记为同族：**强制面的失败消息须携带可判别成因**（脱敏后）。此改动与上一条同域，一并归所有者处置。

## 与在册事项的关系

- **L02**（在线升级语义）：本案证明"检查点/恢复"链的实际可执行性尚未被任何绿色证据支撑；L02 的排空闸设计若实施，其演练前置=本缺陷闭合。
- **F0a**：更新演练是 F0a 硬项，`update_docker` 长期挂账；本件把该挂账从"未跑"转为"跑过且红+根因在册"。
- **A13**：同一条 inspect 路径比对 `CurrentSchemaID`；A13 的脊写侧全等绑定不受本件影响（不同咽喉）。
- **A19**（沙箱边界取证票）：与本件无关，其激活另按附二引用键核对（见同轮登记）。
