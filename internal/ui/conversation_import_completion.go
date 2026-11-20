package ui

import (
	"encoding/json"
	"errors"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type conversationImportCompletion struct {
	session, key, epoch string
	busy                bool
	error               string
}

func (v *View) importedConversationStatus() *conversationStatus {
	m := v.Model
	if m.ArchivedContext() != "" {
		return nil
	}
	for _, s := range m.Snapshot.Sessions {
		if s.ID != m.SessionID || !s.ReadOnlyHistory() {
			continue
		}
		status := &conversationStatus{id: "import:" + s.ID, title: "只读会话", body: s.Origin.Reason, icon: modelEye, priority: 2}
		if status.body == "" {
			status.body = "此导入历史仅可查看，暂不能继续发送。"
		}
		label, checking := "重新核对历史", "正在核对历史"
		if s.Origin.Truncated {
			label, checking = "补全历史", "正在补全历史"
			status.body = "历史尚未完整读取，补全为来源的当前完整历史后即可继续。会话名称和草稿保留。"
		}
		if m.WorkerMinor < 42 {
			update := "请先更新所属设备的执行服务，再核对历史。"
			if s.Origin.Truncated {
				status.body = update
			} else {
				status.details = update
			}
			status.actions = []statusAction{{label: "设备详情", run: func(*n.Context) { v.showFeedbackDevice() }}}
			return status
		}
		busy := false
		if state := v.importCompletion; state != nil && state.session == s.ID {
			busy, status.details = state.busy, state.error
		}
		if busy {
			status.title = checking
		}
		status.actions = []statusAction{{label: label, disabled: busy || !m.Connected || v.Controller == nil, run: func(*n.Context) { v.completeImportedConversation(s) }}}
		return status
	}
	return nil
}

func (v *View) completeImportedConversation(session domain.Session) {
	m := v.Model
	if v.Controller == nil || !m.Connected || m.WorkerMinor < 42 || v.importCompletion != nil && v.importCompletion.busy {
		return
	}
	var workspace domain.Workspace
	for _, w := range m.Snapshot.Workspaces {
		if w.ID == session.WorkspaceID {
			workspace = w
			break
		}
	}
	if workspace.ID == "" || workspace.Archived || session.Origin == nil {
		return
	}
	state := &conversationImportCompletion{session: session.ID, key: m.ConnectionID(), epoch: m.ConnectionEpoch, busy: true}
	v.importCompletion = state
	in := protocol.ConversationImportInput{ConversationQuery: protocol.ConversationQuery{WorkspaceID: workspace.ID, Revision: workspace.Revision, HarnessID: session.HarnessID, SourceRef: session.Origin.SourceRef, Directory: session.Context.CWD}, SessionID: session.ID, SessionRevision: session.Revision}
	if !v.do(app.Job{ConnectionID: state.key, Method: "conversation.import", Input: in, Apply: func(m *app.Model, b json.RawMessage, err error) {
		state.busy = false
		if v.importCompletion != state {
			return
		}
		// Journal events own history replacement. A late response cannot switch
		// devices, overwrite a new connection's state, or submit the saved draft.
		if v.Model.ConnectionID() != state.key || v.Model.ConnectionEpoch != state.epoch {
			state.error = "设备连接已改变，请重新核对历史"
			return
		}
		var result domain.Session
		if err == nil && (json.Unmarshal(b, &result) != nil || result.ID != session.ID || result.WorkspaceID != workspace.ID || result.HarnessID != session.HarnessID || result.Origin == nil || result.Origin.SourceRef != session.Origin.SourceRef || result.Origin.Truncated || result.Origin.ResumeRef == "") {
			err = errors.New("设备未确认历史已完整补全")
		}
		if err != nil {
			state.error = err.Error()
			return
		}
		if m.SessionID == session.ID {
			v.Controller.SelectDevice(state.key, session.ID)
		}
	}}) {
		state.busy, state.error = false, "设备操作繁忙，请重试"
	}
}
