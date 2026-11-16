# Mermaid 原生渲染接入调研

日期：2026-11-16。范围：为 Shioji 的原生 Markdown 增加 Mermaid 图表渲染，优先采用已有用户和维护团队的库。本轮只做隔离探针；没有修改产品代码、主模块依赖、已安装 App 或 worker。

## 结论

建议优先采用 **Pele 1.13.0 + macOS JavaScriptCore**，以 `usvg` 将输出归一化为全路径 SVG，再交给 MyGo 的原生 SVG 组件绘制。这个组合的解析、字体测量回调、矢量转换和 MyGo 绘制已分别跑通；仍需产品接入与签名运行验收。

`beautiful-mermaid` 可以在无 DOM 的 JavaScriptCore 中执行，但本轮样例的渲染速度、错误处理与显示接入成本均不如 Pele。之前推荐的纯 Go 库适合技术探针，采用度不足以支持将其直接选为生产依赖。

Pele 是较新的项目，社区关注度不能等同于多年生产验证。它使用自己的布局，不保证与官方 Mermaid 像素一致。以上为工程建议，尚未成为已确认的生产支持或新增产品要求。

## 版本与采用度

| 方案 | 已核对版本或来源 | 采用度与项目定位 | 主要取舍 |
| --- | --- | --- | --- |
| 官方 Mermaid | 官方仓库与 mermaid-cli | 查询时约 9.1 万 Star；npm 2026-11-09 至 15 日下载 26,260,125 | 生态最广，但官方渲染依赖 DOM；CLI 引入 Node 与 Chromium |
| beautiful-mermaid | npm 1.1.3；elkjs 锁定为 0.11.1 | 查询时约 1.13 万 Star；同周下载 961,310；Craft Agents 使用 | 无 DOM；ELK 同步入口依赖内部 FakeWorker 实现；文字宽度估算 |
| Pele | npm 1.13.0 | 查询时约 4580 Star；Obsidian 团队开发 | 无运行时包依赖；可注入字体测量；项目历史短 |
| mermaid-rs-renderer | 官方 macOS arm64 发布包 0.3.1 | 查询时约 1823 Star；作者标注早期开发 | 无 JS 或浏览器，但增加 Rust 原生组件；与 Mermaid 的兼容程度仍需验证 |
| f1sherb0y/mermaid-go | 0.1.0 | 查询时 0 Star、2 次提交 | 场景模型方便原生适配，缺少采用与维护历史 |
| zkrebbekx/go-mermaid | 0.1.6 | 查询时 12 Star | 轻量、直接输出 SVG，但仍要解决本项目的文字与标记显示 |

Star 与下载量只是采用度信号，下载量包含 CI、依赖安装等活动，不代表独立用户数。原始查询记录见本轮对话与本地来源归档。

## 实测条件与结果

环境为本机 macOS 27.2、Apple Silicon。所有探针在 `.ryugi/probes/mermaid-research/` 的独立 npm/Go 模块中执行，产品模块不新增依赖。Go 探针使用 `CGO_ENABLED=0`；JavaScriptCore 通过 purego 调用系统公开 C API。

每个有效样例调用四次，以下“重复中位数”为后三次的中位数。结果包含 JS 调用和 SVG 字符串返回，不含 CSS 归一化、usvg 或 MyGo 绘制；是定向样例测量，不是完整性能基准。

| 方案 | 小型中文循环流程图 | 10 节点链 | 50 节点链 | 100 节点链 |
| --- | ---: | ---: | ---: | ---: |
| beautiful-mermaid + JavaScriptCore | 11.805 ms | 19.249 ms | 92.911 ms | 180.127 ms |
| beautiful-mermaid + Goja | 45.461 ms | 79.106 ms | 373.506 ms | 744.465 ms |
| Pele + JavaScriptCore，内置宽度表 | 0.496 ms | 0.419 ms | 1.701 ms | 3.288 ms |
| Pele + JavaScriptCore，MyGo 原生字体测量 | 0.486 ms | 0.534 ms | 2.236 ms | 4.223 ms |

