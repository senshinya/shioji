package main

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"shioji/internal/domain"
)

func (a *mobileApp) seedConversationMotion(board, sid string) {
	a.previewConversationMotion = board
	m := a.model
	m.ConnectionEpoch = "preview-conversation-motion"
	detail := m.Details[sid]
	detail.Queue = domain.QueueState{SessionID: sid, Revision: 1}
	if strings.HasPrefix(board, "motion-send") {
		detail.Session.Status = domain.ExecutionCompleted
		detail.Items = detail.Items[:len(detail.Items)-1]
		detail.Turns = detail.Turns[:len(detail.Turns)-1]
	}
	m.Drafts["session:"+sid] = map[string]string{
		"motion-send":          "发送后的新消息",
		"motion-send-delayed":  "发送后的新消息",
		"motion-steer-delayed": "插入当前执行轮的消息",
		"motion-steer":         "插入当前执行轮的消息",
		"motion-queue":         "先排队再强制插入",
		"motion-queue-auto":    "轮次结束后自动发送",
	}[board]
	m.Snapshot.Sessions[0] = detail.Session
	m.Details[sid] = detail
}

func (a *mobileApp) seedReplyHeightMotion(sid string) {
	m := a.model
	m.ConnectionEpoch = "preview-reply-height"
	detail := m.Details[sid]
	detail.HistoryLoaded = true
	detail.Queue.Items = nil
	start := time.Now()
	turn := domain.Turn{ID: "reply-growth-turn", Status: domain.ExecutionRunning, StartedAt: &start}
	detail.Turns = []domain.Turn{turn}
	detail.Items = []domain.Item{
		{ID: "growth-history", Kind: "assistant", TurnID: turn.ID, Final: true,
			Text: strings.Repeat("此前已核对设备连接、会话事件与发送状态。\n\n", 26)},
		{ID: "growth-prompt", Kind: "user", TurnID: turn.ID, Order: 1, Status: "sent", SentAt: &start,
			Text: "检查回复的布局动画"},
		{ID: "growth-reply", Kind: "assistant", TurnID: turn.ID, Order: 2, Text: "已记录第一段回复。"},
	}
	m.Details[sid] = detail
	time.AfterFunc(8*time.Second, func() {
		mygo.RunOnMain(func() {
			if m != a.model || m.SessionID != sid {
				return
			}
			current := m.Details[sid]
			current.Items = slices.Clone(current.Items)
			current.Items[len(current.Items)-1].Text += "\n\n追加第一段进展。\n\n追加第二段进展。\n\n追加第三段进展。\n\n追加第四段进展。"
			m.Details[sid] = current
			a.refresh()
		})
	})
}

func (a *mobileApp) previewAcceptMotion(in domain.Submission, method, turn string) {
	board, m := a.previewConversationMotion, a.model
	if board == "motion-home-preparing" || board == "motion-home-handoff" {
		a.refresh()
		time.AfterFunc(5*time.Second, func() {
			mygo.RunOnMain(func() {
				if a.model == m {
					m.Accepted(in, domain.Operation{ID: in.OperationID, State: domain.OperationPreparing})
					a.refresh()
				}
			})
		})
		if board == "motion-home-handoff" {
			time.AfterFunc(9*time.Second, func() {
				mygo.RunOnMain(func() {
					if a.model == m && a.firstSubmissionVisible(a.draftPage) {
						a.confirmPreviewFirstSubmission(in)
					}
				})
			})
		}
		return
	}
	if board == "motion-send-delayed" || board == "motion-steer-delayed" {
		a.refresh()
		time.AfterFunc(6*time.Second, func() {
			mygo.RunOnMain(func() {
				if a.model == m {
					a.confirmPreviewMotion(in, method, turn)
				}
			})
		})
		return
	}
	a.confirmPreviewMotion(in, method, turn)
}

func (a *mobileApp) confirmPreviewMotion(in domain.Submission, method, turn string) {
	if method == "turn.enqueue" {
		q := a.model.Details[in.SessionID].Queue
		q.Revision++
		q.Items = append(q.Items, domain.QueueItem{ID: in.OperationID, Input: in})
		a.model.SetQueue(q)
	} else {
		a.previewMotionJournal(in, turn)
	}
	a.model.Accepted(in, domain.Operation{ID: in.OperationID, SessionID: in.SessionID, State: domain.OperationConfirmed})
	a.refresh()
	if method == "turn.enqueue" && a.previewConversationMotion == "motion-queue-auto" {
		time.AfterFunc(5*time.Second, func() {
			mygo.RunOnMain(func() { a.previewDispatchMotion(in.SessionID, in.OperationID, "") })
		})
	}
}

func (a *mobileApp) previewDispatchMotion(sid, id, turn string) {
	q := a.model.Details[sid].Queue
	index := slices.IndexFunc(q.Items, func(item domain.QueueItem) bool { return item.ID == id })
	if index < 0 {
		return
	}
	input := q.Items[index].Input
	q.Items = slices.Delete(slices.Clone(q.Items), index, index+1)
	q.Revision++
	a.model.SetQueue(q)
	a.previewMotionJournal(input, turn)
	a.refresh()
}

func (a *mobileApp) previewMotionJournal(in domain.Submission, turn string) {
	m := a.model
	detail := m.Details[in.SessionID]
	now := time.Now()
	if turn == "" {
		if count := len(detail.Turns); count > 0 && detail.Turns[count-1].Status.Active() {
			detail.Turns[count-1].Status = domain.ExecutionCompleted
			detail.Turns[count-1].CompletedAt = &now
		}
		turn = "preview-turn:" + in.OperationID
		detail.Turns = append(detail.Turns, domain.Turn{ID: turn, Status: domain.ExecutionRunning, StartedAt: &now})
	}
	detail.Session.Status = domain.ExecutionRunning
	detail.Items = append(detail.Items, domain.Item{ID: "input:" + in.OperationID, TurnID: turn, Kind: "user", Status: "sent",
		Text: in.Text, Attachments: in.Attachments, Skills: in.Skills, ReferencePositions: in.ReferencePositions, SentAt: &now})
	m.Details[in.SessionID] = detail
	for index, session := range m.Snapshot.Sessions {
		if session.ID == in.SessionID {
			m.Snapshot.Sessions[index] = detail.Session
			break
		}
	}
}

func (a *mobileApp) confirmPreviewFirstSubmission(in domain.Submission) {
	m := a.model
	session := domain.Session{
		ID: "motion-first-session", WorkspaceID: in.WorkspaceID, Title: "核对新会话的准备状态",
		ThreadID: "motion-first-thread", HarnessID: in.HarnessID, Config: in.Config,
		Status: domain.ExecutionRunning,
	}
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, session)
	m.Details[session.ID] = domain.SessionDetail{Session: session, HistoryLoaded: true}
	delivery := in
	delivery.SessionID = session.ID
	a.previewMotionJournal(delivery, "")
	m.Accepted(in, domain.Operation{ID: in.OperationID, SessionID: session.ID, State: domain.OperationConfirmed})
	a.completeDraftSubmission(session.ID)
	a.refresh()
}
