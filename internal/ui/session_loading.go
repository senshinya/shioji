package ui

import (
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/conversationui"
	"shioji/internal/domain"
)

const (
	sessionLoadingDelay = 200 * time.Millisecond
	sessionLoadingFade  = 120 * time.Millisecond
)

var sessionLoadingEase = n.CubicBezier(.2, 0, 0, 1)

// Each pane owns only its current opening. Worker reads, execution and pending
// requests retain their existing independent lifecycle.
type sessionLoading struct {
	session, connection, epoch string
	thread                     string
	started, ready             time.Time
	finished                   bool
	error                      string
	frame, skeleton, content   float32
	history, progress, body    float32
	readyFrame, readyProgress  float32
}

func loadingFraction(elapsed, duration time.Duration) float32 {
	return sessionLoadingEase(min(1, max(0, float32(elapsed)/float32(duration))))
}

func (v *View) syncSessionLoading(c *n.Context) {
	m := v.Model
	if m.SessionID == "" || v.homeSend != nil || v.Controller == nil || v.devices || v.workspaceEditor != nil {
		v.sessionLoad = nil
		return
	}
	d := m.Details[m.SessionID]
	thread := d.Session.ThreadID
	if s := v.sessionLoad; s != nil {
		switch {
		case s.session != m.SessionID || s.connection != m.ConnectionID():
			v.sessionLoad = nil
		case s.thread != "" && thread != "" && s.thread != thread:
			motion := v.historyExit
			if motion != nil && motion.in.Restart && motion.in.SessionID == m.SessionID && motion.phase >= historyDispatched {
				// The initiating pane already owns the replacement bubble and anchor.
				s.thread = thread
			} else {
				v.sessionLoad = nil
			}
		case s.epoch != m.ConnectionEpoch:
			if s.finished && d.HasLoadedHistory() {
				// An already visible reader may have jumped to an older page.
				s.epoch = m.ConnectionEpoch
			} else {
				v.sessionLoad = nil
			}
		}
	}
	// A new visit opens at latest. The older window is still useful during
	// the current visit, but cannot be the new visit's first visible frame.
	ready := d.HasLoadedHistory() && (d.History == nil || !d.History.Newer)
	if v.sessionLoad == nil {
		if ready {
			v.sessionLoad = &sessionLoading{session: m.SessionID, connection: m.ConnectionID(), epoch: m.ConnectionEpoch, thread: thread}
			v.sessionLoad.finish()
			return // Cached history, including an authoritative empty history.
		}
		known := false
		for _, session := range m.Snapshot.Sessions {
			known = known || session.ID == m.SessionID
		}
		if !known {
			return
		}
		v.sessionLoad = &sessionLoading{session: m.SessionID, connection: m.ConnectionID(), epoch: m.ConnectionEpoch, thread: thread, started: c.Now()}
	}
	s := v.sessionLoad
	if thread != "" {
		s.thread = thread
	}
	if s.finished {
		return // Paging and history replacement in this visit stay silent.
	}
	if ready {
		if s.ready.IsZero() {
			if list := v.lists[m.SessionID]; list != nil {
				list.ScrollToEnd()
			}
		}
		if s.frame == 0 && s.progress == 0 || c.Preferences().ReduceMotion {
			s.finish()
			return
		}
		if s.ready.IsZero() {
			s.ready = c.Now()
			s.readyFrame, s.readyProgress = s.frame, s.progress
		}
		elapsed := c.Now().Sub(s.ready)
		exit := loadingFraction(elapsed, sessionLoadingFade)
		s.progress = s.readyProgress * (1 - exit)
		s.body = s.readyFrame * (1 - exit)
		s.frame = s.readyFrame + (1-s.readyFrame)*exit
		if s.ready.Sub(s.started) < sessionLoadingDelay {
			// Fast history appears while the skeleton leaves. Preserve the
			// skeleton's effective alpha as its enclosing frame fades in.
			s.skeleton = s.body / s.frame
			s.history, s.content = exit, exit
			if elapsed >= sessionLoadingFade {
				s.finish()
			} else {
				c.AnimationFrame()
			}
			return
		}
		s.skeleton = 1 - loadingFraction(elapsed-sessionLoadingFade, 180*time.Millisecond)
		s.history = loadingFraction(elapsed-sessionLoadingFade, 120*time.Millisecond)
		s.content = loadingFraction(elapsed-sessionLoadingFade-20*time.Millisecond, 220*time.Millisecond)
		if elapsed >= sessionLoadingFade+240*time.Millisecond {
			s.finish()
		} else {
			c.AnimationFrame()
		}
		return
	}
	s.ready = time.Time{}
	s.error = ""
	if read := m.HistoryReads[m.SessionID]; read != nil && !read.Pending {
		s.error = read.Error
	}
	if p := v.historyPage; p != nil && p.session == m.SessionID && p.error != "" {
		s.error = p.error
	}
	if phase := v.currentConnectionPhase(); phase != app.ConnectionIdle {
		s.error = v.connectionBlocker()
	}
	elapsed := c.Now().Sub(s.started)
	s.frame = loadingFraction(elapsed, sessionLoadingFade)
	s.body, s.progress = s.frame, 0
	s.skeleton, s.content, s.history = 1, 0, 0
	if c.Preferences().ReduceMotion || s.error != "" {
		s.frame, s.body = 1, 1
	}
	if elapsed < sessionLoadingDelay && s.error == "" {
		if s.frame < 1 {
			c.AnimationFrame()
		} else {
			c.After(sessionLoadingDelay - elapsed)
		}
		return
	}
	s.progress = loadingFraction(elapsed-sessionLoadingDelay, sessionLoadingFade)
	if c.Preferences().ReduceMotion || s.error != "" {
		s.progress = 1
	}
	if s.frame < 1 || s.progress < 1 {
		c.AnimationFrame()
	}
}

