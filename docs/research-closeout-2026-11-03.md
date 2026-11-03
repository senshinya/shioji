# 技术调研收尾

2026-11-03。本轮按用户要求覆盖其余技术问题。随后用户恢复实机验证，真实 Tailnet 双向连接、严格主机校验 SSH、direct／DERP、节点身份恢复、原生 GUI Keychain 和真实中文候选窗已通过，详见 [补充实机验收](live-acceptance-2026-11-03.md)。VoiceOver 本轮由用户跳过，Air 休眠／重启再次延期。所有代码仍为一次性研究夹具，未开始 Tahuna 产品实现，冻结设计未修改。

调研已有足够依据编写实施规格。不能把本文理解为完整 worker、正式安装器或正式客户端已通过验收。结果分为实测通过、已定位的实现约束，以及需要正式制品／指定环境才能执行的验收。

## 可确定的技术建议

以下是调研建议，不扩大已经确认的产品范围：

| 环节 | 建议 |
| --- | --- |
| 平台 | 只做 Apple Silicon macOS，`darwin/arm64`；技术下限为 macOS 13，旧系统实机支持尚未验收 |
| 工具链／UI | Go 1.27.3，MyGo v0.7.4；原生控件、Markdown、代码与 diff，系统 API 处理窗口、菜单、文件选择和凭据 |
| 执行端 | 完整自有 Go worker，用户级后台服务；本地 GUI、SSH 客户端读取同一 worker／数据，不依赖 herdr-core |
| Codex | 固定 0.166.0 作为首个 adapter 契约基线；app-server stdio 只归 worker，诊断与 stdout 分离 |
| 网络 | 官方 tsnet v1.104.0；系统 OpenSSH 经私有 IPC 接入嵌入网络，保持主机校验与目标绑定 |
| 数据库 | modernc.org/sqlite v1.61.1，设备本地 WAL；单 writer、事务提交后确认、明确故障与不确定状态 |
| 文件／Git | 文件 API 用设备端 `os.Root` 约束路径；Git 按 canonical git-common-dir 排序操作，保留每个会话已接受的执行目录 |

