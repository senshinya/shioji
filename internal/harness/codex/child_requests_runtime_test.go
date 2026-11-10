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

// Exercise the installed app-server against a deterministic local Responses
// provider. This does not use the user's Codex home, credentials or paid API.
func TestRealAppServerChildApprovalAfterParentCompletion(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex not installed")
	}
	var providerMu sync.Mutex
	rootCalls, childCalls := 0, 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
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
					if strings.HasPrefix(c.Text, "CHILD_REQUEST:") {
						child = true
					}
				}
			}
		}
		providerMu.Lock()
		step := 0
		if child {
			childCalls++
			step = childCalls
		} else {
			rootCalls++
			step = rootCalls
		}
		providerMu.Unlock()
		var item any
		switch {
		case !child && step == 1:
			item = map[string]any{"type": "function_call", "id": "spawn", "call_id": "spawn", "namespace": "multi_agent_v1", "name": "spawn_agent", "arguments": `{"message":"CHILD_REQUEST: request approval and ask a question","agent_type":"default"}`}
		case child && step == 1:
			item = map[string]any{"type": "function_call", "id": "command", "call_id": "command", "name": "exec_command", "arguments": `{"cmd":"printf CHILD_APPROVED","login":false,"sandbox_permissions":"require_escalated","justification":"Verify child request routing"}`}

		default:
			text := "ROOT_DONE"
			if child {
				text = "CHILD_DONE"
			}
			item = map[string]any{"type": "message", "id": fmt.Sprintf("reply-%t-%d", child, step), "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []map[string]any{{"type": "response.created", "response": map[string]any{"id": fmt.Sprint(child, step)}}, {"type": "response.output_item.done", "item": item}, {"type": "response.completed", "response": map[string]any{"id": fmt.Sprint(child, step), "usage": map[string]any{"input_tokens": 2, "output_tokens": 2, "total_tokens": 4}}}} {
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
	requests := map[string]domain.PendingRequest{}
	parentDone := false
	childOutput := ""
	receipts := 0
	signal := make(chan struct{}, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	runtime, err := Start(ctx, Options{Executable: binary, Home: home, OnEvent: func(e domain.Event) error {
		mu.Lock()
		defer mu.Unlock()
		if e.Request != nil {
			requests[e.Request.ID] = *e.Request
		}
		if e.Turn != nil && e.Turn.Status == "completed" {
			parentDone = true
		}
		if e.Item != nil && e.Item.Kind == "request_result" {
			receipts++
		}
		if e.WorkItem != nil && e.WorkItem.Kind == "assistant" {
			childOutput = e.WorkItem.Text
		}
		select {
		case signal <- struct{}{}:
		default:
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	session := domain.Session{ID: "root", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: "fixture-model", Permission: "default", Plan: true}}
	session.ThreadID, err = runtime.Create(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Submit(ctx, session, "input", "Use a child then finish independently."); err != nil {
		t.Fatal(err)
	}
	await := func(predicate func() bool) {
		t.Helper()
		for {
			mu.Lock()
			ok := predicate()
			mu.Unlock()
			if ok {
				return
			}
			select {
			case <-signal:
			case <-ctx.Done():
				mu.Lock()
				t.Logf("requests=%+v parentDone=%t child=%s", requests, parentDone, childOutput)
				mu.Unlock()
				t.Fatal("real child request lifecycle timeout")
			}
		}
	}
	var approval domain.PendingRequest
	await(func() bool {
		for _, r := range requests {
			if r.Kind == "approval" && r.State == "pending" {
				approval = r
			}
		}
		return parentDone && approval.ID != ""
	})
	if approval.WorkID == "" || approval.SessionID != "root" {
		t.Fatal("real child approval lost work provenance", approval)
	}
	if err = runtime.Respond(domain.Answer{RequestID: approval.ID, Revision: approval.Revision, Decision: "accept"}); err != nil {
		t.Fatal(err)
	}
	await(func() bool {
		return requests[approval.ID].State == "answered" && childOutput == "CHILD_DONE" && receipts == 1
	})
}
