# Lody 执行服务升级、停止与回收源码分析

## 2026-11-18 完整复核与三类收尾修复；下一版建议 0.4.0

**先核对并完成推送，再遍历 43 个生命周期入口／交接；确认的三类遗漏已修复，未在首个发现停止。** 初始本地及 origin/main 均为 `a60d087`；重新 fetch Lody 固定为 `df1abd7fe3a42228a0c3bc8163adb28da2d8fc2e`。相对前轮，52 份生命周期源码逐字节未变，另补存 `worktree-observations.ts`，共 53 份。上游新增的是会话预取／冷打开检查点，不改变本轮执行服务机制。

| 差异 | 修复前可观察结果 | 本轮最终处理与证明 |
| --- | --- | --- |
| S：local branch 身份被 tag 歧义改变 | 已有分支与同名 tag 同时存在时，`refname:short` 变成 `heads/…`。分配器漏认已存在分支、连续撞同一名字；归档还会把歧义拼写写成恢复分支。 | 按 Lody 使用 `branch --show-current` 和 `refname:lstrip=2`。当前／默认／已回收目录与新分支分配一致使用实际本地名称。真实 Git 的创建、菜单身份、归档恢复三条 RED→GREEN；原 branch／tag 不变，恢复仍接在原分支及原线程。产品 `fe5c563`。 |
| T：展示元数据延长执行闲置时钟 | 已读、改名或迟到标题只修改展示元数据，却因 session revision 改变在第19分钟重开20分钟计时；这是前轮已单列但未修的约定问题。 | 运行实例 gate 记录成功接纳的真实原生事件，source title 不计入；结合既有 dispatch epoch、queue revision 和后台分类续期。完整 Store revision 继续用于最终资格检查。四种元数据、迟到输出、原生轮次与已有队列／24h保护回归通过；无新 schema、表、journal 扫描或协议。产品 `36bb52d`。 |
| U：已回收目录的 Git 读取失败被覆盖 | `removedWorktreeStatus` 的 ref 读取报错，被稍后的 Store 查询覆盖，返回空分支列表且 error=nil。 | 与 Lody 的观察服务一样，失败立即返回，不伪造成功或重建目录。实际 worker endpoint 的故障边界 RED→GREEN；准确源错误和目录缺失保持。产品 `2ca0a38`。 |

同一轮完整测试还捕获 Transport 夹具读取空 PID：原 helper 直接创建目标文件，存在不代表内容写完。`ba27f3d` 仅把本轮隔离 helper 的就绪文件改为临时文件写完后 rename，三轮现有集成检查通过；正式后台注销逻辑未改。原失败和两个探针夹具错误均保留，未计作通过。

### 最终证据与保留边界

- 最终源码七包 **795 项父测试**全部实际执行通过（Worker371，无缓存）；竞态 **60 次父测试调用**通过，`count=3`，没有 DATA RACE。vet、六份冻结设计和 diff 检查通过。完整未过滤 root 的 UI／本地研究失败在报告单列，不称全仓全绿。
- 新签名 Worker BuiltAt7101／7102，实际嵌入 `2ca0a38+modified`，协议2.48／PrivateControl2／schema15；1960个源码指纹在签名后未变。真实 Worker／Host／Codex 隔离历史回放 **6 APPLIED、1既定H07 BLOCKED**；成功场景保留原设备／会话／线程／历史与暂停队列、未知输入不重发。旧停止接纳缺失仍按用户已确认的手动处理范围，不算升级成功。
- 一次 fresh-context 整体审阅覆盖上述四个自有提交，无 Critical／Important／Minor，Verdict Yes；12项工程裁定及成本完整记录，无 Minor延期。正式三台设备、SMAppService、Tailnet、SSH凭据和权限未操作。
- 排除了三个旧疑点和一个新疑点：独立子Agent取消不杀父、已有内存回收进行中时不等待、缺少drain证明时允许阻止回退，均与Lody一致；当前真实Git可移除已初始化子模块的已归档worktree，没有将手动目录回退的静态差异算作故障。
- 未登记Agent后代、首次旧存量手动处理、未知owner未接纳准备目录保留的边界保持。前轮P／Q／R修复保持；**在本次43项范围及已接受边界内，修复后没有其他已确认的机制差异**，不将本机隔离结果扩大为正式设备验收。

### 下一版版本号

建议 **`0.4.0`**。`VERSION`及GitHub远端最新数字tag均为`0.3.5`；这批变更包含固定supervisor、自动停止／闲置／归档回收、worktree恢复，以及删除旧强制环境操作流程，属于一次机制与交互方式的版本推进。worker协议也从已发布版2.44增加到2.48，使用新的minor版本比继续0.3.x补丁号更清楚。当前只给建议，**没有修改VERSION、创建tag、发布或部署**。仓库生产构建要求纯数字major.minor.patch，不建议直接填入当前脚本不支持的rc后缀；正式发布仍须以精确提交CI和设备验收为依据。

证据目录：`.ryugi/audits/lody-post-pqr-recheck-20261118/`。原始root、race、RED／GREEN、源码指纹、实际签名描述、完整43项矩阵和12项裁定均保存；同主题document／verify保留既有ID和历史证据。

| # | 入口／交接 | Shioji 落点 | Lody 对照 | 本轮结果 |
| --- | --- | --- | --- | --- |
| 1 | 独立 Host／Worker | supervisor/host_darwin.go；service_control.go | daemon-runner.ts；start.ts | 本轮完整调用链复核，未确认新增差异 |
| 2 | 停止意图覆盖崩溃重启 | supervisor/background_darwin.go | daemon-runner.ts；start-shutdown.ts | 本轮完整调用链复核，未确认新增差异 |
| 3 | 后台注销接纳与准确撤销 | background_stop.go；supervisor/background_darwin.go | daemon-runner.ts 的独立控制／停止所有权 | 本轮完整调用链复核，未确认新增差异 |
| 4 | 普通崩溃预算、退避与健康重置 | supervisor/retry.go；start_retry.go | daemon-runner.ts／supervisor contract | 本轮完整调用链复核，未确认新增差异 |
| 5 | 在线准备与旧服务继续运行 | supervisor/host_darwin.go:update | daemon-runner.ts:prepare | 本轮完整调用链复核，未确认新增差异 |
| 6 | 默认空闲升级准入 | service_control.go:businessStopReady／reserveDispatch | machine-turn-admission.ts:reserveMachineTurn | 本轮完整调用链复核，未确认新增差异 |
| 7 | immediate 排空与封闭 | service_control.go:DrainService | start.ts:switchWhenIdle | P 修复保持；最终准入／原实例检查仍在 |
| 8 | 私有控制独立于业务 socket | supervisor/lifecycle_darwin.go；service_control.go | managed-daemon-ipc.ts；daemon-runner.ts | 本轮完整调用链复核，未确认新增差异 |
| 9 | drained 与最终 writer 释放 | shutdown.go；supervisor/host_darwin.go:shutdown | start-shutdown.ts；start.ts | 本轮完整调用链复核，未确认新增差异 |
| 10 | 候选失败与一次回退 | supervisor/host_darwin.go:update | daemon-runner.ts:decideExit | 排除疑点：两端缺少已确认 drain 均可阻止回退 |
| 11 | 已提交结果与 resume 核对 | supervisor/host_darwin.go:completeCommitted／resume | daemon-runner.ts 的 committed／observing 交接 | 本轮完整调用链复核，未确认新增差异 |
| 12 | 冷修复／未知 writer | supervisor/repair_darwin.go | managed-daemon-release.ts；daemon-runner.ts | 本轮完整调用链复核，未确认新增差异 |
| 13 | 普通提交第一次准备前预留 | server.go:submitLocked；service_control.go:reservePreparation | session-execution-service.ts:runVisibleSessionTurn | 本轮完整调用链复核，未确认新增差异 |
| 14 | 队列准入与分发 | queue.go；server.go:submitLocked／dispatch | session-execution-service.ts 的 inbox／owned turn | 本轮完整调用链复核，未确认新增差异 |
| 15 | Steer 两次附件交接 | queue.go:steerLocked | session-execution-service.ts 的 acknowledged steer／owner | 本轮完整调用链复核，未确认新增差异 |
| 16 | 子 Agent 发送和回执查询 | work_input.go；native_dispatch.go | agent-client.ts 的 native prompt 所有权 | 本轮完整调用链复核，未确认新增差异 |
| 17 | 审批／问题回复 | native_dispatch.go:respondRequest；codex/runtime.go:Respond | agent-client.ts 的 pending request 生命周期 | 本轮完整调用链复核，未确认新增差异 |
| 18 | 压缩的 Resume／初始化交接 | context_action.go:77–118 | session-manager.ts 的 await 后 abort 检查 | P 修复保持；最终准入／原实例检查仍在 |
| 19 | 历史重开最后启动与截断 | history.go:138–174；session_runtime.go | session-manager.ts 的 await 后 abort 检查 | P 修复保持；最终准入／原实例检查仍在 |
| 20 | reload 接纳与迟到返回 | session_reload.go；session_transition.go | session-manager.ts:retireSessionForReconfiguration | 本轮完整调用链复核，未确认新增差异 |
| 21 | 初始化撤销／继任实例 | runtime_start.go；session_runtime.go | session-manager.ts:abandonPendingSessionCreate | P 修复保持；最终准入／原实例检查仍在 |
| 22 | Stop ACK 与真正停止 | stop.go:interruptTurn | session-execution-service.ts:drainCancelledPrompt | 本轮完整调用链复核，未确认新增差异 |
| 23 | 独立子 Agent Stop | server.go:session.work.stop；codex/work.go:StopWork | session-execution-service.ts:cancelSession 的 subagentTaskId 分支 | 排除疑点：Lody 独立子 Agent Stop 同样不强杀父实例 |
| 24 | 强制退休与 native close 并行 | retirement.go:beginRuntimeRetirement | session.ts:terminateOnce／escalateTermination | 本轮完整调用链复核，未确认新增差异 |
| 25 | 失败退休分离与后台重试 | detached_runtime.go；archive_retirement.go | session-manager.ts:detachSession；session.ts 的失败保留 | 本轮完整调用链复核，未确认新增差异 |
| 26 | 登记组、根已退出与 MCP／Node | managedexec/process_darwin.go／retirement_darwin.go；codex/executor.go | session.ts；process-tree-registry.ts；shared/node/process.ts | 本轮完整调用链复核，未确认新增差异 |
| 27 | 真实后台状态与活跃子 Agent | codex/work.go；native_background_test.go | agent-client.ts:getNativeWork；session-gc-manager.ts | 本轮完整调用链复核，未确认新增差异 |
| 28 | 20min／30s、24h GC 与最终资格 | idle_runtime.go；store/runtime_idle.go | session-gc-manager.ts:checkCleanupEligibility | T：展示元数据曾重置执行闲置时钟；本轮已修复，真实事件／dispatch／queue 续期 |
| 29 | 内存压力与并发回收 | memory_pressure_darwin.go；memory_pressure_test.go | session-gc-manager.ts:evictForMemoryPressure | 排除疑点：Lody 已有内存回收进行中时同样不让新请求等待 |
| 30 | 会话和工作区归档 | archive_retirement.go；server.go 的 archive handlers | message-handler.ts:releaseArchivedSessionRuntime | 本轮完整调用链复核，未确认新增差异 |
| 31 | 永久删除与持久 outbox | archive.go:commitArchiveDeletion／cleanArchiveGarbageItem | message-handler.ts:handleSessionDeleted | 本轮完整调用链复核，未确认新增差异 |
| 32 | 过期归档与恢复竞态 | archive.go:cleanupArchives；archive_retirement.go | message-handler.ts 的 metadata archive watcher | 本轮完整调用链复核，未确认新增差异 |
| 33 | worktree GC 独立调度与重试 | worktree_gc.go；worktree_lifecycle.go:cleanOwnedWorktrees | worktree-gc.ts:schedule／sweep | 本轮完整调用链复核，未确认新增差异 |
| 34 | 备份、真实分支、detached ref | worktree_lifecycle.go:archiveOwnedWorktree | worktree-manager.ts:archiveWorktree | 本轮完整调用链复核，未确认新增差异 |
| 35 | 共享 checkout 与 repo 变更保护 | git_dispatch.go；git.go:checkoutGuard／prepareGitLocked | worktree-manager.ts:withRepoLock | 本轮完整调用链复核，未确认新增差异 |
| 36 | 已有／缺失自建目录懒恢复 | worktree_lifecycle.go:restoreOwnedWorktrees | worktree-manager.ts:runWorktreeAddWithPruneRetry | Q 修复保持；S：新增分支与标签同名反例已修复 |
| 37 | 新建 worktree 的部分完成重试 | git.go:481–506 | worktree-manager.ts:createWorktree／runFreshWorktreeAddWithPruneRetry | Q 修复保持；S：新增分支与标签同名反例已修复 |
| 38 | Git 取消、期限和输出管道 | git.go:38–60；shutdown.go:wg.Wait | git-execution.ts:WorktreeGitLive；shared/node/process.ts:runCommand | R 修复保持；进程组取消＋WaitDelay 仍有界 |
| 39 | 安装／目录刷新及 Git 迟到读取 | catalog_refresh.go；runtime_settings.go；git.go；worktree_lifecycle.go:removedWorktreeStatus | managed-runtime-update-coordinator.ts；worktree-manager.ts 的本地恢复 | U：已回收目录读取 refs 错误被覆盖；本轮已修复 |
| 40 | 强制处理 UI、历史事件及新抽象 | internal/ui（356 文件未改）；domain／harness boundary | Lody 的逻辑退休与进程回收职责分离 | 生产 UI 无旧强制页／入口接线；本轮未改 UI／中立协议 |
| 41 | 准确 branch namespace 和同名标签 | git.go；worktree_recovery.go；worktree_lifecycle.go | worktree-manager.ts:1281；worktree-observations.ts:108 | S：3 条真实 Git 消费路径 RED→GREEN；既有 refs 保持 |
| 42 | 展示元数据与执行活动时钟 | idle_runtime.go；runtimeEventGate.activity | session-gc-manager.ts:combineGCSessionSources；message-handler.ts:touchSession | T：已读／改名／source title RED→GREEN，原生轮次／迟到输出对照保持 |
| 43 | 已回收目录的 ref 读取错误 | removedWorktreeStatus | WorktreeObservationsLive.currentBranchAt；WorktreeGitLive.run | U：真实 endpoint＋故障边界返回 error，不伪造空分支成功 |

## 2026-11-18 P／Q／R 修复与最终验证

**三类差异及五个审计反例已修复，本机隔离验证完成，正式设备未部署。** 自有产品提交 `a7985dc`、`bf97ff2`、`bbf84ac`；Lody 依据保持固定 `50d8d70`。并发落入的 IME 提交 `9392f5f` 没有混入本轮三提交审阅包；本轮未改 UI、Tailnet、服务注册或凭据。

| 修复 | 现在的行为 | 验证 |
| --- | --- | --- |
| P：维护最终接纳 | 压缩在 setup／Resume 返回后重验更新准入和准确 runtime；历史重开在最后启动后重新做完整 preflight，在实际截断回调里检查准入；初始化在旧清理后及发布前检查同一封闭状态。取消时先 fence 已知 scope，迟到控制器只清理自己。 | 两条原反例 RED→GREEN，排空后没有压缩接纳或历史截断，也不发布可复用的新 runtime；正常压缩、历史接续与继任保护相邻回归通过。 |
| Q：可恢复的自建 worktree | 在变更 Git 前，使用原 owned_worktrees 保存 CreationOperation／CreationBase。stale 登记 prune 后只重试一次；已实际建好的目录按准确操作、common-dir、分支及 HEAD 核对后供原请求重试。冲突引用改分配新名字，跨调用的仅分支状态使用保存 HEAD 切新引用，不接管既有分支。 | Q1／Q2 RED→GREEN；另复现修正 GC 扫掉未接纳准备目录、失败清理误删旧默认分支两处相邻条件。无归属目录、symlink、其他已登记 worktree 和冲突分支保持。 |
| R：Git 取消及输出收尾 | 每次 Git 有独立进程组，context 取消或输出失败结束本次命令范围；WaitDelay 限制继承管道收尾。没有进程名扫描或扩大到未登记 Agent 后代。 | 真实 Git clean filter 在取消后退出，调用无需先释放测试管道；无关独立进程保持。原 250ms 反例保留，正式回归夹具先观察 filter 已进入再取消，避免负载导致场景尚未开始就过期。 |

**文件保留策略明确：** 未接纳创建没有权威归档／删除 owner 时，准备目录及原件保留供同一操作重试，可能长期占磁盘；它们不持有会话进程或更新 reservation。明确的本地接纳失败只按本次保存的实际分支、Git 身份、回执与引用做无 force 清理。已经接纳的会话／工作区归档及永久删除仍按原机制回收，不因这条未知归属保护变成永久保留。

### 验证与来源

- 未过滤 `go test -json -timeout=10m ./...` 已执行完。七个生命周期相关包共 **789 项父测试通过**，含 Worker **365 项**；三个未改包命中 Go 缓存。全仓仍有 UI 的 `TestWorkspaceHeadingSharesTheWorkspaceRowVisibleEdges`、`TestWorkspaceHeadingInsetsAndNoPerWorkspacePlus`，以及 `build/composer-enter/baseline`、interaction-latency 的 app／ui、worker-recovery-20261116 四个研究快照编译失败；原始命令不改成全绿。
- 本轮并发检查 **33 次父测试调用通过**（count=3），无 DATA RACE。vet、git diff --check 与六份冻结设计检查通过。
- 两只新建临时 ad-hoc 签名 Worker 均严格验签通过，实际 Revision 为 `bbf84ac+modified`、BuiltAt 6101／6102、协议 2.48、PrivateControl 2、schema 15。1960 个源码指纹（含 MyGo）在签名后零变化；不以文档提交改标旧二进制。
- 历史签名回放实际 **7 个场景，6 APPLIED、1 既定 H07 BLOCKED**，count=1。成功项保留设备／会话／线程／历史与暂停队列，未知输入不重发，显式原线程接续成功；H05 未登记后代仍属接受范围，H07 不算成功升级。本轮没有为了扩大数字重复同一通过检查。
- 一次 fresh-context 独立整体审阅覆盖自有三提交及四项 Review Focus，Critical／Important／Minor 均为 None，Verdict Yes。没有二次派审；这只证明源码审阅，不等于正式部署认证。
- 20min／30s／24h、未登记后代与首次旧存量手动处理保持。原已读／改名续期附记、真实 SMAppService／三台正式设备、历史缺失日志与既有 UI／研究问题没有被本轮修复冒充解决。

12 项完整工程裁定及成本、Minor None、一次审阅原文、各项 RED／GREEN 与执行 ledger 在 `.ryugi/audits/lody-pqr-repairs-20261118/`；同主题验证报告保留原 P／Q／R IDs，改为相应的通过检查。下方“3 类差异未修”及更早记录为当时审计原件。

