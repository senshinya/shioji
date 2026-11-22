package main

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/egoist/mygo"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type mobilePreviewReload struct {
	request     domain.SessionReloadRequest
	loseReceipt bool
}

func (a *mobileApp) seedPreviewReload(sid, board string) {
	a.previewReload = &mobilePreviewReload{loseReceipt: board == "session-reload-receipt"}
	a.seedArtboardConversation(false)
	m := a.model
	d := m.Details[sid]
	d.Session.ThreadID, d.Session.Context.DeviceID, d.Session.Revision = "preview-reload-thread", m.Snapshot.DeviceID, 7
	d.Session.Title, d.Session.Status = "核对会话重新加载", domain.ExecutionCompleted
	prompt, reply := d.Items[0], d.Items[4]
	prompt.Text = "整理重连测试结果。"
	reply.Text = "已核对重连。保留这段会话的历史和停发的队列。"
	d.Items, d.Turns, d.Works = []domain.Item{prompt, reply}, d.Turns[:1], nil
	d.Queue.Paused = board != "session-reload-busy"
	m.Details[sid], m.Snapshot.Sessions[0] = d, d.Session
	m.QueueExpanded[sid] = true
	m.WorkerMinor, m.Readiness.Capabilities.Reload = protocol.Minor, true
	if board == "session-reload-legacy" {
		m.WorkerMinor = protocol.SessionReloadMinor - 1
	}
}

func (a *mobileApp) previewSessionReload(method string, in domain.SessionReloadRequest, done func(json.RawMessage, error)) {
	fixture := a.previewReload
	if fixture == nil {
		done(nil, &protocol.RemoteError{Code: "notSent", Message: "此预览缺少可重新加载的运行环境"})
		return
	}
	query := method == "session.reload.operation"
	if query && fixture.request != in {
		done(nil, &protocol.RemoteError{Code: "notFound", Message: "找不到原操作"})
		return
	}
	if !query && fixture.loseReceipt && fixture.request.OperationID != "" {
		done(nil, &protocol.RemoteError{Code: "notSent", Message: "重新加载操作已存在"})
		return
	}
	fixture.request = in
	session := a.sessionByID(in.SessionID)
	time.AfterFunc(350*time.Millisecond, func() {
		mygo.RunOnMain(func() {
			if !query && fixture.loseReceipt {
				done(nil, errors.New("连接在收到回执前中断"))
				return
			}
			result := domain.SessionReloadResult{Operation: domain.SessionReloadOperation{Input: in, State: "completed"}, Session: session}
			data, err := json.Marshal(result)
			done(data, err)
		})
	})
}
