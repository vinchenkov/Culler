# A04 段 S2 中间态（截至 2026-11-20 21:00 +0800，非收口）

**运行位置**：所有者定向机 218（host=F1104，Ubuntu 22.04.4/systemd 249/x86_64/docker 29.8），root 驱动、专用账户 shza04（uid 钉 2100+，避开与机上既有 next-server@uid1001 的自动池撞车）。**每轮结束 teardown 已验证清空**（`getent passwd shza04` = 0，/home/shza04 不存在，`com.culler.*` 单元 = 0）——当前生产机**零残留**。

**逐段结果**：
- B0 预检（磁盘≥25G、repo 干净、账户空闲、github 可达）：**PASS**（52G）。
- B1 账户置备（useradd + docker 组 + enable-linger + repo 移交账户属主副本）：**PASS**（uid=2100）。修复记录：初两轮 userdel 失败根因=自动 uid 命中 1001（被他进程占用→家目录 busy）；钉 uid 后清除成功。
- B2 目标机真实 Linux 发布腿（mise 自举+钉校验、build.sh linux-amd64、mc-prod-bin ELF 校验）：**PASS**——这是 A03 式"验收器在真机跑真构建"的正向前推（linux-amd64 发布资产在原生宿主产成）。
- B3 validate_package --platform linux-amd64：**FAIL reason=validate-failed**，真实报错：
  `mc: Release controls require the Culler host. {"error":{"code":"usage","message":"Release controls require the Culler host."}}`
  即：installer 的 `--check-updates`→`mc __release status-terminal` 在**全新未装主机**上要求"Culler host"前置。这是 A04-V-001 显式登记的"linux 腿 validate installer+setup 真实执行=UNVERIFIED（须 amd64 主机）"残差**首次被 S2 在原生 amd64 宿主触发**。
- B4–B10：**未执行**（B3 早退触发 teardown）。

**待定性质（未自裁）**：B3 是(a)validate_package 检查器对"未装主机 status 探测"的判据假设过宽（应接受 usage-usage 错误为合法"无在装发行"响应），还是(b)`__release status-terminal` 在无 host 时不该以 usage(退出码2)拒绝、该优雅返回"无发行"——二者修复面不同，**(b) 触及 `__release` 动词语义，超出 A04 diff 计划正清单**（属发行控制面），需所有者裁方向后定票。执行侧不自行扩面。

**下一步取决于所有者**：S2 若要跑到 B5+（持久安装→运行→重启→红线→卸载往返），须先解 B3 且确认 `__setup` 旗标集（脚本当前旗标为推断值，可能再触多轮真机迭代）；每轮是生产机往返。是否继续、还是先转 A16 参数裁定，见本轮提问。

---

# 第二轮取证（2026-11-21 01:32Z，S2 第五轮，R=/tmp/a04-s2-092511）

- B0–B2 复绿（B2=真机 linux-amd64 发布腿再次跑通）；B3 status 腿以 HOST_VISION_REFUSAL 断言**通过**，同一 run_installer 的 **setup 腿**新报错：
  `Download and verify Culler... Culler passed the download checks. Download and verify Bun 1.3.12... Bun 1.3.12 is installed. Open Culler setup. Culler setup requires a Mac.`（EXIT B3.validate=1 → RESULT B3 FAIL reason=validate-failed）
- **定性=第三道门**（与第一轮发现分列）：①安装器宿主门（已修，A04③）；②操作者视野门（→A24，安全边界，所有者裁）；③**终端设置向导平台门**——`runTerminalSetup` 对非 darwin 直接拒绝（exit 2），发生在 install.lock 与凭据段**之前**。Linux 部署的前台设置入口整体缺失。
- **处置=断言现实，不自行翻门**：validate_package 对 setup 腿加平台感知断言（linux 记录→按真实 (2,"Culler setup requires a Mac.")；darwin lock 形状在 linux 目标上双向拒绝，新增测试 test_a_linux_setup_check_demands_the_mac_refusal；validate_package_test 18/18，release 工具单测全绿）。提交 shz/TA04S2（rebase 后 2859d30）。**向导移植本身=新立 A25**（DEV-BASELINE，depends A04,A02；与 A02 凭据段纠缠+受 AGENTS 终端文本对抗评审门约束；开工序归所有者）。
- B5 剧本同步：平台门拒绝按 `gap=A25` 记录为数据点，持久段由宿主私有 `__onboard` 动词链驱动（agentsview/home/tunables/surfaces→supervision --activate --reconfigure）。
- teardown 每轮已验证：本轮结束后 account=0 home=0 units=0，**主机零残留**。
- 第六轮（重传 TA04S2@4d350f0 后启动）目标：B3 全绿 → B4 真机镜像 → B5–B10 持久段往返 + mask 红线数据 + 卸载零残留。

# 第三轮取证（2026-11-21 01:48Z，S2 第六轮，R=/tmp/a04-s2-094310）

- **B3 result PASS**：平台感知验证器在真实 linux-amd64 宿主全绿（status 腿 + setup 腿均按真实拒绝断言）。B2 亦二次复验绿（同一宿主腿稳定）。
- **B4 FAIL reason=image-build，根因=演练环境保真缺口，非产品缺陷**：`build-prod.sh` 的 systemd 前置检查 `systemctl --user list-unit-files` 需要用户总线；`sudo -u` 不开 PAM 会话，登录 shell 无 `XDG_RUNTIME_DIR`/`DBUS_SESSION_BUS_ADDRESS` → "Failed to connect to bus"。build-prod 该检查本身**姿态正确**（连不上总线即拒建，fail-closed，未削弱）。
- 修复=runas() 统一注入两变量（指向 B1 已验证存在的 linger socket）；同一缺陷将波及 B6/B7/B8 的 systemctl --user，一次修复全覆盖。第七轮沿用 $R 目录（bundle 增量取回），第六轮原始日志另存 raw-r6.log。

# 第四轮取证（2026-11-21 01:55Z，S2 第七轮）+ B4 真根因=产品缺陷（DEFECT-REGISTER-002）

- 总线保真修复生效（B4 报错从 "Failed to connect to bus" 变为空输出 exit 1）。B0–B3 三连绿（B3 连续两轮 PASS，验证器断言稳定）。
- **定向机三态实测**（systemd 249，root 与用户管理器同语义）：`list-unit-files 'com.culler.*.nonexistentxyz'` → **exit 1、空输出**；`list-units --type=service 'com.culler.*.nonexistentxyz'` → **exit 0、空**；有匹配两者均 0。
- **定性**：build-prod Linux 守卫取了 unit-file(enabled) 集且把 249 的"空匹配=exit 1"读成检查失败 → **全新主机必拒建**；与 A23 同族（真相源=loaded 集才是守卫语义；redeploy 支用 list-units 正确，A03 真机验收 7/7 与此一致）。登记 `evidence/A03/DEFECT-REGISTER-002.md`，修复在 A04 正清单②文件族内（build-prod.sh）：改同源 `list-units --type=service`，错误路径照拒（fail-closed 保持）；`build-prod.test.sh` 假 systemctl 转 loaded 格式+**新增干净主机正例**；`redeploy_test.sh` 全序列轨迹转写（stub 删 list-unit-files 支，回退即响亮失败）。验证：runner/image lane EXIT=0+全 ok-marker、redeploy 前门 lane ok（含四调用序列）。
- 第八轮计划：B4 起续跑持久段（镜像构建约十分钟级，B5 平台门数据点→onboard 链→B6–B10）。
