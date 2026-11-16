# 会话入口的底部定位

会话首个绘制帧的消息尾部必须使用当前会话、当前宽度的完整底部面板高度。不能将上一会话的 `dockHeight` 用于消息留白，再在绘制后修正；这会使列表已经处于末端时仍留下正常滚动无法到达的大块空白，随后突然跳回。

`ListState.EndOverlay` 在列表布局中测量同帧构建的底部面板，将高度计入尾部 padding。底部面板保持独立，滚动区域仍延伸到窗口底部。面板高度变化不叠加滚动跟随动画；新内容继续按原有方式跟随，阅读历史时保持锚点。清除 overlay 不保留旧 inset，空列表也计入实际预留高度。

原先的测试在 `Tester.Frame()` 自动补帧后检查位置，漏掉了真正的首个绘制帧。`internal/ui/session_entry_gap_test.go` 现在在绘制回调中记录每一帧，覆盖亮暗模式、减少动态效果、缓存主会话与首次进入的右侧会话。`third_party/mygo/ui/list_end_overlay_test.go` 覆盖首帧、虚拟化、overlay 移除、空内容、实时跟随和阅读锚点。

真实面板高度也会计入离屏时的滚动范围估计。历史预取仍使用原来的三分之四视口阈值；分页测试显式确认夹具位于该阈值内，避免依赖未测量尾部行的偏小估计。

可选原生组件录制：

```sh
SHIOJI_SESSION_ENTRY_RECORDING="$PWD/docs/research/session-entry-gap/fixed" \
  env -u GOROOT go test ./internal/ui -run '^TestSessionEntryNativeRecording$' -count=1
```

录制使用生产 MyGo 组件及确定性的缓存历史和草稿夹具，以 30 fps 采样，额外保留自动补帧前的首个绘制帧。时间线：1 秒进入多行草稿会话，3 秒返回普通会话，4 秒向上阅读，5 秒切换后快速返回。亮暗模式分别录制。这验证原生布局与会话入口交接，不是远程网络性能或系统输入法验收，不会修改已安装 App、worker 或 Tailnet 服务。

验证：隔离补丁的 `go test ./...` 全部通过；当前主线相关 UI 回归、首帧竞态测试、MyGo UI／updater 测试、UI vet、冻结设计、notices、25 项 Python 检查、secret scan 和开发包构建通过。局域网验收页已检查外层与嵌入画板、四段完整播放、暂停、跳转及控制台。全项目 vet 仍报告既有的 `internal/tailnet/identity_darwin.go` 第 74、81、129 行 `unsafe.Pointer` 告警，相关文件未修改。
