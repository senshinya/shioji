package codex

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
	"slices"
	"time"
)

// Fork copies a persisted source prefix without binding or starting a turn.
// The owning worker binds the returned thread only after its local transaction.
func (r *Runtime) Fork(ctx context.Context, source domain.Session, turn string, before bool) (string, error) {
	if source.ThreadID == "" || turn == "" {
		return "", errors.New("缺少分叉的源线程或轮次")
	}
	var meta struct{ Thread struct{ ID, CWD string } }
	if err := r.call(ctx, "thread/read", map[string]any{"threadId": source.ThreadID}, &meta); err != nil {
		return "", err
	}
	if meta.Thread.ID != source.ThreadID || sourceExecutionPath(meta.Thread.CWD) != source.Context.CWD {
		return "", errors.New("源线程与会话的执行目录不一致")
	}
	params := map[string]any{"threadId": source.ThreadID, "excludeTurns": true, "deferGoalContinuation": true, "config": map[string]any{"thread_unload_delay_secs": 0}}
	if before {
		params["beforeTurnId"] = turn
	} else {
		params["lastTurnId"] = turn
	}
	var out struct {
		Thread struct {
			ID           string
			ForkedFromID *string `json:"forkedFromId"`
		}
		CWD string
	}
	if err := r.call(ctx, "thread/fork", params, &out); err != nil {
		return "", err
	}
	if out.Thread.ID == "" || out.Thread.ID == source.ThreadID || sourceExecutionPath(out.CWD) != source.Context.CWD || out.Thread.ForkedFromID == nil || *out.Thread.ForkedFromID != source.ThreadID {
		return "", errors.New("Codex 未确认正确的分叉来源和执行目录")
	}
	return out.Thread.ID, r.ReleaseFork(ctx, out.Thread.ID)
}

// ReleaseFork hands a persisted fork to the destination runtime. It is safe to
// repeat for a saved result, including after the creating app-server restarted.
func (r *Runtime) ReleaseFork(ctx context.Context, thread string) error {
	if err := r.call(ctx, "thread/unsubscribe", map[string]any{"threadId": thread}, nil); err != nil {
		return err
	}
	for {
		var loaded struct {
			Data       []string
			NextCursor *string
		}
		if err := r.call(ctx, "thread/loaded/list", map[string]any{}, &loaded); err != nil {
			return err
		}
		if loaded.NextCursor != nil && *loaded.NextCursor != "" {
			return errors.New("无法确认分叉线程已释放")
		}
		if !slices.Contains(loaded.Data, thread) {
			return nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// SwitchHistory serializes the durable history replacement with source event
// normalization. No late notification from oldThread can target the new history.
func (r *Runtime) SwitchHistory(oldThread, newThread, session string, commit func() error) error {
	r.eventMu.Lock()
	defer r.eventMu.Unlock()
	if err := commit(); err != nil {
		return err
	}
	r.normalizer.mu.Lock()
	delete(r.normalizer.sessions, oldThread)
	delete(r.normalizer.totals, oldThread)
	if usage, ok := r.normalizer.usage[oldThread]; ok {
		usage.baseline = wire.Counters{}
		r.normalizer.usage[oldThread] = usage
	}
	r.normalizer.mu.Unlock()
	r.normalizer.Bind(newThread, session, false)
	return nil
}
