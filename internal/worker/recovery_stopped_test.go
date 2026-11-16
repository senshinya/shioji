package worker

import (
	"shioji/internal/domain"
	"testing"
)

func TestExplicitlyStoppedHistorySettlesOnlyCoveredTurns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		covered bool
		want    domain.ExecutionState
		state   domain.SourceState
	}{
		{"owned execution", true, "interrupted", domain.SourceIdle},
		{"unconfirmed source", false, "running", domain.SourceActive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := domain.RecoveryHistory{State: domain.SourceActive, ExplicitlyStopped: true, Turns: []domain.Turn{{ID: "turn", Status: "running"}}, ManagedStoppedTurns: map[string]bool{"turn": tc.covered}}
			settleExplicitStoppedHistory(&source, false)
			if source.Turns[0].Status != tc.want || source.State != tc.state {
				t.Fatal("force close recovery cannot return to a usable state", source)
			}
		})
	}
	manual := domain.RecoveryHistory{State: domain.SourceActive, ExplicitlyStopped: true, Turns: []domain.Turn{{ID: "uncertain", Status: "running"}}}
	settleExplicitStoppedHistory(&manual, true)
	if manual.State != domain.SourceIdle || manual.Turns[0].Status != "abandoned" {
		t.Fatal("explicit local end remained permanently blocked", manual)
	}
}
