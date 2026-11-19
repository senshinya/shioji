package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"slices"
	"strings"
	"time"
)

type workInputPhase uint8

const (
	workInputReady workInputPhase = iota
	workInputSending
	workInputUnknown
	workInputChecking
)

type workInputUI struct {
	draft       string
	retry       string
	open, focus bool
	phase       workInputPhase
	operation   domain.WorkInput
	preview     domain.WorkInput
	presentedID string
	started     time.Time
	departed    bool
	error       string
	epoch       string
	settledID   string
	height      float32
}

func (s *workInputUI) locked() bool { return s.phase != workInputReady }

func (s *workInputUI) confirm(in domain.WorkInput) {
	s.settledID = in.OperationID
	if !s.departed && s.draft == in.Text {
		s.draft = ""
	}
	s.phase, s.error, s.departed = workInputReady, "", false
	s.operation = domain.WorkInput{}
}

func (s *workInputUI) reject(in domain.WorkInput, message string) {
	s.settledID = in.OperationID
	if s.departed {
		if s.draft == "" {
			s.draft = in.Text
		} else {
			s.retry = in.Text
		}
	}
	s.phase, s.error, s.departed = workInputReady, message, false
	s.operation, s.preview = domain.WorkInput{}, domain.WorkInput{}
}

func (s *workInputUI) project(detail domain.WorkDetail, now time.Time) domain.WorkDetail {
	in := s.preview
	if in.OperationID == "" {
		return detail
	}
	id := "input:" + in.OperationID
	if s.observe(detail) {
		return detail
	}
	if s.started.IsZero() {
		s.started = now
	}
	item := domain.Item{ID: id, Kind: "user", Text: in.Text, Status: "dispatching", Final: true}
	detail.Items = append(slices.Clone(detail.Items), domain.WorkItem{Item: item})
	return detail
}

func (s *workInputUI) observe(detail domain.WorkDetail) bool {
	in := s.preview
	if in.OperationID != "" && slices.ContainsFunc(detail.Items, func(row domain.WorkItem) bool {
		return row.Item.ID == "input:"+in.OperationID && row.Item.Kind == "user" && row.Item.Text == in.Text
	}) {
		s.preview = domain.WorkInput{}
		if s.operation == in {
			s.confirm(in)
		}
		return true
	}
	return false
}

func (s *workInputUI) messagePose(c *n.Context, bubble n.Element, id string) {
	if id != s.presentedID || s.started.IsZero() || c.Preferences().ReduceMotion {
		return
	}
	progress := min(float32(c.Now().Sub(s.started))/float32(160*time.Millisecond), 1)
	if progress < 1 {
		bubble.Transform(n.Translation(0, 8*(1-n.EaseOut(max(0, progress)))))
		c.AnimationFrame()
	}
}

func (s *workInputUI) result(in domain.WorkInput, b json.RawMessage, err error, query bool) {
	if s.operation != in {
		return
	}
	if err != nil {
		var remote *protocol.RemoteError
		if errors.As(err, &remote) && (remote.Code == "notSent" || query && remote.Code == "notFound") {
			s.reject(in, err.Error())
		} else {
			s.phase = workInputUnknown
		}
		s.error = err.Error()
		return
	}
	var op domain.WorkInputOperation
	if json.Unmarshal(b, &op) != nil || op.Input != in {
		s.phase = workInputUnknown
		s.error = "消息回执与请求不一致"
		return
	}
	switch op.State {
	case "confirmed":
		s.confirm(in)
	case "rejected":
		s.reject(in, op.Error)
	default:
		s.phase = workInputUnknown
		s.error = "尚未确认是否接收"
	}
}
func workInputAllowed(w domain.WorkActivity) bool {
	return w.Kind == "agent" && (w.Active() && w.CanSteer || !w.Active() && w.CanContinue)
}
func (v *View) sendChildInput(w domain.WorkActivity, u *workDetailUI, query bool) {
	v.sendChildInputText(w, u, u.input.draft, query, false)
}

