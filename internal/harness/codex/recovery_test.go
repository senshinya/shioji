package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
	"shioji/internal/testutil"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInspectCompleteItemEvidenceDoesNotFollowNewerLocalTurnState(t *testing.T) {
	for _, state := range []string{"inProgress", "completed", "summary"} {
		t.Run(state, func(t *testing.T) {
			n := NewNormalizer()
			n.Bind("thread", "session", false)
			n.turns["turn"] = domain.Turn{ID: "turn", Status: "completed"}
			n.items["turn/message"] = domain.Item{ID: "turn/message", TurnID: "turn", Kind: "assistant", Text: "transient", Status: "running"}
			r := &Runtime{normalizer: n, protocol: &protocolSupport{methods: map[string]bool{"thread/read": true, "thread/turns/list": true}}, pending: map[string]chan wire.Message{}, done: make(chan struct{})}
			r.stdin = workInputWriter{write: func(b []byte) (int, error) {
				var q wire.Message
				if err := json.Unmarshal(b, &q); err != nil {
					t.Fatal(err)
				}
				result := json.RawMessage(`{"thread":{"id":"thread","cwd":"/device/project","status":{"type":"active"}}}`)
				if q.Method == "thread/turns/list" {
					view, status := "full", state
					if state == "summary" {
						view, status = "summary", "completed"
					}
					result, _ = json.Marshal(map[string]any{"data": []any{map[string]any{"id": "turn", "status": status, "itemsView": view, "items": []any{map[string]any{"type": "agentMessage", "id": "message", "text": "durable complete"}}}}})
				} else if q.Method != "thread/read" {
					t.Fatal("inspection performed an unexpected operation", q.Method)
				}
				r.mu.Lock()
				ch := r.pending[string(q.ID)]
				r.mu.Unlock()
				ch <- wire.Message{Result: result}
				return len(b), nil
			}}
			history, err := r.Inspect(context.Background(), domain.Session{ID: "session", ThreadID: "thread", Context: domain.LaunchContext{CWD: "/device/project"}})
			if state == "summary" {
				if err == nil {
					t.Fatal("summary history granted complete-item evidence")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if history.Turns[0].Status != "completed" {
				t.Fatal("newer live execution state was lost", history.Turns)
			}
			if state == "inProgress" {
				if len(history.CompleteItemTurns) != 0 {
					t.Fatal("local completion upgraded an incomplete source snapshot", history.CompleteItemTurns)
				}
			} else if len(history.CompleteItemTurns) != 1 || history.CompleteItemTurns[0] != "turn" || !history.Items[0].Final || history.Items[0].Text != "durable complete" {
				t.Fatal("full completed source content was replaced by transient memory", history)
			}
		})
	}
}

func TestInspectPagesHistoryPastOneHundredTurnsWithoutCallingModel(t *testing.T) {
	p := testutil.NewProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	completed := make(chan string, 128)
	r, err := Start(ctx, Options{Home: p.Home, OnEvent: func(e domain.Event) error {
		if e.Turn != nil && e.Turn.Status == "completed" {
			completed <- e.Turn.ID
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := domain.Session{ID: "paged", Context: domain.LaunchContext{CWD: folder}, Config: domain.RunConfig{Model: r.Readiness().Models[0].ID, Permission: "read-only"}}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	var last string
	for i := 0; i < 101; i++ {
		last, err = r.Submit(ctx, s, fmt.Sprintf("page-%d", i), "hello")
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-completed:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	before := len(p.Inputs())
	history, err := r.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Turns) != 101 || history.Inputs["input:page-100"] != last || len(p.Inputs()) != before {
		t.Fatalf("history pagination missing tail or replayed input: turns=%d, last=%q", len(history.Turns), history.Inputs["input:page-100"])
	}
	wrong := s
	wrong.Context.CWD = "/wrong-device-path"
	if _, err = r.Inspect(ctx, wrong); err == nil {
		t.Fatal("foreign execution directory accepted")
	}
}

// A long tool-heavy thread can exceed the stream limit when many full turns
// are fetched at once. Recovery must read every page without losing its reader.
func TestInspectToolHeavyHistoryStaysWithinFrameLimit(t *testing.T) {
	cmd := exec.Command("/bin/cat")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	r := &Runtime{cmd: cmd, normalizer: NewNormalizer(), protocol: &protocolSupport{methods: map[string]bool{"thread/read": true, "thread/turns/list": true}}, pending: map[string]chan wire.Message{}, done: make(chan struct{})}
	r.stdin = workInputWriter{write: func(b []byte) (int, error) {
		var q wire.Message
		if err := json.Unmarshal(b, &q); err != nil {
			return 0, err
		}
		var value any = map[string]any{"thread": map[string]any{"id": "thread", "cwd": "/device/project", "status": map[string]any{"type": "notLoaded"}}}
		if q.Method == "thread/turns/list" {
			var args struct {
				Limit  int
				Cursor string
			}
			if err := json.Unmarshal(q.Params, &args); err != nil {
				return 0, err
			}
			start, _ := strconv.Atoi(args.Cursor)
			end := min(start+args.Limit, 24)
			data := []any{}
			for i := start; i < end; i++ {
				data = append(data, map[string]any{"id": fmt.Sprintf("turn-%d", i), "status": "completed", "itemsView": "full", "items": []any{map[string]any{"id": "tool", "type": "commandExecution", "command": "large tool output", "status": "completed", "aggregatedOutput": strings.Repeat("x", 1<<20)}}})
			}
			page := map[string]any{"data": data}
			if end < 24 {
				page["nextCursor"] = strconv.Itoa(end)
			}
			value = page
		} else if q.Method != "thread/read" {
			return 0, fmt.Errorf("unexpected history mutation: %s", q.Method)
		}
		result, err := json.Marshal(value)
		if err != nil {
			return 0, err
		}
		frame, err := json.Marshal(wire.Message{ID: q.ID, Result: result})
		if err != nil {
			return 0, err
		}
		go func() { _, _ = writer.Write(append(frame, '\n')) }()
		return len(b), nil
	}}
	go r.read(reader)
	defer func() {
		writer.Close()
		r.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h, err := r.Inspect(ctx, domain.Session{ID: "session", ThreadID: "thread", Context: domain.LaunchContext{CWD: "/device/project"}})
	if err != nil || len(h.Turns) != 24 || len(h.Items) != 24 {
		t.Fatalf("long history could not recover: turns=%d items=%d err=%v", len(h.Turns), len(h.Items), err)
	}
	if h.Turns[23].ID != "turn-23" || r.Stopped() {
		t.Fatal("paged recovery lost the tail or its reader")
	}
}
