package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type mobileRecoveryState struct {
	request       mobileRequest
	checkingSince time.Time
	confirming    bool
	message       string
}

func (a *mobileApp) recoveryControls(c *ui.Context, parent *mygo.Window, s domain.Session, editor *mobileDraftEditor) {
	m := a.model
	info := s.Recovery
	canContinue := info != nil && info.CanContinue &&
		(s.Status == domain.ExecutionInterrupted || s.Status == domain.ExecutionAbandoned || s.Status == domain.ExecutionFailed)
	if s.ID == "" || s.ReadOnlyHistory() || !s.Status.NeedsRecovery() && !canContinue {
		return
	}
	state := ui.Local(c.Root(), "recovery:"+m.Snapshot.DeviceID+":"+s.ID+":"+s.ThreadID, func() mobileRecoveryState {
		return mobileRecoveryState{}
	})
	checking := !canContinue && s.Status.NeedsRecovery() && (info == nil || info.Phase == domain.RecoveryChecking)
	busy := state.request.busy || state.confirming
	if checking && m.Connected && m.WorkerMinor >= 7 && !busy && state.request.err == "" {
		if state.checkingSince.IsZero() {
			state.checkingSince = c.Now()
		}
		if wait := state.checkingSince.Add(300 * time.Millisecond).Sub(c.Now()); wait > 0 {
			c.After(wait)
			return
		}
	} else {
		state.checkingSince = time.Time{}
	}

	p := palette(c)
	connected := m.Connected && m.ConnectionEpoch != "" && m.WorkerMinor >= 7
	historyReady := m.Details[s.ID].HasLoadedHistory()
	draftReady := !busy && a.draftPage == nil && historyReady &&
		!m.HasPendingInputForSession("") && m.ActiveQueueEdit(s.ID).ID == "" && !editor.pending()
	openDraft := func() { a.prepareRecoveryDraft(parent, s, editor) }
	mobileInlinePanel(c.Key("recovery-panel")).PaddingX(panelInset).Gap(panelContentGap).Children(func() {
		if canContinue {
			title := "上次回复未完成"
			if s.Status == domain.ExecutionInterrupted {
				title = "上次回复已中断"
			}
			ready := m.SessionReadiness(s)
			ui.Row(c).Wrap().MinHeight(mobileTapSize(c)).Gap(2).AlignItems(ui.Center).Children(func() {
				recoveryHeading(c, title)
				action(c.Key("continue"), "继续原会话", func() {
					if _, ok := a.mobileRecoverySource(parent, s); !ok {
						return
					}
					in, err := m.BeginContinuationInSession(s.ID)
					if err != nil {
						state.request.err = err.Error()
						return
					}
					a.deliverSubmission(in, "session.submit", "")
				}).PaddingX(4).KeepFocus().Disabled(!connected || busy || !historyReady || ready.State != domain.RuntimeReady ||
					!ready.Capabilities.Continue || m.HasPendingInputForSession(s.ID) || s.ContextChanged)
				iconButton(c.Key("recovery-more"), "更多恢复操作", moreSymbol, 17, nil).KeepFocus().
					PopoverMenuButton(func(menu *ui.Menu) {
						if menu.Item("在新会话接续").Disabled(!draftReady).Chosen() {
							openDraft()
						}
					})
			})
		} else if checking && state.request.err == "" {
			title := "正在恢复会话…"
			if !connected {
				title = "连接设备后自动恢复会话"
			}
			ui.Row(c).MinHeight(mobileTapSize(c)).AlignItems(ui.Center).Children(func() { recoveryHeading(c, title) })
		} else {
			title := "暂时无法继续"
			if s.Status == domain.ExecutionAbandoned {
				title = "原会话暂时无法恢复"
			}
			ui.Row(c).Padding(6, 0, 0).AlignItems(ui.Center).Children(func() { recoveryHeading(c, title) })
			message := state.message
			if message == "" && info != nil {
				message = info.Message
			}
			if message != "" {
				label(c, message, captionTextSize, p.secondary).Padding(0, 0, 0, 21)
			}
			ui.Row(c).Wrap().GapX(4).Children(func() {
				action(c.Key("retry"), "重试恢复", func() {
					a.reconcileMobileSession(parent, s, state, "check")
				}).KeepFocus().Disabled(!connected || busy)
				if info != nil && len(info.Tasks) != 0 {
					action(c.Key("details"), "查看处理方法", func() { a.openRecoveryDetails(parent, s) }).Disabled(busy)
				}
				if info != nil && info.CanEnd && !info.NeedsStop && s.Status == domain.ExecutionUnknown {
					action(c.Key("end"), "结束旧轮记录…", func() {
						a.confirmEndOldTurn(parent, s, state)
					}).Disabled(!connected || busy)
				}
				if s.Status == domain.ExecutionAbandoned {
					action(c.Key("new-draft"), "在新会话接续", openDraft).Disabled(!draftReady)
				}
			})
		}
		if state.request.err != "" || state.request.busy && !checking {
			state.request.status(c)
		}
	})
}

