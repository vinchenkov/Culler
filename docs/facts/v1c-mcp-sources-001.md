# V-1c MCP 注册来源读源卡 #1（2026-11-21，E1 grep 行锚；行为半边分级同 V-4 模式）

**命题（TM 配置/hooks 行）**：不可信仓库不能自行注册带平台权限的 MCP。

| 家族 | 注册向量（代码实证） | 仓库面能否注入 |
|---|---|---|
| claude | `adapters/claude.ts:122` `mcpServers` 对象由 **adapter 构造**（仅 openwiki 平台服务器；输入=平台路径+commit 参数）| 仓库配置面=~/.claude 被 CONFIG_DIR 迁移切断（V-1a **C1 实测**）+ settingSources:["user"]（注释点名 project 源危险）⇒ **无仓库可达 MCP 注册路（读源+迁移实测双据）** |
| codex | `adapters/codex.ts:285` 仅**观察**事件流；无注入参 | 其 MCP 载体会是 `.codex/config.toml`——HOME 活源=per-run tmpfs 掩码（V-1a **S2 硬断言实测**+V-2 清除），cwd 源不存在（V-1a R.cwd=不读实测）⇒ **无仓库可达路** |
| opencode | `adapters/opencode.ts:45` `mcp.servers` 在**平台生成的 config 文档**内（openwiki local，codemode:false）| 项目 `.opencode/` 原形可读（O3），但 **DISABLE_PROJECT_CONFIG 旗标实测挡效（O3b）**⇒ 仓库文档不进合并链 |
**合读**：三族的 MCP 注册面=平台单源构造；仓库注入所需的两条载体路（HOME 族/项目族）都被 **V-1a 已实测的行为**闭合——**"带平台权限的 MCP 只能来自审核声明（B7）"在装配级有行为证据支撑**（openwiki 本身=平台声明的服务）。
**行为半边门控**：真 T2 会话中让模型尝试运行时注册（如 claude 的 `/mcp`、会话期工具）=活会话面，随 A25/A02 后的 V-1b 同轮（登记于 diff-plan-002 的活会话段）。**本卡不越为"会话期也不可注册"的声明。**
