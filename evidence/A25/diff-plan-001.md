# A25 diff-plan #1（实施轮 A25a：向导 Linux 放行+Mac 文案中性化——含 a/b 分票申报）

状态=CODED（cmd/mc 全包+文案对抗审查在途，回填 §4）。授权=轮十六"批准整条议程"（W2 列 A25）；前置侦察=`evidence/A25/pre-probe-001.md`（§1 已就位面+§2/§2.5 全量闸账）。**顺序义务：本文件先于任何合并落地，码改动小面可全 sightline（4 行文本+1 条件）。**

## 0. 文案对抗审查结论 + ⚑ 审方 LOUD 发现（改变 EVIDENCED 语义，如实采入）
审方（80/40 列、有/无色，读渲染器亲验）裁：原四语 **ACCEPT**，另点名**同面两漏改**并给出确切替换词，本支已逐字采用：
- `setup_terminal_prod.go:475` "Set up this Mac" → **"Set up this machine"**（审方算：23 列，40 列菜单栏 ≤32 适配）。
- `terminal_presentation.go:36,149` "Mac setup complete" → **"Setup complete"**（严格更短=适配只增；同处 `terminal_presentation_test.go:24/:30` 双向断言**同串转写**——`:24` 负控"完成前不宣告"+`:30` 正控"Complete() 后必现"，语义逐字不变=转写非放宽；onboarding 包 `ONB_EXIT=0` 亲验）。
- 审方另有 4 处同族 Mac 文案（`:671` "Mac requirements"/`:708-728` Docker Desktop 复制+mac-install URL/`:652` "macOS refused"/`:658` "Open Docker Desktop"）**判为显式后续票缓改**，本支不动（登记 §5）。
**⚑ LOUD（审方，我 §2 未料到的更早一墙）**：Linux 向导**放行后走不完**——第 2 步凭据经 `provider_credential.go:165→WriteHostSecret`，`host_secret_other.go` 后端返 `ErrHostCredentialUnavailable`=**"Unlock the login Keychain"**（Linux 无人能做的动作）。此墙**早于**我发现的生命周期墙（:222）。⇒ **A25a 单独=半承诺**：Linux 能开向导、卡在第 2 步。审方裁定：**dev/分支层可合**（ACCEPTABLE-WITH-TICKET），**发布层是 BLOCKER**（须与 A02 凭据后端 + A25b 生命周期门同批）。
**我的 EVIDENCED 语义据此收紧**：A25a 本机可证的只是 **Mac 无回归 + Linux 编译 + 文案适配**；"Linux 向导端到端跑通"**本机不可证且现亦不真**（凭据墙未破）——不标 EVIDENCED，停 **CODED**，端到端延付=A02 落地后 + A32/218 运行域（双重具名）。**这也把"Linux 宿主可用"的真实关键路径点明=O1-b→A01→A02（W2 已列）而非 A25a**；A25a 只是把入口 Mac 锁去掉，A02 才是让 Linux 走完的那半。

## 1. A25a 正清单（本支）
| 文件 | 改动 | 性质 |
|---|---|---|
| `mc/cmd/mc/setup_terminal_prod.go` :99-101 | 平台闸 `!=darwin` → `!=darwin && !=linux`；拒绝语中性化 | S1+S2 |
| 同文件 :104 | agent 域拒绝语去 Mac 字样（**判定逻辑一字不动**=run.json 动词域不变量） | S4 |
| 同文件 :88 | 阶段标签 "Prepare your Mac"→"Prepare your machine" | S3-a |
| `mc/cmd/mc/setup_choices_prod.go` :110 | 面板标题同中性化 | S3-b |
禁触：`culler_prod.go:222`（G-LIFE）/`private_release.go:129`（G-OPEN）/`host_secret_platform.go`（G-SEC 正确锁，A02 域）——**见 §2 分票**。

