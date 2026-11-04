# 本机闭环开发基线

2026-11-04。按照首期实施规格 §14 的阶段 1 开始实施。当前可构建、可运行；**不是完整冻结交互或正式发布验收通过的声明**。后续阶段继续以原实施规格为准。

## 已接通

- 单 Go module；独立 `shioji`／`shioji-worker`；MyGo 原生窗口、Markdown、虚拟列表和输入。没有 WebView 或 herdr-core 运行依赖。
- 同用户单 worker：0700 私有目录、0600 socket／数据库、文件锁和本地 peer UID 检查。第二个 owner 不替换活跃 socket。
- SQLite 事务接纳、稳定 DeviceID、工作区主／附加目录、固定会话 launch context、operation 幂等记录、有序 journal 和快照屏障。
- 私有长度前缀协议，1 MiB 上限、版本／nonce／DeviceID／连接 epoch 校验、分页查询与增量 journal 拉取。当前使用客户端轮询，不是带 Ack 流控的推送订阅。
- Codex 0.166.0 专用 stdin/stdout、独立进程组、实际模型目录和默认值、effort／Fast／Plan 能力、基础权限映射与 managed 限制检查。无法安全映射的 managed named profiles 不冒充可用。
- 真实 thread／turn 提交、普通消息增量与最终覆盖、基本 command activity、执行／文件审批、普通 requestUserInput 问题、resolved 生命周期及一次回答保护。未知 request 明确拒绝；未实现 item 显示受限入口，不执行任意客户端工具。
- 首页非空提交，等首轮被 harness 确认才清草稿；失败／不确定保留；Home、会话和问题草稿独立保存；启动回到 Home。已持久化的待确认操作只查询核对，不自动重发。
- 多会话不依赖可见选择运行。GUI 断开不终止 harness。进程死亡使待表单失效，活动执行变为 unknown；worker 重启不重发输入，无法核实的旧进程记录阻止新 runtime。

## 本轮实际证据

| 检查 | 实际边界 |
| --- | --- |
| `go test ./...`、`go test -race ./...` | 本轮全部通过；真实 pinned app-server 使用隔离 CODEX_HOME／本地确定性 provider 的集成测试，无需重复付费模型调用 |
| 双会话／客户端断开／问题重连／审批拒绝 | 实际产品 worker＋SQLite＋Unix socket＋真实 Codex app-server；provider 为本地夹具 |
| app-server SIGKILL | 仅杀测试自己的精确 PID；worker 保留内容、execution unknown，同 OperationID 查询不重发 |
| 录制回放 | `tests/fixtures/codex/live.jsonl` 是现有已登录账号的两轮真实对话；loopback 是真实 app-server 的工具和问题流程，模型输出来自本地夹具。53 个入站帧通过固定 schema，摘要哈希匹配 |
| 原生 GUI 实测 | 系统文件选择器创建临时工作区；中文粘贴、Enter 发送；真实账号回复；确认完成／耗时／input→output→cached footer。未把粘贴当作真实 IME 候选窗验收 |
| GUI 生命周期 | 同一临时会话真实执行 `/bin/sleep 12` 时退出 GUI；原 harness PID 仍在；重新打开可见同一两轮历史、工具与完成回复 |
| 草稿导航 | 原生窗口输入首页草稿，查看已有会话再 Cmd+N，原文保留；状态层另有独立持久化测试 |
| 冻结基线 | `python3 scripts/verify-design-freeze.py --repository`；未改冻结设计文件 |

补充实机检查：最终构建中，编辑后立即退出仍保存最新草稿；隔离本地模型夹具驱动真实 app-server 的原生问题面板，选择 Gray 不提交，点击提交才响应，普通输入框始终可见。该问题面板证据不冒充真实账号模型输出。

原生截图／生命周期过程证据保存在本地 `.ryugi/run/`，供局域网验收页使用；不加入 Git，不发布云端。

## 独立审阅后的修复

整批代码经过一次独立只读审阅，当前阶段的七项 Important 和一项用量问题均已通过回归测试修复。用量问题按实际影响提升为 Important，而非忽略：

- UI 操作绑定其当时可见的连接 epoch；未发送的旧操作不在新连接执行。未确认操作在每次重连核对，不自动重发。
- 草稿落盘独立于 IPC，保存有版本顺序和成功确认；关闭窗口／退出前刷入最新状态，旧异步保存不能覆盖它。
- 每轮显式发送 Plan 或 Default；真实 pinned app-server 的 Plan→Default 测试检查实际模型请求中的模式说明。
- 分页历史按完整序列补放第一页之后的重叠 journal；每页含关联请求／轮次元数据在 768 KiB 内，有进展游标，过大单项提供明确有界预览。
- harness 死亡后，活动 turn 和 session 都显示待核实；保留真实已完成轮次。没有本轮 usage 观测时，字段保持未知。

基础审批／问题表单暂限 16 KiB、每线程最多四个并发 pending request；超限回明确错误。完整资源预算和更大表单体验仍属后续阶段。

