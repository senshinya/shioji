package main

import (
	"encoding/json"
	"time"

	"github.com/egoist/mygo"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

const previewGoalObjective = "验证断线重连、历史顺序和队列回执"

func (a *mobileApp) seedPreviewGoal(id string, legacy bool) {
	m := a.model
	m.WorkerMinor = protocol.GoalMinor
	m.Readiness.Capabilities.Goals = true
	if legacy {
		m.WorkerMinor--
	}
	detail := m.Details[id]
	s := &detail.Session
	s.Title, s.ThreadID, s.Status = "验证长期目标", "preview-goal-thread", domain.ExecutionCompleted
	s.Goal = &domain.Goal{Objective: previewGoalObjective, State: domain.GoalActive, ElapsedSeconds: 95, CreatedAt: 101}
	first, continuation := detail.Turns[0], detail.Turns[1]
	continuation.Status = domain.ExecutionCompleted
	first.Status = domain.ExecutionCompleted
	prompt := detail.Items[0]
	prompt.Text = "/goal " + previewGoalObjective
	detail.Turns = []domain.Turn{first, continuation}
	detail.Items = []domain.Item{
		prompt,
		{ID: "goal-first-result", Kind: "assistant", TurnID: first.ID, Text: "重连已验证。接着核对队列回执。", Final: true},
		{ID: "goal-hook", Kind: "activity", Presentation: "context", TurnID: continuation.ID, Name: "已补充工作区上下文", Final: true},
		{ID: "goal-continue", Kind: "activity", Presentation: domain.GoalContinuationPresentation, TurnID: continuation.ID, Name: "目标自动续跑", Final: true},
		{ID: "goal-next-result", Kind: "assistant", TurnID: continuation.ID, Text: "待发消息的接纳回执与当前会话一致。", Final: true},
	}
	detail.Plan, detail.Works, detail.Requests, detail.Queue.Items = nil, nil, nil, nil
	m.Details[id], m.Snapshot.Sessions[0] = detail, *s
}

func (a *mobileApp) previewControlGoal(in domain.GoalRequest, query bool, done func(json.RawMessage, error)) {
	m := a.model
	time.AfterFunc(180*time.Millisecond, func() {
		mygo.RunOnMain(func() {
			current := a.sessionByID(in.SessionID)
			if a.model != m || query || !current.Goal.SameTarget(&in.Target) {
				done(nil, &protocol.RemoteError{Code: "notFound", Message: "找不到目标操作"})
				return
			}
			goal := in.Target
			switch in.Action {
			case domain.PauseGoal:
				goal.State = domain.GoalPaused
			case domain.ResumeGoal:
				goal.State = domain.GoalActive
			}
			current.Goal = &goal
			if in.Action == domain.ClearGoal {
				current.Goal = nil
			}
			current.Revision++
			op := domain.Operation{ID: in.OperationID, SessionID: in.SessionID, Kind: "goal", State: domain.OperationConfirmed}
			if err := m.ApplyEvent(domain.Event{Type: "goal", Sequence: m.Sequence + 1, SessionID: in.SessionID, Session: &current}); err != nil {
				done(nil, err)
				return
			}
			raw, err := json.Marshal(op)
			done(raw, err)
		})
	})
}
