# Shioji 工作区用量设计与 Lody 源码调查

Ryugi 画板「Shioji 用量 · macOS 与 iOS 原生设计」位于本地 `.ryugi/artifacts/artboard/usage-native`，包含 Mac 亮色、深色和窄窗，以及 iPhone 正常和离线快照，数字均为示例。调查固定于 [Lody 桌面](https://github.com/LodyAI/Lody/tree/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51)、[Lody iOS](https://github.com/LodyAI/lody-ios/tree/512d209a058c41c31a4f4b4ab9ba69f771207c5e) 及桌面仓库固定的 [Codex adapter](https://github.com/LodyAI/acp-extension-codex/tree/38eb665ba369274cb2ce266b26fe1388295aca32) 版本。

## 两端界面

| | Lody macOS | Lody 独立 iOS |
| --- | --- | --- |
| 工作区统计 | Electron 窗口内的 React 设置页。顶部是工作区、时间分段与总 Token／预估费用。下方有日历或时段图、单日详情，以及按模型、按成员堆叠的 Token 趋势。支持分享图。 | Swift/UIKit 设置页。按当前已登录的工作区读取，首屏展示总 Token／费用、可交互柱图、峰值、Token 构成及同步时间。可下拉刷新，显示部分失败；界面只接收工作区聚合值。 |
| 时间 | 24 小时、7 天、30 天、全部，默认 24 小时。前两档请求小时桶，以查看端时区显示；日历和点入的单日详情以 UTC 日标识。 | 同样四档，切换工作区回到 24 小时。时间轴标签当前用 UTC。按起点排序，超过 90 桶时合并相邻桶，合并组内费用有缺失时整组费用缺失。有可用费用桶才显示费用切换。 |
| 会话用量 | 输入区附近的圆环入口展开上下文详情，也可展示适用的账号额度窗口。当前上下文优先，没有上下文才回退额度。订阅额度也在机器／Agent 配置查看。 | Composer 按钮显示圆环和文字，压缩时换成活动指示器。点击打开 UIKit popover，展示模型、上下文和合资格额度窗口；重置时间可切换相对值和本地时钟。Agent 配置另有额度行和详情 sheet。 |

桌面源码见 [Electron 窗口](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/apps/electron/src/main/window.ts#L399)、[统计界面](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/packages/components/src/components/settings/stats-setting-pure.tsx#L316)、[时间口径](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/specs/usage-timeline.md) 和 [会话浮层](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/packages/components/src/components/sessions/session-usage-popover.tsx)。

iOS 源码见 [原生统计页](https://github.com/LodyAI/lody-ios/blob/512d209a058c41c31a4f4b4ab9ba69f771207c5e/packages/LodyKit/Sources/LodyKit/Settings/AIUsageController.swift)、[展示投影](https://github.com/LodyAI/lody-ios/blob/512d209a058c41c31a4f4b4ab9ba69f771207c5e/packages/LodyKit/Sources/LodyKit/Settings/AIUsageSections.swift#L66)、[会话浮层](https://github.com/LodyAI/lody-ios/blob/512d209a058c41c31a4f4b4ab9ba69f771207c5e/packages/LodyKit/Sources/LodyKit/Chat/ChatUsagePanel.swift) 和 [Agent 额度界面](https://github.com/LodyAI/lody-ios/blob/512d209a058c41c31a4f4b4ab9ba69f771207c5e/packages/LodyKit/Sources/LodyKit/Settings/AgentProviderQuota.swift)。

## 统计范围

**Lody 的「全局」指当前工作区。** 桌面和 iOS 的用量查询都以 `workspaceId` 为边界，可汇总同一工作区内不同机器上报的用量。桌面按模型或成员分解，iOS 读取工作区总量与时间序列。「全部」切换为该工作区的全部时间。CLI 上报携带 `machineId`，用量页的查询没有设备筛选参数。[桌面查询](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/packages/components/src/components/settings/settings-data-cache.tsx#L152)、[iOS 查询](https://github.com/LodyAI/lody-ios/blob/512d209a058c41c31a4f4b4ab9ba69f771207c5e/packages/LodyKit/Sources/LodyKit/Cloud/WorkspaceUsage.swift#L47)、[CLI 上报归属](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/apps/cli/src/lib/usage/usage-tracking-service.ts#L91)

Shioji 彩色稿默认展示「全部工作区」，范围是当前客户端已获授权、可见的工作区。也可选一个工作区查看，选择项同时标明所属设备。每个工作区由一个执行设备持有，全局视图可以跨多个执行设备。

## 统计怎样产生

1. **Codex 在执行设备报告线程计数。** 适配器读 app-server 的 `thread/tokenUsage/updated`。每次用 `total` 与上次快照按字段做差，把正增量累积到当前原生 Turn ID 的 scope。模型取提交时固定的模型，迟到的其他 Turn ID 被跳过。新线程从零起算，恢复时先用可取得的快照作基线。缺基线时首份通知只用于对齐，可能漏掉首部分真实消耗。Lody 文档也明确记载根线程这个口径本身不加子代理用量。[Codex 归属与差分](https://github.com/LodyAI/acp-extension-codex/blob/38eb665ba369274cb2ce266b26fe1388295aca32/src/CodexUsage.ts#L25)、[Lody 用量交付说明](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/specs/usage-delivery.md)
2. **拆成互斥 Token 类别。** 普通输入 = 原生输入减去缓存读与写，普通输出 = 原生输出减去推理，其余三类分别记录。例：输入 1,000（含缓存读 400）、输出 200（含推理 50）、无缓存写，拆成普通输入 600、缓存读 400、普通输出 150、推理 50，总计 1,200。缓存与推理都是原计数的子集，重复加会把这组变成 1,650。[适配器计算](https://github.com/LodyAI/acp-extension-codex/blob/38eb665ba369274cb2ce266b26fe1388295aca32/src/CodexUsage.ts#L9)、[Core 数据契约](https://github.com/LodyAI/acp-extension-core/blob/ea90d0b23d4f850c66c0f542f53cbc596ef2c62f/src/usage.ts#L1)
3. **累计快照上报，新增量只给每轮显示。** `modelUsage` 是一个 scope 内按模型累计的值，当前通知的 `delta` 已包含其中。CLI 合并同 scope 待发送快照，用工作区、会话、成员、执行机器、Agent 及 accounting identity 调 `upsertSessionUsageFromCli`；缺模型数据会跳过。失败保留同一载荷重试，队列在进程内，崩溃后存在丢失窗口。逐条回复的详情独立累加分配给该回复的 `delta.usage`，有迟到补报路径。公开规格描述托管端按 key 取累计最大值，服务实现本身在公开仓库之外。[CLI 上报](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/apps/cli/src/lib/usage/usage-tracking-service.ts#L91)、[消息处理](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/apps/cli/src/lib/message-handler.ts#L4705)
4. **费用可能由提供方回报，也可能按表估算。** CLI 保留已报告金额。Codex／Kimi 缺价时会查模型的费率表，按普通输入、普通输出加推理、缓存读取分别乘对应单价。模型费率未知或存在表不支持的缓存写入时不补价。轮次详情中某次贡献缺价时整轮费用未知。桌面已就绪的总览代码会把空的总费用回落为 0；iOS 的缺失值投影保留空值。公开源码不足以核实托管端对缺价、迟到数据和精确归属时刻的处理。[CLI 估价](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/apps/cli/src/lib/usage/usage-tracking-service.ts#L217)、[桌面总览](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/packages/components/src/components/settings/stats-setting-pure.tsx#L387)、[iOS 投影](https://github.com/LodyAI/lody-ios/blob/512d209a058c41c31a4f4b4ab9ba69f771207c5e/packages/LodyKit/Sources/LodyKit/Cloud/WorkspaceUsage.swift#L11)
5. **两端读取服务端聚合。** 桌面读 timeline、calendar 和选中的 day 详情，timeline 提供合计及各时间桶的模型／成员分布。iOS 查询 workspace summary 与 timeline，后者存在时优先取其合计；只允许与所请求工作区和范围匹配的回包。iOS 对柱图做排序、合并和格式化。Lody 的本地模式没有这项云用量服务。调查复核了公开实现，托管账本、生产响应和账单准确性仍未验证。[桌面查询](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/packages/components/src/components/settings/settings-data-cache.tsx#L152)、[iOS 查询](https://github.com/LodyAI/lody-ios/blob/512d209a058c41c31a4f4b4ab9ba69f771207c5e/packages/LodyKit/Sources/LodyKit/Cloud/WorkspaceUsage.swift#L47)

用固定版本的 Codex 适配器运行了上述 1,200 Token 示例，另核对重复通知、其他轮次的迟到通知、新轮切换模型及缺失恢复基线，结果与源码分支一致。

### 三种数字的边界

| 读数 | 来源 | 用途 |
| --- | --- | --- |
| 工作区累计用量 | 已归属到执行活动的 Token，按工作区、模型和时间聚合 | 看某段时间消耗了多少 |
| 会话上下文 | 当前报告占用与模型窗口大小 | 看当前会话还装得下多少 |
| 账号额度 | Provider 的窗口占用比例、周期、重置时刻等，例如 Codex 的 `account/rateLimits/read` | 看当前账号／额度池能否继续执行 |

相同的账号可能横跨多个工作区或客户端，额度窗口也可能覆盖其他消费来源。累计 Token 无法推出额度剩余百分比；压缩会减少当下上下文占用，历史工作区用量仍保留。Shioji 已有会话上下文快照及每轮计数基础。[Lody Codex 额度读取](https://github.com/LodyAI/acp-extension-codex/blob/38eb665ba369274cb2ce266b26fe1388295aca32/src/CodexAppServerClient.ts#L958)、[Shioji 上下文口径](../../context-usage.md)、[Shioji Codex 归一化](../../../internal/harness/codex/normalize.go#L231)

## Shioji 设计提案

- **macOS 主窗口侧栏底部「用量」。** 页头展示范围选择、纳入的设备与工作区数量，以及时间范围。概览用一条窄带展示总 Token 和预估费用。7 天默认展示时段矩阵，每行一天、每格一小时，所有日期共用按 Token 数量划分的蓝色强度。选择日期时审阅线标出该行，图旁显示当日总量、高峰小时、用量占比和各工作区的贡献；窄窗把日期详情放到图下，模型和 Token 构成仍并排。轴与右上角的时区标记一起阅读。这版以 Shioji 原有工作区侧栏为窗口结构，借鉴 Lody 周视图的阅读方式。[Lody 时段组件](https://github.com/LodyAI/Lody/blob/ca1268e4f9f5e13003ec308cf98c5f0f0afeeb51/packages/components/src/components/settings/usage-calendar-visualization.tsx#L1669)
- **iPhone 从「设置 → 用量」进入原生导航页。** 使用与 Mac 相同的全局或单工作区范围。时间分段、两个总数、触摸趋势、构成和模型纵向滚动。iPad 可按可用内容宽度采用同一紧凑页或并列明细。画板默认展示 7 天，便于一眼比较一周；范围选择是设计提案。
- **颜色与读数。** 蓝色表示时段强度和趋势，蓝、青绿、紫区分同一图例内的类别。选中的时段有数值读出，选中的日期有位置标记。离线来源用琥珀色提示并附文字。
- **在线、缺失与离线。** 快照按设备身份、工作区和时间范围隔离。全局视图合并授权来源；可使用匹配的离线快照，并指出设备及缓存时刻。来源缺少快照时明确数据不完整，全部来源均缺失时显示不可用。仅在观测覆盖和费率均有依据时显示完整预估，金额不可用时显示「—」。实测零才显示零。
- **统计归所属 worker。** Adapter 负责来源语义及互斥计数；后台运行与重连采用稳定的执行标识和单调快照，按 worker 保存的设备／工作区、实际模型、轮次归属与采集时间算聚合。客户端用同一时间范围合并它已获授权的工作区摘要。历史显示重放、分叉复制历史和历史重开保留的前缀不重复计数；已经真实消耗的用量在编辑、分叉、归档或删除聊天视图时仍计入原范围。归属与时间桶为提案，实施时需以运行时可观测时间验证迟到数据、断点、跨午夜和时区边界。
- **现有数据边界。** Shioji 固定版本 adapter 当前分别保存输入、输出、缓存输入的轮次差值，缓存输入属于输入。历史记录有缺字段，也未形成可依赖的精确模型／费率／子代理账本。历史回填只纳入具备可信用量、完成时间和执行归属的记录，旧数据缺成本就保持未知。正式界面使用 Go/MyGo，移动端沿用 UIKit 导航。画板用隔离的 SwiftUI／Charts 原型实际渲染，数据与原型交互用于设计评审。
