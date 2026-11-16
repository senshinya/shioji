package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/store"
	"sync/atomic"
	"testing"
	"time"
)

type replyHistoryRuntime struct {
	*memoryRuntime
	history domain.RecoveryHistory
	err     error
	inspect func()
	reads   atomic.Int32
}

// Use the installed app-server against a local SSE endpoint: the first sample
// streams a prefix then loses its connection, and the retry completes under a
// different item ID. No account or external model service is involved.
func TestReplyReconciliationWithRealCodexStreamRetry(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"fixture-model"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		n := requests.Add(1)
		send := func(event map[string]any) {
			b, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], b)
		}
		send(map[string]any{"type": "response.created", "response": map[string]any{"id": fmt.Sprint("response-", n)}})
		id := fmt.Sprint("message-", n)
		text := "已加入方案第 10.1 节"
		item := map[string]any{"type": "message", "id": id, "role": "assistant", "phase": "final_answer", "content": []any{}}
		send(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
		send(map[string]any{"type": "response.output_text.delta", "item_id": id, "output_index": 0, "content_index": 0, "delta": text})
		if n == 1 {
			return
		}
		text += "：完整回复。"
		item["content"] = []any{map[string]any{"type": "output_text", "text": text}}
		send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		send(map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprint("response-", n), "usage": map[string]any{"input_tokens": 4, "output_tokens": 8, "total_tokens": 12}}})
	}))
	defer provider.Close()
	home := t.TempDir()
	config := fmt.Sprintf("model=\"fixture-model\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Fixture\"\nbase_url=\"%s/v1\"\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\nstream_max_retries=2\n[features]\nplugins=false\n", provider.URL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.CreateWorkspace("Retry", []string{cwd})
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.Accept(domain.Submission{OperationID: "retry-input", WorkspaceID: w.ID, Text: "hello", Config: domain.RunConfig{Model: "fixture-model", Permission: "read-only"}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := &Server{store: db, ctx: ctx, cancel: cancel, recoveryWake: make(chan struct{}, 1), recoveryChecked: map[string]recoveryAttempt{}, runtimes: map[string]harness.Runtime{}}
	var sawPartial atomic.Bool
	r, err := (codex.Adapter{Home: home}).Start(ctx, harness.StartOptions{OnEvent: func(e domain.Event) error {
		if e.Item != nil && e.Item.ID != "" && e.Item.Text == "已加入方案第 10.1 节" && !e.Item.Final {
			sawPartial.Store(true)
		}
		_, err := db.Apply(e.SessionID, e)
		if e.Turn != nil && e.Turn.Status == "completed" {
			s.wakeRecovery()
		}
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	session := d.Session
	session.Context.CWD = cwd
	session.ThreadID, err = r.Create(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Apply(session.ID, domain.Event{Type: "session", Session: &session}); err != nil {
		t.Fatal(err)
	}
	s.runtimes[session.ID] = r
	if _, err = r.Submit(ctx, session, "retry-input", harness.TurnInput{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	s.wg.Add(1)
	go s.recoveryLoop()
	defer func() { cancel(); s.wg.Wait() }()
	for {
		d, err = db.Session(session.ID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		visible := []string{}
		retired := false
		for _, i := range d.Items {
			if i.Kind == "assistant" && !domain.HistorySpacer(i) {
				visible = append(visible, i.Text)
			}
			retired = retired || i.Status == "superseded"
		}
		if retired && d.Session.Status == "completed" {
			if len(visible) != 1 || visible[0] != "已加入方案第 10.1 节：完整回复。" {
				t.Fatal("retry duplicated the actual app-server output", visible)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("retry did not settle", ctx.Err(), d.Items)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !sawPartial.Load() || requests.Load() != 2 {
		t.Fatal("fixture did not exercise a streamed retry", sawPartial.Load(), requests.Load())
	}
}

func (r *replyHistoryRuntime) Inspect(context.Context, domain.Session) (domain.RecoveryHistory, error) {
	r.reads.Add(1)
	if r.inspect != nil {
		r.inspect()
	}
	return r.history, r.err
}

func replyReconciliationFixture(t *testing.T) (*Server, domain.Session, *replyHistoryRuntime) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	w, err := db.CreateWorkspace("Replies", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.AcceptForHarness(domain.Submission{OperationID: "input", WorkspaceID: w.ID, Text: "keep user input", Config: domain.RunConfig{Model: "alpha-model"}}, "alpha", domain.LaunchContext{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	session := d.Session
	session.ThreadID = "opaque-source"
	if _, err = db.Apply(session.ID, domain.Event{Type: "session", Session: &session}); err != nil {
		t.Fatal(err)
	}
	items := []domain.Item{
		{ID: "legitimate", TurnID: "finished", Kind: "assistant", Text: "已加入方案第 10.1 节", Final: true, Status: "completed"},
		{ID: "discarded", TurnID: "finished", Kind: "assistant", Text: "已加入方案第 10.1 节", Status: "running"},
		{ID: "answer", TurnID: "finished", Kind: "assistant", Text: "已加入方案第 10.1 节：完整回复", Final: true, Status: "completed"},
		{ID: "tool", TurnID: "finished", Kind: "tool", Text: "keep tool output", Status: "running"},
		{ID: "interrupted", TurnID: "stopped", Kind: "assistant", Text: "keep interrupted output", Status: "running"},
		{ID: "active", TurnID: "next", Kind: "assistant", Text: "keep current output", Status: "running"},
	}
	for _, i := range items {
		if _, err = db.Apply(session.ID, domain.Event{Type: "item.upsert", Item: &i}); err != nil {
			t.Fatal(err)
		}
	}
	for _, turn := range []domain.Turn{{ID: "finished", Status: "completed"}, {ID: "stopped", Status: "interrupted"}, {ID: "next", Status: "running"}} {
		if _, err = db.Apply(session.ID, domain.Event{Type: "turn", Turn: &turn}); err != nil {
			t.Fatal(err)
		}
	}
	r := &replyHistoryRuntime{memoryRuntime: &memoryRuntime{adapter: memoryHarness{id: "alpha"}, done: make(chan struct{})}, history: domain.RecoveryHistory{Available: true, CompleteItemTurns: []string{"finished"}, State: domain.SourceActive, Turns: []domain.Turn{{ID: "finished", Status: "completed"}, {ID: "next", Status: "running"}}, Items: []domain.Item{items[0], items[2], items[3], items[5]}}}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{store: db, ctx: ctx, cancel: cancel, recoveryWake: make(chan struct{}, 1), recoveryChecked: map[string]recoveryAttempt{}, runtimes: map[string]harness.Runtime{session.ID: r}}
	t.Cleanup(func() { cancel(); s.wg.Wait() })
	return s, session, r
}

func runReplyRecovery(t *testing.T, s *Server, done func() bool) {
	t.Helper()
	s.wg.Add(1)
	go s.recoveryLoop()
	deadline := time.Now().Add(2 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("completed reply was not reconciled")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCompletedReplyReconcilesDiscardedStreamWithoutDeduplicatingRealMessages(t *testing.T) {
	s, session, r := replyReconciliationFixture(t)
	before, _ := s.store.Snapshot()
	runReplyRecovery(t, s, func() bool { i, _ := s.store.Item(session.ID, "discarded"); return i.Final })
	for id, want := range map[string]string{"discarded": "", "legitimate": "已加入方案第 10.1 节", "answer": "已加入方案第 10.1 节：完整回复", "tool": "keep tool output", "interrupted": "keep interrupted output", "active": "keep current output", "input:input": "keep user input"} {
		i, err := s.store.Item(session.ID, id)
		if err != nil || i.Text != want {
			t.Fatalf("%s changed: %+v, %v", id, i, err)
		}
	}
	i, _ := s.store.Item(session.ID, "discarded")
	if !domain.HistorySpacer(i) {
		t.Fatal("discarded reply remains visible", i)
	}
	events, err := s.store.Events(before.Sequence, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Item != nil && e.Item.ID == "discarded" && e.Item.Final && e.Item.Text == "" {
			found = true
		}
	}
	if !found {
		t.Fatal("connected clients did not receive the correction")
	}
	if r.reads.Load() != 1 {
		t.Fatal("unexpected history reads", r.reads.Load())
	}
	// A late replay of the abandoned stream must not restore the stale text.
	_, err = s.store.Apply(session.ID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "discarded", TurnID: "finished", Kind: "assistant", Text: "late stream", Status: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	i, _ = s.store.Item(session.ID, "discarded")
	if i.Text != "" || !i.Final {
		t.Fatal("late stream resurrected discarded reply", i)
	}
}

func TestReplyReconciliationKeepsUnconfirmedOrChangedEvidence(t *testing.T) {
	for _, mode := range []string{"unavailable", "read-error", "missing-turn", "running-turn", "changed-item", "changed-source"} {
		t.Run(mode, func(t *testing.T) {
			s, session, r := replyReconciliationFixture(t)
			switch mode {
			case "unavailable":
				r.history.Available = false
			case "read-error":
				r.err = errors.New("history unavailable")
			case "missing-turn":
				r.history.Turns = nil
			case "running-turn":
				r.history.Turns[0].Status = "running"
			case "changed-item":
				r.inspect = func() {
					_, err := s.store.Apply(session.ID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "discarded", TurnID: "finished", Kind: "assistant", Text: "newer event", Status: "running"}})
					if err != nil {
						t.Error(err)
					}
				}
			case "changed-source":
				r.inspect = func() {
					session.ThreadID = "replacement-source"
					_, err := s.store.Apply(session.ID, domain.Event{Type: "session", Session: &session})
					if err != nil {
						t.Error(err)
					}
				}
			}
			runReplyRecovery(t, s, func() bool { return r.reads.Load() > 0 })
			s.cancel()
			s.wg.Wait()
			i, _ := s.store.Item(session.ID, "discarded")
			if i.Final || i.Text == "" {
				t.Fatal("unconfirmed content was removed", i)
			}
		})
	}
}

func TestReplyReconciliationFinalizesAnItemConfirmedInHistory(t *testing.T) {
	s, session, r := replyReconciliationFixture(t)
	r.history.Items = append(r.history.Items, domain.Item{ID: "discarded", TurnID: "finished", Kind: "assistant", Text: "confirmed complete content", Status: "completed", Final: true})
	runReplyRecovery(t, s, func() bool { i, _ := s.store.Item(session.ID, "discarded"); return i.Final })
	i, _ := s.store.Item(session.ID, "discarded")
	if i.Text != "confirmed complete content" {
		t.Fatal("retained item was discarded", i)
	}
}

func TestReplyReconciliationRequiresExplicitCompleteItemSetEvidence(t *testing.T) {
	s, session, r := replyReconciliationFixture(t)
	// Available history can combine a running source page with a newer local
	// completed state. That does not prove the page contains every item.
	r.history = domain.RecoveryHistory{Available: true, State: domain.SourceActive, Turns: []domain.Turn{{ID: "finished", Status: "completed"}}}
	runReplyRecovery(t, s, func() bool { return r.reads.Load() > 0 })
	s.cancel()
	s.wg.Wait()
	i, err := s.store.Item(session.ID, "discarded")
	if err != nil || i.Final || i.Text == "" {
		t.Fatal("available but incomplete history removed output", i, err)
	}
}
