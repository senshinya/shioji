package main

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
)

func (a *mobileApp) connectionPhase() app.ConnectionPhase {
	phase := a.model.ConnectionPhase
	if !a.model.Connected && phase == app.ConnectionIdle {
		if a.model.ConnectionError != "" || a.preview {
			return app.ConnectionUnavailable
		}
		return app.ConnectionOpening
	}
	return phase
}

func mobileConnectionLabel(phase app.ConnectionPhase) string {
	switch phase {
	case app.ConnectionOpening:
		return "正在连接"
	case app.ConnectionSyncing:
		return "正在同步会话"
	case app.ConnectionReconnecting:
		return "正在重连"
	case app.ConnectionSwitching:
		return "正在切换线路"
	case app.ConnectionUnavailable:
		return "连接不可用"
	case app.ConnectionSyncFailed:
		return "会话同步受阻"
	default:
		return ""
	}
}

func (a *mobileApp) connectionNotice(c *ui.Context, parent *mygo.Window) {
	if a.endpoint.Address == "" && !a.preview {
		return
	}
	phase := a.connectionPhase()
	message := mobileConnectionLabel(phase)
	if message == "" {
		return
	}
	p := palette(c)
	ui.Row(c).Margin(3, pageInset).PaddingX(10).MinHeight(mobileTapSize(c)).Gap(7).Radius(10).
		Background(p.inset).AlignItems(ui.Center).Transition(mobilePanelTransition).Children(func() {
		if phase == app.ConnectionUnavailable || phase == app.ConnectionSyncFailed {
			ui.Icon(c, wifiOffSymbol).Size(15, 15).TextColor(p.secondary).PassThrough()
		} else {
			ui.Spinner(c).Size(15, 15).TextColor(p.secondary).PassThrough()
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).PaddingY(5).Children(func() {
			label(c, message, captionTextSize, p.secondary).Role(ui.RoleStatus)
			if phase == app.ConnectionUnavailable {
				detail := "草稿已保留"
				if observed := a.model.Snapshot.ObservedAt; !observed.IsZero() {
					detail = "历史保留至 " + observed.Local().Format("15:04")
				}
				label(c, detail, captionTextSize, p.secondary)
			}
		})
		if phase == app.ConnectionUnavailable {
			action(c, "重连", a.retryConnection).PaddingX(2)
		} else if phase == app.ConnectionSyncFailed && parent != nil {
			action(c, "设备详情", func() { a.openDevice(parent) }).PaddingX(2)
		}
	})
}

func (a *mobileApp) retryConnection() {
	if a.preview {
		a.model.Connected, a.model.ConnectionPhase = true, app.ConnectionIdle
		a.model.ConnectionError = ""
	} else if (a.controller == nil || !a.controller.Do(app.Job{Method: "$reconnect"})) && a.endpoint.Address != "" {
		a.connect(a.endpoint)
	}
	a.refresh()
}

func (a *mobileApp) collectOperationFeedback() {
	a.splitRoot().operationHistory.Collect(a.endpoint.DeviceID, a.model.Feedbacks, a.model.UncertainSubmissions)
	if a.endpoint.DeviceID != "" {
		a.model.Feedbacks = nil
	}
}

func (a *mobileApp) operationFeedbackRecords(workspace, session string) []app.Feedback {
	return a.splitRoot().operationHistory.For(a.endpoint.DeviceID, workspace, session)
}

