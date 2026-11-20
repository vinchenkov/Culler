# A03 diff 计划预登记 #1（003 附三：先列后改；改动超出本清单=STOP-1）

票：A03 systemd 监督替换 launchd（supervision/services/uninstall/redeploy/dev-reset 平台面）
基线：shz/dev@f46c028（IMPL-AUTH-003 签发态）；O3 准入事实=卡 §3.5（Ubuntu 22.04.4/systemd 249/x86_64/graphical.target，agent-run E1）
003 正清单展开（主款二 A03 行的具体化）：

## 允许文件族
1. `mc/deployment/supervision*.go`（含 agentsview_supervision.go / update_stop.go / update_checkpoint.go / lifecycle.go 的**平台分叉化改造**）
2. 新文件：`mc/deployment/supervision_darwin.go`、`supervision_linux.go`、`supervision_units.go`（共享类型/接口）——**命名沿用仓内既有平台后缀惯例**（host_secret_darwin/_other、runtimeauth_exchange_darwin/_linux 同族，非新发明）
3. `mc/cmd/mc/setup_*_prod.go`、`doctor_prod.go`、`culler_prod.go`、`culler_uninstall_prod.go`、`onboard_state_prod.go` 的 launchd 引用点平台化
4. 根脚本：`install.sh`、`redeploy.sh`、`redeploy-dashboard.sh`、`uninstall.sh`、`dev-reset.sh` + `scripts/install-source.sh`（Linux 分支）
5. 对应测试：以上各文件 `_test.go`、`*_darwin_test.go` 保持、新增 `supervision_linux_test.go` 等；`install_test.sh`/`redeploy_test.sh`/`dev-reset_test.sh` 的 Linux 断言分支
6. `mc/deployment/host_release.go`（新增文件的发布清单登记——闭包测试强制）
7. `evidence/A03/`（§7.1、扫描记录、真机验收脚本与其运行回传）

## 禁触区（本票内）
- `mc/substrate/*.sql`、dispatch wire、directives/skills、`.github/workflows/*`（含 release.yml——A03 无 workflow 需求）；launchd **现有语义在 darwin 构建中不得删除或削弱**（并存替换，非平台抛弃——owner 既定：mac 保持兼容）；`install_test.sh` 等既有 darwin 断言不得为通过而放宽。

## 实现骨架（决策点标注）
- **D-1 unit 模型（票内决定，随 §7.1 呈报）**：默认=**user-unit + enable-linger**（匹配"单操作者、MC_HOME 在用户目录、mac 用 LaunchAgent"的对称形态；graphical.target/root 会话在场不构成用 system-unit 的理由）。否决备选=system-unit（需 root 写 /etc，破坏"部署不上升权限"的既有姿态）。若 diff 中发现与之冲突的既有契约句（supervision_release 相关），STOP-2 上报。
- **D-2 结构保证**：launchctl 字符串在 Linux 构建中**物理缺席**（build tag 分文件，非运行时 if-false）——沿用 A10 故障钩子的同型论证：不靠"关掉了"，靠"不存在"；测试=`GOOS=linux go vet`+源码守卫断言 linux 对象文件无 launchctl 符号（`go test ./deployment` 内以文本+构建双检）。
- **阶段切分**：①seam 抽取（supervision.go/activate 现有 launchd 细节迁入 _darwin，_common 接口定型；**行为零变化**——darwin 全测试必须原样绿）→ ②linux 实现（unit 渲染、daemon-reload/enable/start/stop 封装、激活/回滚/状态三态与 launchd 语义对齐：previous-plist 记录→previous-unit-file 记录）→ ③scripts 平台分支（`uname -s` 判定；Linux 路径的 doctor/dev-reset/uninstall/redeploy）→ ④update_stop/checkpoint 平台化（launchd"已加载则拒 build-prod"的等价 systemd 检查）→ ⑤真机验收脚本（附五五要件）。
- **每阶段独立提交票分支**，收口=合并 ff-only；全道 `mc/check.sh` + `resident/check.sh` + shell 测试族；改动触及 boundary/e2e 所证之物=跑对应 lane（本票预计触 supervision 语义→`update_docker` lane 属 A04 后联合面，登记为 EVIDENCED 义务或注明不适用理由）。
- **真机验收（执行者=执行侧，签发登记④）**：目标机 218；**modality 子决策**：只用非持久化手段（`systemd-analyze verify` 渲染产物 + `systemd-run --user`/transient unit 试验启停语义），**不安装常驻 unit、不 enable-linger、不触任何既有服务/文件**——持久化部署验证留给真正的产品安装时刻（属 F0a 全景而非 A03 单票），脚本判据按此定形并显式标注覆盖边界。
- **EVIDENCED 包**：§7.1 记录 + UNVERIFIED 清单（真机持久部署行为——如上 modality 有意不覆盖，照录不掩）+ diff 计划兑现核对 + 扫描（新文件目标根）+ AM/台账同轮 + 双面哈希。

## 风险预判（STOP 触发前科登记）
seam 抽取若牵动 supervision_release_test/supervision_resident_contract_test 的契约句（它们测的是"发布态监督"语义而非 launchd 本身）→ 预期兼容；若断言硬编码 LaunchAgents 路径于共享层 → 属既有测试的平台假设缺陷：迁移进 _darwin_test，不改语义，§7.1 注记。

