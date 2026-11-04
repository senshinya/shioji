// Package testutil supplies a deterministic model endpoint for real Codex integration tests.
package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type Provider struct {
	Home    string
	Release chan struct{}
	Started chan struct{}
	server  *httptest.Server
	mu      sync.Mutex
	counts  map[string]int
}

func NewProvider(t *testing.T) *Provider {
	t.Helper()
	p := &Provider{Home: t.TempDir(), Release: make(chan struct{}), Started: make(chan struct{}, 16), counts: map[string]int{}}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	config := fmt.Sprintf("model=\"fixture-model\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Fixture\"\nbase_url=\"%s/v1\"\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\nrequest_max_retries=0\nstream_max_retries=0\n", p.server.URL)
	if err := os.WriteFile(filepath.Join(p.Home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func (p *Provider) serve(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		w.WriteHeader(400)
		return
	}
	encoded, _ := json.Marshal(body["input"])
	kind := "fast"
	if strings.Contains(string(encoded), "SHIOJI_SLOW") {
		kind = "slow"
	}
	if strings.Contains(string(encoded), "SHIOJI_APPROVAL") {
		kind = "approval"
	}
	if strings.Contains(string(encoded), "SHIOJI_QUESTION") {
		kind = "question"
	}
	p.mu.Lock()
	count := p.counts[kind]
	p.counts[kind]++
	p.mu.Unlock()
	if kind == "slow" {
		select {
		case p.Started <- struct{}{}:
		default:
		}
		select {
		case <-p.Release:
		case <-r.Context().Done():
			return
		}
	}
	item := map[string]any{"type": "message", "id": "message_" + kind + fmt.Sprint(count), "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "SHIOJI_" + strings.ToUpper(kind) + "_DONE"}}}
	if kind == "question" && count == 0 {
		args := `{"questions":[{"id":"q","header":"Choice","question":"Continue?","options":[{"label":"Yes","description":"Continue"},{"label":"No","description":"Decline"}]}]}`
		item = map[string]any{"type": "function_call", "call_id": "question_call", "name": "request_user_input", "arguments": args}
	}
	if kind == "approval" && count == 0 {
		args := `{"cmd":"printf SHIOJI_DENIED_COMMAND","login":false,"sandbox_permissions":"require_escalated","justification":"Disposable integration check; the client will deny"}`
		item = map[string]any{"type": "function_call", "call_id": "approval_call", "name": "exec_command", "arguments": args}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range []map[string]any{{"type": "response.created", "response": map[string]string{"id": "resp"}}, {"type": "response.output_item.done", "item": item}, {"type": "response.completed", "response": map[string]any{"id": "resp", "usage": map[string]int{"input_tokens": 11, "output_tokens": 5, "total_tokens": 16}}}} {
		b, _ := json.Marshal(e)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e["type"], b)
	}
}
