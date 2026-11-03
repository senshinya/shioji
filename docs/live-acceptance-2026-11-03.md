# 实机补充验收

2026-11-03，用户恢复此前延期的验证。本轮使用两台 Apple Silicon Mac（macOS 27.2）、MyGo v0.7.4、官方 tsnet v1.104.0 和一次性研究程序。没有开始生产实现，`design/` 保持冻结。此前失败与局部结果保留，以下结果更新对应的延期状态。

## 实测结果

| 项目 | 结果 | 证据与边界 |
| --- | --- | --- |
| 真实中文输入法 | 通过 | 用户操作候选窗，确认空格选词后 Enter 发送；候选窗打开时 Enter 不误发，再按一次才发送。[记录](research/live-acceptance-2026-11-03/ime.json)。未记录具体输入法名称，不代表所有输入法已验收 |
| 原生应用 Keychain | 通过 | 自有随机 32 字节测试凭据的写入、读取、更新、删除、删除后不存在均通过。[原生记录](research/live-acceptance-2026-11-03/first-ephemeral-node.json)。不读取用户已有凭据 |
| 真实 Tailnet 登录 | 通过 | 用户在浏览器授权测试节点。控制端不安装／调用系统 Tailscale；两端官方节点均 `TUN:false` |
| 控制端出站 SSH | 通过 | 系统 OpenSSH → 私有 IPC → 官方 tsnet → Air 官方 tsnet → Air 已有 sshd；严格校验用户独立确认的主机指纹。[操作记录](research/live-acceptance-2026-11-03/actions.jsonl) |
| Air 入站到本机 | 通过 | Air 官方节点主动 Dial 本机官方 listener，8 字节二进制载荷及半关闭后收到完整 23 字节响应；不是 Air 普通系统 socket 绕行 |
| 应用层入站拒绝 | 通过 | 同一 tailnet 的研究节点未获显式应用授权时回传失败、收到 0 字节；绑定当前 netmap 中 StableNodeID/IP 后才通过。[拒绝记录](research/live-acceptance-2026-11-03/before-peer-authorization.json)。未更改用户 Tailnet ACL，不冒充 ACL 拒绝测试 |
| 真实路径和 RTT | 通过 direct／DERP | 到 Air 原有 Herdr 嵌入节点观测到 direct 约 4.6～8.3 ms 和 DERP 约 7.8 ms；两官方节点通过研究进程专用调试配置禁用 UDP，实测 DERP 约 7.25 ms，区域 `901 / home / Hangzhou Home`。只影响测试进程，没有改系统路由／防火墙。peer relay 未出现 |
| SDK 节点重启 | 通过 | 同进程关闭并重建 tsnet，StableNodeID 和地址保留；新 SSH／回传成功，旧 epoch 被拒绝 |
| 应用完整退出再打开 | 通过 | 同一二进制／签名读取 Keychain master 和加密状态，无需再次浏览器授权，StableNodeID 与地址保留，新 SSH／回传成功，旧 epoch 被拒绝。[恢复记录](research/live-acceptance-2026-11-03/process-restart.json) |
| 正式签名环境 | 未具备 | GUI 应用子进程检查也为 0 个有效 code-signing identity；不能用 CLI 上下文差异解释成已有 Developer ID。[操作记录](research/live-acceptance-2026-11-03/actions.jsonl) |

## 影响方案的结论

**持久身份与临时节点不同。** 首个夹具使用 `Ephemeral:true`，正常退出触发 Logout，不能据此检验持久身份恢复。随后使用普通节点、自己的 Keychain master 和 AES-GCM StateStore，才完成同二进制完整退出恢复。固定上游在 ephemeral Shutdown 路径明确调用 Logout，见 [ipnlocal 源码](https://github.com/tailscale/tailscale/blob/c8125a977e8714ac9f0d1a1fb43c714490d38bd3/ipn/ipnlocal/local.go#L1344)。正式应用应默认持久节点，将退出、停用、注销明确区分。

**Keychain 结果取决于运行身份与上下文。** 用户解锁 login 后，当前命令行检查仍返回 `-25308`，原生 GUI 随机条目读写却成功。另一次 ad-hoc 重编译访问旧夹具条目等待授权；清理自有旧条目后以固定版本重测通过。不能把 GUI 成功推广为后台 worker、锁屏、升级签名连续性或任意 helper 的共享访问成功。建议唯一网络 owner 持有密钥；其后台身份、正式签名升级和锁屏行为仍需正式制品验收。研究状态文件权限为 0600、目录 0700，认证 URL 只在内存与原生登录按钮中使用，报告不含 URL 或密钥。加密状态损坏拒绝路径本轮未做真实文件篡改注入。

**连接必须经过嵌入网络。** 用户提供的 Air `100.88.0.105` 属于 Herdr 内嵌节点；Air 无系统 Tailnet 接口，普通 Python socket 的源地址是 LAN 地址，不能作为嵌入 Tailnet 入站验证。因此额外部署短期官方 tsnet 节点，并由用户在本机浏览器授权。最终双向链路两端均为官方 tsnet，不依赖 Herdr 或系统 Tailscale 运行时；原有 Herdr 节点仅用于早期互通参考。

**Tailnet 身份不等于 worker 执行授权。** listener 在 WhoIs 后核对当前 peer 身份和显式允许记录。研究中的 echo 与仅转发已有 SSH 的 listener，不是已批准的生产协议、配对机制或通用端口暴露。epoch 只隔离过期连接，不代替 IPC 权限、设备身份或执行授权；生产入站认证仍需实施规格确定。

**路径是一次观测。** RTT 来自 peer probe，不是本机到 DERP 的延迟；显示 DERP 依据实际报告的 region，direct 依据实际 endpoint。没有将 home relay 或空字段推定成当前路径。真正 peer-relay、多种 ACL 拒绝、复杂 NAT／跨地域、长时运行仍未覆盖。

## 用户选择与清理

用户本轮明确跳过 VoiceOver；Air 休眠／唤醒和重启／登录仍跳过。已准备的工作区／队列和真实 Codex 待请求夹具均撤除，没有休眠、注销或重启 Air。[生命周期清理](research/live-acceptance-2026-11-03/lifecycle-cleanup.json)、[协议夹具清理](research/live-acceptance-2026-11-03/power-protocol-cleanup.json)。

Air 短期官方节点服务已停止、临时目录已删除；控制端节点已 Logout，自有 Keychain master 与加密状态已删除，进程、IPC 和私有目录均已移除。[最终本机清理](research/live-acceptance-2026-11-03/final-local-cleanup.json)、[Air 清理](research/live-acceptance-2026-11-03/actions.jsonl)。未操作 Air 原有 Herdr 节点。Logout 不保证 Tailnet 管理页的普通设备记录被删除：若仍出现 `tahuna-acceptance-tn-j6s9_xap` 测试设备，可在管理页移除该条；没有将后台记录删除标记为已完成。

本轮研究可收尾。剩余发布验收包括正式签名／公证／隐私与后台 Keychain、旧版 macOS、设备电源恢复、peer relay 与真实模型服务。MyGo 虚拟化历史选择扩展、完整 adapter／worker 和生产授权协议属于后续实施，不能用本轮探针代替。

研究源代码、版本与证据哈希见 [清单](research/live-acceptance-2026-11-03/source-index.json)。