JavaScriptCore 加载 bundle 约 20–27 ms。首次使用 ELK 的 beautiful-mermaid 小图调用约 71 ms；Pele 加原生字体回调的首个小图约 18 ms。冷启动受文件、字体和进程缓存影响，不能把上述数字视作应用启动承诺。

Pele 字体回调实际执行 860 次：JS 经 `JSObjectMakeFunctionWithCallback` 调用 Go，再用 MyGo `Shape` 与字形 advance 返回宽度。不是只在配置中声明了回调。

Rust CLI 对有效样例完成了 SVG 生成。小图重复启动进程约 3–11 ms，100 节点约 111 ms；这个数包含进程启动和字体扫描，与常驻 JS 引擎的数字不属于相同测量边界。

## 兼容性与错误行为

本轮共 19 项定向样例，覆盖中文、英文混排、循环、条件边文字、子图、多行、粗斜体、不同箭头、时序、类、ER、状态、XY、饼图、frontmatter、init、click、转义与 10/50/100 节点链。

- Pele：17 个有效输入生成 SVG；未闭合标签和截断箭头两项输入明确返回带位置的错误，符合探针预期。19 项定向生成／错误检查通过，不等于所有 Mermaid 类型的视觉验收通过。
- beautiful-mermaid：中文和其声明支持的类型能够生成 SVG；不支持饼图和 frontmatter。对两个截断输入均返回了图，不能依靠它判断流式内容是否完整。`init` 被忽略或过滤，不能把生成成功视作已执行该设置。
- Rust：中文、子图与额外类型生成成功；截断箭头返回错误，但未闭合标签样例仍生成了图。`scene` API 是另一种接入选择，本轮未编译或验证该 API，不能把 CLI 结果归为 scene 接入成功。

Pele 官方说明支持 Mermaid 12.1.0 内置类型、frontmatter 与 init，但采用自己的布局和主题，不支持任意 HTML 标签或外部 Mermaid 插件。数学公式需要单独提供 typesetter；本轮没有接入数学或图片。应用内初始支持范围应以产品验收的类型为准。

## 原生显示链路

直接把生成的 SVG 交给 MyGo 不成立：当前 `third_party/mygo/ui/svg.go` 明确不支持文字、标记与过滤器。Pele 的箭头已是普通路径，beautiful-mermaid 则使用 SVG marker。两者都依赖 CSS 变量，beautiful-mermaid 还包含 `color-mix()` 与 Google Fonts 的 `@import`。

本轮验证并排除了以下直接显示方式：

1. macOS `NSImage(data:)` 能解析 SVG，但原始 beautiful-mermaid 样例在绘制时为空白。只成功解码不代表页面内容正确。
2. 固化主题颜色并移除外部字体引用后，NSImage 可以显示中文、路径和形状，但丢失 beautiful-mermaid 的箭头标记。
3. Pele 的简单图、子图与时序图经 NSImage 显示正常，多行标签中的定位 tspan 却被挤成一行，出现裁切。不能直接采用该链路作为通用图表显示。

可行探针链路为：

```text
Mermaid 源码
  → Pele 在 JavaScriptCore 中解析与布局
  → 固化本地主题颜色和字体，关闭链接／图片
  → usvg 0.50.0 将文字、箭头与变换归一化为矢量路径
  → MyGo ParseSVG 与 Image 原生绘制
```

usvg 的四个小图样例重复启动中位数约 2.8–5.9 ms，已包含进程启动与显式加载两份字体。结果中没有 `<text>` 或 `<marker>`，保留普通矢量路径。MyGo 的实际 headless 原生渲染器已绘制中文暗色流程图和中英多行／粗斜体图；截图逐张检查后，多行与箭头完整。截图不是 HTML 替代图，也不是已安装 Shioji 的窗口验收。

本轮 `normalize.py` 只处理已知版本生成的有限 token，是探针，不是生产 SVG/CSS 解析器或安全过滤器。生产实现需要明确支持范围并对未知内容返回源码回退，不可静默丢掉样式或文本。

文字转路径会失去标签的原生选区与语义文本。应保留源码查看和复制，并提供可访问说明；不能宣称图中标签可选中。原生字体测量与 usvg 的字体选择还需要统一到同一组字体及回退，避免布局与字形宽度漂移。

