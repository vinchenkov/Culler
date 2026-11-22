# A14 验证记录 #1（TM §7.1 · V-2 残留族 + V-1a 三族发现面 · 容器边界实测）

- verification_id: **A14-V-001**
- target_component: 沙箱内 harness 配置的**读取面与持久面**——生产掩码（tmpfs/重定向）是否真挡住活发现源；哪些层跨 run 持久。
- tested_version: 运行点=`shz/dev@69f1ff9` 面（提交时刻 2026-11-21 21:22 +0800，紧随 13:19Z 运行）；容器基镜=宿主上的 `mc-prod` 标签（记录器内以该标签名直接 `docker run --user 10002:10002 --network none`，见 `a14-v2-residue.sh:37-52`；该镜像由本执行侧在 A16 轮以真机 `build-prod` 产成，其 arch=amd64 ∧ rev=9e0c54b 断言在册于 `evidence/A16/verification-record-A16-V-002.md`——本记录引用该在册断言，未在本轮重算）。记录器与结果表随票入库（`a14-v2-residue.sh` / `a14-v2-results-218.tsv` / `a14-v2-raw-218.log`）。
- environment: 218.84.111.20（host=F1104，x86_64，docker 29.4.0）；记录器一律**字面 argv**（无拼命令），正控 fail-fast=仪器自证；未装 unit、未 reboot、未触任何既有服务。
- results（逐行原样，ts/host/uid 前缀在表中）：
  - `R1.writes` **PASS** — 容器内写入路径可测（探针先证明自己在能写的地方写过）。
  - `R2.home-direct-persists` **OBSERVED** — `$HOME` 直层跨 run 持久（如实记观察，不升格为"设计如此"）。
  - `R2.masked-dirs-clear` **PASS** — 掩码目录随 run 清除（tmpfs 生命周期符合 A16 设计）。
  - `R2.cache-persists` **OBSERVED** — cache 卷持久。
  - 交叉发现（与同轮 V-1a 三族表合读，事实卡 `docs/facts/v1-cli-inventory-001.md`）：**"读的不持久、持久的不读"**——codex 的活源（`$HOME/.codex`、`CODEX_HOME`）恰在 per-run tmpfs 掩码之下（硬断言 PASS），而持久层（HOME 直层/cache）不被这三族当作配置源。两族各自咬合，故**残留污染通道未被观察到**，但该结论的作用域限于本轮实测的三族与该版本 CLI（版本正控 codex/claude v2.1.322/opencode v2.2.7）。
- coverage boundary（不自扩）：
  - 本记录**不**覆盖 V-5 族（socket/API/host-gw/loopback 鉴权与 X-1 三场景）——该族门控在活会话（A25 持久段/A02），**未执行、无记录**。
  - 直层持久面的**策略处置**（是否需要跨 run 清洗）属在册呈报项，不由本记录代裁。
  - upscan 探针：形状缺陷后经自纠**改标未测**（clean 不洗结论），故其格位在组建册中为 UNKNOWN，本记录不为其出 PASS。
- evidence_ref: `evidence/A14/a14-v2-results-218.tsv` sha256 `55f620e636768a1eb06647b12b6e2cec8d57e16723d4ad06ffbbf494941d7c44`；`a14-v2-raw-218.log` sha256 `6125700a8fae2ceefa032b5597e63b3ce5c72b2263b4c37afc14407b5074a721`；三族发现表 `a14-v1a-measured-218.tsv` `175a4414…5983` / `a14-v1a-claude-measured-218.tsv` `f98129e0…2ff3c1` / `a14-v1a-opencode-measured-218.tsv` `866ced89…ac30f4`；原始件 `a14-v1a-raw-218.log` `2cce0c21…0d1b82`。
- teardown: 演练目录与临时容器随轮清除；宿主残留于当轮实测为零（A16 轮同宿主复验），mc-prod 镜像**声明性保留**为后续轮部署材料（非残留垃圾，特此声明）。
- verdict: **V-2 残留族=已实测（PASS/OBSERVED 逐格如实，零预设）**；A19 引用键的 V-2 半边自本记录起**存在**（`A14-V-001`），V-5 半边仍缺。
- reviewed_by: host-agent（执行兼任复核声明沿用；Integrity 未做，不宣称）；verified_at: 运行 2026-11-21（+0800），登记 2026-11-22。
