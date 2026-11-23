# A25b diff-plan #1（B1 切片：生命周期门去 GOOS 历史锁；含"先改后列"再犯的如实自记）

**⚑ 顺序违例自记（同族第三次：A23 申报过一次、A25a 预铸避免了、本轮又犯）**：本文件写于码改动之后。事实=码改动极小（2 行条件+文案）且双平台编译/vet 先于本文件完成，但**纪律是"先列后改"不分大小**；升格自课：**开支后的第一个动作=Write 本 plan 骨架（哪怕只有标题+清单占位），第二个动作才是编辑任何源文件**——写进 §H 供波门审。若波门判本切片需按序重做，回撤=1 revert 重放（零连带）。

## 1. 正清单（批文=轮二十一"全按推荐"内 A25b=B1；pre-probe §2.5 四件套证据）
| 文件 | 改动 | 语义 |
|---|---|---|
| `mc/cmd/mc/culler_prod.go` :222 | `GOOS != "darwin" \|\| !canInspectProjectVision()` → `!canInspectProjectVision()` | **B1 逐字**：GOOS 半=历史锁删除；门=视觉+委托谓词（A24 设计件语义），Linux 宿主自然通过 |
| 同文件 :223/:228 | 两语 "Lifecycle operations require the Culler Mac host." → "Lifecycle operations run on the operator's Culler host, not in an agent." | 操作者文本（对抗审查待跑，见 §3）；一句同时 truthful 覆盖两失败因（谓词拒=非操作者位；身份非 host-scope=agent 位） |
| 同文件 import | 删 `"runtime"` | **编译器抓到我的错判**：我一度以为 :96 `runtime.Release` 仍用包——实为 `:94 runtime, err := readCullerRuntime` 的**局部变量遮蔽**。若没 build 就是一枚"记忆≠事实"上线。教训：结论性断言（"仍被使用"）须以编译器/读取为凭 |
| `cmd/mc/culler_prod_test.go` :519/:565 | skip 条件 `GOOS != darwin` → `!canInspectProjectVision()`；理由句同步 | 测试在产品会拒的同一谓词处 skip（同谓词一致性），Linux 上不再被 GOOS 白跳 |
| 同文件 import | 删 `"runtime"`（该文件唯一使用即两 skip 条件） | |

## 2. 禁触区
`canInspectProjectVision()` 本体（A24 域，零动）｜`private_release.go:129` **G-OPEN（"Apple silicon open"句）不动**——B1 批准只覆盖生命周期门；G-OPEN 在 pre-probe 标"随 B 一并定形"但我把它留在申报单里未获单独确认，**不擅自并入**（一项决定不扩）。｜dashboard/resident 零动。

## 3. 证据与延付
- 双平台编译+vet：`DARWIN_OK / LINUX_OK / VET_DONE`（亲跑）。
- 待跑：合并前 mc 全道；操作者文本对抗审查（两语，80/40 有/无色——与 A02 的四语合并一次审查单，节省往返回合：审后再合 dev，若审出改词则改词重审至 accept）。
- **EVIDENCED 延付具名**："Linux 宿主跑 lifecycle 真绿"=CI Linux（候 Actions）或 218（生产宿主逐次候令）；与 A31/A32/A25 同一解锁闸。故 A25b 状态=CODED/IN_PROGRESS，不标 EVIDENCED。
- 与 A24 的账：A24 的三域矩阵 EVIDENCED 本就含"warm-helper=目标机"段（在册），A25b 不重复其义务、亦不借其状态。
