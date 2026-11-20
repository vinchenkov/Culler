# G-B-ENVELOPE-DESIGN · agent 构建身份：run.json release 绑定设计件（A13 交付）

状态：**PROPOSED 设计提案（wire 形状改动=设计决定——本件只提案；批准前无实施许可，实施另发工单动作随批准）**。范围：DC §5 G-B"run.json 增版本字段+容器侧比对"；AM §4b G-B 验收=envelope 字段测试+混版 run 拒绝/标注证据。

## 1. 现状事实（E1 直读，dev@4c64eef）

- `RunIdentity.ReleaseBuildID` 字段**已存在于 schema**（mc/verbs/verbs.go:152-160，`omitempty`）。
- **不对称的生产者现状**：collie 信封写入该字段（resident/src/effects.ts:625 `release_build_id: config.releaseBuildId ?? "development"`）；**pipeline（agent）run.json 不写**（effects.ts:1341-1360 构造无此键）→ agent 容器内 `LoadIdentity` 得空值。
- **消费者现状**：仅 operator 面比对（operator_bridge.go:175、verbs/operator_requests.go:83——皆空值跳过的宽松式 `!= ""` 前置）。agent 写路径（runner 经 mc verb）**无任何构建身份比对** → 这正是 DC §5 G-B 命名的缺口（与 DC-FACTS"agent run.json has no release_build_id"行一致）。
- **次生缺陷（本次追链新发现，登记为事实）**：collie 生产者的 `?? "development"` 回退把"配置缺失"伪装成合法 dev 身份——生产部署若 releaseBuildId 未供，信封静默带 `development`，比对面全部失活。fail-open 形态，应一并处置。（同族宽松式参照：evaluation 捕获记录 `config.releaseBuildId ?? ""`，effects.ts:1383——非脊写面，仅作家族注记。）

## 2. 设计（最小面：**不新增字段**，用既有 `release_build_id` 承担身份绑定）

拒绝 `envelope_version` 新键提案：版本语义已被 release 全等吸收（matched-pair 部署中镜像/宿主/信封同源），再加一维=第二权威，无新增可判信息。

### 2.1 生产者（resident effects，pipeline 分支）
- `run.json` 增 `release_build_id: config.releaseBuildId`，**必填**；缺失=effect fail-closed（不 spawn），错误指名修复（重部署/修配置），**无回退值**。
- collie 分支删除 `?? "development"` 回退，统一必填语义（dev 部署的 config 值本就是 `development` 字符串——由配置显式给出，而非由缺失伪装）。

### 2.2 消费者（容器内 mc，agent-scope 入口）
- 新增单一咽喉（与 G-A 挂 OpenSpine 同法）：agent-scope 身份装载点强制
  `validReleaseBuildID(id.ReleaseBuildID) && id.ReleaseBuildID == releaseBuildID(自身)`；
  不符→域级拒绝（先于任何 spine 打开；exit 1；文案含两侧身份）。宿主 scope 不受影响（其身份=进程自身）。
- 语义定位：把"更新窗口重启旧 run 容器混版写"从**静默**变为**显式 blocked run**——与既有混合版本语义（dispatch=摆锤停摆非静默腐化）同族，操作者可见可修。

### 2.3 兼容与失败路径
- matched-pair 部署（宿主二进制/镜像/resident 同 commit）下正常态永不触发拒绝；触发即真混版或信封被篡改（后者本被 ro 挂载+锁域防线覆盖，此为纵深）。
- 恢复面：blocked run 的再启动走既有 launch 协议（IG §5.3），重生成信封=新身份绑定，无历史信封复用。
- 风险声明：旧信封（升级前生成、字段缺失）+新容器镜像组合会在装载点被拒——**这是设计意图**（旧 run 容器不应以新二进制写脊），但会把 update-checkpoint 窗口的一部分行为从"侥幸可用"变"显式停"，须随 F0a 更新演练验证（与 update 停摆机制合拍，不新造窗口）。

## 3. 验收映射（G-B 实施票将含，=AM §4b envelope 测试+混版拒绝/标注）

1. 生产者单测：pipeline/collie 两分支 run.json 必含 release_build_id；config 缺失→effect 失败不 spawn（正反例）。
2. 消费者单测：agent 身份缺字段/错值→拒绝先于 spine 打开（可观测零副作用断言）；正确值→放行（正控）。
3. 漂移守卫：源码级禁 `?? "development"` 回退与 `omitempty` 生产路径复活（与既有源守卫家族同式）。
4. 更新演练：升级窗口旧 run 重启=显式 blocked 非静默（F0a 面，可与 update_docker lane 合流）。

## 4. 边界声明

本件不改动任何生产代码；wire 形状批准、实施授权、F0a 验收判定各自独立。若批准，实施=一个小面积双端改动（TS 生产点 2 处 + Go 装载点 1 处 + 测试），无 schema.sql 触面。
