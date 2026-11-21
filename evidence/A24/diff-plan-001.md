# A24 diff 计划预登记 #1（003 附三：先列后改；改动超出本清单=STOP-1）

票：A24 Linux 宿主操作者视野门——按所有者 grill 六裁 **Q5=(b) 定向窄化**：`canInspectProjectVision()` 从"恒 GOOS==darwin"改为"非 Culler 容器=放行"（宿主=放行；agent/helper 容器=拒）。
基线：shz/dev@ce54cba；blcheck 62/0；dep A04✓（S2 真机取证派生此票）。

**本票性质=安全边界变更。** DEV-BASELINE A24 行明载"实现票另走 003 附三正清单批准，批准前本门一字不松"。⇒ 本文件=正清单**草案，待所有者批准**；批准前 runtime_scope_prod.go 零改动，本 worktree 不含生产码变更（仅本登记件 + 可选夹具预备）。**所有者批准本清单=进码与激活；不批准=门原样。**

## 现状事实（E1 读源）
`mc/cmd/mc/runtime_scope_prod.go:41`：`func canInspectProjectVision() bool { return runtime.GOOS == "darwin" }`。
9 个 broker 站点全部引用此单函数（main.go:88/251/254/257/260、culler_prod.go:222、dashboard_upload.go:41、private_release.go:121、project_setup.go:98、project_delete.go:158）——**唯一咽喉，无第二处 OS 判据**。
run.json 身份边界是**独立另一层**：`verbs.RequireHostScope(id)`（id≠nil 即拒），不被本票触碰。

## 设计（(b) 窄化，行为保序）
新判据=**"不在任何 Culler docker 容器内"**：`/.dockerenv` 存在 ⇒ 拒；不存在 ⇒ 放行（限 {darwin, linux}，其余 OS 维持拒——纯产品宿主集合，无新增 OS）。
逐域结果（与现状对比）：
- **Mac 宿主**（无 /.dockerenv，darwin）：true → **与现状逐字相同**。
- **Linux 宿主**（无 /.dockerenv，linux）：true → **这是 (b) 唯一放宽**，即本票目的。
- **helper 容器**（/.dockerenv，linux）：false → **与现状相同**（今天 linux 恒 false）。
- **agent/pipeline 容器**（/.dockerenv + /mc/run.json）：false → **与现状相同**；且 RequireHostScope 因 run.json 仍独立拒（双检）。

关键保序论证：现状下 helper/agent 在 linux 上本就返回 false（GOOS≠darwin），本改动对**任何容器域返回值不变**，只把"宿主 linux"从 false 改 true。容器内进程无法伪造 /.dockerenv 缺位：agent 以 uid 10002 + `--cap-drop ALL` 运行，无 CAP_SYS_ADMIN，不能对 /.dockerenv 做 mount/umount/unlink（root 属主）。⇒ 放宽面严格=操作者真实宿主 shell。

## 允许文件族（正清单；批准后进码范围）
- `mc/cmd/mc/runtime_scope_prod.go`：仅 `canInspectProjectVision` 函数体（新增 `os.Stat("/.dockerenv")` 判定 + GOOS 值域；其余行逐字不动）。
- `mc/cmd/mc/runtime_scope_prod_test.go`（如无则新建）：三域矩阵的**宿主侧单元断言**——用临时 rootfs + 可控路径探针或直接以构建期 seam 覆盖判据输入（不放宽：每个"拒"用例必须断言 false）。
- `mc/cmd/mc/*_test.go` 既有转写：**任何"linux ⇒ 视野门 false"的既有断言须逐动词改钉为新语义（host linux=true / container=false），改钉=契约变更呈报，非为绿而改**；无法在单测注入 /.dockerenv 的存在域，改由集成/真机覆盖（下段），单测只证 GOOS 值域与函数纯度。
- `tools/blcheck/status.json`、`docs/contracts/ACCEPTANCE-MATRIX.md`（收口轮）。
- `evidence/A24/`（diff-plan、验证记录、真机三域原始输出）。

## 禁触区（负清单，违者 STOP）
- `verbs/RequireHostScope`、run.json 身份装载、dispatch wire、`dispatch.Decent`/Decide、prepare/attest/commit seam 的 canonical JSON——**身份门与线形一字不动**（本票只动 broker 分发的 OS 粗门）。
- 9 个 broker 函数体本身（只动它们引用的谓词）。
- `mc/substrate/*.sql`、supervision 渲染、agent 输入面（directives/brief/skills）——除非所有者在批清单时另行点名。

## 真机三域验收（F0a 硬前置，五要件同规）
脚本 `evidence/A24/a24-vision-acceptance.sh`（在 218 root 运行）：逐动词（project list / delivery start / __release status / environment save / project delete / dashboard upload）在三域实测：
1. **宿主**（root shell，无 /.dockerenv）：动词**到达其真实后端**（有部署=正常 JSON；无部署=业务级错误，非"Culler host"/"requires a Mac"式门拒）。判据=退出码与消息**不含门拒 token**。
2. **helper 容器**（`docker exec <helper> mc <verb>`）：断言**门拒**（false 域）。
3. **agent 容器**（管道 run 内 `mc <verb>`，有 run.json）：断言**双层拒**——/.dockerenv 门 + RequireHostScope。
原始输出 ts/host/uid 前缀，RAW BEGIN/END 包裹；磁盘预检。此轮**与 A16 资源矩阵同轮**跑（共享部署实态，减生产机往返；两者都需 helper+agent 容器在位）。

## 风险与回滚
- 风险=误把容器域放行（→真机三域矩阵逐域证 false 拦截）；误把宿主某动词漏放（→逐动词正例拦截）。**不引入新放行面到容器**是核心不变量，矩阵反证为唯一仲裁。
- 回滚=还原单函数到 `return runtime.GOOS == "darwin"`（纯局部，无状态/迁移）。
- 14 日时效：003 签发轮起算；A24 非 Wave A 解锁集内票（解锁集=A03/A04/A14/A16/A19/A13）——**A24 开工本身需所有者把本票并入授权批次**；本登记仅为待批材料，不假定已获准。

## 待所有者一步批准项
批准本正清单（文件族 + 禁触区 + 三域矩阵验收 = 进码范围）；并明确 A24 纳入哪个授权批次（003 之外追加，或下一授权）。批准前本门一字不松。
