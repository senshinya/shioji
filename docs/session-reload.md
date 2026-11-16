# 会话重新加载

左侧会话行的右键菜单提供「重新加载」，与该行的更多菜单共用入口。操作针对点击的会话及其所属设备，不切换当前阅读的会话，也不清除正文、普通草稿、历史编辑稿、附件或暂停的待发队列。

只有支持协议 minor 45 的已连接 worker 和明确支持重新加载的 Agent（接续历史与正常卸载均可用） 可以执行。运行中的轮次、待回答问题／授权、未确认输入、独立后台工作、未完成历史操作和未暂停的待发队列阻止重载；worker 在与提交、队列派发相同的边界内重新验证，不能仅依赖菜单状态。

worker 先持久化带稳定操作标识的接纳回执，再正常卸载旧会话、回收其运行环境、创建新环境并接续同一个不透明历史引用。过程不调用 Submit、Steer 或历史替换，不创建新会话，不改变 Git 分支和启动目录。重载失败保留历史与草稿；不能确认旧代次退出时，沿用已有执行归属核对。

`session.reload` 接纳操作，`session.reload.operation` 只查询原回执。重复请求返回同一结果；连接失败后客户端保留原标识，重新打开菜单时查询，不重新启动。worker 重启时把未完成的重载标记为失败，不重放输入或重载。

Codex adapter 使用原生 `thread/unsubscribe` 并确认线程卸载，让 SessionEnd 钩子先运行；新进程再执行 `thread/resume`。由 worker 创建的线程使用会话级 `thread_unload_delay_secs=0`，不改用户的全局配置。共享 worker 不包含 Codex 协议方法名。

## 插件监听器的生效时机

已验证的 Codex 版本把 SessionStart 钩子排到恢复后的第一条正常输入执行。重新加载会结束旧 Ryugi 监听器并恢复新运行环境；新版监听器在下一条正常输入时启动。Shioji 不为启动插件偷偷发送占位消息。真实 app-server、exec-server、Ryugi hooks 和私有回调管道的隔离验证见 `TestSessionReloadRealCodexKeepsThreadAndRestartsWatcherOnNextInput`；模型服务使用本地确定性夹具，不是生产账号调用。
