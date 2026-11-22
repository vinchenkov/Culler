# A14 验证记录 #3（TM §7.1 · V-3 git 交付层矩阵 · repo 信任面与具名 credential 缺口）

- verification_id: **A14-V-003**
- target_component: 容器内 `git` 的**配置继承与执行面**——产品环境形是否挡住仓库自带的 hooks/credential 向量（Worker 交付路径上的供应链面）。
- tested_version: 运行点=`shz/dev@67d7e52` 面（提交时刻 2026-11-21 22:41 +0800，14:39Z 运行）；记录器 `a14-v3-git-mechanism.sh`（sha256 `2d4a2bc8c94a380c60f51bb23db08c9a68cd5ebe11872dee5b5ab59b093447b7`）、结果表 `a14-v3-results-218.tsv`、原始件 `a14-v3-raw-218.log`（sha256 `5937f1a870f833f9182f6085428f96c22669c0d2a495fd24c5ac974204ecc97e`）。
- environment: 218.84.111.20（host=F1104），git 2.34.1（宿主实测版本）；一次性 401 监听器作挑战源；未装 unit、未 reboot。
- 仪器迭代（三次，全留痕）：① dry-run 因 refspec 空跑=假绿通道，弃；② **死端口不触发 helper**——序事实=**git 2.34 先连接后鉴权**，故早期"探测无响应"不作数；③ 一次性 401 监听器=终态仪器。
- results（逐行原样）：
  - `G1.naive-push` OBSERVED — 上游默认环境形下 repo 自带 `pre-push` **被执行**（操作者面中招的既成事实）。
  - `G2.global-null` OBSERVED — 只置 `GIT_CONFIG_GLOBAL=/dev/null` 不足以挡 repo 层。
  - `G3.product-env` **PASS** — 产品环境形（HOME 重定向 + `GIT_CONFIG_GLOBAL=/dev/null` + `GIT_CONFIG_NOSYSTEM` + `hooksPath` 重置）下 push 侧 **repo hooks 零执行**。
  - `G4.credential-helper` OBSERVED — **repo-local `credential.helper` 在真 401 挑战下被真实执行**：G3 的四件套**不覆盖 credential 向量**（repo config 系 git 自身信任层）。
  - `G4b.include-shape` OBSERVED — `include.path` 形态同层可达（登记为同族面，未单独处置）。
- 具名缺口与其闭合归属：全仓 grep 当时**无** `credential.helper` 重置、无 ASKPASS 处置 = **具名缺口呈报**（事实卡 `docs/facts/v3-git-mechanism-001.md`）；执行侧未顺手改交付域。该缺口的**后续闭合=A26**（`-c credential.helper=` 命令行末位优先空重置，已 CODED+EVIDENCED 于 dev，见 `evidence/A26/verification-record-A26-V-001.md`）——本记录保留为 A26 的 provenance 输入，**不据 A26 反写本记录为 PASS**（G4 的 OBSERVED 是"缺口存在"的证词，其修复证据在 A26 侧）。
- coverage boundary（不自扩）：矩阵覆盖 **push 侧 + credential 侧**；未覆盖 pull/fetch 面的等价性、未覆盖 hook 之外的 `alias`/`pager` 执行面、未覆盖非 2.34 版本的 git（版本漂移即需复测）。测试环境=容器内宿主 git，非镜像内 git 版本（若镜像 git 版本不同，结论随版本重测）。
- teardown: 一次性监听器随轮关闭；演练仓库与临时目录清除；宿主残留实测 0。
- verdict: **V-3 git 层=闭合（G3 PASS + G4/G4b 具名缺口在册并已另行闭合）**；产品路半（真 run 的交付路径上的同形验证）仍随 A25 持久段挂账。
- reviewed_by: host-agent（执行兼任复核声明沿用；Integrity 未做，不宣称）；verified_at: 运行 2026-11-21（+0800），登记 2026-11-22。
