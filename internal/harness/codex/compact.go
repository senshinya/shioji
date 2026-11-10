package codex

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
)

func (r *Runtime) Compact(ctx context.Context, s domain.Session) error {
	if !r.supports("thread/compact/start") {
		return harness.Unsupported("context-compaction")
	}
	if !r.Observes(s) {
		return errors.New("压缩目标已改变，请重新打开会话")
	}
	r.normalizer.mu.Lock()
	if r.normalizer.manualCompaction == nil {
		r.normalizer.manualCompaction = map[string]bool{}
	}
	r.normalizer.manualCompaction[s.ThreadID] = true
	r.normalizer.mu.Unlock()
	err := r.call(ctx, "thread/compact/start", map[string]string{"threadId": s.ThreadID}, nil)
	var call *CallError
	if err != nil && (!errors.As(err, &call) || !call.Uncertain) {
		r.normalizer.mu.Lock()
		delete(r.normalizer.manualCompaction, s.ThreadID)
		r.normalizer.mu.Unlock()
	}
	return err
}

func (r *Runtime) InspectCompaction(ctx context.Context, s domain.Session, action domain.ContextAction) (domain.ContextObservation, error) {
	var none domain.ContextObservation
	if action.SourceRef == "" || action.SourceRef != s.ThreadID {
		return none, errors.New("压缩历史归属已改变")
	}
	history, err := r.Inspect(ctx, s)
	if err != nil {
		return none, err
	}
	return compactionObservation(history, action), nil
}

func compactionObservation(history domain.RecoveryHistory, action domain.ContextAction) domain.ContextObservation {
	var none domain.ContextObservation
	after := action.BeforeTurnID == ""
	var candidates []domain.ContextObservation
	for _, turn := range history.Turns {
		if turn.ID == action.BeforeTurnID {
			after = true
			continue
		}
		if !after {
			continue
		}
		if action.TurnID != "" && action.TurnID != turn.ID {
			continue
		}
		if turn.Presentation != "compaction" {
			continue
		}
		for _, item := range history.Items {
			if item.TurnID == turn.ID && item.Presentation == "compaction" {
				candidates = append(candidates, domain.ContextObservation{OperationID: action.OperationID, TurnID: turn.ID, State: turn.Status})
				break
			}
		}
	}
	// Without a receipt, multiple candidate operations cannot be disambiguated.
	if len(candidates) != 1 {
		return none
	}
	return candidates[0]
}
