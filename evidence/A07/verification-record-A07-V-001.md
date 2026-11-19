# A07 验证记录（TM §7.1 · 执行事实）

- verification_id: A07-V-001
- target_component: install.sh 摘要校验守卫的自测否定例（install_test.sh 新增 4 摘要格式例+1 尺寸例）
- tested_version: shz/TA07@2c47b626288becd4364d78621f1ef20f6c5a3d56（merge shz/dev=Already up to date，测试即跑于该提交）
- environment: MacBookPro darwin 24.6.0 arm64；shell 套件自带 stub 夹具（无网络/无 Docker）
- attack_scenario: `bash -c './install_test.sh'`（全量含新块）——record.installer.sha256=大写 64/小写 63/非十六进制 64 → 断言"下载后执行前"以 'checksum is invalid' 拒绝且 $TMP/args 不存在（未执行任何下载代码）；合法摘要+record 尺寸+1 → 'failed verification' 拒绝（尺寸断言非死代码）
- expected_result: 五例全部按守卫专属消息 fail-closed，既有 45+ 用例零回归
- actual_result: run1（提交前）exit 0；run2（合并态提交）exit 0；断言 grep 守卫独有字符串，证明路径触达 235-236 行而非泛化失败
- evidence_ref: evidence/{A07-suite-run1.txt, A07-suite-run2-postmerge.txt, verification-record-A07-V-001.md}
- evidence_digest: run1 与 run2 文件内容摘要=提交 2c47b62 内可重算（套件确定性输出）
- test_script_digest: install_test.sh@2c47b62（git 锚定）
- execution_id: A07-run-01
- supersedes_verification_id: 无
- verdict: PASS（作用域=本提交+本环境；A09 迁移改动 install.sh 后此套件为其合并前置）
- reviewed_by: host-agent（执行兼任复核，显式声明；Integrity Reviewer 未做）
- verified_at: 2026-11-19 21:04 (+0800)
- 范围注记（诚实收窄）：立项时认定两缺口——②恢复候选篡改例经预读证实**已存在**（install_test.sh:419-423），撤回；本单交付=缺口①（格式守卫自测）。TM B9-T "验证器自测[F0a]" 的其余面（digest 唯一性/不可变输入链 [固化] 项）不在本单范围，链建成（B07）后复验。工单 EVIDENCED ≠ 义务完成。
