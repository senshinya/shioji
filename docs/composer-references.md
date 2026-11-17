# 输入框引用

2026-11-07：原生输入框增加 `@` 文件选择与 `/` Skill 菜单。使用现有灰阶菜单、图标字面度量、输入框与标签；没有新增验收页或录屏资源。UI 人工观感仍由用户确认。

## 文件

- 在正文的词首输入 `@`，按文件名／相对路径过滤所属设备的工作区文件。方向键选择，Enter／Tab 确认，Esc 关闭，输入法组合期间不接管按键。电子邮箱里的 `@` 保持普通文字。
- 首页搜索工作区目录；已有会话使用其已接纳的 CWD 和附加目录。工作区后来增加的目录不会扩大旧会话的范围。多根目录结果显示所属根和相对目录。
- Git 目录通过 Git 列出已跟踪和非忽略文件；普通目录跳过隐藏项及常见构建目录。返回最多 40 条，并保留截断标记。搜索和文件传输走独立认证连接，不阻塞停止／回答命令。
- 选择时由 owning worker 校验目录身份，用 `os.OpenRoot` 限制读取范围。拒绝越界链接、目录、特殊文件和不支持的内容；复用附件限制：PNG／JPEG／GIF 不超过 8 MiB，UTF-8 文本／代码不超过 128 KiB，最多 8 个附件且合计不超过 16 MiB。
- 内容保存为工作区内的不可变资源，新增 `Attachment.SourcePath` 记录原设备路径；客户端不会把该路径当成本机文件读取。行内文件标签支持点击预览和原生整块删除，发送、队列、编辑及重开保留同一份快照。读取期间禁用发送，失败保留原查询和草稿。

## Skill

- 统一使用 `/` 作为 Skill 菜单入口；消息开头、空白或换行后的词首均可触发，已有引用后可以继续添加其他 Skill。选择后保存结构化引用，用户继续输入普通正文；不向正文拼接 `$name` 或任何 harness 的命令语法。`/compact` 命令仍只在整条消息开头提供。
- 列表来自工作区所属设备的所选 harness；已有会话固定使用自己的 harness。只有实际声明 Skills 能力且实现中立 SkillProvider 的运行时提供入口。
- `SkillReference` 只携带名称和 opaque ID。草稿额外记录设备、工作区、harness 归属；Worker 在接纳与实际投递前重新匹配该上下文中仍启用的目录项。目录未知、引用失效或归属不匹配时明确拒绝，保留输入。
- Codex adapter 使用固定 0.166.0 schema 的 `skills/list`，并将引用翻译为原生 `skill` input。原生路径解释及请求结构留在 adapter；共享 Worker／应用／UI 不解析 ID 或依赖 `$` 表达。
- Skill 引用随草稿保存、队列编辑、历史重开和恢复草稿保留；恢复合并按完整归属去重。迟到确认只清理原提交归属的引用。当前仍只支持 Codex，未新增 Claude runtime。

## 协议与交互

Worker 保持 major 2；minor 11 增加 `file.search`、`file.snapshot`、`skills.list` 及 typed Skill 字段。低于 minor 11 的 worker 不显示这两个新入口。minor 12 将引用位置保存在已发送消息中，并增加显式的历史引用替换；低于 minor 12 的 worker 不接纳新的行内引用发送，含行内引用的历史编辑也要求升级，避免静默丢失。队列编辑中缺失／null 的 skills 字段保留原值，显式空列表表示移除全部；新 UI 总是提交明确列表。

菜单保留输入焦点，异步结果按设备、工作区、harness、会话与编辑身份隔离。远端快照晚到时不抢回用户随后转移的焦点。弹层从编辑器位置展开／收回，候选区高度同步变化；搜索词更新保留同一弹层与既有结果，不重复播放开合；取消和过期结果不会再次打开弹层。文件与 Skill 在原触发位置显示“图标＋名称”原生附件节点，随正文换行，支持光标、选区、整块删除、撤销／重做及名称复制。草稿、队列和已发送消息使用中立 `ReferencePosition` 保存一基 rune 位置；adapter 不接收这些展示位置。已发送标签在正文中保留原位置，文件点击预览同一份不可变快照；正文选区、虚拟卸载后的复制和整条消息复制都展开标签名称。历史加载和分叉保留同一位置；原位历史编辑复用原生文档及候选菜单，支持引用的新增、整块删除与撤销，独立于底部普通草稿。确认时显式提交当前引用集合，空集合表示全部移除；旧客户端未提供该字段时保留原引用。旧消息未记录位置时将引用放在正文末尾，不推测其原始位置。旧版保留的历史编辑草稿补回源引用；用户已明确删除的引用不会在重开或重载时复活。恢复生成的新摘要显式重置旧位置，将保留的引用放在正文末尾，不把旧位置插进摘要内部。普通附件上传状态不重建编辑文档或清空撤销。遵循减少动态效果。

