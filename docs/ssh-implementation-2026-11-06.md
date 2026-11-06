# Direct SSH 与同设备多客户端

状态：开发环境闭环已接通；人工待验收。用户已要求开始，并授权使用局域网 MacBook Air 做实机验证。

## 本轮范围

- 系统 OpenSSH 复用已有用户认证与 SSH config，强制主机校验，不保存密码、不修改用户密钥和系统 SSH 设置。
- 自有 worker 增加私有 stdio bridge 和开发环境 bootstrap；桥只连接同用户的 worker Unix socket，stdout 仅传协议。控制端在命令与事件连接上使用同一传输边界。
- 原生设备集合、独立连接状态和 readiness、远程路径输入；远程目录验证、文件和 Git 操作由 owning worker 执行。
- 客户端独立保存显式添加的设备。每台设备独立订阅快照／事件，后台任务不依赖选中会话；A SSH 与 B 本地 IPC 指向同一 B worker 和 DeviceID。
- 断线重连保留草稿与待发队列，重新核对已接受操作，不重发未知提交。

## 工程选择与验收边界

沿用已批准实现规格的 Go／MyGo、设备权威存储与版本化 byte stream。本轮先交付开发环境 SSH 闭环；正式 Runtime.app／SMAppService、Developer ID 安装／升级、公证和嵌入式 tsnet 仍属于后续阶段，不用 ad-hoc 开发部署冒充正式分发。

新设备必须由用户主动添加。已有兼容 worker 直接复用，不替换运行中的 worker；自有开发 worker 使用内容摘要与签名验证、版本目录和私有 staging。缺少兼容 Codex 时如实显示 setup，不上传第三方 Codex 或复制账号，后续安装／登录需要用户明确操作。

首次未知 SSH 主机先要求用户通过独立来源核对并建立 OpenSSH 信任；本轮不自动接受 key，也不提供关闭校验的选项。传输和 worker 设备身份各自验证，地址变化不改变已绑定 DeviceID。

## 工作记录

- 已找到先前独立校验的 Air SSH 主机并重新以严格校验连接成功，确认 arm64／macOS 27.2。
- 首次只读检查：Air 原先无 Shioji worker 或数据库；已找到其已有 npm 安装中的原生 Codex 0.166.0，并通过自有 worker 取得 ready，无需复制账号或安装第三方程序。
- 分步执行：bridge／SSH 传输 → 自有开发 bootstrap → 客户端设备聚合与路由 → 原生设备／远程目录 UI → 双端、断线和并发实机验证。

- 已从原生设备页添加 Air、输入其绝对路径创建隔离 Git 工作区，并使用实际 6.1 sol / medium 完成两轮文件编辑。
- 第二轮只结束验收 GUI 的命令／watch 两条 SSH 子进程，原生界面显示断线待核对；自动重连后同一 worker 继续完成。协议复核为 1 个会话、2 个用户消息、2 个完成轮次和 2 个 operation，无重复提交；文件变更在原生 Diff 中显示。
- 自检修复：工作区弹窗请求代号，防止旧结果影响新弹窗；当前设备离线不阻挡其他设备的全局搜索；缓存以离线状态恢复，各设备 pin／sequence／epoch 独立。
- 安全复核发现并复现跨设备 Queue／Request／Operation 归属问题，现将快照、事件及 RPC 回执检查提前到 reducer／UI 回调前；pending 操作按连接隔离。新增回归覆盖其他设备的会话、权限、草稿不被覆盖，以及拒绝的内容不修改原缓存。

## 验证结果与待验收

- Air 本地第二个原生客户端连接同一默认 worker，打开 A 创建的同一工作区和会话；DeviceID／WorkspaceID／SessionID 一致，B 客户端的远程连接集合为空。
- 已验证严格主机校验：使用独立临时 known_hosts 放入错误 key，真实 OpenSSH 在远程命令执行前拒绝；不修改用户信任集合。
- `CGO_ENABLED=0 go test ./...`、`go vet ./...`，以及 app／transport／worker 的 race 检查通过；53 个录制输入帧的 pinned schema 与摘要校验通过。冻结设计的 6 个仓库可见文件保持一致。
- 设备页按相同亮色冻结稿核对集合宽度、详情阅读列、行高和图标；补充深色、添加 SSH、远程路径表单及 850×650 窄窗截图。窄窗在列表和详情间切换，详情超高时滚动。
- 实机截图保存在忽略的 `build/ssh-preview/screenshots/`，人工验收集中在局域网 `artboard/verify-ssh`。截图不作为用户已通过验收的证明。

本轮实机覆盖连接、远端建区／文件修改／Diff、SSH 桥中断恢复与 B 本地共享数据；执行审批交互、真实双任务并发、休眠／换网及进程崩溃恢复尚未完成本轮实机验收。单元测试或此前研究结果不抵扣这些边界。保留隔离验收仓库和运行中的 Air worker，便于后续检查。

后续自动实机验收补齐真实双任务、问题与执行审批的断线恢复、图片／代码输入及历史附件读取，见 [Air 会话验收报告](air-session-acceptance-2026-11-06.md)。长历史和进程故障使用另一个明确标注的协议回放 worker，发现并修复死进程记录阻止 runtime 重启的问题；不将其冒充真实模型故障注入，休眠与实际换网仍未覆盖。
