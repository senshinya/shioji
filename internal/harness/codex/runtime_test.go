package codex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/testutil"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRealAppServerRunsTwoThreadsAndSurvivesClientContextCancellation(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("pinned Codex not installed")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"fixture-model"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r\"}}\n\nevent: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"id\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"SHIOJI_RUNTIME_OK\"}]}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"usage\":{\"input_tokens\":4,\"output_tokens\":2,\"total_tokens\":6}}}\n\n")
	}))
	defer server.Close()
	home := t.TempDir()
	config := fmt.Sprintf("model=\"fixture-model\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Fixture\"\nbase_url=\"%s/v1\"\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n", server.URL)
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	completed := map[string]bool{}
	items := map[string]string{}
	events := make(chan struct{}, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	startCtx, cancelStart := context.WithCancel(ctx)
	defer cancelStart()
	runtime, err := Start(startCtx, Options{Executable: binary, Home: home, OnEvent: func(e domain.Event) error {
		mu.Lock()
		defer mu.Unlock()
		if e.Turn != nil && e.Turn.Status == "completed" {
			completed[e.SessionID] = true
		}
		if e.Item != nil && e.Item.Kind == "assistant" && e.Item.Final {
			items[e.SessionID] = e.Item.Text
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
	if len(runtime.Readiness().Models) == 0 {
		t.Fatal("no catalog")
	}
	cancelStart() // Closing the caller's startup context must not own the process lifetime.
	for _, id := range []string{"a", "b"} {
		session := domain.Session{ID: id, Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: runtime.Readiness().Models[0].ID, Permission: "read-only"}}
		thread, err := runtime.Create(ctx, session)
		if err != nil {
			t.Fatal(err)
		}
		session.ThreadID = thread
		if _, err = runtime.Submit(ctx, session, "input-"+id, "test "+id); err != nil {
			t.Fatal(err)
		}
	}
	for {
		mu.Lock()
		done := len(completed) == 2
		if done && (!strings.Contains(items["a"], "SHIOJI_RUNTIME_OK") || !strings.Contains(items["b"], "SHIOJI_RUNTIME_OK")) {
			t.Errorf("cross-thread results: %+v", items)
		}
		mu.Unlock()
		if done {
			break
		}
		select {
		case <-events:
		case <-ctx.Done():
			t.Fatal("two sessions did not finish")
		}
	}
}
func TestIncompatibleExecutableNeverReportsReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := Start(ctx, Options{Executable: "/usr/bin/false"})
	if err == nil {
		t.Fatal("incompatible executable reported ready")
	}
}

func TestTurningPlanOffChangesActualNextTurnToDefaultMode(t *testing.T) {
	p := testutil.NewProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	completed := make(chan string, 4)
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
	session := domain.Session{ID: "plan-session", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: r.Readiness().Models[0].ID, Permission: "read-only", Plan: true}}
	session.ThreadID, err = r.Create(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range []bool{true, false} {
		session.Config.Plan = plan
		id, err := r.Submit(ctx, session, domain.NewID(), "mode test")
		if err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-completed:
			if got != id {
				t.Fatal("wrong turn completed")
			}
		case <-ctx.Done():
			t.Fatal("turn timed out")
		}
	}
	inputs := p.Inputs()
	if len(inputs) != 2 || !strings.Contains(inputs[0], "Plan Mode") || !strings.Contains(inputs[1], "# Collaboration Mode: Default") {
		t.Fatal("turning Plan off did not produce explicit Default collaboration instructions")
	}
}
