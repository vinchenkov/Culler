# A16 diff 计划登记 #4（**撤回自我更正版**：opencode 种卷能力产品已有；004 初稿前提错误，如实作废）

初稿（本文件前一提交）提议给 `runner/agent-runner` 加 postinstall 安装步 + 新 `opencode-seed.ts` 以"补种卷能力"。**该前提被追链证伪**：产品有完整种卷链——
- `mc/deployment/opencode.go:324 InstallOpenCodeArchive`（校验归档→建卷→以 `openCodeInstallScript` 字面脚本在安装容器内解包/单程序计数/版本核对 2.2.7=opencodecatalog/catalog.json→写 release-id/archive-sha256）；
- 调用方：`opencode_source.go:388`（源构建路）与 `opencode.go:156/:188`（更新路）；卷名 `mc-opencode-<ns>`（openCodeVolume，A04 平台化后各宿主自洽）。

⇒ **A16 零生产码改动**；004 初稿的 postinstall 方案与既有权威并行=违"不造第二权威"，撤回并入册为纪律样本（**写计划前先追链到调用方**——本票第二次由事实回收自己越界的提案，前例=兼容探针越界）。

## V-1a opencode 轮的正确形状（证据件活动，非生产码）
探针脚本在定向机复刻产品安装容器的**同字面脚本**（从源文照抄，非再发明）种一次性卷 `a14-oc-probe`，然后以生产 XDG 形状（适配器：XDG_{DATA,CONFIG,CACHE,STATE}_HOME 全指每-run 目录，opencode.ts:58）跑发现行；trap 删卷。复刻仅限"装出产品同款卷内容"以测 **CLI 配置发现**；卷生命周期语义（谁种何卷何时）归产品更新路，V-1a 不判。
形状=下一提交 a14-v1a-opencode.sh。
