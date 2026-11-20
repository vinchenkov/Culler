# A04 diff 计划预登记 #1（003 附三：先列后改；改动超出本清单=STOP-1）

票：A04 安装/发布资产双架构（install.sh 矩阵 + arch 闸 host-native-arch 政策 + opencode x64 包链 + CI 矩阵）
基线：shz/dev@4c2fd56（A03 EVIDENCED 态）；blcheck 开票复核 **59/0 violations**
CODED 准入事实组：A03 行终报（218=x86_64/Ubuntu 22.04.4/systemd 249/docker 29.4/cgroup v2/53G 余）+ 本轮八组架构敏感点全量清单（执行侧 E1，file:line 级，摘要并入本计划各阶段）
所有者预裁定（grill 轮 2026-11-20，"接受建议"）：**D-0 = amd64 一等公民、arm64 零回归**（VISION 逐字一致）；**Q4(ii) = ④ stop 吞错失败路径真机数据并入本票验收器**；**Q6(a) = 验收器内置只读容量预检（不足→FAIL reason=disk-envelope）**。

## 政策不变量（全程有效，违者=STOP）
1. **无仿真原则保持并泛化**：现状 `containerruntime_linux.go:115-116` 对非 arm64 宿主 fail-closed（"no emulation"）。目标态=镜像架构必须**等于宿主原生架构**（宿主 ∈ {arm64, amd64}），永不引入 qemu/binfmt 路径；改的是常量表，不是这条语义。
2. **darwin 现有链路零行为变化**：install.sh Darwin:arm64 路径、macOS CI 作业、darwin 测试转写**逐字保留**；amd64 全部以**新增分支/新增用例**进入。
3. **Record schema（release.json format=1）不改**：多架构采用**每平台一条记录**方案（`release-darwin-arm64.json` 保持现名 `release.json` 兼容 + 新增 `release-linux-amd64.json`，资产名各带平台段）。理由：Record 单 Platform 字段+client.go 白名单+publish/install/updates 四方咬合，改 schema=跨面 wire 变更；每平台记录令既有验证链逐字复用。**此为本票 D-1（票内设计决定，§7.1 呈报）**；若实施中发现与既有契约句冲突→STOP-2。
4. **003 附二 P1-3 workflow 白名单兑现**：release.yml 仅按本计划阶段⑤命名作业改动 + 四个守护测试同步；security-nightly 全作业允许；其余 workflow 零触；不新增凭证面（PR 作业依旧零云凭据）。
5. 禁触区照旧：substrate SQL、dispatch wire、directives/skills、openwiki。

## 允许文件族（正清单；超出=STOP-1）
- 阶段①政策核心：`mc/verbs/containerruntime_linux.go`、`mc/onboarding/prepare.go`、`mc/cmd/mc/{helper_manager,redeploy_check_prod,setup_published_prod,setup_release_prod}.go`、`mc/deployment/project_image.go`、对应 `_test.go`（amd64 镜像用例新增）；`mc/distribution/{record.go,client.go}`（平台常量表→合法集 {darwin-arm64, linux-amd64}；client 白名单+平台匹配）、`mc/cmd/culler-package/main.go`（`--platform` 值域、DWARF/ELF 分支 EM_x86_64=`\x3e\x00`）。
- 阶段②构建资产：`runner/image/build-prod.sh`、`build.sh`、`Dockerfile`、`runtime.Dockerfile`、`project-manifest.sh`、`runner/{agent-runner,opencode}/package*.json` 的架构选包机制（构建期参数化，不改锁文件语义→允许生成新锁文件段）；`release/{build.sh,image_layers.py,security_scan.py,image_contents.py,validate_package.py}`（arm64 断言→宿主派生参数表）。
- 阶段③发布与安装链：`install.sh`（主机门 {Darwin:arm64, Linux:x86_64}；**plutil 依赖替换**=darwin 保持 plutil、Linux 走 python3 JSON 解析，两路输出同构断言）、`release/installer.sh`（Linux 分支：无 xcode-select；host_bun `archives.linux-x64`；systemd 前置指回 A03 栈）、`release/publish.py`（每平台记录与资产上传）、`mc/distribution/update_policy.json`（如更新门需平台维度则在本票内加，保持版本语义不动）。
- 阶段④opencode x64：`release/opencode/{pins.json,build.sh,check_archive.py,check.sh}`、`mc/deployment/{opencode.go,opencode_source.go,opencode_source.py,opencode_models.go}`、`mc/cmd/mc/setup_update_*_prod.go` 下载选择点。
- 阶段⑤CI：`.github/workflows/release.yml`（作业改动清单见⑤）、`security-nightly.yml`、`dashboard/src/{release-workflow,security-gate,smoke-gate}.test.ts` + `release/check.sh` 面。
- 阶段⑥真机验收：`evidence/A04/`（脚本+原始输出+§7.1）、`install_test.sh`/`scripts/install-source_test.sh` 的 amd64 镜像用例与 `CULLER_FIXTURE_ARCH` 新夹具变量（既有 Linux-拒绝用例 :428 改语义须先在本清单写明：拒绝面从"非 Darwin"收窄为"非 {Darwin:arm64, Linux:x86_64}"——**这是断言变化，非放宽**：新增 Linux:arm64/aarch64 拒绝用例保住 fail-closed 面）。
- `tools/blcheck/status.json`、`docs/contracts/ACCEPTANCE-MATRIX.md`（收口轮）、`mc/deployment/host_release.go`/`runner_release.go`（若新运行文件——闭包测试强制）。

