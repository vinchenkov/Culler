# A26 diff 计划预登记 #1（003 附三：先列后改；超出正清单=STOP-1）

票：A26 宿主 git 推送环境的 credential.helper 清空隔离（V-3 G4 实测缺口的交付域修复）
依据：所有者 2026-11-22 Q6——"批准立票，小票独立承载；立票不等于代码授权。" 实测来源 = `docs/facts/v3-git-mechanism-001.md`：产品四件套环境（HOME 重定向 + GIT_CONFIG_GLOBAL=/dev/null + GIT_CONFIG_NOSYSTEM + core.hooksPath 重置）对 repo 携带 **hooks** 零执行（G3 PASS），但 repo-local `credential.helper` 在 401 认证协商下**真执行**（G4 OBSERVED 哨兵笔迹）；全仓 grep 无 credential.helper 重置 / 无 GIT_ASKPASS 处置 = 具名缺口。git 2.34 无 helper 允许名单。

## 现状事实（E1，进码前须先复核行号）
- 推送 native git 执行环境在 `resident/src/repository.ts`（信封→native 执行；`runNative` 路径）。**精确注入点与现 env 构造在票分支进码时二次读源钉行号**（本登记不臆测行号）。
- 危险向量：被 clone/仓库携带的 `.git/config`（git 视为可信源）内 `[credential] helper=!cmd` → 认证协商时执行任意宿主命令，可读取注入凭据。T2/受审核外部仓库尤重。

## 允许文件族（正清单；批准生效前进码范围）
- `resident/src/repository.ts`：git 执行环境——在**任何平台受控 helper/凭据注入之前**，对推送/传输类 git 调用注入 `-c credential.helper=`（空重置，清除继承的 helper 链），保留平台自身所需的受控认证机制（**不得清除平台注入的那一条**）。
- `resident/src/repository.test.ts`：正反双控——① 仓库带恶意 `credential.helper` 时，推送环境**实际不执行之**（用哨兵文件笔迹判"生效"，非仅断命令串含参数）；② 平台受控 helper（若存在）仍送达；③ 无凭据路径不回归。
- `evidence/A26/`：V-3 G4 哨兵回归自动化（把 401 挑战竞技场纳入 lane）+ V-3 产品路验证材料（持久段时随 A25 复跑，**不外扩到其他 Git 凭据路径**）。
- `mc/…` 若涉及 mc 侧 git 环境同步，仅在 diff-plan-002 增补列出后进码；本票默认只 resident 侧。

## 禁触区
- 平台受控凭据机制本身（provider proxy / projector / 连接 token 路径）不得被清空逻辑波及；
- `mc/substrate/*.sql`、dispatch wire、supervision 渲染；
- V-3 事实卡的"呈报"定性不改——本票是修复实现票，验收后 G4 行由 OBSERVED 转"产品路已挡"。

## 真机/单元验收（五要件同规）
- 单元：哨笔证明恶意 helper **不执行**（结果断言，非字符串断言）；平台 helper 不被误清（反向）。
- 集成：V-3 竞技场 G4 纳入回归——产品环境形下 401 挑战不再触发 repo helper 执行（对照本票前 OBSERVED）。
- 不写任何真实凭据字面量到源码/示例/测试（凭据只从 env/密钥服务读）。

## 风险与回滚
- 风险：`-c credential.helper=` 若位置晚于平台注入，会把平台 helper 一并清掉（=认证断裂）；**清空必须先于注入**。
- 回滚：移除该 `-c` 前置（纯局部）。
- 时效：独立票，不进当前 IMPL-AUTH-003 Wave A；激活需所有者另发令（本登记=先列后改，未授进码）。
