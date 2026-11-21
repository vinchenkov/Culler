# A24 diff 计划增补 #2（机制事实注记 + 三域剧本设计；批准前仍零生产码）

## 追补机制事实（E1 读源，纠正 diff-plan-001 的一处隐含简化）
001 说"批准后可进码=单函数谓词"——**谓词面成立，但行为链需在批准包中明示**，防误读为"翻门即全绿"：
- `shouldDelegateToHelper()`（runtime_scope_prod.go:12）同样 `GOOS=="darwin"`：darwin 宿主 broker→warm helper；**linux 宿主 broker→`runLocal`**（main.go:401），其 spine 打开=**`MC_SPINE` 直开模式**（main.go:11：MC_SPINE 未设则由 MC_HOME 推导/拒绝命名文件；设了=命名的可直达峰文件）。
- ⇒ A24 放宽**不需要也不得**顺手动委托谓词（那是部署拓扑决定，归 A25/F0a 形态）；linux 宿主放行后，动词在"有可达峰 spine"时真工作、无部署时给**业务级错误**——三域矩阵的宿主域断言因此定为：**输出不得含门拒 token**（"Release controls require the Culler host." / "requires a Mac." 族），而非要求 exit 0。
- 此事实同时收窄风险面：放宽域=宿主 shell 的读视野 + 其 `MC_SPINE` 所指文件（宿主属主权限），容器域返回值逐字不变（001 论证维持）。

## 三域剧本（a24-vision-acceptance.sh）设计钉（批准轮实装）
参数 `A24_STATE=pre|post`（同一剧本钉两态，翻转由批准事件而非改剧本触发）：
- **域 H（宿主 root shell）**：逐六动词（project list / environment save(无效体，证达后端) / delivery start / __release status / dashboard upload(拒形) / project delete(缺参形)）记录 (exit, 输出)。
  - `pre`（现态）：全部 = 门拒 token。
  - `post`：全部 = **无门拒 token**（exit 可为 1/2 业务/用法错，须带非门因文本）。
- **域 HELPER**：`docker exec <本剧本自建的 helper 形容器（/.dockerenv+linux、无 /mc/run.json）> mc <verb>` 或宿主 `docker run` 同形——`pre`/`post` **都必须**门拒（容器域不变量，正/反两态同钉=防放宽渗漏）。
- **域 AGENT**：同形容器**加挂 /mc/run.json**（假信封）——断言**双层**：门拒（pre/post 同）且即便经 `MC_SPINE` 直开路径，`RequireHostScope(id≠nil)` 亦拒（token："run.json callers are refused"）。
- 五要件同规（逐字头句="操作者视野门的三域行为尚未验证，本脚本为其验收器"+前提+token 判据+ts/host/uid 原始块+自清）；helper/agent 临时容器命名 `a24-*`、EXIT trap 删除；不触碰任何既有部署物。
- **与 A16 矩阵同宿主可同轮跑**（mc-prod 镜像在位；两者都不需持久部署）。

## 进码文件族维持 001（runtime_scope_prod.go 谓词 + 测试转写 + 证据件）；本增补=把"宿主域判据=无门 token 而非 exit 0"与"委托谓词禁触"升格为批准包正文，防实现时自作主张扩面。
