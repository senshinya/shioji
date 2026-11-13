# Shioji 发布

当前版本：`v0.0.1`。`VERSION` 保存不带 `v` 的数字版本；两个 App bundle 的版本均由构建脚本写入。`dev.shioji.app`、`dev.shioji.runtime` 与现有服务身份保持不变。

## 自动发布

GitHub 仓库：`senshinya/shioji`，本地 remote 为 `upstream`。`origin` 保持原地址。

- CI 在 push / pull request 上运行 Apple Silicon macOS 测试、设计冻结校验、第三方许可校验、源码及完整 Git 历史凭据扫描，并构建开发包。
- `.github/workflows/release.yml` 在 GitHub Release **published** 时触发。只推 tag 不触发生产发布。手动重跑需要选择已存在的 release tag。
- Tag 必须与 `VERSION` 对应。生产构建强制要求 Developer ID Application 签名、hardened runtime、Apple 接受公证以及 stapled tickets；没有降级到 ad-hoc / 未公证包的路径。
- 先公证 App，再生成、公证并 staple DMG。仅将 `Shioji-0.0.1-macos-arm64.dmg` 与 `.dmg.sha256` 上传到原 Release。不创建额外 Release。
- 两处 worker 使用同一份已签名二进制，打包后 `cmp` 核验，避免安全时间戳引起相同版本的 BuildID 不同。

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

## 本机构建

```sh
bash packaging/build-dev.sh
# 指定本机已有 Developer ID identity 和 notarytool profile 后：
CODE_SIGN_IDENTITY='<证书指纹>' NOTARY_PROFILE='<本机公证配置名称>' RELEASE_TAG=v0.0.1 bash packaging/build-release.sh
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