Go 1.27 官方要求 macOS 13；合并实验二进制的 Mach-O 同样标记 `minos 13.0`。MyGo 的 macOS 12 文档下限不能覆盖所选 Go 工具链。Codex 固定二进制标记 minos 11.0，但不降低整个应用的最低版本。两台实机均为 macOS 27.2，13～26 的运行兼容性需要相应系统验证。[Go 1.27 发布说明](https://go.dev/doc/go1.27#darwin)。

## 本轮补齐的实测

| 项目 | 结果和证据 | 实际边界 |
| --- | --- | --- |
| 合并依赖构建 | [记录](research/macos-2026-11-03/combined-build.json)：MyGo＋tsnet＋SQLite，CGO_ENABLED=0，原生 arm64；SQLite 启动正常 | 验证链接和最小启动，不是完整运行时集成 |
| Hardened Runtime | ad-hoc 签名、deep/strict 校验通过；真实原生窗口显示正常 | 没有 Developer ID、可信时间戳、公证或 Gatekeeper 分发验收 |
| `SMAppService.agent` | [本机](research/macos-2026-11-03/service-management.json)、[Air](research/macos-2026-11-03/service-management-air.json)：bundle 解析、注册、启动 worker、注销和清理均通过 | Air 由 SSH 调用，用户已登录 GUI；不是注销、开机未登录或用户禁用后台项目后的验收 |
| OpenSSH 接嵌入网络 | [记录](research/macos-2026-11-03/tsnet-ssh.json)：私有 IPC→官方 tsnet→临时 SSH 服务，认证、二进制／EOF、错误 host key、旧 epoch 均验证 | 本地 control/DERP，不是真实 Tailnet 或实际 Air 的 tsnet SSH |
| app-server 崩溃 | [记录](research/macos-2026-11-03/codex-recovery.json)：等待问题、等待审批、输出一半时 SIGKILL，随后 resume | 旧 turn 在本例中存为 interrupted，旧请求不复活；没有自动模型调用 |
| 权限与真实工具执行 | [执行记录](research/macos-2026-11-03/codex-execution.json)：只读拒绝写入；workspace-write 允许当前目录写入、拒绝实验中的外部目录写入 | localhost 固定 provider、临时文件；不代表所有系统版本和权限 profile 已验收 |
| Codex contract | [路由表](research/macos-2026-11-03/codex-routes.json)、[检查](research/macos-2026-11-03/protocol-contract.json)：19 item、11 request、85 notification 全量分类；5 条合成事件经过固定 schema 校验 | 路由研究与小型投影实验，不是 115 个已实现 handler 或所有功能已启用 |
| SQLite 故障 | [本机](research/macos-2026-11-03/sqlite-fault-local.json)、[Air](research/macos-2026-11-03/sqlite-fault-air.json)：FULL、READONLY、BUSY、损坏副本、迁移中 SIGKILL、快照、备份、checkpoint | FULL 由 page quota 触发，不填满用户磁盘，不模拟物理断电 |
| 双栏流式与长历史 | [记录](research/macos-2026-11-03/mygo-stream.json)：每栏 5,000／50,000 逻辑行，双流推进、独立草稿与输入保留 | 模拟流，真实窗口激活后检查；不是实际 agent 负载或完整产品性能基准 |
| 原生键盘 | [记录](research/macos-2026-11-03/native-os-input.json)：系统窗口／AX、中文粘贴、Shift+Enter、Enter 发送与清空 | 中文已提交文本；没有把粘贴当作 IME 候选窗测试 |
| 文件 scope | [记录](research/macos-2026-11-03/root-scope.json)：父路径、外逃 symlink 拒绝；1,000 次 symlink 竞争未读到外部夹具 | worker 文件 API 的边界，不是 agent 进程的 OS sandbox |
| Git 上下文 | [记录](research/macos-2026-11-03/git-context.json)：主仓库／会话 worktree 分离，公共锁身份一致，dirty checkout 和重复分支被拒绝 | 临时仓库；生产串行队列、冲突 UI 和崩溃补偿尚待实现 |

此前的 Air 生命周期、双客户端一致性、操作去重和队列持久化证据继续有效，详见 [macOS 报告](macos-worker-research-2026-11-03.md) 与 [双客户端需求及验证](multi-client-workspaces.md)。

## 协议和恢复的明确结论

worker 存储必须区别 accepted、dispatched、confirmed 和 indeterminate。SQLite 幂等记录只能阻止本地重复接受；外部副作用已经发生、确认尚未落盘时仍有不确定窗口，不承诺 exactly-once，不自动重发 prompt、工具或队列。

app-server 存活而 SSH／GUI 断开时，待请求可继续存在；app-server 本身重启时，本轮三种在途场景都恢复为 interrupted，旧问题／审批没有重新发出，部分输出也未必进入 Codex 历史。因此 worker 要保留自己已经提交的内容，明确失效旧表单，重新读取权威历史；不得把原 pending 或 running 缓存继续显示为当前运行。

请求键使用 worker 所持有的 harness instance 与原始 typed request ID。数值 `0` 有效，与字符串 `"0"` 不同。GUI 重连不会创建新的 harness instance，也不能因此使仍在运行的待请求失效；私有 transport 的 epoch 只隔离过期连接，不等于任务生命周期。

`item/completed` 的最终内容覆盖流式增量；迟到增量不另起消息。未知 item 用有界可读兜底，未知 request 返回明确错误，不能默认允许。实时媒体、Windows、remote control 等已在路由表中显式分类，未纳入首期。`turn/plan/updated` 才是结构化计划步骤；普通文本 plan item 不冒充它。

工具写入被拒绝时，实验中 turn 仍可最终 completed；不能把“本轮结束”解释成“每个工具都成功”。工具预检失败也未必有 commandExecution completed item，应保留实际受支持的错误／内容事件，不从终端字符串推断成功活动。

最新请求 usage 与整轮 usage 分开。缺少整轮基线或任一字段时保持相应 unknown；cached input 是 input 子集，不能再次相加。实时本地计时不替代确认的完成耗时。

## 运行配置和权限

真实 `model/list`、`collaborationMode/list`、`configRequirements/read`、`permissionProfile/list` 均已查询。使用离线 fixture provider 时返回的是该上下文的 catalog，不是用户账号可用性或价格承诺。实际应用要逐设备／profile 查询、分页、应用 managed requirements，并在提交时校验，不能拿设计稿目录替代。

四个基础配置在实际 thread/start 中被接受：

| UI 名称 | sandbox | approvalPolicy |
| --- | --- | --- |
| Read only | read-only | never |
| Default | workspace-write | on-request |
| Auto Approve | workspace-write | never |
| YOLO | danger-full-access | never |

这是首个 adapter 的建议映射，不是对受管理设备的覆盖承诺。命名 permission profile 可进一步改变可用性；禁止的选项应关闭并解释限制。workspace-write 的实际有效访问还受 cwd、额外 roots、临时目录和网络设置影响，不能简化为 UI 工作区文件夹清单。改变 picker 不解决已经 pending 的请求。[官方 App Server 文档](https://developers.openai.com/codex/app-server/)。

Fast 从 catalog 的 serviceTiers／实际默认获取，独立于 effort；不根据模型名称猜测。Plan 作用于下一轮 collaboration mode，既不改写活动轮，也不等于计划 inspector。

## 原生组件的边界与性能

合并依赖的 stripped 二进制为 28,756,818 字节，gzip 数值见构建记录；未计入产品图标、资源、签名、公证封装或 Codex。当前 Codex 二进制本身约 257 MiB，是否随产品分发会显著影响制品体积。建议 worker 管理自己的受校验 Codex 版本目录，同时可探测已安装的兼容版本；不覆写 Homebrew 或用户 Codex，不因找不到它而悄悄使用另一台设备运行。

在已激活的双栏原生夹具中，每栏逻辑行数从 5,000 增至 50,000，采样 RSS 约 120～122 MiB，可访问节点约百级；automation RPC p95 约 3～4 ms。该时间是本机 automation 请求往返，不是绘制帧时间或用户按键到屏幕延迟。CPU 只记录 `ps` 样本，不用它宣称正式 SLO。

首次双栏夹具缺少列高度约束，触发每栏最多 4,096 行构建，出现 57,386 个可访问节点和约 1.3～1.5 GB RSS。给列明确 FillHeight 后恢复虚拟化；结果保留在 [首次记录](research/macos-2026-11-03/mygo-stream-first-run.json)。另一轮仅后台启动、未充分观察实时刷新，记录在 [高度修正后记录](research/macos-2026-11-03/mygo-stream-height-only.json)；最终运行增加真实窗口激活和至少 40 次更新的检查。这两轮局部功能 PASS 不算最终压力验收。

**仍需做原生选择扩展。** `markdown.Options.ShareSelection` 配合原生 Selectable 容器支持可见跨消息选择，其他消息 streaming 时可保留；但选择端点被虚拟列表卸载后，固定 MyGo 的 `syncTextSelection` 会清除范围。[实测](research/macos-2026-11-03/mygo-selection.json)。如果要让选择跨越已卸载历史，需要稳定的 ItemID／文本 offset 范围及原生组件接口扩展；CopyData／SelectedTexts 只覆盖已挂载内容，单独换 clipboard callback 不能解决。不得靠常驻全部历史来掩盖这个边界，也不能宣称框架已完整满足该交互。

## macOS 服务、隐私和凭据

本机与 Air 的 ad-hoc `SMAppService.agent` 都能启动 bundle 内 Go worker 并注销，说明不必默认退回 root daemon 或 herdr-core。正式实现需把 notRegistered、enabled、requiresApproval、notFound 分开；API 成功或 registered 不等于用户始终允许运行。服务更新后重新注册，升级前停止接受新运行操作，确认空闲并保存一致备份；数据库已迁移而旧 binary 不兼容时应拒绝启动。[Apple Service Management](https://developer.apple.com/documentation/servicemanagement/smappservice)、[固定 SDK 头文件说明](research/macos-2026-11-03/remaining-sources/SMAppService.h.txt)。

Local Network 的诊断明确报告 `localNetworkDenied`。Apple 按 responsible code 和签名身份关联授权，后台 helper 应有正确 bundle 关联与用途说明；ad-hoc 身份不能替代正式身份连续性。SSH bridge 在受控网络通过，不会使此前直接 LAN 的 Python bridge 自动获得权限。不得重置隐私、放宽 host key 校验或改变系统路由来“修好”验收。[Apple TN3179](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy)。

早期 CLI 上下文中 MyGo Secrets 写入返回 `OSStatus -25308`，只读诊断报告默认 Keychain locked。用户后来解锁 login，原生 GUI 自有随机条目读写／更新／删除通过，而 CLI 仍返回该错误；后续同二进制节点身份恢复也通过。不能将 CLI 结果概括成所有上下文不可用，也不能将 GUI 结果推广为后台 helper 或签名升级通过。没有修改密码、读取已有凭据或降级明文。[写入结果](research/macos-2026-11-03/mygo-keychain.json)、[状态](research/macos-2026-11-03/keychain-status.json)。产品要呈现“凭据存储暂不可用／需解锁”，保留可重试状态；节点 master key、认证 token 不写普通日志或明文配置。建议单一网络 owner 负责读取自己的密钥，避免 app／worker 无意间依赖跨二进制 Keychain 共享；正式身份升级与锁屏后的行为需要正式包验收。

建议把 tsnet 的唯一 owner 放在随应用分发的 Go 后台组件，前端通过 IPC 取得状态和连接，入站／出站共用身份。这样可以保持 worker 与窗口生命周期分离。该进程归属仍是建议，不是新增产品决定；网络开关、彻底退出后的行为要在实施规格明确。通用 localhost TCP／UDP 暴露也不是已批准功能，首期应先落实经过独立授权的 Tahuna 入站通道。[入站专项](tailnet-inbound-research-2026-11-03.md)。

## 数据与资源限制

实验中的 20,000 条 2 KiB journal 记录按 50 条一批写入，FULL／fullfsync 下在两台设备完成；最终 checkpoint 后 WAL 为 0。批次 p50／p95 和内存值见各自记录。这只能指导初始批次，不是生产吞吐或断电持久性保证。

建议的初始预算：UI 每 16～33 ms 合并一次增量，worker 每 25 ms 或 50 条事件提交一批；审批、操作接受和队列变化立即提交。每连接限制未确认数据窗口，超时慢客户端断开后以 snapshot/cursor 补齐；不能让单个慢客户端堵住 app-server stdout。大图片、文件、MCP 输出与诊断以受限资源引用分页加载，不无限放大单条 JSON 帧或重建全部会话。具体字节额度、保留天数和清理策略属于实现期配置与负载验证。

数据库放私有本地应用目录。损坏时保留故障副本、停止接纳执行，进入显式恢复流程；不因 open/migrate 失败便删库重建。旧 cursor 被裁剪时要求重新加载一致 snapshot，不能偷偷跳过事件。备份覆盖 WAL 一致状态；副作用状态不明时先核对，不自动重试。

最终审计修正了迁移夹具：原先空 `select` 可能触发 Go 的死锁退出，原记录不能证明 SIGKILL。现已改为保持存活，并断言子进程的实际退出信号为 SIGKILL；本机与 Air 均重跑通过。原记录以 `sqlite-fault-*-first-run.json` 留存并标注失效范围。

## 后续验收范围

| 项目 | 当前状态 | 何时执行 |
| --- | --- | --- |
| 真实 Tailnet、Air tsnet SSH、入站授权、路径和恢复 | 双官方节点、strict SSH、应用授权拒绝、direct／DERP 和身份恢复通过；peer relay／多种 ACL 场景未覆盖 | 见补充实机验收；复杂网络在指定环境继续 |
| 真实中文候选窗与 VoiceOver | 中文候选窗两种流程用户确认通过；VoiceOver 本轮明确跳过 | 产品骨架继续回归输入；读屏按后续范围安排 |
| Air 休眠／唤醒、重启／登录恢复 | 用户明确暂不打断 Air | 方便中断设备使用的测试时段；夹具可重建，临时登录服务已撤除 |
| Developer ID、公证、Gatekeeper、后台／正式 Keychain 身份连续性 | GUI 子进程也确认无有效签名身份；GUI 自有凭据通过，后台／升级未验 | 有正式签名制品后；不向用户索取私钥或密码 |
| macOS 13～26 实机、磁盘物理故障、MDM／用户禁用后台项目／TCC 提示与文件授权 | 现有设备和制品未覆盖 | 在指定系统、管理策略及正式签名制品上做发布验收 |
| 真实 Codex 登录、账号可用模型和实际模型服务 | 本轮未使用账号或外部模型；协议／工具实测使用 loopback provider | 有用户授权的测试账号与服务环境后做集成验收；不将离线 catalog 当作账号授权 |
| 完整 adapter、虚拟列表选择扩展、长期资源上限和生产恢复流程 | 已定位技术与契约，尚未实施 | 纳入实施规格与产品测试，不把研究代码直接宣布为正式 worker |

Air 的临时服务、上传文件、测试仓库和电源测试登录项均已清理；未休眠、注销或重启 Air，没有更改系统 SSH、隐私、Keychain 或网络设置。证据与脚本哈希见 [来源清单](research/macos-2026-11-03/remaining-source-index.json)。

补充：本页早期表格的受控网络与已提交中文测试保持原实验范围；后来的真实网络／物理输入通过不改写旧失败记录。最新证据、节点清理和剩余验收以 [实机补充验收](live-acceptance-2026-11-03.md) 为准。
