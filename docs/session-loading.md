# 会话初次读取

打开会话且没有可显示的历史时，原生消息区提供读取反馈。已有消息缓存直接显示，普通历史翻页、后台刷新、历史修改和新会话发送不重播这个加载状态。每个并排会话独立维护一次展示生命周期，切换设备、连接或会话后不复用旧状态。

## 历史改写与分叉

收到另一端的历史改写事件后，旧线程的已读状态、分页请求和查询锚点一起失效。无论从侧栏重开还是从归档列表返回，该会话都等待新线程的最新历史。有分页能力的 worker 由会话视图重新读取，旧版 worker 由控制器重新读取完整历史。本机发起改写的窗口沿用已有的修订气泡和阅读锚点。

已知边界：如果另一端在断线期间改写历史，且重连快照跨过该改写事件，旧线程缓存和阅读锚点仍可能保留到下一次历史读取。本次保留这项行为。

分叉保留已确认来源的版本检查。只有能证明本地保留内容完整时，才用源历史立即预填分叉；来源还有更早或更新分页时，等待所属 worker 的第一份完整显示窗口，并由既有的锚点交接定位到所选回复。

浅色和深色的三条原生回归覆盖快速切回、从归档返回及部分历史分叉。定向测试、旧版 worker 读取、竞态检查、客户端构建和设计冻结通过。局域网 HTTP 画板与嵌入内容已检查 UUID 兼容、两段完整播放、暂停、跳转及 Chrome 控制台。录制和日志位于 `.ryugi/audits/session-entry-content/replacement-fix/`，使用确定性的 worker 回执。实际安装版和设备仍待复验。

`go test -timeout 15m ./cmd/... ./internal/...` 全部通过，UI 包 397.024 s，worker 包 236.262 s（`source.log`）。

## 2026-11-18：初始读取登记前的实时尾部

继续排查发现，实时事件也可以携带完整会话元数据，因此会话 ID 无法证明历史已加载。快速切走取消初读后，已到达的尾部事件仍留在模型中。另一个复现路径是重开曾跳到旧消息的会话，界面先显示较早分页，controller 随后才清掉旧页并读取最新历史。

入口现在以成功读取的历史为依据。分页协议通过权威历史窗口建立缓存，旧版 worker 在全量读取成功后记录同样的状态。正常刷新保留已读来源；初读取消不会将实时消息升级成缓存。重开只有较早分页的会话时，保留读取反馈，最新窗口到达后再从底部显示。当前会话中的旧页在连接恢复时继续显示，并保留阅读位置。已确认分叉继续从核对过的历史前缀接续显示。

两条路径均用相同的原生视图和确定性回执逐帧验证。亮暗各一段，均以半速播放，前半段是带会话元数据的事件，后半段是旧分页重入，左侧为修复前，右侧为修复后。复现、定向测试与竞态输出保存在 `.ryugi/audits/session-entry-content/fix/`。局域网 HTTP 下已重新检查 UUID 兼容补丁、外层页面、嵌入画板、完成播放及进度跳转，Chrome 控制台没有消息。安装版和真实远程设备上的行为仍待验证。

`go test -timeout 15m ./cmd/... ./internal/...` 最终通过，UI 包耗时 364.067 s（`source-recheck.log`）。首次运行发现七组测试夹具直接注入历史但未建立已读状态，或在未进入会话时提前注入旧页。还原测试的真实初始化顺序后，七组定向复检与完整套件均通过。定向竞态、UI/app/domain vet、设计冻结检查及 Apple Silicon 客户端构建通过。

### 上轮排查记录

未打开的会话可能先因请求、队列或后台工作建立仅含元数据的 detail，再收到一条实时消息。此时初始历史读取尚未登记。旧判断仅检查 `Items` 非空，会把这条消息当作历史缓存，提前结束读取呈现；后续 `Uncached` 判断也会继承这个错误，完整历史返回时才闪出全部内容。

入口现在要求已读取的会话身份或有效的权威历史窗口，才接纳其内容为缓存；新旧协议的初始读取及显式重试同时保留这一来源判断。事件尾部继续留在模型中供历史合并，不清空消息、草稿或执行状态。已有缓存和真实空历史沿用原交接。

`TestSessionEntryWaitsForHistoryWhenLiveTailPrecedesRead` 通过真实导航、`ApplyEvent`、`BeginHistoryRead` 与 `MergeHistoryPage` 复现这一时序；旧代码在亮暗模式下均报告 `before=1 pending=1 uncached=false`，修正后通过。`TestInitialHistoryDoesNotTreatPreexistingEventTailAsCache` 覆盖 controller 的实际初始读取回调；旧代码同样失败。`TestSessionEntryMarkdownFirstPaint` 逐次检查原生绘制，确认缓存 Markdown 入口不依赖补帧才显示完整布局。