验证覆盖真实 owning-worker 文件快照、Git ignore、目录扩张与越界拒绝、普通／队列草稿重载、键盘选择／光标／焦点、双会话式独立 pane 菜单、旧队列字段兼容，以及本机真实 Codex app-server 加确定性测试模型服务的 Skill 指令传递。测试服务不代表付费模型端到端验收。

## Skills configuration

Skill references still use the enabled-only catalog and retain their owning device/workspace/harness. Protocol 2.25 adds separate management operations, including disabled entries and bounded read-only Skill documents. Device > Agent > Skills manages user-owned entries, while workspace settings show project entries and inherited sources; inherited entries are read-only in that view and link to their device settings. Disabled references in existing drafts are retained for explicit correction and rejected on actual submission if no longer offered. Native configuration changes do not rewrite already accepted messages or input snapshots.

The conversation toolbar's workspace directory popup opens downward and retracts upward at its trigger with constant opacity; Reduce Motion skips height movement. Its existing folder rows, management entry, keyboard dismissal and focus return are preserved.

## 2026-11-17 回车发送回归

MyGo 富文本编辑器用 `composing` 保留输入法撤销事务；原生 `UnmarkText` 或空 preedit 结束可见组合后，这个事务仍可能存在。原先发送按键和 `Document.Composing` 同时检查事务标志，导致 Enter 跳过应用处理并插入换行。现在只以实际 marked text 判断组合状态，原生后续 replacement 仍加入同一步撤销。

`internal/ui/composer_enter_test.go` 覆盖从左侧重新打开已有会话后立即发送，以及无组合、取消组合和提交中文三种路径的亮暗模式；`third_party/mygo/ui/richtext/ime_test.go` 直接覆盖原生 unmark、空 preedit 和后续 replacement 的撤销／重做。仅重新打开会话本身未复现故障，状态残留是已复现的一条原因，不能据此断言已解释所有系统输入法时序。

本机证据保存在忽略的 `build/composer-enter/`：`video-before` 用 Go overlay 恢复修改前两个判断，亮暗回归均因草稿多出 `\n` 失败；`video-after` 使用修复后的生产 View，按键产生 `turn.enqueue`。四段 MP4 均为 1200×820、30 fps、4 秒，已完整解码检查。动作：1.000 秒打开已有会话，1.033 秒输入文字，1.067 秒组合，1.100 秒结束组合，1.133 秒回车。数据与 RPC 使用确定性夹具，原生组件渲染录像不代表已安装 App、系统候选窗或真实 worker 接纳验收；当前运行 App 未替换。独立开发包为 `build/composer-enter/Shioji.app`。

输入框定向回归、对应 UI／richtext race、MyGo `go test ./ui/...` 及 race、UI／richtext vet、设计冻结、开发包构建与签名校验通过。未使用富文本的最小 native／webview 构建前后尺寸分别保持 9,407,282／7,011,506 字节。完整 `go test -timeout 20m ./...` 未通过：旧 `build/interaction-latency/baseline/internal/{app,ui}` 缺失类型，`build/worker-recovery-20261116` 含重复入口；此外 `TestWorkspaceHeadingSharesTheWorkspaceRowVisibleEdges` 与 `TestWorkspaceHeadingInsetsAndNoPerWorkspacePlus` 失败。恢复本次修改前逻辑后，这两项侧栏检查仍以相同的几何值失败（`sidebar-baseline.log`／`sidebar-current.log`）；没有将完整测试集报告为通过。

跨午夜完成的 `go test -timeout 20m ./cmd/... ./internal/...` 排除了旧 build 工具，仍只因上述两项侧栏检查失败（UI 384.674 秒）；其余产品包通过，worker 239.853 秒通过。记录为 `product-tests.log`。未修改正在使用的 App、worker 或 Tailnet 服务，也未启动预览服务。
