package codex

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRealAppServerDirectedChildInputPreservesParent(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex not installed")
	}
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var callsMu sync.Mutex
	rootCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"data":[{"id":"fixture-model"}]}`)
			return
		}
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, e := gzip.NewReader(reader)
			if e != nil {
				t.Error(e)
				return
			}
			defer gz.Close()
			reader = gz
		}
		data, _ := io.ReadAll(reader)
		var q struct {
			Input []struct {
				Role    string
				Content []struct{ Text string }
			}
		}
		json.Unmarshal(data, &q)
		child, steer, cont := false, false, false
		for _, in := range q.Input {
			if in.Role == "user" {
				for _, c := range in.Content {
					child = child || strings.Contains(c.Text, "CHILD_ORIGIN")
					steer = steer || strings.Contains(c.Text, "CHILD_STEER")
					cont = cont || strings.Contains(c.Text, "CHILD_CONTINUE")
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(e map[string]any) {
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e["type"], b)
		}
		emit(map[string]any{"type": "response.created", "response": map[string]any{"id": domain.NewID()}})
		var item any
		if child || steer || cont {
			if !steer && !cont {
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			text := "CHILD_INITIAL"
			if steer {
				text = "CHILD_STEER_OK"
			}
			if cont {
				text = "CHILD_CONTINUE_OK"
			}
			item = map[string]any{"type": "message", "id": domain.NewID(), "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}}
		} else {
			callsMu.Lock()
			rootCalls++
			step := rootCalls
			callsMu.Unlock()
			if step == 1 {
				item = map[string]any{"type": "function_call", "id": "spawn", "call_id": "spawn", "namespace": "multi_agent_v1", "name": "spawn_agent", "arguments": `{"message":"CHILD_ORIGIN: check rotation","agent_type":"default"}`}
			} else {
				item = map[string]any{"type": "message", "id": "parent", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "ROOT_DONE"}}}
			}
		}
		emit(map[string]any{"type": "response.output_item.done", "item": item})
		emit(map[string]any{"type": "response.completed", "response": map[string]any{"id": domain.NewID(), "usage": map[string]any{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5}}})
	}))
	defer provider.Close()
	home := t.TempDir()
	config := fmt.Sprintf(`model="fixture-model"
model_provider="fixture"
[features]
plugins=false
plugin_discovery_prefetch=false
multi_agent=true
[model_providers.fixture]
name="Fixture"
base_url="%s/v1"
wire_api="responses"
requires_openai_auth=false
supports_websockets=false
`, provider.URL)
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	roster := map[string]domain.WorkActivity{}
	texts := map[string]string{}
	parent := ""
	events := make(chan struct{}, 128)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	runtime, err := Start(ctx, Options{Executable: binary, Home: home, OnEvent: func(e domain.Event) error {
		mu.Lock()
		defer mu.Unlock()
		if e.Work != nil {
			roster[e.Work.ID] = *e.Work
		}
		if e.WorkItem != nil {
			texts[e.WorkID] += e.WorkItem.Text
		}
		if e.Item != nil && e.Item.Kind == "assistant" {
			parent = e.Item.Text
		}
		select {
		case events <- struct{}{}:
		default:
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	messenger, ok := any(runtime).(harness.WorkMessenger)
	if !ok {
		t.Fatal("runtime has no independent child message boundary")
	}
	s := domain.Session{ID: "root", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: "fixture-model", Permission: "auto-approve"}}
	s.ThreadID, err = runtime.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Submit(ctx, s, "root-input", "Spawn a child, then finish."); err != nil {
		t.Fatal(err)
	}
	await := func(f func() bool) {
		t.Helper()
		for {
			mu.Lock()
			ok := f()
			mu.Unlock()
			if ok {
				return
			}
			select {
			case <-events:
			case <-ctx.Done():
				mu.Lock()
				snapshot, _ := json.Marshal(roster)
				mu.Unlock()
				t.Fatal("child input lifecycle", string(snapshot), ctx.Err())
			}
		}
	}
	var child domain.WorkActivity
	await(func() bool {
		for _, w := range roster {
			if w.Kind == "agent" && w.CanSteer {
				child = w
			}
		}
		return child.ID != "" && parent == "ROOT_DONE"
	})
	steerInput := domain.WorkInput{OperationID: "steer-child", SessionID: s.ID, WorkID: child.ID, Mode: "steer", Text: "CHILD_STEER: check root"}
	if err = messenger.SendWork(ctx, s, steerInput); err != nil {
		t.Fatal("actual child steer", err)
	}
	once.Do(func() { close(release) })
	await(func() bool {
		return roster[child.ID].State == "completed" && strings.Contains(texts[child.ID], "CHILD_STEER_OK")
	})
	if err = messenger.SendWork(ctx, s, domain.WorkInput{OperationID: "continue-child", SessionID: s.ID, WorkID: child.ID, Mode: "continue", Text: "CHILD_CONTINUE: check parent"}); err != nil {
		t.Fatal("actual child continue", err)
	}
	await(func() bool {
		return roster[child.ID].State == "completed" && strings.Contains(texts[child.ID], "CHILD_CONTINUE_OK")
	})
	got, err := messenger.WorkInputReceived(ctx, s, steerInput)
	if err != nil || !got {
		t.Fatal("actual child receipt lookup", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if parent != "ROOT_DONE" {
		t.Fatal("child input changed parent", parent)
	}
}
