# V-4 Collie 附件渲染路径读源事实卡 #1（2026-11-21，E1 grep/读文件行锚；行为半边分级见 §3）

## 1. 渲染链（附件引用 → 浏览器），逐段行锚（dashboard/src + mc/cmd/mc）
| 段 | 位置 | 防御事实（读源） |
|---|---|---|
| 引用形态 | ui/colliefmt.js `attachmentView`（:270–294） | 类型化声明**全字段严验**（sha 64hex/direction 枚举/整数 size/字符串 session）；**缺任一=降级 inert 文本，零 URL**；旧串形同 inert |
| URL 构造 | 同函数 :291 | 仅由已验字段拼成 + `encodeURIComponent`；"nothing is derived"（注释逐字）——无路径注入面 |
| 内联类型面 | INLINE_MEDIA_TYPES（:257） | **raster-only 白名单，按裸小写 type 取键**（参数不能买内联）；**image/svg+xml 刻意缺席**（可脚本化）——注释点名该设计 |
| 字节来源权威 | api.ts:466 | 服务**不自己开文件**：`mc __attachment-bytes <session> <dir> <sha> <size>` 为唯一字节出口（spine 行身份对账+发布文件核验，动词=权威；refusals→HTTP 映射 api.ts:307） |
| 文件句柄时序 | hostfile.ts:2 | 动词验后**即时**复核该文件（TOCTOU 意识在码） |
| 消息正文/HTML 面 | ui/app.js 全文件 | `innerHTML` **仅 1 处**（:2327，输入=自建的报告文档树，非附件/非模型文本）；文本面 73 处 `textContent/createTextNode`——无 markdown→HTML 活性渲染路在 app 表面 |
## 2. 既有测试面（Implemented+Tested，单元级）
`colliefmt.test.ts` / `api.test.ts` 在册（含白名单/inert 判定/映射用例）——**单元级已有；本卡不代读其全绿史**，全绿以 dashboard 道（983/0 于 A16 轮实测）为近证。
## 3. 行为半边（浏览器真会话注入哨兵）——分级判定
- 本卡=**机制存在+代码级不可活性**（结构性：文本面 textContent、图片面 raster+来源端不解释内容、URL 严验）。
- **端到端可达性实测（活 Collie 会话+真附件+Chromium）=UNKNOWN，随持久段（A25 后）**：dashboard/smoke 家族有真 Chromium 基建，附件注入哨兵用例=持久段的 smoke 增量（届时按 diff-plan-002 增补段申请文件族）。
- 越层声明禁止：本卡不构成"渲染注入已不可达"的 PASS——它是**读源级强证据+行为级待验**的两段如实登记。
