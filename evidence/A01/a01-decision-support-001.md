# A01 决策支持件 #1：把八件形状裁翻译成 A02 具体代码代价（零裁决；本轮新增亲验事实使形状问题大幅收敛）

供所有者一次批量裁 A01。方法=只读亲验（file:line 全部本代理本轮实开文件核对）；结论性判断**零代裁**。
**⚑ 本轮最重要新事实（比工作表更进一步）**：**载体抽象缝已经存在且干净**——
- `hostSecretStore` 接口=3 方法 Read/Write/Delete（host_secret.go:17-21 [E1 亲验]）；
- **后端由 `platformHostSecretStore()` 单点选型**（:23），darwin 版=host_secret_darwin.go:33、"other"版=host_secret_other.go:7（全 Err 桩）——**Linux 后端=新增一个 `host_secret_linux.go` 返回新 store，通用层零改**；
- 写后读回+摘要校验语义在通用路径（:61-66），**载体无关**；引用元数据（reference 文件）已存 MC_HOME 内、载体无关（:54/:95-96）；darwin 分片细节经 `DeleteParts` **可选接口**隔离（:91-93）——非分片后端自动走 :99 单删路径，无需任何适配；
- 可用性谓词 `HostCredentialCommandsAvailable()`（platform:7）消费面=**仅 3 处**（agentprofiles.go:37 CLI 闸 / fixture:121 / 生产定义本身）；
- 失败文案=**单点常数** `ErrHostCredentialUnavailable`（:15，"Unlock the login Keychain" Mac 专属语）+ agentprofiles.go:38 一句——文案中性化面=2 行。
⇒ **A01 八件的真正分歧不在"代码大小"（各候选的 A02 码面几乎同形：一个新 store 文件 + 选型行 + 文案 2 行 + 迁移/测试），而在三件事：①解锁模型（人在场/开机自解/网络）②恢复叙事（口令失/TPM 复位/服务断）③"同后端"定义与 CI 零凭据的相容**。下面的矩阵把三件事按候选摊开。

## 1. 候选 × A02 代码面（各候选具体长什么样）
| 候选 | 新文件 | store 实现要点 | 选型行改法 | 独有连带面 |
|---|---|---|---|---|
| **C1 age+口令** | host_secret_linux.go（可扩 darwin 共存或替换，见件 1） | age 解密封装（子进程或库）；口令从 env/交互取 | platformHostSecretStore() 加 linux 分支 | **口令通道=唯一未闭合递归点**（服务环境无口令⇒开机需人？systemd 问密码=与 headless 冲突；答"秘密之秘密"=落 keyfile 则回到 C2a 同构问题） |
| **C2a systemd-creds + 宿主密钥** | 同上 | `systemd-creds encrypt/decrypt` 或 FileKernelModule；保护密钥=宿主侧（/etc/machine-id 派生 or TPM） | 同上 | 密钥自身存放递归（=件 2）；249 能力=文档主张未实测（218 一测可定，候令） |
| **C2b creds + TPM2 绑定** | 同上 + tpm 探测 | 绑定 PCR；绑定失败=明确降级拒 | 同上 | **重签发流程必须本轮定形**（件 3；TPM 复位/换机=断链，U-6"恢复=重授权"天然满足——重授权路径=向导凭据步现成） |
| **C3 KMS** | 同上 + 出口策略 | 网络取数据密钥 | 同上 | **三冲突全在**（PS §2.7 未写宿主侧新 egress=治理缺口；CI 服务身份=凭据正面违 AGENTS:134 零凭据不变量；本地优先张力）；U-2 设施有无=UNKNOWN |
| （参照）C0 维持 | — | — | — | Linux 永不可用=D3 死锁，仅理论项 |

**共同部分（选谁都一样，可直接预置进 A02 diff-plan 的不变段）**：文案 2 行中性化（:15+agentprofiles:38，操作者文本义务触发对抗审查）；platform:7 谓词改"派生自后端可用性"（一行）；CLI 动词集 credential-read/renew/proxy-tls/credential-migrate/connection-delete 在 Linux 的放行=谓词自然结果零额外面；迁移工具（件仅 C1 替换 darwin 时需要——**"抽象层读"保留 darwin⇒无存量迁移**）。

## 2. 八件形状 × 快速代答（每件给"若选 X 则代码面=Y"的机械后果）
1. **"同后端"定义**：**抽象层读**⇒C2a/C2b 活（darwin 留 C0、Linux 用 creds，四层一致仅载体层分 OS——O1-b 表 F:72 所指现状抽象即支持此读）；**载体层读**⇒C2 系死，只剩 C1（全 OS age）或 C3，且 **darwin 存量迁移=必做**（A02 面积×2）。倾向注（非裁）：载体层读会迫使 Mac 也重做，逆"不重写已验证机制"护栏（MASTER §9-3）。
2. 秘密之秘密落点：C1⇒口令载体必答（headless 下唯一免人方案=另一 keyfile，同构回 C2a）；C2a⇒宿主密钥=/etc/ 机器级文件（递归到此闭合于文件权限+root，可接受面）；C2b⇒TPM（递归闭合于硬件）。
3. 重签发定形：C2b⇒本轮必须在 A01 写"TPM 复位/换机=重走凭据步"一句（向导已有该步=文档化非新码）；否则 C2b 挂"恢复未定形"。
4. C3 收/拒：收⇒先补 PS §2.7 宿主侧 egress 条款 + CI 测试密钥环设计（新面）；拒⇒表 §1 该行划线即可。
5. Git 行三对象：与后端选型**正交**（无论选谁都要显式覆盖 IG:47/F14b），可模板化一句。
6. viewer 明文 token：同上正交，纳管/例外二选一写一句。
7. 备份密钥环归 ADR-0 或 A06：U-6=否已定"备份不含凭据"⇒**建议归 A06**（ADR-0 免扩面）——但归谁不阻塞 A02。
8. 文案范围表：上表"共同部分"+A25 diff-plan §5 四语（深水区随 A02 一并收=审方缓改清单已在册）。

## 3. 各运行域现在能证什么（诚实的验证地图）
- **Mac 本机**：C1 全可测；C2 系=编译+接口+桩语义可测（systemd-creds 本机不存在），**真机证不了**；
- **CI runner（Linux，需件②Actions 启用）**：C2a/C2b 的**容器内**语义可测（runner 是 systemd 环境，但注意 creds 在容器/runner 的可用性=新 UNKNOWN）；C1 全可测；
- **218（真 Linux 宿主）**：C2a/C2b **唯一真实证域**（machine-id/TPM 在场性一测便知；TPM 有无=件 3 生死，U-9 可就地终结）；属生产宿主操作，候您令；
- **探针建议**（若想在裁前把 UNKNOWN 打掉）：218 上跑三行只读探针（`systemd-creds --version`、`ls /dev/tpm*`、`cat /etc/machine-id` 存在性）——把 C2a/C2b 从"文档主张"升为"现场事实"，10 分钟内完成。候令。

## 4. 一句话式批量答题模板（可直接改写勾选）
`件1=抽象层；件2=C2a 用机器密钥闭合，件3=本轮写死"TPM 复位=重走凭据步"；件4=C3 拒（CI 冲突+治理缺口，U-2 未知不赌）；件5=覆盖句模板随 ADR 起草呈；件6=纳管；件7=归 A06；件8=按 §1 共同部分+A25 §5 清单；探针=准/不准（218）`
（以上每项都是**您的裁**，模板只为一次答完；空白件=维持候裁，A02 仍不启动。）
