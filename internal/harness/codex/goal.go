package codex

import (
	"context"
	"errors"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex/wire"
)

func normalizeGoal(g *wire.Goal) *domain.Goal {
	return &domain.Goal{
		Objective: boundedJSONText(g.Objective, 32<<10),
		State:     domain.GoalState(g.Status), ElapsedSeconds: max(g.ElapsedSeconds, 0), CreatedAt: g.CreatedAt,
	}
}

type goalContinuation struct {
	goal      *domain.Goal
	ready     bool
	candidate string
}

func (n *Normalizer) observeGoal(thread string, goal *domain.Goal) {
	state := n.goalContinuations[thread]
	if state == nil {
		if !goal.Active() {
			return
		}
		if n.goalContinuations == nil {
			n.goalContinuations = make(map[string]*goalContinuation)
		}
		state = &goalContinuation{}
		n.goalContinuations[thread] = state
	}
	if !state.goal.SameTarget(goal) {
		state.ready = false
	}
	if goal.Active() {
		state.goal = goal
	} else {
		// A pause or clear after a turn has started does not change its origin.
		state.goal = nil
		if state.candidate == "" {
			delete(n.goalContinuations, thread)
		}
	}
}

func (n *Normalizer) observeGoalTurn(thread string, started bool, previous, turn domain.Turn) {
	state := n.goalContinuations[thread]
	if state == nil || turn.ID == "" {
		return
	}
	if started && previous.ID == "" {
		state.candidate = ""
		if state.ready && turn.Status == domain.ExecutionRunning && turn.Presentation == "" {
			state.candidate = turn.ID
		}
		state.ready = false
	} else if !started && previous.Status == domain.ExecutionRunning && n.workState().currentTurns[thread] == turn.ID {
		state.candidate = ""
		state.ready = state.goal.Active() && turn.Status == domain.ExecutionCompleted && turn.Presentation == ""
	}
	if state.goal == nil && state.candidate == "" {
		delete(n.goalContinuations, thread)
	}
}

// Codex's public turn record omits turn_trigger. A native Goal continuation
// starts after a completed root turn under the same active goal. Wait for
// model output: direct and native queued inputs emit a user item before it.
// Manual compaction is already identified at its source request.
func (n *Normalizer) goalContinuationItem(p wire.Params) *domain.Item {
	state := n.goalContinuations[p.ThreadID]
	if state == nil || state.candidate == "" || state.candidate != p.TurnID {
		return nil
	}
	var marker *domain.Item
	switch p.Item.Type {
	case "userMessage", "enteredReviewMode", "exitedReviewMode":
	case "agentMessage", "reasoning", "plan", "commandExecution", "fileChange", "mcpToolCall", "dynamicToolCall", "collabAgentToolCall", "webSearch", "imageView", "imageGeneration", "sleep":
		marker = &domain.Item{ID: "goal-continuation:" + p.TurnID, TurnID: p.TurnID, Kind: "activity", Presentation: domain.GoalContinuationPresentation, Name: "目标自动续跑", Final: true}
	default:
		return nil
	}
	state.candidate = ""
	if state.goal == nil {
		delete(n.goalContinuations, p.ThreadID)
	}
	return marker
}

func (r *Runtime) goalsEnabled(ctx context.Context, thread string) bool {
	for _, method := range []string{"experimentalFeature/list", "thread/goal/get", "thread/goal/set", "thread/goal/clear"} {
		if !r.supports(method) {
			return false
		}
	}
	for cursor := ""; ; {
		var out struct {
			Data []struct {
				Name    string `json:"name"`
				Enabled bool   `json:"enabled"`
			} `json:"data"`
			Next string `json:"nextCursor"`
		}
		params := map[string]any{"limit": 100}
		if thread != "" {
			params["threadId"] = thread
		}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err := r.call(ctx, "experimentalFeature/list", params, &out); err != nil {
			return false
		}
		for _, f := range out.Data {
			if f.Name == "goals" {
				return f.Enabled
			}
		}
		if out.Next == "" || out.Next == cursor {
			return false
		}
		cursor = out.Next
	}
}

func (r *Runtime) ReadGoal(ctx context.Context, session domain.Session) (*domain.Goal, error) {
	if !r.ready.Capabilities.Goals {
		return nil, harness.Unsupported("goal")
	}
	return r.readGoal(ctx, session.ThreadID)
}

func (r *Runtime) readGoal(ctx context.Context, thread string) (*domain.Goal, error) {
	var out struct {
		Goal *wire.Goal `json:"goal"`
	}
	if err := r.call(ctx, "thread/goal/get", map[string]string{"threadId": thread}, &out); err != nil {
		return nil, err
	}
	if out.Goal == nil {
		return nil, nil
	}
	if out.Goal.ThreadID != thread {
		return nil, errors.New("Goal result belongs to a different conversation")
	}
	return normalizeGoal(out.Goal), nil
}

func (r *Runtime) CreateGoalThread(ctx context.Context, session domain.Session) (string, error) {
	if !r.ready.Capabilities.Goals {
		return "", harness.Unsupported("goal")
	}
	return r.create(ctx, session, true)
}

func (r *Runtime) ChangeGoal(ctx context.Context, session domain.Session, change domain.GoalChange) (*domain.Goal, error) {
	if !r.ready.Capabilities.Goals {
		return nil, harness.Unsupported("goal")
	}
	if change.Action == domain.SetGoal || change.Action == domain.ResumeGoal {
		if !r.goalsEnabled(ctx, session.ThreadID) {
			return nil, harness.Unsupported("goal in this conversation")
		}
		if err := r.Validate(session.Config); err != nil {
			return nil, err
		}
		if err := r.refreshHooksBeforeInput(ctx); err != nil {
			return nil, err
		}
	}
	params := map[string]string{"threadId": session.ThreadID, "origin": "user"}
	switch change.Action {
	case domain.ClearGoal:
		return nil, r.call(ctx, "thread/goal/clear", params, nil)
	case domain.SetGoal:
		params["objective"] = change.Objective
		params["status"] = "active"
	case domain.ResumeGoal:
		params["status"] = "active"
	case domain.PauseGoal:
		params["status"] = "paused"
	default:
		return nil, errors.New("unknown goal action")
	}
	var out struct {
		Goal *wire.Goal `json:"goal"`
	}
	if err := r.call(ctx, "thread/goal/set", params, &out); err != nil {
		return nil, err
	}
	if out.Goal == nil || out.Goal.ThreadID != session.ThreadID {
		return nil, &CallError{Uncertain: true, Message: "Goal response omitted the requested conversation"}
	}
	return normalizeGoal(out.Goal), nil
}
