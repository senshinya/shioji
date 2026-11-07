# 调研归档与仓库范围

Shioji 的 Git 基线保留实施规格、调研结论、当前设计说明、菜单尺寸规范、固定协议 schema 与冻结校验脚本。`sources.lock.json` 和 `verification.json` 保留来源版本与首轮验证摘要。

2026-11-07：按用户要求清理了本地验收页、预览程序、截图、录屏、重复导出与隔离验收状态。历史报告中的媒体和验收页链接不再提供资源；下述范围说明保留为原归档规则。冻结的仓库设计源文件和原始清单保持不变，清理后只对仓库可见设计子集运行冻结校验；完整本地媒体归档已不存在。

以下内容继续留在原位置，本地可读，Git 忽略：

- `design/reference/` 的 Lody、T3 Code 等产品截图和调研素材。
- `design/` 的 HTML/JS 原型及样式、评审页，以及全部截图（含 current-v37）、录屏与扫描结果；仅 spec.html、menus.css 与 protocol/ 进入 Git。
- `scripts/` 的 CJS 浏览器检查、原型生成、预览和发布工具。
- `docs/research/` 中各轮原始探针结果、机器环境记录和动态公证状态。
- `scripts/research/` 中的一次性实验与本机签名／部署脚本。
- `.ryugi/` 本地工具状态和 `publish/` 重复导出。

历史报告中指向这些文件的链接用于核对本地归档，新克隆不会附带它们。它们不是生产应用的依赖。不要用 `git add -f` 批量把归档加入源码仓库；需要晋升为可维护测试的夹具，应先脱敏、裁剪并放入正式 tests/fixtures，而不是把整轮实验复制进产品。

仓库设计校验：

```sh
python3 scripts/verify-design-freeze.py --repository
```

完整本地归档校验：

```sh
python3 scripts/verify-design-freeze.py
```

原冻结清单保持不变。Git 排除归档不会删除本地文件，也不等于为这些本地档案建立了远端备份。
