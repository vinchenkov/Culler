# A12 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: A12-V-001
- target_component: G-F 宿主 mc→helper 构建身份逐入口覆盖（测试出口）——docs/facts/g-f-coverage-matrix.md + `__system-state` ensure 门（mc/cmd/mc/systemstatus.go）+ 绑定测试（cmd/mc/gf_build_identity_a12_test.go、operatorcmd/a12_dump_test.go、verbs/doctor_runtime_a12_test.go）+ f1-operator-fence-001 P2/P3 闭合
- tested_version: shz/TA12@b73eded（基线 shz/dev@b5662b4）
- environment: macOS arm64 fast-lane（`test_fake_routing test_cli_faults` 形态=委托谓词可编程）；无模型 token；docker 往返面不在本件（声明 R3）
- attack_scenario: 混合版本写入——宿主进程与新/旧 helper 二进制跨进程边界组合：argv 入口（通用 verb 自委托、`__system-state`、attachment RPC）无帧级身份字段时是否可静默用旧二进制写脊；帧入口（onboard/transfer/dispatch/doctor/operator）身份分量被替换时是否必拒
- expected_result: 逐入口给出 E（帧全等）/R（ensure 对账）/S（schema 门）至少一层的**运行时或源级钉死**；缺口要么修复要么显式声明残余；不宣称"混合版本已排除"（F0a Exit 专属）
- actual_result: ① 摸底=11 写入口逐文件追链（spawn→身份层→子端），产出矩阵（含 F1 P2 全表 81 条：spine 写 24/读 19/host-local 38；P3 RPC 边界）② 唯一实然缺口=`systemSpineCall` 委托分支无 ensure——修复=ensure 门+seam var（复用既有机制，**未动任何 wire 形状**，R4 声明）③ 新测试：门拒绝（错误注入+到达断言）/源守卫×2（spawn 前必有 reconcile）/RPC 四边界（version-mismatch、commit/rollback 无 BEGIN、begin 杂料）/DoctorRuntime 五分量逐项拒绝+匹配放行 ④ **全道 `./mc/check.sh` EXIT=0：0 FAIL、0 UNVERIFIED（macOS 全 skip 集为空）、26 包 ok；cmd/mc=444.1s、verbs=514.2s、operatorcmd=0.04s；日志 /tmp/a12-full-lane.log 归档**
- evidence_ref: docs/facts/g-f-coverage-matrix.md（入口表+残余 R1-R4）+ docs/facts/f1-operator-fence-001.md §5-P2/P3 + 上述测试文件
- evidence_digest: 扫描门三包根——operatorcmd findings 0（seal 8093fa40…）；verbs 11 条全部落未触碰上游文件（dispatchverb/ops/seedingprojection/sourcegit/taskrevision，seal a052f71b…）；cmd/mc 38 条中唯一落改动文件项=systemstatus.go:83，git blame=9dbc043 上游行（本次仅位移未改写；argv 数组形态=已知 FP 家族第 3 实例）；两新测试文件+verbs 新测试 0 命中
- test_script_digest: gf_build_identity_a12_test.go / a12_dump_test.go / doctor_runtime_a12_test.go @b73eded（git 锚）
- execution_id: A12-run-01
- verdict: PASS（作用域=机制存在性+测试钉死+缺口修复；**G-F 义务完成判定与"混合版本风险已排除"声明均不属本件**——F0a Exit+DC §6 复测包络待记，残余 R1-R4 归验收 weigh；operatorcmd 测试文件名 a12_dump_test.go 系工具钩子拦截重命名所致，内容与职责以文首注释为准，登记为外观瑕疵）
- reviewed_by: host-agent（兼任声明；非独立验收、非 RR、非 Integrity 通过）
- verified_at: 2026-11-20 (Asia/Shanghai)
