# F0B-E03-RESULT-FIELDS · 仓库观察结果全表注册件（E03 READY 前置）

状态：**REGISTERED 字段表（本件）；跨实现向量=declared-pending（B03 同规：家族向量齐备前，本族状态=declared 非 verified）**。用途：E03 的 canonical 摘要比较（docs/facts/f1… A12 矩阵行/DT §6 同谱）需要封闭字段集；本件即该封闭集的规范登记。语义编码规则全部消费 shz-canon-v1（B03/F0b-1，AM v0.1.29 批准件），本件不新设编码语义。

## 1. 记录形态

存储点=`repository_observations.result`（JSON 文本，`repository_observation` 路径 mc/verbs/delivery.go）；生产形态=`json.Marshal(RepositoryResult)`。摘要域=下表**全字段闭集**；重录比较=两侧按 shz-canon-v1 规范化后的事件摘要全等（比较对象是语义值，**不是**序列化字节——原缺陷正是拿字节相等当语义相等）。

## 2. RepositoryResult（顶层）

| 字段 | 类型 | 语义必选性 | 编码注记 |
|---|---|---|---|
| `wiki_update` **或** `task_id` | string / int64 | **二选一必在**（subject 域；dispatch.Subject.Valid()） | wiki_update=小写标识符形；task_id=十进制词法（数值词法存活律） |
| `input_commit` | string | 必在 | Git 提交形（`ValidCommit`：小写 hex 40/64） |
| `destination_commit` | string | 必在 | 同上 |
| `result_commit` | string | 必在 | 同上 |
| `retained_ref` | string | 必在 | refs/mc/… 规范路径形 |
| `temporary_bytes` | int64 | 必在 | 十进制词法 |
| `status` | string | 必在 | 封闭枚举（按操作族；实现票枚举补源=本表 v 修订，E03 时冻结值集） |
| `version_id` | *int64 | 可选 | 缺席/存在二态，无 null 编码（presence tag） |
| `candidate` | *Candidate | 可选 | 见 §3 |
| `applied` | *Applied | 可选 | 见 §5 |
| `evaluation_pending` | bool | 可选（omitempty 形） | **注意**：omitempty 使 false≡缺席——摘要域按"存在且为真"记，false 与缺席同值（声明性事实，非缺陷掩盖） |
| `cleanup_error` | string | 可选 | 操作者可见文本；入摘要前**不截断不改写**（digest 保语义完整性） |
| `operator_action` | string | 可选 | 同上 |

**语义必选性 ≠ Go 标签**（B03 的 ENVELOPE/SCHEMAS 反转教训执行于此列）：`omitempty` 只描述编码缺席，本列按业务合法态标注。

## 3. Candidate（`candidate`）

| 字段 | 类型 | 语义必选性 | 编码注记 |
|---|---|---|---|
| `submitted_commit` / `destination_commit` | string | 必在 | 提交形 |
| `destination_branch` | string | 必在 | 分支名规范形 |
| `commit` | string | 可选（合并成功时存在） | 提交形 |
| `status` | string | 必在 | 封闭枚举 |
| `conflict_paths` | []string | 可选（冲突态存在） | 逐元素路径规范形；有序（出现序=语义） |
| `changes` | *CandidateChanges | 可选 | {candidate: ChangeRecord} |

## 4. ChangeRecord（`changes.candidate`）

`from_commit`,`to_commit`（提交形，必在）；`inventory_path`,`patch_path`（部署内路径，必在，**绝对路径含主机态——摘要域内该两字段视为不透明定位符**，E03 实现若发现其破坏跨记录稳定性，按 wire 变更流程提案例外，不私改）；`inventory_sha256`,`patch_sha256`（小写 hex64，必在）；`patch_bytes`（int64 十进制）；`files`（int 十进制）。

## 5. Applied（`applied`）

`authorization`（标识符形，必在）；`commit`（提交形，必在）；`recovered`（bool，必在语义——false 有信息量：**不得**按 omitempty 缺席处理；本 Go 结构无 omitempty，成立）；`cleanup_error`（可选文本）。

## 6. 注册态与 E03 前置判定

- 本表=封闭字段集注册完成（E03 READY 前置的形式要件满足）。
- **declared-pending**：status 值集枚举、§4 路径定位符的跨记录稳定性例外、家族向量套件（30 例式最小集+边界）——三者随 E03 实施票补齐；在此之前"该家族的跨实现符合性"不得宣称（AM §4b/DC ⑧ 同律）。
- 变更纪律：本表任何字段增删=wire 形状变更（prepare/attest/commit 冻结件相邻域），须设计决定+重绑定。