func (s *sessionLoading) finish() {
	s.finished = true
	s.frame, s.content, s.history = 1, 1, 1
	s.skeleton, s.progress, s.body = 0, 0, 0
}

func (v *View) loadingHistoryOpacity() float32 {
	if s := v.sessionLoad; s != nil {
		return s.history
	}
	return 1
}

func (v *View) loadingBlocksComposer() bool {
	return v.sessionLoad != nil && v.sessionLoad.content == 0
}

func (v *View) loadingComposerFrame(frame n.Element) {
	if s := v.sessionLoad; s != nil {
		frame.Opacity(s.frame)
	}
}

func (v *View) loadingComposerContent(content n.Element, home bool) {
	if s := v.sessionLoad; !home && s != nil {
		content.Opacity(s.content).IgnoreInput(s.content == 0).Disabled(s.content == 0)
	}
}

func (v *View) loadingComposerSkeleton(c *n.Context, t *n.Theme) {
	s := v.sessionLoad
	if s == nil || s.skeleton == 0 {
		return
	}
	n.Column(c.Key("opening-composer-skeleton")).Absolute().Left(0).Right(0).Top(0).Bottom(0).IgnoreInput(true).Role(n.RoleNone).Label("发送框加载骨架").Opacity(s.skeleton).Draw(func(p *n.Painter, r n.Rect) {
		ink := t.Border.Alpha(.65)
		if t.Dark {
			ink = t.Border.Alpha(.5)
		}
		left, right := r.X+14, r.X+r.W-14
		p.Fill(n.Rect{X: left, Y: r.Y + 19, W: 64, H: 6}, ink, 3)
		p.Fill(n.Rect{X: left, Y: r.Y + 48, W: min(240, r.W*.38), H: 6}, ink, 3)
		cy := r.Y + r.H - 22
		p.Fill(n.Rect{X: left, Y: cy - 8, W: 16, H: 16}, ink, 4)
		p.Fill(n.Rect{X: left + 28, Y: cy - 3, W: min(104, (r.W-100)*.35), H: 6}, ink, 3)
		p.Fill(n.Rect{X: left + 28 + min(104, (r.W-100)*.35) + 12, Y: cy - 3, W: min(64, (r.W-100)*.22), H: 6}, ink, 3)
		p.Fill(n.Rect{X: right - 28, Y: cy - 14, W: 28, H: 28}, ink, 14)
	})
	// History hydration must not hide an already known active turn's Stop.
	if v.loadingBlocksComposer() && v.Model.ActiveTurn() != "" && v.Model.Active() && !v.stoppingTurn(v.Model.ActiveTurn()) {
		n.Column(c.Key("opening-stop")).Absolute().Right(14).Bottom(8).Children(func() {
			if control(c, 14).Size(28, 28).Radius(14).Background(t.Accent).TextColor(t.AccentText).Label("停止当前轮").Disabled(!v.Model.Connected || !v.Model.RunReadiness().Capabilities.Interrupt).Children(func() { n.Icon(c, stop).Size(18, 18) }).Clicked() {
				v.interrupt()
			}
		})
	}
}

