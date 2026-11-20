# A03 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A03-V-001
- target_component: systemd 监督替换 launchd 的全部平台面——deployment 服务生命周期 seam（渲染/激活/检查点/更新控制）、cmd/mc 服务控制器、四个发布脚本（build-prod / dev-reset / redeploy-dashboard / redeploy）的服务管理器分支、`MC_OS_OVERRIDE` fail-closed 解析器、A03 真机验收器（五要件）。launchd 现有语义在 darwin 构建中原样保留（并存替换，非平台抛弃）。
- tested_version: shz/TA03 票分支终态（阶段①–⑤ 提交序列，终态提交 4ba3cba；合并态以本记录随附的 dev 合并提交为准，AM 同轮锚定）
- environment: 开发与快线=MacBookPro darwin 24.6.0 arm64（mise go 1.25.12 / bun 1.3.12）。真机=所有者指定部署机 218.84.111.20（host=F1104，Ubuntu 22.04.4 LTS，systemd 249.11-0ubuntu3.21，x86_64，root 用户管理器 running，经所有者指令"当前服务器你就可以进行真机验收"由执行侧 ssh 运行；验收=非持久化模式：transient unit `--collect`、零 enable、零 linger、零常驻 unit 安装、自有临时目录退出即删）
- attack_scenario（执行清单与实测退出码）:
  1. `./mc/check.sh`（gofmt/vet 全 tag+linux arm64、shell 测试族含根脚本、全 Go 测试）：**exit 0，UNVERIFIED=0**
  2. shell lanes：`redeploy_test.sh`（9 处 launchctl 用例显式 Darwin + 新 Linux 全序列用例 **不传 override**，经假 uname=Linux 端到端证明"默认=真实主机"；含 stop/disable/wait/守卫完整调用转写）**ok**；`redeploy-dashboard_test.sh` 6 用例 **ok**；`dev-reset_test.sh` **ok**；`runner/image/build-prod.test.sh`（Linux 守卫+141 错误路径+Darwin 钉选）**ok**
  3. `runner/image/check.sh`、`resident/check.sh`、`dashboard/check.sh`、`runner/shared/check.sh`：**exit 0**（image lane 首跑因 worktree 缺 `npm ci --prefix runner/openwiki` 锁定依赖而红，按 CI 同法安装后绿；未改任何源码）
  4. blcheck（build 后自仓库根运行）：**tickets=59 violations=0** warnings=2（A06/A13 既有注释语法告警，历轮同态）
  5. 扫描门×2（封印）：`scan-2026-11-20T07-48-49.306Z-637f02b0e089` seal `sha256:cac21331b2dc379d0be0b136210bc2f7e7372ea0605ddfd7a7d3d589c2b63d21`（485 findings）、`scan-2026-11-20T09-17-31.236Z-d70858c5e863` seal `sha256:7ded61d2fe489cced99de9cc303c7b342f93cc548027d2fb50940705d45373a2`（551 findings）。**落在我票内触及文件的条目唯一一条**：`update_platform_linux.go:22` 被挂消息"环境变量 → culler_evals/records.py:229 路径穿越"——消息引用文件与本位置无数据流关系（错配对），且该 `XDG_CONFIG_HOME` 读取与 systemd 自身解析 user-unit 目录的语义一致、变量为操作者自有。裁决=REJECTED-with-reason（静态边界证据，非可利用性证明；不宣称项目安全）。
  6. 真机验收（218，交叉编译 linux/amd64 测试二进制 + 验收脚本）：三轮迭代。**第 1–2 轮 FAIL 抓出两个真实渲染器缺陷**：(a) `ExecStart` 渲染进 `[Unit]` 段（`[Service]` 头在其后）——systemd 判 unknown-key 忽略并致 unit 致命；(b) `WorkingDirectory=` 与 `StandardOutput/append:` 的引号在 systemd 249 **不被剥离**（引号成为路径字面字符→"not absolute"）。修复+断言升级（见 actual_result）。**第 3 轮 `ACCEPT: PASS` 7/7**（C0 环境在场、C1 全套 Go 测试含 ④ 四态真机绿、C1b 三 unit 导出、C2 `systemd-analyze --user verify` 三 unit 全 exit 0 无告警、C3 transient 启动 `is-active`=active/0、C4 `systemctl --user disable --now` 真实停止运行单元（transient 上返回 0，终态 inactive）、C5 不存在单元 `is-active`=**inactive/exit 3**（观测事实；④映射含 inactive，契约钉"非零 ∧ token∈{inactive,failed,unknown}"）、C6 零残留，system running）。原始输出字节原样=`a03-acceptance-218-raw-20261120T170520Z.out`。
  7. 过程缺陷如实登记：一次 systemctl 桩插入吞掉 docker 桩首行致套件早死而 exit 0——本票此后 lane 判据=退出码 ∧ ok 标记在场 双查；验收脚本首轮 results 行丢 reason 参数、C5 判据过度钉 token（unknown），均为验收器自身缺陷，已在 PASS 轮前修正（缺陷轮原始件未回传，登记于此）。
