package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestRealAppServerRecoversSingleItemLargerThanFormerFrameLimit(t *testing.T) {
	text := "国😀\u2028\u2029" + strings.Repeat("x", 17<<20) + "source-tail"
	var modelCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && req.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[{"id":"fixture-model"}]}`)
			return
		}
		modelCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"type": "response.created", "response": map[string]string{"id": "r"}},
			{"type": "response.output_item.done", "item": map[string]any{"type": "message", "id": "large-message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": text}}}},
			{"type": "response.completed", "response": map[string]any{"id": "r", "usage": map[string]int{"input_tokens": 4, "output_tokens": 2, "total_tokens": 6}}},
		} {
			data, err := json.Marshal(event)
			if err != nil {
				t.Error(err)
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
		}
	}))
	defer server.Close()
	home := t.TempDir()
	config := fmt.Sprintf("model=\"fixture-model\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Fixture\"\nbase_url=\"%s/v1\"\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\nrequest_max_retries=0\nstream_max_retries=0\n[features]\nplugins=false\n", server.URL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	completed := make(chan struct{}, 1)
	r, err := Start(ctx, Options{Home: home, OnEvent: func(e domain.Event) error {
		if e.Turn != nil && e.Turn.Status == "completed" {
			completed <- struct{}{}
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
	s := domain.Session{ID: "large-history", Context: domain.LaunchContext{CWD: folder}, Config: domain.RunConfig{Model: "fixture-model", Permission: "read-only"}}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := r.Submit(ctx, s, "large-input", "produce a large fixture response")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-completed:
	case <-r.Done():
		t.Fatal("real app-server was killed while delivering a large item")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	r.Close()
	// Recovery uses a new process and only persisted source history. The model
	// endpoint must not be called again to reconstruct the accepted turn.
	restarted, err := Start(ctx, Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	var source struct {
		Data []struct {
			Items []struct{ Type, Text string }
		}
	}
	if err := restarted.call(ctx, "thread/turns/list", map[string]any{"threadId": s.ThreadID, "limit": 1, "itemsView": "full"}, &source); err != nil {
		t.Fatal("could not read the persisted large source item", err)
	}
	if len(source.Data) != 1 || len(source.Data[0].Items) != 2 || source.Data[0].Items[1].Type != "agentMessage" || source.Data[0].Items[1].Text != text {
		t.Fatal("persisted fixture no longer contains the complete large source item")
	}
	source.Data = nil
	h, err := restarted.Inspect(ctx, s)
	if err != nil || len(h.Turns) != 1 || h.Inputs["input:large-input"] != turn || len(h.Items) != 1 {
		t.Fatalf("real large-item recovery failed: turns=%d items=%d err=%v", len(h.Turns), len(h.Items), err)
	}
	if !h.Items[0].Final || !strings.HasPrefix(h.Items[0].Text, "国😀\u2028\u2029") || len(h.Items[0].Text) > 129<<10 {
		t.Fatal("recovery lost text or retained unbounded presentation output")
	}
	imported, err := restarted.ImportConversation(ctx, s.Context, s.ThreadID)
	if err != nil || imported.Truncated || len(imported.Turns) != 1 || len(imported.Items) != 2 {
		t.Fatalf("real large-item import failed: turns=%d items=%d err=%v", len(imported.Turns), len(imported.Items), err)
	}
	if modelCalls.Load() != 1 || restarted.Stopped() {
		t.Fatal("history access replayed input or stopped the process", modelCalls.Load())
	}
	t.Logf("persisted source item: %d bytes; recovered turns: %d; imported items: %d; model calls: %d", len(text), len(h.Turns), len(imported.Items), modelCalls.Load())
}
