# Lody 执行服务升级、停止与回收源码分析

核查日期：2026-11-17。结论用于 Shioji 本轮升级改造。用户已决定直接采用 Lody 的机制，接受其未登记进程覆盖范围，不再增加独立会话宿主或完整后代追踪架构。本轮未修改产品实现、未停止或更新三台设备。

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
