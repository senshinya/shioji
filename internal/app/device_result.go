package app

import (
	"encoding/json"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func (m *Model) sessionOnOtherDevice(key, id string) bool {
	for other, state := range m.DeviceStates {
		if other == key {
			continue
		}
		for _, s := range state.Snapshot.Sessions {
			if s.ID == id {
				return true
			}
		}
	}
	return false
}
func (m *Model) validateSearchResults(key string, results []domain.SearchResult) error {
	state := m.DeviceStates[key]
	if state == nil {
		return errors.New("搜索设备已断开")
	}
	for _, r := range results {
		owned := false
		for _, w := range state.Snapshot.Workspaces {
			if w.ID == r.WorkspaceID {
				owned = true
				break
			}
		}
		if !owned || m.sessionOnOtherDevice(key, r.SessionID) {
			return errors.New("搜索结果不属于查询设备")
		}
	}
	return nil
}

// Successful RPC receipts are authoritative only inside the requested device
// and operation. Check them before a UI callback can change drafts or config.
func (m *Model) validateDeviceReply(key string, job Job, body json.RawMessage) error {
	state := m.DeviceStates[key]
	if state == nil {
		return errors.New("设备连接已改变，请重新核对操作")
	}
	if in, ok := job.Input.(protocol.SignInInput); ok {
		var result domain.SignInStatus
		if json.Unmarshal(body, &result) != nil || result.ID != in.ID || result.HarnessID != in.HarnessID {
			return errors.New("登录回执与请求不一致")
		}
	}
	fail := errors.New("设备回执与请求的工作区、会话或操作不一致")
	if in, ok := job.Input.(domain.ContextActionRequest); ok {
		var result domain.ContextActionResult
		if json.Unmarshal(body, &result) != nil || result.Operation.ID != in.OperationID || result.Operation.SessionID != in.SessionID || result.Operation.Kind != "compact" || result.Session.ID != in.SessionID || result.Session.Context.DeviceID != state.Snapshot.DeviceID || m.sessionOnOtherDevice(key, in.SessionID) {
			return fail
		}
		owned := false
		for _, s := range state.Snapshot.Sessions {
			owned = owned || s.ID == in.SessionID && s.WorkspaceID == result.Session.WorkspaceID
		}
		if !owned {
			return fail
		}
	}
	if job.Method == "session.work.send" || job.Method == "session.work.operation" {
		in, ok := job.Input.(domain.WorkInput)
		if !ok {
			return fail
		}
		var op domain.WorkInputOperation
		if json.Unmarshal(body, &op) != nil || op.Input != in || m.sessionOnOtherDevice(key, in.SessionID) {
			return fail
		}
		owned := false
		for _, session := range state.Snapshot.Sessions {
			owned = owned || session.ID == in.SessionID
		}
		if !owned {
			return fail
		}
	}
	if job.Method == "session.fork" || job.Method == "session.restart" || job.Method == "session.history-operation" || job.Method == "session.cancel-history" {
		var id, sid string
		switch in := job.Input.(type) {
		case domain.HistoryChange:
			id, sid = in.OperationID, in.SessionID
		case protocol.HistoryOperationQuery:
			id, sid = in.ID, in.SessionID
		default:
			return fail
		}
		var op domain.HistoryOperation
		if err := json.Unmarshal(body, &op); err != nil {
			return err
		}
		if op.ID != id || op.Source.ID != sid || op.Input.SessionID != sid || op.Session.ID == "" || op.Source.Context.DeviceID != state.Snapshot.DeviceID || op.Session.Context.DeviceID != state.Snapshot.DeviceID || op.Session.WorkspaceID != op.Source.WorkspaceID || m.sessionOnOtherDevice(key, op.Session.ID) {
			return fail
		}
		owned := false
		for _, source := range state.Snapshot.Sessions {
			if source.ID == sid && source.WorkspaceID == op.Source.WorkspaceID {
				owned = true
			}
		}
		if !owned || op.Input.Restart && op.Session.ID != sid || !op.Input.Restart && (op.Session.ID == sid || op.Session.ForkedFrom != sid) {
			return fail
		}
		if in, ok := job.Input.(domain.HistoryChange); ok && !op.Input.Equal(in) {
			return fail
		}
	}
	if in, ok := job.Input.(protocol.RecoveryInput); ok {
		var result protocol.RecoveryResult
		if err := json.Unmarshal(body, &result); err != nil {
			return err
		}
		if result.SessionID != in.SessionID || m.sessionOnOtherDevice(key, result.SessionID) {
			return fail
		}
		owned := false
		for _, s := range state.Snapshot.Sessions {
			if s.ID == in.SessionID {
				owned = true
			}
		}
		if !owned {
			return fail
		}
	}

	if in, ok := job.Input.(protocol.SessionChangeInput); ok {
		var session domain.Session
		if err := json.Unmarshal(body, &session); err != nil {
			return err
		}
		if session.ID != in.SessionID || m.sessionOnOtherDevice(key, session.ID) {
			return fail
		}
		owned := false
		for _, old := range state.Snapshot.Sessions {
			if old.ID == session.ID && old.WorkspaceID == session.WorkspaceID {
				owned = true
			}
		}
		if !owned {
			return fail
		}
	}
	if job.Method == "workspace.update" || job.Method == "workspace.archive" {
		var w domain.Workspace
		if err := json.Unmarshal(body, &w); err != nil {
			return err
		}
		expected := ""
		switch in := job.Input.(type) {
		case domain.Workspace:
			expected = in.ID
		case protocol.WorkspaceArchiveInput:
			expected = in.WorkspaceID
		}
		if w.ID != expected || w.DeviceID != state.Snapshot.DeviceID {
			return fail
		}
	}
	var submission *domain.Submission
	switch in := job.Input.(type) {
	case domain.Submission:
		submission = &in
	case protocol.SteerInput:
		submission = &in.Input
	}
	if submission != nil {
		var op domain.Operation
		if err := json.Unmarshal(body, &op); err != nil {
			return err
		}
		in := submission
		if op.ID != in.OperationID || op.SessionID != "" && (m.sessionOnOtherDevice(key, op.SessionID) || in.SessionID != "" && op.SessionID != in.SessionID) || op.State == "confirmed" && op.SessionID == "" {
			return fail
		}
		if owner := m.PendingDevices[op.ID]; owner != "" && owner != key {
			return fail
		}
		owned := false
		for _, w := range state.Snapshot.Workspaces {
			if w.ID == in.WorkspaceID {
				owned = true
			}
		}
		if !owned {
			return fail
		}
	}
	if in, ok := job.Input.(protocol.QueueInput); ok {
		var queue domain.QueueState
		if err := json.Unmarshal(body, &queue); err != nil {
			return err
		}
		if queue.SessionID != in.SessionID || m.sessionOnOtherDevice(key, queue.SessionID) {
			return fail
		}
	}
	if job.Method == "workspace.create" {
		var w domain.Workspace
		if err := json.Unmarshal(body, &w); err != nil {
			return err
		}
		if w.ID == "" || w.DeviceID != state.Snapshot.DeviceID {
			return fail
		}
		for other, s := range m.DeviceStates {
			if other == key {
				continue
			}
			for _, old := range s.Snapshot.Workspaces {
				if old.ID == w.ID {
					return fail
				}
			}
		}
	}
	return nil
}
