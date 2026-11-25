# Shioji 发布

当前正式版为 `v0.9.1`，最近的 macOS Beta 为 `v0.8.1`。`VERSION` 保存不带 `v` 的数字构建版本，macOS App 和 Runtime bundle 的版本均由构建脚本写入。iOS 与 iPadOS 的版本号和构建号记录在 `cmd/shioji-mobile/mygo.json`。

## 变更记录与发布说明

完整变更见 [CHANGELOG.md](../CHANGELOG.md)。每次发布前比较上一正式标签与待发布代码的实际差异，核对面向用户的行为、兼容性和升级要求；不能只根据 commit message 生成说明。

使用 `Added`、`Changed`、`Deprecated`、`Removed`、`Fixed`、`Security` 分类，只保留有内容的分类；版本标题包含数字版本、UTC 发布日期和源码／比较链接。说明用户影响，排除本地产物、内部重构和纯测试调整。GitHub Release 正文复用对应版本条目，并补充该版安装与更新方式。修改历史说明保留原发布日期，不移动标签或替换已发布二进制。

v0.9.1 在所属设备执行服务升级时自动导入旧 Agent 登录配置，每个原 Agent ID 对应一条「系统」Profile。macOS 设备页各标签的主操作共用右侧位置。执行服务使用协议 2.62，数据库沿用 schema 17。GitHub Release 分发 Apple Silicon macOS 安装包。

v0.9.0 新增 DeepSeek Responses 接入和设备级多账号 Profile。DeepSeek 需要 API Key 及执行设备上的 Codex app-server。执行服务使用协议 2.62，数据库沿用 schema 17。GitHub Release 分发 Apple Silicon macOS 安装包。

v0.8.4 改善 macOS、iPhone 和 iPad 的对话动画与滚动，修复 macOS 会话分叉定位和外观主题卡片边框，并修复克隆后无法确认 Git 分支及 Codex 账号路由短暂故障阻止启动的问题。后两项修复需要更新所属设备的执行服务。执行服务沿用协议 2.61 和 schema 17。

v0.8.3 改善 macOS、iPhone 和 iPad 的对话、表单与导航，修复 Antigravity 登录状态、用量、介入回执和停止状态，并扩展 Skill 查找来源。运行时修复需要更新所属设备的执行服务。本版使用协议 2.61 和 schema 17。

v0.8.2 正式支持 Antigravity CLI 和会话内切换 YOLO，macOS、iPhone 和 iPad 在工具组中直接显示思考摘要。Antigravity 所属设备需安装并授权 `agy`，并更新执行服务。执行服务使用协议 2.60 和 schema 17。

v0.8.1 Beta 增加 Antigravity 官方单色图标。Mac App 与 Antigravity 所属设备的执行服务均需更新，执行服务沿用协议 2.60 和 schema 17。

v0.8.0 Beta 增加 Antigravity CLI 支持。请在会话所属设备安装并授权 `agy`，同时更新该设备的执行服务。GitHub 提供 Apple Silicon macOS 安装包，执行服务使用协议 2.60 和 schema 17。

v0.7.13 修复 Codex 在 MCP 服务启动失败时导致会话提前结束的问题，并改善 iOS/iPadOS 的会话标题间距和工具结果折叠时的阅读位置。执行修复需要更新设备的执行服务，沿用协议 2.58 和 schema 17。

v0.7.12 调整 macOS 对话的工具组标题，并在设备详情里显示 Tailnet 对端延迟和连接路径。更新 Mac App 即可获得这两项界面改进。执行服务沿用协议 2.58 和 schema 17。

v0.7.11 改善 macOS 与 iOS 的发送及介入当前轮动画，并修复本机执行服务重新连接和重启后的轮次恢复。执行服务沿用协议 2.58 和 schema 17。设备需更新执行服务才能获得重启恢复修复。

