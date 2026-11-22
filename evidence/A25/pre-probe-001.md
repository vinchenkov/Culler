# A25 前置侦察 #1（只读；Linux 向导移植的真实余量——远小于票面叙事）

日期=2026-11-22 轮十八；基线 dev `798cc05`。方法=读源+grep 亲验（无码改动）。**本件不构成进码授权**（轮十二 Q8 的义务=文档件早已交；A25 代码授权=W2 批准议程内，按"先 diff-plan 后动码"走下一步）。

## 1. 已就位（无需移植）
- `mc/onboarding/terminal_linux.go` **整文件已存在且正确**：`TCGETS/TCSETS` termios + `unix.Poll` 输入等待（Linux 可 poll /dev/tty，比 Darwin 的 select 方案更直接）——A03/A04 时代产物。
- `terminal_unix.go`（`darwin || linux` 共用主体）+ `terminal_pty_test.go`（同标签，真 pty 正/负控）**双平台跑过**（Linux 交叉编译在 mc/check.sh 每个 build tag 面为绿）。
- 监督/服务面=systemd 分支 A03✓；双架构资产 A04✓；发布安装路径 Linux 已在边界道内（boundarydocker tag 覆盖 cmd/mc）。

## 2. 真余量（小面，全部具名）
| # | 面 | 现状（亲验） | 移植动作 |
|---|---|---|---|
| S1 | 平台闸 | `setup_terminal_prod.go:99-102` `runtime.GOOS != "darwin"` ⇒ 打印 `"Culler setup requires a Mac."` 拒（exit 2） | 放行 linux；**`install.sh`/`scripts/` 无同语**（grep 亲验零命中）⇒闸面=1 处 |
| S2 | 拒绝语 | 同上 :100 | 改平台中性句（80/40 列+有/无色对抗审查义务触发） |
| S3 | 标签 | :88 步骤标签 `"Prepare your Mac"`；`setup_choices_prod.go:110` 面板语 `"Prepare your Mac again"` | 中性化为宿主形（与 A09 品牌域有交叉——**归 A25 还是随 A09 的 shz 改名波一并做，实施时申报择一**，不两处半改） |
| S4 | run.json 闸 | :103-106 拒容器内 setup（"operator's Mac terminal"）——**本质=动词域纪律（host agent 不得跑 setup），非平台锁**，Linux 宿主同样适用 | 仅文案去 Mac 字样，判定逻辑不动（AGENTS 不变量：verb scope 只认 /mc/run.json 存在性） |
| S5 | Linux 真机端到端 | 本机 Mac 无法证 | 载体=A32 `opt-in-lanes` runner 首跑（**候属主启用 fork Actions**）；启用前 S1-S4 可 CODED，EVIDENCED 挂延付具名（与 A31 同闸）|

## 3. 风险注
- S3 若归 A25：向导文面三语改动=AGENTS"操作者可见文本"全义务（审到 accept 为止）。
- S1 放行后 Linux 宿主首跑路径=发布安装向导（A24 谓词/凭据步 A02 候值）——**向导非凭据段（welcome/prepare/review/start/dashboard）可先绿**；凭据段在 A02 前按既有拒绝语义走，A25 不改其形。
- 票行前置读源义务（轮十二 Q8 文档件=docs/design/a25-linux-front-desk-001.md）与本件互补：前台文档定"该长什么样"，本件定"代码还差哪三行"。
