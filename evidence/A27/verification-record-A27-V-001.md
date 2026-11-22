# A27 验证记录 #1（TM §7.1 · 更新检查点脊副本的读径修复 · R1+R3+入口守卫）

- verification_id: **A27-V-001**
- target_component: 更新事务的 **脊检查点校验读径**（DEFECT-REGISTER-003-v2 的 site 1）——把"对已停写 WAL 副本用 `mode=ro` 只读连接"改为"暂存副本→当前二进制进程内快照读"，并把校验失败的四类成因分标；外加**入口隔离守卫**（快照读入口结构上不可被活脊误用）。
- tested_version: 票分支 `shz/TA27i`，提交链 `73f3909`（R3 诚实化，已单独并 dev）→ `a1fe268`（STOP 呈报）→ `2a68849`（V-γ 主体）→ `f585bbd`（**范围收缩**：site-2 安装门与 verbs 读径重构回撤，本票只留 site-1+守卫+R3）。dev 基线 06ebe48。
- 形状决定的来路（为何不是"三处一起翻"）：见 `evidence/A27/stop-1-read-path-shape-001.md`。决定性事实=`checkUpdateSpine` 以 **`j.Previous.Image`（上一发布镜像，:255）** 起容器，镜像内 mc 是旧二进制 ⇒ 任何"新动词/给旧动词加 flag"在混合版更新路径上直接不可用；唯一可行=把稳态读移到**宿主当前进程**（V-γ）。

## 环境（两宿主各跑一次，同构造）
- 本机：MacBookPro darwin 24.6.0 arm64；`mc-prod:latest`（本机件）；被测 Linux 二进制=本 checkout `GOOS=linux GOARCH=arm64 go build ./cmd/mc`。
- 定向机 218.84.111.20（x86_64，docker 29.4.0，`mc-prod f77ab1459d89`）；被测=同 checkout `GOARCH=amd64` 交叉构建的测试二进制**原生执行**；未装 unit、未 reboot、未触任何既有服务；原始件=`evidence/A27/a27-lane-218-raw.log`（sha256 前缀 `ee5cfdb0…`，与宿主原件逐字节一致后删宿主副本，218 残留实测=文件 0/卷 0/容器 0）。

## 结果（逐条，含失败轮如实）
1. **必跑验收道 `update_docker`（本 fork 首条转绿的 docker 类道）**：`TestUpdateCheckpointDockerRestoresAfterInterruptedVolumeCopy`
   - 本机 arm64：**PASS**（`LANE_EXIT=0`，39.4s→收缩后 24.6s）。
   - 定向机 218 amd64 原生：**PASS**（`EXIT=0`，39.45s）。
   - 该用例含**中断注入恢复**：`RestoreUpdateCheckpoint` 中途失败→重试→`original` 数据回位 + `config.toml` 回位 + **恢复后脊身份经 `InspectCheckpointSpineIdentity`（快照门）核 schema/uuid/tables**（旧版此处用 `mc __release-database` 活读形，正落在它读不动的"停写 WAL 无 sidecar"态——本票修复的第二处见证）。
2. **R3 判别性（负控=它本会伪装）**：修复前 lane 报 `The spine checkpoint failed its integrity or schema check.`（真因 CANTOPEN 被吞）；`73f3909` 后、site-1 转正前，同一 lane 如实报 `…could not run: {"error":…unable to open database file (14)}`。**即"成因可读"这一性质被两次运行差实证**（非仅代码走查）。
3. **入口守卫（所有者 Q1 追加四要件的机械化）**：`mc/verbs/spine_read_entry_guard_test.go`
   - `TestSettledReadEntriesStayWithinTheAllowlist`：全树扫快照读入口引用，白名单**恰**={spine_transfer.go:1, deployment/update_checkpoint.go:2}；越名即红；并断言 `update_checkpoint.go` **不再含 `"__release-database"`**（防检查点校验退回活读动词=重引版本偏斜缺陷）。
   - `TestSettledReadEntriesStayWithinTheAllowlist` 附带阳性自证=该测试自身在实施中期因 allowlist 计数漂移**真实红过一次**（`2a68849` 时 cmd/mc 尚含快照引用），说明守卫有牙；收缩后 `f588`/本文提交时 `ok mc/verbs`。
   - `TestSettledReadEntriesGuardRejectsPlantedMisroutes`：植入①陌生文件取快照门、②活脊 inspector（onboard_spine.go）取快照门、③cmd 偷引 settled 检查器——**三例均必红**（错误路由可失败的机器证）。
