# Harness 中立模型与适配边界

状态：2026-11-07 已实现首轮 harness 中立边界。生产仍只注册 Codex；独立内存驱动仅用于多 harness 契约测试。本文“当前实现”列出实际接口和兼容行为，后续详细内容模型与接口草图仍是设计建议，不能作为已支持能力的清单。最初研究依据为 [Codex 0.166.0 schema](../design/protocol/codex-0.166.0/inventory.json) 和[官方 App Server 文档](https://developers.openai.com/codex/app-server/)。

Codex adapter 启动时检查当前安装导出的协议形状和实际能力，不再按单一 CLI 版本白名单拒绝正常升级；0.166.0 保留为冻结录制基线，0.167.0 已通过真实 App Server 集成测试。缺少可选能力时显式关闭，必需上下文／安全契约不兼容时拒绝启动。详见 [本机版本与更新](local-worker-updates.md)。

运行边界已由用户确定：采用自有完整 worker，取消 herdr-core 依赖。Codex adapter 位于 owning device 的 worker 内；客户端通过版本化 worker 协议接收中间结构，不自行持有远端 app-server 连接。

首期执行设备限定为 macOS。最新可行性实验见 [macOS worker 技术验证](macos-worker-research-2026-11-03.md)；研究夹具与当前生产实现分别验证。

## 目标

自动会话命名使用可选 `harness.TitleGenerator`，由 owning worker 保存任务并接纳标题结果；具体模型选择、辅助进程与认证留在 adapter。当前实现和验证边界见 [自动会话命名](session-titles.md)。

工具图片与文本文件通过可选的 `StartOptions.ImportResource` 回调导入 owning worker 的资源仓库；adapter 翻译编码与源资源身份，共享事件只携带不可变附件引用。文件路径范围、快照保存与独立传输由 worker 负责，详见[工具图片结果](tool-image-results.md)与[工具文件结果](tool-file-results.md)。

让原生 UI 渲染稳定的会话内容、操作状态、变更和待处理请求；Codex 的 JSON RPC 方法、wire union、数字／字符串 request ID 及响应编码保留在 Codex adapter 中。接第二个 harness 时增加 adapter 和必要能力，不复制 Codex 的整个协议做“通用协议”。

中间层分成四项：

1. 领域实体：设备、工作区、会话和被接受的运行上下文。
2. 语义事件：有标识、有序、可增量更新的内容与生命周期。
3. 状态投影：供 UI 查询的消息、工具组、pending 请求、plan、changes 和统计。
4. 操作与能力：当前 harness／模型／环境实际允许什么，以及用户操作是否被接受。

这些不是一个 `role + text` 消息数组，也不是让 UI 对 `map[string]any` 自行判断 Codex 类型。

## 已确认的依赖边界

```text
原生 UI／应用层
        ↕ Shioji 的版本化命令、事件、快照与能力描述
Worker 编排／通用领域模型
        ↕ harness 中立的接口
具体 Harness Adapter（双向翻译）
        ↕ 该 harness 的协议、配置、错误与进程接入
Codex app-server／后续 harness
```

中间层定义产品语义：提交输入、引导当前执行、中断、处理请求、计划、用量、历史分叉与恢复等。它不以某个 harness 的 RPC 方法、枚举或对象形状作为自己的定义。执行、连接、投递、请求和呈现状态继续独立；上一轮显式状态重构不表示 harness 解耦也已经完成。

翻译层属于具体 adapter，必须双向工作：

- 出站：把中立输入、附件引用、权限意图和选项映射成该 harness 实际支持的命令；保留提交结果未知与明确拒绝的区别。
- 入站：把该 harness 的事件、请求、结果、用量与错误转换为中立语义；原生 request ID、响应编码和专有状态机留在 adapter 内。
- 生命周期：把该 harness 的启动、连接、执行代理、历史操作及恢复机制封装在 adapter 后面。Worker 继续拥有设备端的接纳、队列、持久化、进程所有权与安全边界，不能把这些权威责任下放给 UI 或外部进程。

组合／注册入口可以选择具体 adapter；共享编排仅依赖中立契约。仅增加一个包住 `codex.Runtime` 的接口还不够：如果签名依然传递 Codex 的附件、错误或执行代理类型，或调用方仍须识别 Codex 状态与请求格式，边界仍未成立。

能力是差异的明确表达，不要求所有 harness 实现相同功能。引导、中断、计划、分叉、原位重开、恢复及模型选项分别按实际支持情况声明。不支持的操作明确禁用或返回中立的“不支持”错误；不得把引导实现成取消后重发，也不得把分叉能力冒充原会话重开能力。

模型、推理配置、速度偏好和权限控制使用中立语义或有类型的选项描述。具体可选值及映射由 adapter 提供，不让通用模型固定携带 Codex 的 `serviceTier`、`SandboxPolicy` 或 `collaborationMode` 结构。没有对应能力的 harness 不显示这些选项。名称、图标、型号等目录数据可以包含具体 harness 的内容；共享 UI 通过相同结构展示，不硬编码产品名称或按名称切换协议逻辑。

不透明来源标识只用于关联和交还对应 adapter。共享代码不解析其格式，也不把完整原生 payload 放进 `any`、任意 JSON 或 `harnessOptions` 来规避隔离。受限诊断材料与中间事件分开保存，不供普通 UI 或业务状态机解释。

## 当前实现与验证

| 职责 | 实现边界 |
| --- | --- |
| 设备侧中立接口 | `internal/harness/harness.go` 定义 Adapter、Runtime、中立附件、历史准备／激活、投递错误与不支持错误；不导入具体实现 |
| 通用目录与能力 | `domain.HarnessInfo` 分别携带模型、权限选项、Plan 和执行／历史能力；不合并不同 harness 的选项 |
| Codex 双向翻译 | `internal/harness/codex/adapter.go` 封装 app-server 和 Executor，协议状态、审批原始决策及来源响应保留在该包 |
| Worker 组合入口 | CLI 和 `internal/worker/defaults.go` 注册实际 adapter；共享编排只依赖中立契约 |
| 会话路由 | 创建时持久化 HarnessID；后续提交、队列、恢复、分叉和重开按该身份路由，未知或缺失实现明确失败，不使用设备默认实现替代 |
| 生命周期与安全 | Worker 继续拥有接纳、幂等、队列、进程归属、停止证明和持久化；adapter 只报告允许的会话事件，不能更新 Worker 的工作区／会话身份 |
| 历史操作 | Worker 持久化准备结果并原子接纳激活；Codex 内部执行 fork、释放和切换。Restart 与 Fork 独立声明，不强制其他实现使用新来源 ID |
| 宿主消息回传 | Adapter 将宿主 IPC 转为 `IncomingMessage`；worker 校验当前执行代次、会话和 opaque 历史来源后接纳到持久队列，不绕过暂停、权限或执行管理 |
| 原生呈现 | 运行菜单和设备信息从目录展示名称、图标及选项；已有会话固定 Agent；不支持的操作禁用或隐藏 |

`RunConfig` 中 Plan、Fast、effort 和权限 ID 表达 Shioji 的产品选项；具体目录及原生映射由 adapter 决定。不支持这些配置的驱动不显示对应控件。PermissionChoice 的名称和说明来自 adapter，原生沙盒结构不进入通用配置。内存驱动使用与 Codex 不同的模型／权限并省略 effort、Fast 和 Plan，以检验这一约束。

Codex adapter 为所属运行实例提供私有 Unix socket，兼容 Ryugi 1.58.0 已使用的桌面 IPC（`tools/list` 与 `send_message_to_thread`）。仅该子进程获得宿主连接环境；App Server 继续使用独占 stdio。Ryugi 的源码、安装内容和配置保持不变。环境需要 Ryugi 原本依赖的 Node；没有 Node 时仍可正常使用 Codex。此 IPC 是版本固定的兼容接口，不是通用插件框架，也不开放 App Server API。退出关闭 socket；旧历史／旧执行代次的回调被拒绝。回执在队列持久化后返回，同一 call ID 的重试复用已接纳操作；Ryugi 另起 call ID 的重试不具有端到端恰好一次保证。暂停队列保持暂停。

验证原版 watcher 可运行 `SHIOJI_RYUGI_ROOT=/absolute/path/to/installed/ryugi go test ./internal/worker -run TestInstalledRyugiWatcher -count=1`；测试只在独立 `CODEX_HOME` 和临时项目中信任并执行已安装脚本，使用本地模型服务，不修改用户或插件配置。

兼容规则：

- Worker 协议为 **major 2 / minor 11**。minor 10 增加多 harness 目录，minor 11 增加文件引用和 typed Skill 目录／输入（见 [输入框引用](composer-references.md)）。保留顶层默认目录投影供旧客户端读取，新客户端按 HarnessID 选择独立目录；旧 worker 的能力兼容限于已协商的 minor <= 9 单运行时协议。
- 已保存的 `ThreadID` 继续是不透明来源引用，不因改名重写数据库。历史阶段名 `forked` 保留既有存储含义，表示已有可重试的准备结果，不要求其他 harness 真正创建 fork。
- 旧提交的空 HarnessID 不改变其操作摘要；Worker 将默认身份作为接纳元数据写入新会话。已有会话身份不会随首页或设备默认变化。
- 根目录 catalog 的归属保存在 `catalog-owner`，与当前默认选择独立。无归属文件但存在旧根进程记录时，由组合入口明确识别旧单 Codex 布局。
- 首页按工作区和 harness 保存运行配置；离线首帧、目录暂不可用或应用重启不清空已选配置、普通草稿与附件。
- 每个启动实例提供独有且稳定的 Done channel，Worker 用它识别生命周期，避免对接口实现值施加 Go 可比较性要求。

验证包括：

1. `internal/architecture` 的 AST 依赖检查禁止 domain、protocol、app、ui、共享 worker 以及中立 harness 包导入具体 adapter，组合入口除外。
2. 独立内存驱动覆盖多目录／会话路由、未知身份、能力拒绝、未知投递保留输入与暂停队列、越界事件、不可比较的实现值、独立启动超时、稳定进程归属，以及仅支持原会话重开的实现。
3. 应用测试覆盖配置独立性、保存／重载、首次离线渲染、目录到达与草稿保留；原生 UI 测试覆盖实际目录菜单和能力对应控件。
4. Codex 保留协议测试及真实 app-server／Worker 集成测试。集成测试使用本机 Codex 二进制与测试模型服务，不等同于第二个真实 harness 或真实模型端到端验收。

本轮不包含第二个真实 harness、动态插件装载或通用工作流引擎。下文建议结构与当前 Go API 不一致时，以实现及上述边界为准。

## 身份与运行上下文

| 实体／标识 | 含义与规则 |
| --- | --- |
| DeviceID | 稳定的执行设备身份；地址和连接方式另存 |
| WorkspaceID | 一个设备上的目录集合，显式 primary folder 和 Git 默认 |
| SessionID | Tahuna 自己的会话 ID，不等同于 wire thread ID |
| HarnessID | 首期 `codex`；会话创建后固定，不跟随首页 picker 改变 |
| RuntimeInstanceID | 持有 harness 进程／连接的运行实例；重建时变化 |
| SourceRef | adapter 保留的 source thread／turn／item 和不透明 request key |
| OperationID | 用户操作的应用 ID，贯穿提交、确认、失败和核实 |
| StreamID + Sequence | worker 分配的事件顺序和恢复游标；不是 Codex 原生序号 |

`LaunchContext` 至少保存 owning device、workspace、harness、真实 working directory、实际接纳的 additional directories、仓库／worktree 身份、实际权限和生效运行配置。远端 path 是带 DeviceID 的资源，不能传给控制 Mac 的 `os.ReadFile`。

Workspace 的权威定义和 session 索引归 owning worker 保存。A 远程访问 B 与 B 本地访问取得 B 的同一份定义；B 安装客户端时复用既有 worker 和数据目录。客户端独立管理主动连接的设备，不交换连接列表，也不在 worker 之间复制其他设备的目录。工作区集合需要版本与变更订阅，使本地和远程视图收敛到相同已接受状态；断线缓存明确标记为上次已知状态。具体一致性与并发建议见 [多客户端需求](multi-client-workspaces.md)。

添加 workspace folder 不自动授予 agent 访问；adapter 把已选择目录映射到该版 harness 的 roots、sandbox 和 permission 能力，回传实际接受的 context。不能用 workspace folder 数组替代权限模型。

source identifiers 可以作为不透明值存在 domain；Codex 的 `ThreadItem`、`ServerRequest`、`SandboxPolicy` 等 wire 类型不得离开 adapter。JSON RPC 的 ID 必须保留原始类型，不能将数字 1 和字符串 `"1"` 合并；数字 0 是有效 ID，不能用真假值判断是否存在。

## 内容模型

建议 `TimelineItem` 是带稳定 ID 和 `ContentRevision` 的有类型 union；`Message` 只是其中一类。以下名称表达语义，最终 Go 命名可在实现时调整。

| 中间类型 | 主要内容 |
| --- | --- |
| Message | user／assistant、文本块、图片／音频／资源引用、Skill／文件引用、交付状态 |
| Context | hook 等注入上下文，有来源，默认轻量折叠 |
| ReasoningSummary | harness 明确公开的摘要或状态；不推测隐藏思维链 |
| ToolActivity | 工具名、typed 参数概要、状态、输出引用、退出／错误信息、父子关联 |
| FileChange | device-scoped 路径、操作、patch／revision、状态；显示与应用结果分开 |
| ResourceResult | text、image、audio、resource、JSON、unknown 等受限内容 |
| RequestAnchor | pending request 的来源位置，处理后同 ID／位置变为 receipt |
| ReviewBoundary | 审阅开始／结束与原始结果内容 |
| CompactionBoundary | 压缩状态与范围，不能当普通 assistant 回复 |
| Notice | 有级别、来源和作用域的警告／说明 |
| UnsupportedContent | 来源类型、可读概要及受限诊断引用 |

ToolActivity 保留 command、MCP、client tool、web search、delegation、media generation、wait 等 detail variant；工具结果可能是混合内容，不能强制变成字符串。子 agent 关联保留 source child thread 和父子关系，不自动创建应用 workspace，也不把子 agent 输出重复拼成主 agent 正文。

`Reference` 指向 file／skill／URI 等资源，含 owning device、完整目标和显示名称；胶囊只负责展示。Markdown 的本地相对链接／图片也通过此资源解析器访问 owning device；不能直接使用控制 Mac 的 `markdown.FileImages(remoteDir)`。外部 HTML／MCP UI 默认原生资源入口，未经产品决定不嵌入浏览器。

## 事件和投影

建议事件 envelope 带 `SchemaVersion`、SessionID、source、StreamID／Sequence、观测时间和有类型 payload。观测时间不是服务器执行时间，跨设备时钟也不构成全局顺序。

| 事件 | 投影操作 |
| --- | --- |
| SessionSnapshot | 原子接纳一个 revision 的会话状态，再处理更高序号事件 |
| TurnStarted／TurnEnded | 创建／结束对应 turn，不从最后一句文字推断 |
| ItemUpserted | 同 ID 创建或补齐内容，固定原始事件顺序 |
| TextAppended／OutputAppended | 只更新目标 item 和 revision，重复事件不重复追加 |
| ItemFinalized | 用最终 payload 替换／核对内容，纠正 streaming，不追加第二份正文 |
| RequestOpened／RequestResolved | 更新 pending 集合和原位来源／回执 |
| PlanReplaced | 替换该 session 最新结构化步骤，不插入正文 |
| ChangesReplaced | 替换指定 turn／revision 的聚合变更；不与 item patch 重复计数 |
| UsageObserved | 保存统计值、范围、来源、计数 epoch 和完整性 |
| OperationChanged | 更新发送、介入、中断、回答操作的确认状态 |
| RuntimeConnectionChanged | 连接视图变化，不擅自改 turn 结果 |

UI 消费 projection patch，渲染由各自稳定 ID 定位的可见项。adapter 处理 wire 乱序、重复 final、历史 hydration 和未知事件；应用 reducer 保持确定性。最终状态到达后若 delta 又来，按 source revision／生命周期规则忽略过期数据或报告诊断，不重开已完成 item。

工具组是投影层派生结构：连续工具构成一组，最新组保持展开；后续非空 agent prose 首次出现时只自动折叠前组一次。tool completion、late output、用户草稿不触发折叠。用户显式展开状态独立于自动折叠标记，导航和 rerender 不反复关闭历史组。

工具失败属于具体调用，不能推导整轮失败。命令旁原位显示非零退出码；没有退出码的失败、拒绝和中断显示对应状态，展开查看实际输出。折叠组以“Ｎ 项未成功”保留轻量提示，不显示整组“执行失败”。当前调用全部结束不意味着工具组封口；只有后续非空正文或确认的终止轮次，才允许在全部调用已结束时显示收尾。全成功显示“已完成”，包含未成功或未知结果显示中性的“调用结束”。整轮由 harness 明确报告 failed 时，回复 footer 显示“本轮失败”及实际原因；单条调用的重试不抹除之前的退出信息。

建议用以下独立状态描述界面，而不是单一 `SessionStatus`：

```text
Connection: connected | reconnecting | disconnected
Execution: idle | running | completed | interrupted | failed | unknown
Attention: pending approvals 集合 + pending questions 集合
Operation: prepared | dispatched | accepted | rejected | indeterminate
```

运行中可以有非阻塞问题；断线时保留最后确认的 execution，并给出当前确认程度。发出 interrupt 不等于 execution 已 interrupted。一个 turn 完成不等于整个任务／session 永远结束。

## 请求模型和回答

`PendingRequest` 具有独立 RequestKey、session、source turn／item、阻塞性、能力来源、状态和 typed body。UI 可把审批／问题合成 pager，但 domain 保持生命周期独立。

| body | 内容与响应 |
| --- | --- |
| ExecutionApproval | 命令／目录／文件／网络上下文和 harness 明确允许的 decisions |
| PermissionGrant | 请求的权限集合、可选择子集、支持的 grant scopes；拒绝约束另存 |
| QuestionSet | 多个 question、稳定 question ID、字段类型、选项、自定义答案支持及约束 |
| ExternalInteraction | URL／外部认证／扩展流程，不当作普通单选问题 |

表单类型保留 text、secret、integer、number、boolean、single choice、multi choice、object 等真实类型。QuestionSet 的回答是结构化值，数字和布尔不能转成展示字符串后丢失类型；adapter 再按该请求精确编码成 wire response。

Codex `item/tool/requestUserInput` 的普通问题没有显式 multiple 字段，不能用 answers 的字符串数组推断多选。该版本的 protocol-backed 多选来自 MCP elicitation 的 array／enum schema。MCP 扩展 schema 未支持时明确显示受限流程，不能给未知字段编造选项。

draft key 是 `(SessionID, RequestKey, QuestionID)`，不是当前页面索引。pager 的 page key 是稳定 question／approval identity；新请求只追加，不替换当前项或抢焦点。一次提交当前 request 的完整答案，成功只移除该 request 的页。

审批 decision 与 grant lifetime 分开；`允许本轮`／`允许本会话` 分别决定 scope，不能作为权限复选项。响应只含所选且确实请求过的权限；修改 Permission picker 不回答任何现有审批。

请求至少有 pending／submitting／answered／invalidated／unknown 状态。离线可编辑 draft，不能发送。若服务端先 resolved，则 invalidated，即使表单本地已填写；已提交的 receipt 不能被迟到的 resolved 覆盖为空。resolved 若没有可确认的用户回答，只显示“已结束／已失效”等真实结果，不编造回答回执。

Codex `agentMessage.delivery=async` 的 questions 没有 JSON RPC request ID，应进入另一种异步交互关联，回答路径单独验证；首期若不能确认该路径，保留可读问题和明确的不支持状态，不伪造 server request response。

## 操作接口草图

以下仅表达职责，不是可编译的最终 API；未列出的参数类型也需要在实施规格中确定。

```go
type HarnessAdapter interface {
    Describe(context.Context) (HarnessDescriptor, error)
    CreateSession(context.Context, LaunchContext) (AcceptedSession, error)
    LoadSession(context.Context, SourceSessionRef) (SessionSnapshot, error)
    StartTurn(context.Context, TurnSubmission) (SubmissionReceipt, error)
    Steer(context.Context, SteerSubmission) (SubmissionReceipt, error)
    Interrupt(context.Context, InterruptRequest) (OperationReceipt, error)
    Respond(context.Context, RequestResponse) (OperationReceipt, error)
    Subscribe(context.Context) (EventStream, error)
}
```

`HarnessAdapter` 是 worker 内部接口。`Subscribe` 是适配器当前连接上的事件流，不承诺持久 cursor；snapshot／journal replay 是 worker 对应用提供的另一层版本化协议。已接受队列由 worker 管理，不要求每种 harness 提供队列 wire 方法。客户端仍持有未发送草稿和问题答案草稿，接收 worker snapshot／patch 后维护本地显示投影。

`SubmissionReceipt` 含 OperationID、source turn ID、接受状态与生效配置；adapter 无法确认的响应不得写 accepted。非空新建提交发生在 app service 的完整流程里，只有 `CreateSession` 成功仍不足以清掉 home prompt；首轮明确被拒绝时保留草稿，并清理／标记已确认未开始的临时 thread。确认不明时保留 provisional 会话和 OperationID 用于核实，不自动销毁、重发或显示成已启动任务。

`HarnessDescriptor` 提供模型列表、具体 default、effort、service tiers、协作模式、输入格式、权限约束、history／steer／interrupt 等能力，每项 supported／unsupported／unknown。固定 agent 的已有会话只读展示 Agent；改变 model 时正规化 effort／Fast。另一个 harness 不支持 steer 时禁用该操作并说明，不能 cancel 后重新发一轮伪装 steer。

`LoadSession` 默认接回已接受的 launch context，不向 thread/resume 填入当前 workspace 最新目录覆盖历史。切分支是拥有设备与 repository 锁的 Git 操作，只在 idle、connected、无 pending 请求时执行，明确确认实际 checkout 结果后更新状态。

Go 实现建议在 adapter 内手写最小 transport／RPC dispatcher，按固定 schema 维护或生成必要的 wire structs，再转换为 domain union。生成器需验证 nullable、optional、union 和数字／字符串 ID，不能把任意 JSON Schema 到 Go 的生成结果当成已经验证的客户端。`json.RawMessage` 仅用于 adapter 的分派、未知分支和受限诊断；UI 不解析它。第二个 adapter 未出现前，不增加动态插件加载器或自定义脚本运行平台。

## Codex 类型映射

这份表覆盖冻结版本的 19 种 ThreadItem；语义结构可复用，source variant 留给诊断与 adapter。

| Codex type | 中间层去向 |
| --- | --- |
| userMessage | Message，user，保留混合输入和关联 client ID |
| hookPrompt | Context |
| agentMessage | Message，assistant；异步问题另行关联 |
| functionCallOutput | ResourceResult，关联工具与受限内容 |
| plan | 独立 Plan 文本内容；不是结构化步骤 |
| reasoning | ReasoningSummary |
| commandExecution | ToolActivity，command detail |
| fileChange | FileChange，关联 patch 和实际应用状态 |
| mcpToolCall | ToolActivity，MCP detail 与混合结果 |
| dynamicToolCall | ToolActivity，client-tool detail |
| collabAgentToolCall | ToolActivity，delegation detail |
| subAgentActivity | 子 agent 活动关联 |
| webSearch | ToolActivity，search／open／find detail |
| imageView | ResourceResult，device-scoped image |
| sleep | ToolActivity，wait detail |
| imageGeneration | ToolActivity 与生成资源引用 |
| enteredReviewMode | ReviewBoundary，开始 |
| exitedReviewMode | ReviewBoundary，结束与原始结果 |
| contextCompaction | CompactionBoundary |

11 种服务端请求的职责如下；并非每一种都生成用户表单。

| 请求 | 处理位置 |
| --- | --- |
| item/commandExecution/requestApproval | ExecutionApproval |
| item/fileChange/requestApproval | ExecutionApproval，通过 item ID 找文件／patch |
| item/permissions/requestApproval | PermissionGrant |
| item/tool/requestUserInput | QuestionSet |
| mcpServer/elicitation/request | QuestionSet／ExternalInteraction，按 schema 类型 |
| item/tool/call | 客户端工具桥；未注册工具时明确失败，不执行未知工具 |
| account/chatgptAuthTokens/refresh | adapter 认证服务；不进入会话表单 |
| attestation/generate | 可选宿主能力桥；没有提供者则不宣称支持 |
| currentTime/read | 可选时间桥，按实际能力协商 |
| applyPatchApproval | 旧协议文件审批兼容 |
| execCommandApproval | 旧协议命令审批兼容 |

85 种通知不都属于 timeline：thread／turn／item 与 request 更新投影；account／模型／配置归运行环境；fs／search／process 归对应服务；realtime 音频／SDP 是媒体通道数据；Windows、remote control 等首期未启用能力明确分类为不支持或内部诊断。现已据 [inventory](../design/protocol/codex-0.166.0/inventory.json) 建立 [逐项研究路由表](research/macos-2026-11-03/codex-routes.json)，coverage 检查通过；它不是已实现的 adapter。增加类型时检查遗漏，不能用默认静默忽略掩盖 coverage。

## 用量、时间和错误

`UsageObservation` 建议包含 `Scope`（latestRequest／turn／session）、可空 input／output／cachedInput、source、turn ID、计数 epoch 与完整性。缺字段保持 unknown；cachedInput 是 input 子集。只有满足同一线程、可靠基线和完成边界时才生成 turn aggregate；其他 harness 也必须说明自己提供的是哪种口径。

`TurnTiming` 分开记录 harness elapsed、明确等待区间和应用 observed 时间。服务端 `durationMs` 不自动等于排除等待后的“工作耗时”；缺乏完整等待边界时不能悄悄减去猜测值。历史恢复没有时间证据时显示未知，实时本地时钟只能作为当前计时，完成后用确认数据冻结。

错误规范化为 authRequired、hostKeyUntrusted、deviceUnavailable、runtimeUnavailable、unsupportedCapability、invalidInput、conflict、overloaded、protocolMismatch、storageFailure 和 executionFailure 等。保留来源错误代码／可重试性／受限诊断引用；不能把 SSH 断线当成模型失败。

## 验证契约

优先使用真实 schema 校验过的 fixtures 和回放事件测试 reducer，再进行真实 runtime 检查：

- delta → final 一份内容；迟到 output 不重排 item；恢复 snapshot 后不重复回执。
- 两个 session 同时 streaming／pending，切换 UI 不停执行、不串草稿／模型／pager。
- 一个 request 多题完整提交、多 request 独立；resolved 和 submitting 竞态；多选、secret、数值／布尔类型。
- queue、steer、interrupt 的接受与终态分离；停止、断线和重启都不自动排空／重发队列。
- 没有 turn usage baseline、统计重置和断线补史时保持正确范围或未知。
- 远端 resource path 不读控制 Mac；工作区变化不扩大运行 session 的目录与权限。
- 未知 item 可读兜底，未知 server request 不默认允许；敏感参数不进入持久 raw log。

原始 wire payload 只在需要诊断或恢复的受限存储中保留，按类型脱敏并限额；不把账户 token、私钥、登录 URL 与秘密答案放进通用事件日志。中间结构自身带 schema version；新增字段可兼容读取，改语义时迁移 snapshot／journal，不能让新 adapter 用不同含义解释旧事件。

### Device runtime defaults and account presentation

Runtime options are typed Shioji launch defaults (`RuntimeOptions`), owned and atomically persisted by the device worker. Adapters declare the supported fields and translate arguments, configuration directories, environment variables, initial instructions and auxiliary title-model choices. Existing execution runtimes retain their accepted adapter; changing defaults does not stop their processes. The Codex adapter validates its allowed CLI options and reserves transport and ownership variables. Options are version-gated for older workers.

Account presentation uses a small public `RuntimeAccount` projection (authentication label, subscription label, identity and actual state). Credentials and native authentication response structures do not enter shared events or UI state. Login guidance and commands come from the adapter and are scoped to the selected device; unknown authentication state is never reported as logged in.

Model visibility, favorites and order are client menu preferences scoped by stable DeviceID and harness. They do not remove models from the authoritative catalog, modify accepted session configurations or turn a hidden model into an unavailable one.

## 应用内认证

2026-11-09 增量：独立可选 `harness.AuthenticationProvider` 提供中立的浏览器／设备验证码授权。worker 持有临时授权状态与稳定操作 ID，adapter 负责具体协议、来源登录 ID 与凭据存储；共享配置、事件与会话状态不携带具体认证结构。认证不借用会话运行时，不隐式退出已有账号，也不改变 Agent 启用选择。账号和模型经真实回读后更新，挑战不进入持久化投影。详见 [Agent 登录](agent-signin.md)。
