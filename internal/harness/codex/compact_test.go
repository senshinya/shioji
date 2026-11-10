package codex

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/testutil"
	"testing"
	"time"
)

func TestRealManualCompactionUsesOwnTurnWithoutUserInput(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("Codex unavailable")
	}
	p := testutil.NewProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	events := make(chan domain.Event, 128)
	r, err := Start(ctx, Options{Home: p.Home, OnEvent: func(e domain.Event) error { events <- e; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.ready.Models = []domain.Model{{ID: "fixture-model"}}
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := domain.Session{ID: "compact-session", Config: domain.RunConfig{Model: "fixture-model", Permission: "read-only"}, Context: domain.LaunchContext{CWD: cwd}}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	root, err := r.Submit(ctx, s, "first", "Explain red-black tree rotation")
	if err != nil {
		t.Fatal(err)
	}
	await := func() []domain.Event {
		var out []domain.Event
		for {
			select {
			case e := <-events:
				out = append(out, e)
				if e.Turn != nil && !e.Turn.Status.Active() {
					return out
				}
			case <-ctx.Done():
				t.Fatal("no terminal source turn", out)
				return nil
			}
		}
	}
	await()
	adapter := &adapterRuntime{Runtime: r}
	var compactor harness.ContextCompactor = adapter
	if !adapter.Readiness().Capabilities.Compact {
		t.Fatal("compatible source compaction unavailable")
	}
	err = compactor.Compact(ctx, s)
	if err != nil {
		t.Fatal("real compaction rejected", err)
	}
	got := await()
	found := false
	for _, e := range got {
		b, _ := json.Marshal(e)
		t.Log(string(b))
		if e.Item != nil && e.Item.Presentation == "compaction" {
			found = true
			if e.Item.TurnID == root {
				t.Fatal("compaction reused user turn")
			}
		}
		if e.Item != nil && e.Item.Kind == "user" {
			t.Fatal("compaction fabricated a user message", e.Item)
		}
		if e.Turn != nil && e.Turn.Presentation != "compaction" {
			t.Fatal("standalone compaction has ordinary reply lifecycle", e.Turn)
		}
	}
	if !found {
		t.Fatal("source did not expose a compaction item", got)
	}
	history, err := r.Inspect(ctx, s)
	if err != nil {
		var raw json.RawMessage
		r.call(ctx, "thread/read", map[string]string{"threadId": s.ThreadID}, &raw)
		t.Logf("source read for %s %s: %s", s.ThreadID, s.Context.CWD, raw)
		t.Fatal(err)
	}
	t.Logf("persisted history: %+v", history)
	if history.Inputs["input:first"] != root {
		t.Fatal("compression lost accepted input identity", history.Inputs)
	}
	observation, err := compactor.InspectCompaction(ctx, s, domain.ContextAction{OperationID: "compact", SourceRef: s.ThreadID, BeforeTurnID: root})
	if err != nil || observation.State != "completed" || observation.TurnID == "" || observation.TurnID == root {
		t.Fatal("compaction receipt cannot be recovered", observation, err)
	}
}