func (a *mobileApp) openOperationHistory(parent *mygo.Window, workspace, session string) {
	var w *mygo.Window
	w = a.page("最近操作", func(c *ui.Context) {
		mobilePageBody(c, func() {
			entries := a.operationFeedbackRecords(workspace, session)
			if len(entries) == 0 {
				mobileMessage(c, "暂无操作记录")
			}
			for _, entry := range entries {
				a.operationFeedbackRow(c.Key(entry.ID), w, a.endpoint.DeviceID, entry, "", "", nil)
			}
		})
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) openRecentOperations(parent *mygo.Window) {
	if a.wide && parent == a.home && a.detail != nil {
		parent = a.detail
	}
	var w *mygo.Window
	var opening app.SessionLocation
	var deadline time.Time
	var problem string
	open := func(target app.SessionLocation) {
		if !a.pageCurrent(w) || target.DeviceID == "" {
			return
		}
		if a.endpoint.DeviceID != target.DeviceID {
			if err := a.openDeviceFromResults(w, target.DeviceID); err != nil {
				problem = err.Error()
				w.Invalidate()
				return
			}
		} else if _, session := a.operationSource(target); session.ID != "" && a.model.Snapshot.DeviceID == target.DeviceID {
			a.openSessionFrom(w, session.ID)
			return
		}
		opening, deadline, problem = target, time.Now().Add(sessionCatalogTimeout), ""
		w.Invalidate()
	}
	w = a.page("最近操作", func(c *ui.Context) {
		if opening.DeviceID != "" {
			if a.model.Connected && a.model.ConnectionEpoch != "" && a.model.Snapshot.DeviceID == opening.DeviceID && a.endpoint.DeviceID == opening.DeviceID {
				_, session := a.operationSource(opening)
				opening = app.SessionLocation{}
				if session.ID == "" {
					problem = "原会话已改变或移除"
				} else {
					mygo.RunOnMain(func() { a.openSessionFrom(w, session.ID) })
				}
			} else if !c.Now().Before(deadline) {
				opening, problem = app.SessionLocation{}, "无法连接操作所属的设备"
			} else {
				c.After(deadline.Sub(c.Now()))
			}
		}
		mobilePageBody(c, func() {
			if opening.DeviceID != "" {
				mobileMessage(c, "正在连接操作所属的设备…")
			} else if problem != "" {
				mobileMessage(c, problem)
			}
			history := &a.operationHistory
			entries := history.Recent()
			if slices.ContainsFunc(entries, func(entry mobileclient.RecentOperation) bool { return entry.Kind != app.FeedbackSubmissionUncertain }) {
				action(c.Key("clear"), "清空已完成记录", func() { history.ClearSettled(); a.refresh() }).AlignSelf(ui.End)
			}
			if len(entries) == 0 {
				mobileMessage(c, "暂无操作记录")
			}
			for _, entry := range entries {
				ref := app.SessionLocation{DeviceID: entry.DeviceID, WorkspaceID: entry.WorkspaceID, SessionID: entry.SessionID}
				scope, session := a.operationSource(ref)
				var navigate func()
				if session.ID != "" && opening.DeviceID == "" {
					navigate = func() { open(ref) }
				}
				a.operationFeedbackRow(c.Key([4]string{entry.DeviceID, entry.WorkspaceID, entry.SessionID, entry.ID}), w,
					entry.DeviceID, entry.Feedback, scope, session.Title, navigate)
			}
		})
	})
	a.pushNative(parent, w)
}

func (a *mobileApp) operationSource(ref app.SessionLocation) (string, domain.Session) {
	catalog := a.sessionCatalog(ref.DeviceID)
	device := mobileDeviceName(catalog.Name, catalog.Address)
	if device == "" {
		if saved := a.savedDevice(ref.DeviceID); saved.Endpoint.DeviceID == ref.DeviceID {
			device = a.deviceLabel(ref.DeviceID, saved.Endpoint.Address)
		}
		if device == "" {
			device = "设备不可用"
		}
	}
	if catalog.Snapshot.DeviceID != ref.DeviceID {
		return device, domain.Session{}
	}
	for _, workspace := range catalog.Snapshot.Workspaces {
		if workspace.ID == ref.WorkspaceID && workspace.DeviceID == ref.DeviceID {
			parts := []string{device, workspace.Name}
			for _, session := range catalog.Snapshot.Sessions {
				if session.ID == ref.SessionID && session.WorkspaceID == workspace.ID &&
					(session.Context.DeviceID == "" || session.Context.DeviceID == ref.DeviceID) {
					return strings.Join(append(parts, session.Title), " · "), session
				}
			}
			return strings.Join(parts, " · "), domain.Session{}
		}
	}
	return device, domain.Session{}
}

func (a *mobileApp) operationFeedbackRow(c *ui.Context, w *mygo.Window, device string, entry app.Feedback, scope, session string, navigate func()) {
	p := palette(c)
	ui.Column(c).PaddingY(10).Gap(4).Children(func() {
		label(c.Key("title"), entry.Title, controlTextSize, p.ink).FontWeight(580)
		if scope != "" {
			label(c.Key("scope"), scope, captionTextSize, p.secondary)
		}
		if entry.Body != "" {
			label(c.Key("body"), entry.Body, captionTextSize, p.secondary)
		}
		if entry.Details != "" {
			label(c.Key("details"), entry.Details, captionTextSize, p.secondary).Selectable()
		}
		if in, pending := a.model.Pending[entry.ID]; device == a.endpoint.DeviceID && entry.Kind == app.FeedbackSubmissionUncertain && pending &&
			in.WorkspaceID == entry.WorkspaceID && in.SessionID == entry.SessionID {
			a.operationReceipt(c.Key("lookup"), w, in)
		}
		if navigate != nil {
			action(c.Key("source"), "查看会话", navigate).Label("查看会话：" + session).AlignSelf(ui.Start).PaddingX(0)
		}
	})
}

func (a *mobileApp) composerOperationError(c *ui.Context, parent *mygo.Window, editor *mobileDraftEditor, showGeneric bool) {
	m := a.model
	history := &a.splitRoot().operationHistory
	source := app.SessionLocation{DeviceID: a.endpoint.DeviceID, WorkspaceID: editor.target.workspace, SessionID: editor.target.session}
	if failure, ok := history.SubmissionFailureFor(source); ok && !m.HasPendingInputForSession(source.SessionID) {
		a.submissionFailurePanel(c.Key(failure.ID), parent, editor, failure, func() {
			history.AcknowledgeSubmissionFailures(source)
		})
	}
	if !showGeneric || m.Error == "" || m.Error == domain.ModelUnavailableMessage || m.Error == m.ConnectionError {
		return
	}
	message := mobileclient.RedactOperationDiagnostics(m.Error)
	if _, submission := history.SubmissionFailure(source.DeviceID, message); !submission {
		label(c, message, captionTextSize, palette(c).warning).Role(ui.RoleStatus)
	}
}

func (a *mobileApp) submissionFailurePanel(c *ui.Context, parent *mygo.Window, editor *mobileDraftEditor, failure app.Feedback, acknowledge func()) {
	p := palette(c)
	summary, _, _ := strings.Cut(failure.Details, "\n")
	const summaryLimit = 100
	if runes := []rune(summary); len(runes) > summaryLimit {
		summary = string(runes[:summaryLimit]) + "…"
	}
	hasDetails := summary != failure.Details
	panel := mobileInlinePanel(c).Padding(panelInset).Gap(panelContentGap)
	expanded := ui.Local(panel, "expanded", func() bool { return false })
	panel.Children(func() {
		label(c.Key("title"), failure.Title, controlTextSize, p.ink).FontWeight(620).Role(ui.RoleStatus)
		if summary != "" && !*expanded {
			label(c.Key("reason"), summary, captionTextSize, p.secondary).Selectable()
		}
		ui.Row(c.Key("failure-actions")).Wrap().Gap(panelContentGap).Children(func() {
			action(c.Key("return"), "返回草稿", func() {
				if editor.current(a, parent) {
					acknowledge()
					editor.focusRequested = true
					a.refresh()
				}
			}).PaddingX(4).KeepFocus()
			if hasDetails {
				title := "查看详情"
				if *expanded {
					title = "收起详情"
				}
				action(c.Key("toggle-details"), title, func() { *expanded = !*expanded }).
					PaddingX(4).KeepFocus().Expanded(*expanded)
			}
		})
		if hasDetails {
			mobileDisclosureBody(c.Key("failure-details"), *expanded, func(body *ui.Context) {
				ui.Scroll(body).FillWidth().MaxHeight(112).Clip().Label("发送失败详情").Children(func() {
					label(body, failure.Details, captionTextSize, p.secondary).FillWidth().Selectable()
				})
			})
		}
	})
}
