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

本节初版中，远程目标拒绝此类上传，并说明控制 Mac 的路径不可用。当时提出的后续方案（现已按下节实现）：上传到 owning worker 的私有资源目录后提供该设备上的路径，保留现有工作区归属、完整上传与摘要校验，不写入用户仓库；这需要扩展现有类型校验及 adapter 输入约定。

本轮验证（2026-11-17，本机源码工作区，未安装到正式 App）：

- 新增附件用例先在旧行为上失败，再验证本机路径、远程拒绝、迟到结果、混合选择、大小限制、原生撤销／重做和待发送消息的下一条草稿。最终 `env -u GOROOT go test -race ./internal/ui ./internal/attachments -run 'TestUnsupportedAttachmentPaths|TestLocalAttachmentPathPreserves|TestAttachmentPath|TestAttachmentLimitsDoNot|TestLateAttachmentPreparation|TestPendingSend|TestOrdinaryAttachmentUpdate' -count=1` 通过 UI 检查；该筛选在 attachments 包没有匹配用例。此前该包的 `TestAttachmentRejectsBinaryAndFalseDigest` 普通和竞态检查通过。
- 最终源码的 `env -u GOROOT go build -o /tmp/shioji-attachment-paths ./cmd/shioji` 通过；`python3 scripts/verify-design-freeze.py --repository` 通过，6 个文件匹配冻结基线。
- `env -u GOROOT go test -timeout 20m ./...` 未通过：本地 `build/worker-recovery-20261116` 的临时程序重复定义 `main`／`ident`；原生 UI 全套在该轮通过（462.565s，早于最后一次待发送草稿处理修改，最后修改由上述竞态回归验证）。worker 包出现以下失败：`TestArchiveAuditClosedRuntimeMustNotRemainInIdleCache`、`TestArchiveAuditStaleRetirementMustNotAbandonRestoredInput`、`TestArchiveAuditDeletionMustNotLeaveRetirementPoisoningSweep`、`TestArchiveAuditPausedQueueMustNotRequireExtraForcedDeletion`、`TestArchiveAuditGarbageCloseMustNotBlockOtherSessions`、`TestArchiveAuditConcurrentWarningMustUseFreshVerdictWithoutAwaitingReclamation`、`TestArchiveAuditFailedVictimsMustNotConsumeSuccessfulEvictionLimit`、`TestArchiveAuditPermanentDeletionMustRemoveOwnedRuntimeRecord`。
- `env -u GOROOT go test -timeout 20m ./cmd/... ./internal/...` 也未通过：`TestWorkspaceDeletionIsAtomicAndRetainsOtherWorkspaces`、`TestIsolatedNetworkHostRetainsProcessAfterGUIDetaches`、`TestExplicitArchiveDeletionStillRefusesActiveWork`、`TestArchiveDeletionRefusesUnverifiedLiveRuntime`、`TestWorkerArchiveCleanupUsesOwningClockAndSkipsLiveWork`、`TestArchiveDeletionCannotBypassUnforcedMaintenance` 失败；该轮剩余 UI 测试按进程身份核对后主动终止，无子进程。网络后台启动用例随后单独重跑通过（6.428s）。全量执行期间 store／worker 文件存在本轮之外的并行修改；本次附件改动不涉及这些文件，未对失败归因，也未修复它们。

## 2026-11-17 远程上传与路径引用实现

已按画板 A 接入原生输入框上方确认区。类型不支持的本机文件先保存私有不可变副本；目标为远程设备时，只有明确“上传并引用”后才开始传输。支持真实回执字节进度、逐项／整体取消、失败重试，以及已完成文件的原生行内标签、独立撤销与元数据查看。准备中的资源不进入正文标签，不会因继续打字丢失；未完成或失败资源不能被发送操作静默遗漏。长文件列表内部滚动，确认／取消／重试保持可见。

“对此设备不再提示”只有确认时才按稳定 DeviceID 持久化。勾选草稿也按工作区、设备及编辑代次隔离，取消不记住勾选；同名设备、不同地址和 SSH／Tailnet 路由不会扩大确认范围。每个待发资源有自己的接纳设备／工作区与确认状态；普通附件上传器及其重试入口不处理路径资源，切换工作区不重新绑定或传到另一台设备。已取消或完成的资源不会被退场动画保留的旧副本再次上传。