## 阶段切分（每阶段独立提交票分支）
① 政策核心：常量→值域表；**行为零变化**（darwin+arm64 全套原样绿）；containerruntime 探针改为"宿主原生 ∧ 镜像==宿主"双臂判定，非白名单架构仍 fail-closed 且消息如实。
② 构建资产：build-prod/CI 脚本 `TARGET_ARCH` 参数（默认=宿主原生，显式越境值 fail-closed）；Dockerfile 六层下载改按 `TARGETARCH`（buildx 内置）选择 URL/校验；release-images 已用 buildx，改动最小点确认；agent-runner/opencode 的 npm 原生包按架构生成选择（构建期 npm --force 面收窄为显式锁表，禁"顺手升级"）。
③ 发布/安装链：D-1 每平台记录落地；install.sh 双门+双解析器；installer.sh Linux 分支；publish.py 多资产多记录（既有 5 资产名逐字不变）。
④ opencode x64 包链：pins→平台映射；check_archive/opencode.go ELF e_machine 参数表；x64 包真实纳入构建与更新流。
⑤ CI 矩阵：`application-package`（matrix: darwin-arm64 + linux-amd64 两腿）、`deployment-linux`/`dashboard-browser`（各 +`ubuntu-24.04` x86_64 腿）、`release-images`（buildx 双 `--platform` 产物+双 digest）、`product-image`（按平台加载/推送）、`release-downloads`（双记录双资产上传）、`image-vulnerabilities`/`release-checks`/`release-version`/`go-cache-cleanup`（矩阵感知聚合或双作业；以四守护测试改动为准绳）；`security-acceptance` 与 `security-nightly` 双架构腿。**本地验证面**：push-CI 不真实运行（不推送纪律），故 CI 改动以 YAML 结构测试（dashboard 三面）+ 本机等价步骤脚本复核，登记 UNVERIFIED=真实 GitHub 运行面（F0a/发布时刻）。
⑥ 真机验收（五要件+Q6 容量预检+Q4(ii) 数据顺带采集）。**modality 子决策=须所有者点头**：F0a"amd64 净机安装"在 218 以**新建专用账户 + 本地暂存的真实发布夹具**（资产由 218 本机 amd64 构建）完成 持久安装→运行→`uninstall.sh` 完整卸载 序列——本票将首次做持久化 Linux 部署，顺带关闭 A03 残余"持久化监督面+真实卸载完整性"大半；非净机（既有服务与用户共存），账户级隔离如实登记。批准前 ⑥ 只交付脚本与判据，不执行持久段。

## 风险预判与 STOP 映射
- plutil 替换与 installer.sh Linux 分支=最高不确定面；③ 内先测试后合入（fixture 网络栈同 install_test.sh 家族）。
- 任何"为绿而改 darwin 转写"=违规；golden/镜像用例只增不移。
- npm 原生包面若发现上游无 linux-x64 对应物（opencode 2.2.7 / claude-agent-sdk / codex 任一缺包）→ **STOP-4 型事实上报**（不得静默降级或改选版本）。
- 14 日时效：003 签发轮起算，本票体量最大；若⑤/⑥ 越窗，按 003 §6 走进展重审而非缩判据。
- 收口 EVIDENCED 必含逐字句：**systemd 真实监督行为 UNVERIFIED（无运行环境）**——A04 若 ⑥ 获持久安装批准，该句残余面收窄但**句随票携带直至 F0a 联合判定**，转写规则与 A03 同。
