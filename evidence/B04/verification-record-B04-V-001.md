# B04 验证记录（TM §7.1 · 执行事实，不构成义务级判定）

- verification_id: B04-V-001
- target_component: F0b-2 根算法设计件——docs/design/f0b-2-root-algorithm.md + vectors/{root_vectors.json, reference_root_encoder.py}
- tested_version: shz/dev@531a718
- environment: python3（stdlib, -I）；无网络
- attack_scenario: 推导器双跑逐字节同摘要 `9d1c3e9ccd1304f0d55da47857185fe775511d59ee3d6a303a490e87a582f0a5`；11 例内建关系断言（乱序=定序、NFC 双形=同根且**原始输入确不同**——首轮发现并修复了字面量被编辑器归一化造成的假阳性对，修正后 drift 警告实证套件换代）；重复 id/短摘要=显式错误
- expected_result: 确定性+关系全过；语义不越 DC（只冻"实际算法"，输入集合/排序/空集语义为引用非新设）
- actual_result: 达成。一处设计裁决记录了理由（Merkle over fold：D07 包含证明需求）；一处已知边界写明（单叶根=叶本身）
- evidence_ref: evidence/B04/verification-record-B04-V-001.md + 扫描记录（docs/design 目标根，见 dev evidence）
- evidence_digest: root_vectors.json=`9d1c3e9c…`（内容摘要，重跑复现）
- test_script_digest: reference_root_encoder.py @531a718（git 锚）
- execution_id: B04-run-01
- verdict: PASS（作用域=本提交；跨实现符合性=B07 对 vectors 跑通时才成立，本件证"定义确定"非"已被多实现同意"——AM §4b/DC ⑧ 同律）
- reviewed_by: host-agent（兼任声明；F0b-2 的批准动作待所有者/Authority，本记录不自我批准）
- verified_at: 2026-11-20 01:00 (+0800)