协议 minor 47 新增 `file-path` 资源及 `PathAttachments` 能力声明。worker 仍在原有私有资源目录按工作区和随机资源 ID 保存，验证大小、摘要和不可变元数据；未完成上传及跨工作区引用被拒绝。客户端不能提供该资源的远端写入／来源路径。Codex adapter 仅传原文件名和所属 worker 决定的实际路径，不把二进制内容作为文本／图片输入，不修改用户仓库，不扩大所选权限。ASCII PDF 也按类型进入路径流程，不能因字节恰好是 UTF-8 而绕过确认。

保留单文件 8 MiB、最多 8 个文件及合计 16 MiB；目录、空文件和超限继续报错。旧 worker 或不支持路径引用的 Agent 明确不可用，正常旧版图片／文本流程保留。客户端重启后，已确认但未完成的文件保持可重试状态，不自动发送或补传。取消的临时内容沿用既有资源保留／清理机制；本轮未引入自动删除已完成原始资源的策略。

实现规格：[远程附件上传与路径引用](ryugi/specs/2026-11-17-remote-attachment-upload.md)。自动验证使用真实认证 worker 连接、实际 Codex app-server 加本地确定性模型端点；原生状态截图来自生产 MyGo 渲染器的明确状态样例。正式远程设备升级和真实用户环境的部署验收另行进行，不修改现有设备、权限、凭据或 Tailnet 节点。

本轮源码提交为 `4021878`、`bb79bd7`、`e24aa8d`、`f4ff331`。最终审查发现双会话窗格中附件准备回调错误使用主窗格工作区，已用原始窗格作用域修复；远程／本机及有效／已失效的右窗格回填均有回归检查。审查没有其他 Critical 或 Minor 问题。

最终验证（本机 macOS arm64 共享源码工作区，包含其他任务的修改；2026-11-17）：

- `env -u GOROOT go test -race ./internal/attachments ./internal/app ./internal/ui ./internal/worker ./internal/harness/codex -run 'TestRemoteAttachment|TestPathAttachment|TestAttachmentRejectsBinary|TestHistoryEditorEntryDoesNotRewrapInlineMessageForOneFrame' -count=1` 五个包通过。
- `env -u GOROOT go test ./internal/worker -run '^TestPathAttachmentActualWorkerAndCodexUseOwningDevicePath$' -v -count=1` 通过（1.860s）。实际认证 worker／Codex 链路验证完整路径输入、拒绝未完成／伪造／跨工作区资源和另一客户端下载字节一致。
- 生产 MyGo 渲染器生成亮暗、900／1280 DIP 确认、上传、失败和完成截图，并录制确认／勾选／取消／再次添加。原生截图与已选设计 A 的两组对照保存在本机画板，视觉验收尚待用户判断；截图不是物理远程部署证据。
- `SHIOJI_APP_PATH=build/remote-attachments/Shioji.app CODE_SIGN_IDENTITY=- SHIOJI_RELEASE_UPDATES=0 bash packaging/build-dev.sh` 成功；`codesign --verify --deep --strict --verbose=2 build/remote-attachments/Shioji.app` 通过。独立开发版位于 `build/remote-attachments/Shioji.app`，worker 自述 major 2／minor 47、revision `f4ff331+modified`。未安装或覆盖正式 App。
- `python3 scripts/verify-design-freeze.py --repository` 通过，6 个冻结文件一致。实际局域网 HTTP 验证报告、外层／嵌入画板、图片与录屏完成渲染／播放，无页面和控制台错误；非安全上下文中生成 1000 个唯一合法 v4 UUID。LAN 补丁回归检查通过。
- `env -u GOROOT go test -timeout 20m ./cmd/... ./internal/...` 未通过：仅剩 `TestWorkspaceHeadingSharesTheWorkspaceRowVisibleEdges`（16 个子例）与 `TestWorkspaceHeadingInsetsAndNoPerWorkspacePlus` 的导航标题左右间距断言失败。该导航基线在附件实现前的 `923f4e2` 已改动，本轮未修改相关导航。UI 包 381.273s，worker 包 196.206s，其余包通过。首次 `./...` 另包含被忽略的 `build/worker-recovery-20261116` 临时程序重复定义 `main`／`ident`；当轮发现的本任务历史编辑 10 DIP 间距回归已修复，上述最终专项包含其检查。

本机验证报告与原始证据保存在 `.ryugi/artifacts/verify/remote-attachment-upload/`；原生对照及录屏在 `.ryugi/artifacts/artboard/remote-attachment-upload/pages/verify/`。没有发布云端。预览端口 27320 复用已有服务，不因本任务完成停止其他任务的预览；临时无头浏览器已关闭。
