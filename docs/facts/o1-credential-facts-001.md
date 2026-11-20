# O1-CREDENTIAL-FACTS-001 · 凭据后端：事实基线（O1-a）与候选方案材料（O1-b 输入）

性质：**决策支持件**。含事实清单、规范需求锚点、候选载体材料、六检查轴逐候选事实矩阵（§3b）、UNKNOWN 登记与批准前置清单。**本件不做比较结论、不推荐、不裁决**——架构比较、风险分析、推荐方案与批准前置的给出归部署所有者（2026-11-20 指令："先冻结 O1 的事实基线，再批准后端选型；不一次性批准长链"）。E1=本会话直读文件（行级锚点）；F*=事实；U*=UNKNOWN（不得以选型偏好代替）。

## §1 事实基线（O1-a）

**存储抽象与平台现状**
- F1 宿主密钥统一走 `hostSecretStore` 接口（Read/Write/Delete）+ 抽象层 host_secret.go；darwin 实现=登录钥匙串，经 `/usr/bin/security` 子进程 + service/account manifest（host_secret_darwin.go:33-148，argv 数组无 shell）。
- F2 **非 darwin 平台实现=显式 unavailable**：`host_secret_other.go` 全部方法返回 `ErrHostCredentialUnavailable`；错误文案为 macOS 专属（"Unlock the login Keychain"）——移植 Linux 时该文案亦需处置（表面事实，登记备查）。Linux **文件布局**原子交换已有实现（runtimeauth_exchange_linux.go RENAME_EXCHANGE），与密钥存储无关。→ A02 票面（host_secret_linux+迁移工具）承载此缺口。
- F3 `config.toml` 的 `CredentialSource` 为**纯元数据**（kind/name/urls/client_id/catalog_entry，mc/config/config.go:73-80）——**无秘密值**；秘密值的引用面=钥匙串 service/account 与 MC_HOME 下的引用路径文件（host_secret_lifecycle.go:18-57 retain/delete）。
- F4 本地生成 token 的例外面：agentsview/wiki viewer token=**明文文件**，纪律=0600+属主 uid+硬链接数=1+64-hex 形态校验（viewer_secrets.go:20/133）。O1 ADR 须显式声明该面**在范围内（换载体）或列为记录在案的例外**，不得留空白。

**读取/写入/生命周期主体（对应 IG 表 A）**
- F5 读取：宿主进程专属。proxy 凭据经 resident→`runMc(["agent-profile","credential-read"])` 取记录（provider-credentials.ts:81；记录含 api_key/access_token/id_token/refresh_token 子集 :333），stdout 单 JSON 回宿主内存；**agent 容器不持原密钥**——agent 只见 `culler_proxy_*` 作用域 token（D-IG-1：服务端哈希存储、可撤销；forbidden 头正则禁凭据外泄 provider-proxy.ts:131；SecretFilter+redactJSON 全路径脱敏 :219-260/341+）。credential-read 属内部命令，Collie 侧提交被传输层拒（A22 期实跑用例在案）。
- F6 写入/签发：saveAgentConnection+revision CAS（deployment/api_key_validation.go:64-77）；OAuth 获取/续期=宿主 runtimeauth 族（acquire/exchange/verifier；codex 登录流）。撤销：删连接→`deleteReplacedProviderReferences`+`DeleteInstallationHostSecrets`（卸载路径）；治理面冻结/撤销双签语义归 IG 表 A/B。
- F7 轮换：`credential-renew` 主动 OAuth 续期（provider-credentials.ts:104）+ 条目版本推进语义（IG §3-⑥：旧版本即时不可用）。**刷新令牌的持久化载体=同一 hostSecretStore**（refresh_token 在记录内）。

