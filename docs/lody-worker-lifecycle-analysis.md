# Lody 执行服务升级、停止与回收源码分析

核查日期：2026-11-17。结论用于 Shioji 本轮升级改造。用户已决定直接采用 Lody 的机制，接受其未登记进程覆盖范围，不再增加独立会话宿主或完整后代追踪架构。以下参考源码分析在实施前完成；实施现状见下段，三台正式设备没有停止或更新。

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