v0.7.10 增加 Codex 用量限制提示，改善发送后和长会话的底部跟随，同时纳入原生 iPhone 和 iPad 客户端。GitHub 发布 macOS 安装包。执行服务使用协议 2.58 与 schema 17。移动端的 Agent 设置和部分 Git 变更确认需要更新所属设备的执行服务。

v0.7.9 增加图片预览的旋转与独立缩放，简化 Mac 会话的发送失败草稿，并修复跨客户端更新执行服务后仍显示旧拒绝提示的问题。执行服务沿用协议 2.55 和 schema 17。更新 App 即可获得本版变更。该版发布时重新构建并签名了 worker，因此已安装 v0.7.8 worker 的设备会看到可用更新。两版的 worker 编译输入相同。

v0.7.8 改善多会话并行时的发送响应，支持回答 Codex 异步提问，并加快长会话加载。本机及远程设备需更新所属执行服务以获得设备端改动。新版执行服务使用协议 2.55，更新时备份并将数据库迁移到 schema 17。回退到只支持 schema 16 的执行服务需要使用迁移前的备份。

v0.7.7 加快长会话历史分页，改进全局搜索、设置、Goal 展开和更改侧栏，并修复 Agent 版本探测短暂超时导致模型目录不可用的问题。执行服务沿用协议 2.53 和 schema 16。本机及远程设备均需更新所属执行服务，才能获得设备端的分页优化与探测修复。

v0.7.6 改善 Skill 菜单筛选、Git 分支核对和长会话的设备响应，修复 Agent 安装探测阻塞状态读取、已有会话沿用过时配置及独立页面顶栏间距。执行服务沿用协议 2.53 和 schema 16。Skill 菜单与会话配置修复通过更新 App 获得；本机及远程设备均需更新所属执行服务，才能获得设备端的读取与核对优化。

v0.7.5 改进工具组的状态呈现和展开／折叠动画，清除 Goal 时平滑收起目标栏，并统一主窗口各页面的顶栏上边距。执行服务沿用协议 2.53 和 schema 16。这些界面改动通过更新 App 获得。

v0.7.4 调整 Goal 与待发送队列的布局，改进对话内的请求回执，并修复 Codex 导入接续及执行服务升级。执行服务沿用协议 2.53 和 schema 16。远程设备需要更新执行服务，才能获得设备端的导入与存储扫描修复。

v0.7.3 修复本机与远程执行服务的断线续接、提交核对、停止、队列和升级。新版连接恢复和目录分页使用协议 2.53，数据格式沿用 schema 16。远程设备需更新执行服务以获得完整修复。

v0.7.2 在对话中标记 Goal 自动续跑，并将每次带标记的续跑计为一个历史轮次。远程会话的分页变化需要更新所属设备的执行服务。执行服务沿用协议 2.51 和 schema 16。

v0.7.1 将 `/goal` 加入原生斜杠菜单。执行服务使用协议 2.51 和 schema 16。

v0.7.0 新增原生 Codex Goal 模式和界面缩放快捷键，并修复长工具结果折叠后的滚动范围。Goal 需要所属设备的执行服务协议 2.51，以及支持且启用了 Goals 的 Codex 运行环境。执行服务继续使用 schema 16。

v0.6.3 改善定时任务布局、队列编辑和会话归档反馈，并支持远程执行服务上传断线续传。执行服务沿用协议 2.50 与 schema 16。

v0.6.2 改善定时任务编辑和消息入队反馈，修复归档删除回执、Git 分支监控及 Codex 登录环境探测。执行服务沿用协议 2.50 与 schema 16。

v0.6.1 支持按设备设置归档保留期，集中显示定时任务所需的执行服务更新，并修复本机设备图的多余连接端点。执行服务支持经设备页确认后从 schema 15 备份并迁移到 schema 16。

v0.6.0 增加设备端定时任务，调整会话 Hooks 入口及审核界面，升级原生界面运行库，并改善停止回复和归档删除。定时任务需要所属设备的 worker 协议 2.50。更新执行服务时数据格式迁移到 schema 16。