**跨进程/重启/恢复行为**
- F8 跨进程共享=**经宿主 CLI 串行读**（mc 唯一开脊者+resident spawn），无多写者共享同一密钥文件的路径；钥匙串本身承担并发。
- F9 重启后可用性（darwin）：登录钥匙串需**已解锁会话**；launchd 常驻的 daemon 在无 GUI 登录会话时的钥匙串访问=环境事实（当前开发机=macOS GUI+登录会话；**生产目标形态下该项未证** → U-3/U-5）。
- F10 备份面：DC §7 备份七要素覆盖 spine+配置；**OS 密钥库不在 MC_HOME 内**→当前恢复语义=凭据重新获取（非随备份还原）。该事实在 O1 决策中定型：备份含密（不可能，OS 库外置）/恢复流程显式重获（现状）/后端可备份化（KMS 类）。
- F11 不可用=fail-closed 已实证：`ProviderCredentialError(origin=credential_store)` → proxy 502 语义、无回退字面量、无静默降级（provider-proxy.ts:33-35 与 PS §3-① 语义一致）；IG §5.1"〔待:文件〕格=Planned 禁入生产"为治理侧同律。

**环境一致性**
- F12 CI：PR 作业零 provider 凭据零发布权限（AGENTS 不变量+release 工作流）。测试=合成字面量（`synthetic-provider-*`）+注入桩，**不依赖任何真实后端**——即 dev/CI 现状与"生产后端选型"解耦面已经存在。
- F13 三部署层（dev/source-prod/published）共用同一 hostSecretStore 抽象；dev 层（fake harness）当前亦走真实 darwin 钥匙串（同一代码路径，无 mock 后端）。

**轴一/轴四补证（本轮新读，供 6 轴矩阵引用）**
- F16 spine 零凭据值列：schema.sql 内 `credential` 仅出现为失败**归因枚举标签**（`cause IN ('unknown','credentials','quota','connection')`，:557），无 api_key/access_token/refresh_token/secret 值列。
- F17 proxy token 存储形态=**进程内存**：签发时只留 SHA-256 digest（provider-proxy.ts:137-139，查持 :527），原 token 单次交付；`revokeConnection` 与全表均为内存态（:116）→ **proxy 重启=全部代理 token 即刻失效**，agent 侧需重新注册（现状行为事实：轮换简单，可用性耦合单进程）。
- F18 日志/事件/错误全路径脱敏：SecretFilter+redactJSON+多形编码覆盖（原文/JSON 转义/percent/base64/base64url/unicode 大小写，provider-proxy.ts:219-260/232-243），observation 与 refusal 体皆过；axis-1 的"日志/调试输出"面现状=**多层主动脱敏而非依赖后端**。

**Git 面（IG v0.2.2 明令 O1 ADR 显式覆盖）**
- F14 全仓 **零** GIT_ASKPASS/credential helper 管理代码（检索实证：仅 provider/credential 命名命中，无 git 凭据路径）→ 现状=宿主 git 环境凭据**由操作者 ambient 配置**，不受产品管理；与 TM B8-I"token 不出宿主"的宣称之间存在**未闭合面**：目的地/host 的 git 凭据读取路径既未固定也未测试（V-3/A16 域交叉）。ADR 须对 Git 行三选一：纳入同一后端/规定显式 helper 白名单/声明例外与残余。
- F15 push/PR 的执行域与凭证注入归 DT（§0 权威边界），O1 只裁决**存储与读取载体**。

## §2 规范需求锚点（既有契约的消费，非本件新设）
IG 表 A（Provider/Git 行：读取主体/轮换/撤销）；IG §3-⑥版本推进；D-IG-1；PS §3-①（secret 解析失败 fail-closed——已满足，F11）；TM B8-I/S；DC §7.4 双事件轮换**模式**（可用于后端自身轮换）；AM A8（表逐格落地纪律）。D-IG-2 六项验证（IG-T4）属**锚定密钥 O11 域**，与 O1 供应商凭据域不同，勿混装。

## §3 候选载体材料（O1-b 输入；各候选=事实描述，无评分）

