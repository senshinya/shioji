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
	fail := errors.New("设备回执与请求的工作区、会话或操作不一致")
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