4. **快照入口守卫（substrate 层，Q1"活脊不得误走"的硬闸）**：`substrate.OpenSnapshotReadOnly` 现对 `<path>-wal`/`-shm` 任一存在**即拒**（"snapshot precondition unmet"）⇒ 一个仍在写的脊（必有 -wal）**不可能被快照读静默读旧**；进程内正/反向单测覆盖（closed-WAL 可读、residue 拒读）——定向机侧此格**不以容器复现**（按设计无 verb 携带 settled 意图，V-γ 要点），标 **agent-run 进程内 E1，宿主无关（无平台分支）**。
5. **回归不破**：`mc/substrate`（OpenReadOnly 活读语义未变、spine_transfer 快照读者行为未变）、`mc/verbs`（含 `TestReadOnlyInspectionsOpenThroughTheSharedPath`——**该在制不变量测试曾被我 site-2 的 inspectSpine 参数化改动打破，本票因此收缩回撤，复绿**）、`mc/deployment` untagged ok、`docker_boundary` `ok 118.5s EXIT=0`、`resident/check.sh` 774/0（TS 面未触，前后同一份）。
6. **全道**：合并前置=票树 `mc/check.sh` + `resident/check.sh` 双绿（值见 AM v0.1.79 条目；本记录撰写时二者后台在跑，若任一红则不并 dev，本记录随之标注——**先并后验违规零容忍**，在册硬规则）。

## 范围边界（诚实收缩，非遗漏掩饰）
- **site 2（安装/修复/更新门的 `p.database()`）本轮不改**：追证结论入此——`prepare`(update 路径)、`stopAndRecheck`、`drain`、`checkCandidateProjectImages` 四个调用点在**真部署已有数据且已停服务**时，同样以 `mc __release-database`（活读形）打"无 sidecar 的停写 WAL 脊"⇒ **prod 同型红**，且**今日单测因 fake 直接喂 JSON 而看不见**（又一"挂账变哑"实例）。但把它转正需连带转写 `TestPublishedMaintenance*/TestCandidate*/TestPublishedCancellation*` 的 docker fake（新暂存形）——**这是 site-2 自己的票面**，混进本 blocker 票=放大 blast radius（正是全道当场抓破的：我首版把 site-2 一并翻 settled，`MC_EXIT=1` 打破 5 个在制测试，遂回撤）。**具名跟进项：A28（拟）安装门停读径**，含"追证三点静置性→逐点定类"的已做分析（`stop` 返回后/ack 后=journalled 静置，fresh-install/无据=活读或 absent 短路）。
- **site 3（redeploy 前门）不改**：它读**活脊**（"while the deployment runs"），G 案已证 sidecar 在场时 mode=ro 可用；其"tick 静置窗口偶发无 sidecar→门可能红"是**编排时序**问题（检查前确保静置），与 **L02 排空闸同源**，移交 L02，本票不以 immutable 掩盖。
- **零** dispatch/wire/schema/envelope/相位词/工作流改动；禁触区未碰。

## 扫描门
`security_scan` 目标根 normal，seal `458b74c62026d20082053097c62aa8dc2577fdf9ed60cce6d1918197538402fa`，193 条；blame 基准 join（本票四提交行集，阳性对照=substrate 守卫行归因 2a68849 命中以证 join 有效）=**落本票改动行 0 条**（`setup_published_prod.go` 6 条全 blame=9dbc043 上游存量）。静态边界证据，coverage 非全量，**不宣称项目安全**。

- reviewed_by: host-agent（执行者兼任复核声明沿用；Integrity 未做，不宣称）
- verdict: **site-1（已证实阻断）修复完成且双宿主必跑道绿 + R3 + 入口守卫机械化；site-2/3 显式划出本票**（附全道抓破的证据）。EVIDENCED 状态转写随 AM 登记轮；义务完成判定仍归 F0a Exit+治理链（Authority=所有者本人）。
