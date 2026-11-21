# V-2 跨 run 残留事实卡 #1（2026-11-21，a14-v2-residue.sh @218，a14-v2-results/raw-218 入档）

形状=生产装配逐字复刻（HOME=项目 home bind + `.agents/.claude/.codex` per-run tmpfs 掩码 + /cache 命名卷；`--user 10002` 全同）。run#1 写三标记 → run#2（全新掩码、同 bind/同卷）读三处。

## 实测（4 行全绿/如实）
| 面 | 结果 | 定性 |
|---|---|---|
| HOME **直层**（`/home/agent/*`） | **持久**（OBSERVED） | 模型可写 + 跨 run 存活 = V-2 的策略承载面 |
| 掩码目录（`/home/agent/.claude/…`） | **清除**（PASS） | per-run tmpfs 按设计隔断 |
| `/cache` 命名卷 | **持久**（OBSERVED） | 设计共享（字节帽 CULLER_CACHE_LIMIT_BYTES=2GiB 在册） |

## 与 V-1a 两轮交叉=核心安全结论（"读的不持久、持久的不读"咬合）
- **codex**：活发现源=`$HOME/.codex/`（V-1a 实测唯一活源）——恰在**掩码层** ⇒ 上 run 写的 codex 配置**不会被下 run 读到**（清除+不读双保）。
- **claude**：活源=`$HOME/.claude.json`（**直层=持久面**！）——但产品 `CLAUDE_CONFIG_DIR` 迁移（adapter:61，V-1a C1 实测挡效）使其**不被读** ⇒ 持久但不读。
- 残余（如实呈报，非本卡可判）：直层仍持久**其他**文件名——任何"未来进程按 HOME 直层读某文件"的新发现源（bash rc/其他 CLI 版本漂移）都会继承"持久+可读"组合。=**V-1d 时序与 CLI 版本重测**归活会话轮的原因；项目目录源（仓库可写）在 claude 由 `settingSources:["user"]` 关闭（SDK 单元面）、在 codex 无 cwd 源（实测）。
- 政策问题留所有者/活会话轮：项目 home 直层是否需要收窄（如 root-squash/只 seed 清单强制）——本卡只呈事实面与咬合结构，不预设结论。
