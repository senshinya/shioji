# macOS worker 技术验证

最新状态：用户随后恢复实机验证，真实 Tailnet 双向链路、严格主机校验 SSH、direct／DERP、持久节点恢复、GUI Keychain 与物理中文候选窗已通过；VoiceOver 跳过，Air 电源测试仍延期。见 [实机补充验收](live-acceptance-2026-11-03.md)。下文保留早期实验的当时范围。
2026-11-03。首期客户端和执行设备都只支持 Apple Silicon macOS，不支持 Intel；自有完整 worker、取消 herdr-core 已由用户确定。本文记录第二轮调研与后续补充实验，安装方式、最低系统版本、数据库和协议字段仍是技术建议。没有开始产品运行时实现，也没有修改冻结设计。

后续的合并构建、Hardened Runtime、两台机器的 SMAppService、实际工具 sandbox、app-server 在途崩溃、数据库故障、双栏压力和原生选择边界已收尾，见 [最终调研报告](research-closeout-2026-11-03.md)。真实 Tailnet、真实 IME／VoiceOver、Air 电源测试均由用户明确留作后续验收；原有失败记录不按后续局部成功覆盖。

## 结论

自有 worker + Codex app-server stdio 的路线在两台 Apple Silicon Mac 上具备实施依据。Air 上已验证普通用户部署、SSH 断开后进程存活、崩溃重启，以及真实 app-server 待回答问题跨 SSH 重连继续处理。Codex 使用本地固定模型响应，不涉及账号、外部模型或用户仓库。

建议采用用户级 launchd 服务管理 worker；worker 持有 app-server 的独立管道，并提供自己的版本化控制协议与事件游标。SQLite 可先以纯 Go 驱动 `modernc.org/sqlite v1.61.1` 作为实现候选；事务确认必须在落盘后发布，确认不明的外部操作不能自动重发。

正式签名包的后台服务授权、真实 Tailnet 登录和路径测量、物理输入法／VoiceOver、设备休眠／注销／重启仍需后续验收。普通网络桥接还发现了与系统 OpenSSH 不一致的连接失败，不能把它掩盖为已通过。

## 一、实验范围和证据

控制 Mac 与 MacBook Air 均为 macOS 27.2、arm64。Air 通过用户提供的 SSH 地址连接；ED25519 指纹经用户在 Air 本机核对，仅放入测试专用 known_hosts。没有关闭主机验证，没有修改用户 SSH 配置或认证凭据。