v0.5.0 增加 Hook 批量信任与审查侧栏的键盘重排，统一原生配置字段、菜单、浮层与会话处理卡片，并修复全局搜索、首次 SSH 缩放、归档导航及历史加载。批量信任需要所属设备的 worker 协议 2.49，旧 worker 仍可逐项审批。本版沿用 schema 15。

v0.4.0 引入 Hooks 管理、远程文件上传确认与路径引用、空闲会话重新加载和设备菜单中的 worker 更新。执行服务采用固定 supervisor 与自动环境回收，移除旧强制处理页面；执行闲置20分钟、30秒扫描，等待原生后台任务最多保护24小时。归档释放原环境，自建worktree安全备份后回收并可恢复。新能力需worker协议2.48／schema15；旧存量缺失停止接纳字段仍按明确的手动迁移边界处理，不自动重放未知输入，不自动降级存储格式。

v0.3.5 增加原生 Mermaid 图表、Markdown 图片缩略图、会话窗口缩放、执行环境强制恢复，以及归档批量删除与带警告的强制删除；同时修复运行配置继承、文件链接定位、非 Git 目录更改视图及会话控件误触。归档运行环境回收与强制删除需要所属设备的新版 worker（协议 minor 44）；旧版或卡死 worker 的恢复可走独立恢复程序，仍需明确确认关闭范围和风险。恢复不自动续答或发送队列，项目文件不会回滚或删除，独立 Tailnet 节点不随执行服务恢复而关闭。

v0.3.2 修复长会话导入后无法接续的问题，正式导入完整历史并按需分页；已有部分副本可在原会话中“补全历史”，保留名称、配置和草稿。完整导入及补全需要更新所属设备的 worker（协议 minor 42）。同时增加原生 Markdown 文件预览、按设备创建工作区和会话读取状态，改善历史阅读、工具折叠与输入框动效。空白新会话默认沿用所属目录的当前 Git 分支；手动选择及未发送草稿继续保留。文件及 Markdown 关联资源仍由所属设备的 worker 按会话权限读取。

v0.3.0 增加归档管理与到期清理、执行服务阻塞恢复及子 Agent 消息分页，并同步跨客户端的完成提醒状态。归档保留期默认 30 天，可在“外观 → 对话”设置为永不；到期只删除 Shioji 记录与自有资源，项目文件保留。新功能需配套更新所属设备的 worker；归档和维护保护使用 schema 12／13，不能自动降级到忽略这些保护的旧版。

v0.1.0 及之后版本可通过 App 内更新；v0.0.1 没有自动更新入口，需要手动安装新版。设备负载、保持唤醒、端口转发与网页链接下载需配套更新所属设备的 worker；旧 worker 不提供这些能力。GUI 更新不自动升级远程 worker，也不停止独立执行服务或 Tailnet。端口转发当前仅传递 TCP 字节，不改写 HTTPS 证书、Host、Origin 或页面内绝对 URL；真实远程 SSH 与 Tailnet 对端的端到端验收仍待完成。

## 自动发布

GitHub 仓库：`senshinya/shioji`，本地 remote 为 `upstream`。`origin` 保持原地址。

