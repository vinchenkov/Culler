# A24 diff 计划增补 #3（pre 基线轮的实质设计发现——批准包范围必须修正；门仍一字未松）

日期：2026-11-21。来源：**定向机 pre 基线轮实测**（ACCEPT: PASS 于正确不变量集；`a24-pre-baseline-218.tsv` + `a24-pre-raw-218.log` 入册）。本件推翻 002 自己的一处判断，如实呈报。

## 实测分层（比读源看到的更深）
1. **三域拒因表（pre 实况）**：AGENT 域的 project/delivery/task 由 `RequireHostScope` 身份拒（"run.json callers are refused"，exit 1）——比 OS 门更硬、且**与 A24 无关**；H/HELPER 域里 `__release` 是**唯一**干净门 token 站点；其余动词在门 false 时落到普通/结构拒（unknown verb/usage）。
2. **第三道确定性防线**：容器内 spine 打开被 `GuardLockDomain` 拒绝（overlay 根挂载、非块设备卷 ⇒ "spine must live on a runtime-local named volume"）。helper/agent 域的 spine 面即使前两道全开也过不了这道——纵深防御实证，登记。
3. **决定性发现（修正 002 §追补）**：生产构建的宿主 mc 把 spine 路径钉死为常量 `/mc/spine/spine.db`（helper 视角）；`MC_SPINE` 直开模式**只存在于测试缝构建**（runtime_scope_testfake.go；生产函数直接 return 常量）。⇒ **只翻 `canInspectProjectVision` 一个谓词，在 Linux 宿主上的实际效果≈仅 `__release`（其数据面本就是宿主文件/记录）；project list / delivery / environment / task 视野类动词翻门后仍撞 `/mc/spine` 路径墙**——"宿主=放行"的裁定意图（操作者在 Linux 宿主能用 `culler` 面）并不达成。

## 达成裁定意图所需（真正的范围修正，归您批准）
把 Linux 宿主的 broker 路径接通=**两谓词同翻**：
- `canInspectProjectVision()`：Linux 宿主（无 /.dockerenv）=true；容器域不变（agent 由身份+锁域双拒兜底）。
- `shouldDelegateToHelper()`：Linux 宿主=true（与 darwin 同构——宿主 broker→ensure warm helper→`docker exec` 委托进锁域）。**这正是 002 曾说"禁触"的谓词——实测证明不禁触=裁定落空。**禁触区的原始顾虑（部署拓扑决定）如实转化为本增补的批准对象。
文件族变化：`runtime_scope_prod.go` 两函数（仍一个文件）；测试转写面扩至 main.go 委托序列的 linux 用例（既有 brokerDoctor/Backup 的 darwin 钉需加 linux 对应形）；三域矩阵 post 判据以本基线表为准（H.project-list 等行应从 `/mc/spine` 错变为经 helper 的业务应答或"deployment unavailable"族）。
**helper 在 Linux 侧本就由 A03 监督栈+向导段管理生命周期，delegate 只是"宿主命令找自己的 helper"，不改 helper 的创建权威——无第二权威问题；但 darwin 语义（委托=找 MC_HOME 派生名的 helper）在 Linux 部署上的可用性属 A25 段实况，post 矩阵将在部署在位时复验。**

## 状态
- 门**仍一字未松**（本轮生产码零改动）；001/002 的批准包以**本件为最终版**呈所有者（Q1 的实质内容已被实测改写——先列后改纪律的意义所在）。
- A16 矩阵轮已证 mc-prod/宿主二进制在定向机可用；A24 post 轮=批准进码后同宿主复跑，剧本 v2 无需再改（基线已钉）。
