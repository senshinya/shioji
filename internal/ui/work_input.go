package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
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
	open, focus bool
	phase       workInputPhase
	operation   domain.WorkInput
	error       string
	epoch       string
	height      float32
}

func (s *workInputUI) locked() bool { return s.phase != workInputReady }
func (s *workInputUI) result(in domain.WorkInput, b json.RawMessage, err error, query bool) {
	if s.operation != in {
		return
	}
	if err != nil {
		var remote *protocol.RemoteError
		if errors.As(err, &remote) && (remote.Code == "notSent" || query && remote.Code == "notFound") {
			s.phase = workInputReady
			s.operation = domain.WorkInput{}
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
		if s.draft == in.Text {
			s.draft = ""
		}
		s.phase = workInputReady
		s.operation = domain.WorkInput{}
		s.error = ""
	case "rejected":
		s.phase = workInputReady
		s.operation = domain.WorkInput{}
		s.error = op.Error
	default:
		s.phase = workInputUnknown
		s.error = "尚未确认是否接收"
	}
}
func workInputAllowed(w domain.WorkActivity) bool {
	return w.Kind == "agent" && (w.Active() && w.CanSteer || !w.Active() && w.CanContinue)
}
func (v *View) sendChildInput(w domain.WorkActivity, u *workDetailUI, query bool) {
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
		if s.locked() || !workInputAllowed(w) || strings.TrimSpace(s.draft) == "" {
			return
		}
		mode := "continue"
		if w.Active() {
			mode = "steer"
		}
		in = domain.WorkInput{OperationID: domain.NewID(), SessionID: v.Model.SessionID, WorkID: w.ID, Mode: mode, Text: s.draft}
		s.operation = in
		s.phase = workInputSending
		s.error = ""
	}
	method := "session.work.send"
	s.epoch = v.Model.ConnectionEpoch
	if query {
		method = "session.work.operation"
	}
	if !v.do(app.Job{Method: method, Input: in, Apply: func(_ *app.Model, b json.RawMessage, err error) { s.result(in, b, err, query) }}) {
		if query {
			s.phase = workInputUnknown
		} else {
			s.phase = workInputReady
			s.operation = domain.WorkInput{}
		}
		s.error = "未能连接设备，草稿已保留"
	}
}
func (v *View) workInput(c *n.Context, t *n.Theme, w domain.WorkActivity, u *workDetailUI) {
	s := &u.input
	if s.operation.OperationID != "" && (s.epoch != v.Model.ConnectionEpoch || !v.Model.Connected) && s.phase != workInputUnknown {
		s.phase = workInputUnknown
		s.epoch = v.Model.ConnectionEpoch
		s.error = "连接已变化，尚未确认是否接收"
	}
	available := workInputAllowed(w)
	if !available && !s.open && s.draft == "" && s.operation.OperationID == "" {
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
			if !editing {
				label := "发送消息"
				if w.CanContinue && !w.Active() {
					label = "继续执行"
				}
				trigger := n.ButtonBase(c.Key("open-child-input")).FillWidth().Height(28).PaddingX(8).Radius(8).Justify(n.Start).Label(label)
				trigger.Children(func() {
					n.Text(c, label).FontSize(12).TextColor(t.TextMuted)
					n.Spacer(c).Grow(1)
					n.Icon(c, arrow).Size(14, 14).TextColor(t.TextMuted)
				})
				if trigger.Clicked() {
					s.open = true
					s.focus = true
				}
				return
			}
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
				field := n.TextAreaBase(c.Key("child-message"), &s.draft).FillWidth().Lines(1, 3).MinHeight(32).MaxHeight(72).Padding(0, 2).FontSize(12).FixedLineHeight(20).Label("子 Agent 消息输入").Placeholder("补充指令…").PlaceholderColor(subtle(t)).Disabled(s.locked())
				if !w.Active() {
					field.Placeholder("继续这个任务…")
				}
				if s.focus && s.open && !s.locked() {
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
						label = "继续执行"
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
					} else {
						n.Text(c, label).FontSize(11).TextColor(t.TextMuted)
					}
					n.Spacer(c).Grow(1)
					if control(c.Key("close-child-input")).Height(24).PaddingX(4).Label("收起子 Agent 输入").Children(func() { n.Text(c, "收起").FontSize(11).TextColor(t.TextMuted) }).Clicked() {
						s.open = false
					}
					disabled := s.locked() || !available || !v.Model.Connected || v.Controller == nil || strings.TrimSpace(s.draft) == ""
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
		})
		feedback := n.Column(c.Key("child-input-feedback")).FillWidth().Shrink(0).Clip()
		animatedContentHeight(c, feedback, s.error != "" || s.phase == workInputUnknown, 180*time.Millisecond, func() float32 {
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
				}
			})
			return 0
		})
	})
}
