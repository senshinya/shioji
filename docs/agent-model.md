# Agent 中间模型与 Codex 适配边界

状态：2026-11-03 技术草案。用户要求中间结构能够支持后续 agent；本文中的字段和接口是建议，尚未实现。初始事实依据为 [Codex 0.166.0 schema](../design/protocol/codex-0.166.0/inventory.json) 和[官方 App Server 文档](https://developers.openai.com/codex/app-server/)。

运行边界已由用户确定：采用自有完整 worker，取消 herdr-core 依赖。Codex adapter 位于 owning device 的 worker 内；客户端通过版本化 worker 协议接收中间结构，不自行持有远端 app-server 连接。

首期执行设备限定为 macOS。最新可行性实验见 [macOS worker 技术验证](macos-worker-research-2026-11-03.md)；这些研究夹具尚未实现本文件的生产中间协议。

## 目标

让原生 UI 渲染稳定的会话内容、操作状态、变更和待处理请求；Codex 的 JSON RPC 方法、wire union、数字／字符串 request ID 及响应编码保留在 Codex adapter 中。接第二个 harness 时增加 adapter 和必要能力，不复制 Codex 的整个协议做“通用协议”。

中间层分成四项：

1. 领域实体：设备、工作区、会话和被接受的运行上下文。
2. 语义事件：有标识、有序、可增量更新的内容与生命周期。
3. 状态投影：供 UI 查询的消息、工具组、pending 请求、plan、changes 和统计。
4. 操作与能力：当前 harness／模型／环境实际允许什么，以及用户操作是否被接受。

这些不是一个 `role + text` 消息数组，也不是让 UI 对 `map[string]any` 自行判断 Codex 类型。

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
