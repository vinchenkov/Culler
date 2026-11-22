# A14 验证记录 #4（TM §7.1 · V-7 uid 矩阵 · 跨 uid 文件访问的拒绝面）

- verification_id: **A14-V-004**
- target_component: agent uid（10002）与其它 uid 之间对**脊/凭据投放面/运行日志/配置**的互读拒绝——多租户最小权限的实态证明。
- tested_version: 运行点=`shz/dev@67d7e52` 之后的同宿主续轮（2026-11-21 23:33 +0800=15:33Z）；记录器 `a14-v7-uid-matrix.sh`（sha256 `81bb9b95094cc066b5594b1f8e0c5512efccd33380144ca07540c0e5ae35cb20`）、结果表 `a14-v7-results-218.tsv`（sha256 `f809571211efdb961eb2fb255ca02acbf77481b8830b76fc977d5c1d58649f5c`）、原始件 `a14-v7-raw-218.log`（sha256 `7583b8bc3a16f6a321169d685a94d2200af2aa29f62b92434e64c2318be1325c`）、嵌套复测 `a14-v7-nested-recheck-218.log`（sha256 `f2dfd9266b82f7edff8332cd2d93555461ab3ba17d4e4e0636850f83a46022b7`）。
- environment: 218.84.111.20（host=F1104）；演练账户=当轮临时创建的**其它 uid**，原始件实测行取值为 `uid=3100`（4 行；另有 `uid=0` 驱动行 21 条与 `uid=10002` 产品段 6 条）——与产品 10002 段不撞；字面 argv；未装 unit、未 reboot。更正留痕：本行初稿写"uid 钉 2100 段"系**从记忆而非原始件取值**，登记前核 `a14-v7-raw-218.log` 才改为本值（2100 段是 A16 轮的账户约定，非本轮）。
- results（十格逐行，全 PASS）：`R1.spine-same-owner`（同主可读=正控，证明仪器能读）、`R2.provider-0644`、`R3.runlog-0640`、`R4.private-0700dir`、`R5.config-0600`、`R6.open-share`、`R7.spine-other-uid`、`R8.provider-other-uid`、`R9.runlog-other-uid`、`R10.config-other-uid` — 跨 uid 的四类敏感面一律拒读/拒写，同 owner 正控放行（**矩阵含放行格，故拒绝结论不是权限位一律为 0 造成的假象**）。
- **本轮的自纠（最重要的一段，如实留痕）**：初版对 provider 投放面用**扁平竞技场**（父目录 0700）得出"越权被挡"的强断言，随后复测发现**断言超出证据**：真实产品布局是 0700 目录**加**逐文件 bind 挂载的组合，嵌套形态下 per-file bind 的可见性与扁平目录不同。复测件=`a14-v7-nested-recheck-218.log`，两条都在嵌套竞技场下重跑后**都拒**，才把结论收回"复现装配形态"的作用域。**纪律入册**：竞技场必须复刻**装配/挂载语义**，不能只复刻 mode 位——"arena reproduces assembly, not just mode bits"。事实卡 `docs/facts/v7-uid-matrix-001.md` §2 含该自我修正段。
- coverage boundary（不自扩）：证明的是 **uid 维**的文件访问拒绝面；不含 ACL/xattr/SELinux/AppArmor 标签维、不含运行中通过 `/proc/<pid>/` 与 `ptrace` 的间接读取路径（该面属镜像与 A16 降权组合的另一族）、不含跨部署（多 spine）场景。
- teardown: 演练账户删除、竞技场目录清除；宿主残留实测 0（user=0/容器=0）。
- verdict: **V-7=闭合（含一次自我收窄的作用域修正）**。
- reviewed_by: host-agent（执行兼任复核声明沿用；Integrity 未做，不宣称）；verified_at: 运行 2026-11-21（+0800），登记 2026-11-22。
