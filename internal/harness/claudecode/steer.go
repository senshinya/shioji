package claudecode

import (
	"context"
	"errors"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

type steerInput struct {
	operation string
	receipt   chan error
}

func (t *activeTurn) ownsFrame(in frame) bool {
	return in.matchesInput(func(id string) bool {
		_, pending := t.pendingSteer[id]
		return id != "" && (id == t.uuid || pending || t.acceptedSteer[id] != "")
	})
}

func (in frame) matchesInput(owns func(string) bool) bool {
	// Some older CLI builds omit prompt IDs. Preserve the single-turn
	// fallback for unstamped output, but never use a conflicting stamp.
	if len(in.UserMessageUUIDs) > 0 {
		for _, id := range in.UserMessageUUIDs {
			if owns(id) {
				return true
			}
		}
		return false
	}
	if in.UserMessageUUID != "" {
		return owns(in.UserMessageUUID)
	}
	return in.Origin.Kind == "" || in.Origin.Kind == "human"
}

func (r *runtime) Steer(ctx context.Context, s domain.Session, turn, id string, input harness.TurnInput) error {
	return r.SteerWithHandoff(ctx, s, turn, id, input, nil)
}

func (r *runtime) SteerWithHandoff(ctx context.Context, s domain.Session, turnID, operationID string, input harness.TurnInput, claim func() error) error {
	receipt, err := r.SteerDeferred(ctx, s, turnID, operationID, input, claim)
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

func (r *runtime) steerTarget(s domain.Session, id string) *activeTurn {
	t := r.turn
	if r.closing || r.Stopped() || r.session.ID != s.ID || r.session.ThreadID != s.ThreadID ||
		t == nil || t.turn.ID != id || t.stopping || t.heldResult != nil || t.turn.Presentation != "" {
		return nil
	}
	return t
}

func (r *runtime) SteerDeferred(ctx context.Context, s domain.Session, turnID, operationID string, input harness.TurnInput, claim func() error) (<-chan error, error) {
	ended := &harness.DeliveryError{Message: "Claude Code 当前轮次已经结束"}
	if operationID == "" {
		return nil, errors.New("追加输入缺少稳定操作标识")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.Goal != nil {
		return nil, harness.Unsupported("steer-goal")
	}
	prepared, err := r.prepareInput(ctx, s, input)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	target := r.steerTarget(s, turnID)
	r.mu.Unlock()
	if target == nil {
		return nil, ended
	}
	if claim != nil {
		if err := claim(); err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.steerTarget(s, turnID)
	if t != target {
		return nil, ended
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := nativeInputID(s.ThreadID, operationID)
	if t.pendingSteer == nil {
		t.pendingSteer, t.acceptedSteer = make(map[string]steerInput), make(map[string]string)
	}
	if _, pending := t.pendingSteer[id]; pending || t.acceptedSteer[id] != "" {
		return nil, errors.New("已登记此追加输入")
	}
	receipt := make(chan error, 1)
	t.pendingSteer[id] = steerInput{operation: operationID, receipt: receipt}
	message := map[string]any{"type": "steer", "thread": s.ThreadID, "uuid": id, "turn": t.uuid, "content": prepared.Blocks}
	addSkillCommand(message, prepared)
	if err := r.send(message); err != nil {
		return nil, deliveryUnknown()
	}
	return receipt, nil
}

func (r *runtime) observeSteers(t *activeTurn, in frame) error {
	if len(t.pendingSteer) == 0 {
		return nil
	}
	switch in.Type {
	case "command_lifecycle":
		switch in.State {
		case "started":
			r.confirmSteer(t, in.CommandUUID)
		case "cancelled", "discarded", "refused":
			r.rejectSteer(t, in.CommandUUID)
		}
	case "user":
		if in.IsReplay {
			r.confirmSteer(t, in.UUID)
		}
	default:
		r.confirmSteer(t, in.UserMessageUUID)
		for _, id := range in.UserMessageUUIDs {
			r.confirmSteer(t, id)
		}
	}
	if t.heldResult != nil && len(t.pendingSteer) == 0 {
		result := t.heldResult
		t.heldResult = nil
		return r.processFrame(*result)
	}
	return nil
}

func (r *runtime) confirmSteer(t *activeTurn, id string) {
	pending, ok := t.pendingSteer[id]
	if !ok {
		return
	}
	delete(t.pendingSteer, id)
	t.acceptedSteer[id] = pending.operation
	if t.heldResult != nil {
		accumulateUsage(&t.segmentUsage, t.heldResult.Usage)
		t.heldResult = nil
	}
	received := t.turn
	received.Status = domain.ExecutionUnknown
	r.recordReceipt(pending.operation, received)
	pending.receipt <- nil
}

func (*runtime) rejectSteer(t *activeTurn, id string) {
	if pending, ok := t.pendingSteer[id]; ok {
		delete(t.pendingSteer, id)
		pending.receipt <- &harness.DeliveryError{Message: "Claude Code 没有开始处理这条追加输入"}
	}
}

func (*runtime) finishSteers(t *activeTurn) {
	for id, pending := range t.pendingSteer {
		pending.receipt <- deliveryUnknown()
		delete(t.pendingSteer, id)
	}
}

func (r *runtime) watchSteerDisposition(t *activeTurn, held *frame) {
	time.AfterFunc(5*time.Second, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.turn == t && t.heldResult == held && len(t.pendingSteer) > 0 && !r.closing && !r.Stopped() {
			// A missing disposition leaves delivery unknown. Retire this
			// private query so unread input cannot execute after settlement.
			_ = r.input.Close()
		}
	})
}

func accumulateUsage(total *inputUsage, add inputUsage) {
	total.Input += add.Input
	total.Output += add.Output
	total.Cache += add.Cache
}

var _ harness.InputHandoffRuntime = (*runtime)(nil)
