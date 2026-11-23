# 执行服务的 Runtime 归属

macOS 文件隐私权限以实际负责进程的应用身份核验。SSH、`setsid` 和后台脱离仅处理连接与进程生命周期，不将 worker 的权限归属转给 Shioji Runtime。执行服务的常驻 supervisor 由签名的 `dev.shioji.runtime` 宿主启动；SSH 只上传制品、提交升级操作和查询原操作。

## 宿主与启动

执行 Runtime 制品保存在设备用户的 `Runtime/Execution/<摘要>/Shioji Runtime.app`，所属 worker 的 `execution-host.json` 记录所选制品。制品经过摘要、签名、Bundle ID 和宿主协议核对后，才可用于接管。

正式宿主使用原有 `Runtime/Shioji Runtime.app` 路径。更新采用签名完整包的原子替换，不注销后台项目，不停止运行中的执行或网络服务，也不改变 Tailnet 身份及系统隐私授权。

已启用登录服务时，通过原 launchd 项启动 Runtime；未注册时，通过 LaunchServices 按需启动 Runtime，不开启登录启动。系统要求审批时先返回对应错误，不绕开后台项目授权。

Runtime 选择并核验实际 worker 构建，由该构建提供 supervisor 实现；Runtime 自身保持为负责应用。worker 的 `runtimeHost` 声明接管能力，`runtimeHosted` 表示当前运行代次由 Runtime 启动；二者不等同于登录服务开关 `managed`。

## 升级接续

旧版迁移、数据库升级和现有 SSH supervisor 的普通升级共用所属设备的排空与身份核验。已有 Runtime supervisor 的普通升级继续使用原更新协议。

迁移助手等待业务守卫放行，核对 writer 与 supervisor 的实际进程、排空回执以及原有锁。源进程退出后，在持有控制锁期间持久化 `installing` 回执，再启动 Runtime，最后释放控制锁并退出。助手不成为新的常驻 supervisor，不提前接纳新任务。

Runtime 获取同一控制锁，按回执核对设备、数据库版本与候选构建；候选以暂停状态启动并通过就绪核对后，才提交构建选择并恢复接纳。数据库迁移保留既有备份、原子替换与中断回滚语义。

重新连接只查询原升级操作，不重发未知提交。Runtime 未在启动期限内取得控制时，查询返回可见错误，底层接管回执仍可由正常重连接续。状态查询本身不启动进程或重放输入。

旧宿主缺少当前 writer 身份查询时，仅从经过同用户核验的 worker socket 对端读取 Shioji 的启动标识，并用 worker RPC 及进程身份复核；参数和环境内容不进入日志。新宿主直接提供只读身份查询。