证据：[整包](../.ryugi/audits/lody-pqr-repairs-20261118/backend-summary.txt)、[并发](../.ryugi/audits/lody-pqr-repairs-20261118/race-summary.txt)、[签名历史](../.ryugi/audits/lody-pqr-repairs-20261118/historical-summary.txt)、[来源](../.ryugi/audits/lody-pqr-repairs-20261118/source-provenance.txt)、[全仓失败范围](../.ryugi/audits/lody-pqr-repairs-20261118/root-summary.txt)、[审阅](../.ryugi/audits/lody-pqr-repairs-20261118/review.txt)、[裁定](../.ryugi/audits/lody-pqr-repairs-20261118/rulings.txt)。


## 2026-11-17 推送后的整轮复核：3 类差异、5 个条件已复现（本轮未修）

**已推送实现与上一轮文档；完整复核结束，仍有 P／Q／R 三类差异。** 审计产品基线 `ac6a075`，远端 `origin/main` 已核对同一 SHA。重新 fetch 的 Lody 为 `50d8d70791e93acb8b3973f5d86d8eeba9d3b52d`；较 `d6df2f2` 只有定时任务 #2850 变化，52 份保存的源码／测试原件逐字节核对无误。下述检查期间 1006 个产品／测试文件指纹未改；本轮没有修产品、部署正式设备或改变已接受的边界。

### P．高：压缩／历史重开最后一次原生等待后仍漏更新准入

