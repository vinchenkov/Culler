# A02 diff-plan #1（正稿；预铸件的实例化。批文=轮二十一"全按推荐"+探针件。定稿批准注：ADR 尾行=待您笔，本 plan 以批文为开工据，若判"批文即定稿"请回一词）

**【轮二十二·实施前两处更正，具名如下，语义/验收零变】**
1. **blob 落点更正**：原写"`MC_HOME/host-credential-blobs/`"——**实施时发现与接口不相容**：store 方法签名=`(service, account)`，无 home 参数（darwin 靠 keychain 全局+service 内嵌 home 哈希避开了此题）。改 home 进接口=违反本 plan §2 禁触区。修正=**blob 与 salt 同置用户态状态根**（`$XDG_STATE_HOME`，默认 `~/.local/state` 下的 culler host-credentials 目录），blob 按 `<service>/<account>.enc` 隔离（service=`Culler-<sha256(规范 home)>` 天然一部署一命名空间）。**U-6/件 3 语义分毫不动**：MC_HOME 备份只含引用、不含 blob/盐 ⇒ 任何恢复=重授权；且此布局与 darwin 拓扑同构（真秘密在 MC_HOME 之外，F10 在册先例）。盐缺失=报错不再生（原样保留）。
2. **HKDF 取法升级**：原写"标准库内手写 HKDF-expand 12 行"（彼时未核工具链）——`.mise.toml` 亲验 pin **go=1.25.12**，而 `crypto/hkdf` 自 1.24 起即标准库 ⇒ **直接用 `crypto/hkdf.Key`**，手写件取消，依赖仍为零。

## 1. 正清单（Linux 后端；darwin 载体零触碰）

## 0. 设计修正一笔（诚实前置，非扩权）
ADR 草稿件 2 曾写 salt="0600/root 存 /var/lib"——**探针后自验推翻**：服务态=非 root（systemd --user 单元，A03 既定形态），root-owned 路径运行期不可写。修正=**salt 落用户态、MC_HOME 之外**：`$XDG_STATE_HOME 或 ~/.local/state/culler/credential-salt`（0600，首写生成，O_EXCL）。**U-6 语义完整保持**（MC_HOME 备份不含盐⇒任何仅-MC_HOME 恢复=重授权；新机 machine-id 异⇒重授权）；"同机全量状态恢复可解"= 灾备语义非备份语义，与件 3 批准文一致。

## 1. 正清单（Linux 后端；darwin 载体零触碰）
| 文件 | 改动 |
|---|---|
| `mc/deployment/host_secret_linux.go`（**新**） | `//go:build linux`。`platformHostSecretStore()` 返回 hostKeyStore：blob 目录=`MC_HOME/host-credential-blobs/`（ensureOwnerDirectory 形制，与 references 同礼）；密钥=HKDF-SHA256(IKM=machine-id 字节 ‖ salt, salt=blob 随机头, info=service+account 域分离)；AES-256-GCM，密文文件=`<service>-<account>.enc`（0600，含 12B nonce 头）；Delete 常规单删（无分片⇒不实现 DeleteParts，通用路径自落 :99）。machine-id 读=/etc/machine-id（缺⇒`/var/lib/dbus/machine-id`；皆缺=Err）；盐文件读不到=**Err（绝不静默再生**——再生即旧 blob 永久不可解而表面正常，植入"丢盐=全量重授权且报错诚实"测试） |
| `mc/deployment/host_secret_other.go` | 标签 `!darwin` → `!darwin && !linux`（一行） |
| `mc/deployment/host_secret_platform.go` | `HostCredentialCommandsAvailable()` linux=true（darwin 维持；test 标签不动） |
| `mc/deployment/host_secret.go` :15 | 失败常数中性化（操作者文本对抗审查触发：80/40 有/无色审到 accept） |
| `mc/cmd/mc/agentprofiles.go` :38 | CLI 闸语中性化（同义务） |
| `mc/deployment/host_secret_linux_test.go`（**新**） | 判别性组：写后读回=原值；**盐缺=拒且旧 blob 不可解**（负控：塞假盐不得解开真 blob=域分离检验）；machine-id 变=解不开（重授权叙事的行为证）；blob 篡改=GCM 认证拒；空/超限尺寸拒（:42 语义继承）；Delete 幂等面；**impl 断言：blob 文件内容不含明文**（子串探针，防"加密"名不副实）；HKDF/AEAD 为纯 stdlib+x/crypto(hkdf 或本地 51 行实现，倾向后者=少一依赖需裁——默认：**标准库内手写 HKDF-expand**（sha256 HMAC 循环，12 行），零新依赖） |
| 既有测试 | 转写仅当钉死旧失败语（grep 亲验后列，禁放宽）；`host_secret_other_test.go` 若按 `!darwin` 假设 Linux 吃桩须转写（列正稿后跑） |

## 2. 禁触区
`host_secret_darwin.go`（零字节）｜ 接口/通用路径 host_secret.go 除 :15 常数外零动 ｜ 消费面 4 文件（provider_credential/provider_tls/connection_delete/agentprofiles 除 :38 一行）零动 ｜ wire/schema/dispatch 零动 ｜ fixture 后端零动（CI 零凭据结构已成立）。

## 3. 验收
① `mc/check.sh` 全道（含 Linux 交叉编译面=本码首次被真实编译而非桩）；② 单测判别性组全绿；③ **218 端到端（credential-save→read→proxy-tls→delete + 删盐=重授权实跑）：⚑ 本 plan 初稿曾写"=a准授权内"——纠正：a准原文只覆盖三行只读探针；在生产宿主跑凭据动词=持久操作，需波门单独请示，未获令前此项具名延付（同 CI/Actions 域）**；④ A25 完成链解锁：Linux 向导可过第 2 步（真 harness 往返仍候 Actions/218 e2e——延付具名不变）；⑤ 扫描门按分域纪律：新 .go 入覆盖集核+blame-join+阳性对照。
EVIDENCED=①②③④⑤全；**A25 票同步只解"可 CODED"非端到端全证**（Actions 未开，具名分层）。

## 3.5 深水区两语判定（件 8 批准范围内的实施分岔，如实）
全上下文亲读（:695-731+测试面）后：
- **已改**：`:652` "macOS refused access…"→"The system refused access…"（EPERM 通用，原句在 Linux=假归因；零测试钉，grep 亲验）。
- **缓改（偏差申报）**：`:727` mac-install URL 的 OS 派生**不做**——本环境网络阻断（curl/WebFetch 皆败，000≠404）⇒**无法验证 Linux 文档深 URL 的真实性，拒绝凭记忆硬编码**（"文档不得制造机制/事实"红线）；且 `setup_terminal_prod_test.go:229` 现钉此 URL，改=连带转写。解锁条件=波门一句话（给正确地址）或 Actions 开启后 CI 现场核。**其余 §5 三语经亲读判"两 OS 皆真实"（Docker Desktop 存在 Linux 版），不改=避免为改而改**。

## 4. 风险/回滚
风险=盐路径语义与"重装 OS"交互已写死件 3；machine-id 变更（克隆 VM 常见）=全员重授权——**这是批准的安全姿态非 bug**，但须在 UPDATES/操作者文案一句（范围表件 8 内）。回滚=revert 两提交（码+账），darwin 面逐字无差可双证。
