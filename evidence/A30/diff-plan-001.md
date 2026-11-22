# A30 diff-plan #1（登记票面：native-proxy resident 晚启时 Collie 容器不 birth——DEFECT-005 承载票）

**状态=立票登记，实施未授权**（轮十二 Q1："仅批准立票，不授权实施"；进码需另发明示令，A26/A27/003A 先例）。正清单在根因定位**收进票面之后**才成形——本文先固化所有者指定的五项保留证据与最小复现，根因/判据半留预勘回填位。
锚：`evidence/A03/DEFECT-REGISTER-005.md`（症状/隔离复现/两次取证自我纠正）；独立立票理由=所有者裁文（"同属时序不足以同边界；已暴露运行缺陷不得挂未授权生命周期功能票"）。

## 1. 五项保留证据（在册，本票携带）
1. **失败签名**：`collie_native_proxy_test.go:88 timed out (1m30s) waiting for the native Collie draft before provider completion; last: … no native draft yet` + 容器日志取证的 daemon 行 `No such container: mc-collie-h-<id>`；codex/claude-sdk 两子界同签名（首轮 268s/隔离轮 292.8s 两次）。
2. **父提交同签名证据**：`04cd6b2`（无 A29 改动）单跑同测同红（`PARENT_EXIT=1`，308.24s）⇒ 非 A29 引入；**先于本批存在于 trunk**（DEFECT-004 修复使其首次可见=洋葱结构，005 §定性）。
3. **通过边界**：同道 `TestCollieConversation`/`CollieResume`/`ColliePacketArtifacts` 三枚全绿 ⇒ collie 基础 birth 路径整体可用；红被夹逼到本测特有面=`collie send` 在前、resident（带 preload `CULLER_COLLIE_PROXY_FIXTURE`）**晚启**（`startNativeProxyResident`，测内次序注释级事实）。
4. **时序证据（回填位）**：本测自身 resident.log 零 collie 行（wake 从未发射）；spine 行级取证**当前 UNKNOWN**（005 §3：两次误读卷已作废；纪律=行取证前先证卷身份，从保现场树本身读卷名）。预勘回填：wake 路径 map（dispatch 驱动/ socket 驱动/ send 无 resident 时静默丢）+ 首发 tick 对"active 会话无容器"的处理。
5. **行为判据（修复前后，草案回填位）**：二择一（预勘定设计归属后择一入正清单）——(a) 晚启 resident 的**首 tick 必须为 pending-send 会话 birth 容器**（ durable 挂起语义）；或 (b) 无 resident 可接受时 `collie send` **fail-closed 拒**（操作者句）。设计声明文本以 `mc/dispatch/README.md` + `verbs/collie.go` 注释为准（预勘第 4 步定"设计欠哪个、测试假设哪个"）。

## 2. 根因预勘（在途，回填 §1-4/5）
只读预勘已派（wake-path 端到端 map / send-without-resident 假设检验 / codex 子界隔离复现+卷身份自证的行取证 / 与三枚通过测的最小差 / 设计-测试归属判定）。回填后本文升 v2 并呈**正清单候选文件**（预计触面：`verbs/collie.go` send/start 状态面 与/或 resident boot-first-tick/adopt 面 与/或 dispatch 输入面——**均属未授权区，v2 前不动**）。

## 3. 边界（不扩面声明）
不改 L02 票面（双残余已挂）；不并"计划内重启身份"家族（D-C 缓行裁文继续有效）；判据 (a)/(b) 择一是**设计决定归所有者**；本票验收车道候选=`docker_e2e` 本测两子界转绿 + 三枚通过测不回归 + 全道（在册）。
