package main

import (
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/conversationui"
	"shioji/internal/domain"
)

// Live items can arrive before an initial history read has even been queued.
func hasLatestSessionHistory(detail domain.SessionDetail) bool {
	return detail.HasLoadedHistory() && (detail.History == nil || !detail.History.Newer)
}

func (a *mobileApp) openingConversation(c *ui.Context, split bool) bool {
	opening := a.sessionOpening
	sid, title, scope, caption, problem := a.sessionLoadingTarget, "", "", "正在读取会话", ""
	var retry func()
	if opening != nil {
		sid, title, scope = opening.location.SessionID, opening.title, opening.scope
		caption, problem = "等待设备连接后读取会话", opening.problem
		if opening.retry && problem != "" {
			retry = func() {
				opening.problem, opening.retry = "", false
				a.awaitSessionListTarget(opening)
				a.retryConnection()
			}
		}
	} else {
		if sid == "" || a.model.SessionID != sid {
			return false
		}
		detail := a.model.Details[sid]
		read := a.model.HistoryReads[sid]
		if detail.HasLoadedHistory() || a.preview && len(detail.Items) != 0 {
			a.sessionLoadingTarget = ""
			return false
		}
		session := a.session()
		title, scope = session.Title, a.conversationHeaderScope(session)
		if !a.model.Connected {
			caption = "等待设备连接后读取会话"
		} else if read != nil && read.Error != "" {
			problem = "无法读取会话历史"
			retry = func() {
				if a.controller != nil {
					a.controller.Select(sid)
				}
			}
		}
	}
	p := palette(c)
	c.Root().Background(p.canvas)
	ui.Column(c.Key("opening-session")).Fill().Background(p.canvas).Transition(mobileFeedbackFade(120 * time.Millisecond)).Children(func() {
		if split {
			a.conversationHeader(c, title, scope, nil, nil)
		}
		mobileSessionLoading(c.Key(sid), caption, problem, opening == nil && a.model.Connected, retry)
		if problem == "" {
			mobileContent(c, conversationComposerWidth).Padding(4, conversationComposerInset, 0).
				Children(func() {
					mobileComposerFrame(c, false).FillWidth().Height(mobileTapSize(c)).
						Role(ui.RoleNone).DrawOver(func(painter *ui.Painter, box ui.Rect) {
						painter.Fill(ui.Rect{X: box.X + 16, Y: box.Y + box.H/2 - 3, W: min(104, box.W/3), H: 6}, p.line, 3)
					})
				})
		}
	})
	return true
}

func mobileSessionLoading(c *ui.Context, caption, problem string, reading bool, retry func()) {
	p := palette(c)
	stack := ui.Column(c).FillWidth().Grow(1).Basis(0).MinHeight(0).AlignItems(ui.Center).Justify(ui.Center).Clip().
		Transition(mobileFeedbackFade(120 * time.Millisecond))
	stack.Children(func() {
		if problem != "" {
			ui.Column(c).PaddingX(pageInset).Gap(8).AlignItems(ui.Center).Children(func() {
				label(c, problem, captionTextSize, p.secondary).Role(ui.RoleStatus)
				if retry != nil {
					action(c, "重试", retry)
				}
			})
			return
		}
		const delay = 200 * time.Millisecond
		started := ui.Local(stack, "started", c.Now)
		elapsed := c.Now().Sub(*started)
		if elapsed < delay {
			c.After(delay - elapsed)
		}
		reduced := c.Preferences().ReduceMotion
		ui.Column(c).FillWidth().MaxWidth(480).PaddingX(36).Gap(20).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Height(132 + 26).Gap(6).AlignItems(ui.Center).Children(func() {
				if elapsed < delay {
					return
				}
				ui.Box(c.Key("loading-curve")).Size(132, 132).Role(ui.RoleNone).
					Transition(mobileFeedbackFade(120 * time.Millisecond)).Draw(func(painter *ui.Painter, r ui.Rect) {
					conversationui.PaintSessionLoadingCurve(painter, r, p.ink, painter.Now().Sub(*started)-delay, reduced)
					if !reduced {
						painter.AnimationFrameRate(60)
					}
				})
				text := oneLine(c.Key("loading-caption"), caption, captionTextSize, p.secondary).Role(ui.RoleStatus).
					Transition(mobileFeedbackFade(120 * time.Millisecond))
				if reading {
					mobileShimmeredText(c, text, started.Add(delay))
				}
			})
			ui.Column(c).FillWidth().Gap(12).Role(ui.RoleNone).Children(func() {
				for _, width := range []float32{88, 100, 72, 42} {
					ui.Box(c).WidthPercent(width).Height(6).Radius(3).Background(p.line)
				}
			})
		})
	})
}