| 检查 | 结果 | 证据与边界 |
| --- | --- | --- |
| 临时上传、摘要核对、原生二进制运行 | 通过 | [Air 记录](research/macos-2026-11-03/air.json)；无 root、远端未安装 Go |
| launchd user / Background 与 gui / Aqua 服务 | 通过 | 两种 domain 分开验证；不代表无人登录或注销后也已经验证 |
| 断开部署 SSH 后继续运行 | 通过 | 独立第二条 SSH 连接观察到相同实例及递增心跳 |
| SIGKILL 后重启、旧子进程清理 | 通过 | 原生 Go 实验进程及同进程组 child；不等于所有 Codex 后台终端都已覆盖 |
| 真实 app-server 的并发、Steer、中断、审批和问题 | 两端通过 | [本机](research/macos-2026-11-03/codex-local.json)、[Air](research/macos-2026-11-03/air.json)；固定 loopback provider，无外部模型 |
| 待问题跨 SSH 断开、重连、回答 | Air 通过 | 临时协议宿主保持 app-server PID 与 request ID，另一 thread 可先完成 |
| SQLite 事务崩溃和不明确操作窗口 | 两端通过 | [本机](research/macos-2026-11-03/sqlite-local.json)、[Air](research/macos-2026-11-03/air.json)；SIGKILL，不是断电 |
| 纯 Go SQLite driver 的 WAL / FULL / fullfsync、提交前后崩溃 | 两端通过 | [本机](research/macos-2026-11-03/sqlite-driver-local.json)、[Air](research/macos-2026-11-03/sqlite-driver-air.json)；CGO_ENABLED=0 |
| tsnet 用户态连接与节点状态重用 | 通过 | [受控测试](research/macos-2026-11-03/tsnet-controlled.json)；本地 test control / DERP，不是真实用户 tailnet |
| 官方 tsnet 双向 TCP、私有 worker socket 桥、授权拒绝和流清理 | 通过 | [入站记录](research/macos-2026-11-03/tsnet-inbound.json)；三临时节点、本地 control/DERP，不是真实跨设备 Tailnet |
| A 经 SSH、B 经本地 IPC 访问 B 唯一数据源 | Air 通过 | [双客户端记录](research/macos-2026-11-03/multi-client-air.json)；相同快照、并发 revision、操作去重、游标补齐、崩溃和 schema 迁移，协议夹具而非已安装 GUI |
| 两客户端同时回答真实 Codex 问题／审批 | Air 通过 | 同一有效 request 仅发一次 wire response、一次 resolved；第二个回答被拒绝；审批选择拒绝 |
| MyGo 输入、选择、跟随底部 | 8 项通过 | [上游定向检查](research/macos-2026-11-03/mygo-input.json)，headless |
| MyGo 原生窗口、5,000 行历史、中文已提交文本、发送键 | 通过 | [原生 fixture](research/macos-2026-11-03/mygo-native.json) 与 [截图](research/macos-2026-11-03/native-fixture.png)；不是产品界面或真实 IME 验收 |
| 上游真实屏幕输入法测试 | 未通过 | [限制记录](research/macos-2026-11-03/native-e2e-limitation.json)：等待窗口可见超时，不能按局部 PASS 算成功 |
| 系统 OpenSSH 通过普通 TCP ProxyCommand 桥访问 Air | 未通过 | [桥接记录](research/macos-2026-11-03/ssh-bridge.json)：Python connect 报 EHOSTUNREACH，直接 SSH 同时成功 |
| 同环境 Network.framework 隐私诊断 | 确认拒绝 | [诊断记录](research/macos-2026-11-03/local-network-framework.json)：`unsatisfiedReason=localNetworkDenied`；不是签名包授权验收 |

实验脚本在 [scripts/research](../scripts/research)。它们是一次性研究夹具；其中 Python 只服务于实验，不是产品运行依赖。临时协议宿主直接使用 Codex 操作，未实现最终中间协议或完整 journal，不能据此宣布 worker 已完成。

Air 的临时服务和上传目录已清理；纯 Go driver 的独立测试目录也已删除。未注销或重启 Air，未变更后台项目、隐私或睡眠的系统设置。

## 二、部署和后台服务

### 本轮确认的行为

Air 上用户级服务可以脱离发起 SSH 的生命周期。`user/<uid>` 与 `gui/<uid>` 是不同 domain；显式按 Background / Aqua session type 配置后，两者都通过测试。第一次缺少适当 session type 的 user-domain bootstrap 返回错误，修正后通过，不能从这次错误推断 user domain 永远不可用。

