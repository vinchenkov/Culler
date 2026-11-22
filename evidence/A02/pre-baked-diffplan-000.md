# A02 预铸 diff-plan #0（候 A01 裁决即执行；本件**零裁决、零码改动**——只把"答案落地后当天可开工"准备到位）

状态=PRE-BAKED（授权=轮十六批准 W2 含 A02 票本身，但 A01 未裁⇒**A02 不得启动**；本文件为裁后执行序）。
基线事实（本轮全部亲验）：
- 载体缝：`hostSecretStore` 接口=Read/Write/Delete 三法（host_secret.go:17-21）；选型注入点=单行 `platformHostSecretStore()`（:23）。
- **build 标签形态（本轮新亲验，决定改动面）**：darwin 版=`//go:build darwin`；**桩版=`//go:build !darwin`（⇒Linux 今天吃桩=全部 Err）**；fixture=`//go:build test_fake_routing`（**CI/测试道已走独立 fixture⇒U-7"CI 零凭据"结构上已成立，A02 无需新机制**）；platform 谓词=`//go:build !test_fake_routing`；lifecycle 平台无关。
- ⇒ **A02 接线不变段（各候选相同，可预批）**：①新增 `mc/deployment/host_secret_linux.go`（`//go:build linux`，实现三法）；②桩文件标签 `!darwin`→`!darwin && !linux`（**一行**）；③`host_secret_platform.go:7` 谓词由 GOOS 硬编码改为"派生自后端可用性"（linux 返 true；darwin 维持；**一行级**）；④失败文案两行中性化：`host_secret.go:15` 常数（去"login Keychain"专属语）+`agentprofiles.go:38`（**操作者文本义务⇒80/40 有/无色对抗审查到 accept**）；⑤消费面零改（provider_credential.go:165/333、provider_tls.go:55、connection_delete.go:168 全走接口+谓词）；⑥迁移工具：按 U-6=否（备份不含凭据、恢复=重授权）**Linux 无存量迁移**——票行"迁移工具"读为"Linux→Linux 格式演化预留"，裁时确认此读法（ADR 件 7 相关）。
- 校验语义自动继承：写后读回+摘要比对在通用路径（:61-66），任何新后端免费获得；`DeleteParts` 可选接口（:91-93）⇒非分片后端零适配。

## 候选分支（A01 件 1-4 的答案决定走哪条；每条=仅"新后端文件内部+独有连带"不同）
### 支 C1（age+口令）
- 后端=age 加密封装；密钥来源=**必答件 2**（口令 env/交互/挂载文件——headless 下第三选=变相 C2a）。
- 独有连带：口令通道治理句入 ADR；darwin 是否替换=件 1（载体层读⇒替换+mac 迁移，面积×2）。
### 支 C2a（systemd-creds + 机器密钥）
- 后端=`systemd-creds` 子进程或 machine-id 派生密钥封装；**无人在场解锁**=D3 契合。
- 独有连带：件 2 闭合于"/etc 机器级密钥+root 权限"（递归到此为文件系统信任，与 darwin keychain 同层不自洽问题——ADR 承认即可）；249 能力实测=**218 探针候令**（三行只读）。
### 支 C2b（C2a + TPM2 绑定）
- 独有连带：件 3 必答（复位/换机=重走凭据步——向导该步现成，文档化非新码）；TPM 不在场=探测后**明确降级 C2a 或拒**（ADR 一句）。
### 支 C3（KMS）
- 独有连带：PS §2.7 宿主侧 egress 新条款（**契约修订=Authority 行为**）+CI 测试密钥环设计；执行侧注=正面冲突多、建议拒，裁在您。

## 裁后执行序（收到 A01 答案的同一轮即开，无需再请示细节）
1. 支选定→diff-plan-001 正稿（把"不变段①-⑥+支内清单"实例化，**先列后改**义务即满足）。
2. 码：新后端文件+标签行+谓词行+文案两行；单测（接口形+写后读回判别性+植入错误负控）；`host_secret_linux_test.go`。
3. 道：mc 全道+resident+dashboard（文案触及面）+boundary（Linux 引擎侧若可用）+**扫描门按分域纪律先核覆盖集**；218 端到端=生产宿主操作**逐次候令**（不随 W2 批准自动延伸）。
4. 连带转绿链：A02 EVIDENCED ⇒ A09（改名波）/A06（备份）/C01（registry）开跑；**A25 完成+218 探针终结 U-9** ⇒ A24/A14 活会话格/A19 解闸；发布层门（A02+A25b+件 4 文案收口）满足后候 D5 式独立推送令。
5. 若 A01 件 1=抽象层读：**本 plan 的 ①-⑥ 全量适用、darwin 零触碰**——与"不重写已验证机制"护栏（MASTER §9-3）一致。

## 本件刻意不做
不预写 ADR 正文（八件答案未落）；不选支（零代裁）；不碰 218（候令）；不推 main（D5 一次性授权已用尽，新推送需新令）。
