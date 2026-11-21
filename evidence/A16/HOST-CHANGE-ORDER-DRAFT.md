# 宿主变更工单草案 — 218 启用 ext4 project quota（A16 D-1(a) 升级路径；未授权执行）

状态：**草案，未获执行授权**。A16 票内闭合集=裁定 b+c（tmpfs 真 ENOSPC + 工作区看门狗）；本单是"工作区真配额"残余项（宿主契约/F0a）的预备工单。所有者批准并执行本单后，A16 做小增量切换到 (a) 并撤销残余登记；不批准则 b+c 长期有效。**本单任何步骤都不由执行侧自行发起。**

## 目标
把运行 Culler 部署与项目工作区的文件系统变为 **project quota 生效**（per-workspace 真写失败 ENOSPC），闭合 TM §5.2:99"工作区灌满宿主共享 FS"这一 b/c 不覆盖维。

## 前置事实（O3 卡/本轮取证）
Ubuntu 22.04.4，systemd 249，/home 与 /var/lib/docker 在同一 ext4 设备（无独立分区），**当前未挂 prjquota**；docker 29.8 使用 overlayfs/containerd；53G 可用（变更窗口前应 ≥40G 余量复核）。

## 步骤（维护窗口，root）
1. 备份：spine `mc backup`（经 culler 正规通道）+ `/home` 快照（若另有外部备份机制则引用之，本单不假设）。
2. 检查现有文件系统特性：`tune2fs -l $(findmnt -no SOURCE /home)` — 需 `project` 特性；缺失则 `tune2fs -O project,orphan_file /dev/<dev>`（ext4 惰性特性，**不需重格式化**；执行前以 `dumpe2fs` 留档）。
3. 挂载选项：`/etc/fstab` 该设备行加 `prjquota`（与现有选项并列），`mount -o remount /home` 生效；`findmnt -no OPTIONS /home | grep prjquota` 验证。
4. 为每个部署工作区根建项目树：以部署账户的 `state/projects/<id>/` 与 scratch 根为 quota 项目边界（目录 `chattr +P` 继承 + 每项目 `projectid` 分配表，由部署脚本按项目枚举；**共享根目录不得整体入同一项目**——否则失去单沙箱爆炸半径）。
5. 设限：`setquota -P <projectid> 0 <hard-KB> 0 0 <fs>`，hard=本卡 T2/T1 工作区值+缓冲（建议 soft=裁定软限、hard=软限×1.25，留看门狗先于真 ENOSPC 的余量）。
6. 验证（写失败语义）：在受限工作区内 `dd` 越 hard → **ENOSPC 即时失败**；spine 卷与它项目不受影响（对照 dd 成功）；run 容器观察日志出现资源类错误且 resident/dashboard 存活（爆炸半径≤沙箱）。
7. docker 侧：overlayfs 上层在 `/var/lib/docker`（同一 fs 时）——**工作区 bind 配额不覆盖容器可写层**；可写层维仍由看门狗+tmpfs 兜底，或后续把 docker data-root 移入独立配额项目（另单）。

## 回滚
去挂载选项重启服务即可回到 b+c 态（quota 元数据滞留无害）；`tune2fs -O ^project` 仅在无项目在用后执行。

## 风险与授权面
- tune2fs 特性变更+remount 影响**整机**（共享宿主上有他人负载）→ 属宿主 provisioning 行为，越过"部署不升权限"边界 ⇒ **必须所有者点名执行**，且建议先经 `dumpe2fs` 留档+可回退窗口。
- 配额粒度=项目目录，非容器可写层（第 7 条如实标注未闭合维）。
