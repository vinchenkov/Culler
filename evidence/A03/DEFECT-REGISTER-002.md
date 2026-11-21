# Defect register 002 — build-prod.sh 的 Linux 监督守卫在干净主机上误拒（真机 S2 验收器抓出）

状态：**已在 A04 段 S2 票内修复**（`runner/image/build-prod.sh` 在 A04 diff-plan-001 阶段② 正清单文件族内；修复提交见 shz/TA04S2 同轮）。本文件为登记件，不宣称 A03 票曾覆盖此面。

## 位置与症状

`runner/image/build-prod.sh` 的 Linux 守卫分支（"refuses while a `com.culler.*` unit is loaded"之 systemd 形态，A03 立支）：

```sh
if units=$(systemctl --user list-unit-files --no-legend 'com.culler.*' 2>&1); then
```

Ubuntu 22.04 / systemd 249 实测（O3 定向机，2026-11-21 取证）：

- `list-unit-files` 带模式且**零匹配** → **exit 1、无输出**；
- `list-units` 带模式零匹配 → exit 0、无输出；
- 二者有匹配均 exit 0。

于是守卫在全新主机（零 culler 单元）上走 else 分支，打印
`build-prod.sh: the systemd service check failed (exit 1):` 并 exit 3——
**每次全新安装必被拒建**（S2 第六/七轮 B4 连续复现，原始日志 `raw.log` RUN/EXIT 对）。

## 根因分类

真相源选择错误，与 A23（redeploy plist/list 分裂）同族：守卫语义 = "受监督部署**在载**"（darwin 面 `launchctl list` 即 loaded 集），Linux 面却取了 **unit-file（enabled）集**。空集在两个 API 下的失败语义不同，把"无在载"误读成"检查失败"。

## 修复（非放宽）

守卫改用与 `redeploy.sh:read_units` 同源的 `systemctl --user list-units --type=service --no-legend 'com.culler.*'`（249 实测空匹配=exit 0）；错误路径（总线故障等，exit≠0 且带文本）仍拒建——fail-closed 姿态逐字保持。测试面：`build-prod.test.sh` 的 Linux 假 systemctl 改答 loaded 行格式，**新增干净主机正例**（守卫放行至编译器、无拒绝文案）；`redeploy_test.sh` 全序列期望轨迹同步转写（stub 的 list-unit-files 支删除——若代码回退即轨迹不匹配而响亮失败）。

## 取证

S2 第六轮（HOST_VISION 断言后首个新根因）与第七轮（总线保真修复后）连续同 token 复现；systemd 249 三态实测行见 `evidence/A04/s2-interim-status.md` 第四轮段。此缺陷由验收器在真机抓出，本机 darwin 测试面不可见（Linux 分支需 `MC_OS_OVERRIDE` 夹具，而空集语义只有真 systemd 才暴露——stub 历来答 exit 0）。
