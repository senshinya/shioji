# 正式签名补充验证

2026-11-03，Apple Silicon macOS 27.2；一次性研究包，不是 Tahuna 产品实现。主界面使用 MyGo，后台为自有 Go worker，SMAppService／Network.framework 使用小型 Swift 平台诊断组件。冻结设计未修改。

## 已通过的验证

| 项目 | 实际结果 |
| --- | --- |
| Developer ID | 用户已安装 Application 身份，Team `XB3WDBXWG3`；与本机私钥配对。缺少的 Apple 官方 G2 中间证书已导入 login 钥匙串。未导出私钥、改证书信任或私钥访问控制 |
| 完整包签名 | v1/v2/v3 主应用及四个内部 executable 都有 Developer ID、Hardened Runtime、安全时间戳，strict 验证通过；v1/v2 四组件 designated requirements 相同 |
| 签名失败定位 | Python 和直接 shell 均曾返回 `errSecInternalComponent`；用户终端与自动化诊断均报告 login `unlocked:false`。用户在自己终端解锁后，两种调用均成功。不能将 Python 调用方式写成已确认根因 |
| SMAppService | 正式签名 bundle 注册 Aqua Go worker 成功、状态 enabled；有序 unregister 后服务与 socket 消失 |
| 后台 Keychain | v1/v2/v3 worker 均完成自有随机凭据 set/get、update、delete、not-found；未读取已有用户凭据 |
| 界面与 worker 分离 | 关闭 v1 界面后，同一 worker PID、节点和 Keychain 操作继续正常；重新打开界面不创建替代 worker |
| 签名升级 | 有序停止 v1、保留加密状态、同路径替换 v2 并重新注册；新 worker PID 读取旧 master／AES-GCM 状态成功，节点 ID 与 IPv4/IPv6 保留，无需浏览器重新授权。v3 继续复用同一身份 |
| 本地网络 | 用户允许研究应用后，设置中确认开关 on；v1 原 worker 仍被拒绝。v3 分离主程序／worker UUID 并重启后，后台 Go TCP22 收到 Air SSH banner，GUI 子进程 Network.framework 报告 satisfied／ready |

证据：[签名环境](research/signed-acceptance-2026-11-03/signing-environment.json)、[完整签名](research/signed-acceptance-2026-11-03/bundle-signatures.json)、[v3 签名](research/signed-acceptance-2026-11-03/v3-signatures.json)、[稳定身份要求](research/signed-acceptance-2026-11-03/upgrade-requirements.json)、[v1→v2](research/signed-acceptance-2026-11-03/upgrade-v1-v2.json)、[操作记录](research/signed-acceptance-2026-11-03/actions.jsonl)。

## 实现约束与验证边界

原始夹具复制同一个 Go executable 作为主程序和 worker，导致不同程序共享 Mach-O UUID。Apple TN3179 明确提醒缺少或重复 UUID 可能干扰本地网络隐私身份跟踪。v3 使用分别构建的主程序／worker，各有 UUID，保持同一 Team／identifier，实际连通通过。这里验证的是“用户允许＋UUID 分离＋重启”这一组合；没有做回退 A/B，不把 UUID 宣称为唯一因果。正式构建应分别生成各个独立 executable 并检查 UUID。[Apple TN3179](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy)。

第一次网络失败仍保留在记录中：Go 返回 no route to host，Network.framework 报告 localNetworkDenied。不能据此判定 Air 离线；也不能只看设置开关或 SMAppService 注册成功就宣称连接可用。没有重置隐私、修改路由、运行 root daemon 或关闭系统保护。

正式签名并不自动批准 Keychain、后台服务或网络权限。当前成功发生于用户已登录、login 已解锁的环境；锁屏、休眠、重启／登录、用户关闭后台项目、managed policy、证书更换及正式产品升级仍需专项验收。没有将普通 bundle 更新等同于活动 Codex turn 或数据库迁移恢复。

当前探针没有完成生产 worker、配对授权协议或 Codex adapter；UI 含研究诊断按钮与异步文本，不代表产品界面验收。

## 清理与公证

临时节点已 Logout，自有 master 删除后 not-found 已确认；服务注销、进程／socket、加密状态与私有运行目录均已清理。[清理记录](research/signed-acceptance-2026-11-03/cleanup.json)。Tailnet 管理页可能保留已注销的 `tahuna-signed-4e696a62` 设备行，后台记录删除未标记为完成；没有操作用户原有节点。

签名应用暂留 `~/Applications/Tahuna Signed Research.app` 用于后续公证，未注册后台服务。约 38 MB ZIP 只含签名应用，不含运行目录、凭据或登录 URL。[包摘要](research/signed-acceptance-2026-11-03/notary-prepared.json)。公证前 Gatekeeper 按预期拒绝：`Unnotarized Developer ID`。

用户已在本人终端配置 Keychain 公证 profile 并明确确认上传。Apple 已接收提交 `7bc6f43d-b613-4bc1-82a6-b5eb5b0ede88`，2026-11-03 22:50 时仍为 In Progress。尚未将附票据或 Air Gatekeeper 标记为通过。

已启动有界后台等待任务：只等待这一次提交，不重复上传；Accepted 后执行已准备的附票据／strict signature／本机与 Air Gatekeeper 检查，Air 的自有临时目录随后清理。遇到认证／网络／审核失败或等待上限会保存 needs-attention／still-processing 状态。最新状态以 [后台状态](research/signed-acceptance-2026-11-03/notary-watcher.json) 为准；审核通过后的证据写入 `notarized-gatekeeper.json`。后台任务不启动 Air 的应用 UI 或注册 worker，因此 Air 的结果仅是带 quarantine 的 Gatekeeper CLI 验证，不冒充物理首次打开。

公证凭据保留在用户自己的 Keychain，不进入源码、ZIP 或报告。VoiceOver 与电源操作继续跳过。

研究源和证据索引：[清单](research/signed-acceptance-2026-11-03/source-index.json)。
