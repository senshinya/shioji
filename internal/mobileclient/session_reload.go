package mobileclient

import (
	"slices"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type SessionReloadTarget struct {
	DeviceID    string `json:"deviceId"`
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
	ThreadID    string `json:"threadId"`
}

func ReloadTargetFor(deviceID string, session domain.Session) SessionReloadTarget {
	if deviceID == "" || session.Context.DeviceID != deviceID {
		return SessionReloadTarget{}
	}
	return SessionReloadTarget{DeviceID: deviceID, WorkspaceID: session.WorkspaceID, SessionID: session.ID, ThreadID: session.ThreadID}
}

func (t SessionReloadTarget) Valid() bool {
	return t.DeviceID != "" && t.WorkspaceID != "" && t.SessionID != "" && t.ThreadID != ""
}

// SessionReloadIntent survives disconnects so the client can read the original
// operation after relaunch, without rebuilding the runtime a second time.
type SessionReloadIntent struct {
	Target  SessionReloadTarget         `json:"target"`
	Request domain.SessionReloadRequest `json:"request"`
}

func (i SessionReloadIntent) Valid() bool {
	return i.Target.Valid() && i.Request.OperationID != "" && i.Request.SessionID == i.Target.SessionID && i.Request.Revision >= 0
}

func (i SessionReloadIntent) Accepts(r domain.SessionReloadResult) bool {
	return i.Valid() && r.Operation.Input == i.Request && i.Target == ReloadTargetFor(r.Session.Context.DeviceID, r.Session) &&
		r.Session.Revision >= i.Request.Revision &&
		(r.Operation.State == "running" || r.Operation.State == "completed" || r.Operation.State == "failed")
}

func SessionReloadReason(m *app.Model, session domain.Session) string {
	if !m.Connected {
		return "设备未连接"
	}
	if m.WorkerMinor < protocol.SessionReloadMinor {
		return "需要更新执行服务"
	}
	if session.Archived || session.ReadOnlyHistory() {
		return "当前会话仅可查看"
	}
	if !ReloadTargetFor(m.Snapshot.DeviceID, session).Valid() || !m.SessionReadiness(session).Capabilities.Reload {
		return "当前会话暂不可重新加载"
	}
	if session.ExecutionGuard != "" || session.ContextChanged || session.Status.NeedsRecovery() {
		return "请先核对会话执行状态"
	}
	workspace := slices.IndexFunc(m.Snapshot.Workspaces, func(w domain.Workspace) bool {
		return w.ID == session.WorkspaceID && w.DeviceID == m.Snapshot.DeviceID
	})
	if workspace < 0 {
		return "工作区尚未确认"
	}
	if m.Snapshot.Workspaces[workspace].Archived {
		return "工作区已归档"
	}
	detail := m.Details[session.ID]
	for _, requests := range [][]domain.PendingRequest{detail.Requests, m.Snapshot.PendingRequests} {
		if slices.ContainsFunc(requests, func(r domain.PendingRequest) bool {
			return r.SessionID == session.ID && r.State.AwaitingResolution()
		}) {
			return "请先处理待回答问题或授权"
		}
	}
	if session.ContextAction != nil && session.ContextAction.State.BlocksCheckout() {
		return "请等待上下文操作结束"
	}
	if !session.Status.CanSubmit() || slices.ContainsFunc(detail.Turns, func(t domain.Turn) bool { return t.Status.BlocksCheckout() }) {
		return "请等待当前轮次结束"
	}
	if slices.ContainsFunc(detail.Works, func(w domain.WorkActivity) bool { return w.Active() }) {
		return "请等待后台任务结束"
	}
	for _, input := range m.Pending {
		if input.SessionID == session.ID {
			return "请等待消息投递确认"
		}
	}
	if m.HistoryActions[session.ID].OperationID != "" {
		return "请等待历史操作结束"
	}
	if len(detail.Queue.Items) != 0 && !detail.Queue.Paused {
		return "请先暂停待发队列"
	}
	return ""
}
