# A18+A21 联合验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A18-A21-V-001（联合）
- target_component: spine ACTIVE/SNAPSHOT 读取拆分（A18）+ 生产卷布局容器实证（A21）
- tested_version: shz/TA21（merge TA18@68f37dd+修正提交）→ 同批合并 dev@4ff8ff2/e34f544
- environment: MacBookPro darwin 24.6.0 arm64；**Docker Desktop daemon 29.8.0（本会话拉起——此前 MCP 断连≠daemon 缺失）**；go 1.25.12；容器镜像 alpine:latest；被测 mc 为分支交叉构建 GOOS=linux GOARCH=arm64 CGO_ENABLED=0
- attack_scenario: `go test -tags docker_boundary -run TestA21AgentUserSpineReadsOnProductionLayout`：
  ① 宿主 substrate 装配 spine+seed activity+关闭 writer（checkpoint 清 sidecar，断言 -wal/-shm 不存在）
  ② 正控（生产布局）：卷内 chown 10002 + dir 0700 + db 0600（复刻 helper 实产权限）→ **uid 10002 容器经 rw 共享卷 `mc activity list` 成功**，读到 seed 行 → 证 C1 ACTIVE 在生产布局可用
  ③ 最小权限反证：读后卷内 db 文件 sha256=宿主原值 且 stat=0600 → 容器读未触碰 db 本体
  ④ 负控（假设 :ro 布局）：同脊只读挂载 → `activity list` **诚实失败**且错误命名 driver read-only/cantopen 条件 → 证 ACTIVE 不再静默退化 immutable（C1 核心不变量，真实 Docker 环境实证非 chmod 模拟）
- expected_result: 四步全过
- actual_result: 全过（首跑一处**我方断言 bug**：sha256sum 输出含路径列被我当模式——修解析非改语义，重跑绿）。联合分支 `mc/check.sh` exit 0/0 FAIL/0 UNVERIFIED；substrate 目标根扫描 0 findings（seal sha256:2e8f98c8…）
- 关键澄清（窗口定性修正）：A18 讨论期"移除回退→容器读回归"的担忧经实测**限定为仅假设性 :ro 挂载布局**；现行生产=agent/helper 共享 **rw** spine 卷（effects.ts:725、helper_manager.go:230/373、uid 一致 10002）不触发窗口。"A18 全面通过"仍不宣称——e2e 全链路（真实 dispatch 起容器 + 并发写者 WAL 可见性跨容器）属 F0a 面，待 A03/A04。
- evidence_ref: evidence/A21/mc-check-joint.log；本文；docker 探针为可复现测试（提交内锚定）
- execution_id: A18-A21-run-01
- verdict: PASS（作用域=本提交+本 Docker 环境+alpine 探针容器；非 e2e、非 Linux 原生宿主）
- reviewed_by: host-agent（执行兼任复核，显式声明；Integrity Reviewer 未做）
- verified_at: 2026-11-20 00:40 (+0800)
- 边界: A18/A21 工单 EVIDENCED ≠ 相关契约义务 IMPLEMENTATION_VERIFIED；AM v0.1.30 已按现有枚举记录合并事实。
