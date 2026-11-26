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
	runID := r.workRunIDs[in.TaskID]
	now := time.Now().UTC()
	switch in.Subtype {
	case "task_started":
		if turn == nil || !in.Background && in.TaskType != "local_agent" ||
			runID != "" && in.RunID <= runID || exists && in.RunID == "" {
			return nil
		}
		if exists {
			// A resumed task keeps the native task ID, but a new run owns
			// a different activity and cannot inherit the old run's tools.
			work.State, work.CompletedAt, work.CanStop = "unknown", &now, false
			if err := r.settleWork(in.TaskID, work); err != nil {
				return err
			}
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
		if r.workPermission == nil {
			r.workPermission = make(map[string]string)
		}
		if in.RunID != "" {
			if r.workRunIDs == nil {
				r.workRunIDs = make(map[string]string)
			}
			r.workRunIDs[in.TaskID] = in.RunID
		}
		kind := "background"
		if in.TaskType == "local_agent" {
			kind = "agent"
		}
		title := boundedText(r.redact(in.Description), 512)
		if title == "" {
			title = "Claude Code 后台任务"
		}
		work = domain.WorkActivity{ID: domain.NewID(), Reference: in.TaskID, Kind: kind,
			OriginRef: r.session.ThreadID, SourceItemID: turn.tools[in.ToolUseID], TurnID: turn.turn.ID,
			Title: title, StartedAt: &now, State: "running", CanStop: true}
		r.work[in.TaskID] = work
		r.workPlan[in.TaskID] = turn.plan
		r.workPermission[in.TaskID] = turn.permission
		if in.ToolUseID != "" {
			r.workCalls[in.ToolUseID] = in.TaskID
		}
	case "task_progress":
		if !exists || in.RunID != runID {
			return nil
		}
		work.Latest = boundedText(r.redact(in.Description), 4096)
		r.work[in.TaskID] = work
	case "task_notification":
		if !exists || in.RunID != runID {
			return nil
		}
		switch in.Status {
		case "completed", "failed":
			work.State = in.Status
		case "stopped", "cancelled", "interrupted":
			work.State = "stopped"
		default:
			return nil
		}
		work.Latest, work.CompletedAt, work.CanStop = boundedText(r.redact(in.Summary), 4096), &now, false
		return r.settleWork(in.TaskID, work)
	default:
		return nil
	}
	return r.emit(domain.Event{Type: "work.upsert", Work: &work})
}

func (r *runtime) settleWork(taskID string, work domain.WorkActivity) error {
	for _, item := range r.workTools[taskID] {
		if item.Final {
			continue
		}
		item.Final, item.Status = true, "unknown"
		if work.State == "stopped" {
			item.Status = "interrupted"
		}
		if err := r.emit(domain.Event{Type: "work.item", WorkID: work.ID, WorkItem: &item}); err != nil {
			return err
		}
	}
	for _, p := range r.approvals {
		if p.request.WorkID == work.ID {
			if err := r.invalidateApproval(p); err != nil {
				return err
			}
		}
	}
	if err := r.emit(domain.Event{Type: "work.upsert", Work: &work}); err != nil {
		return err
	}
	delete(r.work, taskID)
	delete(r.workTools, taskID)
	delete(r.workPlan, taskID)
	delete(r.workPermission, taskID)
	for call, target := range r.workCalls {
		if target == taskID {
			delete(r.workCalls, call)
		}
	}
	return nil
}

func (r *runtime) observeWorkItem(in frame) error {
	agent := in.AgentID
	if agent == "" {
		agent = r.workCalls[in.ParentToolUseID]
	}
	work, exists := r.work[agent]
	if !exists || len(in.ErrorCode) != 0 && string(in.ErrorCode) != "null" {
		return nil
	}
	tools := r.workTools[agent]
	if in.Type == "user" || in.Type == "assistant" {
		sidecar := singleToolResultSidecar(in.Message.Content, in.ToolUseResult)
		detaches := sidecarDetachesTool(sidecar)
		for _, result := range in.Message.Content {
			item, ok := tools[result.ToolUseID]
			if result.Type != "tool_result" || !ok || item.Final || detaches {
				continue
			}
			item = r.redactItem(r.completeTool(item, result, sidecar))
			tools[result.ToolUseID] = item
			if err := r.emit(domain.Event{Type: "work.item", WorkID: work.ID, WorkItem: &item}); err != nil {
				return err
			}
		}
	}
	if in.Type != "assistant" {
		return nil
	}
	// Child output is projected from completed frames only. Each frame has
	// its own UUID, even when multiple frames share the same message ID.
	source := in.UUID
	if source == "" {
		source = in.Message.ID
	}
	for i, block := range in.Message.Content {
		item := domain.Item{ID: work.ID + ":" + source + ":" + strconv.Itoa(i), WorkID: work.ID,
			TurnID: work.TurnID, Status: "completed", Final: true}
		switch block.Type {
		case "text":
			item.Kind, item.Text = "assistant", block.Text
		case "thinking":
			item.Kind, item.Presentation, item.Text = "reasoning", "reasoning", block.Thinking
		case "tool_use":
			item.Kind, item.Name, item.Text = "tool", block.Name, boundedText(string(block.Input), 8<<10)
			item.Presentation = sdkToolPresentation(block.Name)
			item.Truncated = len(item.Text) != len(block.Input)
			if block.ID != "" {
				if tools[block.ID].ID != "" {
					continue
				}
				item.Final, item.Status = false, "running"
				if r.workTools == nil {
					r.workTools = make(map[string]map[string]domain.Item)
				}
				if tools == nil {
					tools = make(map[string]domain.Item)
					r.workTools[agent] = tools
				}
				tools[block.ID] = r.redactItem(item)
			}
		default:
			continue
		}
		item = limitReplyText(r.redactItem(item))
		if err := r.emit(domain.Event{Type: "work.item", WorkID: work.ID, WorkItem: &item}); err != nil {
			return err
		}
	}
	return nil
}

func (r *runtime) StopWork(ctx context.Context, s domain.Session, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	var taskID string
	if !r.closing && !r.Stopped() && r.session.ID == s.ID && r.session.ThreadID == s.ThreadID {
		for nativeID, work := range r.work {
			if work.ID == id && work.Active() && work.CanStop {
				taskID = nativeID
				break
			}
		}
	}
	if taskID == "" {
		r.mu.Unlock()
		return errors.New("未找到此会话仍在运行的 Claude Code 后台任务")
	}
	requestID, reply := domain.NewID(), make(chan bool, 1)
	if r.workReplies == nil {
		r.workReplies = map[string]chan bool{}
	}
	r.workReplies[requestID] = reply
	err := r.send(map[string]string{"type": "stop_work", "thread": s.ThreadID, "task_id": taskID, "request_id": requestID})
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.workReplies, requestID)
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
