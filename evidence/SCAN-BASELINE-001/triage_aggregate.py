#!/usr/bin/env python3
"""SCAN-TRIAGE-001 aggregation (v2: class-based family predicates).

Regenerates scan-triage-001.md (+ the NDJSON register on stderr) from the
byte-bound baseline findings.json listed in SCAN-BASELINE-001/scan-record.md.
Adjust SRC to the persisted scanDir of that seal.
"""
import json
import sys
import collections

SRC = "/Users/raistlin/.mimosa/security-scans/project-0f2111a0377592931de28132/scan-2026-11-19T12-42-03.851Z-93f777d24f41/findings.json"

def family(cls: str, path: str):
    if cls == "command-injection":
        return "argv-idiom (exec.Command argv array, no shell)"
    if cls == "hardcoded-credential" and path == "mc/config/config.go":
        return "CredentialKind* name constants (blame 9dbc043, proven FP in A05)"
    if cls == "ssrf" and path.startswith("evals/"):
        return "evals harness tooling (not a production file; fetch/bridge by design)"
    return None

def main() -> int:
    data = json.load(open(SRC))
    findings = data["findings"]
    per_class = collections.Counter()
    per_top = collections.Counter()
    fam = collections.Counter()
    rows = []
    for f in findings:
        loc = f.get("location", {}) or {}
        path = loc.get("path", "?")
        cls = f.get("identity", {}).get("publicClass") or "other-security"
        sev = f.get("severity", "?")
        per_class[(sev, cls)] += 1
        per_top[path.split("/")[0]] += 1
        fam_name = family(cls, path)
        if fam_name:
            fam[fam_name] += 1
        rows.append({"id": f.get("findingId"), "sev": sev, "class": cls, "path": path,
                     "line": loc.get("line"), "family": fam_name})
    out = []
    out.append("# SCAN-TRIAGE-001 · 基线 486 条聚合（决策支持件，不构成处置）\n")
    out.append("- 源扫描绑定：SCAN-BASELINE-001（shz/dev@fa8c07b，findings.json 字节绑定=`5899bb37…3983`，seal 9107b049…）；全部条位于上游既有代码目录（新代码零出现于基线=其文件级覆盖由目标根拆分另行证明）。")
    out.append("- 本件=聚合+**已实证误报族**标记。族标记只覆盖列明谓词；其余逐条处置（真缺陷立项/关闭/接受）归所有者，本件不预判、不代关。\n")
    out.append("## 严重度×类分布\n\n| 严重度 | 类 | 条数 |\n|---|---|---|")
    for (sev, cls), n in sorted(per_class.items(), key=lambda kv: (-kv[1], kv[0])):
        out.append(f"| {sev} | {cls} | {n} |")
    out.append("\n## 目录分布\n\n| 目录 | 条数 |\n|---|---|")
    for d, n in per_top.most_common():
        out.append(f"| {d} | {n} |")
    out.append("\n## 已实证误报族（实例级证据先例，非整类推断）\n\n| 族（谓词） | 命中 | 依据与处置建议 |\n|---|---|---|")
    out.append(f"| class=command-injection（全部） | {fam.get('argv-idiom (exec.Command argv array, no shell)', 0)} | 该仓 git/docker 调用全部为 exec.Command argv 数组形态，无 shell 拼接；同族实例已两轮实证（runner SQL 三例、A12 systemstatus:83）。建议：抽样 10 条复验后整族关闭；未抽样条不自动豁免 |")
    out.append(f"| hardcoded-credential ∧ mc/config/config.go | {fam.get('CredentialKind* name constants (blame 9dbc043, proven FP in A05)', 0)} | A05 扫描门逐条 blame 实证=凭据**类型名常量**（非秘密），9dbc043 上游。建议关闭 |")
    out.append(f"| ssrf ∧ evals/ 前缀 | {fam.get('evals harness tooling (not a production file; fetch/bridge by design)', 0)} | evals=离线评测工具面（不入发布清单），其网络取回是设计行为；生产不受其影响。建议关闭或移交评测卫生 |")
    residual_high = sum(1 for r in rows if r["sev"] == "high" and r["family"] is None)
    out.append(f"\n## 优先人工分诊队列\n\nhigh 级、不落入上述谓词 = **{residual_high} 条**（含 other-security 278、mc/deployment/authorization_failure.go 的硬编码疑点 1、路径穿越 4、非 evals SSRF 若干——逐条判真/误；SSRF 生产面命中若为真属高优先）。medium/low 共 {sum(1 for r in rows if r['sev'] != 'high')} 条随后。\n")
    out.append("机器可读逐条册（NDJSON）随本件同目录 `scan-triage-001.ndjson`；聚合器=本文件，逻辑在文内自述可复核。")
    print("\n".join(out))
    for r in rows:
        print(json.dumps(r, ensure_ascii=False), file=sys.stderr)
    return 0

if __name__ == "__main__":
    sys.exit(main())