| 候选 | 平台/依赖 | 读取主体形态 | 重启/headless | 轮换-撤销-恢复 | 环境一致性 | 实现现状 | 已知事实性风险 |
|---|---|---|---|---|---|---|---|
| **C0 darwin-keychain（存续）** | macOS only；`/usr/bin/security` 子进程 | 现状 F5-F7 | F9：登录会话依赖，headless Linux 不适用 | 现状 F7 | dev=生产（仅 mac） | **已实现已测**（darwin 面全套测试在仓） | 单平台；A02 目标不可达 |
| **C1 age+口令文件** | 跨平台；age 二进制+口令输入通道 | 解密到宿主内存（同 F5 面） | 口令供给=交互/单元配置——headless 下口令从何来是核心问题（无 OS 会话则需另一信任根，退化为配置问题） | 文件级替换=原子 rename 可携；撤销=重加密 | dev/CI 可全同构（无网络依赖） | **零实现**（A02 面） | 口令-载体循环依赖需正面回答；文件权限=唯一强制层 |
| **C2 systemd-creds/TPM2** | Linux+systemd（版本入 U-1） | 服务态解密，绑定机器/TPM | **设计即 headless**；宿主重启自动可用 | 轮换=重加密发布；恢复=**TPM 绑定即不可随备份还原**（同 F10 语义但更强绑定）；迁移他机=需显式重签发 | dev(mac)/CI 不可同构→双实现面（darwin 存续+Linux creds） | 零实现（A02 面） | 双平台双后端=语义一致性维护成本（F12/F13 现状的镜像问题）；绑定形态依赖发行版细节（U-1） |
| **C3 外部 KMS/HSM 接口** | 任何有网络的 Linux/mac；服务身份依赖 | 宿主以服务身份取明文数据密钥 | 与主机重启解耦；**网络=新可用性依赖**（不可用时 fail-closed 可满足 F11，但 RTO 由外部 SLA 决定） | 轮换/撤销=KMS 策略面（最完整，含审计） | 三环境一致可行；CI 需身份供给（现零凭据面冲突点：F12 的 CI 隔离是该候选的反例约束） | 零实现；D-IG-2 的"B 优先"属 O11 锚定密钥域，**不构成对本候选的预决**（§2 末行） | 单宿主离线部署（VISION 本地优先）与云依赖的张力；成本；主机服务身份自身的存储=递归问题（需回答服务凭据存哪） |
（候选集来源=A01 票面枚举+现状基线 C0；审方可加候选或并拆，本件不封闭该集合。）

## §3b 所有者六检查轴 · 逐候选事实矩阵（轴=所有者 2026-11-20 指令原文所列六项；格=事实/由现状 F* 延伸的机制事实，**无判定无评分**）

