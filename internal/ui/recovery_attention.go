package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"time"
)

func (v *View) recoveryAttention(c *n.Context, t *n.Theme, content n.Element, d domain.SessionDetail, state *recoveryState, info *domain.SessionRecovery, disabled, busy bool) {
	if state.Selected == nil {
		state.Selected = map[string]bool{}
	}
	var tasks []domain.RecoveryTask
	if info != nil {
		tasks = info.Tasks
	}
	if len(tasks) == 0 {
		tasks = state.Result.Tasks
	}
	selected := false
	allStopped := len(tasks) > 0
	for _, task := range tasks {
		selected = selected || state.Selected[task.ID] && task.State == "running"
		allStopped = allStopped && task.State == "stopped"
	}
	title, message := "暂时无法继续", "后台任务的状态尚未确认，草稿和待发送消息已保留。"
	if d.Session.Status == "abandoned" {
		title, message = "原会话暂时无法恢复", "可以重试恢复，或先在新会话整理接续草稿。"
	}
	if !v.Model.Connected {
		message = "连接设备后会自动尝试恢复，草稿和待发送消息已保留。"
	}
	if v.Model.WorkerMinor < 7 {
		message = "请先更新此设备的 Shioji worker，再恢复会话。"
	}
	if state.Details && d.Session.Status != "abandoned" {
		message = "仍有未确认结束的任务，继续前需要先处理。"
	}
	if state.Details && allStopped {
		message = "所选进程已停止。确认没有其他后台任务后，即可结束旧轮记录。"
	}
	if state.Confirm == "stop-tasks" {
		message = "将停止所选进程及同组进程，可能影响此设备的其他任务。旧轮记录和待发送消息仍保持暂停。"
	}
	if state.Confirm == "stop-runtime" {
		message = "将停止对应的 Agent 进程并重新检查。旧版共享进程可能关联多个会话；工具执行和待发送消息仍保持暂停。"
	}
	if state.Confirm == "end" {
		message = "请确认没有其他后台任务仍在修改此工作区。继续会结束旧轮记录，不会自动发送待发送消息。"
	}
	content.Padding(12, 14).Gap(8).Radius(requestRadius).Background(t.Surface).Border(1, t.Border)
	content.Children(func() {
		n.Row(c).Gap(7).AlignItems(n.Baseline).Children(func() {
			inlineSymbol(c, recoveryIcon, title, n.Font{Family: c.Theme().Font, Size: 13, Weight: 400}, 14, t.TextMuted)
			n.Text(c, title).FontSize(13).FixedLineHeight(20)
		})
		n.Text(c, message).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)}).FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
		n.Row(c).Gap(8).AlignItems(n.Center).Children(func() {
			label, command := "重试恢复", "check"
			if state.Details && info != nil && info.CanStopTasks && v.Model.WorkerMinor >= 8 {
				label, command = "停止所选进程", "confirm-tasks"
			}
			if state.Details && allStopped {
				label, command = "确认任务已结束", "confirm-end"
			}
			if state.Details && info != nil && info.NeedsStop {
				label, command = "重启 Agent 并检查", "confirm-runtime"
			}
			if state.Confirm != "" {
				command = state.Confirm
				label = "确认并恢复"
				if command == "end" {
					label = "确认任务已停止"
				}
				if command == "stop-tasks" {
					label = "确认停止所选进程"
				}
			}
			if busy {
				label = "正在恢复…"
			}
			button := ghost(c, label).Height(26).FontSize(12).PaddingX(10).Background(t.Border).Disabled(disabled || command == "confirm-tasks" && !selected)
			if button.Clicked() {
				switch command {
				case "confirm-tasks":
					state.Confirm = "stop-tasks"
					state.Details = true
				case "confirm-end":
					state.Confirm = "end"
				case "confirm-runtime":
					state.Confirm = "stop-runtime"
					state.Details = true
				default:
					state.Confirm = ""
					v.reconcileSession(d.Session, command, state)
				}
			}
			if state.Confirm != "" {
				if ghost(c, "取消").Height(26).PaddingX(8).FontSize(12).Clicked() {
					state.Confirm = ""
				}
			} else {
				label := "查看处理方法"
				if state.Details {
					label = "收起详情"
				}
				if ghost(c, label).Height(26).PaddingX(8).FontSize(12).Clicked() {
					state.Details = !state.Details
				}
				if d.Session.Status == "abandoned" && ghost(c, "在新会话接续").Height(26).FontSize(12).PaddingX(8).Disabled(busy).Clicked() {
					v.recoveryDraft(d.Session.ID)
				}
			}
		})
		if !state.Details {
			return
		}
		n.Column(c.Key("recovery-details")).FillWidth().Gap(8).Transition(disclosureMotion(c, 240*time.Millisecond)).Children(func() {
			n.Text(c, "执行设备 · "+v.Model.CurrentDeviceName()).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
			if d.Session.Context.CWD != "" {
				n.Text(c, d.Session.Context.CWD).FillWidth().FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Selectable()
			}
			if len(tasks) == 0 {
				for _, item := range d.Items {
					if item.Kind == "tool" && item.Status != "completed" && item.Status != "failed" {
						tasks = append(tasks, domain.RecoveryTask{Command: item.Name, State: "unknown"})
					}
				}
			}
			if len(tasks) > 0 {
				if info != nil && info.CanStopTasks {
					n.Text(c, "以下进程可能与任务相关。核对实际命令后选择要停止的进程，默认不会停止任何进程。").FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
				}
				n.Scroll(c.Key("recovery-task-list")).FillWidth().MaxHeight(132).Children(func() {
					n.Column(c).FillWidth().Gap(8).Padding(8, 10).Radius(6).Background(codeSurface(t)).Children(func() {
						for _, task := range tasks {
							n.Column(c.Key(task.ID + task.Command)).FillWidth().Gap(4).Transition(resultBlockMotion(c)).Children(func() {
								label := "运行状态未确认"
								if task.State == "running" {
									label = fmt.Sprintf("可能相关 · PID %d", task.PID)
									if task.ProcessGroup > 0 && task.PID != task.ProcessGroup {
										label += fmt.Sprintf(" · 进程组 %d", task.ProcessGroup)
									}
								}
								if task.State == "stopped" {
									label = "已停止"
								}
								n.Text(c, label).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)}).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
								command := task.Command
								if task.ProcessCommand != "" {
									command = task.ProcessCommand
								}
								n.Text(c, command).FillWidth().Font(v.Model.Preferences.CodeFont).FontSize(11).FixedLineHeight(18).Selectable()
								if task.PID > 0 && task.State == "running" && task.ProcessCommand != "" && v.Model.WorkerMinor >= 8 && info != nil && info.CanStopTasks {
									checked := state.Selected[task.ID]
									if controlFeedback(c, n.Checkbox(c.Key(task.ID+"/select"), &checked, "停止此进程组").MinHeight(26).FontSize(12).Label(fmt.Sprintf("选择进程 %d", task.PID)).Disabled(disabled || state.Confirm != ""), 4).Changed() {
										state.Selected[task.ID] = checked
									}
								}
							})
						}
					})
				})
			}
			canStop := info != nil && info.CanStopTasks && v.Model.WorkerMinor >= 8
			if !canStop && !allStopped {
				n.Text(c, "当前信息不足，无法由 Shioji 安全处理这些任务。可重试恢复，或在设备确认任务已结束后使用高级处理。").FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted)
			}
			if state.Advanced && info != nil && info.Message != "" {
				n.Text(c, info.Message).FillWidth().FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
			}
			if state.Result.Message != "" && (info == nil || info.Message != state.Result.Message) {
				n.Text(c, state.Result.Message).FillWidth().FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
			}
			canEnd := info != nil && info.CanEnd || state.Result.CanEnd
			if canEnd && !canStop && !allStopped {
				if ghost(c, "高级处理").Height(24).PaddingX(0).FontSize(11).Clicked() {
					state.Advanced = !state.Advanced
				}
				if state.Advanced && ghost(c, "我已在设备上停止这些任务").Height(26).FontSize(12).PaddingX(8).Disabled(disabled).Clicked() {
					state.Confirm = "end"
				}
			}
		})
	})
}
