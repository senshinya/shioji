package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"time"
)

var forkIcon = svg("M6 6v3a4 4 0 0 0 4 4v1M14 6v3a4 4 0 0 1-4 4M6 2a2 2 0 1 0 0 4a2 2 0 1 0 0-4M14 2a2 2 0 1 0 0 4a2 2 0 1 0 0-4M10 14a2 2 0 1 0 0 4a2 2 0 1 0 0-4")

func (v *View) historyReady(s domain.Session) bool {
	m := v.persistenceModel()
	if !v.splitRoot().canManage(m.WorkspaceConnection(s.WorkspaceID)) || s.Archived || s.ContextChanged || s.ExecutionGuard != "" || s.ThreadID == "" {
		return false
	}
	state := m.DeviceStates[m.WorkspaceConnection(s.WorkspaceID)]
	minor := m.WorkerMinor
	if state != nil {
		minor = state.WorkerMinor
	}
	if minor < 9 {
		return false
	}
	switch s.Status {
	case "idle", "completed", "failed", "interrupted":
		return true
	}
	return false
}
func (v *View) beginHistoryEdit(item domain.Item) {
	if v.historyExit != nil || v.historyBusy[v.Model.SessionID] {
		return
	}
	v.collectBindings()
	if err := v.Model.BeginHistoryEdit(item); err != nil {
		v.Model.Error = err.Error()
		return
	}
	v.historyFocus = item.ID
}
func (v *View) forkSession(source domain.Session, turn string) {
	root := v.splitRoot()
	if v.Model.SessionID != source.ID && root.secondary != nil && root.secondary.Model.SessionID == source.ID {
		root.secondary.forkSession(source, turn)
		return
	}
	if !v.historyReady(source) || v.historyBusy[source.ID] || v.historyExit != nil && v.historyExit.in.SessionID == source.ID {
		return
	}
	if pending, ok := v.Model.HistoryActions[source.ID]; ok {
		v.historyCommand(pending, !v.historyForked[source.ID])
		return
	}
	in := domain.HistoryChange{OperationID: domain.NewID(), SessionID: source.ID, Revision: source.Revision, TurnID: turn}
	if v.Model.SessionID != source.ID || v.Model.Details[source.ID].Session.ID == "" {
		v.historyCommand(in, false)
		return
	}
	v.beginHistoryMotion(in)
}
func (v *View) restartHistory() {
	m := v.Model
	sid := m.SessionID
	edit := m.HistoryEdits[sid]
	if edit.ItemID == "" || v.historyBusy[sid] || v.historyExit != nil && v.historyExit.phase != historyDispatched || !m.Connected {
		return
	}
	if edit.OperationID != "" {
		pending := v.Model.HistoryActions[sid]
		if pending.OperationID == "" {
			pending = domain.HistoryChange{OperationID: edit.OperationID, SessionID: sid}
		}
		v.historyCommand(pending, !v.historyForked[sid])
		return
	}
	source := m.Details[sid].Session
	if !v.historyReady(source) {
		m.Error = "请先停止当前轮次并处理待答问题或执行状态"
		return
	}
	text := m.Drafts[m.HistoryDraftKey()]
	if strings.TrimSpace(text) == "" && !v.historyHasAttachments() {
		return
	}
	in := domain.HistoryChange{OperationID: domain.NewID(), SessionID: sid, Revision: source.Revision, ItemID: edit.ItemID, Restart: true, Text: text, Config: m.Config()}
	v.beginHistoryMotion(in)
}
func (v *View) historyHasAttachments() bool {
	edit := v.Model.HistoryEdits[v.Model.SessionID]
	for _, item := range v.Model.Details[v.Model.SessionID].Items {
		if item.ID == edit.ItemID {
			return len(item.Attachments) > 0
		}
	}
	return false
}
func (v *View) historyCommand(in domain.HistoryChange, query bool) {
	m := v.persistenceModel()
	source := domain.Session{}
	for _, s := range m.Snapshot.Sessions {
		if s.ID == in.SessionID {
			source = s
			break
		}
	}
	if source.ID == "" {
		m.Error = "源会话已不可用"
		return
	}
	key := m.WorkspaceConnection(source.WorkspaceID)
	method := "session.fork"
	var payload any = in
	if in.Restart {
		method = "session.restart"
	}
	if query {
		method = "session.history-operation"
		payload = protocol.HistoryOperationQuery{ID: in.OperationID, SessionID: in.SessionID}
	}
	if v.historyBusy == nil {
		v.historyBusy = map[string]bool{}
	}
	v.historyBusy[in.SessionID] = true
	if v.historyChecked == nil {
		v.historyChecked = map[string]string{}
	}
	epoch := m.ConnectionEpoch
	if state := m.DeviceStates[key]; state != nil {
		epoch = state.ConnectionEpoch
	}
	v.historyChecked[in.OperationID] = epoch
	if !query {
		m.HistoryActions[in.SessionID] = in
	}
	var prepare func() error
	if v.splitRoot().Saver != nil {
		v.splitRoot().queueState()
		prepare = v.splitRoot().Saver.FlushLatest
	}
	if !v.splitRoot().do(app.Job{ConnectionID: key, Method: method, Input: payload, Prepare: prepare, Apply: func(model *app.Model, b json.RawMessage, err error) {
		v.historyBusy[in.SessionID] = false
		if err != nil {
			v.historyExit = nil
			model.Error = err.Error()
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && (remote.Code == "notSent" || query && remote.Code == "notFound") {
				edit := model.HistoryEdits[in.SessionID]
				edit.OperationID = ""
				delete(model.HistoryActions, in.SessionID)
				if edit.ItemID != "" {
					model.HistoryEdits[in.SessionID] = edit
				}
			} else if !query {
				model.Error += "；草稿已保留，可核对重开结果"
			}
			return
		}
		var op domain.HistoryOperation
		if err = json.Unmarshal(b, &op); err != nil {
			model.Error = err.Error()
			return
		}
		if op.ID != in.OperationID || op.Input.SessionID != source.ID || op.Source.WorkspaceID != source.WorkspaceID || op.Session.WorkspaceID != source.WorkspaceID || op.Session.Context.DeviceID != source.Context.DeviceID {
			model.Error = "历史操作回执与来源不一致"
			return
		}
		if op.State == "failed" {
			v.historyExit = nil
			delete(model.HistoryActions, in.SessionID)
			model.Error = op.Error
			edit := model.HistoryEdits[in.SessionID]
			edit.OperationID = ""
			if edit.ItemID != "" {
				model.HistoryEdits[in.SessionID] = edit
			}
			return
		}
		if op.State == "forked" {
			v.historyExit = nil
			if v.historyForked == nil {
				v.historyForked = map[string]bool{}
			}
			v.historyForked[in.SessionID] = true
			model.HistoryActions[in.SessionID] = op.Input
			model.Error = "分叉已保存，可以继续完成本次操作"
			return
		}
		if op.State != "complete" {
			v.historyExit = nil
			model.Error = "重开结果尚未确认，原会话保持不变"
			return
		}
		delete(v.historyForked, in.SessionID)
		if op.Input.Restart {
			model.Error = ""
			return // Confirmed ordinary operation events retire the edit draft.
		}
		delete(model.HistoryActions, in.SessionID)
		// RPC completion may beat the independent watch stream. Admit the
		// validated receipt to the owning device catalog, which builds the
		// aggregate on every frame, without moving its journal cursor.
		catalog := &model.Snapshot.Sessions
		if device := model.DeviceStates[key]; device != nil {
			catalog = &device.Snapshot.Sessions
		}
		found := false
		for _, s := range *catalog {
			found = found || s.ID == op.Session.ID
		}
		if !found {
			*catalog = append(*catalog, op.Session)
		}
		model.RefreshDevices()
		root := v.splitRoot()
		if !query && root.focusedSessionID() == source.ID {
			// The worker confirmed this exact prefix. Mount it immediately while
			// the normal authoritative session hydration catches up.
			v.seedHistoryFork(op)
			if v.historyExit != nil {
				v.historyExit.targetSession = op.Session.ID
				v.historyExit.targetThread = op.Session.ThreadID
			}
			root.navigateSession(op.Session.ID, op.Session.WorkspaceID)
			root.focusComposer = true
			root.sessionEntry = &sessionEntryMotion{session: op.Session.ID, operation: op.ID, workspace: op.Session.WorkspaceID, epoch: model.ConnectionEpoch}
		} else {
			v.historyExit = nil
		}
	}}) {
		v.historyBusy[in.SessionID] = false
		v.historyExit = nil
		delete(m.HistoryActions, in.SessionID)
		m.Error = "操作繁忙，请重试"
		if in.Restart {
			edit := m.HistoryEdits[in.SessionID]
			edit.OperationID = ""
			m.HistoryEdits[in.SessionID] = edit
		}
	}
}

