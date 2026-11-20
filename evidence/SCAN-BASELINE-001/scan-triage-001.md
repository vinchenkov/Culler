# SCAN-TRIAGE-001 · 基线 486 条聚合（决策支持件，不构成处置）

- 源扫描绑定：SCAN-BASELINE-001（shz/dev@fa8c07b，findings.json 字节绑定=`5899bb37…3983`，seal 9107b049…）；全部条位于上游既有代码目录（新代码零出现于基线=其文件级覆盖由目标根拆分另行证明）。
- 本件=聚合+**已实证误报族**标记。族标记只覆盖列明谓词；其余逐条处置（真缺陷立项/关闭/接受）归所有者，本件不预判、不代关。

## 严重度×类分布

| 严重度 | 类 | 条数 |
|---|---|---|
| high | other-security | 278 |
| medium | other-security | 109 |
| high | command-injection | 72 |
| high | ssrf | 15 |
| high | hardcoded-credential | 4 |
| high | path-traversal | 4 |
| low | insecure-randomness | 4 |

## 目录分布

| 目录 | 条数 |
|---|---|
| dashboard | 161 |
| mc | 149 |
| runner | 78 |
| resident | 62 |
| evals | 19 |
| performance | 10 |
| release | 7 |

## 已实证误报族（实例级证据先例，非整类推断）

| 族（谓词） | 命中 | 依据与处置建议 |
|---|---|---|
| class=command-injection（全部） | 72 | 该仓 git/docker 调用全部为 exec.Command argv 数组形态，无 shell 拼接；同族实例已两轮实证（runner SQL 三例、A12 systemstatus:83）。建议：抽样 10 条复验后整族关闭；未抽样条不自动豁免 |
| hardcoded-credential ∧ mc/config/config.go | 3 | A05 扫描门逐条 blame 实证=凭据**类型名常量**（非秘密），9dbc043 上游。建议关闭 |
| ssrf ∧ evals/ 前缀 | 12 | evals=离线评测工具面（不入发布清单），其网络取回是设计行为；生产不受其影响。建议关闭或移交评测卫生 |

## 优先人工分诊队列

high 级、不落入上述谓词 = **286 条**（含 other-security 278、mc/deployment/authorization_failure.go 的硬编码疑点 1、路径穿越 4、非 evals SSRF 若干——逐条判真/误；SSRF 生产面命中若为真属高优先）。medium/low 共 113 条随后。

机器可读逐条册（NDJSON）随本件同目录 `scan-triage-001.ndjson`；聚合器=本文件，逻辑在文内自述可复核。
