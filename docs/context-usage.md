# 上下文用量与自动压缩反馈

会话输入框底栏在模型选择左侧提供 28×28px 的上下文图标入口，仅展示可见直径 16px 的圆环，不在入口外露百分比或状态文字。圆环与相邻文字按公共字体度量对齐；压缩中在原位替换为旋转圆弧，状态变化不改变入口宽度或按钮位置。入口、模型选择与发送／停止按钮统一使用 6px 布局间距。百分比和压缩状态保留在悬停详情中。首页不显示上下文入口。

悬停打开 232px 紧凑浮层，向入口上方展开、右端对齐入口。显示已用、剩余、上限及报告时间；未报告和未知上限均明确显示未知，不将缺失数据视为零，不从聊天文字估算。离线保留最后一次报告并标注。浮层在未知、已报告、压缩中和离线之间保留相同高度，不顶起输入框。移入浮层保持展开，离开入口与浮层 180ms 后收回，允许跨越两者间的空隙；不抢走输入焦点，Escape 可关闭，键盘聚焦入口也可查看。

圆环与浮层进度条使用 500ms EaseOut 插值，数字使用已有 240ms Numeric content transition；浮层复用 120ms、以按钮为原点的展开／收起。详情状态文字交叉淡化；减少动态效果时沿用公共适配，停止旋转。会话切换的动画状态按 SessionID 隔离。

`domain.ContextUsage` 是独立的中立快照，通过 `context.usage` 事件由所属 worker 保存到 Session；既有 snapshot、watch、历史打开和多客户端投影携带相同数据。它不改变执行状态、不插入聊天项或发送完成通知。按 worker 保存的轮次顺序忽略早先轮次的迟到报告，普通 runtime 元数据刷新不抹去快照；更换模型及历史替换后清空旧报告，改变推理强度不清空。模型更换同时保存已有轮次的顺序下界，拒绝切换之前的迟到报告；该下界由 worker 持久化并跨 runtime 元数据刷新保留。

Codex adapter 从固定 0.166.0 的 `thread/tokenUsage/updated` 读取 `last.totalTokens` 和 `modelContextWindow`，翻译为当前占用与上限。`total` 继续只用于原有的轮次用量计算；累计计费 token 不是上下文占用。具体协议名和字段只存在于 adapter。窗口上限采用运行时报告，不按模型名字写死，也不假定知道自动压缩阈值。

压缩活动采用独立中立呈现类型 `compaction`，与工作区 hook 的 `context` 区分；原生 UI 只在对应轮次仍运行时显示压缩状态，终止／失败／失联不会遗留永久转圈。压缩失败或停止不会冒充成功。没有新增手动压缩命令或额外修改 harness 设置。

参考固定源码：[Lody 用量入口](https://github.com/LodyAI/Lody/blob/99ad483ecc4320dc5ee550c6545b3c23995ce52d/packages/components/src/components/sessions/session-usage-popover.tsx)、[T3 Code 圆环动画](https://github.com/pingdotgg/t3code/blob/25bf84ab07f53f141c20239c63e383823a9d5ea0/apps/web/src/components/chat/ContextWindowMeter.tsx)、[T3 Code 的 Codex 用量翻译](https://github.com/pingdotgg/t3code/blob/25bf84ab07f53f141c20239c63e383823a9d5ea0/apps/server/src/orchestration-v2/Adapters/CodexAdapterV2.ts)。

验证包括数据口径／未知值、迟到事件／持久化补放、模型变化、真实固定 Codex 配合本地确定性端点的 worker 链路、亮暗／850px 窄窗中圆环中间帧、浮层锚点与高度、输入焦点和草稿保持，以及终止后退出压缩状态。另在正式原生应用中调用真实在线模型验证，收到已用 31,540、上限 475,000 的报告；未伪造在线回复。未人为消耗长上下文触发线上自动压缩，该状态通过协议与原生呈现回归覆盖。亮暗模式、圆环位置与悬停动效已由用户验收通过。
