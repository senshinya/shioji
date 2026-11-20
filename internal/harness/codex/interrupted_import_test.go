package codex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
	"shioji/internal/testutil"
)

func TestInterruptedImportContinuesWithOriginalPrompt(t *testing.T) {
	p := testutil.NewProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	terminal := make(chan domain.Turn, 8)
	options := Options{Home: p.Home, OnEvent: func(e domain.Event) error {
		if e.Turn != nil && (e.Turn.Status == "interrupted" || e.Turn.Status == "completed") {
			terminal <- *e.Turn
		}
		return nil
	}}
	source, err := Start(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := domain.Session{ID: "outside", Context: domain.LaunchContext{CWD: dir}, Config: domain.RunConfig{Permission: "read-only", Model: "fixture-model"}}
	s.ThreadID, err = source.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	id, err := source.Submit(ctx, s, "first-input", "SHIOJI_SLOW Original interrupted prompt")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := source.Interrupt(ctx, s, id); err != nil {
		t.Fatal(err)
	}
	select {
	case turn := <-terminal:
		if turn.ID != id || turn.Status != "interrupted" {
			t.Fatal(turn)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	close(p.Release)
	r, err := Start(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	pv, err := r.ImportConversation(ctx, s.Context, s.ThreadID)
	if err != nil || pv.ResumeRef == "" || len(pv.Turns) != 1 || pv.Turns[0].Status != "interrupted" {
		t.Fatalf("interrupted-only history cannot continue: %+v, %v", pv, err)
	}
	destination := s
	destination.ID, destination.ThreadID = "imported", ""
	destination.Origin = &domain.ConversationOrigin{SourceRef: s.ThreadID, ResumeRef: pv.ResumeRef}
	destination.ThreadID, err = r.PrepareConversation(ctx, destination)
	if err != nil || destination.ThreadID == "" || destination.ThreadID == s.ThreadID {
		t.Fatalf("expected separate prepared thread, got %q, %v", destination.ThreadID, err)
	}
	if err = r.Resume(ctx, destination); err != nil {
		t.Fatal(err)
	}
	id, err = r.Submit(ctx, destination, "second-input", "Continue with a fresh message")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case turn := <-terminal:
		if turn.ID != id || turn.Status != "completed" {
			t.Fatal(turn)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	inputs := p.Inputs()
	if !strings.Contains(inputs[len(inputs)-1], "Original interrupted prompt") || !strings.Contains(inputs[len(inputs)-1], "Continue with a fresh message") {
		t.Fatal("new model request did not carry both imported and new input")
	}
	unchanged, err := r.ImportConversation(ctx, s.Context, s.ThreadID)
	if err != nil || unchanged.ResumeRef != pv.ResumeRef {
		t.Fatal("continuation changed the source history", err)
	}
}

func TestInterruptedImportRejectsChangesBeforeAndDuringFork(t *testing.T) {
	r := &Runtime{protocol: &protocolSupport{methods: map[string]bool{"thread/read": true, "thread/turns/list": true, "thread/fork": true, "thread/unsubscribe": true, "thread/loaded/list": true}}, pending: map[string]chan wire.Message{}, done: make(chan struct{})}
	var sourceChanged, copyChanged, active bool
	var forks, released int
	r.stdin = workInputWriter{write: func(b []byte) (int, error) {
		var q wire.Message
		var args map[string]any
		json.Unmarshal(b, &q)
		json.Unmarshal(q.Params, &args)
		thread := args["threadId"]
		var result any
		switch q.Method {
		case "thread/read":
			status := "notLoaded"
			if thread == "source" && active {
				status = "active"
			}
			result = map[string]any{"thread": map[string]any{"id": thread, "cwd": "/project", "status": map[string]string{"type": status}}}
		case "thread/turns/list":
			content := "partial answer"
			if thread == "source" && sourceChanged || thread == "copy" && copyChanged {
				content += " extra output"
			}
			result = map[string]any{"data": []any{map[string]any{"id": "t", "status": "interrupted", "itemsView": "full", "items": []any{map[string]any{"id": "m", "type": "agentMessage", "text": content}}}}}
		case "thread/fork":
			if _, ok := args["lastTurnId"]; ok {
				t.Error("interrupted import used an explicit turn boundary")
			}
			forks++
			result = map[string]any{"thread": map[string]any{"id": "copy", "forkedFromId": "source"}, "cwd": "/project"}
		case "thread/unsubscribe":
			if thread != "copy" {
				t.Errorf("unsubscribed source %v", thread)
			}
			released++
			result = map[string]any{}
		case "thread/loaded/list":
			result = map[string]any{"data": []string{}}
		default:
			t.Errorf("unexpected method: %s", q.Method)
		}
		raw, _ := json.Marshal(result)
		r.mu.Lock()
		ch := r.pending[string(q.ID)]
		r.mu.Unlock()
		ch <- wire.Message{Result: raw}
		return len(b), nil
	}}
	ctx := context.Background()
	launch := domain.LaunchContext{CWD: "/project"}
	pv, err := r.ImportConversation(ctx, launch, "source")
	if err != nil || pv.ResumeRef == "" {
		t.Fatalf("import is not continuable: %v, %s", err, pv.Reason)
	}
	s := domain.Session{Context: launch, Origin: &domain.ConversationOrigin{SourceRef: "source", ResumeRef: pv.ResumeRef}}
	active = true
	if id, err := r.PrepareConversation(ctx, s); err == nil || id != "" || forks != 0 {
		t.Fatal("active source was forked", id, err, forks)
	}
	active, sourceChanged = false, true
	if id, err := r.PrepareConversation(ctx, s); err == nil || id != "" || forks != 0 {
		t.Fatal("changed source was forked", id, err, forks)
	}
	sourceChanged, copyChanged = false, true
	if id, err := r.PrepareConversation(ctx, s); err == nil || id != "" || forks != 1 || released != 1 {
		t.Fatal("mismatched fork escaped verification or was left loaded", id, err, forks, released)
	}
}
