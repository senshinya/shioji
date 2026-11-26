package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/store"
)

func sdkTestTurn(id string) *activeTurn {
	now := time.Now()
	return &activeTurn{uuid: id, turn: domain.Turn{ID: id, StartedAt: &now, Status: domain.ExecutionRunning},
		items: map[string]domain.Item{}, tools: map[string]string{}}
}

func feedSDK(t *testing.T, r *runtime, messages ...string) {
	t.Helper()
	for _, message := range messages {
		var in frame
		if err := json.Unmarshal([]byte(message), &in); err != nil {
			t.Fatal(err)
		}
		if err := r.processFrame(in); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClaudePermissionDenialsLeaveTheTurnUsable(t *testing.T) {
	for _, tc := range []struct{ name, notice, result, text string }{
		{"notice", `{"type":"system","subtype":"permission_denied","tool_name":"Bash","tool_use_id":"call","message":"Denied by rule"}`,
			`{"type":"result","subtype":"success"}`, "Denied by rule"},
		{"result ledger", `{"type":"system","subtype":"future_notice","message":["another","valid","shape"]}`,
			`{"type":"result","subtype":"success","permission_denials":[{"tool_use_id":"call","tool_name":"Bash","tool_input":{}}]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var item domain.Item
			var status domain.ExecutionState
			r := runtime{turn: sdkTestTurn("turn"), opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
				if e.Item != nil && e.Item.Kind == "tool" {
					item = *e.Item
				}
				if e.Turn != nil {
					status = e.Turn.Status
				}
				return nil
			}}}
			feedSDK(t, &r, `{"type":"assistant","message":{"id":"message","content":[{"type":"tool_use","id":"call","name":"Bash"}]}}`,
				tc.notice, tc.result)
			if status != domain.ExecutionCompleted || !item.Final || item.Status != "failed" || len(item.Parts) != 1 ||
				item.Parts[0].Text == "" || tc.text != "" && item.Parts[0].Text != tc.text {
				t.Fatalf("turn status %s, denial item %+v", status, item)
			}
		})
	}
}

func TestClaudeSDKResultClassifiesNativeTerminalReasons(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		want         domain.ExecutionState
		limited      bool
	}{
		{"aborted streaming", `"terminal_reason":"aborted_streaming"`, domain.ExecutionInterrupted, false},
		{"aborted tools", `"terminal_reason":"aborted_tools"`, domain.ExecutionInterrupted, false},
		{"rate limited", `"api_error_status":429`, domain.ExecutionFailed, true},
		{"overloaded", `"api_error_status":529`, domain.ExecutionFailed, false},
		{"API error", `"terminal_reason":"api_error"`, domain.ExecutionFailed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var final *domain.Turn
			r := runtime{turn: sdkTestTurn("turn"), opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
				if e.Turn != nil {
					final = e.Turn
				}
				return nil
			}}}
			feedSDK(t, &r, `{"type":"result","subtype":"success",`+tc.fields+`}`)
			if final == nil || final.Status != tc.want || (final.UsageLimit != nil) != tc.limited {
				t.Fatalf("unexpected terminal state: %+v", final)
			}
		})
	}
}

func TestClaudeLateToolResultUpdatesOwningTurn(t *testing.T) {
	items := map[string]domain.Item{}
	r := runtime{turn: sdkTestTurn("first"), session: domain.Session{ThreadID: "thread"}, opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
		if e.Item != nil {
			items[e.Item.ID] = *e.Item
		}
		return nil
	}}}
	feedSDK(t, &r, `{"type":"assistant","message":{"id":"message","content":[{"type":"tool_use","id":"call","name":"Read"}]}}`,
		`{"type":"result","subtype":"success"}`)
	r.turn = sdkTestTurn("second")
	feedSDK(t, &r, `{"type":"user","session_id":"wrong-thread","message":{"content":[{"type":"tool_result","tool_use_id":"call","content":"wrong thread"}]}}`,
		`{"type":"user","user_message_uuid":"second","message":{"content":[{"type":"tool_result","tool_use_id":"call","content":"wrong prompt"}]}}`)
	if items["first:message:0"].Status != "unknown" {
		t.Fatal("foreign result settled the first turn's tool")
	}
	feedSDK(t, &r, `{"type":"user","session_id":"thread","user_message_uuid":"first","message":{"content":[{"type":"tool_result","tool_use_id":"call","content":"original tool output"}]}}`,
		`{"type":"user","user_message_uuid":"first","message":{"content":[{"type":"tool_result","tool_use_id":"call","content":"duplicate"}]}}`)
	item := items["first:message:0"]
	if len(items) != 1 || item.TurnID != "first" || item.Status != "completed" || len(item.Parts) != 1 ||
		item.Parts[0].Text != "original tool output" || r.turn.turn.ID != "second" {
		t.Fatalf("late result changed the wrong item or turn: %+v", items)
	}
}

func TestClaudeAssistantToolResultUsesNativeToolID(t *testing.T) {
	r := runtime{turn: sdkTestTurn("turn")}
	feedSDK(t, &r, `{"type":"assistant","message":{"id":"message","content":[{"type":"tool_use","id":"call","name":"Read"}]}}`,
		`{"type":"assistant","message":{"id":"message","content":[{"type":"tool_result","tool_use_id":"call","content":"assistant result"}]}}`)
	item := r.turn.items["turn:message:0"]
	if item.Status != "completed" || len(item.Parts) != 1 || item.Parts[0].Text != "assistant result" {
		t.Fatalf("assistant result did not settle tool: %+v", item)
	}
}

func TestClaudeDetachedToolFinishesInALaterTurn(t *testing.T) {
	var item domain.Item
	r := runtime{turn: sdkTestTurn("first"), opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
		if e.Item != nil {
			item = *e.Item
		}
		return nil
	}}}
	feedSDK(t, &r, `{"type":"assistant","message":{"id":"message","content":[{"type":"tool_use","id":"call","name":"WebFetch"}]}}`,
		`{"type":"user","tool_use_result":{"detachedToolCall":true},"message":{"content":[{"type":"tool_result","tool_use_id":"call","content":"Pending"}]}}`,
		`{"type":"result","subtype":"success"}`)
	if len(item.Parts) != 0 || item.Status == "completed" {
		t.Fatalf("detachment receipt settled the tool: %+v", item)
	}
	r.turn = sdkTestTurn("second")
	feedSDK(t, &r, `{"type":"user","user_message_uuid":"second","message":{"content":[{"type":"tool_result","tool_use_id":"call","content":"detached output"}]}}`)
	if item.TurnID != "first" || item.Status != "completed" || len(item.Parts) != 1 || item.Parts[0].Text != "detached output" {
		t.Fatalf("detached result did not update the original tool: %+v", item)
	}
}

func TestClaudeBackgroundRunRejectsEarlierRunNotifications(t *testing.T) {
	db, sid := sdkTestStore(t)
	r := runtime{turn: sdkTestTurn("turn"), session: domain.Session{ID: sid},
		opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
			_, err := db.Apply(sid, e)
			return err
		}}}
	feedSDK(t, &r,
		`{"type":"system","subtype":"task_started","task_id":"task","task_type":"local_agent","run_id":"run-01","description":"first"}`)
	first := r.work["task"].ID
	feedSDK(t, &r,
		`{"type":"system","subtype":"task_started","task_id":"task","task_type":"local_agent","run_id":"run-02","description":"resumed"}`,
		`{"type":"system","subtype":"task_progress","task_id":"task","run_id":"run-01","description":"old progress"}`,
		`{"type":"system","subtype":"task_notification","task_id":"task","run_id":"run-01","status":"completed","summary":"old completion"}`)
	resumed := r.work["task"]
	if !resumed.Active() || resumed.ID == first || resumed.Title != "resumed" || resumed.Latest != "" {
		t.Fatalf("old run changed the resumed task: %+v", resumed)
	}
	feedSDK(t, &r,
		`{"type":"system","subtype":"task_notification","task_id":"task","run_id":"run-02","status":"stopped","summary":"resumed task stopped"}`,
		`{"type":"system","subtype":"task_started","task_id":"task","task_type":"local_agent","run_id":"run-01","description":"late start"}`)
	done, err := db.Work(sid, resumed.ID)
	if err != nil || len(r.work) != 0 || done.Work.State != "stopped" || done.Work.Latest != "resumed task stopped" {
		t.Fatalf("matching notification did not settle task: %+v, active=%+v, error=%v", done.Work, r.work, err)
	}
}

func TestClaudeLocalCommandOutputAndResultFallback(t *testing.T) {
	for _, tc := range []struct {
		name, message, result, want string
	}{
		{"local output", `{"type":"system","subtype":"local_command_output","uuid":"command","content":"local output"}`, `"result":""`, "local output"},
		{"local output echoed in result", `{"type":"system","subtype":"local_command_output","uuid":"command","content":"local output"}`, `"result":"local output"`, "local output"},
		{"result only", `{"type":"system","subtype":"init"}`, `"result":"fallback reply"`, "fallback reply"},
		{"assistant takes precedence", `{"type":"assistant","message":{"id":"reply","content":[{"type":"text","text":"assistant reply"}]}}`, `"result":"fallback reply"`, "assistant reply"},
		{"compaction", `{"type":"system","subtype":"init"}`, `"local_command":"compact","result":"Not enough messages to compact"`, ""},
		{"error envelope", `{"type":"system","subtype":"init"}`, `"is_error":true,"result":"internal error payload"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := map[string]domain.Item{}
			r := runtime{turn: sdkTestTurn("turn"), opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
				if e.Item != nil {
					items[e.Item.ID] = *e.Item
				}
				return nil
			}}}
			feedSDK(t, &r, tc.message, `{"type":"result","subtype":"success",`+tc.result+`}`)
			if tc.want == "" && len(items) != 0 || tc.want != "" && len(items) != 1 {
				t.Fatalf("unexpected reply items: %+v", items)
			}
			for _, item := range items {
				if item.Text != tc.want || item.Kind != "assistant" || !item.Final {
					t.Fatalf("unexpected reply: %+v", item)
				}
			}
		})
	}
}

func sdkTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	w, err := db.CreateWorkspace("fixture", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.AcceptForHarness(domain.Submission{OperationID: "test", WorkspaceID: w.ID, Text: "prompt"}, "claude-test", domain.LaunchContext{CWD: root})
	if err != nil {
		t.Fatal(err)
	}

	return db, op.SessionID
}

func TestClaudeLongSDKMessagesFitTheJournal(t *testing.T) {
	db, sid := sdkTestStore(t)
	chunk := strings.Repeat("<", 16<<10) + "汉字"
	text := strings.Repeat(chunk, 34)
	r := runtime{turn: sdkTestTurn("turn"), session: domain.Session{ID: sid},
		opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
			_, err := db.Apply(sid, e)
			return err
		}}}
	feedSDK(t, &r, `{"type":"stream_event","event":{"type":"message_start","message":{"id":"stream"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text"}}}`)
	wireChunk, err := json.Marshal(chunk)
	if err != nil {
		t.Fatal(err)
	}
	for range 34 {
		feedSDK(t, &r, `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(wireChunk)+`}}}`)
	}
	wireText, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	feedSDK(t, &r, `{"type":"assistant","message":{"id":"stream","content":[{"type":"text","text":`+string(wireText)+`}]}}`,
		`{"type":"assistant","message":{"id":"stream","content":[{"type":"thinking","thinking":`+string(wireText)+`}]}}`,
		`{"type":"assistant","message":{"id":"next","content":[{"type":"text","text":"subsequent reply"}]}}`,
		`{"type":"result","subtype":"success"}`)
	detail, err := db.Session(sid, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var next bool
	boundedCount := 0
	for _, item := range detail.Items {
		prefix, marked := strings.CutSuffix(item.Text, "\n[Output limit reached]")
		if strings.HasPrefix(item.ID, "turn:stream:") && item.Final && item.Truncated && utf8.ValidString(item.Text) &&
			marked && prefix != "" && strings.HasPrefix(text, prefix) {
			boundedCount++
		}
		next = next || item.Kind == "assistant" && item.Final && item.Text == "subsequent reply"
	}
	if boundedCount != 2 || !next || detail.Session.Status != domain.ExecutionCompleted {
		t.Fatalf("bounded items=%d, subsequent reply=%v, status=%s", boundedCount, next, detail.Session.Status)
	}
}

func TestClaudeEventSinkFailureShutsDownThroughStdin(t *testing.T) {
	cmd := exec.Command("/bin/cat")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var lost bool
	r := runtime{turn: sdkTestTurn("turn"), session: domain.Session{ID: "session"}, process: cmd, input: input,
		done: make(chan struct{}), stopProxy: func() {}, opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
			if e.Type == "runtime.lost" {
				lost = true
				return nil
			}
			return errors.New("injected event sink error")
		}}}
	r.receive(bytes.NewBufferString(`{"type":"assistant","message":{"id":"reply","content":[{"type":"text","text":"reply"}]}}`+"\n"), nil)
	if !cmd.ProcessState.Success() || !lost {
		t.Fatalf("event sink failure killed bridge before stdin shutdown: %v, lost=%v", cmd.ProcessState, lost)
	}
}
