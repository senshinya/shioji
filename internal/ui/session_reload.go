package ui

import (
	"encoding/json"
	"errors"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type sessionReloadUI struct {
	in         domain.SessionReloadRequest
	busy       bool
	queryAfter time.Time
}

func (v *View) sessionReloadReason(s domain.Session) string {
	m := v.persistenceModel()
	key := m.WorkspaceConnection(s.WorkspaceID)
	if !v.splitRoot().canManage(key) {
		return "设备未连接"
	}
	minor := m.WorkerMinor
	if state := m.DeviceStates[key]; state != nil {
		minor = state.WorkerMinor
	}
	if minor < 45 {
		return "需要更新执行服务"
	}
	if !v.historyReady(s) || !m.SessionReadiness(s).Capabilities.Reload || v.historyBusy[s.ID] {
		return "当前会话暂不可重新加载"
	}
	for _, w := range m.Snapshot.Workspaces {
		if w.ID == s.WorkspaceID && w.Archived {
			return "工作区已归档"
		}
	}
	if d, ok := m.Details[s.ID]; ok {
		for _, r := range d.Requests {
			if r.State.AwaitingResolution() {
				return "请先处理待回答问题或授权"
			}
		}
		for _, turn := range d.Turns {
			if turn.Status.Active() || turn.Status == "unknown" {
				return "请等待当前轮次结束"
			}
		}
		for _, work := range d.Works {
			if work.Active() {
				return "请等待后台任务结束"
			}
		}
		if len(d.Queue.Items) > 0 && !d.Queue.Paused {
			return "请先暂停待发队列"
		}
	}
	if s.ContextAction != nil && (s.ContextAction.State.Active() || s.ContextAction.State == "unknown") {
		return "请等待上下文操作结束"
	}
	return ""
}

func (v *View) sessionReloadMenu(menu *n.Menu, s domain.Session) {
	root := v.splitRoot()
	pending := root.sessionReloads[s.ID]
	if pending != nil && !pending.busy && time.Now().After(pending.queryAfter) && v.splitRoot().canManage(v.persistenceModel().WorkspaceConnection(s.WorkspaceID)) {
		v.sendSessionReload(s, pending, true)
	}
	if menu.Item("重新加载").Disabled(v.sessionReloadReason(s) != "" || pending != nil).Chosen() {
		if root.sessionReloads == nil {
			root.sessionReloads = map[string]*sessionReloadUI{}
		}
		pending = &sessionReloadUI{in: domain.SessionReloadRequest{OperationID: domain.NewID(), SessionID: s.ID, Revision: s.Revision}}
		root.sessionReloads[s.ID] = pending
		v.sendSessionReload(s, pending, false)
	}
}

func (v *View) sendSessionReload(s domain.Session, pending *sessionReloadUI, query bool) {
	pending.busy = true
	method := "session.reload"
	if query {
		method = "session.reload.operation"
	}
	key := v.persistenceModel().WorkspaceConnection(s.WorkspaceID)
	root := v.splitRoot()
	if !v.do(app.Job{ConnectionID: key, Method: method, Input: pending.in, Apply: func(m *app.Model, b json.RawMessage, err error) {
		if root.sessionReloads[s.ID] != pending {
			return
		}
		pending.busy = false
		pending.queryAfter = time.Now().Add(3 * time.Second)
		if err != nil {
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && (remote.Code == "notSent" || query && remote.Code == "notFound") {
				delete(root.sessionReloads, s.ID)
			}
			m.ReportFeedback(app.Feedback{ID: pending.in.OperationID, ConnectionID: key, SessionID: s.ID, Kind: app.FeedbackFailure, Title: "重新加载未完成", Body: err.Error()})
			return
		}
		var result domain.SessionReloadResult
		if json.Unmarshal(b, &result) != nil || result.Operation.Input != pending.in || result.Session.ID != s.ID || result.Session.ThreadID != s.ThreadID || result.Session.WorkspaceID != s.WorkspaceID {
			m.ReportFeedback(app.Feedback{ID: pending.in.OperationID, ConnectionID: key, SessionID: s.ID, Kind: app.FeedbackFailure, Title: "重新加载结果待核对"})
			return
		}
		if result.Operation.State == "running" {
			return
		}
		delete(root.sessionReloads, s.ID)
		for i, old := range m.Snapshot.Sessions {
			if old.ID == s.ID && old.Revision <= result.Session.Revision {
				m.Snapshot.Sessions[i] = result.Session
			}
		}
		if d, ok := m.Details[s.ID]; ok && d.Session.Revision <= result.Session.Revision {
			d.Session = result.Session
			m.Details[s.ID] = d
		}
		kind, title, body := app.FeedbackSuccess, "会话已重新加载", ""
		if result.Operation.State != "completed" {
			kind, title, body = app.FeedbackFailure, "重新加载失败", result.Operation.Error
		}
		m.ReportFeedback(app.Feedback{ID: pending.in.OperationID, ConnectionID: key, SessionID: s.ID, Kind: kind, Title: title, Body: body})
	}}) {
		pending.busy = false
		pending.queryAfter = time.Now().Add(3 * time.Second)
	}
}