## 布局复查（2026-11-04）

对照最后覆盖的 Lody／conversation-frame／questions 样式重新检查。修复实际侧栏宽度比冻结稿窄 16 px、正文与 composer 使用不同水平基准、Markdown 默认 13 px／19.5 px 行距、深色旧配色、问题自定义答案框保留默认 60 px 最小高度等偏差。会话现在使用同一 768 px 居中列；Home 保持 736 px。工具栏与底部面板独立覆盖在贯穿窗口的虚拟列表上；按底部面板实际布局高度更新历史留白，随多行草稿和问题展开变化。

新增原生几何回归：1440／950 px 对齐、全高 scrollport、32 px 短答案、850×600 下长模型名与动态增长草稿、长工作区名称。检查测量更新能够收敛，不持续请求重绘。完整 Go 测试和冻结基线校验通过。

实机截图使用产品同一 View 的隔离原生预览宿主：1440×900 浅／深色 Home 和会话、问题展开／折叠，以及 950×650 深色长问题和多行草稿。会话只读回放上一轮真实账号记录；长内容和待答问题为布局夹具，不冒充新的 harness 事件。原先验收页把 1440 px 参考稿缩成 1240 px，本轮改为相同尺寸，并保留修复前截图。尚未实现的 Git 分支栏等控件仍不伪造；这些缺口和完整视觉验收分开记录。

## 验收评论修复（2026-11-04）

本地验收页的 13 条评论按冻结规格接通：SVG 导航／搜索／设备／设置图标，导航间距，去掉 Permission 的下拉字符并显示盾牌和权限名称，run 箭头独立居中。token 超过 1000 显示一位小数 k，原生详情保留三项精确数值。

已接通 device-owned Git 查询与显式 checkout、Home 分支草稿和新 worktree、真实会话标题／内容搜索、Changes 文件导航及 MyGo 原生 diff reader。所有 Git／文件读取经 owning worker；UI 不读取设备路径。Home 选择不 checkout；接纳时串行校验 busy/pending/indeterminate、dirty、实际目录身份及分支；同分支普通提交允许 dirty。原 launch context 不重写，外部变化使旧会话 contextChanged，明确确认后才能继续。worker 自己每两秒核对执行目录，不依赖窗口渲染；该轮询为当前工程实现，后续可换原生 watcher。Git 接纳复用 worker 现有 dispatchMu：不同 repo 的短 Git 查询／接纳仍串行，已运行 agents 保持并行。

实测使用独立临时 Git 仓库、真实 worker＋固定 app-server＋本地确定性 provider：分支选择后实际 HEAD 仍为 main、session 数不变；新 worktree 独立执行；正文搜索返回 Recorded text complete；修改的 example.go 在会话旁显示真实 +3/−1 diff。未发起新的真实账号模型请求。用量缩写单独回放上一轮真实账号录制并核对精确值。新截图按 1440×900 亮对亮、暗对暗排列；950×650 抽屉为单独压力证据。仅预览宿主临时保持窗口可见，避免 MyGo 对完全遮挡窗口暂停绘制；产品未改为置顶窗口。

## 明确未完成

- 阶段 2：queue／steer、完整 pinned 类型路由、结构化 Plan、跨虚拟卸载逻辑选择、流式批处理及长期资源预算。当前普通输入保持可编辑；活动 turn 的非空提交暂不可用，不能把它当作 queue。
- 原生交互仍待验收：run-menu 的侧子菜单／完整键盘行为、完整目录管理、可配置发送键、tool group 的 prose 边界折叠、全部细节和动画。当前运行配置能修改下一轮模型／effort／Plan／Fast；选择项用菜单内返回页，不能宣称已完整复刻冻结菜单。
- 阶段 3–4：SSH bootstrap、同设备多客户端安装流程、嵌入 tsnet、配对授权、RTT／路径与网络诊断。
- 阶段 5：正式 Runtime.app／SMAppService 宿主、签名／公证／升级、登录与电源恢复、正式 Keychain／隐私／支持系统矩阵。开发 bundle 仅 ad-hoc 签名，通过独立后台进程验证 GUI 退出后的继续执行；不自动注册登录服务。
- worker 或 harness 死亡后的 source-history 自动核对／孤儿显式处理 UI 尚待完善。当前保守地保留 unknown／indeterminate，不提供可导致重复副作用的自动恢复。
- 当前原生截图需要用户在本机验收；VoiceOver 和物理 IME 尚未在本产品重新验收。研究制品的通过记录不抵扣本产品测试。

## 重复测试与录制

```sh
env -u GOROOT go test ./...
env -u GOROOT go test -race ./...
python3 scripts/verify-codex-recordings.py
python3 scripts/verify-design-freeze.py --repository
```

录制器只允许固定 conversation notification，工作目录为临时目录；账号、配置、RPC 初始化结果、catalog 和 stderr 不进入录制。固定 schema 校验先于消费测试。真实账号录制会发起两次模型请求，普通 `go test` 不调用真实账号。重新录制是显式操作，不在测试失败时自动执行。
