package main

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type mobileChildInput struct {
	key                   app.WorkKey
	draftKey, receiptKey  string
	draft, message        string
	pending               domain.WorkInput
	local, stopped        bool
	ready, busy, checking bool
	focused               bool
	controller            *app.Controller
	epoch                 string
}

func newMobileChildInput(m *app.Model, key app.WorkKey) mobileChildInput {
	suffix := key.SessionID + ":" + key.ID
	s := mobileChildInput{key: key, draftKey: "work:" + suffix, receiptKey: "work-receipt:" + suffix}
	s.draft = m.Drafts[s.draftKey]
	var saved domain.WorkInput
	if json.Unmarshal([]byte(m.Drafts[s.receiptKey]), &saved) == nil && s.owns(saved) {
		s.pending, s.local = saved, true
	}
	return s
}

func (s *mobileChildInput) owns(in domain.WorkInput) bool {
	return in.OperationID != "" && in.SessionID == s.key.SessionID && in.WorkID == s.key.ID
}

func (s *mobileChildInput) observe(a *mobileApp) {
	d := a.model.WorkDetails[s.key]
	if remote := d.PendingInput; s.ready && s.pending.OperationID == "" && remote != nil &&
		s.owns(remote.Input) && remote.State.Unsettled() {
		s.pending, s.stopped, s.message = remote.Input, remote.ExecutionStopped, ""
	}
	if s.pending.OperationID == "" {
		return
	}
	id := "input:" + s.pending.OperationID
	for _, entry := range d.Items {
		if item := entry.Item; item.ID == id && item.Kind == "user" && item.Status == "sent" && item.Text == s.pending.Text {
			s.finish(a, true)
			return
		}
	}
}

func (s *mobileChildInput) finish(a *mobileApp, confirmed bool) {
	m := a.model
	if s.local {
		delete(m.Drafts, s.receiptKey)
		if confirmed && s.draft == s.pending.Text {
			s.draft = ""
			delete(m.Drafts, s.draftKey)
		}
		a.save()
	}
	d := m.WorkDetails[s.key]
	if d.PendingInput != nil && d.PendingInput.Input == s.pending {
		d.PendingInput = nil
		m.WorkDetails[s.key] = d
	}
	s.pending = domain.WorkInput{}
	s.busy, s.local, s.stopped = false, false, false
	s.message = ""
}

func (s *mobileChildInput) view(a *mobileApp, c *ui.Context, w *mygo.Window, source domain.Session, work domain.WorkActivity) {
	if work.Kind != "agent" {
		return
	}
	if s.busy && (s.controller != a.controller || s.epoch != a.model.ConnectionEpoch) {
		s.busy = false
	}
	s.observe(a)
	if s.pending.OperationID != "" {
		p := palette(c)
		mobileInlinePanel(c).Padding(panelInset, panelInset, 0).Gap(panelContentGap).Children(func() {
			message := s.message
			if s.busy {
				message = "正在发送…"
				if s.checking {
					message = "正在核对输入…"
				}
			} else if message == "" {
				message = "尚未确认是否接收"
				if s.stopped {
					message = "旧执行已停止，消息接纳结果未确认"
				}
			}
			label(c, message, captionTextSize, p.secondary)
			label(c, s.pending.Text, controlTextSize, p.ink).Selectable()
			action(c, "核对子 Agent 输入回执", func() {
				s.submit(a, w, source, "")
			}).AlignSelf(ui.Start).PaddingX(0).Disabled(s.busy || !a.model.Connected)
		})
		return
	}
	mode, title := "", ""
	switch {
	case work.Active() && work.CanSteer:
		mode, title = "steer", "介入子 Agent"
	case (work.State == "completed" || work.State == "stopped") && work.CanContinue:
		mode, title = "continue", "接续子 Agent"
	}
	if mode != "" || s.draft != "" {
		p := palette(c)
		surface, geometry := mobileComposerSurface(c, s.draftKey, false, s.focused || s.draft != "", nil)
		surface.Children(func() {
			textRight := (mobileTapSize(c) + 5) * (1 - geometry.ControlsProgress)
			ui.Column(c).Margin(geometry.TextTop, textRight, 0, 12).Padding(8, 4).Height(geometry.EditorHeight).Children(func() {
				field := ui.TextAreaBase(c.Key("child-draft"), &s.draft).Label("补充给子 Agent").Placeholder("补充给子 Agent…").
					Multiline(true).Fill().FontSize(bodyTextSize * c.Preferences().TextScale).TextColor(p.ink).PlaceholderColor(p.secondary).
					ReturnKey(ui.ReturnNewLine).
					OnTextKey(func(key ui.TextKey) bool {
						if key.Key != ui.KeyEnter || key.Composing() {
							return false
						}
						switch action := a.model.Preferences.MobileShortcutAction(mobileComposerBinding(key.Mods)); action {
						case "newline":
							if key.Mods == 0 || key.Mods == ui.Shift {
								return false
							}
							key.InsertText("\n")
						case "send", "steer":
							if mode != "" && (action != "steer" || work.Active()) {
								s.draft = key.Text()
								a.model.Drafts[s.draftKey] = s.draft
								s.submit(a, w, source, mode)
							}
						}
						return true
					}).
					OnChange(func() {
						a.model.Drafts[s.draftKey] = s.draft
						a.save()
					})
				if focused := field.Focused(); focused != s.focused {
					s.focused = focused
					c.AnimationFrame()
				}
			})
			if mode != "" {
				ui.Row(c).Absolute().Right(5).Top(geometry.ControlsY).Height(mobileTapSize(c)).Children(func() {
					sendButton(c, title, sendSymbol, false, !s.ready || !a.model.Connected || strings.TrimSpace(s.draft) == "",
						func() { s.submit(a, w, source, mode) }).KeepFocus()
				})
			}
		})
	}
	if s.message != "" {
		label(c, s.message, captionTextSize, palette(c).warning)
	}
}

