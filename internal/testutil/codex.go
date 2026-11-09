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
	Home        string
	Release     chan struct{}
	Started     chan struct{}
	server      *httptest.Server
	mu          sync.Mutex
	counts      map[string]int
	inputs      []string
	titleInputs []string
	execCommand string
	execTTY     bool
}

func NewProvider(t *testing.T) *Provider {
	t.Helper()
	p := &Provider{Home: t.TempDir(), Release: make(chan struct{}), Started: make(chan struct{}, 16), counts: map[string]int{}}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	config := fmt.Sprintf("model=\"fixture-model\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Fixture\"\nbase_url=\"%s/v1\"\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\nrequest_max_retries=0\nstream_max_retries=0\n[features]\nplugins=false\n", p.server.URL)
	if err := os.WriteFile(filepath.Join(p.Home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func (p *Provider) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"fixture-model"},{"id":"gpt-6-luna"},{"id":"gpt-6.1-sol"}]}`)
		return
	}
	var body map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		w.WriteHeader(400)
		return
	}
	encoded, _ := json.Marshal(body["input"])
	kind := "fast"
	textFormat, _ := body["text"].(map[string]any)
	format, _ := textFormat["format"].(map[string]any)
	schema, _ := format["schema"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	_, titleRequest := properties["title"]
	if strings.Contains(string(encoded), "SHIOJI_SLOW") {
		kind = "slow"
	}
	if strings.Contains(string(encoded), "SHIOJI_RECOVERY_EXEC") {
		kind = "recovery_exec"
	}
	if strings.Contains(string(encoded), "SHIOJI_RECOVERY_EXEC_SECOND") {
		kind = "recovery_exec_second"
	}
	if strings.Contains(string(encoded), "SHIOJI_APPROVAL") {
		kind = "approval"
	}
	if strings.Contains(string(encoded), "SHIOJI_QUESTION") {
		kind = "question"
	}
	if titleRequest {
		kind = "title"
	}
	p.mu.Lock()
	recorded, _ := json.Marshal(body)
	if titleRequest {
		p.titleInputs = append(p.titleInputs, string(recorded))
	} else {
		p.inputs = append(p.inputs, string(recorded))
	}
	count := p.counts[kind]
	command, tty := p.execCommand, p.execTTY
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
	if titleRequest {
		item["content"] = []any{map[string]string{"type": "output_text", "text": "{\"title\":\"夹具会话标题\"}"}}
	}
	if kind == "question" && count == 0 {
		args := `{"questions":[{"id":"q","header":"Choice","question":"Continue?","options":[{"label":"Yes","description":"Continue"},{"label":"No","description":"Decline"}]}]}`
		item = map[string]any{"type": "function_call", "call_id": "question_call", "name": "request_user_input", "arguments": args}
	}
	if (kind == "recovery_exec" || kind == "recovery_exec_second") && count == 0 {
		args := `{"cmd":"printf '%s' $$ > .shioji-recovery-tool-pid; sleep 60","login":false,"tty":false,"yield_time_ms":10000}`
		if command != "" {
			b, _ := json.Marshal(map[string]any{"cmd": command, "login": false, "tty": tty, "yield_time_ms": 10000})
			args = string(b)
		}
		item = map[string]any{"type": "function_call", "call_id": "recovery_exec_call", "name": "exec_command", "arguments": args}
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

func (p *Provider) SetCommand(command string, tty bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.execCommand, p.execTTY = command, tty
}

func (p *Provider) Inputs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.inputs...)
}

// TitleInputs keeps auxiliary requests separate from conversation delivery,
// so recovery/queue tests count accepted user turns rather than background work.
func (p *Provider) TitleInputs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.titleInputs...)
}
