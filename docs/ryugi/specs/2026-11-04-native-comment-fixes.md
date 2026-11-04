# 原生验收评论修复

来源：本地 artboard/shioji-stage-1 的 13 条用户评论（2026-11-04）。沿用冻结设计，不新增视觉方案。

1. Permission 删除文字下拉字符，补盾牌图标和明确权限名称；run 配置的箭头使用 12 px SVG，和文字垂直居中。sidebar 新建／待处理使用对应 SVG；顶部补搜索，底部设备有图标，设置使用图标；恢复冻结导航间距。
2. 超过 1000 的 token 显示为一位小数 k；未知仍为 —，详情保留精确 input/output/cached。
3. 补实际 owning worker 的 Git 查询、可搜索分支选择及 Home 新建 worktree。Home 选择保存意图，接纳提交时执行；已有会话分支只能显式切换，导航绝不 checkout。按主规格 §9 保护 shared checkout：busy/pending/indeterminate 拒绝切分支，真实切分支要求完全干净，普通同分支提交可 dirty；已有 launch context 保留；其他 idle 会话变 contextChanged，需要显式确认；每次提交检查外部 Git 变化。非 Git 工作区仍可提交，不伪造 main。
4. 搜索真实的工作区会话标题／对话内容，通过 worker 查询；选择结果只导航，保留草稿。
5. 右侧栏入口打开 Changes 文件导航，使用 worker 的真实 checkout 文件改动。文件在会话旁的独立原生 diff reader 打开。无结构化 Plan 事件不伪造 Plan。侧栏独立全高，工具栏在分隔线终止；窄窗口用抽屉。
6. 实机截图覆盖浅深色、Git/非 Git、搜索和右栏；沿用现有评论线程更新证据并逐条回复。重复模型验证使用录制／本地确定性 provider，无额外真实账号模型调用。

不扩大到队列、SSH/Tailnet、设置页完整功能。所有新增设备文件/Git操作只能通过 authenticated worker typed API，不在 UI 使用本机路径读取远端文件。新功能不修改冻结设计。
