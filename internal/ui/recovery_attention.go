package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"time"
)

func (v *View) recoveryAttention(c *n.Context, t *n.Theme, content n.Element, d domain.SessionDetail, state *recoveryState, info *domain.SessionRecovery, disabled, busy bool) {
	title, message := "暂时无法继续", "正在核对来源历史，草稿和待发送消息已保留。"
	if d.Session.Status == "abandoned" {
		title = "原会话暂时无法恢复"
		message = "可以重试核对，或在新会话整理接续草稿。"
	}
	if !v.Model.Connected {
		message = "连接设备后会继续核对，草稿和待发送消息已保留。"
	}
	if state.Confirm == "end" {
		message = "请确认没有其他后台任务仍在修改此工作区。继续会结束旧轮记录，不会自动发送待发送消息。"
	}
	processingPanelSurface(content, t).
		Padding(processingPanelHeadingInset, processingPanelInset, processingPanelExpandedInset, processingPanelInset).Gap(8)
	content.Children(func() {
		n.Row(c).Gap(processingPanelIconGap).AlignItems(n.Baseline).Children(func() {
			inlineSymbol(c, recoveryIcon, title, n.Font{Family: t.Font, Size: 13, Weight: 500}, processingPanelIconSize, t.TextMuted)
			n.Text(c, title).FontSize(13).FontWeight(500).FixedLineHeight(processingPanelHeadingLine).Grow(1)
			v.feedbackStatusPager(c, t, len(v.feedbackStatuses), v.feedbackSelection)
		})
		n.Text(c, message).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 180*time.Millisecond)}).FillWidth().PaddingStart(processingPanelTextInset).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
		if state.Details {
			n.Column(c.Key("recovery-details")).FillWidth().PaddingStart(processingPanelTextInset).Gap(8).Transition(disclosureMotion(c, 240*time.Millisecond)).Children(func() {
				n.Text(c, "执行设备 · "+v.Model.CurrentDeviceName()).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted)
				tasks := state.Result.Tasks
				if info != nil && len(info.Tasks) > 0 {
					tasks = info.Tasks
				}
				for _, task := range tasks {
					if task.Command != "" {
						n.Text(c, task.Command).FillWidth().Font(v.Model.Preferences.CodeFont).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Selectable()
					}
				}
				if info != nil && info.Message != "" {
					n.Text(c, info.Message).FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted).Selectable()
				}
				if state.Result.Message != "" && (info == nil || info.Message != state.Result.Message) {
					n.Text(c, state.Result.Message).FillWidth().FontSize(12).FixedLineHeight(18).TextColor(t.TextMuted).Selectable()
				}
			})
		}
		n.Row(c).FillWidth().Wrap().PaddingStart(processingPanelTextInset).Gap(messageFooterGap).AlignItems(n.Center).Children(func() {
			label := "重试恢复"
			if busy {
				label = "正在核对…"
			}
			if state.Confirm == "end" {
				label = "确认任务已停止"
			}
			if ghost(c, label).Height(processingPanelActionHeight).FontSize(12).PaddingX(10).Background(t.Border).Disabled(disabled).Clicked() {
				action := "check"
				if state.Confirm == "end" {
					action = "end"
				}
				state.Confirm = ""
				v.reconcileSession(d.Session, action, state)
			}
			if state.Confirm != "" {
				if ghost(c, "取消").Height(processingPanelActionHeight).PaddingX(8).FontSize(12).Clicked() {
					state.Confirm = ""
				}
			} else {
				detailLabel := "查看处理方法"
				if state.Details {
					detailLabel = "收起详情"
				}
				if ghost(c, detailLabel).Height(processingPanelActionHeight).PaddingX(8).FontSize(12).Clicked() {
					state.Details = !state.Details
				}
				if (info != nil && info.CanEnd && !info.NeedsStop || state.Result.CanEnd) && ghost(c, "结束旧轮记录…").Height(processingPanelActionHeight).PaddingX(8).FontSize(12).Disabled(disabled).Clicked() {
					state.Confirm = "end"
				}
				if d.Session.Status == "abandoned" && ghost(c, "在新会话接续").Height(processingPanelActionHeight).FontSize(12).PaddingX(8).Disabled(busy).Clicked() {
					v.recoveryDraft(d.Session.ID)
				}
			}
		})
	})
}
