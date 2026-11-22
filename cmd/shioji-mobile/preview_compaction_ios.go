package main

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/egoist/mygo"

	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

type mobilePreviewCompaction struct {
	loseReceipt bool
	failFirst   bool
	request     domain.ContextActionRequest
}

func (a *mobileApp) seedPreviewCompaction(id, board string) {
	a.previewCompaction = &mobilePreviewCompaction{loseReceipt: board == "context-compaction-receipt", failFirst: board == "context-compaction"}
	m := a.model
	m.WorkerMinor = protocol.Minor
	m.Readiness.Capabilities.Compact = true
	detail := m.Details[id]
	session := &detail.Session
	session.Title = "核对长期会话上下文"
	session.ThreadID = "preview-context-thread"
	session.Context.DeviceID = m.Snapshot.DeviceID
	session.Status = domain.ExecutionCompleted
	session.ContextUsage = &domain.ContextUsage{TurnID: detail.Turns[0].ID, UsedTokens: new(int64(40000)), LimitTokens: new(int64(100000))}
	prompt, reply := detail.Items[0], detail.Items[4]
	prompt.Text = "整理本轮的重连测试结果。"
	reply.Text = "已验证断线重连。会话保留原消息与草稿，执行状态从 Worker 恢复。"
	detail.Items = []domain.Item{prompt, reply}
	if board == "context-compaction-auto" {
		session.Status = domain.ExecutionRunning
		detail.Turns[1].StartedAt = new(time.Now())
		detail.Items = append(detail.Items, domain.Item{ID: "preview-auto-compaction", Kind: "activity", Presentation: "compaction",
			TurnID: detail.Turns[1].ID, ActivityOrder: 10, Status: "running", Name: "正在压缩上下文"})
	} else {
		detail.Turns = detail.Turns[:1]
	}
	if board == "context-compaction-history" || board == "context-compaction-order" {
		const operation = "preview-prior-compaction"
		session.ContextAction = &domain.ContextAction{OperationID: operation, SourceRef: session.ThreadID,
			TurnID: "preview-prior-maintenance", State: domain.ExecutionCompleted}
		prompt.Order, reply.Order = 10, 13
		reply.Text = "后续回复留在原来的位置。"
		detail.Items = []domain.Item{prompt, reply}
		if board == "context-compaction-history" {
			detail.History = &domain.HistoryWindow{First: 10, Last: 13, Older: true}
		} else {
			command := domain.Item{ID: "context-command:" + operation, TurnID: session.ContextAction.TurnID,
				Kind: "command", Presentation: "compact", Name: "压缩上下文", Order: 11, Final: true}
			detail.Items = []domain.Item{prompt, command, reply}
		}
	}
	detail.Plan, detail.Works, detail.Requests, detail.Queue.Items = nil, nil, nil, nil
	m.Details[id] = detail
	m.Snapshot.Sessions[0] = *session
}

func (a *mobileApp) previewContextOperation(method string, in domain.ContextActionRequest, done func(json.RawMessage, error)) {
	fixture := a.previewCompaction
	if fixture == nil {
		done(nil, &protocol.RemoteError{Code: "notSent", Message: "此预览会话没有压缩运行环境"})
		return
	}
	query := method == "session.context.operation"
	if query && fixture.request != in {
		done(nil, &protocol.RemoteError{Code: "notFound", Message: "找不到原压缩操作"})
		return
	}
	fixture.request = in
	m := a.model
	time.AfterFunc(300*time.Millisecond, func() {
		mygo.RunOnMain(func() {
			detail := m.Details[in.SessionID]
			now := time.Now()
			if !query {
				detail.Items = append(detail.Items, domain.Item{ID: "context-command:" + in.OperationID, Kind: "command",
					Presentation: "compact", Name: "压缩上下文", Final: true, SentAt: &now})
			}
			detail.Session.ContextAction = &domain.ContextAction{OperationID: in.OperationID, RequestRevision: in.Revision,
				SourceRef: detail.Session.ThreadID, State: domain.ExecutionUnknown, PreviousStatus: domain.ExecutionCompleted}
			if !query && fixture.loseReceipt {
				fixture.loseReceipt = false
				detail.Session.Status = domain.ExecutionUnknown
				a.publishPreviewCompaction(detail)
				done(nil, errors.New("压缩结果尚未确认"))
				return
			}
			turnID := "preview-compaction:" + in.OperationID
			detail.Session.ContextAction.TurnID = turnID
			detail.Session.ContextAction.State = domain.ExecutionRunning
			detail.Session.Status = domain.ExecutionRunning
			detail.Turns = append(detail.Turns, domain.Turn{ID: turnID, Presentation: "compaction", StartedAt: &now, Status: domain.ExecutionRunning})
			detail.Items = append(detail.Items, domain.Item{ID: turnID, TurnID: turnID, Kind: "activity", Presentation: "compaction",
				Name: "正在压缩上下文", Status: "running", ActivityOrder: 10})
			a.publishPreviewCompaction(detail)
			raw, err := json.Marshal(domain.ContextActionResult{Operation: domain.Operation{ID: in.OperationID, SessionID: in.SessionID,
				Kind: "compact", State: domain.OperationConfirmed}, Session: m.Details[in.SessionID].Session})
			done(raw, err)
			status := domain.ExecutionCompleted
			if fixture.failFirst {
				status = domain.ExecutionFailed
				fixture.failFirst = false
			}
			time.AfterFunc(450*time.Millisecond, func() {
				mygo.RunOnMain(func() {
					detail := m.Details[in.SessionID]
					if action := detail.Session.ContextAction; action == nil || action.OperationID != in.OperationID {
						return
					}
					action := *detail.Session.ContextAction
					action.State = status
					detail.Session.ContextAction = &action
					detail.Session.Status = action.PreviousStatus
					detail.Turns[len(detail.Turns)-1].Status = status
					item := &detail.Items[len(detail.Items)-1]
					item.Final, item.Status, item.Name = true, string(status), mobileclient.ContextCompactionLabel(status)
					a.publishPreviewCompaction(detail)
					a.refresh()
				})
			})
		})
	})
}

func (a *mobileApp) publishPreviewCompaction(detail domain.SessionDetail) {
	detail.Session.Revision++
	for i, session := range a.model.Snapshot.Sessions {
		if session.ID == detail.Session.ID {
			a.model.Snapshot.Sessions[i] = detail.Session
			break
		}
	}
	a.model.Details[detail.Session.ID] = detail
}
