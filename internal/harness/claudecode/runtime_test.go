package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestClaudeCodeProfileRunsAnIsolatedStreamingTurn(t *testing.T) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("Claude Code not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	events := make(chan domain.Event, 64)
	model := "claude-sonnet-4-6"
	workspace := t.TempDir()
	writePath := filepath.Join(workspace, "approval-fixture.txt")
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "fixture-key" {
			t.Errorf("wrong credential scope")
			w.WriteHeader(401)
			return
		}
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Stream   bool
			Messages json.RawMessage
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if !request.Stream {
			fmt.Fprint(w, `{"id":"msg_fixture","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(string(request.Messages), "write-fixture") && !strings.Contains(string(request.Messages), `"tool_use_id":"toolu_fixture"`) && !strings.Contains(string(request.Messages), `"type":"tool_result"`) {
			arg, _ := json.Marshal(map[string]string{"file_path": writePath, "content": "approved"})
			event := func(name, payload string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload) }
			event("message_start", `{"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`)
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_fixture","name":"Write","input":{}}}`)
			delta, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "input_json_delta", "partial_json": string(arg)}})
			event("content_block_delta", string(delta))
			event("content_block_stop", `{"type":"content_block_stop","index":0}`)
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":2}}`)
			event("message_stop", `{"type":"message_stop"}`)
			return
		}

		for _, event := range []struct{ kind, data string }{
			{"message_start", `{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"isolated reply"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`},
			{"message_stop", `{"type":"message_stop"}`},
		} {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.kind, event.data)
		}
	}))
	defer service.Close()

	a := Adapter{Executable: bin, id: domain.NewID(), home: t.TempDir()}
	if _, err = a.ConfigureAPI(ctx, domain.APIConfiguration{Endpoint: service.URL, Model: model, Credential: "fixture-key"}); err != nil {
		t.Fatal(err)
	}
	r, err := a.Start(ctx, harness.StartOptions{OnEvent: func(e domain.Event) error { events <- e; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = r.Close(closeCtx)
	}()
	session := domain.Session{ID: domain.NewID(), Context: domain.LaunchContext{CWD: workspace}, Config: domain.RunConfig{Model: model, Permission: "default"}}
	session.ThreadID, err = r.Create(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Submit(ctx, session, "fixture", harness.TurnInput{Text: "Return a short response"}); err != nil {
		t.Fatal(err)
	}
	done := func(approve bool) {
		t.Helper()
		sawText, sawApproval := false, false
		for {
			select {
			case e := <-events:
				if e.Type == "request.open" && e.Request != nil {
					if !approve || sawApproval {
						t.Fatalf("unexpected tool approval: %s", e.Request.Title)
					}
					if err := r.Respond(domain.Answer{RequestID: e.Request.ID, Revision: e.Request.Revision, Decision: "accept"}); err != nil {
						t.Fatal(err)
					}
					sawApproval = true
				}
				if e.Item != nil && e.Item.Kind == "assistant" && e.Item.Final && e.Item.Text == "isolated reply" {
					sawText = true
				}
				if e.Turn != nil && e.Turn.Status != domain.ExecutionRunning {
					if e.Turn.Status != domain.ExecutionCompleted || !sawText || sawApproval != approve {
						t.Fatalf("terminal %v, text %v, approval %v", e.Turn, sawText, sawApproval)
					}
					return
				}
			case <-ctx.Done():
				t.Fatal("no Claude terminal event")
			}
		}
	}
	done(false)
	if _, err = r.Submit(ctx, session, "write", harness.TurnInput{Text: "write-fixture"}); err != nil {
		t.Fatal(err)
	}
	done(true)
	if data, err := os.ReadFile(writePath); err != nil || string(data) != "approved" {
		t.Fatal("approved tool did not create its file", err)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	r, err = a.Start(ctx, harness.StartOptions{OnEvent: func(e domain.Event) error { events <- e; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Resume(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Submit(ctx, session, "resume", harness.TurnInput{Text: "Another response"}); err != nil {
		t.Fatal(err)
	}
	done(false)
}

func TestClaudeExitInvalidatesApprovalAndReportsUnknownExecution(t *testing.T) {
	cmd := exec.Command("/usr/bin/true")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var events []domain.Event
	r := runtime{opts: harness.StartOptions{OnEvent: func(e domain.Event) error { events = append(events, e); return nil }},
		process: cmd, input: input, stopProxy: func() {}, done: make(chan struct{}), session: domain.Session{ID: "session"},
		approvals: map[string]approval{"request": {request: domain.PendingRequest{ID: "request", Revision: 1, State: domain.RequestPending}}}}
	r.receive(strings.NewReader(""), "initialize", make(chan error, 1))
	if len(events) != 2 || events[0].Type != "request.resolve" || events[0].Request.State != domain.RequestInvalidated ||
		events[0].Request.Revision != 2 || events[1].Type != "runtime.lost" || events[1].SessionID != "session" {
		t.Fatalf("unexpected process-exit events: %#v", events)
	}
}