强制结束 Go 实验服务后，launchd 创建了新实例，并清理旧实例同进程组内的 child。重启服务只证明进程管理有效；原 app-server 管道和在途请求不会因此自动接回。生产设计不建议用 `AbandonProcessGroup=true` 来隐藏孤儿进程问题。[Apple launchd 说明](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)、[Apple launchd.plist 源码文档](https://github.com/apple-oss-distributions/launchd/blob/main/man/launchd.plist.5)。

### 建议的产品路径

- 本机 worker 随应用分发；优先通过 `SMAppService.agent` 注册 bundle 内服务，读取注册和授权状态。该 API 从 macOS 13 提供，因此建议最低版本从 macOS 13 起评估，尚未确认为产品最低版本。
- 远端通过 SSH 上传已签名的 worker 制品或小型 worker bundle，在当前用户下安装和注册；客户端统一处理版本发现、制品校验、启动和诊断。正式注册可能需要目标 Mac 允许后台项目，不能承诺永远无需本机确认。
- 本轮直接 bootstrap 临时 plist 证明了开发测试路径，尚未证明正式签名 bundle、Service Management 注册和后台项目禁用状态的完整部署体验。
- 不使用 root daemon 作为默认方案。GUI 会话、Background 用户服务、注销、开机未登录和 FileVault 解锁是独立场景；本轮只验证了用户已登录时的行为，不承诺开机前运行。

Apple 建议持久后台服务使用 Service Management，并允许用户在系统设置中禁用它。客户端需把“需允许后台项目”“服务被禁用”“未安装”“版本不兼容”分别呈现。[SMAppService](https://developer.apple.com/documentation/servicemanagement/smappservice)、[后台进程管理](https://developer.apple.com/documentation/appkit/managing-ongoing-background-processes-in-your-mac)。

MyGo 的现有开机启动封装使用 `mainAppService`，用于主应用登录启动；它不直接等于任意 worker agent 的注册接口。需要小范围 Go 平台封装或上游补足，不需要 Swift 前端。[MyGo 固定版实现](https://github.com/egoist/mygo/blob/v0.7.4/internal/darwin/login.go)。

### 制品与升级

用户已明确首期不支持 Intel，只构建和验收 `darwin/arm64`。早期 probe 的 amd64 跨编译记录保留为历史，不进入支持范围。两台实机均为 macOS 27.2，不能据此确认旧版 macOS；MyGo、tsnet、Codex 及 SQLite driver 仍需一起确定最低系统版本。

安装流程建议使用私有 staging 目录 → 校验签名发布清单与制品 → 原子切换版本 → 验证 IPC 和协议版本，失败回退。两端 SHA 一致只能证明传输一致，发布者可信性仍依赖签名发布链。签名与公证使用 Developer ID 和现代 notarization 流程；本轮没有使用签名凭据或上传制品。[Apple 公证流程](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow)。

初期采用空闲升级：worker 暂停接纳新运行操作，确认没有活动轮、待处理请求或需要保留的后台工具，再切换。不能只看当前 turn 已结束就强制重启。数据库迁移必须纳入回退策略；旧二进制不一定能读新 schema。备份应使用受支持的 SQLite backup／一致快照方式，不能运行中只复制主 db 而忽略 WAL。

补充的 Air fixture 通过 v1→v2 schema 迁移，保留 DeviceID、WorkspaceID、会话 context、队列与事件游标；旧 binary 拒绝 schema 2，随后 v2 可重新读取。此项只证明明确拒绝不兼容数据库的可行性，不是安装器原子升级、迁移中断或自动回退验收。本机 `security find-identity -v -p codesigning` 返回 0 个有效身份，因此本轮无法进行 Developer ID 正式签名／公证链验证。

## 三、Codex 运行边界

固定运行版本 `0.166.0`，参考源 tag `rust-v0.166.0` 的测试夹具。使用独立 CODEX_HOME、工作目录和仅监听 loopback 的确定性 provider；RPC 实验不读取用户 Codex 登录或调用 OpenAI 模型。源码和运行二进制分别记录指纹，[来源](research/macos-2026-11-03/sources.json)。

确认的行为：

1. 同一 app-server 内两个 thread 可在本例中并行，一个请求等待时另一个可完成。这支持按用户／运行配置共享进程的候选，不能直接推成所有配置都安全共享。
2. `turn/steer` 的错误 `expectedTurnId` 被拒绝，正确值返回原 turn ID；随后 interrupt 由 `turn/completed.status=interrupted` 确认。没有验证模型已经采纳 steer 内容。
3. execution approval 可独立拒绝；问题可以按 request 回答，回答和中断均观察到 `serverRequest/resolved`。本轮 execution fixture 被拒绝，没有执行申请中的命令。
4. Air 上临时宿主持有 app-server 管道，原 SSH 请求结束后，新 SSH 请求仍取得相同 PID、thread、turn、request ID；回答后原 turn 完成。该结果验证控制通道与 app-server 所有权分离。
5. app-server 重新启动后可以恢复已完成 thread 的历史；这不证明在途 turn 或旧请求可以复活。

实测 server request ID 为数字 **0**；实现必须区分“ID 不存在”和 `0`，并保留数字／字符串类型。去重范围还需包含 harness instance 与 connection epoch。[官方 App Server 协议](https://developers.openai.com/codex/app-server/)。

### 进程粒度与权限

建议每个 OS 用户与有效配置 profile 管理 app-server 实例，profile 包含实际二进制／版本、Codex home 与需要隔离的认证和全局配置。首期共享多个 thread 以控制开销；遇到不能逐 thread 隔离的能力再拆实例，不先按 workspace 建一套进程。

worker 的文件、Git 和资源读取接口单独验证 workspace/session scope；不暴露任意远端 shell RPC。Codex 自身 sandbox 和 approvalPolicy 由 adapter 映射，worker 管理命令不能借该映射绕过路径边界。路径需在设备端 canonicalize，并处理符号链接和 repository 边界；UI 传来的路径不是授权凭证。

暂停／退出／升级时，先使用实际支持的中断和后台工具清理操作，再处理所属进程组。Codex 工具可能有自己的进程组或终端；本轮同组 child 测试不能证明任意后代都已清理。worker 崩溃后先核实残留和原操作状态，不启动第二个副本执行同一 prompt。

## 四、存储与恢复

### 数据所有权

| 控制 Mac | owning device 的 worker |
| --- | --- |
| 设备连接设置、工作区缓存、未发送 home/session 草稿 | 工作区定义、目录／Git 默认、会话索引与已接受 launch context |
| question 答案草稿、布局、阅读位置 | 已接受队列、OperationID 与确认状态 |
| 运行状态和历史的可重建显示缓存 | event journal、snapshot revision、有效请求和输出索引 |

Codex 原生历史继续由 Codex 管理。worker journal 用来接续客户端事件和操作，不复制一个“保证恢复全部 Codex 内部状态”的数据库。客户端缓存可以重建；关闭 UI 不应清空 worker 的已接受队列。

工作区数据归属已根据用户最终的 A／B 客户端需求修订：B 的本地客户端和 A 的远程客户端访问 B worker 的同一份定义，安装应用不创建第二套本机身份或覆盖已有运行数据。设备连接列表由各客户端独立管理，不做 worker 间目录复制。多客户端一致性与并发建议及新增 Air 验证见 [需求补充](multi-client-workspaces.md)。协议客户端首次本地接入、双向修改、revision 冲突、幂等重试和有序补齐均通过；这不等于两份正式 GUI 安装的完整验收。

### SQLite 候选

`modernc.org/sqlite v1.61.1` 是本轮优先候选：Go driver、CGO_ENABLED=0，在两台 Mac 运行成功；实验内 SQLite 版本为 3.54.1。WAL、`synchronous=FULL` 和 macOS `fullfsync` 配置可读取确认，提交前 SIGKILL 回滚、提交后 SIGKILL 保留，integrity_check 通过。依赖记录见 [go.mod](research/macos-2026-11-03/sqlite-driver-go.mod) 与 [go.sum](research/macos-2026-11-03/sqlite-driver-go.sum)。[驱动说明](https://pkg.go.dev/modernc.org/sqlite@v1.61.1)。

另一个 CGo-free 候选 `ncruces/go-sqlite3` 使用 Wasm 转换与自有 VFS；维护者说明了每连接内存成本和 WAL 共享内存约束，本轮只读其资料，没有做功能或性能对比，不以未测数据给出优劣结论。[项目说明](https://github.com/ncruces/go-sqlite3)、[VFS](https://github.com/ncruces/go-sqlite3/blob/main/vfs/README.md)。

worker 数据库放设备本地私有应用目录，不放 workspace／Git 仓库／网络共享目录。WAL 的共享内存要求使网络文件系统不适合作为默认数据库位置。建议串行 writer、受限读连接和 busy timeout；批次大小、checkpoint 和输出保留上限在真实负载下测定。[SQLite WAL](https://sqlite.org/wal.html)。

关键接受、请求生命周期和队列推进在事务提交后对外确认；高频文本增量合并小批次，公布 cursor 只越过已提交事件。若 UI 希望先显示未落盘内容，需要显式区分临时序号，不能让客户端把它当恢复游标。

### 不明确的外部操作

崩溃夹具重现了：本地记录已准备 → 外部动作发生 → 尚未记下接受结果便崩溃。重启后数据库完整，也仍无法仅凭它知道外部动作是否发生。OperationID 唯一约束只能阻止 worker 内重复插入，不能让 Codex 副作用天然幂等；`clientUserMessageId` 也不能未经验证当成幂等保证。

因此，dispatched 且未获确认的操作进入 indeterminate，先与 thread／turn／item 和实际进程核实。停止、断线、休眠、重启都不自动重发。snapshot 的序号与事件尾必须在一致事务边界接续；裁剪只发生在已有可恢复快照覆盖后。

本轮进程崩溃测试不能证明断电安全。SQLite 文档区分 WAL 下 FULL 和 NORMAL 的同步语义，以及 macOS 的 F_FULLFSYNC；电源故障、磁盘满、损坏和 production driver 升级仍需独立验证。[SQLite PRAGMA](https://sqlite.org/pragma.html)。

## 五、SSH、tsnet 与 macOS 权限

### SSH 路线

系统 OpenSSH 已通过 Air 的普通用户认证和严格主机校验。它可以保留用户已有的 Host alias、agent、跳板机和认证流程；Go SSH library 能使用 net.Conn，但不能凭此承诺完整 OpenSSH 配置兼容。

建议先验证“系统 OpenSSH + 应用自有字节 bridge”的统一路线：直接 SSH 用系统网络；Tailnet 时 ProxyCommand 与应用内 tsnet 建立私有 IPC，由嵌入节点 Dial 对端。bridge 属于我们的制品，不要求系统 Tailscale。bridge 不得往 stdout 写日志，保持字节和 EOF／half-close，并约束可连接的会话／目标。ProxyCommand 与用户 ProxyJump 等配置的冲突必须明确处理，不静默覆盖。[OpenSSH 配置](https://man.openbsd.org/ssh_config)。

这条路线仍未通过集成：本轮 Python TCP bridge 对 Air 报 `EHOSTUNREACH`，而直接 `/usr/bin/ssh` 成功；系统 Python 与 Homebrew Python 的普通连接均失败。现有证据排除了“Air 整体离线”，但没有证明具体原因。不得为了得到通过结果而关闭隐私保护或绕过主机验证。

### macOS 的权限会影响连接和部署

macOS 15 起有 Local Network 权限，Apple 说明了签名身份、responsible code 和进程上下文的差异。普通 bridge 的失败与权限／运行上下文有关是待验证假设；不能仅凭 errno 作结论。正式签名包需要配置用途说明，并验证允许、拒绝和设置变更后的行为。[Apple TN3179](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy)。

补充使用独立、未签名的 Network.framework 诊断程序访问同一 Air，明确观察到 `currentPath.unsatisfiedReason=localNetworkDenied`。这确认该运行上下文受到 Local Network 拒绝，为 bridge 异常提供更具体证据；它不能证明每个 Python／正式签名服务都共享相同 TCC 归属，也没有使原 bridge 检查通过。未更改系统设置或重置授权。

同样，SSH 登录成功或 worker 属于同一用户，不表示它已得到 Documents／Downloads／Desktop 等受保护路径的访问授权。签名后的 worker／app-server／工具链要实测 TCC 和 Keychain 行为；不默认要求 Full Disk Access，不通过系统隐私重置来掩盖问题。[Apple 文件访问权限](https://support.apple.com/guide/security/controlling-app-access-to-files-secddd1d86a6/web)。

### tsnet 已验证和未验证的部分

固定 Tailscale `v1.104.0` 的 TestConn、TestStartStopStartGetsSameIP 通过：两个嵌入节点在本地测试 control / DERP 下传输、探测，并验证节点 state 重用。未调用系统 tailscale，未向用户真实 tailnet 注册节点。

真实 Tailnet 的登录、ACL 可见 peers、到 Air 的 SSH、DERP／peer relay 路径与 RTT 仍未验证，需要用户在后续原生节点登录流程中接入测试 tailnet。TSMP 成功而 Endpoint／DERP 字段为空在本轮受控测试中确实出现，进一步说明不能凭空字段推断直连；路径型探测须另验证。

用户明确将真实 Tailnet 验证留到稍后；本轮没有注册真实测试节点。新增的官方 tsnet 受控入站测试已验证 A↔B、WhoIs 后的夹具授权拒绝、二进制／半关闭和未完成连接主动清理。相邻 herdr-gpui 的 fork 入站与 SSH bridge 已只读核对，适配范围和未决项见 [入站专项报告](tailnet-inbound-research-2026-11-03.md)。

## 六、MyGo 原生体验

原生 fixture 使用固定 `v0.7.4`，渲染 5,000 条示例历史时暴露 158 个可访问节点，取得真实 native 内容截图；这证明当前场景采用了虚拟化，不能替代 CPU、RSS 或持续流式负载基准。中文已提交文本、Shift+Enter 换行、Enter 发送／清空通过，未使用 HTML 或 WebView 绘制该窗口。

第一次 fixture 中输入已清空，但提交计数标签未即时刷新。显式使用持久 `ui.Services.Invalidate()` 调度外部状态更新后，通过相同检查。生产界面仍应在 worker 接受后经 `Window.Update` 发布状态；输入处理不持有过期 Element，也不把按键触发当运行接受。[MyGo Services](https://github.com/egoist/mygo/blob/v0.7.4/ui/services.go)、[Views](https://github.com/egoist/mygo/blob/v0.7.4/docs/ui/views.md)。

上游真实屏幕测试未观察到窗口可见并超时；其局部 PASS 不算屏幕验收。原生 fixture 使用框架自动化接口输入已提交文字，不经过物理输入法候选窗口。中文 IME 组合期间 Enter 不误发、VoiceOver 实际朗读、跨消息选择和双会话持续 streaming 仍需真实窗口验收。

## 七、可进入实施规格的内容

可以围绕以下顺序写实施规格：用户级 worker 服务与身份／版本握手 → Codex adapter → 已提交事件／快照与操作状态 → SSH 控制通道 → 原生会话投影与交互。

最先需要补齐的验收门槛是：正式签名 bundle 的后台项目与隐私授权、SSH bridge／tsnet 的完整链路，以及真实输入法和 VoiceOver。worker 自身崩溃、磁盘故障和升级回退需故障注入；设备注销、休眠和重启需在不影响日常工作的测试时段验证。本轮没有把这些未测场景标记为已支持。

补充结果：受控 OpenSSH＋官方 tsnet 完整字节链路、两端 ad-hoc SMAppService 和 SQLite 故障注入已通过；Go 1.27 将技术最低版本限定为 macOS 13。剩余正式制品与用户延期的验收、Keychain locked 的实际限制、虚拟化选择扩展均在最终报告分开记录，不继续列为未查明的调研问题。
