# 显式生命周期与等价性验证

基线：`6e8bcb0070e490c5ef8cf2a9a4ce6572b3d1a638`。

本次是行为保持重构。保持 JSON 状态值、数据库格式、协议版本、事件顺序、错误边界、草稿保留规则、键盘交互和动画时序不变。不引入通用状态机框架，不重新创建验收页、截图或录像。

此前清单中的 64 组是生命周期分组，并不都是尚未建模的有限状态机。单一二态开关、已由函数调用顺序表达的启动／关闭流程，以及外部状态的直接投影，不额外包一层框架。需要消除的是多布尔组合、字符串状态判断重复、nil／时间戳隐含阶段，以及跨回调分散的状态转换。

## 实现约束

- 业务状态在 domain 使用有名称的字符串类型，保留原有线格式；各正交维度独立，不能用一个总枚举替代连接、投递、执行、请求和呈现。
- 共享业务守卫由语义明确的状态方法表达，保留不同操作实际不同的许可条件。
- UI 使用模块内的小型阶段枚举与事件方法；状态转换拥有原有的时间戳、测量值和来源身份。焦点请求、取消函数、连接句柄等一次性效果或资源不充当阶段枚举。
- 外部观察与本地命令分开：投递未确认不能变成执行失败，运行结束不能跳过 UI 输出尾部，旧 epoch 的结果不能改变当前状态。

## 等价性标准

用同一份独立于新实现的事件探针，分别执行旧基线和工作树代码，逐观察点比较域对象 JSON、持久化结果、错误、可见文本、焦点、布局几何和复制结果。只归一化随机标识、临时路径和真实墙钟字段；不归一化状态、事件类型、revision、队列顺序或业务文本。

另外运行既有测试、针对显式阶段的转换测试、竞态检测、冻结稿校验及原生构建。该验证针对列出的有限轨迹与既有测试覆盖，不宣称证明所有可能的并发调度等价。

进度与覆盖清单随实现更新；未完成项不得标记为已验证。

## 原清单逐项处置

“显式化”指本次替换组合状态或集中状态守卫；“保留”指流程原本已有明确的事件／资源所有权，或只是彼此独立的二态偏好，不另造一个与真实状态竞争的缓存。

