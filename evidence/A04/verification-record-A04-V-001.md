# A04 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A04-V-001
- target_component: 安装/发布资产双架构——amd64 一等公民、arm64 零回归。覆盖：原生架构值域门与仿真检测、镜像构建与 release 检查按 TARGETARCH、AgentsView/host-Bun/OpenCode 每平台钉与包链、每平台发布记录（D-1）、双解析器+双主机门的前端与 installer、CI 孪生作业、非持久真机验收器。darwin 链路逐字保留（并存，非抛弃）。
- tested_version: shz/TA04 票分支终态（①fdf477a ②f3d778f ③2ca15cc ④48f32d2 ⑤d92bdd5 ⑥S1 44bbdbe + 回归修 ec6d6cd；合并态以 AM 同轮 dev 提交锚定）
- environment: 开发/快线=darwin 24.6.0 arm64（mise go 1.25.12/bun 1.3.14）。真机=所有者定向 218.84.111.20（host=F1104，Ubuntu 22.04.4，systemd 249，x86_64，docker 29.8.0，root 用户管理器可达，磁盘 ~54GB 可用）；非持久模式：无 unit 安装、无 enable、无 linger、无既有服务/文件改动，运行目录自清。
- attack_scenario（执行清单与实测退出码）:
  1. `./mc/check.sh`：**exit 0，UNVERIFIED=0**（gofmt/vet 全 tag + linux arm64、根脚本 shell 族、全 Go 测试）。**过程如实登记**：④⑤ 提交前我未跑 redeploy_test.sh，全道首轮抓到真实回归——build-prod 新增 `uname -m` 调用 vs redeploy_test uname 假桩对所有参数只答 Linux（Exec 派生失败）；按其它套件同法给假桩补 `-m`（测试保真，非放宽断言），复跑绿（ec6d6cd）。教训回灌：跨阶段触及构建脚本必跑全 shell lane 族再提交。
  2. 交叉编译双平台真实构建：`release/build.sh` darwin 腿本机全流程绿（含 validate_package 的 installer+setup 双交接）；`CULLER_RELEASE_PLATFORM=linux-amd64` 腿构建绿（assets/meta/ELF 均 amd64，内嵌 verify-binary 过）。
  3. `runner/image/check.sh`、`runner/shared/check.sh`、`resident/check.sh`、`dashboard/check.sh`：**exit 0**（openwiki 锁定依赖已在 A04 worktree 预装）。守护测试 `release-workflow/security-gate/smoke-gate`：**16 pass 0 fail**。
  4. blcheck：**tickets=59 violations=0** warnings=2（A06/A13 既有语法告警）。
  5. 扫描门（deep）：seal `06694fc4098060246323e1291ec78811f6eaadd985275b5a882244f2be02bab7`，551 findings。**落在本票 diff 行：0 条**。逐条归因：`opencode_source.go:211/215/220`、`client.go:338/374` blame=上游 9dbc043 非我改动行，且属已知 FP 族（字面 argv 的 exec、带 owner/mode/nosymlink 守卫的 token Open）；`agentsview-proxy.ts`（5 high SSRF）根本不在 A04 diff；`publish.py`/`security_scan.py` 的 SSRF medium=消息 sink 指向未改动 dashboard .ts 的错配对。**静态边界证据，不宣称项目安全**。依赖面：3 包 10 建议（与基线同态）。
  6. 真机段 S1（非持久）：**ACCEPT: PASS**（`a04-acceptance-218-s1-raw.log` 字节 cmp 一致，远端已清、`a04-*` 单元零残留）。A0 磁盘预检 free≈54GB ≥20GB PASS；**A1**=交叉编译的 linux/amd64 deployment 测试二进制在真机跑监督/架构/AgentsView/hostbun 全套 PASS——**首轮如实 FAIL**（误传 darwin `go test -c` 二进制触发 Exec format error，验收器判 FAIL 非静默），改 GOOS/GOARCH=linux/amd64 重编后绿：验收器抓出执行侧真实构建错误的又一实证；**A2**=mc.amd64 --help 原生执行 exit 0（ELF x86-64 exec-format 证明）；**A3=SKIP reason=front-door-not-shipped**——发布前端脚本因操作者扫描策略拒绝传输到目标机而如实挂账，不冒充 PASS（该平台门的行为面已由本机 install_test.sh 的 Linux 生产主机正例 + 真机 A1 部署套件双重覆盖）。
- expected_result: Linux/amd64 获得真实发布/安装/构建能力且 arm64/darwin 零回归；架构门宿主相等且无仿真；每平台记录各证其台；真机非持久面通过。
- actual_result: 全达成（范围如上）。实质修强三处并登记：①容器探针从"查 GOARCH==arm64"改为"image-arch ∧ uname-machine 一致"（旧检查在 qemu 仿真 arm64 二进制时反通过）；②OpenCode 归档检查从"恒查 arm64"改为"必须等于宿主架构"（amd64 主机收 arm64 归档从前放行、现拒）；③facts/candidate/install 容器脚本改 find 单程序发现，去架构字面。真实两腿构建 + 真机非持久验收均绿。
- evidence_ref: evidence/A04/（diff-plan-001.md、a04-release-acceptance-s1.sh、a04-acceptance-218-s1-raw.log、blcheck-a04.ndjson、本记录）
- evidence_digest: 全道/image/resident/dashboard/shared lane exit 0；S1 原始 7284+ 字节 cmp BYTE-EXACT；扫描 seal 06694fc4…（值全见扫描门段）
- test_script_digest: 票分支文件 git 锚定（终态 ec6d6cd + 收口提交）
- execution_id: A04-run-01
- supersedes_verification_id: 无
- verdict: PASS（作用域=本票分支 + darwin 开发机快线 + amd64 真机非持久面）。**systemd 真实监督行为 UNVERIFIED（无运行环境）**——附五要求逐字句，本票随 EVIDENCED 携带，其残余经 S1 已收窄但未清零：A1 在真机跑了监督渲染/更新控制用例（ amd64 原生执行），然而**持久化监督面仍未验证**——常驻 unit 安装+enable、开机/登录驻留、KeepAlive/Restart 故障重启、enable-linger、真实 installer→setup→运行→uninstall 完整往返、真实卸载完整性。**残余显式清单**：①持久段 S2=待执行（需所有者在目标机创建专用账户并批准持久安装，属 F0a Exit 硬项，附五"运行=所有者在真机"，非本授权可自动发生）；②A3 前端脚本真机直接执行面=传输受限挂账（操作者扫描策略拒传 install.sh），随 S2/F0a 闭合；③孪生 CI 作业在真实 GitHub runner 的执行面=本 fork 不推送，F0a/发布时刻项；④update_docker/docker_e2e/nightly lanes 未跑；⑤A03 遗留红线（platformUpdateServices stop 吞错的失败路径真机数据）——本票 S1 未含该采集，仍挂账（原计划并入 A04 验收器，因 S1 采非持久模式且 A3 传输受限而未竟，如实登记不吞）。
- reviewed_by: host-agent（执行兼任复核，显式声明；Integrity Reviewer 未做，不宣称）
- verified_at: 2026-11-20 19:50 (+0800)
- 边界: 工单 EVIDENCED ≠ 义务完成；F0a Exit 不因开发证据齐备而自动通过；arm64 镜像/资产钉仍全功能保留，发布域严格二平台（darwin-arm64/linux-amd64），linux-arm64 仅持 AgentsView/CI 开发钉不产发布记录；不推导任何契约冻结或 IMPLEMENTATION_VERIFIED。
