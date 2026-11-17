# 附件与图片输入

状态：开发接通、自动验证通过；已取得原生亮暗／窄窗截图，人工待验收。

采用 [Lody 附件条](https://github.com/LodyAI/Lody/blob/main/packages/components/src/components/chat/chat-composer.tsx)和 [T3 Code 附件预览](https://github.com/pingdotgg/t3code/blob/main/apps/web/src/components/chat/ComposerAttachmentStrip.tsx)的紧凑卡片、显式移除、点击预览。保持项目的中性色、输入框边距与原生组件；附件增删随输入框实际高度过渡，尊重减少动态效果。

- 支持文件选择、拖入与粘贴图片／文件。支持 PNG、JPEG、GIF 图片和 UTF-8 文本／代码文件；不把 PDF、其他二进制、音频或视频冒充已支持。单文件 8 MiB、图片 4000 万像素、文本 128 KiB，每条最多 8 个、合计 16 MiB。
- 附件先保存不可变的本机私有副本，草稿、队列编辑及重启保留描述；不在 JSON 状态或事件中写图片 base64。仅收到已确认提交才清除对应附件，迟到的文件准备结果不能进入已取消编辑后恢复的普通草稿。
- 64 KiB 分块通过独立的已认证设备连接上传，不占用 Stop、请求应答或事件订阅连接。worker 按工作区及随机资源 ID 存储，校验摘要和实际类型；不提供任意路径读取接口，不改用户仓库或扩大沙盒目录。部分上传不可用于提交，相同分块重试校验内容，已完成资源不能换名或替换。
- 图片经固定 Codex 0.166.0 的 `localImage` 输入；路径由所属 worker 生成。文本附件以独立文本输入携带名称和完整内容。图片无须同时输入文字；普通发送、排队和 Steer 使用同一结构化输入。草稿切换工作区时重新准备当前拥有者的副本，不能复用其他工作区的资源。
- 历史预览通过资源 ID 从原设备读取并校验回执和摘要；远程路径不在控制端读取。图片预览原生显示，文本原生可选，不执行 HTML 或自动打开外部链接。缩略图缓存有界，保留的原始附件仍在私有磁盘内；自动清理历史资源尚未实施，避免误删已接纳或待核实的输入。

验证使用真实 app-server 加本地确定性模型端点，确认实际请求包含 `input_image` 及图片数据，而不是将路径或说明文字当作图片。覆盖跨工作区拒绝、分块重试、持久化、另一个客户端下载、图片单独发送、队列与草稿竞态。测试不调用付费模型。

传输协议 minor 6、数据库 schema 5；旧执行服务可继续纯文字使用，但附件入口要求升级，不能静默丢弃字段。

真实设备补充：MacBook Air 使用独立私有目录和临时 worker，经现有已校验 SSH 连接完成 UTF-8 文本与 PNG 的多分块上传、另一个连接的下载，并逐字节核对；上传期间命令连接可用。未请求模型，原有 worker 身份保持不变；临时 worker 验证后已关闭。验收副本和日志保持在临时私有目录，不覆盖原有设备状态。

## 2026-11-17 类型不支持时的路径回退

目标为本机时，文件选择、拖入或粘贴文件中类型不支持的文件改为在草稿末尾附上带引号的绝对路径，不上传其内容；保留当前文字和原生撤销。读取失败、目录、空文件和超出大小限制仍报错。结果回填前重新核对工作区与本机归属，取消队列编辑后的迟到结果不回填。发送回执等待期间附路径只进入下一条草稿，不带回上一条提示词。路径只是消息文字，不额外授予 Agent 文件访问权限。

远程目标当前继续拒绝此类上传，并说明控制 Mac 的路径不可用。后续方案建议（未实现、未确认为产品要求）：上传到 owning worker 的私有资源目录后提供该设备上的路径，保留现有工作区归属、完整上传与摘要校验，不写入用户仓库；这需要扩展现有类型校验及 adapter 输入约定。

本轮验证（2026-11-17，本机源码工作区，未安装到正式 App）：

- 新增附件用例先在旧行为上失败，再验证本机路径、远程拒绝、迟到结果、混合选择、大小限制、原生撤销／重做和待发送消息的下一条草稿。最终 `env -u GOROOT go test -race ./internal/ui ./internal/attachments -run 'TestUnsupportedAttachmentPaths|TestLocalAttachmentPathPreserves|TestAttachmentPath|TestAttachmentLimitsDoNot|TestLateAttachmentPreparation|TestPendingSend|TestOrdinaryAttachmentUpdate' -count=1` 通过 UI 检查；该筛选在 attachments 包没有匹配用例。此前该包的 `TestAttachmentRejectsBinaryAndFalseDigest` 普通和竞态检查通过。
- 最终源码的 `env -u GOROOT go build -o /tmp/shioji-attachment-paths ./cmd/shioji` 通过；`python3 scripts/verify-design-freeze.py --repository` 通过，6 个文件匹配冻结基线。
- `env -u GOROOT go test -timeout 20m ./...` 未通过：本地 `build/worker-recovery-20261116` 的临时程序重复定义 `main`／`ident`；原生 UI 全套在该轮通过（462.565s，早于最后一次待发送草稿处理修改，最后修改由上述竞态回归验证）。worker 包出现以下失败：`TestArchiveAuditClosedRuntimeMustNotRemainInIdleCache`、`TestArchiveAuditStaleRetirementMustNotAbandonRestoredInput`、`TestArchiveAuditDeletionMustNotLeaveRetirementPoisoningSweep`、`TestArchiveAuditPausedQueueMustNotRequireExtraForcedDeletion`、`TestArchiveAuditGarbageCloseMustNotBlockOtherSessions`、`TestArchiveAuditConcurrentWarningMustUseFreshVerdictWithoutAwaitingReclamation`、`TestArchiveAuditFailedVictimsMustNotConsumeSuccessfulEvictionLimit`、`TestArchiveAuditPermanentDeletionMustRemoveOwnedRuntimeRecord`。
- `env -u GOROOT go test -timeout 20m ./cmd/... ./internal/...` 也未通过：`TestWorkspaceDeletionIsAtomicAndRetainsOtherWorkspaces`、`TestIsolatedNetworkHostRetainsProcessAfterGUIDetaches`、`TestExplicitArchiveDeletionStillRefusesActiveWork`、`TestArchiveDeletionRefusesUnverifiedLiveRuntime`、`TestWorkerArchiveCleanupUsesOwningClockAndSkipsLiveWork`、`TestArchiveDeletionCannotBypassUnforcedMaintenance` 失败；该轮剩余 UI 测试按进程身份核对后主动终止，无子进程。网络后台启动用例随后单独重跑通过（6.428s）。全量执行期间 store／worker 文件存在本轮之外的并行修改；本次附件改动不涉及这些文件，未对失败归因，也未修复它们。
