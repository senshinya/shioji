package main

import (
	"encoding/json"
	"errors"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

const importCompletionMinor = 42

type mobileImportCompletion struct {
	model      *app.Model
	epoch      string
	generation string
	request    mobileRequest
	confirmed  bool
}

func (a *mobileApp) importedHistoryFooter(c *ui.Context, parent *mygo.Window, s domain.Session, detail domain.SessionDetail) bool {
	if s.Origin == nil || s.Archived || a.workspace(s.WorkspaceID).Archived {
		return false
	}
	m := a.model
	key := m.WorkKey(s.ID, "")
	slot := a.importCompletions[key]
	if slot != nil && (slot.model != m || slot.epoch != m.ConnectionEpoch || slot.generation != m.Snapshot.Generation || !s.ReadOnlyHistory()) {
		delete(a.importCompletions, key)
		slot = nil
	}

	title, description, failure := "只读会话", s.Origin.Reason, ""
	var next string
	var activate func()
	disabled := false
	switch {
	case !s.ReadOnlyHistory():
		if s.ThreadID != "" || detail.History == nil || !detail.History.Invalidated {
			return false
		}
		// The Worker journal owns history replacement. Re-read the invalidated
		// window on its device, including when another client completed it.
		title, description = "正在读取完整历史…", ""
		load := func() {
			detail.Session = s
			a.readTranscriptHistory(s.ID, detail, domain.HistoryQuery{})
		}
		read := m.HistoryReads[s.ID]
		if read == nil && m.Connected && a.controller != nil {
			load()
		}
		if !m.Connected {
			title, description = "完整历史待同步", "连接所属设备后继续读取。"
		} else if read != nil && read.Error != "" {
			title, failure = "完整历史待同步", read.Error
			next, activate = "重新读取", load
		}
	case !s.Origin.Truncated:
		if description == "" {
			description = "此导入历史仅可查看。"
		}
	case m.WorkerMinor > 0 && m.WorkerMinor < importCompletionMinor:
		description = "此会话保留了部分历史。更新所属设备的执行服务后即可补全。"
		next, activate = "设备详情", func() { a.openDevice(parent) }
	default:
		description = "此会话保留了部分历史。补全后保留原会话名称和草稿。"
		next, activate = "补全历史", func() { a.completeImportedHistory(parent, s.ID) }
		disabled = !m.Connected || a.controller == nil
		if slot != nil {
			failure = slot.request.err
			if slot.request.busy || slot.confirmed {
				title, disabled = "正在补全历史…", true
			}
		}
	}
	c = c.Key("imported-history")
	mobileContent(c, conversationComposerWidth).Padding(4, conversationComposerInset, 8).Transition(mobilePanelTransition).Children(func() {
		ui.Column(c).FillWidth().Padding(1, 11, 2).Radius(cardRadius).Background(palette(c).inset).Children(func() {
			remoteStatus(c, readOnlySymbol, title, description)
			if failure != "" {
				label(c, failure, captionTextSize, palette(c).warning).Padding(0, 0, 2, 24)
			}
			if activate != nil {
				action(c, next, activate).Disabled(disabled).AlignSelf(ui.Start).Margin(0, 0, 0, 12)
			}
		})
	})
	return true
}

func (a *mobileApp) completeImportedHistory(parent *mygo.Window, sid string) {
	m, controller := a.model, a.controller
	s := a.sessionByID(sid)
	w := a.workspace(s.WorkspaceID)
	if controller == nil || !m.Connected || m.WorkerMinor < importCompletionMinor || !a.pageCurrent(parent) ||
		s.ID == "" || !s.ReadOnlyHistory() || !s.Origin.Truncated || s.Archived || w.ID == "" || w.Archived {
		return
	}
	key := m.WorkKey(s.ID, "")
	if old := a.importCompletions[key]; old != nil && old.model == m && old.epoch == m.ConnectionEpoch &&
		old.generation == m.Snapshot.Generation && (old.request.busy || old.confirmed) {
		return
	}
	state := &mobileImportCompletion{model: m, epoch: m.ConnectionEpoch, generation: m.Snapshot.Generation,
		request: mobileRequest{busy: true}}
	if a.importCompletions == nil {
		a.importCompletions = make(map[app.WorkKey]*mobileImportCompletion)
	}
	a.importCompletions[key] = state
	in := protocol.ConversationImportInput{
		ConversationQuery: protocol.ConversationQuery{WorkspaceID: w.ID, Revision: w.Revision, HarnessID: s.HarnessID,
			SourceRef: s.Origin.SourceRef, Directory: s.Context.CWD},
		SessionID: s.ID, SessionRevision: s.Revision,
	}
	if !controller.Do(app.Job{ConnectionEpoch: state.epoch, Method: "conversation.import", Input: in,
		Apply: func(current *app.Model, data json.RawMessage, err error) {
			if a.model != m || current != m || a.controller != controller || m.ConnectionEpoch != state.epoch ||
				m.Snapshot.Generation != state.generation || a.importCompletions[key] != state {
				return
			}
			state.request.busy = false
			var result domain.Session
			if err == nil && (json.Unmarshal(data, &result) != nil || result.ID != s.ID ||
				result.WorkspaceID != s.WorkspaceID || result.HarnessID != s.HarnessID ||
				result.Origin == nil || result.Origin.SourceRef != s.Origin.SourceRef || result.Origin.Truncated || result.Origin.ResumeRef == "") {
				err = errors.New("设备未确认历史已完整补全")
			}
			if err != nil {
				state.request.err = err.Error()
			} else {
				state.confirmed = true
			}
			a.refresh()
		},
	}) {
		state.request = mobileRequest{err: "设备忙，请重试"}
	}
	a.refresh()
}
