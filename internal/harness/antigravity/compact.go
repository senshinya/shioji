package antigravity

import (
	"context"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

const compactCommand = "/compact"

// The command advertisement can arrive before the new/resumed session reply.
func (r *runtime) observeCommands(id string, commands []struct{ Name string }) {
	if id == "" || r.session.ThreadID != "" && r.session.ThreadID != id {
		return
	}
	if r.commandOffers == nil {
		r.commandOffers = make(map[string]bool)
	}
	compact := false
	for _, command := range commands {
		if strings.TrimPrefix(command.Name, "/") == "compact" {
			compact = true
			break
		}
	}
	r.commandOffers[id] = compact
	if r.commandChanged != nil {
		close(r.commandChanged)
		r.commandChanged = nil
	}
}

func (r *runtime) waitForCompactCommand(ctx context.Context, id string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		r.mu.Lock()
		compact, advertised := r.commandOffers[id]
		if r.commandChanged == nil {
			r.commandChanged = make(chan struct{})
		}
		changed := r.commandChanged
		r.mu.Unlock()
		if advertised {
			return compact
		}
		select {
		case <-changed:
		case <-r.Done():
			return false
		case <-ctx.Done():
			return false
		}
	}
}

func (r *runtime) PrepareCompaction(ctx context.Context, s domain.Session) error {
	if !r.Observes(s) || !r.waitForCompactCommand(ctx, s.ThreadID) {
		return harness.Unsupported("context-compaction")
	}
	return ctx.Err()
}

func (r *runtime) Compact(ctx context.Context, s domain.Session) error {
	if err := r.PrepareCompaction(ctx, s); err != nil {
		return err
	}
	r.direct.Lock()
	defer r.direct.Unlock()
	now := time.Now().UTC()
	t := &logicalTurn{
		turn:    domain.Turn{ID: "agy:" + domain.NewID(), StartedAt: &now, Status: domain.ExecutionRunning, Presentation: "compaction"},
		primary: make(chan error, 1), tools: make(map[string]domain.Item),
	}
	return r.beginPrompt(ctx, s, t, []contentBlock{{Type: "text", Text: compactCommand}}, nil, nil)
}

// The native protocol provides no durable compaction receipt to query after loss.
func (*runtime) InspectCompaction(context.Context, domain.Session, domain.ContextAction) (domain.ContextObservation, error) {
	return domain.ContextObservation{}, nil
}

func (r *runtime) compactionActivity(t *logicalTurn, status domain.ExecutionState) error {
	name := "正在压缩上下文"
	switch status {
	case domain.ExecutionCompleted:
		name = "压缩命令已结束"
	case domain.ExecutionInterrupted:
		name = "上下文压缩已停止"
	case domain.ExecutionFailed:
		name = "上下文压缩失败"
	}
	item := &domain.Item{ID: t.turn.ID + ":compaction", TurnID: t.turn.ID, Kind: "activity", Presentation: "compaction", Name: name, Status: string(status), Final: !status.Active()}
	return r.emit(domain.Event{Type: "item.upsert", Item: item, ActivityStart: status.Active()})
}