- CI 在 Apple Silicon macOS 上测试 Swift 权限桥、MyGo 原生更新器和发布脚本，校验设计冻结与第三方许可，扫描源码及完整 Git 历史中的凭据，并构建开发包。
- `.github/workflows/release.yml` 在 GitHub Release **published** 时触发。只推 tag 不触发生产发布。手动触发会构建所选 commit 的版本，上传到该次 Actions 的临时构建附件；不创建或修改 GitHub Release。
- CI 按提交保存 Go 编译／测试缓存，并复用相同源码和 Xcode 版本的 Swift 权限桥缓存。正式 Release 要求发布提交在 `main` 上的最新 CI 成功，并重新进行源码校验、构建、签名、公证和打包。全量 Go 测试在发布前本机运行。
- Tag 必须与 `VERSION` 对应。生产构建强制要求 Developer ID Application 签名、hardened runtime、Apple 接受公证以及 stapled tickets；没有降级到 ad-hoc / 未公证包的路径。
- 先公证并 staple App，再生成全量自动更新归档，然后生成、公证并 staple DMG。正式版本将 DMG、校验文件与 `Shioji-<版本>-darwin-arm64-<SHA256>.tar.gz` 上传到原 Release，全部成功后最后上传 `update-darwin-arm64.json`。归档按内容摘要命名，同一 Release 重跑也保留旧清单对应的归档。不创建额外 Release。预发行仅上传 DMG 与校验文件，不向正式客户端发布更新清单。
- 正式打包检查 worker 的传递源码依赖与模块文件。输入沿用时，从 GitHub Release 复用同一 Go 版本构建的原始已签名 worker，以保留 BuildID。打包会核对归档摘要、Developer ID 和构建元数据，再将同一份 worker 放入两个 bundle，由 `cmp` 核验。

## 首次配置 GitHub：只需在这台 Mac 的终端执行

工作流使用 GitHub 的 `release` 环境；已创建该环境。凭据输入不经过聊天、源码或提交记录。

最简单的入口：在 Finder 中双击 `packaging/Configure Release.command`，它会依次完成下方两步；在系统窗口确认，在终端隐藏输入专用密码即可。

### 1. 允许导出这一张开发者签名证书

```sh
python3 scripts/configure-github-release.py certificate
```

脚本自动选取本机唯一有效的 Developer ID Application 身份，只导出这一张身份。导出用随机密码加密，直接通过标准输入写入 GitHub Secrets，最后删除临时副本。原证书、私钥和钥匙串权限不变。macOS 可能要求本机确认；若返回 `-25308`，请在这台 Mac 本身的终端执行并允许系统提示，而非在无人值守或 SSH 进程中反复尝试。多张证书时用 `--identity` 指定公开 SHA-1 指纹。

配置项：`BUILD_CERTIFICATE_BASE64`、`P12_PASSWORD` Secrets，以及公开的 `APPLE_TEAM_ID` 环境 variable。

### 2. 给 Apple 公证创建一个专用密码

