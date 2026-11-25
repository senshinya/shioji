package claudecode

import (
	"encoding/json"
	"strings"

	"shioji/internal/domain"
)

// The approved tool input can omit the plan text. The completed assistant
// tool-use record carries the proposal even when Claude strips it before the
// host permission callback.
func (r *runtime) publishPlan(turn *activeTurn, call string, raw json.RawMessage) error {
	if !turn.plan || turn.planEmitted[call] {
		return nil
	}
	var proposal struct{ Plan string }
	if json.Unmarshal(raw, &proposal) != nil || strings.TrimSpace(proposal.Plan) == "" {
		return nil
	}
	text := []rune(r.redact(proposal.Plan))
	plan := domain.Plan{TurnID: turn.turn.ID, Truncated: len(text) > 128<<10, Narrative: string(text[:min(len(text), 128<<10)])}
	if turn.planEmitted == nil {
		turn.planEmitted = make(map[string]bool)
	}
	turn.planEmitted[call] = true
	return r.emit(domain.Event{Type: "plan.text", Plan: &plan, Text: plan.Narrative})
}
