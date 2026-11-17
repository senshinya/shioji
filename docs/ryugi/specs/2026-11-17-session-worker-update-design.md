# 会话设备浮层中的执行服务更新

2026-11-17 用户确认单一动态按钮设计并要求实施，特别要求融入现有按钮，保持一致间距。视觉讨论保留在本机 `html/session-worker-update`，该 HTML 仅为设计演示。

浮层保持原有 292 DIP 宽度、负载图、连接信息、保持唤醒及设备详情。在保持唤醒与设备详情之间，只有可用更新、正在更新、等待或更新失败时增加一颗操作按钮。不显示独立的“执行服务”状态行、分割线、版本栏或常驻占位。当前版本、比客户端更新的版本及未获知更新时隐藏。更新完成以实际服务状态为准，整行移除。

更新与设备详情复用 `devicePopoverAction`：26 DIP 操作行、上下各 1 DIP、左右外扩 6 DIP、左右内缩 6 DIP、4 DIP 圆角、12 DIP 字号、14 DIP 图标槽与 6 DIP 图文间距，内容左对齐。使用原生字体字面和基线度量，更新时复用 `serviceSpinner`，不以按钮默认居中排布代替左对齐。

按钮文字随真实阶段变化：更新执行服务、正在准备更新、等待空闲后更新、正在更新执行服务、正在重新连接或正在核对更新结果。正在进行的状态不可再次提交。构建及失败原因通过悬停查看。旧服务需要迁移时进入所属设备的现有迁移流程，不从浮层绕过。

点击等待按钮在会话内打开所属设备的更新管理确认，可继续等待、取消更新，或明确选择停止整台设备任务并更新。确认说明暂停待发队列，保留会话历史、草稿与文件，不自动续答。沿用现有更新、取消和立即更新命令；不新增 worker／supervisor 升级链路。确认绑定设备、连接代次、目标构建与操作 ID；身份、操作或阶段变化时关闭旧确认。

本机、远程与并排视图使用所属设备的状态和连接，操作不修改会话选择或草稿。关闭浮层不取消 worker 已接纳的操作，不重发未知输入，不控制 Tailnet 生命周期。

验证包括：原生组件回归、亮暗与宽窄截图、按钮内外布局和消失后高度、设备归属、等待管理与一次影响确认、过期操作／连接拒绝、进行中不可重复提交、旧版迁移和单 worker 本机路径。截图使用生产 MyGo View 与夹具状态，不能当作真实设备升级验收。

## 本轮实施验证

源码已接入，保留在当前工作区，未提交或部署正式设备。本地 `verify/session-worker-update` 记录命令原始输出，并引用 `artboard/verify-session-worker-update` 的生产 MyGo View 截图与交互录制；两个视觉组仍等待用户验收。隔离开发包为 `build/device-popover-update/Shioji.app`。UI 包完整回归、定向 race、UI vet、仓库冻结设计和开发包签名验证通过。

完整回归未全绿：`internal/harness/codex` 的 `TestCatalogCloseRetriesAfterEarlierCleanupFinishes`、`TestPendingActivityDoesNotCountHistoryObservationAsExecution`，以及 `internal/worker` 的 `TestLateLossDuringRetirementCannotStartAnotherGeneration`、`TestLodyMechanismShutdownLetsCapturedCallbacksDrain`、`TestExplicitContinuationKeepsOriginalNativeThreadAndPausedQueue`、`TestArchivePolicyAndPermanentDeletion` 失败。这些包存在其他任务的生命周期修改，本轮未更改。`go test ./...` 另受本地 `build/worker-recovery-20261116` 重复入口及 `docs/research/lody-worker-lifecycle-2026-11-17/recheck` 混合 package 影响，未擅自清理历史资料。

## 更新完成后的高度过渡

用户追加要求：更新成功到最新、按钮消失时，浮层须有高度动画。更新行使用共用 `animatedContentHeight` 测量并驱动真实布局高度，180 ms EaseOut；其后的设备详情与浮层底边直接跟随同一高度，不给外层再叠加 Size 缓动。初次打开已可见的更新行直接建立完整高度；收起中保留原生内容并裁切，整行立即失去交互，结束后卸载且不留占位。快速重新出现从当前高度反向过渡；减少动态效果时立即切换。

补充原生回归检查了开始／中间／结束高度、设备详情同步位移、顶部锚点与宽度稳定、反向连续性、减少动态效果、服务状态丢失，以及既有行距／图标字面对齐。亮暗录像保留在本地验收画板的 height 组；均为生产 MyGo 组件与夹具状态。

高度过渡追加验证：UI 包完整回归、专项 race、UI vet、设计冻结与隔离开发包签名检查通过。本轮正式范围回归的 supervisor 就绪超时单独重跑通过，原失败不改写；worker 的 `TestDelayedDispatchCannotResurrectAnAbandonedInputOnReplacementRuntime`、`TestArchivePolicyAndPermanentDeletion`、`TestWorkspaceArchiveRejectsActiveExecutionAndBlocksNewSessions` 失败。当前日志已替换本地验证报告中的正式回归证据，未沿用上一轮六项失败作为本轮结果，亦未修改其他任务的生命周期代码。
