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
	"strings"
	"sync"
	"testing"
	"time"
)

// Uses the installed real app-server and a local deterministic Responses server.
// No existing CODEX_HOME, account, project files or paid model requests are used.
func TestRealAppServerIndependentChildStopAndBackgroundWork(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex not installed")
	}
	var providerMu sync.Mutex
	rootCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"fixture-model"}]}`)
			return
		}
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, e := gzip.NewReader(r.Body)
			if e != nil {
				t.Error(e)
				return
			}
			defer gz.Close()
			reader = gz
		}
		body, _ := io.ReadAll(reader)
		var q struct {
			Input []struct {
				Role    string
				Content []struct{ Text string }
			}
		}
		json.Unmarshal(body, &q)
		child := false
		for _, in := range q.Input {
			if in.Role == "user" {
				for _, c := range in.Content {
					child = strings.Contains(c.Text, "CHILD_ONLY_WAIT")
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if child {
			fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"child\"}}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case <-r.Context().Done():
			case <-time.After(12 * time.Second):
			}
			return
		}
		providerMu.Lock()
		rootCalls++
		step := rootCalls
		providerMu.Unlock()
		var item any
		switch step {
		case 1:
			item = map[string]any{"type": "function_call", "id": "spawn", "call_id": "spawn", "namespace": "multi_agent_v1", "name": "spawn_agent", "arguments": `{"message":"CHILD_ONLY_WAIT: wait for more instructions","agent_type":"default"}`}
		case 2:
			item = map[string]any{"type": "function_call", "id": "exec", "call_id": "exec", "name": "exec_command", "arguments": `{"cmd":"printf first; sleep 3; printf last","login":false,"yield_time_ms":1000}`}
		default:
			item = map[string]any{"type": "message", "id": "root-message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "ROOT_DONE"}}}
		}
		for _, e := range []map[string]any{{"type": "response.created", "response": map[string]any{"id": fmt.Sprint(step)}}, {"type": "response.output_item.done", "item": item}, {"type": "response.completed", "response": map[string]any{"id": fmt.Sprint(step), "usage": map[string]any{"input_tokens": 2, "output_tokens": 2, "total_tokens": 4}}}} {
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e["type"], b)
		}
	}))
	defer provider.Close()
	home := t.TempDir()
	config := fmt.Sprintf("model=\"fixture-model\"\nmodel_provider=\"fixture\"\n[features]\nplugins=false\nplugin_discovery_prefetch=false\nmulti_agent=true\n[model_providers.fixture]\nname=\"Fixture\"\nbase_url=\"%s/v1\"\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n", provider.URL)
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	roster := map[string]domain.WorkActivity{}
	parentDone := false
	output := map[string]string{}
	parentText := ""
	events := make(chan struct{}, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	runtime, err := Start(ctx, Options{Executable: binary, Home: home, OnEvent: func(e domain.Event) error {
		mu.Lock()
		defer mu.Unlock()
		if e.WorkItem != nil {
			output[e.WorkID] = e.WorkItem.Text
		}
		if e.Item != nil && e.Item.Kind == "assistant" {
			parentText = e.Item.Text
		}
		if e.Work != nil {
			roster[e.Work.ID] = *e.Work
		}
		if e.Turn != nil && e.Turn.Status == "completed" {
			parentDone = true
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
	session := domain.Session{ID: "root", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: "fixture-model", Permission: "auto-approve"}}
	session.ThreadID, err = runtime.Create(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Submit(ctx, session, "input", "Use a child, then execute a command, then finish."); err != nil {
		t.Fatal(err)
	}
	await := func(predicate func() bool) {
		t.Helper()
		for {
			mu.Lock()
			done := predicate()
			mu.Unlock()
			if done {
				return
			}
			select {
			case <-events:
			case <-ctx.Done():
				mu.Lock()
				snapshot, _ := json.Marshal(roster)
				mu.Unlock()
				t.Fatalf("independent lifecycle not observed: %s", snapshot)
			}
		}
	}
	var child domain.WorkActivity
	await(func() bool {
		background := false
		for _, w := range roster {
			if w.Kind == "agent" && w.Active() && w.CanStop {
				child = w
			}
			background = background || w.Kind == "background" && w.Active()
		}
		return parentDone && child.ID != "" && background
	})
	if err = runtime.StopWork(ctx, session, child.ID); err != nil {
		t.Fatal("actual independent child stop", err)
	}
	await(func() bool { return roster[child.ID].State == "stopped" })
	await(func() bool {
		for _, w := range roster {
			if w.Kind == "background" && w.State == "completed" && strings.Contains(output[w.ID], "firstlast") {
				return true
			}
		}
		return false
	})
	mu.Lock()
	defer mu.Unlock()
	if parentText != "ROOT_DONE" {
		t.Fatal("child or command output contaminated parent", parentText)
	}
}
