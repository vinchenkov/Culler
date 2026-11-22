# Defect register 003 — v2 修正与爆炸半径（同轮自我否证后的机制定形）

本文件是对同日 v1（`DEFECT-REGISTER-003.md`）的**修正与扩面**：v1 的机制归因被自己的对照组否证，v1 文本保留为发现过程记录，**结论以本件为准**。执行者=执行侧；本轮**零生产码改动**（`mc/deployment/`·`mc/cmd/mc` 更新面不在任何现行正清单）。

## 1. v1 归因为何被推翻（仪器自证的两条对照）

v1 说"卷以 readonly 挂载 ⇒ 无法建 `-shm` ⇒ `SQLITE_CANTOPEN(14)`"。三条对照把它打掉了两面：

| 对照 | 条件 | 结果 | 打掉的是 |
|---|---|---|---|
| F 案 | WAL 脊副本 + **可写**挂载 + `--user 10002` | **同样失败** | "readonly 挂载是触发因" |
| G 案 | WAL 脊副本 + **readonly** 挂载，但 `spine.db-shm` 在场（32 KiB） | **exit=0 读成功** | "readonly 挂载必然失败" |
| (a) 案 | 同一 WAL 副本，Linux **宿主 fs**（目录对该 uid 可写），无 sidecar | **mode=ro 读成功（75 表）** | "Linux/驱动不支持" |

⇒ readonly 挂载既非充分也非必要。**真正的量是：打开连接的一方能否取得 `-shm`。**

## 2. v2 机制定形（实测三态）

`substrate.openReadOnly(path, false)`（`mc/substrate/substrate.go:107-125`）构造的 DSN 是 `file:<p>?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)`——**`mode=ro` 且不带 `immutable=1`**。SQLite 对 WAL 库在 `-shm` 缺失时需先建立/恢复它，这是一次写；只读连接被拒。同文件同容器下的三态实测（定向机 218，镜像 `mc-prod` = `sha256:f77ab1459d89…`，驱动 modernc.org/sqlite **v1.53.0**=产品同版）：

| 挂载 | DSN 形 | 结果 |
|---|---|---|
| 可写卷，目录对该 uid **不可写** | `mode=ro` | `ping-error attempt to write a readonly database (1544)` |
| 可写卷，目录对该 uid 不可写 | `mode=ro&immutable=1` | **ok tables=75** |
| readonly 挂载 | `mode=ro` | `unable to open database file (14)` |
| readonly 挂载 | `mode=ro&immutable=1` | **ok tables=75** |

**一句话**：被 `cp -a` 复制出来的"已停写 WAL 脊副本"，其 `-wal/-shm` sidecar **本就不存在**（SQLite 在最后一个连接关闭时回收之，A16-V-002 与本案卷内 `ls` 两次独立见证同形），于是任何走 `mode=ro` 的读法在此副本上都不可用；`immutable=1` 是唯一能读它的形——**而这恰是仓库里已经存在的 `OpenSnapshotReadOnly`（`substrate.go:97`，内部 `openReadOnly(path, true)` 在 `:101`）**。

## 3. 爆炸半径：三处生产位点同形（读源 E1 行级 + 命令形实测）

| 位点 | 命令形出处 | 内部实现 | 证据等级 |
|---|---|---|---|
| 更新检查点校验 | `mc/deployment/update_checkpoint.go:635`（被 `:255` 调用） | `__release-database` → `InspectReleaseDatabase` → `inspectSpineReadOnly:368 → OpenReadOnly` | **实测红**（update_docker lane 该步失败 + A/K 两案 + 两宿主） |
| 发布安装的数据库检查 | `mc/cmd/mc/setup_published_prod.go:758-760` | 同上同一 verb 同一挂载形 | 命令形**实测红**；"何时触达"=读源——`inspectSpineReadOnly:365` 对**零字节**文件短路（C 案 exit=0），全新安装无脊故不触达，**有既有数据的安装/修复才触达** |
| redeploy 前门数据库检查 | `mc/cmd/mc/redeploy_check_prod.go:100-104`（`redeploy.sh:338-340` 驱动） | `__database-schema` → `InspectDeployedDatabase`（`onboard_spine.go:299`）→ 同一 `inspectSpineReadOnly` | 命令形**实测红**（D 案 exit=1 同前缀错误）；触达链=读源 |

**为何活了 231 提交不被发现**：三处都只在"部署已有数据"时触达；A03/A04 S2 各轮是全新脊（零字节/不存在→短路），fast 套件与 boundary 用 `substrate.Open` 的正常连接（非 `mode=ro` 于副本），`update_docker` lane 从未跑过。与 DEFECT-002（同一"从未被跑的形"病灶）同族：**长期记 UNVERIFIED 的挂账不会自己变绿，只会自己变哑**。

## 4. 修复方向（呈报，未执行；本轮把它从猜测变成有对照的选项）

- **R1 最小形**：三处"读副本"改走 `OpenSnapshotReadOnly`（`:90-96` 文档前条件"完成且稳定的快照，无写者触及其 sidecar"在 `cp -a` 副本上成立）。形状风险=该谓词把"稳定性"交给调用方，须防被用于**活脊**（活脊上 `immutable=1` 会静默漏掉已提交帧——`:94-95` 自己写明了这条，且 `:96` 声明"**no fallback ever selects it**"：这条入口的设计就是**只由明确的调用方选定**，故 R1 不违该契约，但也意味着**没有任何机制会自动替检查点选它**，须逐位点显式改）。
- **R2 语义形**：让检查点产**非 WAL 副本**（复制前对活脊 `wal_checkpoint(TRUNCATE)`，或改用仓库既有备份机构 `snapshotInto`，`spine_transfer.go:151`）。更干净但对活脊有一次写，属更新时序的设计决定。
- **R3 必做的第三件（与 R1/R2 正交）**：v1 已登记的诊断面缺陷——`:640`/`:112` 把多因压成一句、真因随被弃 `err` 消失（本轮 F 案"我以为的 14"实际可能是 1544，正因截断而**无法判定**，此即该缺陷的现场成本）。强制面失败消息须携带可判别成因（脱敏后）。
- 验收形状须含**三态表**（可写/只读挂载 × sidecar 有/无 × mode=ro/immutable），否则同类退化仍可潜伏。

## 5. 原始件与残留
`def003-out-20261122T043723Z.log`（一轮 A–F）sha256 `9bf21735…d71`；`def003-r2b-20261122T043919Z.log`（二轮 G–L）`94cba972…ec1`；`def003-r2-20261122T043845Z.log`（**被弃用的无效轮**，仪器缺陷见其 §前言，保留为过程证据）`9c249fef…eea`。定向机残留实测=本案件文件 0 / 卷 0 / 容器 0（宿主上他人 `tmp3z3u*` 等文件与本件无关，未触）。