func (v *View) sessionLoadingOverlay(c *n.Context, t *n.Theme) {
	s := v.sessionLoad
	if s == nil || s.body == 0 && s.progress == 0 {
		return
	}
	bottom := max(108, v.dockHeight) + 8
	n.Column(c.Key("session-loading:" + s.session)).Absolute().Left(18).Right(18).Top(48).Bottom(bottom).Clip().AlignItems(n.Center).Justify(n.Center).Children(func() {
		if s.error != "" {
			n.Column(c).AlignItems(n.Center).Gap(12).Opacity(s.progress).Children(func() {
				label := "无法读取会话历史"
				action := statusAction{label: "重试读取", accessibilityLabel: "重试读取会话", run: func(*n.Context) { v.retrySessionLoading() }}
				if blocker := v.connectionBlocker(); blocker != "" {
					label, action = blocker, v.connectionRecoveryAction()
				}
				n.Text(c, label).FontSize(12).TextColor(t.TextMuted).Tooltip(feedbackDetails(s.error))
				button := ghost(c, action.label).Disabled(action.disabled)
				if action.accessibilityLabel != "" {
					button.Label(action.accessibilityLabel)
				}
				if button.Clicked() {
					action.run(c)
				}
			})
			return
		}
		n.Column(c).FillWidth().MaxWidth(560).AlignItems(n.Center).Gap(24).Children(func() {
			// Reserve the curve and label before their delayed entrance, so the
			// immediately visible skeleton keeps the same position throughout.
			n.Column(c).Height(168 + 8 + 20).AlignItems(n.Center).Gap(8).Opacity(s.progress).Children(func() {
				if s.progress == 0 {
					return
				}
				v.sessionLoadingCurve(c, t, s)
				caption := "正在读取会话"
				if !v.Model.Connected || v.currentConnectionPhase() == app.ConnectionSwitching {
					caption = "等待设备连接后读取会话"
				}
				label := n.Text(c, caption).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
				shimmer(c, label, t.TextMuted, t.Text, s.started.Add(sessionLoadingDelay))
			})
			n.Column(c).FillWidth().PaddingX(8).Gap(12).Role(n.RoleNone).Label("会话正文加载骨架").Opacity(s.body).Children(func() {
				ink := t.Border.Alpha(.65)
				if t.Dark {
					ink = t.Border.Alpha(.5)
				}
				for _, width := range []float32{88, 100, 72, 42} {
					n.Column(c).WidthPercent(width).Height(6).Radius(3).Background(ink)
				}
			})
		})
	})
}

func (v *View) retrySessionLoading() {
	if v.sessionLoad == nil {
		return
	}
	if v.Model.WorkerMinor >= 37 && v.historyPage != nil {
		v.requestHistory(domain.HistoryQuery{}, true)
	} else {
		v.Controller.SelectDevice(v.Model.ConnectionID(), v.Model.SessionID)
	}
}

func (v *View) sessionLoadingCurve(c *n.Context, t *n.Theme, s *sessionLoading) {
	reduced := c.Preferences().ReduceMotion
	n.Column(c.Key("opening-rose")).Size(168, 168).Role(n.RoleNone).Label("会话加载曲线").Draw(func(p *n.Painter, box n.Rect) {
		now := p.Now()
		if !s.ready.IsZero() {
			// Freeze the exit pose while the curve fades.
			now = s.ready
		}
		conversationui.PaintSessionLoadingCurve(p, box, t.Text, now.Sub(s.started.Add(sessionLoadingDelay)), reduced)
		if !reduced && s.ready.IsZero() {
			p.AnimationFrameRate(60)
		}
	})
}