| # | 生命周期 | 处置与状态来源 |
|---|---|---|
| 1 | Worker 所有权、监听、关闭 | 保留 `Owner` 的获取／监听／关闭协议与 OS 文件锁；句柄不是额外的业务状态。 |
| 2 | Codex 初始化与能力目录 | 保留顺序初始化和失败清理；就绪结果使用 `ReadinessState`。 |
| 3 | 会话 Runtime／Executor 的创建与替换 | 保留 `dispatchMu` 下的资源创建、停止证明和替换；不缓存进程是否存活。 |
| 4 | Managed scope 的准入与围栏 | 保留持久化 generation 和二态准入；停止必须读取实际进程证明，不能靠枚举推断。 |
| 5 | Managed launch ticket | `LaunchState` 明确 prepared／registered／stopped，线格式不变。 |
| 6 | Executor 注册与 ACK | 保留注册边界和 `sync.Once` 完成通道；已看见注册包与 ACK 已完成是不同事实。 |
| 7 | 操作投递 | `OperationState` 区分 accepted／dispatched／confirmed／rejected／indeterminate／abandoned。`DeliveryOutcome` 统一外部调用结果的投递与执行投影。 |
| 8 | 会话／轮次执行 | `ExecutionState` 和语义守卫；执行、连接、请求、呈现仍为独立维度。 |
| 9 | 审批与问题生命周期 | `RequestState`；删除 adapter 的重复 `responded` 标记，submitting 即已提交本地响应。resolved、answered、invalidated 保持区别。 |
| 10 | 流式 Item 与最终载荷 | 保留 `Feed` 的结构化事件分派与单一 `Final` 权威标记；最终载荷仍覆盖增量。 |
| 11 | 单轮 Token 用量 | 每轮 `turnUsage` 集中 baseline 与是否收到 usage；缺失、零值、计数回退仍有区别。 |
| 12 | 队列暂停与推进 | 保留设备端暂停记录和串行调度守卫；不会把会话执行与队列暂停合成一个总状态。 |
| 13 | 队列条目 | `QueueItemState` 与 `Reserved()`；空字符串仍代表等待，保持数据库和协议兼容。 |
| 14 | 恢复检查／待处理／就绪 | `RecoveryPhase`；继续资格和停止证明保持独立。 |
| 15 | 自动恢复去重 | 保留显式 `(Revision, Generation)` 尝试记录，不把去重缓存做成生命周期。 |
| 16 | 旧工具进程的归属与停止 | 保留真实 PID、启动身份及停止验证；不以本地 UI 阶段替代进程证据。 |
| 17 | Git 共享检出与上下文变化 | 采用 `BlocksCheckout()`、请求与队列语义守卫；原锁、事务和外部 Git 复核边界不变。 |
| 18 | 历史分叉／重开事务 | `HistoryPhase` 明确 prepared／forked／complete／failed；事务和幂等查询顺序不变。 |
| 19 | 自动标题与手动覆盖 | 保留 source title、revision 和单一覆盖策略，不引入第二套标题状态。 |
| 20 | 工作区／会话归档 | 保留显式归档动作和二态持久化属性，继续使用实际运行／请求守卫。 |
| 21 | 本地／SSH worker 引导 | 保留 probe、复用、部署、握手的顺序协议；不增加自动重放。 |
| 22 | IPC 握手与命令回执 | 保留协议帧、epoch 和请求 ID 匹配；连接由实际句柄与关闭信号拥有。 |
| 23 | 重连、快照与就绪 | 就绪采用类型化状态，历史水合另用显式阶段；连接存活仍取真实连接结果。 |
| 24 | 设备 source 替换 | 保留 source token 与取消上下文，旧来源无法更新新来源。 |
| 25 | Job 准备／未发送／结果未知 | 保留 `Prepare` 与协议错误分类；未发送和投递结果未知仍走不同草稿路径。 |
| 26 | 事件订阅与 cursor | 保留 generation／sequence 的顺序协议和取消所有权；不新增并行游标状态。 |
| 27 | 历史分页与首次水合 | `historyLoadPhase` 替换 loaded／opening 双表，包含失败后仍需折叠历史的 deferred 阶段。 |
| 28 | 历史缓存与重载 | 保留 LRU 访问序号、保留请求和显式重新加载；缓存成员关系不是新的会话状态。 |
| 29 | 客户端提交与草稿 | 保留 `ApplyOperation` 事件归约器和兼容的 Pending／Resolved 持久化格式；消费类型化操作结果，不新增另一份提交状态。 |
| 30 | 附件准备与设备绑定 | 保留上传任务的 token、目标身份和确认位；准备完成与目标匹配是独立条件。 |
| 31 | 分块附件传输 | 保留 offset／checksum／Complete 的明确回执协议，不缓存一个可能与回执冲突的阶段。 |
| 32 | StateWriter | 保留版本排序与互斥写入协议；clean／dirty 由 latest／written 的关系决定，Close 由 `sync.Once` 拥有。 |
| 33 | 通知去重与投递 | 保留事件去重、真实通知对象和取消句柄，生命周期由原 API 明确拥有。 |
| 34 | 导航与双栏焦点 | 保留主／副会话身份和独立焦点；页面选择是持久化导航值，不与执行状态耦合。 |
| 35 | 发送／排队／插队／停止 | 保留 `sendMessage`／`interrupt` 和键盘分派；实际执行许可使用类型化守卫。 |
| 36 | 队列编辑与被置换草稿 | 保留明确的 Edit／Finish／Cancel 动作和 token；普通草稿与编辑草稿仍独立。 |
| 37 | 队列拖拽 | `queueDragPhase` 明确 armed／dragging／saving；revision 与 epoch 仍校验来源。 |
| 38 | 队列入场与高度同步 | `queueArrivalPhase` 明确等待确认、等待行、两者均等待及动画中；任一到达顺序都只启动一次时钟。 |
| 39 | 历史原位编辑意图 | 保留 `HistoryEdits` 和 `HistoryActions` 的幂等操作身份；不把 UI 退出动画当成已投递。 |
| 40 | 历史退出与滚动交接 | `historyExitPhase` 与独立的 `historyAnchorPhase`；来源和目标可按任意顺序完成锚定。 |
| 41 | 发送消息入场 | `sentMotionPhase` 区分等待消息行与动画中；焦点请求仍是一项一次性效果。 |
| 42 | 首页会话上下文入场 | 保留会话身份与两个独立的动画时钟；已进入会话是单一导航事实，不替代 Runtime 状态。 |
| 43 | 回复渐显 | `replyPhase` 替换 live／fresh 组合，保留首次挂载前被替换的情况；Markdown 继续拥有真正的增量渐显。 |
| 44 | 回执入场与完成指示 | 保留原 receipt gate 和 Markdown 实际 reveal 回调；后台完成仍须等待可见输出动画耗尽。 |
| 45 | 工具组折叠 | `ToolDisclosure` 明确正文前后、展开折叠四态；转换集中在 Toggle／OpenHistory／ObserveProse，旧布局键保持兼容。 |
| 46 | 请求卡片出现与消失 | `requestCardPhase` 明确 hidden／active／closing；关闭完成前保留不可交互快照。 |
| 47 | 请求切页与高度动画 | `requestPagePhase` 明确初始测量、切页测量、待滑动、滑动和稳定；保留相同高度／内容时序。 |
| 48 | 新问题提醒 | 保留显式 known／unread 集合与截止时刻；提醒宽度由原生动画拥有，缩回后清空计数。 |
| 49 | 侧栏合并与分屏 | 保留 `InspectorLayout` 值对象和原生 Drag／Drop 协议；打开、布局、选中视图、拖拽是正交状态。 |
| 50 | Diff 列表与文件加载 | `changesLoadPhase` 明确首次加载／刷新；`fileDiffPhase` 明确文件等待、可用、刷新及有／无缓存失败。旧路径回执抑制与共享错误显示保持原样。 |
| 51 | 图片缩略图缓存 | `attachmentImagePhase` 明确 loading／ready／failed，加载中不参与淘汰。 |
| 52 | 图片预览与缩放 | `previewPresence` 替换 Open／Visible／MotionReady；包含首次测量前关闭，高清图仍等展开完成后切换。 |
| 53 | 当前会话查找与结果定位 | `findMode` 与 `findPlacement` 分别表达查找方式及定位阶段；等待水合、等待行、测量和定位不再依赖布尔组合。 |
| 54 | 全局多设备搜索 | 保留 query 身份和每设备结果聚合协议；旧 query 回执不能替换新 query。 |
| 55 | 原生列表阅读／发送锚点 | `listAnchorMode` 替换 pinned／pinStart／pinSent／pinAnimated，明确固定、阅读滑动、发送滑动及其待初始化阶段。 |
| 56 | 虚拟化文字选择 | 保留有名称的 retained selection 记录与 clear／retain／restore 协议；不保活整段历史。 |
| 57 | 左侧导航预览 | 保留 hover 项身份、明确的 300 ms deadline 及当前阅读位置；hover 与阅读选择保持独立。 |
| 58 | 恢复 UI | `recoveryCheckPhase` 区分 idle／waiting／visible，新的检查重新计时；显示模式复用类型化 RecoveryPhase。详情、确认与 busy 仍为独立用户动作／请求状态。 |
| 59 | Git 分支目录加载 | `gitLoadPhase` 合并 pending／loaded 双表，首次请求和保留旧结果刷新有独立阶段。 |
| 60 | 设备与管理表单 | SSH 使用 `sshCheckPhase`，包含关闭后检查仍待回执；其他表单保留独立窗口显隐、单一请求 busy 与明确 token 身份，不合成总状态。 |
| 61 | 运行配置菜单 | `runMenuPhase` 合并 open／page 为 closed／main／models／efforts；传给原生 Popover 的 bool 只作临时适配。 |
| 62 | 设置窗口与字体加载 | 保留窗口创建／复用／关闭协议与字体结果通道；页面选择和搜索条件是独立值。 |
| 63 | 通知授权测试 | 保留单一 notificationPending 及真实 API 回执；授权成功／拒绝由系统返回。 |
| 64 | 复制反馈与数字翻动 | 保留明确的反馈截止时间和原生数字动画；不增加与动画采样值重复的业务状态。 |

