# A24 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A24-V-001（CODED 证据轮；EVIDENCED 需 post 三域矩阵，见残余）
- target_component: 操作者视野门 + helper 委托门的 Linux 宿主放行（diff-plan-003 双谓词版，IMPL-AUTH-003A 发出授权，绑 003A 全值 `1227f5ad…3873`、diff-plan-003 `d557d5fb…3d4c`）。`canInspectProjectVision` 与 `shouldDelegateToHelper` 从 `GOOS==darwin` 改为 operator-host 判据（`{darwin,linux}` 且无 `/.dockerenv`）；身份层 `RequireHostScope`（run.json）与锁域守卫 `GuardLockDomain` **逐字未动**（独立两道防线）。
- tested_version: shz/TA24impl（两谓词 + seam + 单测）；合并态以 AM 同轮 dev 提交锚定。
- environment: darwin 24.6.0 arm64（mise go 1.25.12）。
- attack_scenario（执行清单与实测退出码）：
  1. `./mc/check.sh`：**exit 0，0 FAIL**（gofmt/vet 全 tag 含 linux arm64、shell 族、全 Go 测试含新三域矩阵用例）。
  2. 三域矩阵单元（seam 注入）：darwin-host→两谓词 true；linux-host→**true（A24 变更）**；linux-helper-container（/.dockerenv 在）→false；darwin-container→false；unsupported-os→false。**负证 TestContainerDomainNeverWidens**：任意 OS 下 /.dockerenv 在场 ⇒ 两谓词恒 false（放宽不外溢容器）。首轮我自己的测试用 `errors.New("no such file")` 作 notExist，`os.IsNotExist` 不匹配纯文本错误 ⇒ linux_host 如实 FAIL，改 `os.ErrNotExist`（=Stat 真实返回）后绿——**仪器自纠，非放宽断言**，与 Q7/R1 同族教训。
- expected_result: Linux 宿主操作者可经 `culler`/mc 宿主动词行使视野，容器域行为逐字不变。
- actual_result: 谓词层达成（seam 证三域返回值）。**关键前置事实（pre 基线轮实测，diff-plan-003 §决定性发现）**：生产宿主 mc 的 spine 路径=常量 `/mc/spine/spine.db`（helper 视角），故视野动词在 Linux 宿主真达后端**依赖 `shouldDelegateToHelper`→warm helper 委托链**（本票已同翻该谓词），而 Linux 侧 helper 的实际预热/编排归 A03 监督栈 + A25 向导段。
- 残余（EVIDENCED 前置，不吞）：**post 三域矩阵须在定向机以在位部署真跑**（H 域经 helper 业务应答、HELPER/AGENT 域逐字等于 `a24-pre-baseline-218.tsv` 基线含 `AGENT.*` 行零变化）——该轮需 Linux 持久部署（A25 向导段产物），如 A16-e2e/V-003 惯例记为运行域待定向机，本机/无部署不作 PASS。CODED 证据已交，票保持 IN_PROGRESS 至 post 矩阵。
- evidence_ref: evidence/A24/（diff-plan-001/002/003、a24-vision-acceptance.sh、a24-pre-baseline-218.tsv、本记录）
- reviewed_by: host-agent（执行兼任复核声明沿用；Integrity 未做，不宣称）
- verified_at: 2026-11-22 (+0800)
- 边界: CODED=代码+单元证据已提交，非验收；义务完成判定随 F0a Exit + post 矩阵 + Authority。