| 轴 | C0 darwin-keychain（现状基线） | C1 age+口令 | C2 systemd-creds/TPM2 | C3 外部 KMS |
|---|---|---|---|---|
| ①凭据入普通库/日志/事件/调试 | **不入**：spine 零凭据列（F16）；全路径脱敏独立于后端（F18）；agent 面只见 proxy token（F5） | 同左（后端无关的既有层）；**新增面**：解密后明文在宿主内存+密文文件可被备份/同步面卷入（需文件排除策略事实） | 同左；密文可随常规文件面（无口令旁路，解密仅服务态）——**日志面同构** | 同左；**新增面**：取密流量经外部端点（传输层 TLS 事实待定形）；本地仅剩缓存策略问题（是否需要落盘=实现决定） |
| ②跨进程共享/并发 | OS 库承担（F8）；mc/resident/git-helper 多读同 account 为既有形态 | 文件级：并发读安全（immutable 载荷+原子替换）；**写并发**需单写者约定（现状 F8 形态可延用） | 服务态解密：与②最贴合（unit 级隔离）；跨 unit 共享=同一密文各自解（无共享句柄） | 天然多消费者；**并发限额/QoS 由外部 SLA 决定**（可用性面转入轴五） |
| ③轮换衔接 | 条目版本推进+旧版即拒（F7，IG §3-⑥）；钥匙串替换原子性=Write+manifest（F1） | 重加密+rename 原子发布；**新旧并存窗由部署脚本决定**——需明定 supersedes 时序（DC §7.4 模式可套） | 重加密=可携公钥操作，发布同③；TPM 绑定使"旧机旧钥"自然失效（衔接=重签发） | KMS 版本/别名策略原生（轮换最完整面）；旧密钥可保留解密窗（可配置语义，规范文档待取） |
| ④容器重建/宿主重启/备份恢复 | 容器重建=无影响（agent 不持密钥，F5）；宿主重启=**登录会话依赖未证**（F9/U-3）；备份恢复=**密钥不随备份**，重获语义（F10） | 文件随备份走（恢复=口令问题）；口令通道 headless 未定（§3 已列） | 容器重建无影响；宿主重启=原生自动；**重装/换机=TPM 绑定断链**（恢复=重签发流程定形）；备份不含明文亦不含绑定钥（F10 同型更强） | 与本机绑定解耦（恢复最干净：服务身份通即可用）；**但备份策略若含服务身份文件，恢复面转义到该文件**（递归点，§3 已列） |
| ⑤后端不可用 fail-closed | 现状实证（F11） | 口令错/文件缺=解密失败→同 F11 形态；**须防"空口令=放行"实现陷阱**（列 ADR 验收项） | unit 未起/TPM 不可用=解密失败→同 F11 | 网络断/限流=取密失败→F11 形态可满足；**部分可用（慢）形态需超时纪律**（PS/BL 时间语义交叉，事实登记） |
| ⑥dev/CI/prod 一致 | 现状：CI 零凭据解耦已存（F12）；dev=真钥匙串（F13）；**Linux dev 机不可用该后端**（F2） | 三环境同构最容易（纯文件+口令）；CI 可用合成口令夹具=现状 F12 语义延续 | dev(mac)/CI 无 systemd → **双实现面**（与 C0 共存），"同一安全语义"=接口层一致而非载体一致（需在 ADR 明写该定义） | 三环境同服务可行；CI 零凭据原则与服务身份**正面冲突**（F12 vs 取密需要身份——CI 只能桩或专用测试密钥环，此为该候选须回答的一致性事实） |

矩阵读法：格内=载体属性/既有事实的延伸，标 **加粗** 处为该候选引入的**新事实面**（不是缺陷判定）；判定归所有者的比较件。

## §4 UNKNOWN 登记（须由所有者/环境事实填充，事实未到处维持 UNKNOWN）
U-1 目标发行版与 systemd 版本（含 systemd-creds 可用性）｜U-2 目标环境是否已有 KMS/HSM 及形态｜U-3 生产宿主 headless 程度（无 GUI 登录会话时 darwin 不适用已定，影响 C1 口令通道）｜U-4 宿主数量与迁移/重装频率（备份-恢复语义定形）｜U-5 当前 git remote 实际用什么凭据（操作者环境事实，F14 的产品外半面）｜U-6 备份恢复演练目标（DC §7.7 三层证据）是否要求凭据随恢复可用｜U-7 dev/CI/prod 后端是否**必须**同一（F12/F13 给出的是现状不是应然）。

## §5 批准前置清单（本材料完备性的自证边界）
①U-1..U-7 填充（部分可由 owner 直接给出，部分=O3 服务器事实）；②Git 行（F14）的三选一决定；③F4 viewer-token 例外面的处置决定；④若选 C2/C3：双实现面或外部依赖的**残余风险是否需 RR**（Authority 未指定前该路径本身受阻——治理依赖提示，非建议）。本文提交=O1-a 材料交付；**事实基线的"冻结"动作归所有者**。