## 验证覆盖

差分探针不读取新增 phase 字段，相同源码会注入旧基线的临时工作树运行，结束后删除。观察项包括：

- 提交接受、投递、确认、拒绝、结果未知、放弃及迟到回执；普通草稿与队列编辑的保留。
- 持久化队列预留、未知结果、重启恢复；执行状态与待处理请求的提交守卫矩阵。
- 请求响应／服务端解决／失效的不同顺序和重复事件；用量缺失、提前到达、部分计数及计数回退。
- 亮暗模式、减少动态效果；查找、问题追加／切页／折叠／移除；队列行与确认的不同到达顺序。
- 60 Hz 时钟逐帧推进下的回执到正文交接、代码块渐显、编辑重开、分叉退出、图片打开关闭、原生阅读／发送锚点。
- SSH 检查取消、待回执时重开和错误回执；文件 Diff 首次加载、刷新失败、缓存保留、迟到／错路径回执与返回。

按帧记录可见文本、布局、焦点、草稿、复制结果与 RGBA 像素 SHA-256；图片只在内存中渲染，不保存媒体。仅归一化从结构化标识字段发现的随机标识及其引用、测试临时路径，以及 Store 自动生成的 `sentAt`。正文中的普通 UUID 字面量、正文／错误中的日期、固定的 harness 开始与完成时间均保持原值。

