package store_test

import (
	"encoding/json"
	"shioji/internal/domain"
	"testing"
)

func TestContextSnapshotSurvivesReopenWithoutChangingTurnState(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "context", WorkspaceID: w.ID, Text: "explain"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old", "new"} {
		_, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: id, Status: "completed"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: id, TurnID: id, Kind: "assistant", Text: "reply", Final: true}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"new", "old"} {
		var e domain.Event
		json.Unmarshal([]byte(`{"type":"context.usage","contextUsage":{"turnId":"`+id+`","usedTokens":25000,"limitTokens":200000}}`), &e)
		applied, err := s.Apply(op.SessionID, e)
		if err != nil {
			t.Fatal("context report rejected", err)
		}
		if applied.Session.Status != "completed" {
			t.Fatal("context report changed execution state", applied.Session)
		}
		if applied.ContextUsage == nil || applied.ContextUsage.TurnID != "new" {
			t.Fatal("watch emitted an older context report", applied.ContextUsage)
		}
	}
	d, err := s.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(d.Session)
	var saved struct {
		ContextUsage struct {
			TurnID      string
			UsedTokens  int64
			LimitTokens int64
		}
	}
	json.Unmarshal(b, &saved)
	if saved.ContextUsage.TurnID != "new" || saved.ContextUsage.UsedTokens != 25000 || len(d.Items) != 3 || len(d.Turns) != 2 {
		t.Fatalf("late old report replaced context or created transcript items: %s %+v", b, d)
	}
	events, err := s.Events(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var restored domain.Session
	for _, e := range events {
		if e.Session != nil && e.Session.ID == op.SessionID {
			restored = *e.Session
		}
	}
	b, _ = json.Marshal(restored)
	json.Unmarshal(b, &saved)
	if saved.ContextUsage.TurnID != "new" {
		t.Fatal("watch replay lost context snapshot")
	}
	plain := d.Session
	plain.ContextUsage = nil
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "session", Session: &plain}); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Session(op.SessionID, 0, 100)
	if d.Session.ContextUsage == nil {
		t.Fatal("ordinary runtime session refresh erased the context report")
	}
}

func TestContextReportIsInvalidatedByModelChangeButNotEffortChange(t *testing.T) {
	for _, model := range []string{"first", "second"} {
		s := open(t)
		w := workspace(t, s)
		op, err := s.Accept(domain.Submission{OperationID: "initial", WorkspaceID: w.ID, Text: "hello", Config: domain.RunConfig{Model: "first", Effort: "low"}})
		if err != nil {
			t.Fatal(err)
		}
		s.SetOperation(op.ID, "confirmed", "")
		s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "completed"}})
		used, limit := int64(25), int64(100)
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "context.usage", ContextUsage: &domain.ContextUsage{TurnID: "turn", UsedTokens: &used, LimitTokens: &limit}}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Accept(domain.Submission{OperationID: "next", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "next", Config: domain.RunConfig{Model: model, Effort: "medium"}}); err != nil {
			t.Fatal(err)
		}
		d, _ := s.Session(op.SessionID, 0, 100)
		if (d.Session.ContextUsage != nil) != (model == "first") {
			t.Fatal("reused a different model's context or erased an effort-only report", model, d.Session.ContextUsage)
		}
		refresh := d.Session
		refresh.ContextUsageAfterTurnOrder = 0
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "session", Session: &refresh}); err != nil {
			t.Fatal(err)
		}
		// A report can arrive after the next submission has changed models.
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "context.usage", ContextUsage: &domain.ContextUsage{TurnID: "turn", UsedTokens: &used, LimitTokens: &limit}}); err != nil {
			t.Fatal(err)
		}
		d, _ = s.Session(op.SessionID, 0, 100)
		if model != "first" && d.Session.ContextUsage != nil {
			t.Fatal("late report restored the previous model's context", d.Session.ContextUsage)
		}
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "next-turn", Status: "running"}}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "context.usage", ContextUsage: &domain.ContextUsage{TurnID: "next-turn", UsedTokens: &used, LimitTokens: &limit}}); err != nil {
			t.Fatal(err)
		}
		d, _ = s.Session(op.SessionID, 0, 100)
		if d.Session.ContextUsage == nil || d.Session.ContextUsage.TurnID != "next-turn" {
			t.Fatal("new model's current report was rejected", d.Session.ContextUsage)
		}
	}
}
