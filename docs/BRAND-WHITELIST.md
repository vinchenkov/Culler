# BRAND-WHITELIST（R1 · A08 交付件）

R1 品牌文档层的验收基线：`grep -ri culler` 的全仓残留必须**逐处落入本表某类**。每类标注**终局**（永存 / A09(R2) 清除 / 治理事件后更新）。R2（A09）完成的判定之一，就是 B/C 类清空后仅剩 A/D 类。

## A · 永久保留（合规与叙事身份）

| 处 | 说明 |
|---|---|
| `LICENSE` 上游版权行与 MIT 全文 | D15 合规义务，不可移除 |
| `LICENSE` fork 段 + `README.md` "Derived from Culler" 节 | derived-from 致谢本身含上游名 |
| `docs/contracts/` 契约语料、`ANALYSIS-*`、`ITERATION-ROADMAP-*` 中的事实性引用（含文件名 `ANALYSIS-2026-11-19-LoopForge-vs-Culler.md`） | 历史与研究事实，正文封闭；引用上游名即其本义 |
| `CHANGELOG.md` `## 0.1.0` 上游历史节 | 上游版本历史的事实记录 |
| `mc` / `spine` / `MC_*` 内部代号 | D15：工程内核代号不改（**非品牌**，随产品终身） |

## B · R2（A09）清除——运行时标识符（当前真实行为，文档如实描述）

| 面 | 现值（保留原因） | 终值 |
|---|---|---|
| CLI 命令与文档命令例 | `culler`（含 README/INSTALL/SECURITY 命令例与表格、client name `culler`） | `shz`（D20） |
| 部署 home | `~/.culler`、`~/.config/culler/github-token` | `~/.shorizon`（`MC_HOME` 名不变） |
| skill 链接目录 | `~/.agents/skills/culler`、`~/.claude/skills/culler`、`~/.codex/skills/culler` | `~/.agents/skills/shorizon`（R2 表） |
| launchd/systemd | `com.culler.*`（AGENTS.md 不变量段、build-prod 守卫） | `com.<域名根>.shorizon.*`（域名根=O9）；Linux 直接 `shorizon-*` |
| Keychain | `culler.keychain-db`、`Culler-<sha256>` | fork 后端 ADR-0（A01/A02）+ 迁移 |
| worktree 路径 | `/private/tmp/culler-worktrees`（AGENTS.md 明文规定） | `shorizon-worktrees`（A09 一行工单） |
| token 前缀 | `culler_proxy_*`（掩码正则 trace-mask.ts） | `shz_proxy_*` 类（R2，与 P2b 交集） |
| 交付分支与容器/refs | `culler` 分支（GIT-DELIVERY.md、failure-handling-spec.md、schema.sql RAISE 消息、README hero alt）、`culler-worktrees`、`Dockerfile.culler-project`、`.culler/` 目录 | schema 文本**必须蹭 v1 基线冻结一并改**（R2 原则：过 v1 再改=付费迁移）；其余随 A09 |
| 发布通道 | 发布名 `Culler <version>`（INSTALL 发布段）、install.sh 与 README/SECURITY 指向 `github.com/warlockedward/Culler` | 资产名与源指向按 A04/O8；**GitHub 仓库更名是所有者操作，更名后同步 URL 与本文档** |
| 代码内全部 `Culler` 字符串 | `mc/` `resident/` `dashboard/` `runner/` `release/` `scripts/` `tests/` `evals/` `acceptance/` `.github/workflows/`（数千处） | A09 按 R2 表逐面迁移；`openwiki/` 由 Scribe 重写（AGENTS.md 规则，人工不改） |
| dashboard 用户面标题/主题命名/logo/hero 资产 | 现值含 Culler 品牌 | R1 的资产替换需设计输入——**本批未做，挂 A09 或后续 R1 资产单**（不静默丢弃，此行为已知余项） |

## C · R2 末尾单独处理（agent 语义面）

| 处 | 说明 |
|---|---|
| 角色指令/brief/skills 内 "Culler token/Culler Project" | 唯一故意触及 agent 语义的改名：R2 末单独 commit + 角色指令检查（AGENTS.md 三件套同步义务） |

## D · 本批文档层的改写规则（已按此执行）

产品/系统名 prose → S-Horizon；**描述当前真实行为的标识符一字不动**（改了文档就撒谎）；"Culler token" prose → "platform token"；pun 动词 "cull" 保留（非品牌）。

## 验收钩

A08 证据须含：根级文档逐命中人工分类表 + 全仓 `grep -ri culler` 计数（B/C/D 类占比）；A09 收口时以"仅剩 A 类"为其验收判据之一。