短序列穷举另外检查历史水合、工具组、队列入场与回复首次挂载的旧布尔模型：合计 **32,256 次转换比较**。这与旧／新真实组件差分互补，避免只比较最终状态。

## 重跑

```sh
python3 -B scripts/test_state_equivalence.py
python3 scripts/verify-state-equivalence.py --baseline-tree /path/to/clean-baseline-worktree
go test ./...
go test -race ./...
go vet ./...
(cd third_party/mygo && go test -race ./ui ./ui/markdown)
python3 scripts/verify-design-freeze.py --repository
bash packaging/build-dev.sh
```

基线工作树必须干净且 HEAD 严格匹配文首提交。对照运行会临时复制 5 个探针／辅助文件，完成后自动移除，不复制候选实现。重跑时自行建立该基线的临时工作树即可。

## 本轮检查结果

- 旧基线／候选版本：**883 个检查点一致**，含每帧内存像素摘要与交互状态。
- 有限序列模型对照：**32,256 次转换比较通过**；另有预览首次测量前关闭、恢复检查重新计时与列表锚点测试。
- 验证器本身：4 项归一化回归检查通过，业务时间戳变化不会被忽略。
- `go test ./...`、`go test -race ./...`、`go vet ./...` 通过；最后补充的 UI 阶段再次执行了针对性普通／竞态测试。
- MyGo `ui`／`ui/markdown` 竞态检查通过。
- 冻结稿：6 个仓库内冻结文件匹配基线。
- `packaging/build-dev.sh` 构建和 `codesign --verify --strict` 通过。
- 本轮临时对照工作树已移除，没有保留验收页面、截图或录像。原有工作树与运行中的 worker 未改动。

这些结果证明列出的有限轨迹与转换模型一致，不构成所有外部进程行为和所有并发交错的形式化等价证明。
