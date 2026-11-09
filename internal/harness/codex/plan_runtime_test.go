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
	"sync/atomic"
	"testing"
	"time"
)

func TestRealAppServerEmitsStructuredPlanUpdates(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("pinned Codex not installed")
	}
	var count atomic.Int32
	var unsupported atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"fixture-model"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		step := count.Add(1)
		var request struct {
			Model string            `json:"model"`
			Input []json.RawMessage `json:"input"`
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			decoded, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			defer decoded.Close()
			reader = decoded
		}
		if err := json.NewDecoder(reader).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if step == 1 {
			var names func(any) bool
			names = func(value any) bool {
				switch value := value.(type) {
				case map[string]any:
					if value["name"] == "update_plan" {
						return true
					}
					for _, child := range value {
						if names(child) {
							return true
						}
					}
				case []any:
					for _, child := range value {
						if names(child) {
							return true
						}
					}
				}
				return false
			}
			available := false
			for _, tool := range request.Tools {
				available = available || tool.Name == "update_plan"
			}
			for _, item := range request.Input {
				var tools any
				json.Unmarshal(item, &tools)
				available = available || names(tools)
			}
			if !available {
				unsupported.Store(true)
			}
		}
		item := map[string]any{"type": "message", "id": "message", "role": "assistant", "content": []map[string]string{{"type": "output_text", "text": "Tasks verified."}}}
		if step <= 2 && !unsupported.Load() {
			status := "in_progress"
			if step == 2 {
				status = "completed"
			}
			args, _ := json.Marshal(map[string]any{"explanation": "Structured runtime test", "plan": []map[string]string{{"step": "验证任务事件", "status": status}}})
			item = map[string]any{"type": "function_call", "id": fmt.Sprintf("fc%d", step), "call_id": fmt.Sprintf("plan%d", step), "name": "update_plan", "arguments": string(args)}
		}
		body, _ := json.Marshal(item)
		fmt.Fprintf(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r%d\"}}\n\nevent: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":%s}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r%d\"}}\n\n", step, body, step)
	}))
	defer server.Close()
	home := t.TempDir()
	config := fmt.Sprintf("model=\"fixture-model\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Fixture\"\nbase_url=\"%s/v1\"\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n", server.URL)
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	events := make(chan domain.Event, 100)
	runtime, err := Start(ctx, Options{Executable: binary, Home: home, OnEvent: func(e domain.Event) error { events <- e; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	session := domain.Session{ID: "plan-runtime", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: runtime.Readiness().Models[0].ID, Permission: "read-only"}}
	session.ThreadID, err = runtime.Create(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Submit(ctx, session, "input-plan", "test plan"); err != nil {
		t.Fatal(err)
	}
	var statuses []string
	for {
		select {
		case e := <-events:
			if e.Plan != nil {
				if len(e.Plan.Steps) != 1 || e.Plan.TurnID == "" || e.SessionID != session.ID || e.Plan.Steps[0].Text != "验证任务事件" {
					t.Fatal("invalid real plan", e.Plan)
				}
				statuses = append(statuses, e.Plan.Steps[0].Status)
			}
			if e.Turn != nil && e.Turn.Status == "completed" {
				if unsupported.Load() {
					t.Skip("This runtime/model does not expose update_plan; structured notification normalization is covered separately")
				}
				if len(statuses) != 2 || statuses[0] != "running" || statuses[1] != "completed" {
					t.Fatal("real plan lifecycle missing", statuses)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("plan runtime timed out", statuses)
		}
	}
}
