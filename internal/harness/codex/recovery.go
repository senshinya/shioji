package codex

import (
	"context"
	"encoding/json"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
)

func (r *Runtime) Stopped() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// Inspect does not resume a thread or start a turn. Read/list can also expose
// persisted interrupted tails after a previous app-server has exited.
func (r *Runtime) Inspect(ctx context.Context, s domain.Session) (domain.RecoveryHistory, error) {
	if !r.supports("thread/read") || !r.supports("thread/turns/list") {
		return domain.RecoveryHistory{}, harness.Unsupported("recovery")
	}
	out := domain.RecoveryHistory{Inputs: map[string]string{}}
	var meta struct {
		Thread struct {
			ID     string
			CWD    string
			Status struct{ Type string }
		}
	}
	if s.ThreadID == "" {
		return out, errors.New("会话没有已确认的 Codex 线程标识")
	}
	if err := r.call(ctx, "thread/read", map[string]any{"threadId": s.ThreadID}, &meta); err != nil {
		return out, err
	}
	if meta.Thread.ID != s.ThreadID || sourceExecutionPath(meta.Thread.CWD) != s.Context.CWD {
		return out, errors.New("Codex 线程与会话的执行目录不一致")
	}
	switch meta.Thread.Status.Type {
	case "active":
		out.State = domain.SourceActive
	case "idle":
		out.State = domain.SourceIdle
	case "systemError":
		out.State = domain.SourceFailed
	case "notLoaded":
		out.State = domain.SourceUnavailable
	default:
		return out, errors.New("Codex returned unknown thread state")
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		var page struct {
			Data []struct {
				wire.Turn
				Items     []wire.Item `json:"items"`
				ItemsView string      `json:"itemsView"`
			}
			NextCursor *string
		}
		// Match import's small pages to reduce transient allocations. Framing
		// must still accept large responses: even one item can exceed 16 MiB.
		args := map[string]any{"threadId": s.ThreadID, "limit": 10, "itemsView": "full", "sortDirection": "asc"}
		if cursor != "" {
			args["cursor"] = cursor
		}
		if err := r.call(ctx, "thread/turns/list", args, &page); err != nil {
			return domain.RecoveryHistory{}, err
		}
		for _, src := range page.Data {
			if src.ID == "" || (src.ItemsView != "" && src.ItemsView != "full") {
				return domain.RecoveryHistory{}, errors.New("Codex 恢复历史不完整")
			}
			turn := domain.Turn{ID: src.ID, Status: domain.ExecutionState(src.Status), StartedAt: stamp(src.StartedAt), CompletedAt: stamp(src.CompletedAt), DurationMS: src.DurationMS}
			switch turn.Status {
			case "inProgress":
				turn.Status = "running"
			case "completed", "interrupted", "failed":
			default:
				return domain.RecoveryHistory{}, errors.New("Codex 返回了未知的执行状态")
			}
			if src.Error != nil {
				applyTurnError(&turn, src.Error)
			}
			out.Turns = append(out.Turns, turn)
			if turn.Status == "completed" {
				out.CompleteItemTurns = append(out.CompleteItemTurns, turn.ID)
			}
			standaloneCompaction, hasUser := false, false
			for _, item := range src.Items {
				standaloneCompaction = standaloneCompaction || item.Type == "contextCompaction"
				hasUser = hasUser || item.Type == "userMessage"
				if item.Type == "userMessage" {
					var marker string
					json.Unmarshal(item.Fields["clientId"], &marker)
					if marker != "" {
						if previous := out.Inputs[marker]; previous != "" && previous != src.ID {
							return domain.RecoveryHistory{}, errors.New("Codex 输入标识重复")
						}
						out.Inputs[marker] = src.ID
					}
					continue
				}
				// Plans have a separate structured lifecycle. A history narrative is not a plan.
				if item.Type == "plan" {
					continue
				}
				normalized := r.normalizer.normalizeResourceItem(s.ID, itemKey(src.ID, item.ID), item, turn.Status != "running")
				normalized.ID, normalized.TurnID = itemKey(src.ID, item.ID), src.ID
				out.Items = append(out.Items, normalized)
			}
			if standaloneCompaction && !hasUser {
				out.Turns[len(out.Turns)-1].Presentation = "compaction"
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
		if seen[cursor] {
			return domain.RecoveryHistory{}, errors.New("Codex 历史分页未前进")
		}
		seen[cursor] = true
	}
	// For an observed live thread, completed items and accumulated deltas already
	// in the adapter are newer than the history RPC's snapshot. Never reopen them.
	if out.State == "active" && r.Observes(s) {
		r.normalizer.mu.Lock()
		liveTurns := map[string]bool{}
		for _, turn := range out.Turns {
			liveTurns[turn.ID] = turn.Status == "running"
		}
		for i := range out.Items {
			if current, ok := r.normalizer.items[out.Items[i].ID]; ok && liveTurns[out.Items[i].TurnID] {
				out.Items[i] = current
			}
		}
		for i := range out.Turns {
			if current, ok := r.normalizer.turns[out.Turns[i].ID]; ok && current.Status != "running" {
				out.Turns[i] = current
			}
		}
		r.normalizer.mu.Unlock()
	}

	out.Available = true
	if r.ready.Capabilities.Goals && (s.Goal != nil || s.Status.NeedsRecovery()) {
		goal, err := r.ReadGoal(ctx, s)
		if err == nil {
			out.Goal, out.GoalObserved = goal, true
		}
	}
	if a := s.ContextAction; a != nil && a.SourceRef == s.ThreadID {
		observation := compactionObservation(out, *a)
		out.ContextAction = &observation
	}
	return out, nil
}

func (r *Runtime) Observes(s domain.Session) bool {
	r.normalizer.mu.Lock()
	defer r.normalizer.mu.Unlock()
	return r.normalizer.sessions[s.ThreadID] == s.ID && !r.Stopped()
}

func (r *Runtime) Done() <-chan struct{} { return r.done }
