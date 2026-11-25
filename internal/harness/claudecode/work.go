package claudecode

import (
	"context"
	"errors"
	"strconv"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func (r *runtime) observeWork(turn *activeTurn, in frame) error {
	work, exists := r.work[in.TaskID]
	now := time.Now().UTC()
	switch in.Subtype {
	case "task_started":
		if exists || turn == nil || !in.Background && in.TaskType != "local_agent" {
			return nil
		}
		if r.work == nil {
			r.work = make(map[string]domain.WorkActivity)
		}
		if r.workCalls == nil {
			r.workCalls = make(map[string]string)
		}
		if r.workPlan == nil {
			r.workPlan = make(map[string]bool)
		}
		kind := "background"
		if in.TaskType == "local_agent" {
			kind = "agent"
		}
		title := r.redact(in.Description)
		if title == "" {
			title = "Claude Code 后台任务"
		}
		work = domain.WorkActivity{ID: domain.NewID(), Reference: in.TaskID, Kind: kind,
			OriginRef: r.session.ThreadID, SourceItemID: turn.tools[in.ToolUseID], TurnID: turn.turn.ID,
			Title: title, StartedAt: &now, State: "running", CanStop: true}
		r.work[in.TaskID] = work
		r.workPlan[in.TaskID] = turn.plan
		if in.ToolUseID != "" {
			r.workCalls[in.ToolUseID] = in.TaskID
		}
	case "task_progress":
		if !exists {
			return nil
		}
		work.Latest = r.redact(in.Description)
		r.work[in.TaskID] = work
	case "task_notification":
		if !exists {
			return nil
		}
		switch in.Status {
		case "completed", "failed":
			work.State = in.Status
		case "stopped", "cancelled", "interrupted":
			work.State = "interrupted"
		default:
			return nil
		}
		work.Latest, work.CompletedAt, work.CanStop = r.redact(in.Summary), &now, false
		for _, p := range r.approvals {
			if p.request.WorkID == work.ID {
				if err := r.invalidateApproval(p); err != nil {
					return err
				}
			}
		}
		delete(r.work, in.TaskID)
		delete(r.workPlan, in.TaskID)
		for call, target := range r.workCalls {
			if target == in.TaskID {
				delete(r.workCalls, call)
			}
		}
	default:
		return nil
	}
	return r.emit(domain.Event{Type: "work.upsert", Work: &work})
}

func (r *runtime) observeWorkItem(in frame) error {
	work, exists := r.work[r.workCalls[in.ParentToolUseID]]
	if !exists || in.Type != "assistant" || len(in.ErrorCode) != 0 && string(in.ErrorCode) != "null" {
		return nil
	}
	for i, block := range in.Message.Content {
		item := domain.Item{ID: work.ID + ":" + in.Message.ID + ":" + strconv.Itoa(i), WorkID: work.ID,
			TurnID: work.TurnID, Status: "completed", Final: true}
		switch block.Type {
		case "text":
			item.Kind, item.Text = "assistant", block.Text
		case "thinking":
			item.Kind, item.Text = "reasoning", block.Thinking
		case "tool_use":
			item.Kind, item.Name, item.Text = "tool", block.Name, string(block.Input)
		default:
			continue
		}
		item.Text = r.redact(item.Text)
		if err := r.emit(domain.Event{Type: "work.item", WorkID: work.ID, WorkItem: &item}); err != nil {
			return err
		}
	}
	return nil
}

func (r *runtime) StopWork(ctx context.Context, s domain.Session, reference string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	target, ok := r.work[reference]
	if !ok || !target.Active() || !target.CanStop || r.closing || r.Stopped() ||
		r.session.ID != s.ID || r.session.ThreadID != s.ThreadID {
		r.mu.Unlock()
		return errors.New("未找到此会话仍在运行的 Claude Code 后台任务")
	}
	id, reply := domain.NewID(), make(chan bool, 1)
	if r.workReplies == nil {
		r.workReplies = map[string]chan bool{}
	}
	r.workReplies[id] = reply
	err := r.send(map[string]string{"type": "stop_work", "thread": s.ThreadID, "task_id": target.Reference, "request_id": id})
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.workReplies, id)
		r.mu.Unlock()
	}()
	if err != nil {
		return errors.New("Claude Code 后台任务停止请求未确认")
	}
	select {
	case ok := <-reply:
		if ok {
			return nil
		}
		return errors.New("Claude Code 未确认停止此后台任务")
	case <-r.Done():
		return errors.New("Claude Code 运行环境已退出")
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ harness.WorkController = (*runtime)(nil)
