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
