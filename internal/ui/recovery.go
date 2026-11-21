package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"time"
)

type recoveryState struct {
	Result       protocol.RecoveryResult
	Busy         bool
	Confirm      string
	check        recoveryCheckPhase
	checkStarted time.Time
	Details      bool
	Advanced     bool
	Selected     map[string]bool
}

type recoveryCheckPhase uint8

const (
	recoveryCheckIdle recoveryCheckPhase = iota
	recoveryCheckWaiting
	recoveryCheckVisible
)

func (s *recoveryState) observeCheck(pending bool, now time.Time) {
	if !pending {
		s.check, s.checkStarted = recoveryCheckIdle, time.Time{}
		return
	}
	if s.check == recoveryCheckIdle {
		s.check, s.checkStarted = recoveryCheckWaiting, now
	}
	if now.Sub(s.checkStarted) >= 300*time.Millisecond {
		s.check = recoveryCheckVisible
	}
}

var recoveryIcon = svg("M16 6a7 7 0 1 0 1 7M16 2v5h-5")

func (v *View) recovery(c *n.Context, t *n.Theme, d domain.SessionDetail) {
	m := v.Model
	key := m.ConnectionID() + "/" + d.Session.ID
	if v.recoveries == nil {
		v.recoveries = map[string]*recoveryState{}
	}
	state := v.recoveries[key]
	if state == nil {
		state = &recoveryState{}
		v.recoveries[key] = state
	}
	info := d.Session.Recovery
	canContinue := info != nil && info.CanContinue && (d.Session.Status == "abandoned" || d.Session.Status == "interrupted" || d.Session.Status == "failed")
	needsRecovery := d.Session.Status.NeedsRecovery()
	pending := needsRecovery && m.WorkerMinor >= 7 && (info == nil || info.Phase == "checking")
	state.observeCheck(pending, c.Now())

	expanded := needsRecovery && !pending && !canContinue
	active := expanded || canContinue
	if pending {
		remaining := 300*time.Millisecond - c.Now().Sub(state.checkStarted)
		if state.check == recoveryCheckWaiting {
			c.After(remaining)
		} else {
			active = true
		}
	}
	card := n.Column(c.Key("session-recovery:" + key)).FillWidth().MaxWidth(768).Clip().Label("执行恢复")
	height := n.Local(card, "natural-height", func() float32 { return 26 })
	retained := n.Local(card, "session", func() domain.Session { return d.Session })
	mode := n.Local(card, "mode", func() domain.RecoveryPhase { return domain.RecoveryChecking })
	if active {
		*retained = d.Session
		*mode = domain.RecoveryChecking
		if expanded {
			*mode = domain.RecoveryAttention
		}
		if canContinue {
			*mode = domain.RecoveryReady
		}
	}
	target := float32(0)
	if active {
		target = *height
	}
	animation := motionAnimation(c, 240*time.Millisecond)
	if c.Preferences().ReduceMotion {
		animation.Duration = 0
	}
	shown := card.AnimateTo("height", target, animation)
	gapTarget := float32(0)
	if active {
		gapTarget = 8
	}
	gap := card.AnimateTo("gap", gapTarget, animation)
	presence := gap / 8
	card.Height(shown).Margin(0, 0, gap-8, 0).Hidden(shown == 0 && gap == 0)
	n.Inert(card, !active)
	if shown == 0 {
		return
	}
	if *mode == domain.RecoveryChecking {
		card.Background(t.Background)
	}
	busy := state.Busy || m.Connected && info != nil && info.Phase == "checking"
	disabled := busy || !m.Connected || m.WorkerMinor < 7
	card.Children(func() {
		content := n.Column(c.Key("recovery-content:" + string(*mode))).FillWidth().Shrink(0).Transition(fadeMotion(c, 180*time.Millisecond))
		if !c.Preferences().ReduceMotion {
			content.Transform(n.Translation(0, 8*(1-presence)))
		}
		if *mode == "checking" {
			content.Children(func() {
				n.Row(c).Height(26).Gap(7).AlignItems(n.Baseline).Children(func() {
					text := "正在核对上次回复…"
					if !m.Connected {
						text = "连接后核对上次回复"
					}
					inlineSymbol(c, recoveryIcon, text, n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}, 12, t.TextMuted)
					status := n.Text(c, text).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted).Grow(1)
					v.feedbackStatusPager(c, t, len(v.feedbackStatuses), v.feedbackSelection)
					if m.Connected {
						shimmer(c, status, t.TextMuted, t.Text)
					}
				})
			})
		} else if *mode == "ready" {
			content.Children(func() {
				processingPanelSurface(n.Row(c).FillWidth().Height(44).Padding(8, processingPanelInset).Gap(8).Label("会话恢复提示").AlignItems(n.Center), t).Children(func() {
					label := "上次回复未完成"
					if retained.Status == "interrupted" {
						label = "上次回复已中断"
					}
					n.Row(c).Gap(7).AlignItems(n.Baseline).Grow(1).Children(func() {
						inlineSymbol(c, recoveryIcon, label, n.Font{Family: c.Theme().Font, Size: 12, Weight: 400}, 14, t.TextMuted)
						n.Text(c, label).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
						v.feedbackStatusPager(c, t, len(v.feedbackStatuses), v.feedbackSelection)
					})
					button := ghost(c, "继续原会话").Height(28).FontSize(12).PaddingX(10).Disabled(disabled || m.RunReadiness().State != "ready" || v.continuationPending(d.Session.ID))
					if button.Clicked() {
						v.continueSession(button.Bounds())
					}
					more := iconButton(c, moreActionsIcon, "更多恢复操作").Size(28, 28).Disabled(busy)
					v.choiceMenu(c, t, "recovery:"+key, more, []menuChoice{{id: "new-session", name: "在新会话接续", choose: func() { v.recoveryDraft(d.Session.ID) }}})
				})
			})
		} else {
			v.recoveryAttention(c, t, content, d, state, info, disabled, busy)
		}
		invalidate := c.Services().Invalidate
		content.DrawOver(func(_ *n.Painter, r n.Rect) {
			if r.H > 0 && math.Abs(float64(*height-r.H)) > .5 {
				*height = r.H
				invalidate()
			}
		})
	})
}
func (v *View) recoveryDraft(id string) {
	root := v.splitRoot()
	if err := root.Model.PrepareRecoveryDraft(id); err != nil {
		v.Model.Error = err.Error()
		return
	}
	root.closeParallel()
	root.home()
	root.selected = ""
	root.focusComposer = true
}
func (v *View) continuationPending(id string) bool {
	for _, input := range v.Model.Pending {
		if input.SessionID == id {
			return true
		}
	}
	return false
}
func (v *View) continueSession(origin n.Rect) {
	if v.Controller == nil {
		return
	}
	m := v.Model
	in, err := m.BeginContinuation()
	if err != nil {
		m.Error = err.Error()
		return
	}
	sent := v.beginPresentedSend(in, "")
	// The action originates in the recovery row, but its label is not the
	// message text. Keep the normal reading width throughout the transition.
	sent.source.Y = origin.Y
	var prepare func() error
	if v.Saver != nil {
		state, _ := v.persistenceModel().EncodeState()
		v.Saver.Queue(state)
		v.lastQueued = state
		prepare = v.Saver.FlushLatest
	}
	if !v.do(app.Job{Prepare: prepare, Method: "session.submit", Input: in, Apply: func(model *app.Model, b json.RawMessage, err error) {
		if err != nil {
			v.presentedSendError(model, in, err)
			return
		}
		var op domain.Operation
		if err = json.Unmarshal(b, &op); err != nil || op.ID != in.OperationID {
			v.presentedSendError(model, in, errors.New("设备未确认接续请求"))
			return
		}
		model.Accepted(in, op)
		v.acceptHomeSend(model, op)
	}}) {
		delete(m.Pending, in.OperationID)
		delete(m.PendingDevices, in.OperationID)
		v.sent = nil
		v.clearHomeSend()
	}
}

