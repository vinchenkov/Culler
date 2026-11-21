# A16 diff 计划登记 #2（附三正清单增补：dashboard 编辑面；先列后改）

背景：diff-plan-001 正清单把参数表键面列在 `mc/config` + `mc/verbs/configsave.go` + `config.example.toml`。实现时发现 `container.trust_tier` 是**新字符串型旋钮**（"tier" kind），dashboard 的 `knobNode` 对未知 kind 走数字输入兜底——直接发布会让操作者面渲染错误。AGENTS 既定纪律：操作者永不跑 mc，配置面=dashboard；档位选择若只能编辑 TOML 文件，就制造了"执行侧才摸得到"的资源门，违背 D-5 裁定的操作者属性。

## 增补允许文件族（本登记生效后为准）
- `dashboard/src/ui/app.js`：`knobNode` 新增 `tier` 分支=受限 `select`（options 由 config show 下发 {t0,t1,t2}，前端不发散取值面；保存体仍经后端 `applyConfigChanges`+`Validate` 复验，前端选择器只是输入辅助，不是授权面）。
- `dashboard/src/ui/configs.js`：如透传 `options` 已覆盖则**零改动**（现状已透传）；仅当测试暴露缺口才动，动则限于 options 透传。
- `dashboard/src/ui/configs.test.ts` +（如存在的 app 渲染测试面）：新增断言——tier 旋钮渲染为 select、三个选项、值回写正确；其余旋钮渲染不回归。
- 不新增模块文件：`UI_MODULES`/`host_release.go` 清单**无需变化**（app.js/configs.js 已在册）。

## 拒绝的替代
- 把 tier 藏进 TOML（无 dashboard 编辑面）：拒绝——资源门变成执行侧专属。
- 用 integer 0/1/2 伪装枚举：拒绝——操作者面说谎。
- 本票不做 dashboard 而开新票：拒绝——半张表没有意义，渲染错误即发布事故。

## 与政策不变量的关系
本增补不触碰 dispatch wire、substrate SQL、supervision 渲染器；dashboard 改动为纯输入辅助，后端校验为唯一权威（与 AGENTS"前端不能以隐藏按钮代替后端授权"一致：select 不限制后端可保存值的合法性来源，来源仍是 Validate）。

登记轮：AM v0.1.59（同轮 blcheck 与双面哈希照章）。
