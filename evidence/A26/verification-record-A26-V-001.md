# A26 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A26-V-001
- target_component: 宿主 git 原生执行器 credential.helper 清空隔离（V-3 G4 缺口的交付域修复；所有者 Q6 立票 + 本轮进码授权）。`mc/repository/git.go:process()` 的 fixed 配置表新增命令行 `-c credential.helper=`（末位优先，清空继承 helper 链，含 git 视为可信的 repo-local `.git/config` 面）。
- tested_version: shz/TA26impl（git.go 一行 + credential_test.go）；合并态以 AM 同轮 dev 提交锚定。
- environment: darwin 24.6.0 arm64，git（本机）；native executor 为 `__repository` 背后的 Go（repository.ts 经 `mc __repository` 抵达，E1 追链更正 diff-plan 的"repository.ts"措辞——真实 git env 在此，plan 已预告"进码时二次读源钉行号"）。
- attack_scenario：
  1. `./mc/check.sh`：**exit 0，0 FAIL**（全 Go 测试含新哨兵 + repository 全包 ok 44s）。
  2. **双向行为哨兵**（非字符串断言）：repo-local `credential.helper=!touch <pen>` → ① 产品 `g.process()` 环境跑 `credential fill` ⇒ pen **不生成**（重置生效）；② 同一硬化的 env **去掉重置**作对照 ⇒ pen **生成**（证明 repo-local 是 GIT_CONFIG_GLOBAL=/dev/null+NOSYSTEM 挡不住的幸存者，即 V-3 G4 实测向量；且证明是重置、非 protocol.allow 之类旁因挡下）。首轮我自己把 helper 写成 `echo <pen>`（打印路径不落盘）→ 控制组假"不执行"，被测出的正是我仪器的缺陷，修为 `touch` 后两向如设计。
  3. 不误清平台认证：该执行器**本地 file 传输、protocol.allow=never、不携任何平台凭据**（grep 全仓无 git 网络 push/transport），故重置不触及平台受控认证路径（无此路径可破）；A25 段 V-3 产品路取证不外扩其他凭据路径（票界）。
- expected_result: 恶意仓库携带的 credential.helper 在原生执行器认证协商时不执行宿主命令。
- actual_result: 单元行为达成（双向证）。真实部署 401 协商面（活 push/远程）归持久段，与 V-3 产品路一致随 A25。
- 残余：本执行器当前本地-only，向量是**纵深防御 + 未来协议面变更的前置硬化**；真实网络 push 路径若存在于交付域其它处（V-3 产品路），其 credential 处理随该票 A14-V-3 活部署确认。
- evidence_ref: evidence/A26/diff-plan-001.md、credential_test.go；V-3 源事实 docs/facts/v3-git-mechanism-001.md
- reviewed_by: host-agent（执行兼任复核声明沿用；Integrity 未做，不宣称）；verified_at: 2026-11-22 (+0800)
- 边界: CODED 证据；EVIDENCED/验收随票面后续轮与 F0a；不改他票钉选、不写真实凭据字面量（测试用 touch 哨兵，无凭据）。
