package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"strings"
	"testing"
	"time"
)

func TestRealCodexTitleRequestIsEphemeralReadOnlyAndDoesNotLoadWorkspaceTools(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("pinned Codex unavailable")
	}
	requests := make(chan map[string]any, 4)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		item := map[string]any{"type": "message", "id": "title-message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "{\"title\":\"红黑树旋转\"}"}}}
		for _, e := range []map[string]any{{"type": "response.created", "response": map[string]string{"id": "title-response"}}, {"type": "response.output_item.done", "item": item}, {"type": "response.completed", "response": map[string]any{"id": "title-response", "usage": map[string]int{"input_tokens": 20, "output_tokens": 5, "total_tokens": 25}}}} {
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e["type"], b)
		}
	}))
	defer provider.Close()
	home := t.TempDir()
	marker := filepath.Join(home, "mcp-started")
	config := fmt.Sprintf("model=\"fixture-luna\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Fixture\"\nbase_url=\"%s/v1\"\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\nrequest_max_retries=0\nstream_max_retries=0\n[features]\nplugins=false\nplugin_discovery_prefetch=false\n[mcp_servers.title_probe]\ncommand=\"/bin/sh\"\nargs=[\"-c\",%q]\n", provider.URL, "touch "+marker+"; sleep 10")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	runtime, err := Start(ctx, Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.ready.Models = []domain.Model{{ID: "fixture-luna", Efforts: []string{"low"}}}
	manager, err := managedexec.New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	scope, err := manager.Begin("title-only")
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Stop(context.Background())
	if err = scope.Bind("worker", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	r := &adapterRuntime{Runtime: runtime, titleAdapter: Adapter{Home: home}}
	title, err := r.GenerateTitle(ctx, harness.TitleInput{Prompt: "请讲解红黑树旋转", Scope: scope})
	if err != nil || title != "红黑树旋转" {
		t.Fatal("real title generation failed", title, err)
	}
	select {
	case body := <-requests:
		b, _ := json.Marshal(body)
		text := string(b)
		if body["model"] != "fixture-luna" || strings.Contains(text, "Build a native macOS application for controlling parallel coding agents") || strings.Contains(text, "title_probe") {
			t.Fatal("wrong model or leaked workspace tools/instructions", body["model"])
		}
		metadata, _ := body["client_metadata"].(map[string]any)
		if !strings.Contains(fmt.Sprint(metadata["x-codex-turn-metadata"]), `"sandbox_mode":"read-only"`) {
			t.Fatal("title request lost its read-only sandbox")
		}
	case <-ctx.Done():
		t.Fatal("title made no model request")
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("title task launched a user MCP server", err)
	}
	entries, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "*.jsonl"))
	if len(entries) > 0 {
		t.Fatal("auxiliary title was saved as a conversation", entries)
	}
}
