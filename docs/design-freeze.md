# 设计冻结基线

冻结日期：2026-11-03。用户已要求冻结设计，进入技术调研。

`design/` 是本次冻结的源目录；[校验清单](design-freeze-2026-11-03.json) 记录每个文件的 SHA-256 和字节数。冻结时此目录尚无 Git 仓库，清单用于检测后续修改。2026-11-03 已按用户要求初始化 Git（main），origin 为 `ssh://git@ssh.git.shinya.click/shinya/shioji.git`；冻结清单继续独立验证 design/，不替代版本管理或备份。

## 实现依据

- `AGENTS.md` 中用户已确认的产品与交互约束。
- `design/spec.html` 的当前规格和 `design/prototype.html` 的主交互稿。
- `design/menus.css` 等共享样式中的已确认尺寸、密度和中性灰阶。
- `design/screenshots/current-v37/` 的当前截图，以及主稿引用的其他验证截图。
- `design/protocol/codex-0.166.0/` 的协议快照，仅作为该版本依据。

冻结包含研究页、历史截图和独立组件评审页，目的是固定材料，**不表示其中每个历史方案或候选组件都被采用**。`design/session-components.html` 中尚未选择的样式继续保留候选状态；实际 Codex 类型覆盖、生命周期约束与样式是否获选分开处理。

## 后续变更

技术调研、接口草案和验证记录写入 `docs/`，不改写冻结稿。原生实现使用冻结稿作为验收依据；遇到 MyGo 能力缺口，先记录具体差异和补足方案。需要改变产品行为或视觉时，明确提出设计修订，保留旧清单并建立新基线。

2026-11-03 后续确认的架构修订：用户选择自有完整设备端 worker，取消 herdr-core 运行时依赖。冻结稿中提及 herdr-core 的技术说明保留为历史记录，实施时以更新后的 `AGENTS.md` 和技术调研为准；此修订不改变已冻结的视觉与交互。

HTML/CSS/JavaScript 是设计参考，不进入应用主内容渲染。原生实现继续采用 Go + MyGo `ui`，优先复用 MyGo 控件及其系统 API。

预览只在本机或局域网提供；项目级 `autoPublish: false` 保持不变。本次未发布、更新或撤回已有云端材料；目录中先前的发布记录不属于本次冻结操作。

## 校验

Git 仅跟踪当前实现输入：spec.html、menus.css 和固定协议 schema。Lody／T3 Code 等参考截图、HTML/JS 原型及其样式、全部截图和历史评审资料继续保存在本地且保持原冻结哈希，不进入首次 Git 提交。需要运行交互稿或查看参考产品时，使用完整本地归档；新克隆以设计说明和实施规格为依据；最终截图也留在本地供视觉验收。

在项目根目录校验仓库范围（适用于新克隆）：

```sh
python3 scripts/verify-design-freeze.py --repository
```

仓库模式根据 Git 忽略规则筛选原冻结清单，再比较文件集合、字节数与哈希；范围内新增、缺失或修改均会失败。

本机保留全部历史归档时，可额外执行 `python3 scripts/verify-design-freeze.py`，完整校验原始 1,143 个文件。完整模式不会跳过被 Git 忽略的历史文件。清单自身位于 `docs/`，不参与 `design/` 的递归校验。

下一阶段见 [技术调研](technical-research-2026-11-03.md) 和 [Agent 中间模型](agent-model.md)。两份文件的推荐项不自动升级为用户批准的架构决定。