// Reconnection only queries durable history intents; it never replays mutations.
func (v *View) syncHistoryActions() {
	if v.parent != nil || v.Controller == nil {
		return
	}
	for sid, in := range v.Model.HistoryActions {
		if v.historyBusy[sid] {
			continue
		}
		var source domain.Session
		for _, s := range v.Model.Snapshot.Sessions {
			if s.ID == sid {
				source = s
				break
			}
		}
		key := v.Model.WorkspaceConnection(source.WorkspaceID)
		epoch := v.Model.ConnectionEpoch
		connected := v.Model.Connected
		if state := v.Model.DeviceStates[key]; state != nil {
			epoch, connected = state.ConnectionEpoch, state.Connected
		}
		if !connected || epoch == "" || v.historyChecked[in.OperationID] == epoch {
			continue
		}
		v.historyCommand(in, true)
	}
}

func (v *View) historyOperationNotice(c *n.Context, t *n.Theme) {
	sid := v.Model.SessionID
	if motion := v.historyExit; motion != nil && motion.in.SessionID == sid && (motion.phase != historyDispatched || v.historyBusy[sid]) {
		return
	}
	in, ok := v.Model.HistoryActions[sid]
	if v.historyEditorVisible() {
		return
	}
	edit := v.Model.HistoryEdits[sid]
	if !ok && edit.ItemID != "" && edit.OperationID == "" {
		n.Row(c.Key("history-detached-draft")).MaxWidth(768).FillWidth().Gap(12).AlignItems(n.Baseline).Transition(disclosureMotion(c, 200*time.Millisecond)).Children(func() {
			n.Text(c, "原消息已被替换，修改稿已保留").FontSize(12).TextColor(t.TextMuted).Grow(1)
			copy := ghost(c, "复制修改稿").FontSize(12).Height(28)
			copied := n.Local(copy, "copied", func() time.Time { return time.Time{} })
			if copy.Clicked() {
				c.WriteClipboard(v.Model.Drafts[v.Model.HistoryDraftKey()])
				*copied = c.Now().Add(2 * time.Second)
			}
			if c.Now().Before(*copied) {
				copy.Tooltip("已复制")
			}
			if ghost(c, "放弃编辑").Label("放弃历史编辑").FontSize(12).Height(28).Clicked() {
				delete(v.Model.Drafts, v.Model.HistoryDraftKey())
				v.Model.CancelHistoryEdit()
			}
		})
		return
	}
	if !ok {
		return
	}
	n.Row(c.Key("history-operation-notice")).MaxWidth(768).FillWidth().Gap(12).AlignItems(n.Baseline).Transition(disclosureMotion(c, 200*time.Millisecond)).Children(func() {
		text, action := "会话操作结果待核对", "核对结果"
		if v.historyForked[sid] {
			text, action = "分叉已保存", "继续完成"
		}
		n.Text(c, text).FontSize(12).TextColor(t.TextMuted).Grow(1)
		if ghost(c, action).FontSize(12).Height(28).Disabled(v.historyBusy[sid] || !v.Model.Connected).Clicked() {
			v.historyCommand(in, !v.historyForked[sid])
		}
		if ghost(c, "取消本次操作").FontSize(12).Height(28).Disabled(v.historyBusy[sid] || !v.Model.Connected).Clicked() {
			v.cancelHistoryAction(in)
		}
	})
}
func (v *View) cancelHistoryAction(in domain.HistoryChange) {
	v.historyBusy[in.SessionID] = true
	if !v.do(app.Job{Method: "session.cancel-history", Input: protocol.HistoryOperationQuery{ID: in.OperationID, SessionID: in.SessionID}, Apply: func(m *app.Model, b json.RawMessage, err error) {
		v.historyBusy[in.SessionID] = false
		if err != nil {
			m.Error = err.Error()
			return
		}
		var op domain.HistoryOperation
		if err = json.Unmarshal(b, &op); err != nil {
			m.Error = err.Error()
			return
		}
		if op.ID != in.OperationID || op.Input.SessionID != in.SessionID {
			m.Error = "历史操作回执与来源不一致"
			return
		}
		delete(m.HistoryActions, in.SessionID)
		delete(v.historyForked, in.SessionID)
		if op.State == "complete" {
			m.Error = "操作已完成，已接纳的输入保持原状态"
			return
		}
		edit := m.HistoryEdits[in.SessionID]
		edit.OperationID = ""
		if edit.ItemID != "" {
			m.HistoryEdits[in.SessionID] = edit
		}
		m.Error = ""
	}}) {
		v.historyBusy[in.SessionID] = false
	}
}