本地前后证据位于 `docs/research/session-entry-content/`；Go overlay 保留原生产逻辑，与修正后的同一测试分别录制。两段连续对照为生产 MyGo `ui.Tester` 软件渲染、确定性历史回执、左旧右新，各 101 帧；不是已安装 App 或真实远程网络录制。安装版、worker 和 Tailnet 未替换或重启。局域网 HTTP 验收页已重新修补 UUID 启动兼容、核对外层与嵌入画板、两段完整播放，Chrome 控制台为 0 条消息。

本轮检查记录位于 `.ryugi/audits/session-entry-content/`：

- 加载态、首次历史／实时尾部、两个新入口回归及 controller 来源回归通过（`targeted.log`）；入口、缓存重入、加载态与新旧协议初读的定向竞态检查通过（UI 185.792 s，app 2.048 s，`race.log`）。客户端构建、`go vet ./internal/ui ./internal/app`、仓库冻结设计与 diff 检查通过。
- `go test ./cmd/... ./internal/...` 完整运行结束，UI 通过（509.199 s），app 通过（24.358 s）。全量退出码为 1：`TestPrivateLifecycleSignedChildDrainsBeforeOrWithoutBusinessSocket/missing-business-socket`、`TestStopReleasesMaintenanceSlotBeforeLateReply`、`TestLateWorkReplyCannotConfirmRetiredInput/true` 失败（`tests.log`），均未在本次会话入口修改中调整。私有生命周期单独复检通过（8.968 s，`lifecycle-recheck.log`），不据此抹去全量失败。
- 上述另外两个 worker 失败项单独复检通过（6.399 s，`worker-recheck.log`）。保留第一次完整运行的三个失败项，不将重试通过写成完整套件通过。
- 全仓 `go test ./...` 还会遍历忽略的本地 `build/` 实验源码。其编译检查仍被 `build/composer-enter/baseline` 的内部包导入、`build/interaction-latency/baseline` 的缺失类型及 `build/worker-recovery-20261116` 的重复声明阻断（`repository-compile.log`）。首次全仓运行还读到了本轮 overlay 原源码的 `.go` 副本；这些副本已改为 `.go.txt`，复检不再包含这个临时包，未修改既有实验档案。

## 已确认的外观与时序