// A receipt lookup uses the original input. It never issues a new send.
func (s *mobileChildInput) submit(a *mobileApp, w *mygo.Window, source domain.Session, mode string) {
	controller, m := a.controller, a.model
	if s.busy || controller == nil || !m.Connected || !a.pageCurrent(w) || m.WorkKey(source.ID, s.key.ID) != s.key ||
		a.sessionByID(source.ID).ThreadID != source.ThreadID {
		return
	}
	query := mode == ""
	method := "session.work.operation"
	var prepare func() error
	if query {
		if s.pending.OperationID == "" {
			return
		}
	} else {
		if !s.ready || a.stateWriter == nil || s.pending.OperationID != "" || strings.TrimSpace(s.draft) == "" {
			return
		}
		in := domain.WorkInput{OperationID: domain.NewID(), SessionID: s.key.SessionID, WorkID: s.key.ID, Mode: mode, Text: s.draft}
		encoded, err := json.Marshal(in)
		if err != nil {
			s.message = err.Error()
			return
		}
		s.pending, s.local = in, true
		m.Drafts[s.receiptKey] = string(encoded)
		a.save()
		method, prepare = "session.work.send", a.stateWriter.FlushLatest
	}
	in, epoch := s.pending, m.ConnectionEpoch
	s.busy, s.checking, s.message, s.controller, s.epoch = true, query, "", controller, epoch
	if !controller.Do(app.Job{Method: method, Input: in, ConnectionEpoch: epoch, Prepare: prepare,
		Apply: func(current *app.Model, data json.RawMessage, err error) {
			if !a.pageCurrent(w) || a.model != m || current != m || a.controller != controller ||
				s.pending != in || m.ConnectionEpoch != epoch || a.sessionByID(source.ID).ThreadID != source.ThreadID {
				return
			}
			s.busy = false
			if err != nil {
				var remote *protocol.RemoteError
				if errors.As(err, &remote) && (!query && remote.Code == "notSent" || query && remote.Code == "notFound") {
					s.finish(a, false)
				}
				s.message = err.Error()
				w.Invalidate()
				return
			}
			var op domain.WorkInputOperation
			if json.Unmarshal(data, &op) != nil || op.Input != in {
				s.message = "子 Agent 输入回执与当前任务不一致"
			} else {
				switch op.State {
				case domain.OperationConfirmed:
					s.finish(a, true)
				case domain.OperationRejected, domain.OperationAbandoned:
					s.finish(a, false)
					s.message = op.Error
				default:
					s.stopped = op.ExecutionStopped
				}
			}
			w.Invalidate()
		},
	}) {
		s.busy = false
		if !query {
			s.finish(a, false)
		}
		s.message = "请求繁忙，请稍后重试"
		w.Invalidate()
	}
}