func recoveryHeading(c *ui.Context, title string) {
	p := palette(c)
	ui.Row(c).Grow(1).MinWidth(0).Gap(6).AlignItems(ui.Baseline).Children(func() {
		font := mobileStatusFont(c, captionTextSize)
		transcriptSymbol(c, refreshSymbol, title, font, 15, p.secondary)
		label(c, title, captionTextSize, p.ink).Font(font.Family).FixedLineHeight(mobileStatusLineHeight(font)).Shrink(1).MinWidth(0)
	})
}

func (a *mobileApp) mobileRecoverySource(parent *mygo.Window, source domain.Session) (domain.Session, bool) {
	if !a.visibleLinkSource(parent, source) {
		return domain.Session{}, false
	}
	current := a.sessionByID(source.ID)
	w := a.workspace(current.WorkspaceID)
	return current, w.ID != "" && w.DeviceID == a.model.Snapshot.DeviceID && !w.Archived && !current.Archived && !current.ReadOnlyHistory()
}

func (a *mobileApp) prepareRecoveryDraft(parent *mygo.Window, source domain.Session, editor *mobileDraftEditor) {
	m := a.model
	if _, ok := a.mobileRecoverySource(parent, source); !ok || a.draftPage != nil ||
		!m.Details[source.ID].HasLoadedHistory() || m.HasPendingInputForSession("") ||
		m.ActiveQueueEdit(source.ID).ID != "" || editor.pending() {
		return
	}
	editor.Publish(m, editor.target.key)
	previous := m.SessionID
	if err := m.PrepareRecoveryDraft(source.ID); err != nil {
		m.Error = err.Error()
		a.refresh()
		return
	}
	// The native draft page restores the previously selected pane on cancel.
	m.SessionID = previous
	a.presentDraft(parent, true)
	a.save()
}

func (a *mobileApp) reconcileMobileSession(parent *mygo.Window, source domain.Session, state *mobileRecoveryState, action string) {
	current, ok := a.mobileRecoverySource(parent, source)
	if !ok || state.request.busy || !a.model.Connected || a.model.ConnectionEpoch == "" || a.model.WorkerMinor < 7 {
		return
	}
	if action == "end" && (current.Revision != source.Revision || current.Status != domain.ExecutionUnknown ||
		current.Recovery == nil || !current.Recovery.CanEnd || current.Recovery.NeedsStop) {
		state.request.err = "会话状态已改变，请重新核对"
		a.refresh()
		return
	}
	epoch := a.model.ConnectionEpoch
	state.message = ""
	a.pageJob(parent, &state.request, "session.reconcile", protocol.RecoveryInput{SessionID: current.ID, Revision: current.Revision, Action: action},
		decodePage(func(result protocol.RecoveryResult) error {
			if result.SessionID != current.ID {
				return fmt.Errorf("恢复回执与会话不一致，请重新核对")
			}
			if _, currentScope := a.mobileRecoverySource(parent, current); currentScope && a.model.ConnectionEpoch == epoch {
				state.message = result.Message
				a.model.Readiness = result.Readiness
			}
			return nil
		}))
}

func (a *mobileApp) confirmEndOldTurn(parent *mygo.Window, source domain.Session, state *mobileRecoveryState) {
	if _, ok := a.mobileRecoverySource(parent, source); !ok || state.confirming || state.request.busy {
		return
	}
	epoch := a.model.ConnectionEpoch
	state.confirming = true
	go func() {
		result, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent: parent, Type: mygo.MessageWarning, Message: "结束旧轮记录？",
			Detail:  "请先确认旧任务已停止。待发消息会继续暂停。",
			Buttons: []string{"取消", "结束旧轮记录"}, DestructiveButtons: []int{1},
		})
		mygo.RunOnMain(func() {
			state.confirming = false
			if _, ok := a.mobileRecoverySource(parent, source); !ok {
				return
			}
			if err == nil && result.Button == 1 {
				if a.model.ConnectionEpoch == epoch {
					a.reconcileMobileSession(parent, source, state, "end")
				} else {
					state.request.err = "连接已改变，请重新核对"
				}
			}
			a.refresh()
		})
	}()
}

func (a *mobileApp) openRecoveryDetails(parent *mygo.Window, source domain.Session) {
	if _, ok := a.mobileRecoverySource(parent, source); !ok {
		return
	}
	w := a.page("恢复详情", func(c *ui.Context) {
		mobilePageBody(c, func() {
			s := a.sessionByID(source.ID)
			mobilePageContext(c, s.Title, a.deviceName())
			if info := s.Recovery; info != nil {
				if info.Message != "" {
					mobileMessage(c, info.Message)
				}
				for _, task := range info.Tasks {
					if task.Command != "" {
						label(c.Key(task.ID), task.Command, captionTextSize, palette(c).secondary).
							Font("monospace").Selectable().Padding(5, 0)
					}
				}
			}
		})
	})
	a.pushNative(parent, w)
}