## 2. ⚑ A25b 申报（新发现面，本支不做，候裁即办）
前置侦察 §2.5 全量普查发现：**操作者主面生命周期门** `culler_prod.go:222`（`GOOS!=darwin || !vision` ⇒ "Lifecycle operations require the Culler Mac host."）仍 Mac 锁——Linux 机跑完 A25a 向导后 `culler update/services` 即撞此墙，**向导移植只成半个"Linux 宿主可用"**。
- 四件套证据在案：systemd 监督 A03✓ / 双架构发布物 A04✓ / 更新链 Linux 段 A27+A28✓ / **A24 谓词设计语义=视觉门非 Mac 门**（其 EVIDENCED 判据本就是目标机三域矩阵，GOOS 半=历史锁）。
- 形状候选（归所有者裁，我不代决）：**B1** 去 GOOS 半留 `canInspectProjectVision()`（与 A24 设计一致，最小）；**B2** GOOS∈{darwin,linux}（与 A25a 同形保守）；**B3** 维持现状（Linux 生命周期继续拒=与 D3 部署主形态矛盾，需理由）。G-OPEN（`private_release:129` Apple-silicon open 句）随 B 一并定形。
- **不并入 A25a 的理由**：票面 S1-S5 之外扩大操作者面=形状决定，按"一项决定不扩成他项授权"自律，停报候裁。文案矛盾（#1"Mac or Linux host" vs 生命周期"require the Culler Mac host"）**已被审方点名风险**——B 裁前向导可合但操作者会撞墙，审方结论将具名此窗口。
- S3 归属（A25 直改 vs 随 A09 改名波）：本支**已在 A25 直改**（中性词与品牌词无冲突，A09 的 shz 面届时另行）；申报在此，误则零代价回撤。

## 3. 义务登记
- 文案对抗审查（AGENTS 触发=4 语）：在途，结论+修订循环回填 §4；审查含跨消息一致性（§2 B 裁前窗口）。
- 转写面：全仓无测试钉旧四语（grep 亲验）⇒零放宽需求。
- Linux 端到端：S5 载体=A32 runner 首跑（候属主 Actions）——A25a 合并不等它（CODED 即合，EVIDENCED 延付具名，A31 同形）。

## 4. 证据（真实读取，逐件）
- 双平台编译：`BOTH_BUILD_OK`（darwin + linux/arm64 亲跑，含改后 onboarding/cmd-mc）。
- cmd/mc 全包（**post-:475-fix 新鲜跑**，弃用改前陈旧那次）：`A25_CMD2_EXIT=0`（`ok mc/cmd/mc 414.0s`）——证审方两改点+完成语转写无破；且此包绿即证**无 cmd/mc 测试钉旧四语**（若钉必红）。
- onboarding 包：`ONB_EXIT=0`（`ok mc/onboarding 2.3s`，含 `terminal_presentation_test.go:24/:30` 双向断言同串转写——`:24`"完成前不宣告"负控 + `:30`"Complete() 后必现"正控，语义逐字不变=转写非放宽）。
- 文案对抗审查：首轮 4 语 ACCEPT + 2 处漏改 REVISE（`:475`、`:36/:149`）→ 已逐字采用审方原词（"Set up this machine"/"Setup complete"，其 40 列适配自算且严格更短）→ **ACCEPT-TO-MERGE**（审方原话，dev/分支层）；**BLOCKER 仅在发布层**（凭据墙，见 §0）。
- 全道 mc/check.sh + 扫描门：`=?`（回填位，合并前硬规则；扫描按 v0.1.90 分域纪律先核被改 .go 在覆盖集——Go 文件历轮证在集）。

## 5. 审方缓改面（后续票收，本支不动=登记不扩面）
`:671` "Mac requirements"、`:708-728` Docker Desktop 复制文案+mac-install URL、`:652` "macOS refused"、`:658` "Open Docker Desktop"——均属 Linux 向导**深水区文案**（Docker Desktop 在 Linux 不存在），改它们须与 A02 凭据墙+Linux 首跑真实语义一起定（否则改了词仍走不通=制造假就绪）。归 A02/A25b 后续统一收，diff-plan §0 已具名。**这些是"Linux 能用"承诺的组成部分，不随 A25a 半改**。
