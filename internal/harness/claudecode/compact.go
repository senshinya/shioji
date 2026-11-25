package claudecode

import (
	"context"
	"errors"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func (r *runtime) PrepareCompaction(ctx context.Context, s domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.canCompact.Load() || r.session.ID != s.ID || r.session.ThreadID != s.ThreadID || r.Stopped() || r.closing {
		return harness.Unsupported("context-compaction")
	}
	if r.turn != nil || len(r.approvals) != 0 {
		return errors.New("当前会话仍在执行")
	}
	return ctx.Err()
}

func (r *runtime) Compact(ctx context.Context, s domain.Session) error {
	if err := r.PrepareCompaction(ctx, s); err != nil {
		return err
	}
	r.mu.Lock()
	input := preparedInput{Blocks: []map[string]any{{"type": "text", "text": "/compact"}}}
	if r.session.ID != s.ID || r.session.ThreadID != s.ThreadID {
		r.mu.Unlock()
		return errors.New("Claude Code 压缩目标已改变")
	}
	_, receipt, err := r.beginPrompt(ctx, "", input, "compaction", s.Config)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case err := <-receipt:
		return err
	case <-ctx.Done():
		return deliveryUnknown()
	}
}

func (*runtime) InspectCompaction(context.Context, domain.Session, domain.ContextAction) (domain.ContextObservation, error) {
	// The streaming boundary proves a live completion. After losing the
	// process it cannot by itself identify the worker's specific request.
	return domain.ContextObservation{}, nil
}

func (r *runtime) compactionActivity(t *activeTurn, status domain.ExecutionState) error {
	name := "正在压缩上下文"
	switch status {
	case domain.ExecutionCompleted:
		name = "上下文已压缩"
	case domain.ExecutionInterrupted:
		name = "上下文压缩已停止"
	case domain.ExecutionFailed:
		name = "上下文压缩失败"
	}
	item := &domain.Item{ID: t.turn.ID + ":compaction", TurnID: t.turn.ID, Kind: "activity", Presentation: "compaction", Name: name, Status: string(status), Final: !status.Active()}
	return r.emit(domain.Event{Type: "item.upsert", Item: item, ActivityStart: status.Active()})
}

var _ harness.ContextCompactor = (*runtime)(nil)
var _ harness.ContextCompactionPreparer = (*runtime)(nil)
