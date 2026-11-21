# 当前设备状态

会话及首页工具栏复用已有设备入口。按钮保留设备连接指示，不显示延迟；旁边的咖啡杯快速切换该设备的保持唤醒。弹层展示设备名、CPU 与内存折线图、保持唤醒开关及设备详情入口，不重复已连接状态、硬件副标题或解释文案；浮层不显示时间轴文字或组间分割线，用间距组织内容。设备入口、快捷开关及浮层均不展示 hover 解释文案，保留无障碍名称。本机省略连接方式与延迟行。远程设备在两张负载图下方显示实际连接方式和往返延迟；Tailnet 路径复用已观测的直连、DERP、设备中继或未知结果，不展示控制 Mac 的全局 Tailnet 状态。

本轮未实现全局 Tailnet 快捷入口，也未改动其后台生命周期。

## 数据与采样

协议 minor 38 新增 `device.health` 只读快照和 `device.keepAwake` 显式目标状态写入。快照含所属 DeviceID、观察时间、最近一分钟样本与保持唤醒状态。worker 每两秒采样，最多保留 31 个样本；这是首版工程默认值。采样不写入会话事件日志。

CPU 使用 macOS `host_statistics(HOST_CPU_LOAD_INFO)` 的相邻 tick 差值计算整机非空闲比例，首次尚无间隔时为空。内存读取 `hw.memsize` 与 `host_statistics64(HOST_VM_INFO64)`：已用物理内存为匿名页减可清除页，加 wired 页和压缩器的实际物理页，再乘系统页大小。不把文件缓存、swap 或压缩前的逻辑页重复计入。参见 [Apple VM 统计结构](https://github.com/apple-oss-distributions/xnu/blob/main/osfmk/mach/vm_statistics.h) 与 [内核统计实现](https://github.com/apple-oss-distributions/xnu/blob/main/osfmk/kern/host.c)。本轮不采集 GPU。

客户端用独立、已认证的只读连接查询快照，不占用命令连接；查询失败不把 Agent 判定为停止。时间轴按 worker 报告的相对样本年龄映射到客户端时间，避免远端时钟偏移破坏图表。图表固定一分钟和 0–100% 的尺度，缺失样本或超过五秒的采样间隙断开；不补零、不拉伸短历史。断线或八秒没有新状态后，当前值显示未知、折线冻结并标记历史，禁用保持唤醒操作。

## 保持唤醒

worker 直接调用 IOKit，持有与 `caffeinate -is` 对应的两项断言。`PreventUserIdleSystemSleep` 防止因闲置而自动睡眠，`PreventSystemSleep` 在接通电源时生效，可让系统留在 DarkWake。屏幕仍可熄灭。合盖、手动睡眠及关机由 macOS 决定。参见 [Apple power assertion 类型](https://developer.apple.com/documentation/iokit/iopmlib_h/iopmassertiontypes)。

开关属于设备，由同一 worker 的各客户端共享；命令携带明确目标值及修订版本，拒绝过期修改。GUI 等待权威回执，不乐观显示成功；未知结果只查询，不重发。慢到达的旧状态不能覆盖较新修订的回执。旧连接的迟到回执不回填替换连接或其他设备。

明确保存的目标状态写入 worker 私有目录的 `keep-awake.json`。断言的持有者是 worker，GUI 退出或 SSH 断开后仍生效。关闭开关或 worker 正常退出时释放本 worker 创建的两项断言，启动时按保存状态恢复。只有两项断言都创建成功且设置已保存，才报告已启用。

## 验证

原生组件检查亮暗模式、CPU／内存数值基线、开关右端、浮层边界、远程归属、离线与旧版本禁用、丢失回执不重发。worker 集成检查使用真实系统 assertion，验证另一客户端可读取并关闭相同设备设置。原生实机验收使用独立 client／worker 目录，不覆盖用户已有工作区、节点身份或服务。

## 提交前检查

在仅包含本轮改动的独立 Git 快照中，`env -u GOROOT go test ./...` 全部通过。设备状态相关的 app、worker 与采样模块 race 检查通过；真实 CPU／内存采样连续运行 20 次通过。原生采样检查在两秒内等待有效 CPU tick 间隔，不假定内核必定在 100 ms 内刷新计数。

设计冻结、Codex 协议录制、第三方依赖声明、20 项脚本测试及密钥扫描通过。安全和架构审查未发现需阻止提交的问题。本轮临时模拟与验收工具不进入 Git。
