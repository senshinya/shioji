# 会话内容与请求补齐

状态：开发接通、自动验证通过；原生界面人工待验收。用户选择优先补齐内容与请求展示。

沿用 `design/session-components.html`、固定 0.166.0 schema 和现有原生消息列／请求 dock，不修改冻结稿，不引入 WebView。

本轮补齐：文件改动及补丁、MCP／动态工具／协作代理、搜索、公开思考摘要、上下文与审阅事件；计划正文归入右栏，结构化步骤仍只来自结构化事件。工具进度原位更新，final 权威覆盖，重复与迟到事件不增加记录。

请求补齐：按实际 availableDecisions 展示执行审批；额外权限允许选择请求子集并明确本轮／本会话范围；MCP 标准表单支持文本、数值、布尔、单选、多选、格式及约束验证，URL 请求显式打开浏览器并保留请求生命周期。敏感答案只保留内存，不写客户端或 worker 日志／历史。未知扩展不自动批准。

媒体结果先保留明确的类型与资源身份；设备路径不在控制端直接读取。附件上传与输入、会话管理、双会话和长对话已在后续独立模块接通；媒体输出播放、客户端工具执行、账号认证／宿主证明桥接仍未接入，不将只读入口冒充已具备这些能力。

验证：固定 schema 样例的归一化、delta/final/重复、请求回应与 resolved 竞争、权限不扩大、表单约束、持久化恢复及来源隔离；原生亮暗与窄窗截图，展开／收起和底栏高度联动、Reduce Motion。重复测试用协议夹具，不发起新的付费模型调用。人工验收保持待验收。

用户随后授权继续按顺序完成：工作区／会话管理 → 附件与图片输入 → 双会话和长对话。UI/UX 不明确时查 Lody／T3 Code 的实际组件，项目确认规则优先。

本轮参考已读取的 [Lody 工具详情](https://github.com/LodyAI/Lody/blob/main/packages/components/src/components/ai-gui/tool-call-detail.tsx)、[Lody 问题卡片](https://github.com/LodyAI/Lody/blob/main/packages/components/src/components/sessions/ask-user-question-card.tsx)和 [T3 Code 底部待答问题](https://github.com/pingdotgg/t3code/blob/main/apps/web/src/components/chat/ComposerPendingUserInputPanel.tsx)：工具标题不重复到详情中，按参数／结果分段；选项按完整行排列，图标对齐首行；保留独立 composer 和统一请求 dock。参考中的彩色选中与自动翻页不覆盖本项目的中性选中、显式提交和不自动翻页要求。

用户验收调整：请求源位置仅保留状态标识；经 serverRequest/resolved 确认后，把已提交的答案／审批结果作为独立 request_result 项追加到当时的最新位置，位于中间消息之后，随后代理输出之前。该项使用稳定 ID 并由 worker 正常保存／回放；不在原问题下展开答案，也不把断线失效或外部关闭伪装成已回答。回答结果使用左侧中性细线和紧凑的问答文字块，去掉状态标题、外框和嵌套底色，进入时保留轻量动效；敏感答案仍只显示脱敏回执。

界面精简参考重新读取的 [Lody 问题卡片](https://github.com/LodyAI/Lody/blob/main/packages/components/src/components/sessions/ask-user-question-card.tsx)（用间距而非内部色带区分内容）与 [T3 Code 历史问答](https://github.com/pingdotgg/t3code/blob/main/apps/web/src/components/chat/TimelineWorkEntryRow.tsx)（摘要行和展开后的问答文字层级）。项目原生字体、中性色和完整文本优先；不复制其网页组件或改变回答的时间顺序。
