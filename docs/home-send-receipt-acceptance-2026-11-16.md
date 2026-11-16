# 新会话发送时间与回执交接

2026-11-16，用户已验收通过并授权合入 main、推送。

- 点击发送时，视图保存一次本机当前时间，用于新会话的临时用户消息；等待回执不更新该值。
- owning worker 的消息到达后，使用其持久化 `SentAt`。临时时间不写入 worker 协议、提交内容或权威历史。
- 用户消息下方始终保留编辑、复制、时间。消息未确认或尚无 TurnID 时，编辑按钮禁用；复制保持可用。确认后原位置启用编辑，不重新插入按钮或播放按钮入场。

## 自动检查

- 新增回归在修改前分别因“临时消息没有发送时间”和“等待时隐藏编辑按钮”失败，修改后通过。
- `env -u GOROOT go test ./internal/ui -run 'Test(HomeSend|HomeSent|PendingHome|WorkerAcceptance|LateWorkerReceipt|PreparingFooter|HistoryTimestamp|UserMessageActions)' -count=1`：通过，6.501 秒。
- 回归覆盖等待两分钟仍保持点击时间、worker 时间替换、等待时编辑禁用及复制可用、确认后编辑可用，以及亮暗模式回执交接期间逐帧固定操作和时间坐标。
- `python3 scripts/verify-design-freeze.py --repository`：6 个冻结文件通过。
- `python3 -m unittest scripts/test_lan_preview.py`：通过。
- `git diff --check`：通过。

首次 `env -u GOROOT go test ./...` 未通过，记录到本轮范围外的并行改动：

- `shioji/build/worker-recovery-20261116` 中 `inspect.go`、`verify.go`、`cleanup.go` 存在重复 `main` / `ident` 声明。
- `TestCommandWheelScalesConversationChromeAndKeepsComposerEditing` 失败。
- `TestCommandScrollIsReversibleBoundedAndIndependentOfCurrentZoom` 失败。
- `TestInterfaceZoomSurvivesClientRestart` 失败。

另行运行的 `env -u GOROOT go test ./cmd/... ./internal/...`：全部 UI 测试通过（402.616 秒），但 worker 的 `TestDeletedTitleJobDoesNotStopNamingConsumer` 因“运行环境暂不可用”失败。其余产品包通过。全量检查并非全绿，不把这些范围外失败当作本轮通过。

该 worker 测试单独重跑 `env -u GOROOT go test ./internal/worker -run '^TestDeletedTitleJobDoesNotStopNamingConsumer$' -count=1` 通过（0.737 秒）；保留全量运行中的原始失败记录。

## 提交前独立验证

从 main 的 `15647bd` 导出独立源码，只应用本轮 3 个生产文件、1 个回归测试文件与此验收记录，排除其他未提交工作和本地临时程序。该独立源码运行 `env -u GOROOT go test -timeout 20m ./...` 全部通过；UI 包 319.310 秒，worker 包 151.240 秒。冻结设计 6 个文件、47 项第三方声明、25 个脚本测试及源码／历史密钥扫描均通过。

审查范围符合请求，标准深度，未发现阻止提交的问题；纯 UI、测试和记录改动无需额外安全或架构专项审查。仅提交本轮 5 个文件，`view.go` 只纳入发送时间的一处修改。

## 原生与局域网证据

本地 `build/home-send-receipt/` 保存原生程序、构建覆盖、采集脚本、四段视频、截图、操作树及逐帧时间。修改前覆盖只撤销本轮三个生产文件中的相关修改，不修改仓库文件。

亮暗模式分别采集修改前后 MyGo 原生 Metal 窗口，2400×1640，目标 12fps，约 8 秒，按实际采集间隔编码，无插帧。夹具在点击后约 2 秒接纳会话、4 秒提供权威消息、6 秒确认发送；为展示数字更新，worker 时间故意偏移 1 分钟。没有调用真实模型或用户 worker。

修改后原生操作树在四个阶段的编辑、复制、时间坐标完全相同；编辑状态从 `disabled` 切换为 `focusable`。局域网验收页包含亮暗模式前后截图与完整视频，亮暗两个分组均已依据用户本轮答复记录为通过。

最终启动后已对 27319 端口执行 `scripts/prepare-lan-preview.py`。通过实际 `http://192.168.7.131:27319` 地址打开外层页面及内嵌画板，确认非安全上下文 UUID v4 可用，四段视频完整播放、暂停与跳转正常，`pageerror` 和控制台错误均为零。结果保存于 `build/home-send-receipt/lan-check.json`。

原生采集进程均已退出。验收页复用现有 Ryugi 服务，未停止其他任务的共享服务。