func (v *View) sendChildInputText(w domain.WorkActivity, u *workDetailUI, text string, query, retry bool) {
	s := &u.input
	if !v.Model.Connected || s.phase == workInputSending || s.phase == workInputChecking {
		return
	}
	in := s.operation
	if query {
		if s.phase != workInputUnknown || in.OperationID == "" {
			return
		}
		s.phase = workInputChecking
	} else {
		if s.locked() || s.preview.OperationID != "" || !workInputAllowed(w) || strings.TrimSpace(text) == "" {
			return
		}
		mode := "continue"
		if w.Active() {
			mode = "steer"
		}
		in = domain.WorkInput{OperationID: domain.NewID(), SessionID: v.Model.SessionID, WorkID: w.ID, Mode: mode, Text: text}
		s.operation = in
		s.preview, s.presentedID, s.started = in, "input:"+in.OperationID, time.Time{}
		s.phase, s.error, s.departed = workInputSending, "", true
		if retry {
			s.retry = ""
		} else {
			s.draft = ""
		}
	}
	method := "session.work.send"
	s.epoch = v.Model.ConnectionEpoch
	if query {
		method = "session.work.operation"
	}
	key := v.Model.WorkKey(in.SessionID, in.WorkID)
	if !v.do(app.Job{Method: method, Input: in, Apply: func(model *app.Model, b json.RawMessage, err error) {
		if s.operation == in && s.preview == in {
			s.observe(model.WorkDetails[key])
		}
		s.result(in, b, err, query)
		if s.settledID == in.OperationID {
			d := model.WorkDetails[key]
			if d.PendingInput != nil && d.PendingInput.Input.OperationID == in.OperationID {
				d.PendingInput = nil
				model.WorkDetails[key] = d
			}
		}
	}}) {
		if query {
			s.phase = workInputUnknown
		} else {
			s.reject(in, "未能连接设备，草稿已保留")
		}
		s.error = "未能连接设备，草稿已保留"
	}
}
func (v *View) workInput(c *n.Context, t *n.Theme, w domain.WorkActivity, u *workDetailUI) {
	s := &u.input
	if pending := v.Model.WorkDetails[v.Model.WorkKey(v.Model.SessionID, w.ID)].PendingInput; pending != nil && s.operation.OperationID == "" && pending.Input.OperationID != s.settledID && pending.Input.SessionID == v.Model.SessionID && pending.Input.WorkID == w.ID {
		s.operation = pending.Input
		s.phase = workInputUnknown
		s.epoch = v.Model.ConnectionEpoch
		if s.draft == "" {
			s.draft = pending.Input.Text
		}
		s.error = "尚未确认是否接收"
		if pending.ExecutionStopped {
			s.error = "旧执行已停止，消息接纳结果未确认"
		}
	}
	if s.operation.OperationID != "" && (s.epoch != v.Model.ConnectionEpoch || !v.Model.Connected) && s.phase != workInputUnknown {
		s.phase = workInputUnknown
		s.epoch = v.Model.ConnectionEpoch
		s.error = "连接已变化，尚未确认是否接收"
	}
	available := workInputAllowed(w)
	if !available && !s.open && s.draft == "" && s.retry == "" && s.operation.OperationID == "" {
		label := "此子 Agent 暂不可发送"
		if !v.Model.Connected {
			label = "设备已断开"
		}
		n.Text(c.Key("unavailable-input"), label).FontSize(11).TextColor(t.TextMuted).Padding(0, 12, 12, 12)
		return
	}
	n.Column(c.Key("child-input-dock")).FillWidth().Shrink(0).Padding(0, 12, 12, 12).Gap(0).Children(func() {
		target := float32(28)
		if s.open {
			target = max(78, s.height)
		}
		frame := n.Column(c.Key("child-input-frame")).FillWidth().Shrink(0).Clip().Radius(10).Background(t.Surface).Border(1, t.Border).Label("子 Agent 输入区")
		height := frame.AnimateTo("height", target, motionAnimation(c, 240*time.Millisecond))
		frame.Height(height)
		editing := s.open || height > 28.5
		frame.Children(func() {
			label, placeholder := "发送消息", "补充指令…"
			if !w.Active() {
				placeholder = "继续执行"
				if w.CanContinue {
					label = placeholder
				}
			}
			var field n.Element
			if !editing {
				trigger := n.ButtonBase(c.Key("open-child-input")).FillWidth().Height(26).PaddingX(8).Radius(8).Justify(n.Start).Label(label)
				trigger.Children(func() {
					n.Spacer(c).Grow(1)
					n.Icon(c, arrow).Size(14, 14).TextColor(t.TextMuted)
				})
				if trigger.Clicked() {
					s.open = true
					s.focus = true
				}
			} else {
				content := n.Column(c.Key("child-editor-content")).Absolute().Left(0).Right(0).Top(0).Padding(8).Gap(0)
				n.Inert(content, !s.open)
				invalidate := c.Services().Invalidate
				content.DrawOver(func(_ *n.Painter, r n.Rect) {
					if s.height != r.H {
						s.height = r.H
						invalidate()
					}
				})
				content.Children(func() {
					field = n.TextAreaBase(c.Key("child-message"), &s.draft).FillWidth().Lines(1, 3).MinHeight(32).MaxHeight(72).Padding(0, 2).FontSize(12).FixedLineHeight(20).Label("子 Agent 消息输入").Placeholder(placeholder).PlaceholderColor(n.Transparent)
					if s.focus && s.open {
						field.Focus()
						s.focus = false
					}
					field.OnTextKey(func(k n.TextKey) bool {
						if k.Key == n.KeyEscape {
							s.open = false
							return true
						}
						if k.Key == n.KeyEnter {
							bind := composerBinding(k.Mods)
							if bind == v.Model.Preferences.Shortcuts.Newline {
								return false
							}
							if bind == v.Model.Preferences.Shortcuts.Send || bind == v.Model.Preferences.Shortcuts.Steer {
								s.draft = k.Text()
								v.sendChildInput(w, u, false)
								return true
							}
						}
						return false
					})
					n.Row(c.Key("child-input-controls")).FillWidth().Height(28).Gap(6).AlignItems(n.Center).Children(func() {
						label := "追加指令"
						if !w.Active() {
							label = ""
						}
						if s.phase == workInputSending {
							label = "正在发送"
						} else if s.phase == workInputChecking {
							label = "正在查询"
						} else if s.phase == workInputUnknown {
							label = "尚未确认"
						} else if !v.Model.Connected {
							label = "设备已断开"
						}
						if s.phase == workInputSending || s.phase == workInputChecking {
							n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
								serviceSpinner(c, label, n.Font{Family: t.Font, Size: 11}, t.TextMuted)
								n.Text(c, label).FontSize(11).TextColor(t.TextMuted)
							})
						} else if label != "" {
							n.Text(c, label).FontSize(11).TextColor(t.TextMuted)
						}
						n.Spacer(c).Grow(1)
						if control(c.Key("close-child-input")).Height(24).PaddingX(4).Label("收起子 Agent 输入").Children(func() { n.Text(c, "收起").FontSize(11).TextColor(t.TextMuted) }).Clicked() {
							s.open = false
						}
						disabled := s.locked() || s.preview.OperationID != "" || !available || !v.Model.Connected || v.Controller == nil || strings.TrimSpace(s.draft) == ""
						fill := t.Accent
						if disabled {
							fill = subtle(t)
						}
						send := controlFeedback(c, n.ButtonBase(c.Key("send-child-input")), 14, true).Size(28, 28).Radius(14).Background(fill).TextColor(t.AccentText).Disabled(disabled).Label("发送给子 Agent")
						send.Children(func() { n.Icon(c, arrow).Size(28.0*14/24, 28.0*14/24) })
						if send.Clicked() {
							v.sendChildInput(w, u, false)
						}
					})
				})
			}
			// The same text stays mounted from the collapsed affordance to the
			// native field's placeholder, so neither endpoint swaps or duplicates it.
			promptText := label
			if editing {
				promptText = placeholder
			}
			prompt := n.Text(c.Key("child-input-prompt"), promptText).Absolute().Right(32).Height(20).SingleLine().Font(t.Font).FontSize(12).FixedLineHeight(20).PassThrough().Role(n.RoleNone).Label("子 Agent 输入提示")
			font := n.Font{Family: t.Font, Size: 12, Weight: 400}
			// Center ink in the bordered 26-DIP interior; the open position
			// matches the editor's 8-DIP inset and 2-DIP horizontal text padding.
			x, y := float32(8), (float32(28)-2-20)/2+inlineCenterOffset(label, font)
			color := t.TextMuted
			if s.open {
				x, y, color = 10, 8, subtle(t)
			}
			prompt.Left(prompt.AnimateTo("x", x, motionAnimation(c, 240*time.Millisecond))).Top(prompt.AnimateTo("y", y, motionAnimation(c, 240*time.Millisecond))).TextColor(prompt.AnimateColor("color", color, motionAnimation(c, 240*time.Millisecond)))
			if editing && (s.draft != "" || field.Composing()) {
				prompt.Opacity(0)
			}
		})
		feedback := n.Column(c.Key("child-input-feedback")).FillWidth().Shrink(0).Clip()
		animatedContentHeight(c, feedback, s.error != "" || s.phase == workInputUnknown || s.retry != "", 180*time.Millisecond, func() float32 {
			n.Row(c).FillWidth().Padding(6, 2, 0, 2).Gap(6).AlignItems(n.Baseline).Children(func() {
				text := s.error
				if text == "" {
					text = "尚未确认是否接收"
				}
				n.Text(c, text).FontSize(11).FixedLineHeight(18).TextColor(t.TextMuted).Grow(1).MinWidth(0)
				if s.phase == workInputUnknown {
					if control(c.Key("check-child-input")).Height(24).Padding(0).Disabled(!v.Model.Connected).Label("查询子 Agent 发送状态").Children(func() { n.Text(c, "查询状态").FontSize(11).TextColor(t.TextMuted) }).Clicked() {
						v.sendChildInput(w, u, true)
					}
				} else if s.retry != "" {
					if ghost(c.Key("retry-child-send"), "重试上次消息").Height(24).FontSize(11).Disabled(s.locked() || !available || !v.Model.Connected || v.Controller == nil).Clicked() {
						v.sendChildInputText(w, u, s.retry, false, true)
					}
				}
			})
			return 0
		})
	})
}