## 打包与生命周期建议

- Pele 全类型 bundle 约 718 KB；beautiful-mermaid bundle 约 1.57 MB。均为本轮打包结果，包含相应依赖；Bun 仅在构建阶段使用。
- JavaScriptCore 由 macOS 提供，不安装或捆绑 Node、Bun 或浏览器。嵌入式上下文没有网络、文件或命令执行桥；字体宽度是本轮唯一自建业务回调。
- usvg 官方 arm64 可执行文件约 3.3 MiB，resvg 约 4.2 MiB，mmdr 约 6.9 MiB。Pele+usvg 方案需要接受新增原生依赖，不能称为全部纯 Go。
- Pele 与 resvg/usvg 的许可分别为 MIT、MIT 或 Apache-2.0。beautiful-mermaid 自身为 MIT，但锁定的 elkjs 为 EPL-2.0，不能只记录顶层 MIT。这里记录包声明，不构成完整发行许可审查。
- 推荐图表生成在控制 Mac 的渲染辅助进程中执行，通过期限终止处理卡死；不把纯显示工作交给 owning worker。Pele 的公开安全说明同样建议用可终止 worker 实现时间限制。具体辅助进程形式仍待实施设计决定。
- UI 只提交已闭合围栏或最终历史块的不可变源码；按内容、主题、字体和版本缓存，限制并发、输入、输出及结果数量。关闭、切会话、主题变化后的迟到结果不得覆盖当前状态。
- 生产方案要禁用外部图片和脚本回调，删除外部字体请求；图片与文件读取仍遵守已有 owning worker 边界。保留源码与可恢复错误状态。

## 尚需验证

正式接入前需要完成隔离签名 App 中的 JavaScriptCore、辅助进程、usvg 与字体访问测试，以及最低支持系统版本验证；本机 macOS 27.2 结果不能代表旧版本。还需要测量真实应用的体积和内存增量、超时与崩溃恢复、渲染缓存回收、复杂图和 emoji 字体回退，以及亮暗主题、窄窗口、缩放、历史加载和流式闭合的真实原生窗口验收。

本轮没有发布页面、启动网络预览或修改用户服务。仓库冻结校验通过，6 个冻结设计文件均与基线一致。本轮没有运行产品测试全套，因为产品代码没有变化。

## 证据与来源

本地完整来源、版本锁与 JSONL 结果位于 `docs/research/mermaid-2026-11-16/`；探针、下载校验与图片在 `.ryugi/probes/mermaid-research/`。这些原始材料按仓库约定忽略，不强行加入 Git。

- [Pele API 与字体测量接口](https://pele.run/api)
- [Pele 支持范围与布局差异](https://pele.run/compatibility)
- [Pele 的链接、图片与时间限制边界](https://pele.run/security)
- [Pele 官方仓库](https://github.com/obsidianmd/pele)
- [beautiful-mermaid 源码与使用方](https://github.com/lukilabs/beautiful-mermaid)
- [beautiful-mermaid 的 ELK 同步实现](https://github.com/lukilabs/beautiful-mermaid/blob/main/src/elk-instance.ts)
- [beautiful-mermaid 的文字宽度估算](https://github.com/lukilabs/beautiful-mermaid/blob/main/src/text-metrics.ts)
- [resvg/usvg 与许可](https://github.com/linebender/resvg)
- [usvg/resvg 0.50.0 发布包](https://github.com/linebender/resvg/releases/tag/v0.50.0)
- [Rust Mermaid 渲染器](https://github.com/1jehuang/mermaid-rs-renderer)
- [Apple JSEvaluateScript](https://developer.apple.com/documentation/javascriptcore/jsevaluatescript(_:_:_:_:_:_:))
- [npm Mermaid 下载统计](https://api.npmjs.org/downloads/point/2026-11-09:2026-11-15/mermaid)
- [npm beautiful-mermaid 下载统计](https://api.npmjs.org/downloads/point/2026-11-09:2026-11-15/beautiful-mermaid)
