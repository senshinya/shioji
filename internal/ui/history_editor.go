package ui

import (
	"math"
	"strings"
	"time"
	"unicode/utf8"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

var historyReturnIcon = svg("M15 4v5a2 2 0 0 1-2 2H4M8 7l-4 4 4 4")

func (v *View) historyEditorVisible() bool {
	edit := v.Model.HistoryEdits[v.Model.SessionID]
	if motion := v.historyExit; motion != nil && motion.in.Restart && motion.in.SessionID == v.Model.SessionID && motion.in.ItemID == edit.ItemID {
		return false
	}
	for _, item := range v.Model.Details[v.Model.SessionID].Items {
		if edit.ItemID != "" && item.ID == edit.ItemID {
			return true
		}
	}
	return false
}

func (v *View) historyUserMessage(c *n.Context, t *n.Theme, item domain.Item) {
	m := v.Model
	editing := m.HistoryEdits[m.SessionID].ItemID == item.ID
	preview := v.historyExit != nil && v.historyExit.in.Restart && v.historyExit.in.SessionID == m.SessionID && v.historyExit.in.ItemID == item.ID
	if preview {
		item.Text = v.historyExit.in.Text
		// Retain the original confirmed time until the replacement is accepted.
		item.Status = "dispatching"
		editing = false
	}
	frame := n.Column(c.Key("user-message:" + item.ID)).FillWidth().Label("用户消息")
	natural := n.Local(frame, "height", func() float32 { return 0 })
	shown := *natural
	if shown > 0 {
		if v.reduceMotion {
			frame.Jump("height", shown)
		} else {
			shown = frame.AnimateTo("height", shown, n.Animation{Duration: 280 * time.Millisecond, Ease: composerDescentEase})
		}
		frame.Height(shown)
	}
	// Ordinary sends lift their bubble from the composer. Clip only the local
	// editor resize, otherwise that existing flight is cut off at this row.
	if editing || math.Abs(float64(shown-*natural)) > .5 {
		frame.Clip()
	}
	frame.Children(func() {
		row := n.Column(c.Key("user-content")).FillWidth().AlignItems(n.End).Gap(4)
		if *natural > 0 {
			row.Absolute().Left(0).Right(0).Top(0)
		}
		invalidate := c.Services().Invalidate
		row.DrawOver(func(_ *n.Painter, r n.Rect) {
			if math.Abs(float64(*natural-r.H)) > .5 {
				*natural = r.H
				invalidate()
			}
		})
		rowIndex := v.currentTranscriptRow
		// Preserve the message anchor unless an expanded editor would fall
		// behind the independent dock, including after a window resize.
		if editing && *natural > 0 {
			_, height := c.Size()
			fit := n.Local(frame, "fit", func() n.Point { return n.Point{} })
			if fit.X != *natural || fit.Y != height {
				*fit = n.Point{X: *natural, Y: height}
				limit := max(float32(80), height-v.dockHeight-32-*natural)
				if frame.LayoutBounds().Y > limit {
					if state := v.lists[m.SessionID]; state != nil {
						state.AnchorRow(rowIndex, limit, motionAnimation(c, 280*time.Millisecond))
					}
				}
			}
		}
		begin := func() {
			v.beginHistoryEdit(item)
			if state := v.lists[m.SessionID]; state != nil {
				state.KeepRow(rowIndex, frame.LayoutBounds().Y)
			}
		}
		row.Children(func() {
			if editing {
				v.historyEditor(c, t, item)
				return
			}
			bubble := n.Column(c.Key("user-surface:"+item.ID)).MaxWidth(676).Padding(12, 16).Gap(8).Background(userBubble(t)).Radius(14).Label("用户消息气泡").Children(func() {
				if item.Text != "" {
					text := n.Text(c, item.Text).FontSize(14).FixedLineHeight(24).Selectable()
					v.decoratePlainFind(c, text, item, nil)
				}
				v.historyAttachments(c, t, item)
			})
			if v.sent == nil || v.sent.input != item.ID {
				bubble.Transition(n.ElementTransition{Position: true, Size: true, Colors: true, Duration: 280 * time.Millisecond, Ease: composerDescentEase})
			}
			bubble.ContextMenu(func(menu *n.Menu) {
				if menu.Item("修改并重新开始").Disabled(item.Status != "sent" || item.TurnID == "" || !v.Model.RunReadiness().Capabilities.Restart).Chosen() {
					begin()
				}
			})
			v.sentMessageBubble(c, t, bubble, item)
			v.historyMessageActions(c, t, item, preview, begin)
		})
	})
}

func (v *View) historyMessageActions(c *n.Context, t *n.Theme, item domain.Item, preview bool, begin func()) {
	n.Row(c.Key("user-actions:" + item.ID)).Gap(12).Height(24).AlignItems(n.Center).Unselectable().Label("用户消息操作").Children(func() {
		font := n.Font{Family: t.Font, Size: 11, Weight: 400}
		if (item.Status == "sent" || preview) && item.TurnID != "" {
			edit := n.ButtonBase(c.Key("edit")).Size(20, 24).Label("修改并重新开始").Tooltip("修改并重新开始").Disabled(preview || v.historyBusy[v.Model.SessionID] || !v.Model.RunReadiness().Capabilities.Restart)
			color := textHoverColor(c, edit, t.TextMuted)
			edit.Children(func() { inlineSymbol(c, queueEditIcon, "消息", font, 13, color) })
			if edit.Clicked() {
				begin()
			}
		}
		copy := n.ButtonBase(c.Key("copy")).Size(20, 24).Label("复制消息").Tooltip("复制消息")
		until := n.Local(copy, "copied-until", func() time.Time { return time.Time{} })
		if copy.Clicked() {
			c.WriteClipboard(item.Text)
			*until = c.Now().Add(2 * time.Second)
		}
		glyph, label := copyReplyIcon, "复制消息"
		if c.Now().Before(*until) {
			glyph, label = check, "已复制"
			c.After(until.Sub(c.Now()))
		}
		color := textHoverColor(c, copy, t.TextMuted)
		copy.Tooltip(label).Children(func() { inlineSymbol(c, glyph, "消息", font, 12, color).Label(label) })
		stamp, exact := "—", "发送时间未知"
		if item.SentAt != nil {
			local := item.SentAt.Local()
			stamp, exact = local.Format("15:04"), local.Format("2006-01-02 15:04:05")
		}
		if preview {
			exact += " · 等待提交确认"
		}
		width, _ := c.MeasureText(0, n.Span{Text: "00:00", Font: t.Font, Size: 11, Weight: 400, Features: "tnum"})
		clock := n.Row(c.Key("sent-at")).Width(width).Height(20).Gap(0).Justify(n.End).Clip().Label("发送时间：" + stamp).Tooltip(exact)
		shown := n.Local(clock, "value", func() string {
			// The accepted input has a new item ID. Seed its first painted frame
			// from the preview so the native digit transition can cross that boundary.
			if motion := v.historyExit; motion != nil && motion.in.Restart && motion.in.SessionID == v.Model.SessionID && item.ID == "input:"+motion.in.OperationID && motion.item.SentAt != nil {
				return motion.item.SentAt.Local().Format("15:04")
			}
			return stamp
		})
		value := *shown
		if c.Preferences().ReduceMotion {
			value = stamp
		}
		if *shown != stamp {
			*shown = stamp
			c.AnimationFrame()
		}
		animation := motionAnimation(c, 240*time.Millisecond)
		clock.Children(func() {
			for i, digit := range value {
				text := n.Text(c.Key(i), string(digit)).FontFeatures("tnum").FontSize(11).FixedLineHeight(20).TextColor(t.TextMuted)
				if digit != ':' && !c.Preferences().ReduceMotion {
					text.ContentTransition(n.ContentTransition{Numeric: true, Animation: &animation})
				}
			}
		})
	})
}

func (v *View) historyEditor(c *n.Context, t *n.Theme, item domain.Item) {
	m, sid := v.Model, v.Model.SessionID
	key := m.HistoryDraftKey()
	binding := textBinding(v.draftBindings, key, m.Drafts[key])
	edit := m.HistoryEdits[sid]
	exiting := v.historyExit != nil && v.historyExit.in.SessionID == sid
	locked := edit.OperationID != "" || exiting
	bg := t.Background
	if t.Dark {
		bg = t.Surface
	}
	n.Column(c.Key("user-surface:"+item.ID)).FillWidth().MaxWidth(676).Padding(12, 16).Gap(10).Radius(14).Background(bg).Border(1, t.Border).Label("历史消息编辑器").Transition(n.ElementTransition{Position: true, Size: true, Colors: true, Duration: 280 * time.Millisecond, Ease: composerDescentEase}).Children(func() {
		v.historyAttachments(c, t, item)
		input := n.TextAreaBase(c.Key("text"), &binding.Text).FillWidth().Lines(1, 12).MaxHeight(320).Padding(0).FontSize(14).FixedLineHeight(24).Label("编辑历史消息").ReadOnly(locked)
		input.OnTextKey(func(k n.TextKey) bool {
			if k.Key == n.KeyEscape && !locked {
				m.Drafts[key] = k.Text()
				m.CancelHistoryEdit()
				return true
			}
			if k.Key == n.KeyEnter && k.Mods == n.Cmd {
				m.Drafts[key] = k.Text()
				v.restartHistory()
				return true
			}
			return false
		})
		if v.historyFocus == item.ID {
			input.Focus().SetTextSelection(utf8.RuneCountInString(binding.Text), utf8.RuneCountInString(binding.Text))
			v.historyFocus = ""
		}
		input.Changed()
		m.Drafts[key], binding.LastModel = binding.Text, binding.Text
		n.Row(c).FillWidth().Gap(12).AlignItems(n.Center).Children(func() {
			n.Spacer(c).Grow(1)
			cancelText := "取消"
			if edit.OperationID != "" {
				cancelText = "关闭编辑"
			}
			if ghost(c, cancelText).Height(28).FontSize(12).Label("取消历史编辑").Disabled(exiting || v.historyBusy[sid]).Clicked() {
				v.collectBindings()
				m.CancelHistoryEdit()
			}
			label := "保存并重新开始"
			disabled := !v.historyReady(m.Details[sid].Session) || strings.TrimSpace(binding.Text) == "" && len(item.Attachments) == 0 && len(item.Skills) == 0
			if edit.OperationID != "" {
				label = "核对重开结果"
				if v.historyForked[sid] {
					label = "完成重开"
				}
				disabled = !m.Connected
			}
			disabled = disabled || exiting || v.historyBusy[sid]
			fill := t.Accent
			if disabled {
				fill = subtle(t)
			}
			if controlFeedback(c, n.ButtonBase(c), 14, true).Size(28, 28).Radius(14).Background(fill).TextColor(t.AccentText).Label(label).Tooltip(label).Disabled(disabled).Children(func() {
				n.Icon(c, historyReturnIcon).Size(28.0*14/24, 28.0*14/24)
			}).Clicked() {
				v.collectBindings()
				v.restartHistory()
			}
		})
	})
}

func (v *View) historyAttachments(c *n.Context, t *n.Theme, item domain.Item) {
	if len(item.Attachments) == 0 && len(item.Skills) == 0 {
		return
	}
	n.Row(c).Wrap().Gap(8).Children(func() {
		for _, skill := range item.Skills {
			n.Text(c, "/"+skill.Name).FontSize(12).TextColor(t.TextMuted).Padding(3, 7).Radius(5).Background(codeSurface(t))
		}
		for _, a := range item.Attachments {
			var source n.Rect
			button := control(c.Key(a.ID)).Height(64).MaxWidth(220).Padding(6, 8).Radius(7).Background(codeSurface(t)).Label("预览附件：" + a.Name).Children(func() {
				n.Row(c).Gap(8).AlignItems(n.Center).Children(func() {
					source = v.attachmentThumbnail(c, t, a, v.Model.WorkspaceID, 48).Bounds()
					n.Column(c).Gap(3).Children(func() {
						n.Text(c, a.Name).FontSize(12).SingleLine().MaxWidth(140)
						n.Text(c, attachmentSize(a.Size)).FontSize(11).TextColor(t.TextMuted)
					})
				})
			})
			if button.Clicked() {
				if !strings.HasPrefix(a.MIME, "image/") {
					source = button.Bounds()
				}
				v.openAttachment(a, v.Model.WorkspaceID, source)
			}
		}
	})
}
