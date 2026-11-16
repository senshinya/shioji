# 会话初次读取

打开会话且没有可显示的历史时，原生消息区提供读取反馈。已有消息缓存直接显示，普通历史翻页、后台刷新、历史修改和新会话发送不重播这个加载状态。每个并排会话独立维护一次展示生命周期，切换设备、连接或会话后不复用旧状态。

## 已确认的外观与时序

- 使用 [Math Curve Loaders 的 Rose Curve](https://paidax01.github.io/math-curve-loaders/) 五瓣曲线。图形槽位为 168 DIP，轨迹周期为 6.4 秒；保留参考的 78 粒子渐隐尾迹，旋转与轻微胀缩按相同比例放慢。
- 视觉组合位于标题栏与底部面板之间，因底部发送框预留而自然偏上。曲线上方不放消息骨架，下方保留少量静态正文骨架。
- 等待 200 ms 后，曲线、带灰阶扫光的“正在读取会话”、正文骨架与底部发送框骨架同步以 120 ms 原地淡入。
- 真实内容就绪后，读取组合以 120 ms 原地淡出；正文接替，发送框骨架用 180 ms 淡出，内部内容在随后 240 ms 内原地渐显。所有这些交接均无上下位移。
- 发送框沿用真实原生编辑器的测量、背景与边框，骨架只覆盖内容，因此长草稿、附件和队列的实际空间不会因交接而跳动。加载时保留原草稿与编辑器身份，但尚未显示的控件不接纳输入。
- Reduce Motion 使用静态五瓣曲线与普通文字，不运行粒子、旋转、胀缩或扫光；内容就绪后直接交接。

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