## 进度与范围裁决（随阶段追加，不改写既有段）
- **①a（e8680a3）完成**：supervision.go 平台中立化；渲染/TCC/布局入 _darwin，Linux 声明 systemd 布局+拒绝桩；树校验目录改由布局派生；两 TCC 测试整段迁 supervision_darwin_test.go（无 skip/删除）。
- **①b1（d47e658）完成**：activate 文件操作六钩化（activationOrder/install/restore/verify/carryLabel/installedUnitPaths），launchd 实现体逐字迁移；lifecycle 与 agentsview 的 inspect 调用点改走钩子。
- **①b2（68c860b）完成**：update_stop 控制器 + update_checkpoint 绑定路径平台化；Linux checkpoint 路径=~/.config/systemd/user/*.service（实装），Linux stop=②桩。
- **① 完成判定**：deployment 包内**零未标注文件引用 darwin 专属符号**（脚本化扫描 0 命中）；darwin 全套 `./test.sh ./deployment/` 三次 ok；双平台 build+vet ok；cmd/mc 相关定向套件 ok（11.8s）。
- **范围裁决（登记而非扩面）**：cmd/mc 的 launchctl 控制器（culler_services_prod/onboard_state_prod/uninstall_prod 等）**移入阶段②**——该面要做的是"Linux 获得真实 systemd 行为+自己的消息面"，纯 seam 搬迁不改行为却牵动未列入本计划正清单的文件；进入 ② 时按附三先补 diff 计划再动。共享层遗留的 "LaunchAgent" 文案（非行为）同并入 ② 清扫，§7.1 记录。
- **待核问题（提取时照搬未修，红线）**：platformUpdateServices darwin 实现中 stop=true 且 bootout 返回**非** not-found 错误时被静默吞掉——既有语义原样保留，是否缺陷留待 ⑤ 真机验收数据判断。

## ②c 正清单扩充（先列后改；所有者"继续推进"指令下追加）
新增/修改文件：
- 新 `cmd/mc/supervision_platform_darwin.go`、`cmd/mc/supervision_platform_linux.go`——承载：`supervisionInstallDir`、`installedUnitFileName`、`newServiceController`、`newActivationController`、`platformUnitLabelLoaded`、`uninstallServiceUnits`、`stopRunningServiceUnit`；darwin 侧迁入 `cullerLaunchd`/`launchdController`/`launchdLabelLoaded`/`uninstallLaunchAgents`(去 GOOS 卫)/`launchctlBootout` 逐字主体；linux 侧=new `systemctl --user` 实现（is-active/enable --now/disable --now/daemon-reload + 单元目录枚举与 MC_HOME 解析）。
- 修改（仅调用点替换+删除已迁定义）：`culler_services_prod.go`、`onboard_state_prod.go`、`doctor_prod.go`、`culler_prod.go`、`acceptance_s7_prod.go`、`setup_existing_prod.go`、`setup_published_prod.go`、`spine_transfer_prod.go`、`uninstall_prod.go`、`culler_uninstall_prod.go`。
- 保留不动：`report.LaunchAgents` JSON 字段名（消费面契约，A09 统一清扫）；`verbs.CullerLaunch*` 帮助函数命名（同上）。
- 命名诚实性：跨平台符号一律新名（activationController 等），不在 Linux 文件里保留 launchd 字样的**行为**符号。
- 验证面：双平台 build+vet、cmd/mc 定向套件（Onboard|Services|Uninstall|Doctor）+deployment 全套、darwin 行为逐字迁移自证（diff 审查+测试等价）。

## 所有者三项裁决与阶段③④落地（2026-11-20）
- **裁决①（dashboard 最小行为套件）**：新 `redeploy-dashboard_test.sh`，6 用例=Linux 正确 unit+参数、systemd 失败不回退 launchd、缺 unit 拒绝、override fail-closed、无 override 随真实主机（假 uname 驱动）、Darwin 分支仍走 launchctl。fc52d52。
- **裁决②（redeploy.sh 选方案 A + 硬约束）**：统一 `resolve_service_os`（仅接受 Darwin/Linux、大小写不敏感、其余 exit 2、未设=uname）落 4 个发布脚本；**uname 桩未动**（其存在是为 source-update-guard 的语义，非服务面）；全部 launchctl 用例显式 `MC_OS_OVERRIDE=Darwin`（9 处）；新 Linux 用例**不传 override**——同时充当"默认=真实主机"的端到端证明。bdfe4f2。
- **裁决③（:349 潜在缺陷只登记）**：`evidence/A03/DEFECT-REGISTER-001.md`，状态 REGISTERED-NOT-FIXED；Darwin 停止循环 plist 文件驱动 vs 等待循环 launchctl list 驱动的真相源分裂原样保留；Linux 分支为 list 驱动、不继承该缺陷（用例自证 disable 序列）。
- **阶段③收口**：4/4（build-prod、dev-reset、redeploy-dashboard、redeploy）；各套件按"EXIT=0 且 ok 标记在场"双判据。**过程教训**：一次 systemctl 桩插入吞掉了 docker 桩首行，套件早死却返回 0——单看 exit code 不足恃，ok 标记必查。
- **阶段④（platformUpdateServices Linux）**：`deployment/update_platform_linux.go` 实装。逐字镜像 darwin 可观察语义：stop 吞掉一切 systemctl 错误（红线原样保留，注释点名留给真机数据判断）；confirm=`systemctl --user is-active`，exit 0→"remains loaded"，stdout∈{inactive,failed,unknown}→通过，其余→"state is unknown"。`platformInstalledUnitPaths` 保持①b2 实装。新 Linux 门控测试 `update_platform_linux_test.go`（PATH 假 systemctl；argv 集合断言 disable --now ×3、四态映射）；host 不运行=UNVERIFIED，交 ⑤ 在 218 以交叉编译测试二进制执行。验证：GOOS=linux/arm64 vet ok、darwin vet ok、host `./test.sh ./deployment/ -count=1` ok(49.0s)、gofmt 干净。
