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
