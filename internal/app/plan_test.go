package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
)

func TestPlanProjectionReplacesOnlyItsOwnSessionAndPreservesDraft(t *testing.T) {
	m := model()
	m.SessionID = "s"
	m.Drafts["session:s"] = "keep typing"
	m.Details["s"] = domain.SessionDetail{}
	m.Details["other"] = domain.SessionDetail{}
	for i, steps := range []string{`[{"text":"检查","status":"running"}]`, `[{"text":"验证","status":"pending"}]`, `[]`} {
		var event domain.Event
		json.Unmarshal([]byte(`{"type":"plan","sessionId":"s","plan":{"turnId":"t","steps":`+steps+`}}`), &event)
		event.Sequence = m.Sequence + 1
		if err := m.ApplyEvent(event); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(m.Details["s"])
		var detail struct {
			Plan *struct {
				Steps []any `json:"steps"`
			} `json:"plan"`
		}
		json.Unmarshal(b, &detail)
		if detail.Plan == nil || i == 2 && len(detail.Plan.Steps) != 0 {
			t.Fatal("plan replacement/clear ignored", string(b))
		}
	}
	b, _ := json.Marshal(m.Details["other"])
	var untouched map[string]any
	json.Unmarshal(b, &untouched)
	if untouched["plan"] != nil || m.Drafts["session:s"] != "keep typing" {
		t.Fatal("plan changed another session or composer")
	}
}

func TestHistoryHydrationRetainsLatestPlanAcrossPageBoundary(t *testing.T) {
	latest := &domain.Plan{TurnID: "second", Steps: []domain.PlanStep{{Text: "new task", Status: "running"}}}
	c := peer(t, func(codec *protocol.Codec, _ string) {
		for i := 0; i < 3; i++ {
			var f protocol.Frame
			if codec.Read(&f) != nil {
				return
			}
			switch i {
			case 0:
				result(codec, f, domain.SessionDetail{Session: domain.Session{ID: "s"}, Sequence: 1, HasMore: true, Items: []domain.Item{{ID: "a", Order: 1}}, Plan: &domain.Plan{TurnID: "first"}})
			case 1:
				result(codec, f, domain.SessionDetail{Session: domain.Session{ID: "s"}, Sequence: 2, Items: []domain.Item{{ID: "b", Order: 2}}, Plan: latest})
			case 2:
				result(codec, f, protocol.EventPage{Events: []domain.Event{{Sequence: 2, SessionID: "s", Type: "plan", Plan: latest}}})
			}
		}
	})
	detail, err := hydrateSession(context.Background(), c, "s")
	if err != nil || detail.Plan == nil || detail.Plan.TurnID != "second" || detail.Plan.Steps[0].Text != "new task" {
		t.Fatal("history hydration lost latest plan", detail.Plan, err)
	}
}

func TestInspectorSplitHeightSurvivesClientRestart(t *testing.T) {
	m := model()
	var state map[string]any
	data, _ := m.EncodeState()
	json.Unmarshal(data, &state)
	state["inspectorSplitHeight"] = 240
	data, _ = json.Marshal(state)
	path := filepath.Join(t.TempDir(), "client.json")
	if err := WriteState(path, data); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	data, _ = restored.EncodeState()
	state = nil
	json.Unmarshal(data, &state)
	if state["inspectorSplitHeight"] != float64(240) {
		t.Fatal("task panel height not persisted", string(data))
	}
}
