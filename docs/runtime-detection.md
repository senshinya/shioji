# 设备端 Codex 发现与启动环境

安装检测由工作区所属设备的 worker 执行，不依据控制 Mac 的终端环境，也不按 npm／Homebrew 安装渠道判定支持。所选 CLI 必须提供当前 adapter 需要的 App Server 协议。

## 环境与优先级

worker 读取其用户的交互式登录 Shell 环境（`-ilc`），从而恢复 nvm、fnm、Volta、asdf 等工具在启动脚本中设置的 PATH。优先使用继承的绝对 Shell 路径；缺失时读取设备用户的登录 Shell，失败后使用 zsh。候选 Shell 失败可尝试 zsh／bash，整个探测共享三秒预算。bash 同时读取 `.bashrc`，环境使用分隔标记与 NUL 分隔，允许启动脚本输出噪声以及变量值包含换行。

环境按以下规则合并：

1. Shell PATH 在前、worker 原 PATH 在后，按顺序去重。
2. 其他变量保留 worker 已有值，Shell 仅补充缺失值。
3. Agent 显式环境配置最后覆盖；配置目录由 adapter 最后写入 `CODEX_HOME`；未显式配置目录时，保留 worker 原有值，原有值缺失才沿用用户 Shell 的配置。
4. Shell 捕获不导入其他执行上下文的 worker／线程／工具桥变量。

捕获结果仅在所属 worker 内存缓存，不写日志、客户端回执或磁盘。并发请求复用一次探测；成功缓存三十秒，失败缓存两秒，Shell／PATH／ZDOTDIR 或常见 rc 文件状态变化会使缓存失效。调用方取消无需等待共享探测完成；探测自身超时会结束进程组并回收输出管道。输出限制为 1 MiB，环境值和 stderr 不进入错误消息。

## 发现、身份与启动

GUI 的默认启动参数保留为裸命令 `codex`，让 worker 在合并环境后选择安装。显式配置的绝对路径继续优先。已知 Homebrew、npm-global 与 Codex.app 位置仅作为 PATH 未命中时的兜底，不枚举 nvm／fnm 的版本目录。

启动路径与真实文件身份分开：按原入口启动，保留版本管理器依赖的 `codex` 调用名；真实目标的路径、文件身份和元数据用于检测安装变化。npm 的 Node 解释器也参与身份计算，解释器切换或失效会触发重新核对。相对 PATH 命中的程序不被隐式执行。

版本检查、协议 schema 导出、App Server、受管执行器与辅助标题生成均使用同一份已准备环境；运行中的实例保留自己的环境快照，不修改 worker 的全局环境。受管启动器在 worker 接纳前仍使用最小环境，授权后的目标进程才获得完整环境；账号、模型服务和 Skill 配置读取也使用该实际目标环境，避免把启动引导器的最小环境误判为凭据或目录缺失。安装在启动期间变化时，保留既有拒绝接纳／重试处理。

错误区分未找到 Codex、npm 入口缺少 Node、启动器无法执行、版本或协议探测超时，以及真正的协议不兼容。填写 npm 入口的绝对路径不会自动取消其 Node 依赖，但配置的 PATH 现在同时用于检测和实际启动。

## 验证

`internal/harness/codex/launch_environment_test.go` 使用真实 Node、npm 风格 JavaScript 入口、真实安装的 Codex、独立 `CODEX_HOME` 和本地模型夹具验证：

- 受限 PATH 下的显式环境覆盖与登录 Shell 恢复。
- 原入口名称、真实文件／Node 身份、缺少 Node 的明确错误。
- 并发缓存、rc 更新、取消、超时和子进程回收。
- 多行环境、配置优先级与受管上下文隔离。
- App Server、受管执行器及真实辅助标题进程的完整启动链路。

`internal/transport/codex_discovery_test.go` 验证 GUI PATH 中的另一份 Codex 不会在 worker 应用配置之前被提前固定。

这些检查不安装或卸载用户的 Node／Codex，不替换或重启现有 App、worker 或 Tailnet 服务。另一台设备的现有 npm 安装仍需使用包含该修复的 worker 验证。

## 参考

- [Lody 登录 Shell 环境探测](https://github.com/LodyAI/Lody/blob/main/packages/shared/src/node/login-shell-env.ts)与[环境合并](https://github.com/LodyAI/Lody/blob/main/apps/cli/src/agent/setting.ts)。
- [T3 Code 的桌面环境恢复](https://github.com/pingdotgg/t3code/blob/main/apps/desktop/src/shell/DesktopShellEnvironment.ts)与[保留启动器入口的本地安装解析](https://github.com/pingdotgg/t3code/blob/main/apps/server/src/provider/CodexInstallation.ts)。