- expected_result: Linux 构建获得真实 systemd 行为且 launchctl 字符串在 Linux 构建物理缺席；darwin 行为零变化；关键脚本族两平台分支机械测试钉死；真机非持久面验收通过
- actual_result: 全达成（范围如上）。新增实测事实：①249 的 is-active 对不存在单元回 inactive/3；②`disable --now` 对 transient 单元返回 0 且真停；③249 在 WorkingDirectory/append: 上保留引号字符、Environment= 使用 shell 式引号、ExecStart 多字引号合法。渲染器两处缺陷修复由逐字黄金文本断言锁定（片段式 Contains 断言的位置盲区教训入册）。
- evidence_ref: evidence/A03/（diff-plan-001.md、DEFECT-REGISTER-001.md、a03-systemd-acceptance.sh、a03-acceptance-218-raw-20261120T170520Z.out、blcheck-a03.ndjson）
- evidence_digest: 原始验收输出 7284 字节 cmp 校验 BYTE-EXACT；mc-check 全日志与 lane 日志（本机 /tmp 运行期件，关键行已转录本记录）
- test_script_digest: 票分支文件以 git 锚定（终态 4ba3cba + 收口提交）
- execution_id: A03-run-01
- supersedes_verification_id: 无
- verdict: PASS（作用域=本票分支+本机 darwin 快线+218 非持久化真机面）。**systemd 真实监督行为 UNVERIFIED（无运行环境）**——此句为附五要求的开发证据层义务句，此处按已收窄事实如实携带：其"无运行环境"分句对本票已不成立（218 真机已运行验收器），保留原句以维持登记纪律；实际残余 UNVERIFIED 集=**持久化监督面**：常驻 unit 安装+enable、开机/登录后自动驻留、KeepAlive/Restart 语义下的故障重启、enable-linger、真实卸载完整性与真实部署（install.sh Linux 路径）——非持久化模式有意不覆盖，属 F0a 部署时刻；另 `update_docker`/`docker_e2e`/nightly lanes 未在本轮运行（update_docker 联合面=A04 后义务，已登记）；darwin stop 吞错红线（④注释点名）真机失败路径数据仍未取得（C4 仅证 happy 路径）；redeploy.sh :349 plist 驱动停止分裂=REGISTERED-NOT-FIXED（DEFECT-REGISTER-001.md）。
- reviewed_by: host-agent（执行兼任复核，显式声明；Integrity Reviewer 未做，不宣称）
- verified_at: 2026-11-20 17:20 (+0800)
- 边界: 工单 EVIDENCED ≠ 义务完成；F0a Exit 不因开发证据齐备而自动通过；218 为 x86_64 而 mc-prod 镜像 GOARCH=arm64 硬编码=**A04 事实输入**（A03 不涉及镜像构建执行），照录不吞。