- [context_action.go:77](../internal/worker/context_action.go#L77) 的 sessionRuntime 和 [Resume:90](../internal/worker/context_action.go#L90) 之后只检查 transition／ThreadID，没有在 [AcceptContextAction:108](../internal/worker/context_action.go#L108) 前重新检查 updateAdmission。
- [history.go:139](../internal/worker/history.go#L139) 最后一次 sessionRuntime 返回后直接 [ActivateHistory／CommitHistory:154](../internal/worker/history.go#L154)，同样只检查 owner 是否 active。DrainService 封闭更新准入并不等于此维护 owner 已被撤销。
- 隔离真实 Worker／Store、中立 harness 原生等待，各 3 轮：先让 Resume 或新 runtime Start 等待，实际 DrainService 确认 `Ready=true`，再释放。压缩仍 `operation=confirmed, nativeCalls=1`；历史仍 `history=complete`，接纳修订输入，原输入前缀被替换。**证明排空后仍执行／提交历史替换，不宣称已现场造成正式设备会话损失。** 无 drain 的同夹具阳性对照共 6 次通过。
- Lody 的 [machine gate](https://github.com/LodyAI/Lody/blob/50d8d70791e93acb8b3973f5d86d8eeba9d3b52d/apps/cli/src/session/machine-turn-admission.ts#L1) 与 [start.ts:884](https://github.com/LodyAI/Lody/blob/50d8d70791e93acb8b3973f5d86d8eeba9d3b52d/apps/cli/src/commands/start.ts#L884) 封闭后进入取消所有权；[session-manager.ts:812](https://github.com/LodyAI/Lody/blob/50d8d70791e93acb8b3973f5d86d8eeba9d3b52d/apps/cli/src/session/session-manager.ts#L812)／957 在原生创建返回后核对 abort。对齐应落在最后接纳／截断前，并让 sessionRuntime 发布与同一 update/owner fence 一致；不能只在首次 Git 准备后检查。

### Q．中：worktree 部分完成状态缺少 Lody 的恢复／重试

1. **Q1，目录消失但 Git 登记还在。** [restoreOwnedWorktrees:121](../internal/worker/worktree_lifecycle.go#L121) 直接 worktree add。真实 Git 的 3 轮结果均为 `missing but already registered worktree`；相同元数据和原分支只执行本地 `worktree prune` 后，再次原 owning restore 均成功。Lody [runWorktreeAddWithPruneRetry](https://github.com/LodyAI/Lody/blob/50d8d70791e93acb8b3973f5d86d8eeba9d3b52d/apps/cli/src/session/worktree/worktree-manager.ts#L1184) 识别此类错误后 prune 一次再重试。
2. **Q2，Git 已创建目录而调用报告失败。** [prepareGitLocked:481](../internal/worker/git.go#L481) 在 add 报错时直接返回，尚未保存 owning record。故障注入让真实系统 Git 完成 add 后返回失败，3 轮均得到 `directoryExists=true, owningRecords=0`；同 OperationID 重试均撞上 `branch already exists`。Lody [createWorktree:1313](https://github.com/LodyAI/Lody/blob/50d8d70791e93acb8b3973f5d86d8eeba9d3b52d/apps/cli/src/session/worktree/worktree-manager.ts#L1313) 先核对已存在目录，另有 [fresh retry:1223](https://github.com/LodyAI/Lody/blob/50d8d70791e93acb8b3973f5d86d8eeba9d3b52d/apps/cli/src/session/worktree/worktree-manager.ts#L1223) 处理该次创建的分支和 stale add。**这是明确标记的完成回复故障注入，不冒称真实机器已发生过同样 Git 错误。**

两者都须按已确认目录／操作归属核对和有限重试，不能删除任意冲突分支或强制接管别人的目录；不需要新增强制环境页面。

### R．中：Git 的 context 期限没有界定输出管道等待

[gitRun:42](../internal/worker/git.go#L42) 使用 CommandContext，但没有 WaitDelay 或同等输出／进程树取消边界。真实 Git clean filter 的后续进程继承管道时，父 Git 被 context 取消并不意味着 Cmd.Run 能返回。Lody 的 [WorktreeGitLive](https://github.com/LodyAI/Lody/blob/50d8d70791e93acb8b3973f5d86d8eeba9d3b52d/apps/cli/src/session/worktree/git-execution.ts#L95) 通过 [runCommand:1208](https://github.com/LodyAI/Lody/blob/50d8d70791e93acb8b3973f5d86d8eeba9d3b52d/packages/shared/src/node/process.ts#L1208) 持有 processGroup，超时中断 scoped 输出收集并按策略结束该命令树。

三个本机隔离真实 Git 案例使用 250ms 父 context，期限后再观察 300ms 仍未返回；仅释放 filter 管道后约 555–562ms 才退出。这里只用了短预算证明“期限不是返回上限”，没有让正式服务永久挂死，也没有冒称真实升级链路现场失败。由于 worktree collector 加入 shutdown 的 wg，静态调用链表明这类等待可能使退出超过预算，进而拿不到 drained 证明。**本项核查 worker 自己启动的 Git 命令与 I/O 期限，不扩展用户排除的未登记 Agent 后代追踪。**

### 整轮结果、排除的疑点与验证边界

- 40 个入口／交接均列在下表，发现 3 类／5 条条件后继续完成余项，未在首个发现停止。
- 五个新增诊断各 3 轮，共 **15 次预期失败**；它们不计入通过数。两条维护路径无 drain 阳性对照 **6 次通过**；Q1 的 prune 阳性对照 **3 次成功**。
- 七包原有 **781 项父测试通过**，其中六包为 Go 缓存命中、Store 122 项本轮实际执行；这是相邻回归，不抵消新反例。没有本轮新的签名制品或正式升级回放；上一轮签名证据仅对其实际来源有效。
- 特意排除了三项候选：内存压力已有回收时不等待与 Lody 当前代码一致；子 Agent 独立取消不强杀父实例，Lody 也如此；候选退出而缺少 drain 证明时，两端都可能阻止回退，不能将所有安全阻止都标成差异。
- 初始化 probe 初版错误等待整个 Server wg 才 Close，造成诊断夹具超时；修正夹具后重跑。按保存的 scope PID／空 launches／准确 audit-source 与 audit-history 回执，只移除了这一私有临时目录。没有按进程名字清理用户服务。
- 已接受的 20min／30s、24h、未登记后代、首次旧存量手动处理保持；已读／改名续期仍属单列项目约定。真实 SMAppService、三台正式设备、历史缺失日志与原有 UI／研究测试问题没有被本轮审计消除。

证据目录：`.ryugi/audits/lody-complete-recheck-20261117/`。对应 [原件与来源](../.ryugi/audits/lody-complete-recheck-20261117/source-and-push.txt)、[新增反例](../.ryugi/audits/lody-complete-recheck-20261117/probes-summary.txt)、[无 drain 对照](../.ryugi/audits/lody-complete-recheck-20261117/controls-summary.txt)、[相邻回归](../.ryugi/audits/lody-complete-recheck-20261117/neighbours-summary.txt)。

| # | 入口／交接 | Shioji 落点 | Lody 对照 | 本轮结论 |
| --- | --- | --- | --- | --- |
| 1 | 独立 Host／Worker | supervisor/host_darwin.go；service_control.go | daemon-runner.ts；start.ts | 未发现新增差异 |
| 2 | 停止意图覆盖崩溃重启 | supervisor/background_darwin.go | daemon-runner.ts；start-shutdown.ts | 未发现新增差异 |
| 3 | 后台注销接纳与准确撤销 | background_stop.go；supervisor/background_darwin.go | daemon-runner.ts 的独立控制／停止所有权 | 未发现新增差异 |
| 4 | 普通崩溃预算、退避与健康重置 | supervisor/retry.go；start_retry.go | daemon-runner.ts／supervisor contract | 未发现新增差异 |
| 5 | 在线准备与旧服务继续运行 | supervisor/host_darwin.go:update | daemon-runner.ts:prepare | 未发现新增差异 |
| 6 | 默认空闲升级准入 | service_control.go:businessStopReady／reserveDispatch | machine-turn-admission.ts:reserveMachineTurn | 未发现新增差异 |
| 7 | immediate 排空与封闭 | service_control.go:DrainService | start.ts:switchWhenIdle | P：维护末端仍可越过封闭 |
| 8 | 私有控制独立于业务 socket | supervisor/lifecycle_darwin.go；service_control.go | managed-daemon-ipc.ts；daemon-runner.ts | 未发现新增差异 |
| 9 | drained 与最终 writer 释放 | shutdown.go；supervisor/host_darwin.go:shutdown | start-shutdown.ts；start.ts | 未发现新增差异；不以 PID 消失替代持久化证明 |
| 10 | 候选失败与一次回退 | supervisor/host_darwin.go:update | daemon-runner.ts:decideExit | 未发现新增差异；两端都允许未确认 drain 时阻止回退 |
| 11 | 已提交结果与 resume 核对 | supervisor/host_darwin.go:completeCommitted／resume | daemon-runner.ts 的 committed／observing 交接 | 未发现新增差异 |
| 12 | 冷修复／未知 writer | supervisor/repair_darwin.go | managed-daemon-release.ts；daemon-runner.ts | 未发现新增差异；真实未知不会无条件启动第二 writer |
| 13 | 普通提交第一次准备前预留 | server.go:submitLocked；service_control.go:reservePreparation | session-execution-service.ts:runVisibleSessionTurn | 主流程保持；P 的维护末端另列 |
| 14 | 队列准入与分发 | queue.go；server.go:submitLocked／dispatch | session-execution-service.ts 的 inbox／owned turn | 未发现新增差异 |
| 15 | Steer 两次附件交接 | queue.go:steerLocked | session-execution-service.ts 的 acknowledged steer／owner | 未发现新增差异 |
| 16 | 子 Agent 发送和回执查询 | work_input.go；native_dispatch.go | agent-client.ts 的 native prompt 所有权 | 未发现新增差异 |
| 17 | 审批／问题回复 | native_dispatch.go:respondRequest；codex/runtime.go:Respond | agent-client.ts 的 pending request 生命周期 | 未发现新增差异 |
| 18 | 压缩的 Resume／初始化交接 | context_action.go:77–118 | session-manager.ts 的 await 后 abort 检查 | P：已复现，排空后仍 confirmed 并调用 Compact |
| 19 | 历史重开最后启动与截断 | history.go:138–174；session_runtime.go | session-manager.ts 的 await 后 abort 检查 | P：已复现，排空后仍 complete 并替换旧历史 |
| 20 | reload 接纳与迟到返回 | session_reload.go；session_transition.go | session-manager.ts:retireSessionForReconfiguration | 未发现新增差异；接纳位于原生重载前且有 owner |
| 21 | 初始化撤销／继任实例 | runtime_start.go；session_runtime.go | session-manager.ts:abandonPendingSessionCreate | 继任保护保持；最终更新准入与 P 共因 |
| 22 | Stop ACK 与真正停止 | stop.go:interruptTurn | session-execution-service.ts:drainCancelledPrompt | 未发现新增差异 |
| 23 | 独立子 Agent Stop | server.go:session.work.stop；codex/work.go:StopWork | session-execution-service.ts:cancelSession 的 subagentTaskId 分支 | 排除疑点：Lody 此分支同样直接取消子任务，不强杀父实例 |
| 24 | 强制退休与 native close 并行 | retirement.go:beginRuntimeRetirement | session.ts:terminateOnce／escalateTermination | 未发现新增差异 |
| 25 | 失败退休分离与后台重试 | detached_runtime.go；archive_retirement.go | session-manager.ts:detachSession；session.ts 的失败保留 | 未发现新增差异 |
| 26 | 登记组、根已退出与 MCP／Node | managedexec/process_darwin.go／retirement_darwin.go；codex/executor.go | session.ts；process-tree-registry.ts；shared/node/process.ts | 未发现新增差异；未登记后代仍属用户接受范围 |
| 27 | 真实后台状态与活跃子 Agent | codex/work.go；native_background_test.go | agent-client.ts:getNativeWork；session-gc-manager.ts | 未发现新增差异 |
| 28 | 20min／30s、24h GC 与最终资格 | idle_runtime.go；store/runtime_idle.go | session-gc-manager.ts:checkCleanupEligibility | 未发现新增机制差异；已读／改名时钟保留既有附记 |
| 29 | 内存压力与并发回收 | memory_pressure_darwin.go；memory_pressure_test.go | session-gc-manager.ts:evictForMemoryPressure | 排除疑点：Lody 对已在进行的回收同样不等待；WARNING 回收／CRITICAL 拒绝 |
| 30 | 会话和工作区归档 | archive_retirement.go；server.go 的 archive handlers | message-handler.ts:releaseArchivedSessionRuntime | 未发现新增差异 |
| 31 | 永久删除与持久 outbox | archive.go:commitArchiveDeletion／cleanArchiveGarbageItem | message-handler.ts:handleSessionDeleted | 未发现新增差异；迟到事件不恢复已删除会话 |
| 32 | 过期归档与恢复竞态 | archive.go:cleanupArchives；archive_retirement.go | message-handler.ts 的 metadata archive watcher | 未发现新增差异 |
| 33 | worktree GC 独立调度与重试 | worktree_gc.go；worktree_lifecycle.go:cleanOwnedWorktrees | worktree-gc.ts:schedule／sweep | 未发现新增差异 |
| 34 | 备份、真实分支、detached ref | worktree_lifecycle.go:archiveOwnedWorktree | worktree-manager.ts:archiveWorktree | 未发现新增差异 |
| 35 | 共享 checkout 与 repo 变更保护 | git_dispatch.go；git.go:checkoutGuard／prepareGitLocked | worktree-manager.ts:withRepoLock | 未发现新增差异；本项目保持单 owning worker 的序列化边界 |
| 36 | 已有／缺失自建目录懒恢复 | worktree_lifecycle.go:restoreOwnedWorktrees | worktree-manager.ts:runWorktreeAddWithPruneRetry | Q1：缺少 stale registration 的 prune-and-retry |
| 37 | 新建 worktree 的部分完成重试 | git.go:481–506 | worktree-manager.ts:createWorktree／runFreshWorktreeAddWithPruneRetry | Q2：实际目录已建、无 owning record，同一操作撞已有分支 |
| 38 | Git 取消、期限和输出管道 | git.go:38–60；shutdown.go:wg.Wait | git-execution.ts:WorktreeGitLive；shared/node/process.ts:runCommand | R：context 到期未界定 pipe 等待；已用真实 Git clean filter 复现 |
| 39 | 安装／目录刷新及 Git 迟到读取 | catalog_refresh.go；runtime_settings.go；git.go；worktree_lifecycle.go:removedWorktreeStatus | managed-runtime-update-coordinator.ts；worktree-manager.ts 的本地恢复 | 未发现新增差异 |
| 40 | 强制处理 UI、历史事件及新抽象 | internal/ui（356 文件未改）；domain／harness boundary | Lody 的逻辑退休与进程回收职责分离 | 未新增强制页面或入口，未引入迁移／后代追踪框架；旧后台协议不冒称全删 |



## 2026-11-17 L／M／N／O 对齐实施与验证

**四类差异已按 Lody 的职责和时序修复，本机隔离验证完成，正式设备未部署。** 产品提交 `8427145`、`4aeda7a`、`a8169dc`、`b6ae113`、`1b4e56e`；`4ba464a` 只改测试夹具的锁与关闭顺序。Lody 依据保持固定 `d6df2f2`，不复制其运行时，也未增加强制处理页或首次旧存量兼容。

| 对齐项 | 最终行为 | 对应证明 |
| --- | --- | --- |
| L：外部等待与设备准入分开 | Git 状态、diff、监测、已有／已回收 worktree 状态均释放 dispatch 等待；本 checkout 准备仍串行，跨 checkout 的同分支读取不被其他备份串行化，共享仓库变更串行。子 Agent 发送、回执及审批回复由捕获 runtime/gate 持有原生等待。 | 实际私有 prepare-drain 在等待期间可响应；同目录切分支仍阻止输入越过；迟到 Git 读取不修改继任会话。已回收目录读取不重建目录。 |
| M：首次准备 await 前预留 | 输入、reload／history／compact 与子输入准备持有私有 reservation；更新／后台停止看得到它。返回后重新核对接纳、source、会话／工作区及旧 owner。 | 默认 drain／后台停止不能越过恢复中的输入；immediate 已封闭后，原提交及附件准备中的 Steer 不再被接纳。 |
| N：翻译原生后台活动 | 以准确 native thread＋turn 的结构化终态决定 background waiting。父结束不降低仍活跃子 Agent 的 running；迟到输出保留 waiting，未知状态继续保护。 | 实际 Codex Normalizer → Worker／Store → GC 链路，虚拟 23h 保留／24h 回收原环境，原 ThreadID／历史保留；不是现场等待 24h 或真实模型 dev server 验收。 |
| O：worktree GC 独立调度 | 一个独立消费者、一个合并唤醒、原有按 checkout 退避；加入 worker shutdown 的生产者等待。归档／删除退休不用等待整个 Git sweep，备份／共享引用保护保持。 | 真实 archiveLoop 在另一个目录备份仍等待时完成新归档环境退休；共享目录、脏内容备份、detached ref、失败重试与懒恢复原有回归保持。 |

### 一次整体审阅的四处收尾

独立审阅原件的裁定为 **With fixes**，没有二次派审或将其改写成无问题结论。四项均经 RED→GREEN：

1. native 进程退出后，子输入未结回执改为 indeterminate；已 abandoned 的原回执不被覆盖，查询不重发。
2. Steer 两次附件等待后分别复核准入与准确 runtime／source／operation；退休后不会调用旧实例或继任实例。
3. checkoutGuard 的解锁等待后，在实际变更前重验更新准入、原会话、工作区及待处理工作；新建 worktree 同样有最终准入检查。
4. 已回收 worktree 的原生 ref 查询也释放全局锁，返回后检查源会话、目录存在性和所有权记录。

两个旧直接 guard 夹具按真实生产调用边界取得 dispatch 锁，完整根命令触发的原 fatal 输出保留。竞态模式另发现目录清理早于后台 GC 退出；调整测试资源注册顺序后，相关案例连续 10 次通过。三只属于本轮失败夹具的 anchor 按保存的 PID＋启动时间＋进程组准确回收，确认剩余为零并移除其临时状态；没有按名字清理其他进程。

### 最终验证与来源

- 六个相关包完整 **659 项父测试通过**，其中整个 Worker **357 项**；竞态运行 **63 次父测试调用**通过（count=3，非 63 个独立测试）。vet 与六份冻结设计检查通过。
- 最终两只严格 ad-hoc 验签 Worker 的历史回放 **12 APPLIED、2 既定 H07 BLOCKED**。成功场景保留原设备／会话／线程／历史与暂停队列，未知输入不重发，显式原线程接续成功。H05 未登记后代仍在接受的覆盖范围之外，H07 不算升级成功。
- 签名实际嵌入 `1b4e56e+modified`，BuiltAt 5101／5102，协议 2.48、PrivateControl 2、schema 15。构建后的 1006 个源码指纹仅两份测试夹具变化，非测试产品源码零变化；没有用后续测试／文档提交给旧二进制改标。
- 完整未过滤根命令确实执行：原始 27 个正式包中 25 个通过／编译，Worker 的旧 guard 夹具 fatal 与 UI 两项既有间距断言失败，另有三个本地研究快照编译失败。**两处 guard 夹具修正后，整个 Worker 已在最终测试源码重跑通过**；原根输出不改成全绿。356 个 UI 文件与审计基线一致，UI 两项与研究失败没有作为本轮修复成果。
- 20 分钟／30 秒、24h 后台保护、未登记后代与首次旧存量手动处理保持；Tailnet、正式设备服务、SMAppService、SSH 凭据与权限均未操作。前轮通用 revision 的已读／改名续期附记仍未修，单列为本项目约定问题。

完整 15 项工程裁定与成本、无 Minor 延期的清单、原始日志和执行 ledger 在 `.ryugi/audits/lody-lmno-repairs-20261117/`；同主题验证报告保留旧 ID。下方“四类尚未修复”是修复前审计原件，当前以本节为准。

证据：[整包](../.ryugi/audits/lody-lmno-repairs-20261117/lifecycle-final-summary.txt)、[竞态](../.ryugi/audits/lody-lmno-repairs-20261117/race-final-summary.txt)、[签名历史回放](../.ryugi/audits/lody-lmno-repairs-20261117/historical-summary.txt)、[构建来源](../.ryugi/audits/lody-lmno-repairs-20261117/source-provenance.txt)、[原根失败范围](../.ryugi/audits/lody-lmno-repairs-20261117/root-summary.txt)、[完整裁定](../.ryugi/audits/lody-lmno-repairs-20261117/rulings.txt)。

## 2026-11-17 修复后的完整复核：四类差异，七个条件已复现（本轮未修）

**整轮对照已完成，仍有四类未对齐机制 L／M／N／O。** 本轮是审计，不是新一轮修复。Shioji `8bc2b19`；重新 fetch 的 Lody 为 `d6df2f2447cf6903d71e864f12cc1519de0c1b2c`。20 份保存的原始源码重新与固定提交逐字节比对，997 个本项目源码／测试指纹均未改变。以前的 H／I／J／K 修复仍成立，以下是不同调用路径上的遗漏。

### L．高：设备全局调度锁仍跨外部 I/O

- [worktree_lifecycle.go:94](../internal/worker/worktree_lifecycle.go#L94)：恢复仍存在的自建目录时，持有 dispatchMu 调用完整 queryGit。普通 `git.status`、`git.changes`、`git.file`、checkout 与提交的 prepareGit 也在同一全局边界调用 Git，并非只有自建目录这个分支。
- [work_input.go:102](../internal/worker/work_input.go#L102)、[work_input.go:147](../internal/worker/work_input.go#L147)：子 Agent SendWork 与 WorkInputReceived 分别以 30 秒／10 秒预算等待原生 RPC，但不释放全局锁。与已修的普通 Submit／Steer、catalog refresh 做法不同。
- 审批／问题回复在 [server.go:973](../internal/worker/server.go#L973) 也持锁调用 Respond；Codex 的 [runtime.go:281](../internal/harness/codex/runtime.go#L281) 同步 pipe 写入不接收 context。此条是同类静态调用链，**未做真实 Codex pipe 堵塞现场复现**，不单独计为第八个条件。

Lody 在 [machine-turn-admission.ts:1](https://github.com/LodyAI/Lody/blob/d6df2f2447cf6903d71e864f12cc1519de0c1b2c/apps/cli/src/session/machine-turn-admission.ts#L1) 用短同步 gate 管理全机准入；Git 使用 [worktree-manager.ts:195](https://github.com/LodyAI/Lody/blob/d6df2f2447cf6903d71e864f12cc1519de0c1b2c/apps/cli/src/session/worktree/worktree-manager.ts#L195) 的 per-repo lock。原生等待的所有权与全机准入检查分开。不能只因私有控制传输独立即认定内部不再阻塞。

隔离 Git 包装器仅延迟回复、随后执行真实系统 Git；两个 Git 路径各三次让实际 `worker.prepare-drain` handler 在 200ms 观察窗内等待锁。中立 harness 的两个子 Agent 原生等待各三次得到相同结果，释放回复后控制均恢复。**证明额外等待与全局耦合，不把 200ms 观察窗写成永久死锁。** 单条 Git 子命令现有 10 秒预算；原生 pipe 卡住的后果不能用该预算兜底。

对齐方向：短临界区只捕获／持久准入；外部等待使用原 session／checkout 的 owner 并释放设备锁，返回后复核准确实例及接纳条件。不可仅删锁后保留共享 checkout 的竞态。

### M．高：worktree 恢复之后仍可能接纳已经排空的新输入

[server.go:1155](../internal/worker/server.go#L1155) 在接纳前调用 restoreOwnedWorktrees；缺失目录的 [worktree_lifecycle.go:103](../internal/worker/worktree_lifecycle.go#L103) 会释放锁等待 Git。这时没有 dispatch reservation；真正 reservation 在 [server.go:1199](../internal/worker/server.go#L1199) 的数据库接纳后才创建。恢复返回后没有统一重验 updateAdmission；无内存压力时 admitMemoryPressure 直接成功。

三次真实 owning-worktree 恢复流程中，在 Git add 等待时正常调用 DrainService，得到 `Ready=true`、`updating=true`；释放 Git 后原提交仍返回 accepted，并保存新操作。Lody 的 [session-execution-service.ts:4859](https://github.com/LodyAI/Lody/blob/d6df2f2447cf6903d71e864f12cc1519de0c1b2c/apps/cli/src/session/session-execution-service.ts#L4859) 在 runVisibleSessionTurn 的第一次初始化 await 前同步 reserveMachineTurn，idle drain 无法越过该 reservation。

本轮证明“封闭后仍接纳”，**没有证明历史内容已丢失**；后续可能被初始化拒绝、留作未确认或在退出中被中断，均破坏预期的准入边界。同一恢复 helper 还用于 reload、history、compact、子 Agent continue；这些入口需统一检查，不把此次普通 submit 的复现冒充其他入口均已复现。

对齐方向：第一次执行准备 await 前取得可撤销 reservation，解锁返回后再核对升级／后台停止、session／workspace 与原 owner；过期准备只清理自身，不能提交新输入。

### N．中：真实 Codex 后台命令无法进入已实现的 24 小时回收分支

[codex/work.go:246](../internal/harness/codex/work.go#L246) 在根轮结束／另一个 item 开始后，把仍存活命令提升为 background，但 [work.go:291](../internal/harness/codex/work.go#L291) 仍沿用命令的 running 状态；不会产生 idle GC 例外要求的 `background + waiting`。[store/runtime_idle.go:27](../internal/store/runtime_idle.go#L27) 因此始终判忙，[idle_runtime.go:106](../internal/worker/idle_runtime.go#L106) 的 24 小时分支到不了。现有 waiting 测试是手工写入状态，未覆盖 adapter 到 store 这条链。

Lody 的 [agent-client.ts:2717](https://github.com/LodyAI/Lody/blob/d6df2f2447cf6903d71e864f12cc1519de0c1b2c/apps/cli/src/agent/agent-client.ts#L2717) 区分 native running 与 idle 下的 backgroundTaskCount；[session-gc-manager.ts:839](https://github.com/LodyAI/Lody/blob/d6df2f2447cf6903d71e864f12cc1519de0c1b2c/apps/cli/src/lib/session-gc-manager.ts#L839) 只长期保护真正 running，background 超过上限可回收。能力不支持时 Lody 返回 null，不能声称所有 provider 都必然上报。

三次把实际 Codex Normalizer 的结构化命令／轮次事件送入真实 Worker／Store，虚拟推进 25 小时：`state=running, gcIdle=false, gcBackground=false`，环境仍保留。仅把同一记录改成 waiting 的三个阳性对照在上限后回收。**这是确定性时间推进与协议夹具，不是现场等待了 25 小时，也没有启动真实 Codex dev server。**

对齐方向：在 adapter 明确翻译“前台执行”与“轮次已结束、只等后台任务”的中立状态，保留实际活动子 Agent、未决请求与投递保护；不能把所有 running 工具无条件改成 waiting。

### O．中：worktree 清理与新归档环境退休共用串行消费者

[archive.go:21](../internal/worker/archive.go#L21) 的单个 archiveLoop 先 cleanRuntimeRetirements，再在 [archive.go:56](../internal/worker/archive.go#L56) 同步等待 cleanOwnedWorktrees 完整 sweep。Git I/O 虽释放 dispatchMu，archiveWake 却只能积压到这个 sweep 返回；另一个会话可接纳归档，但不能及时开始退休。这与 K 的“等待维护 gate”是不同根因。

Lody 的 [message-handler.ts:4824](https://github.com/LodyAI/Lody/blob/d6df2f2447cf6903d71e864f12cc1519de0c1b2c/apps/cli/src/lib/message-handler.ts#L4824) 按 session 处理归档释放，完成后 `void worktreeGc.schedule()`；[worktree-gc.ts:118](https://github.com/LodyAI/Lody/blob/d6df2f2447cf6903d71e864f12cc1519de0c1b2c/apps/cli/src/session/worktree/worktree-gc.ts#L118) 独立合并扫描，不占用会话归档消费者。

三次使用真实 archiveLoop：自建目录备份进入延迟的 Git add 后，归档另一 checkout 的会话；归档接纳成功，200ms 内其原 runtime 仍存活。释放 Git 后它才退出。**证明跨会话退休延迟，不是永久死锁，数据备份及共享保护没有失效。**

对齐方向：worktree GC 用独立、可合并且有界重试的现有后台 owner 调度；session 退休不等待整个目录扫描。仍须等准确生产者停止、分支及脏文件备份成功才能移除目录。

### 整轮覆盖与排除项

以下 32 项为实际入口／交接检查；“未发现新增差异”仅表示本轮源码对照与相邻检查未产生新证据，不是未来故障的穷尽证明。

| # | 入口／责任 | 对照重点 | 当前落点 | 结论 |
| --- | --- | --- | --- | --- |
| 01 | 独立 Host 与执行 Worker | 停止系统服务由独立 owner 接纳；Worker 不等待自己的注销 | background_darwin.go / background_stop.go | 未发现新增差异 |
| 02 | 停止意图与崩溃重启 | desiredState／停止标志先于 await；停止中不创建替代实例 | host_darwin.go / background_darwin.go | H 已修，保持 |
| 03 | 已知未执行的撤销 | 独立预算、准确实例、重试；不重发 native 动作 | background_darwin.go / background_stop.go | I 已修，保持 |
| 04 | 撤销回复丢失／迟到 | 准确完成 ID 可确认；其他 ID、drain 后拒绝重开 | background_stop.go | 已修，保持 |
| 05 | 普通崩溃策略 | 5 分钟 10 次、健康重置、指数退避；实际默认 jitter=0 | supervisor/retry.go | 未发现新增差异 |
| 06 | Host 退出／父丢失 | 生命周期通道与父身份监测，不因 GUI／SSH 生命周期退休会话 | supervisor/host_darwin.go / cmd/shioji-worker | 未发现新增差异 |
| 07 | 在线准备与候选检查 | 先准备与签名／平台／协议／数据检查，旧服务继续运行 | supervisor/host_darwin.go / probe / stage | 未发现新增差异 |
| 08 | 切换前输入初始化 | 初始化 await 前同步预留；排空后不得再接纳 | worker/server.go:1155–1199 | M：未对齐 |
| 09 | 私有排空控制 | 控制传输独立；内部不能被业务外部 I/O 锁挡住 | service_control.go / server.go / work_input.go | L：内部等待残留 |
| 10 | drained 与 writer | 登记生产者及最后写入结清后才确认，PID 退出不等于排空 | shutdown.go / private_control.go | 未发现新增差异 |
| 11 | 已提交 resume／状态核对 | 保持 applied，准确候选私有 resume；短暂 ACK 丢失持续核对 | supervisor/host_darwin.go / service_control.go | 已修，保持 |
| 12 | 候选失败与回滚 | 先退出已捕获候选再恢复兼容旧版；未知交接不盲启第二个 writer | supervisor/host_darwin.go | 未发现新增差异 |
| 13 | 停止 ACK 与真实完成 | 5 秒核对轮次、原生请求及工作；超时自动退休 | stop.go | 未发现新增差异 |
| 14 | 初始化停止／迟到 Start | 释放原 reservation、事件 gate、启动槽；晚结果不复用 | runtime_start.go / session_runtime.go | 已修，保持 |
| 15 | 强制退休与 native close | 登记 scope 与 adapter Close 并行；不先等待原生 close RPC 才发信号 | retirement.go / codex adapter | 未发现新增差异 |
| 16 | 登记进程／根已退出 | 按捕获身份与进程组；根退出仍观察登记组；失败保留票据 | managedexec/retirement_darwin.go | 接受范围内相符 |
| 17 | MCP／Node 服务准入 | 服务存活与实际 pending 分开；fence 后禁止新工具启动 | codex/executor.go / managedexec | 未发现新增差异 |
| 18 | 闲置 GC 资格 | 活动轮、操作、请求、队列、历史、原生工作保护；封闭前复查 | idle_runtime.go / store/runtime_idle.go | 主体相符；N 见后台分类 |
| 19 | 后台等待与 24h 上限 | Lody 区分 running 与 background；Shioji 真命令一直 running | codex/work.go:246–299 / runtime_idle.go | N：未对齐 |
| 20 | 内存压力 | WARNING 回收、CRITICAL 拒绝，最多 3 个、共享有界预算 | memory_pressure_darwin.go | 未发现新增差异 |
| 21 | reload 维护与强制退休 | 维护可撤销；旧 Unload／Resume 不修改继任者 | session_reload.go / session_transition.go | K 已修，保持 |
| 22 | 嵌套 setup／recovery | 解锁等待后、detach／Begin／发布前核对 owner、runtime、scope | session_runtime.go / recovery.go | J 及审阅收尾已修 |
| 23 | 历史重开／分叉／压缩 | 准确 revision／opaque 引用；提交前再验证；迟到结果受 owner 约束 | history.go / context_action.go | 未发现新增差异 |
| 24 | 子 Agent 发送与回执查询 | 原生 I/O 不应占设备全局调度边界；只查询不重放 | work_input.go:102,147 | L：两条原生等待持锁 |
| 25 | 审批／问题回复 | 准确来源与请求状态；pipe 写入仍在全局锁内 | server.go:973 / codex/runtime.go:648 | L：静态同类路径，未做 pipe 堵塞现场复现 |
| 26 | 会话／工作区归档接纳 | 先持久归档再异步释放；原生维护不挡强制退休 | archive_retirement.go / session_transition.go | K 已修；O 为调度层差异 |
| 27 | 永久删除与墓碑 | 接纳后立即移除可见记录、撤销 Start；outbox 持有物理清理 | archive.go / store archive | 未发现新增差异 |
| 28 | 失败代次后台重试 | 逻辑槽与物理回收分开；准确旧代次不能触碰新代次 | detached_runtime.go | 未发现新增差异 |
| 29 | worktree 备份与共享保护 | 真实分支、脏内容备份、detached ref；所有使用者退出后清理 | worktree_lifecycle.go | 数据保护相符 |
| 30 | worktree 懒恢复与本路径互斥 | 已有目录检查仍跨全局锁；缺失目录恢复会解锁但提交未重验准入 | worktree_lifecycle.go:93,103 / server.go | L／M：未对齐 |
| 31 | worktree GC 与会话退休调度 | Lody 独立 schedule；Shioji 同一 archiveLoop 末尾等待整个 Git sweep | archive.go:21,56 / worktree_lifecycle.go:250 | O：未对齐 |
| 32 | 旧强制 UI／网络服务边界 | 原生 UI 无旧强制页／按钮接线；旧后端模型／协议仍在；Tailnet 独立 | internal/ui / internal/app / service lifecycle | 未恢复入口；保留代码与已接受存量范围单列 |

- **旧强制页与入口**：internal/ui 没有 CloseWorkerEnvironments／WorkerRecoveryJob／WorkerMaintenance 的生产接线；底层 app model、worker 旧协议及保存的回执代码仍存在。没有把“UI 已删除”扩大为“所有后端代码已删”。本轮不重开已接受的首次旧存量手动处理范围。
- **空闲时钟附记保留**：通用 Session.Revision 会因改名／已读变化而续期，与本项目“不因查看续期”的约定仍有距离；Lody 的部分历史读取也会 touchSession，不能把它说成新发现的 Lody 升级阻塞差异。本轮没有修这项。
- **不计为新缺陷**：未登记后代、首次旧存量、真实存储写入失败、不可确认登记身份、未知原生结果继续保留；双方都要求生产者与最后写入结清才可 drained。并行 adapter Close 与 scope.Terminate 已实现；不能仅看 Close 内部串行就说强制信号被它挡住。当前所有正常生产退休 caller 都 force=true，不能用不可达的 normal→force 组合新增一项。
- **测试范围**：无 overlay 的六个生命周期包 **641 项父测试通过，零失败**；诊断共七个条件各三次（15 次父测试调用、21 个叶条件），均复现当前缺口，**不算产品行为已通过**。冻结设计六文件通过。没有重跑签名历史升级或三台正式设备；沿用的历史证据仅保留为前轮记录。

证据：[本轮复现摘要](../.ryugi/audits/lody-post-repair-full-audit-20261117/probes-summary.txt)、[六包相邻检查](../.ryugi/audits/lody-post-repair-full-audit-20261117/neighbours-summary.txt)、[源码来源与未修改证明](../.ryugi/audits/lody-post-repair-full-audit-20261117/source-provenance.txt)、[overlay 原件](../.ryugi/audits/lody-post-repair-full-audit-20261117/probes.go)。L／M 优先；N／O 同一轮补齐。无需新页面、进程选择或额外兼容框架。

## 2026-11-17 四处差异修复与最终验证

**H／I／J／K 四处已修复，本机隔离验证完成，正式设备未部署。** 产品提交为 `2b45226`；`9121455` 只修正新增归档测试的 revision 采集时序，不改产品。Lody 依据仍为 `d6df2f2` 的实际入口；保持 20 分钟／30 秒、24 小时 waiting、未登记后代范围与首次旧存量手动处理，不恢复强制关闭／重启页面。

| 差异 | 当前机制与验证 |
| --- | --- |
| H：关闭期间崩溃重建 | Host 持有停止意图，覆盖 child.done、健康观察、新更新及冷修复；成功注销后仍保持 stopped，只有准确旧实例撤销已确认或已知未执行且旧实例确已退出才恢复资格。真实签名 Worker／Host／transport 与隔离签名模拟 helper 连续三轮：helper 仍等待，原 Worker 已退出，没有替代实例，也没有新输入被接纳。 |
| I：过期撤销预算 | 原生动作只执行一次；撤销用独立 Host 生命周期和每次新 5 秒预算，失败保持 pending 并重试。实际 20 秒前置超时三轮均恢复输入，三个即时失败对照也恢复。Worker 保留最近已完成的准确取消 ID，丢回复后的同 ID 重试可确认；新操作、其他 ID、已接受 drain 均不能被旧取消重新开放。 |
| J：断连恢复未释放 | 合并到已有 clean／detach 分支，不再由早返回保留旧 Agent。解锁等待后核对实际实例；失败分离旧 scope 并交给后台回收，原结果仍未知，新实例沿原 ThreadID 继续，旧输入不重放。真实登记 cat 已停而中立 Close 失败的回归通过；不冒充真实签名 Codex 的现场 Close 故障。 |
| K：归档排在维护后 | 强制退休取代旧维护，先结清其 reload／history 回执、取消上下文并释放逻辑槽，再回收原代次；并行物理回收者仍串行，等待后重验墓碑／待办／实例。会话、工作区归档、永久删除、Stop、迟到 Unload／历史结果及恢复后的新实例保护均通过。异步 setup 内部也捕获同一 owner，在清理等待后、detach／Begin／登记前检查 context、runtime 与 scope；不只依赖外层检查。 |

### 独立审阅与收尾

一次独立整体审阅提出两个 Important：成功撤销丢回复不可确认、嵌套 setup 在外层检查前可能修改继任实例。两项均先复现失败再修复；“继任已运行／正在初始化”的维护用例失败后通过，普通初始化对照保持通过。审阅中的空目标问题按实际副作用升为 Important：空 SessionID 不能成为全会话清理过滤器；现在 recovery、reload、compact、history 均在任何回收前拒绝。没有再次派审或把旧审阅改写成修复后的无问题结论。

### 最终检查及边界

- 最终 Worker 整包 **340 项父测试通过**；worker／supervisor 的 **42 项竞态父测试通过**。相关 vet 与六份冻结设计检查通过。
- 最终双签名 Worker 历史回放 **12 APPLIED、2 既定 H07 BLOCKED**；原设备、会话、线程、历史及暂停队列保留，未知输入不重发，显式接续成功。H05 未登记后代仍按已接受边界处理，H07 不算升级成功。
- 后台控制的实际签名链路共九个场景通过：关闭中崩溃三个、真实 20 秒未执行超时三个、即时失败三个。helper 为隔离签名模拟程序，未调用 SMAppService。K／嵌套 setup 的故障注入使用中立 harness，不能说成正式 Codex 卸载挂起已现场验收。
- 完整未过滤根命令执行完毕，原始结果是正式 27 包中 25 包通过／编译，worker 的新增归档夹具及 UI 两项间距断言失败，另有三个本地研究包编译失败。夹具在接纳确认后过早取 revision；现按已有 dispatch 边界取得稳定 revision，保留原有 1 秒退休／接续断言，父例连续 10 轮通过，**整个 Worker 又完整重跑通过**。没有删除失败输出、放宽预算或把原根命令改成全绿。UI 与研究失败留给相应任务；共享分支上的其他 UI 提交不在本轮审阅／暂存范围。
- 997 个源码指纹与签名构建后比较，仅上述测试文件变化，非测试产品源码没有变化。实际嵌入 Revision 为 `2b45226+modified`，没有冒称后来测试／文档提交的版本。协议、schema、签名校验、writer 所有权、独立 Tailnet 与共享 worktree 保护没有放松。

证据：[最终整包](../.ryugi/audits/lody-direct-repairs-20261117/worker-final-summary.txt)、[竞态](../.ryugi/audits/lody-direct-repairs-20261117/race-summary.txt)、[签名后台控制](../.ryugi/audits/lody-direct-repairs-20261117/signed-background.txt)、[历史回放](../.ryugi/audits/lody-direct-repairs-20261117/historical-summary.txt)、[原根失败范围](../.ryugi/audits/lody-direct-repairs-20261117/root-summary.txt)、[构建来源](../.ryugi/audits/lody-direct-repairs-20261117/source-provenance.txt)、[完整裁定记录](../.ryugi/audits/lody-direct-repairs-20261117/progress.md)。下方“四处待对齐”及各轮数字均为当时审计记录，当前以本节为准。空闲时钟的项目约定附记仍单列，未将其宣称为本轮已修问题。

## 2026-11-17 直接对照 Lody：完整入口清单与当前差异

本轮只比较已实现的调用链。Shioji 文档基线 `40c4689`，产品代码仍为 `f68aad1`；重新 fetch 的 Lody 为 `d6df2f2447cf6903d71e864f12cc1519de0c1b2c`。相对上一轮 `58e33ce` 的上游变化只涉及 CI；保存的 15 份相关源码与上一轮生命周期内容一致。不是用较旧工作目录冒充最新上游。

**修复前确认的四处会造成阻塞或回收延迟的待对齐项：H、I 为上一轮未修；J、K 为本轮新增。没有修改产品代码。** 267 项原有相邻父测试通过（含子测试共 381 项，五包）；这不能抵消故障探针暴露的缺口。

### J. 高：断连恢复的早返回漏掉现有的逻辑释放

[recovery.go:143](../internal/worker/recovery.go#L143) 在存在 managed scope、旧 runtime 不再观察原会话时，先清理旧环境。失败后只返回“旧工具尚未确认停止”，没有进入第 157 行已有的 `detachFailedRuntime`。因此旧实例仍占会话映射，状态保持 unknown，不能沿新实例继续。Stop、GC、归档失败、重新加载和普通换版已有分离处理；这不是要求重建另一套清理架构。

Lody 的 [releaseVisibleTurn](https://github.com/LodyAI/Lody/blob/d6df2f2447cf6903d71e864f12cc1519de0c1b2c/apps/cli/src/session/session-execution-service.ts#L5928) 在 releaseFailure 时分离准确旧 Session 或 abandoned pending create，提交后台 release recovery 并释放执行槽；物理结果仍未知，不复用旧实例、不重放旧输入。应让本分支同样移交现有后台回收者，按准确原实例保护继任者。

隔离诊断连续三次：registered scope 的真实 `/bin/cat` 已停止，但注入中立 harness Close 错误后，`oldInstanceRetained=true scopeDetached=false status=unknown canContinue=false`。同一实例手动调用现有 detach 的三个对照均释放成功。证明的是实际 worker 分支遗漏，**不是宣称真实签名 Codex 已在现场产生该 Close 故障**。

### K. 中：归档强制退休先排在原生维护之后

[archive_retirement.go:44](../internal/worker/archive_retirement.go#L44) 在 fence／force 前取得通用 `sessionTransition`；[session_reload.go:32](../internal/worker/session_reload.go#L32) 在等待原生 Unload 时一直占有它。归档元数据虽然立即接纳，实际旧环境要等维护回复／预算结束后才开始强制回收。删除清理器也用此 gate；区别是永久删除在接纳墓碑时已经封住 scope，不能把归档探针的 `scopeStillAdmitting=true` 套到删除。

Lody 的 [releaseArchivedSessionRuntime](https://github.com/LodyAI/Lody/blob/d6df2f2447cf6903d71e864f12cc1519de0c1b2c/apps/cli/src/lib/message-handler.ts#L4852) 经历史收尾、终端／预览／子会话释放后，直接 [terminateSessionForRelease](https://github.com/LodyAI/Lody/blob/d6df2f2447cf6903d71e864f12cc1519de0c1b2c/apps/cli/src/lib/message-handler.ts#L5189) → `terminateSession(id,true)`；不取得等待原生维护操作的通用队列。它也会等待历史 flush 等自身资源收尾，不能描述成“归档任何 await 都没有”。Shioji 的 Stop 超时已能越过同一维护 gate，应复用相同的准确实例退休原则；迟到维护结果不能回填归档／已删会话或继任代次。

实际 reload 与 archive 入口连续三次：native Unload 保持未回复 300ms 时，`archiveAccepted=true runtimeStillLive=true scopeStillAdmitting=true`；释放回复后归档收敛。现有 reload 原生预算 30 秒，故本项是**额外维护等待，不是已证实的永久死锁**；没有把中立 harness 的等待冒充真实 Codex 挂起。

### 全链路对照表

表内 Lody 行号均来自固定提交 `d6df2f2` 的保存源码；Shioji 均为本轮未改动产品源码。已对齐指入口责任、准入／退休顺序与失败所有权相符，不代表实现语言、协议或平台资源完全相同。

| 入口 | Lody 实际调用与机制 | Shioji 落点与结果 |
| --- | --- | --- |
| 独立停止 owner | `local-cli-host-lease.cjs:200；daemon.ts:44`：应答后异步停止 Host；Worker 不等待自己的注销 | `background_darwin.go／background_stop.go`：已对齐主体；两个收尾缺口见 H／I |
| 停止意图覆盖重启 | `supervisor.ts:190`：先置 desiredState=stopped、换 generation、清 retry timer | `host_darwin.go:263`：待对齐 H：普通 child.done 仍重建可接纳 Worker |
| 已知未执行的撤销 | `session.ts:287；supervisor.ts:190`：收尾由独立 owner 持有，失败不永久缓存 termination | `background_darwin.go:47`：待对齐 I：Shioji 原生适配的撤销沿用过期预算且无重试；上游没有同名 macOS helper |
| 候选在线准备 | `daemon-runner.ts:495`：先准备与验证，失败保留旧 Worker，稳定 ID 不重放 | `host_darwin.go:362／probe.go`：已对齐；Shioji 保留原生签名／协议校验 |
| 更新绝对到期 | `daemon-runner.ts:733；start.ts:873`：最终切换前再次核对原 expiresAt | `host_darwin.go:712／worker/update.go`：已对齐；同 ID 不续期，迟到 drain 不越权 |
| when-idle 判定 | `start.ts；session-execution-service.ts`：看实际执行 owner 与 pending create，不等闲置工具自行退出 | `update.go／service_control.go:151`：已对齐；孤立历史状态不单独挡升级 |
| 父子私有控制 | `daemon-runner.ts:172；start.ts`：独立于业务连接的 drain／resume 与父进程丢失收尾 | `supervisor/lifecycle_darwin.go／cmd/shioji-worker/main.go:353`：已对齐；私有协议 2、准确实例与操作校验 |
| 强制回收登记进程 | `session.ts:319；process-tree-registry.ts:63`：并行主动结束 agent、active process 与 sandbox；不是先等常驻工具消失 | `retirement.go:79／managedexec`：已对齐；覆盖自有 app-server、exec-server 与登记组 |
| 失败句柄与重试 | `session.ts:287；process-tree-registry.ts:23`：失败保留准确树／句柄，下次可重试；组长退出不等于组空 | `retirement.go／detached_runtime.go／managedexec`：已对齐；不永久缓存失败结果 |
| 失败后释放执行槽 | `session-execution-service.ts:5928；session-manager.ts:2074`：detach 原 Session、后台 requestReleaseRecovery，freeTurnSlot | `recovery.go:143／detached_runtime.go:51`：待对齐 J：断连恢复早返回漏走已有 detach；其他入口已走 |
| 初始化取消与迟到创建 | `session-manager.ts；session-execution-service.ts:5931`：abandonPendingSessionCreate，迟到对象仅清旧实例 | `runtime_start.go／archive.go`：已对齐；Stop／删除撤销准确 Start、gate 与 reservation |
| 迟到回调代次隔离 | `session-manager.ts:2074；session-execution-service.ts`：只分离同一实例，旧 finalize 不能改继任实例 | `detached_runtime.go:15／work_input.go`：已对齐；不重放旧输入，不清新绑定 |
| 排空与最后落盘 | `session-manager.ts:1880；start.ts:735`：会话并行退出、尾部回调落盘后，确认登记范围才发 managed-drained | `shutdown.go／lifecycle_darwin.go／host_darwin.go:632`：已对齐；PID 消失、ACK、socket 关闭均不替代 drained |
| 候选 ready 与提交 | `daemon-runner.ts:887,1056,1096`：实际运行状态探测，先保存 selected／applied 后 resume | `host_darwin.go:537,870／health.go`：已对齐；迟确认不改写提交事实，准确候选继续观察重试 |
| 普通崩溃恢复 | `supervisor.ts:137,411,592；retry.ts`：1–60 秒退避，5 分钟 10 次，健康 60 秒；实际 jitter=0，计时从 spawn 开始 | `retry.go／start_retry.go`：已对齐普通策略；停止期间资格遗漏归 H |
| Stop 超时 | `session-execution-service.ts`：取消 ACK 后还等轮次／请求收尾，5 秒后强制释放 | `stop.go:73,120`：已对齐；已显式绕过在途 maintenance transition，归档尚未采用 |
| GC 业务资格与参数 | `session-gc-manager.ts:196,423`：20 分钟闲置；running 不回收；原生 background 保护 24 小时 | `idle_runtime.go／store/runtime_idle.go`：已对齐；扫描 30 秒为用户指定，Lody 默认 60 秒 |
| GC 活跃触点 | `message-handler.ts:8477,8508；session-gc-manager.ts`：显式 touchSession；部分历史读取也 touch | `idle_runtime.go:97／store/runtime_idle.go`：实现触点不同，不能声称 Lody 查看历史从不续期；项目自身约定另记 |
| 内存压力回收 | `session-gc-manager.ts:475`：候选排序、资格重检、失败不占成功额度，真实 running 仍受保护 | `memory_pressure_darwin.go／idle_runtime.go`：已对齐 macOS 所需机制；不引入 Linux／Loro 运行依赖 |
| 归档释放 | `message-handler.ts:4852,5189`：保存收尾后直接 terminateSession(id,true)，不等通用原生维护队列 | `archive_retirement.go:44／session_reload.go:32`：待对齐 K：进入 force 前先等维护 transition；有预算的额外等待 |
| 永久删除归档 | `message-handler.ts:4884`：删除事实与资源回收分开；拒绝迟到写回，释放旧资源 | `archive.go:117,216／store/archive.go`：主体已对齐；清理器也使用同一维护 gate，属 K；删除接纳已先 fence |
| 自有 worktree | `worktree-gc.ts:128；worktree-manager.ts`：核对归档／删除与引用，备份后删除，失败退避，按需恢复 | `worktree_lifecycle.go／store/owned_worktrees.go`：已对齐；包含本产品发现并登记的自建 worktree，不移除外部目录 |
| 安装与配置换版 | `session-manager.ts:1835；session-execution-service.ts`：空闲且无原生工作才退休旧实例，保持原历史 owner | `session_runtime.go:16／catalog_refresh.go`：已对齐；当前轮用实际进程能力，下一轮验证新目录 |
| 辅助生产者与目录 | `session-manager.ts:1855；start.ts`：会话、鉴权等辅助资源分别收尾，保留存储至尾部写入完成 | `shutdown.go:67／catalog_refresh.go／worker_maintenance.go`：已对齐职责；慢原生目录 I/O 不持全局 dispatch 锁 |


### 排除项、计时附记与证据范围

- 重试工具函数的默认抖动是 0.2，但实际 `CliSupervisor` 构造参数默认传 0；健康计时也由 spawn 路径启动。两项均不能再误报差异。通用退休虽没有在途 normal→force 升级分支，但全部产品调用都传 force=true，没有可达的该组合，不作为当前阻塞。
- Lody 的业务状态探测也参与 ready；同样要求清理和登记树消失后才能声明 drained。不能以“上游无阻塞”为由弱化真实工作保护或退出证明。
- 空闲时钟附记：Lody 在 `readSessionHistoryForRpc`／历史长轮询中确有 touchSession。Shioji 则把通用 Session.Revision 纳入 idle 观测；单独诊断显示标记完成已读、改名会重置观测。这偏离本项目“阅读不刷新执行空闲”的较严格约定，另记在 metadata-note.txt，**不算新的 Lody 升级阻塞根因，也不声称 Lody 采用这一约定**。源事件本身会修改 revision，先前“缺少原生活动续期”的疑点已排除。
- [新增两项三轮原件](../.ryugi/audits/lody-direct-alignment-20261117/probes.txt)、[267 项原有相邻检查](../.ryugi/audits/lody-direct-alignment-20261117/neighbours-summary.txt)、[准确源码与夹具边界](../.ryugi/audits/lody-direct-alignment-20261117/source-provenance.txt)、[24 个入口结构化对照](../.ryugi/audits/lody-direct-alignment-20261117/comparison.json)。六次预期失败只证明缺口，不能计为产品通过。
- H／I 原始签名证据在下方上一轮章节所列审计目录，本轮不重复执行系统类故障；993 文件哈希逐个一致，前轮签名二进制对应内容没有变化。新探针通过 Go overlay 注入，未修改产品或原有测试文件。未操作正式设备、真实 SMAppService 或 Tailnet。
- 保持 20 分钟／30 秒、24 小时 waiting、用户接受的未登记后代范围与首次旧存量手动迁移；不增加强制关闭／重启页面。已覆盖本表实际入口，未宣称数学上穷尽所有未来故障。下方各轮记录为历史证据，当前待对齐清单以本节 H／I／J／K 汇总为准。

> **上一轮复核（2026-11-17，f68aad1，本轮未修复）**：后台关闭还有两个控制交接遗漏：关闭未决时崩溃恢复会重新启动可接纳输入的 Worker；已知未执行的前置步骤耗尽预算时，撤销沿用过期 context，原 Worker 无法自动恢复准入。两处各三次复现，三个即时前置失败对照正常，111 项相邻检查通过。993 个产品／原生文件未改，正式设备未操作。前轮修复不撤回，但不能外推到这两种新时序。

## 2026-11-17 再次复核：关闭意图与撤销生命周期

### H. 高：后台关闭未决时，普通崩溃恢复重新开放准入

[backgroundControl](../internal/supervisor/background_darwin.go#L38) 只在接纳新后台操作、升级和冷修复时检查 pending；[Run 的异常退出分支](../internal/supervisor/host_darwin.go#L263) 不核对后台停止意图，在第 295 行仍调用 startSelectedRetry(..., false, "")。原 Worker 的 backgroundStopping 只属于旧进程，替代实例因此重新接受写入与输入。

[真实签名 Worker 诊断](../.ryugi/audits/lody-finalization-alignment-audit-20261117/crash-probe.txt) 三轮都记录 background=disabling、nativeHelperStillWaiting=true、inputAccepted=true。使用实际 RunWorkerSupervisor、transport 验签与临时签名模拟 helper，旧 Worker 仅在捕获准确 PID／启动身份并确认该隔离状态目录后结束。助手仍等待时，Host 按普通恢复策略启动新 Worker，真实 session.submit 已被接纳；后续注销可能再中断该输入。没有反注册真实 SMAppService。

Lody 的 [CliSupervisor.stop](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/packages/cli-supervisor/src/supervisor.ts#L190) 在排队等待退出前同步设置 desiredState=stopped、推进 generation、清除 retry timer；[daemon-runner 的 Host stop](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/apps/cli/src/commands/daemon-runner.ts#L1144) 进入此路径。最小对齐方向：关闭意图由 Host 持有并覆盖普通恢复的启动门；未决时不生成可接纳替代实例，只有明确未执行且同代恢复已确认后才恢复运行资格。

### I. 高：已知未执行的前置超时，撤销却没有独立预算或重试

[stopBackground](../internal/supervisor/background_darwin.go#L47) 的原生 callback 与第 63 行撤销请求共用 20 秒 ctx。前置步骤耗尽预算后，即使明确 NotStarted=true，context.WithTimeout(ctx,5s) 已过期，取消无法发送；第 70 行仍把操作 done。原 Worker 只能由 [cancelBackgroundStop](../internal/worker/background_stop.go#L101) 的准确操作清除标志，但此调用没有到达，也没有后台重试。

[预算诊断](../.ryugi/audits/lody-finalization-alignment-audit-20261117/deadline-probe.txt) 三轮均为 resultNotStarted=true、inputAccepted=false，真实 Worker 返回“后台服务正在关闭”；三个即时前置失败对照均正常接受输入。这里是实际签名 Worker／真实 Host 的前置故障注入，使用原有 20 秒 context，不启动 native unregister。不是已启动但结果未知的既定 fence 边界。

Lody 停止 owner 和会话退休的清理有独立生命周期；Session.terminate 的 finally 清掉本次 termination，以便失败后重试，不把失败 promise 当作永久终局。Shioji 不存在上游同名的 macOS helper，不能声称直接复制一个对应函数即可。最小修正是在现有 Host 中给准确实例／ID 的撤销独立预算及失败重试，在撤销确认或原实例已退出前保留收尾状态；继续拒绝迟到取消重新开放已排空 Worker。

### 本轮重读与验证范围

- 新 fetch 仍为 Lody 58e33ce；保存并对照 Host lease、supervisor stop/retry、daemon runner、start/drained、Session terminate/pending-create、GC、归档／删除、worktree GC／backup、原生后台完成与退休语义。检出目录的 session-manager 等文件存在较旧版本，已经用 git show origin/main 保存准确当前源码并逐条核对相关差异；没有只拿旧检出版本冒充上游。
- [111 项相邻检查](../.ryugi/audits/lody-finalization-alignment-audit-20261117/neighbours-summary.txt) 覆盖五包，包含归档／删除、自建 worktree 共享保护、备份失败重试和延迟恢复。本轮没有确认第三个独立根因，不宣称未来条件已穷尽。
- [完整计数与证据边界](../.ryugi/audits/lody-finalization-alignment-audit-20261117/summary.txt)、[源码与进程范围](../.ryugi/audits/lody-finalization-alignment-audit-20261117/source-provenance.txt)：六次预期故障不算产品通过。原探针的权限枚举和字段拼写错误已修正，最终三轮只计正确夹具。993 文件与前轮签名构建内容一致，构建内 Revision 仍为 ebc90b9+modified；不伪称重新构建版本号。
- 正式设备、系统注销与 Tailnet 未操作；20 分钟／30 秒、24 小时 waiting、未登记后代及首次旧存量手动处理不变，不增加强制页面。


> **本轮收尾修复（2026-11-17，本机隔离验证）**：下方 3648cc6 的两个审计条件已修复。已提交升级保留 applied／rolled-back；迟回复核对准确候选，暂时不确定继续观察与重试，候选退出按原崩溃策略恢复已选版本。后台注销交给独立 Host，Worker 只封住同代准入并提交短请求，客户端按稳定 ID 查询，不重放注销。正式设备未部署，真实 SMAppService 未操作；下方“尚未修复”为当时审计原件。

## 2026-11-17 两个收尾条件的修复与验证

- 提交后收尾覆盖正常升级、回滚、已提交启动和冷修复。确认失败不再进入修改升级 journal 的 failed 分支；精确匹配 PID／实例／设备／版本与 Paused=false 可独立证明已开放。确认预算后记 reconnecting，监测继续重试，真实子进程退出仍受原崩溃次数限制。
- 注销由固定 Host 先应答再异步执行，原生 helper 不由 worker RPC、worker Close 或 Host 的排空等待拥有。Worker 的 maintenance／dispatch 只覆盖准入准备；Host 执行前核对同一操作与实例。明确未启动撤销原 scope 准入，未知结果保持 fence，已接纳 drain 后拒绝迟到取消。
- 协议 48 增加 typed BackgroundStopInput 和独立后台回执查询；旧同步 RPC 在副作用前拒绝。客户端在发送前分配 ID，丢回复后只查询；Host 消失时核对系统注册状态，不能通过另一个 owner 重放原注销。
- [恢复回归](../.ryugi/audits/lody-finalization-fixes-20261117/resume-green.txt)、[签名 Worker／Host／模拟 helper](../.ryugi/audits/lody-finalization-fixes-20261117/background-green.txt) 与 [Host 幂等及撤销](../.ryugi/audits/lody-finalization-fixes-20261117/background-control.txt) 均通过。模拟助手等待 Host 退出的分支约 37ms 交出 drained，没有新的 shutdown blocked；这是模拟时序，不是真实系统注销测量。
- 50 项相邻与 48 项竞态检查通过。最终双签名构建 [历史双轮回放](../.ryugi/audits/lody-finalization-fixes-20261117/historical-summary.txt) 为 12 APPLIED、2 既定 H07 BLOCKED；原线程接续、历史及队列保留，未登记后代边界不变。最终 [993 文件源码快照](../.ryugi/audits/lody-finalization-fixes-20261117/replay-build.json) 与重建 SHA 证明对应关系，355 个 UI 文件未变。
- 全仓检查的 UI、本地研究与额外一次签名子进程启动超时全部在同主题报告单列；不能把单独复测通过解释为根因修复或全仓全绿。20 分钟／30 秒、24 小时 waiting 与首次旧存量手动处理保持，不增加强制关闭入口。


核查日期：2026-11-17。结论用于 Shioji 本轮升级改造。用户已决定直接采用 Lody 的机制，接受其未登记进程覆盖范围，不再增加独立会话宿主或完整后代追踪架构。以下参考源码分析在实施前完成；实施现状见下段，三台正式设备没有停止或更新。

> **修复后继续复核（2026-11-17，两个条件尚未修复）**：3648cc6 对照 Lody 最新 58e33ce，再确认两个控制流程收尾问题：resume 已执行而确认迟到，仍会把已提交升级永久写为 blocked；后台注销 helper 与宿主退出重叠时，worker 持锁及请求等待会妨碍自己的排空。三个失败场景各三次，共 9 次预期失败，另有 6 次正向／诊断对照；37 项既有相邻检查通过。988 个产品文件未改，正式设备未操作。上一轮私有通道、前置失败等五项修复仍保留原适用范围，不能外推为所有异步收尾均已覆盖。

## 2026-11-17 修复后复核：两个控制流程收尾问题

### F. 高：确认迟到仍能把已提交并接纳的升级封成 blocked

[host_darwin.go:850](../internal/supervisor/host_darwin.go#L850) 在 resume 返回错误后仍执行 failed(err,true)，其闭包将此前 applied／rolled-back 改写为 blocked；[resume](../internal/supervisor/host_darwin.go#L869) 的单次预算为 1 秒、总预算为 10 秒。重试补上了短暂端点缺失，但回复超时仍不能证明恢复接纳没有执行。进入 blocked 后，monitor 不再核对该 child，新更新也被拒绝。

完整 host 隔离流程使用既有 holdresume 进程夹具，让恢复接纳完成后应答延后：三次都观测到 selected=next、phase=blocked、liveCandidatePaused=false、accepted=true。候选确实已经接受工作，控制流程却持久封住后续升级。此整流程夹具走原公共协议 0 的兼容路径，不能说成实际签名版本 2 的慢回复升级验收。另用真实 lifecycleClient／ServeLifecycle 的私有版本 2 注入迟回复，三次都证明 opened=true 与调用超时可以同时成立；该诊断本身通过，不将正确的超时返回单独当作缺陷。缺陷在调用方将未确认当成终局失败；相同 h.update 收尾也用于私有 resume。

Lody 的 [daemon-runner.ts:1090](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/apps/cli/src/commands/daemon-runner.ts#L1090) 先持久保存 selected／applied，再发 child IPC resume；send 错误只记日志，不把已提交事实覆盖成 blocked。这里不声称 Lody 对所有 IPC 故障都会自动重试。对齐应保留提交事实，并按准确候选核对是否已开放接纳，再决定后续观察／重试；没有确认不等于没有执行。

### G. 高：后台注销 helper 与宿主退出重叠，会让新 shutdown 记录进入 blocked

[background_stop.go:15](../internal/worker/background_stop.go#L15) 持 maintenance 与 dispatch，直到 [callback 返回](../internal/worker/background_stop.go#L75)；生产 callback 经 [main.go:408](../cmd/shioji-worker/main.go#L408) 调用独立系统 helper。宿主关闭需要的 DrainService 同样需要 dispatch。与此同时，[shutdown.go:43](../internal/worker/shutdown.go#L43) 等待的请求 goroutine 仍在等 helper；所以只把 callback 移出 dispatch，还不足以消除请求与自身停机的循环等待。

先用真实 cat helper 验证锁边界，三次均为 ownerDrainBlockedByHelper=true。再走实际签名 worker、真实 supervisor.Run、真实 transport 目录／签名／bundle 身份检查，在临时 HOME 内使用自建签名 helper 模拟「服务操作未返回，宿主已经开始退出」。三次都在约 7 秒后得到 worker exited without a drained receipt，持久操作 phase=blocked；同一链路只改为 helper 立即返回，三次均正常退出。新记录为普通 shutdown-*，不是已接受手动处理的 H07 首次旧存量。

该 helper 是隔离测试程序，没有调用 SMAppService。本轮证明的是上述相交时序可以破坏真实 host／worker 排空，不声称每次 macOS unregister 必然等待宿主退出，未验证正式系统注册操作的具体时长。真实 SMAppService 或三台设备的现场操作不在本轮证据中。

Lody 的 [daemon.ts:44](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/apps/cli/src/commands/daemon.ts#L44) 向独立 Host 请求停止；[local-cli-host-lease.cjs:200](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/packages/shared/src/node/local-cli-host-lease.cjs#L200) 先结束接纳应答，再异步触发 Host 关闭。对齐点是把系统服务停止的执行与等待交给独立控制者，worker 只负责接纳、隔离新输入和准确排空；不能让被停止的 worker 同步等自己的终止流程返回，也不需要恢复强制环境页面。

### 本轮证据、排除项与边界

- [最终三轮原件](../.ryugi/audits/lody-post-handoff-audit-20261117/probes.txt)、[计数及夹具边界](../.ryugi/audits/lody-post-handoff-audit-20261117/summary.txt)：两个条件，三个失败场景各三次；私有超时诊断和即时 helper 对照各三次通过。失败不计为产品通过。
- [37 项相邻检查](../.ryugi/audits/lody-post-handoff-audit-20261117/neighbours-summary.txt)：无 overlay，覆盖现有 Host、私有通道、已提交启动、上一轮五项修复和后台停止；没有把旧全仓结果当成本轮重新执行。
- [源码与进程范围](../.ryugi/audits/lody-post-handoff-audit-20261117/source-provenance.txt)：当前 988 个产品文件哈希未变化；签名 worker 的 SHA 与上一轮最终构建一致，Lody fresh fetch 仍为 58e33ce。没有修改 cmd／internal／go.mod／go.sum。
- 已排除：Lody 对 switching／observing 等未完成切换重启也会 blocked，与本项目一致；本项目已监测真实父进程丢失，不将缺少字面 disconnect 回调直接说成宿主崩溃后永久遗留。Lody 独立 subagent cancel 也只等待原生取消请求，未把本项目独立 child ACK 路径冒充新的 Lody 差异。
- 原有 20 分钟／30 秒、24 小时 waiting、未登记后代、首次旧存量手动迁移保持。测试初版有一只模拟 helper 因临时目录提前删除而漏读释放标记，已按捕获的准确 PID／启动时间／路径清理；最终夹具等待准确 helper 退出再删目录，三轮不需要补杀。此夹具问题未列作产品发现。

> **五处交接修复（2026-11-17，本机验证，正式设备未部署）**：删除 pending Start、成功退休的旧工作所有权、后台关闭的明确前置失败、私有 resume、目录恢复的全局锁五处均已有修复及回归。38 项并发检查通过；最终签名源码两轮历史重放为 12 次 APPLIED，2 次既定旧状态 BLOCKED，没有把后者算作升级成功。下方“尚未修复”段落为修复前审计原件，当前状态以本节为准。

## 2026-11-17 五处交接修复与验证

| 交接点 | 最终机制 |
| --- | --- |
| 永久删除 pending Start | 墓碑提交后撤销准确 Start 的 context、事件 gate、runtimeStarts 与 dispatch reservation；不再写回已删会话。迟到启动失败只为已提交墓碑幂等返回，真实缺失／存储错误仍按原保护处理。原生晚到结果继续由已捕获 scope／outbox 清理。 |
| 成功退休旧工作 | 通用退休等待成功后，在 dispatch 下复查准确 runtime.Done 与 scope generation，再 RetireWork；覆盖普通换版和归档 needsRelease=false。失败仍保留 unknown，迟到旧退休不能结算继任实例的工作。 |
| 明确未执行的后台关闭 | BackgroundService.NotStarted 只由 helper 启动前的确定结果声明，含真实验签前置失败与 exec.Start 失败；worker 恢复原准入。helper 已启动但响应无效／未知时继续保持 fence，不重发关闭。 |
| 已提交候选恢复接纳 | 私有控制升级至版本 2，新增 resume。准确候选的 instance／operation 与 drain intent 设备身份共用校验；resume 幂等且不关闭通道，已接纳 drain 后拒绝迟到 resume。只在原 10 秒预算内重试短暂失败，保留旧协议兼容回滚的公共路径。describe 与实际握手声明一致，避免旧协议误把 resume 当 drain。 |
| 目录恢复与设置原生 I/O | recoveryRuntime、手动重检及设置刷新在 capture／mark 后释放 dispatch，再等待原生 Close／Start；发布时重新核对更新准入，排空后到达的目录受有界清理且保留准确控制器。保留每个 harness 的 catalog gate；恢复不递归获取已有 maintenance RLock。 |

没有增加强制关闭页面、进程选择入口、独立宿主或全后代扫描。保持 20 分钟／30 秒、24 小时 waiting、未登记后代以及首次旧存量手动处理的既定范围。PrivateControl 版本保护是正常候选／兼容回滚的 wire 边界，不是恢复旧存量迁移兼容。

验证证据保存在 `.ryugi/audits/lody-handoff-fixes-20261117/`：`worker-red.txt`、`helper-red.txt`、`resume-red.txt`、`catalog-red.txt`、`resume-guard-red.txt` 为修复前失败；`lifecycle-race.txt` 的 38 项父测试在 worker、supervisor、transport、shioji-worker 四包通过，包括真实签名子进程、真实 Node 和 SQLite。前置失败测试曾在接纳后立即重试，真实待发输入仍活跃，现明确等待已接纳操作完成后再核对关闭重试，没有放松活动输入保护。

最终两个签名构建的 PrivateControl=2、schema=15；`historical-replay.txt` 与 `historical-summary.txt` 覆盖全部 14 次隔离历史重放，同设备、会话、原线程与历史保持，暂停队列不重放，显式接续成功。H05 未登记后代按已接受范围存活至夹具准确清理；H07 缺失旧接纳证明继续 BLOCKED。本轮没有正式安装、反注册或三台设备部署。988 个产品与测试文件在构建后哈希未变化，详见 `replay-build.json`。

全仓验证：CI 命令 `go test -timeout 20m ./...` 已完整执行。27 个正式包中，26 个通过／编译，UI 包的 `TestWorkspaceHeadingSharesTheWorkspaceRowVisibleEdges` 与 `TestWorkspaceHeadingInsetsAndNoPerWorkspacePlus` 两项既有间距断言失败；全部 355 个 UI 源文件与未改动基线完全相同。根命令还被本地 build 研究快照的未定义类型／重复 main 阻断，详见 `full-suite-summary.txt` 与 `root-suite.txt`。最终六个生命周期相关包全部通过；没有过滤失败项或宣称全仓全绿。首次生命周期整包 CLI 启动检查超时，单独复现又发现实际握手仍声明版本 1；统一至版本 2 后，签名子进程与最终根测试中的 CLI 包通过。

> **继续深查（2026-11-17，尚未修复）**：在前述三处之外，再确认两处：提交新版后的 resume 仍仅走业务 socket，短暂端点缺失会永久转为 blocked；自动恢复模型目录时仍在原生 Start／Close 期间持有全局 dispatchMu，让闲置排空等待。两种故障各三次复现，签名 worker 与后台目录刷新对照各三次通过，另有 55 项既有相邻检查通过。当前共有五处已确认待修机制，产品代码及正式设备均未改动。

## 2026-11-17 继续深查：再确认两个机制差异

### D. 高：已提交候选的恢复接纳没有接入私有通道

[host_darwin.go:869](../internal/supervisor/host_darwin.go#L869) 的 resume 仍只调用公共 worker.sock。正常更新在选中版本及 applied 都落盘后调用它，一次调用失败便由 [host_darwin.go:850](../internal/supervisor/host_darwin.go#L850) 将操作改写为 blocked；重启恢复已提交版本的分支也只尝试一次 resume。继承的私有控制目前只能 drain，尚不能恢复接纳。

隔离完整 supervisor 流程只在 Select 回调中把已 ready 候选的业务 socket 临时改名：旧 worker 已正常排空，新版已被选中，却得到 selected=next、phase=blocked。恢复同一 socket 后仍 blocked、accepted=false，新的更新也被拒绝。三次一致。不是删除 worker 本身或破坏候选身份。

真实 ad-hoc 签名 worker 对照进一步确认：同一 paused 候选带 PrivateControl=1 启动，缺失公共端点时 resume 失败；恢复端点后同一候选可以 resume，ready 的 paused 变为 false；再次移除公共端点，继承私有通道仍正常完成准确 drain。因此问题是恢复接纳仍依赖业务端点，不能用此前「私有 drain 已脱离业务 socket」的通过结果覆盖。

Lody 的 [daemon-runner.ts:1090](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/apps/cli/src/commands/daemon-runner.ts#L1090) 在保存 selected 与 applied 后，通过已捕获 child 的 IPC 发送 lody/managed-resume；[start.ts:825](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/apps/cli/src/commands/start.ts#L825) 验证 supervisor token 后开放轮次准入。对齐方向是把准确候选的恢复接纳也纳入私有生命周期控制。提交后短暂控制失败的收尾须保留已提交事实并可核对，不能仅凭一次业务端点错误把健康候选封死。Lody 对 IPC send 错误本身只记日志，这里不声称上游保证所有通道故障自动重试或绝不失败。

### E. 中：自动恢复目录仍跨原生 I/O 持有全局调度锁

[auto_recovery.go:64](../internal/worker/auto_recovery.go#L64) 获取 dispatchMu 后，逐个调用 recoveryRuntime → openCatalog → startCatalog；[catalog_refresh.go:65](../internal/worker/catalog_refresh.go#L65) 内有 previous.Close、adapter.Start 等原生等待。[service_control.go:160](../internal/worker/service_control.go#L160) 的私有 drain 和公共 drain 都需同一锁，因此控制通道独立并不能消除这个内部等待。

中立 harness 仅暂停目录 Start，真实 Server／SQLite 的 businessStopReady 已为 Ready=true，DrainService 却不能进入；释放该 Start 后立即完成排空，三次一致。目录任务不是已接纳的业务轮次，也不是 resident Node 本身。相反，现有 catalogLoop 在标记 refreshing 后释放 admission 再做原生 I/O，同样等待时，prepare-drain 可以立即返回；对照三次通过。

Lody 的 [machine-turn-admission.ts](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/apps/cli/src/session/machine-turn-admission.ts) 同步核对轮次 reservation；异步运行时准备由 [managed-runtime-update-coordinator.ts](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/apps/cli/src/agent/managed-runtime-update-coordinator.ts) 独立推进。这是 Shioji 额外目录恢复路径没有贯彻短临界区原则，不冒充上游存在同名 Go catalog。应复用本项目 catalogLoop 的边界，仅在 capture／publish／复查时占锁。

本轮证明的是额外阻塞，不是无限死锁。恢复启动分配 20 秒上下文；没有把故意不遵守取消的假 harness 当作真实 Codex 永久卡死证据，也没有从短探针推导未来故障已被穷尽。

### 继续深查的证据与范围

- 基线 `7c97df8`，新 fetch 的 Lody 仍为 `58e33ce`；[源码范围核对](../.ryugi/audits/lody-mechanism-deep-20261117/source-provenance.txt) 中 393 个生命周期文件哈希未变，所以上一轮三处仍未修复。
- [原始探针输出](../.ryugi/audits/lody-mechanism-deep-20261117/deep-probes.txt)：两种预期断言失败各三次，签名 worker／后台目录正向对照各三次通过。失败不计作产品通过记录；[计数与边界](../.ryugi/audits/lody-mechanism-deep-20261117/audit-summary.txt) 已逐项核对。
- [55 项相邻检查](../.ryugi/audits/lody-mechanism-deep-20261117/aligned-neighbours.txt)：supervisor 与 worker，覆盖正常提交、回滚、expiry、公共／私有 drain、目录后台刷新、归档回收、自建 worktree 和删除。没有套用上轮 124 项的旧结果。
- 本轮还重读了准确实例退休、归档释放、worktree 共享保护与异步 Git 清理、pending Start 取消及历史 Inspect 的锁边界；这些路径在本轮没有确认第三个新的独立根因。前述三个未修交接点继续单列，未把其派生现象重复计数。
- 没有正式安装、反注册、远程设备操作或新增运行环境入口。20 分钟／30 秒、24 小时等待保护、未登记后代和首次旧存量手动处理仍按既定决定。

> **追加复核（2026-11-17，尚未修复）**：当前 `43ec93f` 对照 Lody 最新 `58e33ce`，又确认三处交接遗漏：永久删除没有撤销 pending Start、成功退休的部分分支没有释放旧 unknown work 所有权、后台关闭在明确的前置失败后仍永久封住准入。五个具体场景各重复三次，共 15 次预期断言失败，另有 124 项既有相邻检查通过。本轮只审计和隔离复现，产品代码及三台正式设备未改动；不能将此前五项修复外推为所有交接路径均已覆盖。

## 2026-11-17 追加复核：三个仍未对齐的交接点

| 优先级 | 遗漏与用户后果 | 根因与最小对齐方向 |
| --- | --- | --- |
| 高 | 删除正在初始化的会话后，升级仍等待已接纳轮次；初始化随后失败还会取消整台 worker | 删除只关闭 runtime gate，没有撤销 runtimeStarts、原 Start context 和 dispatch reservation；迟到错误进入 failRuntimeLaunch，对墓碑后的 operation/session 读写失败执行 s.cancel。应像现有 Stop 初始化及 Lody abandonPendingSessionCreate 一样先撤销逻辑所有权，迟到结果按准确实例与墓碑收尾。 |
| 高 | 关闭后台服务在 helper 启动前就签名校验失败，但之后无法发送，也无法重试关闭 | backgroundStopping 在 callback 前置 true，只有 err=nil 的部分结果会复位。真实 transport 的验签失败明确没有执行 unregister，却被当成未知已投递保留 fence。应区分明确未执行与结果未知，只对后者保持既有保护。 |
| 中 | 旧实例已成功关闭，归档恢复或换版后的新实例仍被旧 unknown work 永久否决 GC | cleanRuntimeRetirement 的 needsRelease 只计算 starting/running/waiting，成功的普通替换清理也未调用 RetireWork；只接入 GC、失败分离、ReleaseRuntime 的退休标记没有覆盖全部成功关闭路径。应按准确已停止实例统一退休旧工作所有权，保留真正未知的历史结果。 |

### A. 永久删除与 pending Start 的交接

证据位置：[删除事件门](../internal/worker/archive.go#L173)、[初始化收尾](../internal/worker/session_runtime.go#L198)、[失败写回](../internal/worker/session_runtime.go#L249)。隔离中立 harness 暂停原生 Start，提交真实归档及删除事务。删除已落墓碑时仍为 `starts=1 reservations=1`，`businessStopReady` 拒绝；释放原 Start 并让其返回初始化错误后，三轮均取消整台 worker。先通过已有 `stopInitializing` 撤销准确初始化再删除的对照，三轮保持 worker 可用。

这与上轮已修复的「runtime 已创建、卡在 Create」不同：这次卡在更早的 Adapter.Start；上轮墓碑幂等只覆盖 InterruptCompletion，没有覆盖 failRuntimeLaunch 的迟到写回。标准提交的 Start 等待有 20 秒上下文预算，不能将本轮观察夸大成一定无限等待；但墓碑已提交后逻辑租约仍未立即释放，迟到失败取消设备 worker 的后果已经复现。

Lody 参照为 [session-manager.ts:470](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/apps/cli/src/session/session-manager.ts#L470)：从 pending map 移除、取消创建控制器，迟到成功分离并清理准确实例，迟到拒绝不再占用该创建。其 [pending create 终止分支](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/apps/cli/src/session/session-manager.ts#L578) 也有有界等待后移交。这里比较已读的生命周期机制，不声称在 Lody 正式 UI 重现了完全相同的永久删除场景。

### B. 成功退出后的旧工作所有权

证据位置：[归档退休](../internal/worker/archive_retirement.go#L62)、[通用关闭完成](../internal/worker/retirement.go#L121)、[下一代创建](../internal/worker/session_runtime.go#L107)。两种隔离路径都运行真实注册的 cat 进程，取得旧 scope VerifyStopped 成功证明，然后沿同一会话和线程创建新实例、完成新轮次。旧 work 仍为 `State:unknown Retired:false`，GC 返回 `Idle:false`；只对已证明停止的旧工作调用现有 RetireWork，对照立即为 Idle=true。归档／恢复和换版清理各三轮相同。

换版探针为保留准确停止证明，先受控调用与自动换版相同的 cleanSessionRuntime 清理入口，在下一次 Begin 覆盖 scope 记录前核对 VerifyStopped，再走正常接续。不是正式设备安装验收。Lody 的 [detachSession](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/apps/cli/src/session/session-manager.ts#L2074) 会移除准确实例并发出 native work 清空事件，GC 查询当前活实例；旧历史 unknown 不继续替后继实例持有运行时。

### C. 已知未反注册的前置失败

证据位置：[后台关闭](../internal/worker/background_stop.go#L70)、[transport 前置验签](../internal/transport/background_darwin.go#L100)。使用临时 HOME 内的无效 Runtime.app 调用真实 DisableRuntimeService，验签失败发生在 runRuntimeHelper 之前，没有触达正式安装、SMAppService 或真实系统反注册。三轮均留下 `stopping=true scopeAdmitting=false`；后续输入得到「后台服务正在关闭」，再次关闭不会到达 callback（调用数始终为 1）。

上轮恢复的是「unavailable 且 err=nil」的已知非操作；这次是「明确尚未投递但 err!=nil」，不能混用未知结果的 fail-closed 规则。Lody 对照的是 [daemon-runner 的 prepare／校验阶段](https://github.com/LodyAI/Lody/blob/58e33cec245b2f5e7586d0c7beb6a02ce148219c/apps/cli/src/commands/daemon-runner.ts#L470) 与实际排空的分界：准备校验失败不等于已经进入关闭。Lody 没有 Shioji 这一 SMAppService callback；本项是本地停止准入回滚缺陷，不冒充上游有完全同名接口。

### 本轮范围、对照和证据

- [完整失败输出](../.ryugi/audits/lody-mechanism-post-fix-20261117/worker-probes.txt)、[探针源文件](../.ryugi/audits/lody-mechanism-post-fix-20261117/worker_postfix_test.go)、[overlay](../.ryugi/audits/lody-mechanism-post-fix-20261117/worker-overlay.json)：五个失败场景各三次；没有把预期失败计为通过。
- [审计计数与边界](../.ryugi/audits/lody-mechanism-post-fix-20261117/audit-summary.txt)：15 次失败；初始化先分离的三次对照通过，六次成功退休后的 work 结算对照恢复 Idle=true。
- [124 项相邻检查](../.ryugi/audits/lody-mechanism-post-fix-20261117/aligned-neighbours.txt)：worker、store、supervisor、managedexec 四包通过，包括停止超时、私有通道半帧／重试、归档 gate、自建 worktree 与原 GC／失败退休路径。它们不包括本轮 overlay 失败探针。
- [源码与上游版本](../.ryugi/audits/lody-mechanism-post-fix-20261117/source-provenance.txt)：393 个生命周期文件哈希未变化；Lody 4f25bd5→58e33ce 的新增提交未改本次 CLI／supervisor／node 进程核心。没有新增正式运行事件或远程部署证据。

20 分钟／30 秒、24 小时原生 waiting 保护、未登记后代和首次旧存量手动迁移继续按既定决定；Loro Wasm、模拟器、Web 终端和逐轮 MCP selector 的技术栈／功能差异未冒充本项目缺陷。这里确认三处，不承诺没有其他未来环境故障。

> **本轮对齐修复（2026-11-17）**：五处遗漏均已修复，产品提交为 `f3f26d0`、`57e0083`、`0cfc582`、`9e5b33d`、`206b0e8`；验证追加修复 `ee93539` 使 Codex 启动失败返回真正空的 Runtime 接口，避免取消后的清理崩溃。每项均有修复前失败和修复后通过的回归；私有通道还用真实签名 worker 验证一次超时后在公共 socket 缺失时仍可重试并取得准确退出回执。完整验证和历史回放见同主题报告。本轮未停止、更新三台正式设备。

## 2026-11-17 五处修复的最终机制

| 原遗漏 | 最终行为 |
| --- | --- |
| 删除后旧 Done 取消 worker | InterruptCompletion 事务识别已提交墓碑并幂等返回；没有墓碑的真实缺失仍返回错误。 |
| 删除 gate 未释放 | deactivate 总是关闭准确 gate.done 且只关闭一次；删除释放原生等待、dispatch reservation 与配置读锁，后台继续负责物理清理。 |
| 旧 unknown work 阻止后继 GC | worker 自有 Retired 区分历史结果与执行所有权；已证明停止才写 stopped，失败分离与启动失效保留 unknown 并释放旧所有权；新 active 重新获取，当前 unknown 仍阻止 GC。 |
| 一次超时销毁私有通道 | 每个 child 保留一个有界 reader/writer；单次超时仅结束等待，AttemptID 隔离迟到或半帧回复，AdmitBefore／ExpiresAt 与持久 intent／准确回执仍控制退出。 |
| 闲置 Node 阻止关闭后台服务 | PauseAdmission 可撤销地暂停新工具启动；PrepareWorkerStop 保留真实活动轮、原生工作和登录检查；已知反注册未操作恢复原 runtime，受管常驻组留给正常退出。 |

完整验证另发现直接归档／删除的命名测试夹具缺少 dispatch 同步，修正于 `359c152`，同一 race 用例重复 50 次通过；没有放松生产 revision 校验。独立只读最终审查未发现 Critical／Important／Minor；正式部署、未登记后代和首次旧存量迁移不在本轮声明中。`docs/runtime-idle-reclamation.md` 已修正为实际 20 分钟／30 秒与 24 小时原生 waiting 保护期。以下五项保留为修复前审计，描述当时的故障，不代表当前仍未修复。

## 2026-11-17 补齐后再核对：五项遗漏（修复前原始证据）

源码基线为 `1687e46`（完整提交见原始 provenance）；Lody 网络 fetch 后为 `4f25bd5`，此前为 `eff59dc`。两者之间新增的七项提交没有改动本次检查的 CLI 升级、停止、GC 与释放实现，不能将以下差异归因于本轮刚新增的上游机制。

本轮使用 Go overlay 增加隔离探针，没有修改 `cmd/`、`internal/`、`go.mod` 或 `go.sum`。Worker 探针使用真实 Server、SQLite、托管组与中立 harness；关闭后台服务的案例还运行真实 Node 父子登记组。控制通道探针使用实际 Unix socketpair 与同一 lifecycleClient/ServeLifecycle。这里不是三台正式设备现场重现，也不是新版部署验收。

### 1. 高：删除会话的旧实例退出可能取消整台设备的 worker

位置：[auto_recovery.go](../internal/worker/auto_recovery.go#L24) 的 `watchRuntime`。永久删除先移除 Session，但旧 runtime 在物理 Close 完成前仍留在 map。其 Done watcher 抢先执行时，当前实例核验仍能通过，`InterruptCompletion` 却因会话已经删除返回 `sql.ErrNoRows`，随后触发全局 `s.cancel()`。

这会停止整个 worker 的后台循环并拒绝后续连接，不只是释放已删会话。CLI 的 Serve 返回后还会进入整个服务的 Close。Lody 的永久删除／归档会先撤销该会话的完成等待、释放该实例，不把已经删除的对象当作设备级持久化故障。

探针让准确旧实例先关闭 Done、稍后才完成 Close，三轮都得到 `exit of deleted runtime canceled the entire owning worker context canceled`。修正应使已提交删除后的准确旧实例退出成为幂等收尾，同时保留真正数据库错误的故障处理。

### 2. 高：永久删除没有释放等待原生调用的 gate，仍可阻塞升级

位置：[archive.go](../internal/worker/archive.go#L172)、[detached_runtime.go](../internal/worker/detached_runtime.go#L21) 与 [server.go](../internal/worker/server.go#L1311)。删除只执行 `gate.Store(false)`，没有关闭 `gate.done`。之后即使调用 deactivate，Swap 已经是 false，也不会再关闭该 channel。

dispatch 正在等不可及时返回的 Create／Submit 时，只监听调用结果和 gate.done；它仍持有 dispatch reservation 和 toolConfig 的读锁。永久删除已经成功，也不能让这段等待退出，when-idle 升级仍会认为存在已接纳执行。Lody 的逻辑释放要求旧实例不再占用轮次／执行所有权，物理失败另行保留重试。

隔离的未及时返回 Create 三轮均为 `eventGate=false dispatchReservations=1`，随后 gate.done 等待未释放。修正要统一完整的 gate 退休语义，不能只禁用后续事件；这不等于伪造原生调用已经物理结束。

### 3. 中：GC 成功后残留 unknown work，会永久否决后继实例的 GC

位置：[idle_runtime.go](../internal/worker/idle_runtime.go#L150)、[runtime_idle.go](../internal/store/runtime_idle.go#L22)。Codex 的 Invalidate 将 waiting 后台工作改成 unknown；`settleReclaimedBackground` 只处理仍为 waiting 的条目，漏掉已被失效事件改写的旧 work。

探针先核对旧登记代次 `VerifyStopped` 成功，再在原会话／原历史创建新 runtime 并完成新轮；此时旧 work 仍为 unknown，`RuntimeGCIdle` 返回 `Idle:false Background:false`。只将已证实停止的旧 work 改为 stopped，资格立即变为 Idle=true。三轮结果相同。Lody 的 GC 读取当前仍存活实例的 native work，移除旧实例／活动跟踪后，旧报告不会永久否决后继实例。

修正应在准确代次物理回收确认后，终结该代次的活动投影，保留历史结果不确定性，不能把它作为新实例一直忙碌的依据。

### 4. 高：私有生命周期通道一次超时后不可再次使用

位置：[lifecycle_darwin.go](../internal/supervisor/lifecycle_darwin.go#L48)。单次 request 的超时回调和读取错误都会 Close 整条继承连接，worker 的 ServeLifecycle 也随之结束。上一轮新增 admitBefore 避免了迟到接纳，却没有恢复此后独立控制的可用性。

三轮中，正常拒绝后第二个请求均成功；只将首次 handler 延迟到超过请求期限，第二次就固定失败为 `use of closed network connection`，handler 只收到一次调用。旧 worker 的生命周期没有结束，但专用通道已失效，只能回退业务 RPC。**若业务 RPC 同时不可用，后续重试无法借这条通道恢复**，这是结合 shutdown 调用链得出的后果；本轮没有把它冒充正式设备升级现场。

Lody 的 shutdown 请求通过进程级 child IPC 发送，单次尝试不以普通应答等待的超时销毁整个 IPC。修正需要分开单次接纳尝试与通道寿命，并保留消息／操作关联、绝对期限及已接纳 intent 的核对。

### 5. 中：关闭后台服务仍要求常驻 Node 先自行退出

位置：[background_stop.go](../internal/worker/background_stop.go#L53)。设备“登录后启动服务”关闭流程仍逐个调用旧 `Scope.FenceIfIdle`；其原进程存活判据尚未移除。当前 managed worker 的正常更新已经通过 RequestUpdate 走新 supervisor；`update.go` 的旧入口属于既定首次迁移边界，本轮不将它重复计为缺口。

真实登记 Node 父子组下，会话 completed、HasActiveWork=false，三轮均返回 `仍有后台进程，结束后更新`，反注册 callback 尚未调用；只先结束这组已知测试进程，对照就能进入 callback。没有执行本机真实系统服务反注册。Lody 的 daemon stop 由准确 Host 请求排空并终止受管范围，不要求 resident Node 自行消失。

修正应保留真实活动轮、登录和准入核验，将无业务活动的健康常驻进程交给 owning worker 的统一退休流程，不额外恢复强制关闭页面。

### 本轮证据与已核对部分

- [Worker 四项探针与三轮原始输出](../.ryugi/audits/lody-mechanism-followup-20261117/worker-probes.txt)：12 次预期失败；源文件与 overlay 在同一目录。
- [私有通道三轮原始输出](../.ryugi/audits/lody-mechanism-followup-20261117/supervisor-probes.txt)：3 次预期失败，响应及时的对照三轮通过。
- [86 项相邻检查](../.ryugi/audits/lody-mechanism-followup-20261117/aligned-neighbours.txt)：worker、supervisor、store、app 四包均通过，包含归档、自建 worktree、20 分钟 GC、原私有关闭保护、绝对到期及完成投影。它们没有覆盖上述五个新增断点。
- [源码与上游版本证据](../.ryugi/audits/lody-mechanism-followup-20261117/source-provenance.txt)：包括各相关文件 SHA 与上游核心链路 diff 为空的输出。

另外核对了父进程消失后 child 自行关闭、切换中断的 blocked 恢复、force 物理回收并行、旧失败代次分离与后台重试、自建 worktree 的备份／共享保护／延迟恢复。此次未发现这些分支新的确定性差异。`docs/runtime-idle-reclamation.md` 仍写旧的 10 分钟／5 秒及 FenceIfIdle，是过时文档，当前代码实际为 20 分钟／30 秒；不将该文档错误冒充运行参数错误。

未登记后代和旧存量迁移手动处理继续按用户决定，不作为新问题；也没有以安全或归属检查为由新增确认页。

## 2026-11-17 本轮机制补齐

对应用户“继续对齐”，本轮产品改动落在 `cc82644`、`a3d4749`、`876189d`、`ed27617`、`8972737`、`d384f6f`；测试夹具修正为 `6f6efc8`、`0863eaa`。采用职责与生命周期语义，保留本项目的 Go worker、私有 IPC 与 harness 中立边界。

| 路径 | 对齐后的行为 |
| --- | --- |
| 闲置会话接续新安装 | 保留真实轮次、请求和 work 核验，主动退休旧登记组及 resident Node；不再要求空闲内核先自行退出 |
| 关闭与切换 | 支持的 child 从启动即持有独立 FD3 socketpair；最终接纳仍在 owning worker 的 dispatch 边界检查，业务 socket 缺失和 ready 前均有真实签名 child 验证 |
| 超时与到期 | 24 小时到期从原始接纳时间固定保存；空闲关闭的单次尝试另带 admitBefore，在持久 intent 前重查。未接纳的超时尝试回到 waiting，不能迟到关闭旧 writer；已接纳的准确 intent 继续核对 |
| 已提交版本恢复 | 已提交 applied／rolled-back 的暂停恢复保留临时启动重试；未提交候选与首次未提交迁移保持一次性验证／回退 |
| 后台工作晚于父轮结束 | 父轮释放执行槽，只有相同 runtime/history 的已知工作和原生输入回执继续延迟未读／通知；最终观察与完成身份在同一事务发布，无等待 goroutine 或 dispatch reservation |
| 后台中断与撤回 | 准确实例 loss 在 work 失效之前发布 interrupted 与持久说明；Stop、归档、工作区归档、新轮次、历史切换和 shutdown 撤回旧等待。撤回保留 turn watermark，重复旧事件不能恢复完成通知 |

最新上游 `eff59dc` 的 `turn-completion.ts` 与 `session-execution-service.ts:5986–6044` 是延期完成参照：等待不持有写入／轮次槽；同实例工作自然结束为 settled，实例先结束为 interrupted，新轮或显式撤回为 superseded。没有把单个子任务失败改成父轮失败，也没有伪造父轮执行结果。

本项目使用现有持久 Session/Work journal 承载这段语义。Pending completion 尚无 ID／Unread；落盘提交成功后，最后一个 work 或输入回执事件才携带新的 publication ID。断线只从快照重建未读；历史快照不直接生成系统通知。旧 worker 重启和整体 shutdown 撤回未发布等待；关闭入口先原子禁止发布，再沿既有共同截止完成写入，未新增阻塞锁。

本轮检查包括真实 Node 登记组、真实签名 worker、supervisor 与 Codex 本地测试 provider 的历史回放。不是正式设备部署，也不覆盖没有当时拒绝日志的 MBA 晚间具体原因。未登记后代、旧存量停止接纳字段缺失的手动迁移、20 分钟／30 秒参数，以及自建 worktree 已确认的 Git 忽略文件边界仍按用户决定。

最终限定回归命令 `go test ./cmd/... ./internal/... -skip '^TestWorkspaceHeading(SharesTheWorkspaceRowVisibleEdges|InsetsAndNoPerWorkspacePlus)$' -count=1 -timeout=12m` 的 22 个测试包与 5 个无测试包通过；四个相关包的两轮 race、三轮机制专项、六类签名历史条件两轮共 12 次 APPLIED 与 H07 两次 BLOCKED 均保留原始输出。完整未过滤产品回归发现两项既有 workspace-heading 间距测试失败，独立切回修复前 `fa15c3e` 也出现相同结果；根目录 `./...` 还会编译忽略的研究工具，重复 main 导致失败。全量 vet 的四条旧 unsafe.Pointer 诊断位于 diagram/tailnet；本次所改五个包的 vet 单独检查。报告同时保留这些失败与最终限定范围的通过记录，不把它们改写为全量绿色。

## 2026-11-17 实施补充

已按用户追加授权接入自建 worktree 的归档备份、目录回收、保留分支与延迟恢复；多会话共用 checkout 时等最后一个使用者归档，失败按条保留重试。被 Git 忽略的文件不进入备份，与 Lody 相同；原项目与 harness 原生历史不删除。会话永久删除统一为一次确认，应用记录先删除、物理退出由后台处理。

上轮八条可重现差异分别落实为：取得会话闸门后复查待办是否仍有效；删除事务移交并清除原退休待办；退出等待释放全局派发锁；清除 idle／绑定引用；删除不再受暂停队列否决；并发 WARNING 不等待已有回收；仅成功驱逐计入三次配额，并按闲置时间排序；删除后回收 runtime 身份文件。额外覆盖删除回执慢退出、归属文件修复后重试、命名短任务与 worktree 备份中途恢复。

当前资源契约见 [归档管理](archive-management.md)。实现未复制 Lody 的归档 dedupe 检查顺序竞态，也未把前置资源失败阻止后续终止的控制流缺口当成必须对齐的行为。用户接受的未登记后代覆盖范围、首次迁移手动处理以及闲置 20 分钟／30 秒扫描决定保持有效。

## 1 核查范围与版本

固定 Lody 提交为 `fb05adc646f02762a62c19cf8e9dadd9f765a3e0`，CLI 包版本 `0.104.0`；此前调研提交 `37e76b0…` 保留为历史证据。该提交固定的 Codex adapter 子模块仍为 `f2a140b5a630454f3707c48d93ea162ecbad82e7`。所有链接固定到提交，不以以后变化的 main 代表本次结论。

“全面”覆盖本次问题的完整调用链：桌面与设备更新入口、授权与接纳、固定 supervisor、候选准备、业务准入、会话取消、GC、归档、sandbox／进程组、生产者停止、历史落盘、版本提交、回退、首次安装迁移和配套测试。没有把支付、站点、移动端等无关代码列为已审计范围。

实现和测试交叉核查；代码注释与实际分支冲突时以执行分支为准。例如 managed Worker 的 `processGroup` 实际按 managed 模式设定，不能只读旁边“继承 watchdog 组”的注释。原始索引与测试输出保存在本地忽略目录 `docs/research/lody-worker-lifecycle-2026-11-17/`。

## 2 核心结论

Lody 将“有进程存在”和“有工作正在执行”分开。会话正常完成后可保留 Agent 供下一轮使用；闲置到期、归档或设备升级时，由后台主动终止相应环境。它没有先要求所有 MCP／Node 自己退出，再允许开始回收。

它的设备升级采用固定 supervisor 加可替换 Worker。旧 Worker 在新版准备期间继续运行；切换时关闭新任务入口，终止全部会话环境、排空最后的持久化工作并发送 drained 回执，然后启动候选。候选先只准备、核对本地健康；supervisor 持久化目标选择和结果后才恢复新任务接纳。这不是保留 Agent 管道与内存跨升级的设计。[升级协调][runner]、[Worker 切换][start]

普通 GC、归档和升级清理均可走 `terminate(true)`；force 分支跳过原生 `closeSession`，主动终止登记的进程范围。普通正常 `terminate(false)` 才先调用原生 close。不能用正常 close 的能力解释 force 分支。[会话终止][session]、[GC 与归档][handler]

## 3 进程关系与拥有者

| 层级 | 代码中的角色 | 生命周期 |
| --- | --- | --- |
| 桌面 App | Electron `CliService` | 可自行托管 CLI，也可观察已有持久 Host；不是每台设备的全局 supervisor |
| 设备固定入口 | managed SEA 的 `daemon-runner`／`CliSupervisor` | 持有 Host lease、升级日志及选中版本，跨 Worker 切换保留 |
| 设备 Worker | `lody start`／Fleet／MachineRuntime | 承担多个工作区、业务调度、持久化与会话管理 |
| 会话环境 | `Session`／AgentClient／sandbox | 持有 ACP Agent、命令、Agent 终端、凭据租约等 |
| Codex adapter | 固定子模块中的 ACP 适配器 | 内部连接 Codex app-server；Lody 共享调度层不直接用 Codex 原生协议 |
| 手动终端 | Fleet 的 terminal PTY 服务 | 普通 Agent 取消／GC 保留，显式关闭、归档／删除、Fleet 退出时释放 |

Host lease 是操作系统端点独占，不是“PID 文件存在”检查。supervisor 取得 lease 后才发布 PID 记录；token、instanceId 和实际受管 child 共同约束私有控制。已有健康外部 Host 的 attach 和 daemon 拒绝重复拥有是不同分支。Shioji 应保留现有 `owner.lock`、同 UID 私有 IPC 与独立网络服务，移植职责而不是复制 Lody 的端口与云身份。[Host lease][lease]、[桌面托管][electron]

## 4 动作语义对照

| 动作 | Lody 的真实行为 | 何时算完成 |
| --- | --- | --- |
| 一轮回复完成 | 释放该轮的 Inbox／执行槽，通常保留 Agent | 原生请求与执行记录释放，不要求环境退出 |
| Stop | 取消准确轮次；等待 prompt、Steer 等真实收尾；5 秒未结束可强制退休该 Session | 取消回执、轮次释放、物理清理分别处理 |
| 闲置 GC | 通过活动检查后强制退休环境，再卸载内存文档与跟踪 | 终止／卸载成功才删除 GC 跟踪；失败保留重试对象 |
| 归档／删除 | 释放终端、预览、子 Session 与 Agent | 环境终止失败可记录警告后继续完成归档；不重用失败的旧实例 |
| 更新 Worker | 准备候选后停接纳，关闭全部 Session、排空写入和已观察进程 | 当前 Worker 发出有效 drained，并按指定退出码退出 |
| 候选提交 | 本地 ready、实际 PID／版本／machineId 与本次操作相符 | 持久化 selected、applied 后发送 resume |

归档“可以完成”并不等于它证明了所有进程已经消失；升级的 drained 要求也没有因归档容忍错误而取消。两条路径的失败语义不同，移植时必须保留。[归档释放][archive]、[GC 回收][gc-cleanup]

## 5 闲置 GC

默认配置为闲置 20 分钟、每分钟扫描、原生后台任务最多保护到闲置 24 小时；支持环境变量覆盖。计时依据最后活动时间，不以 CPU 为零或没有新用户消息独立判断空闲。候选覆盖已跟踪 Session 和缓存文档；无活动时间的刚创建对象受保护，读取 GC 资格使用 peek，不为检查重新打开已卸载文档。[默认配置与扫描][gc]

资格依次检查：没有活动轮；原生 Agent 不是 running；原生后台任务未处于保护期；没有活动 goal、未决更新、未决用户工作；不在归档互斥中。包含异步检查时，在真正回收前重新读取活动与计时，避免候选选择后开始了新工作。单个对象读失败只保留该对象，不拖住其余候选。

AgentClient 从原生活动报告得到 `running`、`background` 或空值：非 idle 报告属于 running，idle 但后台任务数大于零属于 background。没有活动报告时返回空值，并没有普遍扫描 OS 后代来替代这个报告。常驻 MCP／Node 仅存活不直接否决 GC；实际工作是否上报依赖对应 adapter。[原生活动分类][native-work]

GC 先关会话模拟器／预览，再调用 `terminateSession(id, true)`，成功后卸载文档，最后删除瞬态跟踪。卸载的是内存驻留，不是删除会话历史。下一轮重新创建运行环境并恢复源会话，内存变量不保留。手动终端属于独立 Fleet 生命周期，不由 Agent GC 顺手关闭。

内存压力路径与普通 idle sweep 使用同一清理入口，但资格／预算不同：后台等待可被提前回收，running 仍保留。macOS 使用内核压力 WARNING／CRITICAL，分别用于回收／拒绝新轮次；不以 free RAM 字节阈值替代。每次前台回收数量有界，默认最多 3 个，慢回收等待有界。Loro Wasm 堆大于 3 GiB 时缩短闲置门槛至 2 分钟属于 Lody 的技术栈特性，Go Shioji 不应引入这个门槛。[GC 和内存压力][gc]

## 6 Stop、初始化卡住与失败释放

Stop 校验原 turn，记录取消／暂停意图并发送 ACP cancel。`drainCancelledPrompt` 等待原始 prompt completion、AgentClient 仍持有的 prompt 以及已发出的 Steer 配置请求；不把 cancel ACK 当全部收尾。默认 5 秒后仍不完成，就对该 Session 调用 `terminate(true)`。迟到的旧 Stop 不能清理后来的新轮次。[取消收尾][cancel]

初始化卡住时，先把创建 promise 从 dedupe map 分离，防止重试拿到同一个永远未完成的 promise。若迟到创建成功，再按原实例回收；不是拿 SessionID 去清理任意新实例。

释放失败会保留 `releaseFailure`，分离旧 Session，并安排每会话去重的后台恢复；后台一次尝试约 5 秒，失败记录仍落盘。Lody 某些路径会释放可见执行槽，让后续工作继续，不等待物理清理百分之百完成。这个行为不能被描述成“物理退出已经成功”。Shioji 保留原有输入防重放和实际线程核对，不把界面可继续等同于重发未知输入。

预算集中在 `turn-budgets.ts`：取消 5 秒、单次 Agent 清理 5 秒、退出释放 6 秒加最后失败写入 1 秒、整体 CLI 退出 7 秒。它们是不同阶段的预算，不是一个通用“等 5 秒”。[统一预算][budgets]

## 7 Session 终止与 MCP

并发终止共享同一个进行中的结果；force 可以立即升级已开始的正常关闭。完成后再次调用会重新处理，失败拒绝不会永远缓存成不可重试的结果。

正常分支先释放 Agent 终端，再调用 `agentClient.closeSession`。force 不等待正常 close；随后并行结束 activeProcess、agentProcess 和整个 sandbox，避免顺序等待把预算叠加。终端 disposal 也会加入收尾。进程未确认结束时保留句柄、sandbox 和凭据，不清空引用伪造完成。

物理清理成功后释放凭据租约和外置 MCP 访问；即使一项失败，也尝试释放另一项。成功清理才清空 Agent 引用并发布 terminated；单独资源释放错误仍可能使该次调用失败，不能仅凭 terminated 事件推断全部附属资源都成功。[终止实现][session]

这里的外置 MCP release 是撤销该会话 owner 的访问租约／代理 scope，不是删除用户长期 OAuth／Keychain 凭据或停止用户自己部署的远端 MCP 服务。Agent 自己创建的本地 stdio MCP 另随其进程范围清理。force 虽会立即开始物理终止，函数最后仍会等待终端 disposal 和资源释放，因此 5 秒进程等待不是整个 GC 函数的统一硬截止。[MCP 租约释放][mcp-release]

Codex 正常 close 先分页读出根与已验证子线程的全部后台终端，再逐个 terminate，返回 false 时重新列举，最后 unsubscribe。这条路径比只取消订阅完整，但 GC／归档／升级的 force 分支会跳过它；两条路径不能混算。[Codex close][codex-close]

## 8 macOS 进程管理

macOS 使用没有 Linux cgroup 硬限制的容器；经容器启动的进程各自建立进程组。registry 保留根退出后仍有成员的组，只有全组消失才忘记；失败清理保留原对象，后续可以重试。[容器][container]、[组登记][registry]

正常策略为 TERM 最多 5 秒，再 KILL 最多 5 秒；force 为直接 KILL 后最多等 5 秒。ESRCH 视为已消失，EPERM 不当成消失；最终观察优先于单次信号错误，只剩僵尸时不按仍占资源的执行无限等待。全局观察集合上限 5000，溢出或查询未知不产生成功 drained。[进程终止][process]、[全局观察][observation]

用户已接受未登记、另建组后代的覆盖边界。本轮只采用上述登记范围，不增加扫描所有后代、通用进程管理页或新执行宿主。

## 9 更新准备、接纳与授权

设备操作支持 `when-idle` 和 `immediate`。默认 when-idle 等跨工作区的实际轮次 reservation 归零；reservation 在初始化的第一个 await 前取得，暂停与预留同步完成。立即更新显式暂停并把被中断轮次的原因记为 machine_update。后台进程列表不是这个准入门槛。[准入门][admission]、[切换策略][start]

准备阶段旧服务继续运行：核对当前 child 所属 supervisor；解析目标版本、产品、平台、channel；按 requestId 把意图持久化；下载摘要寻址的制品；检查压缩／解压后摘要与大小、完整提取标记；运行 native probe、Node 版本、CLI 版本及隔离状态目录探针。探针不打开真实凭据和业务库。准备预算约 8 分钟，IPC 等待 10 分钟。[准备代码][runner]、[制品校验][release]

同一 requestId 只能绑定同一目标与选项；终态只查询，不作为新更新重放。操作 prepared waiting 有 24 小时有效期；远端执行授权还有单独期限及临切换重新校验。过期／撤销恢复原 worker 接纳，旧版继续运行。GUI 返回 accepted 只代表接受意图，不代表升级完成。

Lody 远端云版本验证 owner 与后端签发权限，渠道切换和降级要明确授权；加密工作区和部分启动模式会禁用远程更新。Shioji 使用原有 SSH／私有 Tailnet 认证来提供相同的拥有者边界，不引入 Convex、云令牌或 Lody 的身份模型。[更新接纳][handler]、[能力边界][lifecycle]

## 10 退出与最终历史写入

顺序是：关闭新调度与生产入口，取消／释放活跃执行，结束 Session 生产者，等待轮次 finalizer／Inbox release，刷完 ACP 通知与其他历史写入，最后关闭证据存储和文档 writer。`SessionManager.cleanUp({keepWorkspaceDocumentOpen:true})` 就是为了让最后回调仍有地方落盘，不能先关库再结束 Agent。[清理屏障][handler-cleanup]、[Fleet 收尾][fleet]

`start.ts` 在 Fleet、后台运行时安装器、应用子服务全部关闭之后检查已观察的进程树。所有条件成功才给 supervisor 发 `managed-drained`；随后使用升级专用退出码 43。只有根进程退出、版本号打印成功或 UI 断线都不足以替代这份回执。[Worker drain][start]

整体 CLI 有 7 秒退出上限；第二次退出信号可立即结束。若在真正完成 drain 前被上限中止，supervisor 看不到成功回执，会进入失败而不是带着“退出码看似正确”直接换版本。保留这种失败区分比把所有退出都报成功更重要。

这 7 秒从 shutdown 开始计时，6＋1 是 `waitForInboxReleases` 内部的等待／失败写入预算，不是让每个会话先等 5 秒后再加 7 秒。SessionManager 以 Promise.allSettled 并行结束多个会话。最后失败写入由执行释放协调者负责；整个退出硬截止可能先到，源码并没有保证最后记录在磁盘故障时一定成功。移植时必须使用共同截止并保留预先落盘意图，不能把这些数值串联使用。[统一预算][budgets]、[实际退出控制器][shutdown-controller]

## 11 选中版本、候选健康与回退

操作状态有 preparing、waiting、switching、observing、applied、rollback-stop、rolled-back、blocked、failed。固定入口与 Host lease 不随正常 Worker 更新退出；映像按摘要存放，正常切换只改变选择。JSON 以临时文件写入、fsync、rename、父目录 fsync 发布；按 requestId 另外保留长期回执，防止旧请求重放。[持久记录][release]

候选启动时禁止接纳新轮次；本地 ready 要求进度到 ready、状态非 fatal，并匹配实际 candidate PID、版本、machineId 和本次 service instance。ready 可以是网络降级，不要求外部模型在线。候选就绪期限 3 分钟；超时先请求关闭，8 秒后对准确候选强制终止，再等待有界结果。[候选期限][startup]

正常提交顺序为 selected → applied／rolled-back → resume。候选退出且 drain 已确认，恢复保留的旧版本一次；候选未确认退出或持久化未完成时进入 blocked，不启动第二个 writer。旧版本回退本身失败也进入 blocked。supervisor 常规崩溃重试有指数退避与熔断：1～60 秒退避、5 分钟内 10 次故障上限；V8 OOM 单独统计，不能照搬成 Go 内存模型。[supervisor][supervisor]

固定根在 preparing／waiting 重启时丢弃未实施意图并维持已选版本；在 switching／observing／rollback-stop 中断则保守进入 blocked。Lody 的文档给出系统重启、保留原件并归档操作指针、从可信兼容安装器恢复的流程。它不是所有崩溃点都自动回退的实现。本轮采用既有机制与恢复说明，不再为这点另造热交接框架。[恢复说明][packaging]

存储只接受声明兼容的 serviceProtocol 1／dataFormat 1；声明正确是发布者责任，不自动证明任意 schema 安全。禁止候选在 commit 前迁移真实库。Lody 制品主要依赖可信 HTTPS 服务和摘要；文档明确摘要不等于独立发布签名。Shioji 继续使用现有签名与 Apple codesign 检查，保持既有安全边界。[打包约束][packaging]

## 12 安装迁移、桌面退出与 Agent 自身更新

旧 npm 到 managed 的 relay 只在原服务没有被其他服务管理器拥有且已经 drain 时执行；先准备固定入口与目标，再转交 lease。新服务就绪才发布安装入口；失败能取回 lease 就恢复原版本，取不回则 blocked。launchd 管理的旧服务不自动套这条 relay，必须走显式安装迁移，避免注销旧 job 时顺带结束新服务。[首次迁移][migration]

Electron 托管本机 CLI 时，使用同一 CliSupervisor，但嵌入 CLI 不建立自己的进程组，不能组信号误伤 Electron；退出默认给 10 秒正常收尾、再 5 秒 KILL 核对。托管外部服务则不冒领其所有权。远端 managed 更新与 App 自身更新是不同入口。Shioji 已要求执行服务独立于 GUI，因此采用 managed 形态，而非把 GUI 生命周期复制进来。[桌面服务][electron]

Agent 安装更新另由 `ManagedRuntimeUpdateCoordinator` 去重、后台准备，和整个 daemon 更新分开。版本缓存清理不等于已退出证明：该管理器没有普遍 reader lease，不能从 `pruneSupersededVersions` 推导所有运行实例的文件依赖均安全。Shioji 继续保留实际会话版本、能力及下一轮接续规则，不顺便接管用户 Codex 安装。[Agent 安装协调][agent-update]

## 13 测试与证据

本轮在独立临时克隆中安装了最小测试依赖，使用原始测试文件与实际被测模块运行下表。自定义 Vitest 配置只映射原仓库源码路径，没有替换被测进程、GC、supervisor 或迁移逻辑；原测试自身使用的 fake process table／mock 仍按原文件执行。

| 原始测试文件 | 本轮通过数 |
| --- | --- |
| `session-gc-manager.test.ts` | 39 |
| `managed-process-observation.test.ts` | 4 |
| `managed-daemon-migration.test.ts` | 11 |
| `cli-supervisor/supervisor.test.ts` | 29 |
| `cli-supervisor/retry.test.ts` | 5 |
| `cli-supervisor/process-resources.test.ts` | 9 |
| `shared/tests/process.test.ts` | 5 |
| 合计 | **102／102** |

包含真实 POSIX 子进程终止，也包含模拟 OS 的竞态、拒绝退出、相同 supervisor 所有权和 GC 活动重检。测试运行于本机 Node `v24.18.0`，不是 Lody 打包的 Node 26.13.0；本轮没有运行完整 monorepo、SEA 原生打包验收、Electron UI 或真实模型任务，不能把 102 项测试写成生产发行包验收。

还阅读了候选 readiness deadline、制品损坏／断点续传／请求防重放、Session 并发终止／失败保留、归档终止失败不阻塞、最后历史写入屏障等专项测试。它们是已阅读的设计证据，未混入上表已执行数量。先前三个隔离 Darwin 进程组探针仍在历史调研目录中，与本轮测试分开记录。

## 14 对 Shioji 的直接映射

| Lody 机制 | Shioji 当前位置 | 本轮改造 |
| --- | --- | --- |
| 固定 runner＋可替换 Worker | `cmd/shioji-runtime`、transport worker 启动／更新 | 固定执行服务持有更新操作和原所有权，准备后替换 worker |
| when-idle 的业务 reservation | `worker/update.go`、`dispatchMu`、队列与初始化接纳 | 等真实执行，取消“任意后台进程存在就拒绝更新”的前置条件 |
| Session force terminate | `harness/codex/adapter.go`、`runtime.go`、`executor.go`、`managedexec` | 统一回收实际会话的两类 server 与已登记工具，不新建 host |
| 20 分钟 idle GC | `worker/idle_runtime.go`、`store/runtime_idle.go` | 直接采用 Lody 默认计时和工作分类，不把健康常驻内核当繁忙 |
| archive release | `worker/server.go`、`worker/archive.go`、store archive | 接纳归档时保存回收待办并立即清理，保留历史与草稿 |
| 生产者停止再落盘 | worker Close、事件处理与 store.Close | 最后输出和停止结果先入库，之后才关闭协议与 writer |
| drained＋本地 ready＋selected | 既有 staged build、active-worker、owner.lock | 明确持久阶段、准确候选身份、成功提交和兼容回退 |
| per-request durable outcome | 既有 maintenance／recovery 回执 | 查询接续、旧记录转换，不用 partial 全局拒绝服务启动 |
| 简单设备动作 | `internal/ui/worker_maintenance.go` 等旧流程 | 删除旧处理页、PID 选择与强制入口，保留普通更新／重试和一处真实状态 |

具体方案以[修订后的升级与回收设计](ryugi/specs/2026-11-16-worker-upgrade-lifecycle-design.md)为准。原先的独立 host、事件 spool、writer epoch 跨宿主切换方案已撤回，不再作为实施前提。

## 2026-11-17 再读与机制对齐

以同一固定源码 `fb05adc646f02762a62c19cf8e9dadd9f765a3e0` 再读实际调用链，初轮实现还有六处可重现缺口：旧 Stop 误关同实例的下一轮、安装接续／归档清理失败仍保留旧实例、回收占设备级派发锁、本地请求取消丢失原始未决计数、暂时启动失败直接 blocked，以及原生 Close 不遵守截止。相应重现和原始输出见 `docs/research/lody-worker-lifecycle-2026-11-17/recheck/`；该目录保留修改前证据，不重写成通过。

本轮实现逐项修正，并补齐内核压力回收／接纳、临时目录探测、持续 supervisor 健康检查与归档接纳。回收和原生查询的慢等待移出设备级锁，以会话交接保护原状态；初始化、失败清理及旧回调仍按准确代次隔离。归档在任何执行状态均可接纳，队列同事务暂停且不删除，清理失败退休旧实例，恢复不会重发。

两项先前描述需要纠正：retry helper 默认的 20% 抖动没有被 Lody 实际 daemon-runner 采用，托管实例默认为零；健康 60 秒从 spawn 计时，而非 ready。Shioji 已按这两个实际调用点对齐。启动退出码 2／3 只等待依赖／端点恢复，不计入普通 5 分钟／10 次熔断；暂时准备错误按普通退避重试，签名／格式／退出归属不明仍不能盲目重试。持续健康探测为 1 秒周期、800 毫秒预算、3 次失联后标记重新连接，不以失联授权结束活进程。

Lody 的通用 GC 有原生后台活动的 24 小时保护，但固定版本 Codex ACP adapter 没有报告这项活动。Shioji 直连 Codex 能观察到真实后台工作，保留这项保护；不能为了复制缺失报告而丢弃已有执行事实。压力回收可提前终止符合条件的等待型后台工作。闲置 20 分钟／扫描 30 秒、未登记进程范围、首次旧版迁移由用户手动处理，均沿用已有决定。Go 没有 V8 heap，不复制 Lody 的 V8 OOM 专用计数；采用 macOS 的实际内核压力等级。

这些对齐消除已重现的实现缺口，不承诺操作系统权限拒绝、持久化失败、不可确认 writer 退出或候选损坏时仍能成功升级。Lody 本身在交接不确定时也会 blocked。不能把删除强制处理页面理解为删除这些真实失败语义。


## 2026-11-17 再次边界复核：仍有四项未对齐

**历史记录：以下是修复前 `a3dcd32` 的复现，当前实现状态与验证范围见文首“本轮机制补齐”。**

检查 Shioji `a3dcd3211f856cb67fc69b85a2860a95b052d00b`，包含上轮并发回收修复 `40087a9`。同时 fetch 并核对 Lody `eff59dca5f6099d158a8ba1c16f256ddbeae6ad8`；原固定基线仍为 `fb05adc…`，Codex adapter 子模块仍是 `f2a140…`。新增差异没有改变下面四项所依据的原升级／终止主体。没有把已有规格或上一轮测试结论代替当前源码。

### 1. 高优先级：接续新版 Agent 仍被闲置 Node 内核卡住

`internal/worker/session_runtime.go:110` 在确认安装版本变化并检查实际轮次／请求后，仍调用 `Scope.FenceIfIdle`。其 `internal/managedexec/manager_darwin.go:783–800` 要求登记组中不能有 root／anchor 以外的成员。这正是历史“仍有后台进程，结束后更新”的进程存活判据；worker 自身升级虽然已经改用业务准入，这条 Agent 安装接续路径还没有改。

探针使用实际 worker 切换函数、中立版本变化 harness 夹具，以及真实登记 Node 父子进程。前置状态为 completed、HasActiveWork=false。三轮都返回“仍有后台进程，结束后更新”；没有该常驻进程的既有升级用例三轮均通过。该证据不冒充真实 Codex 安装器端到端部署。

Lody 在安全轮次边界退休旧 Session 并强制清理其登记范围，再创建后继；不会要求闲置 MCP／Node 先自行消失。修正方向是保留业务活动、请求和准确代次检查，复用已有退休流程，不再把这个旧 OS 后代空闲检查作为版本接续门槛。

### 2. 高优先级：关闭路径与业务 RPC 共用故障面

Lody 的 managed child 在业务 ready 之前已持有 IPC 生命周期通道；普通 supervisor 的 graceful 请求失败还有 TERM fallback，再给退出预算。Shioji `internal/supervisor/host_darwin.go:595–608` 只能请求业务 socket 上的 worker.drain；请求失败后不立即启动其他正常退出路径，而是等同一预算耗尽，再调用 stopExact(c,0)，几乎同时发送 TERM 与 KILL。

隔离真实子进程没有业务 socket，但收到 TERM 后只需 20 毫秒就能完成写入并出具准确 drained。三轮全部被 KILL，均无 drained；同一子进程预先通过独立信号开始正常退出时，三轮全部成功。探针把总预算压到 250 毫秒以验证顺序，生产预算没有改动。

生产 worker 的 DrainedReceipt 还要求 drainOperation 已由 RPC 设置，所以补一个裸 TERM 并不等于完成对齐。需要从启动就可用的私有生命周期通道承载同一操作、实例和策略，并在 owning worker 的准入边界重新核验；不得把 RPC 错误当成越过 when-idle 的结束授权。这条新探针不能直接证明上轮单次 100 毫秒回滚失败也由此引起。

### 3. 高优先级：已提交版本的恢复没有暂时启动重试

Run 会将磁盘已有 applied／rolled-back 操作以 paused=true 重建，等身份核对和 resume 后再开放接纳。但 startSelectedRetry 在 `internal/supervisor/start_retry.go:41` 对任何 paused 直接返回首次错误，连退出码 2／3 的依赖或端点暂时不可用也不再重试，随后持久 blocked。

真实子进程第一次退出 2，80 毫秒后移除模拟依赖故障；paused=true 三轮立即失败，paused=false 三轮能继续重试并 ready。Lody 已提交选择恢复后的普通启动仍保留其 retryable／occupied 重试路径；只有尚未确认的候选与回退保留一次性失败语义。修正方向是区分“已提交但尚未 resume”和“尚未提交候选”，不能继续用 paused 一位同时决定这两种失败策略。

### 4. 中优先级：等待到期没有在切换前再次核验

Lody 持久 expiresAtMs 并在实际处理升级退出时重新比较绝对时间。Shioji 在准备之后才启动 24 小时 timer；等待循环中，worker.prepare-drain 一旦返回 Ready=true 就先 break，只有非 ready 分支才读取 expires.C。切换前只检查取消，未检查到期，因此等待查询跨过到期边界仍可能使用已过期意图切换。

确定性探针只在 Go overlay 中把等待 TTL 从 24 小时压成 20 毫秒，并让原 fixture 的空闲 RPC 等待 70 毫秒；其余生产分支未改。三轮均 applied，而预期应过期失败并保留旧版。这里是压缩时钟的边界证明，没有声称真实等了 24 小时。修正应使用稳定绝对到期时间，并在关闭旧接纳／切换前复核。

### 相邻机制与明确边界

本轮 22 项已有专项通过：归档立即接纳、失败分离、恢复不自启、初始化停止、迟到归档、删除回执不等待 Close、记录修复后重试、最终写入、辅助进程退出、强制回收与归属保护、自建 worktree 备份／共享／删除／失败重试／其他 checkout 不阻塞，以及无 drained 不启动候选等。没有在归档、永久删除和自建 worktree 再发现新的可复现缺口；这不是全仓库验收声明。

参数差异（20 分钟／30 秒）、未登记 detached 后代不兜底、首次旧状态迁移手动处理，以及 Shioji 额外的同 checkout 安全保护均为既定决定，不重复算成遗漏。Tailnet 为独立服务，其上轮偶发启动失败没有因此被认定为 Lody worker 的机制差异。

Lody 新增 `44ac3e3`／`8168e39`：后台 Agent 工作结束后才发布完成／未读；准确实例先退出则发布中断，Stop／归档／删除／新轮次会撤销旧等待。Shioji 当前在父 turn.completed 时记录 ReplyCompletion 并产生“回复已完成”通知，尚无这套延迟发布。这是原基线之后的上游新语义，本轮仅源码确认，未将其算入上述四项遗漏或称为升级阻塞。`776f82b` 的每轮 MCP 选择替换则属于 Lody 的 per-turn 选择功能，不把 Shioji 当前设备原生配置模型冒充同一功能。

原始失败、正向对照、22 项检查、Go overlay 和源码指纹存于同主题验证报告 evidence/mechanism-audit-*。四项探针共 12 次失败；它们是未修复缺口，没有改写成通过。未更改产品实现或三台正式设备。

[runner]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/commands/daemon-runner.ts#L459
[start]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/commands/start.ts#L707
[session]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/session/session.ts#L266
[handler]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/lib/message-handler.ts#L10281
[lease]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/packages/shared/src/node/local-cli-host-lease.ts
[electron]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/electron/src/main/services/cli-service.ts#L1025
[archive]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/lib/message-handler.ts#L4814
[gc-cleanup]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/lib/message-handler.ts#L12394
[gc]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/lib/session-gc-manager.ts#L195
[native-work]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/agent/agent-client.ts#L2720
[cancel]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/session/session-execution-service.ts#L3570
[budgets]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/session/turn-budgets.ts
[codex-close]: https://github.com/LodyAI/acp-extension-codex/blob/f2a140b5a630454f3707c48d93ea162ecbad82e7/src/CodexAcpClient.ts#L1003
[container]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/platform/sandbox/noop-container.ts
[registry]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/platform/sandbox/process-tree-registry.ts
[process]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/packages/shared/src/node/process.ts#L570
[observation]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/lib/managed-process-observation.ts
[admission]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/session/machine-turn-admission.ts
[release]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/lib/managed-daemon-release.ts#L149
[lifecycle]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/lib/machine-lifecycle.ts
[handler-cleanup]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/lib/message-handler.ts#L11894
[fleet]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/lib/lody-fleet.ts#L700
[startup]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/commands/daemon-runner-startup.ts
[supervisor]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/packages/cli-supervisor/src/supervisor.ts
[packaging]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/scripts/sea/README.md
[migration]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/lib/managed-daemon-migration.ts
[agent-update]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/agent/managed-runtime-update-coordinator.ts
[mcp-release]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/mcp/authentication/service.ts#L548
[shutdown-controller]: https://github.com/LodyAI/Lody/blob/fb05adc646f02762a62c19cf8e9dadd9f765a3e0/apps/cli/src/commands/start-shutdown.ts
