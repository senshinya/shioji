package codex

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
)

func (n *Normalizer) workInputCapabilities(w *domain.WorkActivity, binding agentWorkBinding) {
	w.CanSteer = n.workSteer && w.Active() && binding.currentTurn != ""
	w.CanContinue = n.workContinue && (w.State == "completed" || w.State == "stopped")
}
func (r *Runtime) workInputTarget(s domain.Session, in domain.WorkInput) (string, string, error) {
	r.normalizer.mu.Lock()
	defer r.normalizer.mu.Unlock()
	n := r.normalizer
	if s.ID != in.SessionID || n.sessions[s.ThreadID] != s.ID {
		return "", "", errors.New("子 Agent 来源会话已变化")
	}
	for ref, b := range n.workState().agents {
		if b.id == in.WorkID && b.session == s.ID && b.rootThread == s.ThreadID {
			return ref, b.currentTurn, nil
		}
	}
	return "", "", errors.New("没有可确认的子 Agent 运行实例")
}
func (r *Runtime) SendWork(ctx context.Context, s domain.Session, in domain.WorkInput) error {
	child, turn, err := r.workInputTarget(s, in)
	if err != nil {
		return err
	}
	r.normalizer.mu.Lock()
	w := r.normalizer.workState().works[in.WorkID]
	r.normalizer.mu.Unlock()
	input := []wire.Input{{Type: "text", Text: in.Text}}
	if in.Mode == "steer" {
		if !w.CanSteer || turn == "" || !r.supports("turn/steer") {
			return harness.Unsupported("independent-work-steer")
		}
		var out struct{ TurnID string }
		params := map[string]any{"threadId": child, "expectedTurnId": turn, "clientUserMessageId": "input:" + in.OperationID, "input": input}
		if err = r.call(ctx, "turn/steer", params, &out); err != nil {
			return err
		}
		if out.TurnID != turn {
			return &CallError{Uncertain: true, Message: "子 Agent 未确认消息归属"}
		}
		return nil
	}
	if in.Mode != "continue" || !w.CanContinue || !r.supports("turn/start") {
		return harness.Unsupported("independent-work-continue")
	}
	// Omit model, permission, environment and collaboration overrides: this is the
	// existing child's execution, whose configuration need not match its parent.
	var out struct{ Turn wire.Turn }
	if err = r.call(ctx, "turn/start", map[string]any{"threadId": child, "clientUserMessageId": "input:" + in.OperationID, "input": input}, &out); err != nil {
		return err
	}
	if out.Turn.ID == "" {
		return &CallError{Uncertain: true, Message: "子 Agent 未确认新的执行标识"}
	}
	return nil
}
func (r *Runtime) WorkInputReceived(ctx context.Context, s domain.Session, in domain.WorkInput) (bool, error) {
	child, _, err := r.workInputTarget(s, in)
	if err != nil {
		return false, err
	}
	if !r.supports("thread/turns/list") {
		return false, harness.Unsupported("independent-work-receipt")
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		var page struct {
			Data []struct {
				ID        string
				Items     []wire.Item
				ItemsView string
			}
			NextCursor *string
		}
		params := map[string]any{"threadId": child, "limit": 100, "itemsView": "full", "sortDirection": "desc"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err = r.call(ctx, "thread/turns/list", params, &page); err != nil {
			return false, err
		}
		for _, t := range page.Data {
			if t.ItemsView != "" && t.ItemsView != "full" {
				return false, errors.New("子 Agent 历史不完整")
			}
			for _, i := range t.Items {
				if i.Type == "userMessage" && workString(i, "clientId") == "input:"+in.OperationID {
					return true, nil
				}
			}
		}
		if page.NextCursor == nil {
			return false, nil
		}
		if *page.NextCursor == "" || seen[*page.NextCursor] {
			return false, errors.New("子 Agent 历史游标重复")
		}
		cursor = *page.NextCursor
		seen[cursor] = true
	}
}