- 使用 [Math Curve Loaders 的 Rose Curve](https://paidax01.github.io/math-curve-loaders/) 五瓣曲线。图形槽位为 168 DIP，轨迹周期为 6.4 秒；保留参考的 78 粒子渐隐尾迹，旋转与轻微胀缩按相同比例放慢。
- 视觉组合位于标题栏与底部面板之间，因底部发送框预留而自然偏上。曲线上方不放消息骨架，下方保留少量静态正文骨架。
- 切换到无历史缓存的会话后，正文骨架与底部发送框骨架立即以 120 ms 原地淡入，使用与加载组合退出相同的缓动。只有曲线与带灰阶扫光的“正在读取会话”延迟 200 ms，再以 120 ms 原地淡入；预留它们的空间，骨架不随曲线出现而移动。
- 首次读取在 200 ms 内完成时，正文与输入框内容立即开始渐显，骨架从当前透明度同步用 120 ms 淡出，不等待加载组合退出；输入框随内容开始显示即可使用。已有权威历史缓存直接展示。
- 等待达到 200 ms 的读取在内容就绪后，读取组合以 120 ms 原地淡出；正文接替，发送框骨架用 180 ms 淡出，内部内容在随后 240 ms 内原地渐显。所有这些交接均无上下位移。
- 若读取在骨架或曲线淡入途中完成，从当前透明度继续退出，不跳到全亮；未出现的曲线与文字不补播。已有权威历史缓存仍直接展示。
- 发送框沿用真实原生编辑器的测量、背景与边框，骨架只覆盖内容，因此长草稿、附件和队列的实际空间不会因交接而跳动。加载时保留原草稿与编辑器身份，但尚未显示的控件不接纳输入。
- Reduce Motion 立即显示静态骨架，200 ms 后使用静态五瓣曲线与普通文字，不运行粒子、旋转、胀缩或扫光；内容就绪后直接交接。

## 状态边界

展示读取反馈不会创建会话、提交输入、改变 worker 的操作接纳、触发模型执行或延长 RPC 超时。已知活动轮的停止入口与待处理问题／审批仍可使用，初始历史读取不会接管它们。

读取失败停止曲线和扫光，原位显示“未能读取会话”与重试。断线显示明确的断开状态，重试请求连接所属设备。初始读取和重试沿用已有的 owning-device 请求、revision 与连接 epoch 栅栏；权威返回的空历史也可以完成加载。旧版 worker 的完整历史读取同样发布 pending／error／完成状态。

实现位于 `internal/ui/session_loading.go`，并由原有会话、发送框与历史读取路径接入。回归覆盖延迟显示、快速与缓存路径、权威空历史、失败／重试、断线、并排视图、历史替换不重播、草稿保留、可用的 Stop／待处理请求、原地交接的像素中间帧与 Reduce Motion。

原生截图与录制通过生产 MyGo 组件和确定性的 worker 历史回执生成：

```sh
SHIOJI_SESSION_LOADING_CAPTURE="$PWD/docs/research/session-loading-2026-11-16" \
  env -u GOROOT go test ./internal/ui -run '^TestSessionLoadingNativeRecording$' -count=1
```

这些本地证据保留在忽略的研究目录中，不替代真实远程设备的网络性能测量，也不会安装或重启现有 App、worker 或 Tailnet 服务。

切换入口的逐帧回归位于 `internal/ui/session_loading_entry_test.go`，覆盖亮暗模式、Reduce Motion、骨架立即渐显、延迟曲线不改变骨架位置、快速返回不等待退出，以及读取在两种入场动画中完成时的像素连续性。以下命令重放生产原生视图：先显示已有会话 250 ms，再切换目标会话，分别在瞬时、约 50、250、1000 ms 返回历史，每段采集 150 帧／2.5 秒：

```sh
SHIOJI_SESSION_LOADING_ENTRY_CAPTURE="$PWD/docs/research/session-loading-entry/fast-after" \
  env -u GOROOT go test ./internal/ui -run '^TestSessionLoadingEntryNativeRecording$' -count=1
```

## 本轮验证

- `env -u GOROOT go test ./cmd/... ./internal/...`：本轮完整运行通过，包括原生 UI 与 worker。下述后续并行改动不包含在这次编译快照中。
- `env -u GOROOT go vet ./internal/ui ./internal/app`：通过。首次检查时另一项并行工作移除了 `session_entry_gap_test.go`，Go 的旧文件清单失效；按当前文件清单重新检查已通过。
- `env -u GOROOT go build -o build/session-loading/Shioji ./cmd/shioji`：原生客户端编译通过；没有覆盖或启动现有 App。
- `python3 scripts/verify-design-freeze.py --repository` 与 `python3 scripts/generate-third-party-notices.py --check`：通过。
- 最早的 `go test ./...` 包含本地忽略的 `build/worker-recovery-20261116`，其中多个独立探针声明相同的 `main`／`ident`，因此该命令失败。同一旧快照还出现 `TestNewReplyInkFadesInWithoutFadingExistingText` 的正文墨迹断言失败；当前产品源码包完整运行已包含该测试并通过。没有修改这些临时探针。
- 完整 `go test -race ./internal/ui ./internal/app` 中，`internal/app` 通过；原生 UI 在既有 `TestChildDetailStatusIconAlignsToItsCaptionInk` 的软件像素渲染中达到默认 10 分钟上限，未完成整个 UI 包的竞态验证。
- `go test -race ./internal/ui ./internal/app -run '^(TestSessionLoading|TestLegacyHistory|TestLatestHistoryReadUsesOneIndependentCancellableRequest|TestOpeningShortSession)' -count=1 -timeout=5m`：通过；原生加载、旧协议读取、独立历史连接与短历史入口均完成竞态检查。
- 共享工作区随后出现滚动布局的并行改动：`TestPagedHistoryPrefetchesBeforeReachingTheEdge/newer=true` 在普通及竞态运行中报告缺少 `session.history` 请求。通过 Go overlay 禁用本轮加载接入后仍同样失败，隔离证据保留在 `.ryugi/audits/session-loading/parallel-isolation/`；没有覆盖并行工作的滚动实现。
- 局域网 HTTP 下已核对外层画板、嵌入内容、两个原生视频的独立播放及失败／Reduce Motion 截图；控制台和 `pageerror` 无错误，HTTP UUID 补丁在当前实际服务版本上重新安装。

## 2026-11-17 入口骨架修订

- 先在旧实现运行新增入口回归，复现切换 40 ms 后没有正文骨架，以及读取在 40／240 ms 完成时发送框透明度跳变；随后修改显示时序。修改前后均通过生产 MyGo 视图及确定性历史回执录制，不是安装中 App 的远程性能测量。
- 当前合并源码的 `env -u GOROOT go test ./internal/ui -run '^(TestSessionLoading|TestInitialHistoryWaitsForReadDespiteLiveTail)' -count=1` 通过（12.760 s），同范围 `-race -timeout=3m` 通过（65.187 s）。覆盖原有加载边界、新增入口、位置、像素连续性，以及另一项修复的首次历史读取／实时尾部边界；录制和构建也已按该合并源码刷新。
- `env -u GOROOT go vet ./internal/ui ./internal/app`、`python3 scripts/verify-design-freeze.py --repository` 与 `git diff --check` 通过。
- `env -u GOROOT go build -o build/session-loading-entry/shioji ./cmd/shioji` 通过；本次验证未覆盖或启动现有 App。
- 亮暗模式的 60 fps 前后对比保留在 `docs/research/session-loading-entry/comparison-light.mp4` 和 `comparison-dark.mp4`：左侧为修改前，右侧为修改后，依次在约 50、250、600 ms 返回历史。原始逐帧 PNG 位于同目录 `before/` 与 `after/`。
- 全量 `env -u GOROOT go test ./cmd/... ./internal/...` 已结束：原生 UI 包通过（425.958 s），worker 包通过（239.719 s）；唯一报告失败为 `TestSignedWorkerUpdateKeepsIdentityAndCapturesRunningBuild`（`automatic update did not reconnect`），因此全量退出码为 1。该 worker 升级链路未纳入本次修改。完整输出保留在 `.ryugi/audits/session-loading-entry/tests.log`，不将相关加载回归通过写成全量通过。

## 快速读取与半速录制

- 将 200 ms 内完成的读取从完整退出序列分离：正文和输入框不再等待退出；骨架与真实内容同时交接，120 ms 内完成，保持淡入途中返回时的像素连续性。发送框外框继续渐显时，按其实际透明度计算骨架透明度，避免骨架在退出时反而变亮。
- `TestSessionLoadingFastReadDoesNotWaitForSkeletonExit` 覆盖瞬时、16、50、150、199 ms 返回后的正文透明度、输入可用性和完成时限；移除快速分支的 Go overlay 在 50／150／199 ms 分支失败，当前实现的 `go test ./internal/ui -run '^TestSessionLoading' -count=1` 通过。
- 修改前后的生产原生视图帧分别保留在 `docs/research/session-loading-entry/fast-before/` 与 `fast-after/`。当前 App 没有替换，录制使用确定性历史回执，不是远程网络速度测量。
- `half-speed/instant.mp4`、`fast-50ms.mp4`、`medium-250ms.mp4`、`slow-1000ms.mp4` 均为修改后的 0.5 倍速录制，左亮右暗；每段保留全部 150 帧，以 30 fps 播放 5 秒。已检查帧数、时长并完整解码四段视频。
- 当前快速分支的加载态相关回归通过（2.273 s）；加载态与首次历史／实时尾部交界的竞态回归通过（72.371 s）。原生客户端构建、`go vet ./internal/ui ./internal/app`、仓库冻结稿校验与本次 diff 检查通过。
- 本次快速分支修改后的完整 `env -u GOROOT go test ./cmd/... ./internal/...` 已结束：UI 包通过（350.188 s），执行服务命令包通过；worker 包的 `TestSupervisedStartupArchivesHistoricalPartialFenceWithoutLosingReceipt` 失败，报告历史 partial fence 仍阻止固定服务启动，因此全量退出码为 1。该升级迁移链路未在本次修改中调整；输出保留在 `.ryugi/audits/session-loading-fast/tests.log`。本次验证未替换当前 App。

## 提交范围验证

- 干净检出只带入本次加载态实现、入口回归和本文档 3 个文件，未带入共享工作区的 `Uncached` 判断及 worker 升级改造。客户端构建、UI／app 静态检查、冻结稿与依赖声明校验通过。
- `env -u GOROOT go test -timeout 20m ./...` 已覆盖全部包；首次只有 4 项 Mermaid UI 测试因干净检出未准备图表运行时失败，其余测试通过。按 CI 的 `python3 scripts/bundle-diagram.py` 准备运行时后，`go test ./internal/ui -run '^(TestMermaid|TestSessionLoading)' -count=1` 复验通过（4.877 s）。首次输出与复验输出分别保留在 `.ryugi/audits/session-loading-ship/tests.log`、`targeted-tests.log`，未将首次运行记录改写为全量通过。
