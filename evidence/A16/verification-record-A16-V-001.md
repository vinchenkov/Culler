# A16 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A16-V-001（CODED 证据轮；EVIDENCED=真机资源耗尽矩阵轮，待所有者点名）
- target_component: 沙箱资源强制全链——fd 上限与 docker 日志配额（全档）、trust tier 选择与 T1/T2 参数表（t2 缺表=保存与 spawn 双端拒）、tmpfs size、run 树字节预算、工作区看门狗（stop+sweep.resource 留痕，终止权威仍在 dispatch）、collie-envelope 摘要面扩展（ulimits/log_config/nano_cpus 补证）、dashboard/配置域/嵌入示例、宿主变更工单草案（D-1(a) 备用）。
- tested_version: shz/TA16 链 8ad63bf→（rebase 后）f7c8ce1 配置域→7d1c7e9 spawn/envelope→b5d152b 字节预算→471aa6c 看门狗/事件入册→48b9142 白名单→008d723 冒烟重钉→（本记录提交）；合并态以 AM v0.1.60 同轮 dev 提交锚定。
- environment: darwin 24.6.0 arm64（mise go 1.25.12 / bun 1.3.12/1.3.14 族）；无真机 docker 轮（矩阵=后续所有者点名轮，见残余①②）。
- attack_scenario（执行清单与实测退出码）:
  1. `./mc/check.sh`：**exit 0，全绿零 FAIL**（gofmt/vet 全 tag+linux arm64、shell 族、全 Go 测试含 verbs 595s）。**过程如实登记**：首轮 FAIL×2——(a) 事件注册表守卫抓出 `sweep.resource` 未入册（先登记 diff-plan-003 增补段再入 runner/shared/log.ts，非绕行）；(b) 跟踪/嵌入 config.example 一致性守卫抓出我未同步 `mc/config/config.example.toml`（复制同步后绿）。两个守卫均首轮生效=守护面有效的又一实证。
  2. `resident/check.sh`：**exit 0，772 pass / 0 fail**。含新增：limits 解析矩阵（t0 零不限语义保留、t1 全表解析、**t2 缺表拒解析=D-5 双端强制之宿主端**）、spawn argv 钉（fd/log 全档出现、t0 无 cpus/memory=零回归）、envelope **5 条新负控**（ulimits 删/宽、nano cpus 漂、log size 宽、driver 换，逐名拒绝）、字节预算臂 4 例（预算下最旧先剪、测量失败不动作、计数臂抵扣、t0 无读）、看门狗 5 例（t0 不看、超软限 stop+留痕、恰等/只读/他档不动、setup 在作用域、清单失败跳过）。
  3. `dashboard/check.sh`：**exit 0（983 pass）**；`runner/image/check.sh`、`release/check.sh` exit 0（A04 面回归未破）。
  4. `./dashboard/smoke.sh`（app.js/configs.js 变更义务）：修复后**完整单遍 313 pass / 0 fail / exit 0**（configs 子集另跑 12/0）。**过程如实登记**：configs 冒烟门抓出真实缺口——EDITOR_TYPES 白名单未含 "tier" ⇒ 旋钮被滤（计数 26/27）并链式带崩 stale-save/discard 两例；同轮我误把期望计为 28（自数旋钮错），以服务端真值 27 重钉+tier 选择器存在断言。教训与 A04 同型：**UI 白名单族增改必随 smoke 单文件复跑**。
  5. blcheck：tickets=62 violations=0 warnings=2（沿册）。
  6. 扫描门（deep，focus=本票 11 文件）：seal `sha256:a4377d9da5bcddc40263e7a515bc2875c8c19be2ff060a804103cbedd7fe89c6`，551 findings（与 A04 基线同量），依赖面 3 包 10 建议（同态）。**程序化核对：落在 A16 改动行上的 finding=0 条**；173 条在目标文件但全部位于未触动的上游行（app.js 渲染族、config.go 早期行——blame=上游既有面，非本票引入）。coverage=partial / runStatus=inconclusive（工具既有登记缺陷）；**静态边界证据，不宣称项目安全**。
- expected_result: 裁定值表全机制化且 fail-closed：未配置不可信档=不跑；每一强制标志可被 envelope 见证；t0/darwin 零回归；run 树与日志面在预算内并有留痕。
- actual_result: 达成（本机可证范围内）。三处如实标注：①docker 侧真施加（--ulimit/log-opt/tmpfs size/NanoCpus inspect 实况）仅单测+夹具形为证，真实 daemon 行为=矩阵轮；②看门狗终止后 run 的 spine 终态走既有 container-gone/lease 通道（spine 内专因"资源"词**未造**——wire 形状决定呈所有者）；③t2 语义在保存（mc Validate）与解析（resident positive() 拒零）双端强制，但生产 spawn 全链 t2 实跑=矩阵轮。
- evidence_ref: evidence/A16/（diff-plan-001/002/003、d1-d5-decision-dossier.md、HOST-CHANGE-ORDER-DRAFT.md、本记录、blcheck-a16.ndjson）
- supersedes_verification_id: 无
- reviewed_by: host-agent（执行兼任复核，显式声明；Integrity Reviewer 未做，不宣称）
- verified_at: 2026-11-21 (+0800)
- 边界: CODED/EVIDENCED 皆执行事实；F0a 资源矩阵（TM:177 四维+spine 零污染+X-1）与旧 plan digest 失效面（envelope 版本化：在飞 collie plan 与新 argv digest 不匹配→wake 拒，属既定 fail-closed 语义，运维影响=一次 resident 重启自然排空）**均待真机轮**；A14 执行、A19、V-6 完整六例的前置由此链出。
