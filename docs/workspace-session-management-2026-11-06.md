# 工作区与会话管理

状态：开发接通、自动验证通过；原生界面人工待验收。用户已授权在内容与请求之后继续实现。

采用 Lody 的紧凑重命名输入和 T3 Code 的会话行上下文操作；沿用现有中性菜单、字体与动效。工作区目录只出现在工作区菜单／设置，不挤占会话列表。

- 会话：重命名、归档、恢复。手动名称保存于 owning worker、两个客户端共享，优先于后续 Codex 名称通知；记录实际来源名称，允许显式恢复使用 Codex 名称。
- 工作区：名称、主目录、附加目录与归档／恢复。修改只影响新会话；已有 launch context 不变。移除目录不删除文件；重新添加旧主目录位于附加目录，不自动晋升。
- 归档保留历史与草稿，不停止任务。运行、待审批／待答、投递不确定或队列非空时拒绝归档。工作区内有任一此类会话时同样拒绝。提交与归档共用 worker 调度边界，数据库事务中复核。
- 归档内容从普通侧栏隐藏，通过搜索与工作区菜单查看／恢复；打开归档会话可以阅读，恢复前不能继续发送。归档工作区内不接受新会话或排队消息。
- 变更带 expected revision，跨客户端冲突保留编辑稿并明确要求重新载入；回执验证来源设备和实体身份。
- JSON 状态增加归档字段，数据库版本提升以阻止旧 worker 忽略归档限制后继续写入；不更改 DeviceID 或已有会话上下文。

参考：[Lody 重命名](https://github.com/LodyAI/Lody/blob/main/packages/components/src/components/sessions/rename-session-dialog.tsx)、[T3 Code 会话行](https://github.com/pingdotgg/t3code/blob/main/apps/web/src/components/sidebar/SidebarThreadRow.tsx)。首版采用已有原生菜单与紧凑表单，不引入通用管理框架。