func (v *View) reconcileSession(s domain.Session, action string, state *recoveryState) {
	if state.Busy || v.Controller == nil {
		return
	}
	state.Busy = true
	connection := v.Model.ConnectionID()
	in := protocol.RecoveryInput{SessionID: s.ID, Revision: s.Revision, Action: action}
	if action == "stop-tasks" {
		for id, selected := range state.Selected {
			if selected {
				in.Targets = append(in.Targets, id)
			}
		}
	}
	if !v.do(app.Job{ConnectionID: connection, Method: "session.reconcile", Input: in, Apply: func(m *app.Model, b json.RawMessage, err error) {
		state.Busy = false
		if err != nil {
			state.Result.Message = err.Error()
			return
		}
		var result protocol.RecoveryResult
		if err = json.Unmarshal(b, &result); err != nil || result.SessionID != s.ID {
			state.Result.Message = "恢复回执与会话不一致，请重新核对"
			return
		}
		state.Result = result
		if action == "stop-tasks" {
			state.Selected = map[string]bool{}
		}
		if result.Status == "unknown" || result.Status == "active" {
			state.Details = true
		}
		if device := m.DeviceStates[connection]; device != nil {
			device.Readiness = result.Readiness
			m.RefreshDevices()
		} else if m.ConnectionID() == connection {
			m.Readiness = result.Readiness
		}
		if m.ConnectionID() == connection {
			m.Error = ""
		}
	}}) {
		state.Busy = false
	}
}