用所属开发者账户打开 [Apple 账户](https://account.apple.com/account/manage)，在“登录和安全”中创建 App 专用密码，可命名为 Shioji GitHub。它仅用于公证，**不是 Apple 账户的普通登录密码**。随后执行：

```sh
python3 scripts/configure-github-release.py notarization
```

按提示输入 Apple ID 邮箱和刚生成的专用密码；密码不回显。脚本写入 `APPLE_ID` 与 `APPLE_APP_SPECIFIC_PASSWORD` Secrets。现有本机 notarytool profile 继续留在钥匙串，不能直接给 GitHub runner 使用。工作流在构建前向 Apple 验证新配置。

全部配置完成、CI 通过后，再在 GitHub 的 Releases 页面发布与 `VERSION` 匹配的 Release。不要提前发布一个缺少签名凭据的正式版本。

### 3. 配置 App 自动更新签名（新增，一次即可）

已有证书和公证配置无需重复操作。在这台 Mac 的终端执行：

```sh
python3 scripts/configure-github-release.py updates
```

脚本在 `~/Library/Application Support/Shioji/Release Signing/` 生成独立的 Ed25519 密钥对，私钥权限为 0600；私钥通过标准输入写入 GitHub `release` 环境的 `SHIOJI_UPDATE_PRIVATE_KEY` Secret，公钥写入 `SHIOJI_UPDATE_PUBLIC_KEY` variable。不会打印私钥或把它写入源码。重复执行复用原密钥，并核对 GitHub 已有的公钥；换电脑或丢失本地密钥时，会要求恢复原备份，不生成新身份覆盖远端。请安全备份该目录。**分发首个支持更新的 App 后不得重新生成密钥**，否则已安装客户端无法接受新签名。

首次需要手动安装带更新能力的正式版。之后递增 `VERSION`、使 Release tag 与之匹配，在 GitHub 发布新正式 Release，等待 Release workflow 成功上传清单，客户端便能检查到更新。只推 tag、草稿、失败构建和 Actions 临时附件不会发布更新。更新说明使用该 Release 的正文。代码需要先进入 GitHub 发布所选 tag 对应的提交；仅推送另一个 Git remote 不会更新 GitHub 工作流。

App 菜单和设置「关于」提供检查更新，自动检查默认开启、通常每日一次；下载后由用户选择稍后或立即重启。开发包无更新源；不可写的安装位置无法自动替换。更新包在 HTTPS 下载和 Ed25519 验证后，还核对 Developer ID team、Bundle ID、版本、Apple Silicon 架构与最低 macOS 版本。App 退出仍可被草稿保存失败阻止。

更新 GUI 不停止独立 worker 或 Tailnet 服务；本机 worker 继续按 `docs/local-worker-updates.md` 的安全空闲准入升级，远程 worker 仍需明确操作。首版不发布增量包，也不自动降级。

## 本机构建

发布前在 Apple Silicon Mac 上运行 Shioji 的 Go 测试。Codex CLI 需在 `PATH` 中，供真实 app-server 集成用例调用。

```sh
python3 scripts/bundle-diagram.py
SHIOJI_DIAGRAM_USVG="$PWD/build/diagram-runtime/usvg" go test -timeout 20m ./cmd/... ./internal/...
```

```sh
bash packaging/build-dev.sh
# 指定本机已有 Developer ID identity 和 notarytool profile 后：
# 另需 APPLE_TEAM_ID、SHIOJI_UPDATE_PUBLIC_KEY 与 SHIOJI_UPDATE_PRIVATE_KEY；
# 私钥从安全文件/CI Secret 注入环境，不要写在命令参数或 shell 历史中。
CODE_SIGN_IDENTITY='<证书指纹>' NOTARY_PROFILE='<本机公证配置名称>' RELEASE_TAG="v$(cat VERSION)" bash packaging/build-release.sh
```

产物位于 `build/release/`。构建不会启动或替换用户正在运行的 GUI、worker 或 Tailnet 节点。

## 图标

采用用户选定的 A「潮路」。`packaging/assets/Shioji-source.png` 是不改动的生成原图；`Shioji.icns` 是应用资源。原生转换脚本负责 macOS 轮廓与各标准分辨率：

```sh
swift packaging/make-app-icon.swift packaging/assets
iconutil -c icns packaging/assets/Shioji.iconset -o packaging/assets/Shioji.icns
```

主界面保持既定无 Logo 设计；图标用于应用包、Finder 和 Dock。

## 凭据检查

```sh
bash scripts/scan-secrets.sh
```

同时检查完整 Git 历史及当前受版本控制/未忽略的源码快照。2026-11-13 初始历史扫描发现一个旧局域网预览访问 key；已从当前文档移除并撤销，旧 key 的实际 HTTP 请求返回 403。`.gitleaksignore` 仅允许该已撤销凭据在单个旧提交中的精确 fingerprint，不放行新凭据、当前文档或其他文件。未发现有效 API key、token 或私钥。构建、预览、研究状态和签名材料仍被 `.gitignore` 排除。

历史文档文件名、冻结稿文字和真实签名/测试身份按仓库约束保留；生产 `cmd/`、`internal/` 和打包/发布脚本不使用旧产品名称。

参考：[GitHub 的 macOS 证书导入流程](https://docs.github.com/en/actions/how-tos/deploy/deploy-to-third-party-platforms/sign-xcode-applications)、[Release 触发事件](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#release)、[Apple 公证要求](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)。
