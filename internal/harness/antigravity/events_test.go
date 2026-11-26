package antigravity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

const yoloApproval = `{"sessionId":"native","toolCall":{"toolCallId":"native-tool","title":"Run command"},"options":[{"optionId":"deny","name":"Deny","kind":"reject_once"},{"optionId":"always","name":"Always allow","kind":"allow_always"},{"optionId":"once","name":"Allow once","kind":"allow_once"}]}`

func TestAntigravityChecklistFeedsExistingPlan(t *testing.T) {
	r, _, events := callbackRuntime()
	r.active = &logicalTurn{turn: domain.Turn{ID: "turn"}, primary: make(chan error, 1), tools: map[string]domain.Item{}}
	sendCallback(t, r, "session/update", `{"sessionId":"native","update":{"sessionUpdate":"plan","entries":[{"content":"正在执行的步骤","status":"in_progress"}]}}`, "")
	sendCallback(t, r, "session/update", `{"sessionId":"native","update":{"sessionUpdate":"tool_call","toolCallId":"steps","title":"Run update_steps","kind":"other","rawInput":{"steps":[{"title":"已核对","status":"done"},{"title":"正在执行","status":"in_progress"}]}}}`, "")
	sendCallback(t, r, "session/update", `{"sessionId":"native","update":{"sessionUpdate":"tool_call_update","toolCallId":"steps","status":"completed"}}`, "")
	if len(*events) != 2 || (*events)[0].Plan == nil || (*events)[1].Plan == nil {
		t.Fatalf("checklist was duplicated or lost: %+v", *events)
	}
	first, updated := (*events)[0].Plan, (*events)[1].Plan
	if first.Steps[0].Status != "running" || updated.TurnID != "turn" || len(updated.Steps) != 2 ||
		updated.Steps[0].Status != "completed" || updated.Steps[1].Status != "running" {
		t.Fatalf("incorrect shared plan: %+v, %+v", first, updated)
	}
}

func TestAntigravityCommandKeepsNativeNameAndOutputAcrossUpdates(t *testing.T) {
	r, _, events := callbackRuntime()
	r.active = &logicalTurn{turn: domain.Turn{ID: "turn"}, primary: make(chan error, 1), tools: map[string]domain.Item{}}
	sendCallback(t, r, "session/update", `{"sessionId":"native","update":{"sessionUpdate":"tool_call","toolCallId":"cmd","name":"run_command","title":"Run command","kind":"execute","rawInput":{"CommandLine":"go test ./...","WorkingDirectory":"/workspace","private":"SECRET"}}}`, "")
	sendCallback(t, r, "session/update", `{"sessionId":"native","update":{"sessionUpdate":"tool_call_update","toolCallId":"cmd","status":"completed","rawOutput":{"combined_output":"ok","exit_code":0}}}`, "")
	last := (*events)[len(*events)-1].Item
	if last.Name != "go test ./..." || last.ExitCode == nil || *last.ExitCode != 0 || len(last.Parts) != 0 || last.Text != "ok" {
		t.Fatalf("native command context was lost at completion: %+v", last)
	}
}

func TestYOLOAnswersToolApprovalWithoutOpeningARequest(t *testing.T) {
	r, writes, events := callbackRuntime()
	r.session.Config.Permission = "yolo"
	r.active = &logicalTurn{turn: domain.Turn{ID: "turn"}, prompts: []*nativePrompt{{}}, primary: make(chan error, 1), tools: map[string]domain.Item{}}
	sendCallback(t, r, "session/request_permission", yoloApproval, "approval")
	if !strings.Contains(writes.String(), `"optionId":"once"`) || len(*events) != 1 || (*events)[0].Item == nil || (*events)[0].Item.Kind != "tool" || !r.active.confirmed {
		t.Fatalf("YOLO should allow once and acknowledge the turn without opening an approval: wire=%s events=%+v", writes, *events)
	}
	sendCallback(t, r, "session/request_permission", strings.Replace(yoloApproval, `"native"`, `"foreign"`, 1), "foreign")
	if !strings.Contains(writes.String(), `"id":"foreign","result":{"outcome":{"outcome":"cancelled"}}`) {
		t.Fatalf("an unrelated request did not get cancelled: %s", writes)
	}
}

func TestSwitchToYOLOConvertsOpenApprovalToOneTool(t *testing.T) {
	r, writes, events := callbackRuntime()
	r.active = &logicalTurn{turn: domain.Turn{ID: "turn"}, prompts: []*nativePrompt{{}}, primary: make(chan error, 1), tools: map[string]domain.Item{}}
	sendCallback(t, r, "session/request_permission", yoloApproval, "open")
	if len(*events) != 1 || (*events)[0].Type != "request.open" {
		t.Fatalf("expected a pending manual approval: %+v", *events)
	}
	accepted := false
	if err := r.ChangePermission(t.Context(), r.session, "yolo", func() error { accepted = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !accepted || len(*events) != 3 || (*events)[1].Item == nil || (*events)[1].Item.Kind != "tool" || (*events)[2].Type != "request.resolve" || !(*events)[2].AutomaticallyApproved || !strings.Contains(writes.String(), `"optionId":"once"`) {
		t.Fatalf("started approval remained pending after switching: wire=%s events=%+v", writes, *events)
	}
	sendCallback(t, r, "session/request_permission", yoloApproval, "next")
	if strings.Count(writes.String(), `"optionId":"once"`) != 2 || len(*events) != 3 {
		t.Fatalf("next approval was shown to the user: wire=%s events=%+v", writes, *events)
	}
	r.cfg.ConfigOptions = []configOption{{ID: "mode", Type: "select", CurrentValue: "default", Options: []choice{{Value: "default"}}}}
	if err := r.ChangePermission(t.Context(), r.session, "default", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	sendCallback(t, r, "session/request_permission", yoloApproval, "manual")
	if len(*events) != 4 || (*events)[3].Type != "request.open" {
		t.Fatalf("returning to manual permission did not restore approval prompts: %+v", *events)
	}
	toolID := (*events)[1].Item.ID
	sendCallback(t, r, "session/update", `{"sessionId":"native","update":{"sessionUpdate":"tool_call_update","toolCallId":"native-tool","status":"completed"}}`, "")
	if len(*events) != 5 || (*events)[4].Item == nil || (*events)[4].Item.ID != toolID || !(*events)[4].Item.Final {
		t.Fatalf("native execution result did not update the same tool call: %+v", *events)
	}
}

func TestIdleSessionTitleRequiresMatchingSource(t *testing.T) {
	var events []domain.Event
	r := &runtime{
		session: domain.Session{ID: "worker-session", ThreadID: "native"},
		options: harness.StartOptions{OnEvent: func(e domain.Event) error {
			events = append(events, e)
			return nil
		}},
	}
	for _, update := range []string{
		`{"sessionId":"foreign","update":{"sessionUpdate":"session_info_update","title":"Different session"}}`,
		`{"sessionId":"native","update":{"sessionUpdate":"session_info_update","title":"Renamed by agent"}}`,
		`{"sessionId":"native","update":{"sessionUpdate":"session_info_update","title":"Renamed by agent"}}`,
	} {
		if err := r.handle(packet{Method: "session/update", Params: json.RawMessage(update)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 1 || events[0].Type != "session.title" || events[0].SessionID != "worker-session" || events[0].Text != "Renamed by agent" {
		t.Fatalf("title was lost or assigned outside its scope: %+v", events)
	}
}

func TestCompletedToolResourcesUseOwningWorkerImport(t *testing.T) {
	var source []harness.OutputResource
	var event domain.Event
	r := &runtime{
		session: domain.Session{ID: "worker-session", ThreadID: "native", Context: domain.LaunchContext{CWD: "/device/project"}},
		active:  &logicalTurn{turn: domain.Turn{ID: "turn"}, tools: map[string]domain.Item{}, primary: make(chan error, 1)},
		options: harness.StartOptions{
			OnEvent: func(e domain.Event) error { event = e; return nil },
			ImportResource: func(res harness.OutputResource) (domain.Attachment, error) {
				source = append(source, res)
				return domain.Attachment{ID: res.Kind + "-ref", Name: res.Name}, nil
			},
		},
	}
	msg := `{"sessionId":"native","update":{"sessionUpdate":"tool_call_update","toolCallId":"tool","status":"completed","content":[
		{"type":"content","content":{"type":"resource","resource":{"uri":"fixture://result/report.txt","mimeType":"text/plain","text":"body"}}},
		{"type":"content","content":{"type":"image","mimeType":"image/png","data":"aW1hZ2U="}},
		{"type":"content","content":{"type":"resource_link","uri":"file:///device/project/result.txt","mimeType":"text/plain","name":"result.txt"}},
		{"type":"content","content":{"type":"resource_link","uri":"file:///tmp/other-session.png","mimeType":"image/png","name":"forged.png"}},
		{"type":"content","content":{"type":"resource_link","uri":"file:///device/project/current.png","mimeType":"image/png","name":"current.png"}},
		{"type":"content","content":{"type":"resource_link","uri":"https://fixture.invalid/file","name":"untrusted.txt"}}
	]}}`
	if err := r.handle(packet{Method: "session/update", Params: json.RawMessage(msg)}); err != nil {
		t.Fatal(err)
	}
	if len(source) != 4 || event.Item == nil || len(event.Item.Parts) != 4 {
		t.Fatalf("structured resources lost: %+v, %+v", source, event.Item)
	}
	if source[0].Kind != "file" || string(source[0].Data) != "body" || source[1].Kind != "image" || source[2].Path != "/device/project/result.txt" || source[3].Path != "/device/project/current.png" {
		t.Fatalf("tool resources were not scoped to the worker: %+v", source)
	}
	for _, res := range source {
		if res.SessionID != "worker-session" || res.ItemID != event.Item.ID {
			t.Fatalf("resource lost its ownership: %+v", res)
		}
	}
}

func TestLateToolAndApprovalKeepOriginalTurnWhileAnotherPromptRuns(t *testing.T) {
	r, writes, events := callbackRuntime()
	previous := &logicalTurn{
		turn:    domain.Turn{ID: "previous", StartedAt: new(time.Now())},
		tools:   map[string]domain.Item{"native-tool": {ID: "worker-tool", TurnID: "previous", Kind: "tool", Status: "running", Presentation: "command"}},
		outcome: domain.ExecutionCompleted,
	}
	r.active = previous
	if err := r.finishIfSettled(previous); err != nil {
		t.Fatal(err)
	}
	r.active = &logicalTurn{turn: domain.Turn{ID: "current"}, tools: map[string]domain.Item{}, prompts: []*nativePrompt{{}}, primary: make(chan error, 1)}
	approval := `{"sessionId":"native","toolCall":{"toolCallId":"native-tool","title":"Approve write"},"options":[{"optionId":"allow","name":"Allow","kind":"allow_once"}]}`
	sendCallback(t, r, "session/request_permission", approval, "approval")
	opened := (*events)[len(*events)-1].Request
	if opened == nil || opened.TurnID != "previous" || opened.ItemID != "worker-tool" || r.active.confirmed {
		t.Fatalf("late approval attached to the wrong turn: %+v", opened)
	}
	if err := r.Respond(domain.Answer{RequestID: opened.ID, Revision: opened.Revision, Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(writes.String(), `"optionId":"allow"`) {
		t.Fatalf("approval did not reach source request: %s", writes)
	}
	for _, sid := range []string{"foreign", "native"} {
		msg := fmt.Sprintf(`{"sessionId":%q,"update":{"sessionUpdate":"tool_call_update","toolCallId":"native-tool","status":"completed","rawOutput":{"combinedOutput":"late command result","exitCode":0}}}`, sid)
		sendCallback(t, r, "session/update", msg, "")
	}
	item := (*events)[len(*events)-1].Item
	if item == nil || item.ID != "worker-tool" || item.TurnID != "previous" || item.Status != "completed" || r.active.confirmed {
		t.Fatalf("late tool outcome was not applied to the existing item: %+v", item)
	}
	if item.ExitCode == nil || *item.ExitCode != 0 || item.Text != "late command result" {
		t.Fatalf("native command output was lost: %+v", item)
	}
}

func TestAntigravityQuestionFormReturnsTypedAnswersToOriginalCallback(t *testing.T) {
	r, writes, events := callbackRuntime()
	r.active = &logicalTurn{turn: domain.Turn{ID: "turn"}, tools: map[string]domain.Item{"question-tool": {ID: "question-item"}}, prompts: []*nativePrompt{{}}, primary: make(chan error, 1)}
	form := `{"sessionId":"native","toolCallId":"question-tool","mode":"form","message":"The agent has 2 questions.","requestedSchema":{"type":"object","properties":{
		"q2":{"type":"array","title":"Which checks?","items":{"anyOf":[{"const":"1","title":"Build"},{"const":"2","title":"Tests"}]}},
		"q2_other":{"type":"string","title":"Other"},
		"q1":{"type":"string","title":"Which mode?","oneOf":[{"const":"1","title":"Fast"},{"const":"2","title":"Safe"}]},
		"q1_other":{"type":"string","title":"Other"}}}}`
	sendCallback(t, r, "elicitation/create", strings.Replace(form, `"native"`, `"foreign"`, 1), "foreign")
	if len(*events) != 0 || !strings.Contains(writes.String(), `"action":"decline"`) {
		t.Fatalf("foreign form was accepted: %+v / %s", *events, writes)
	}
	writes.Reset()
	sendCallback(t, r, "elicitation/create", form, "question")
	if len(*events) != 1 {
		t.Fatalf("native form did not surface a request: %s", writes)
	}
	request := (*events)[0].Request
	if request == nil || request.Kind != "form" || request.ItemID != "question-item" || len(request.Fields) != 4 || request.Fields[0].ID != "q1" || request.Fields[2].ID != "q2" {
		t.Fatalf("unexpected question projection: %+v", request)
	}
	answer := domain.Answer{RequestID: request.ID, Revision: request.Revision, Decision: "accept", Answers: map[string][]string{"q2": {"1"}}}
	if err := r.Respond(answer); err == nil || writes.Len() != 0 {
		t.Fatalf("incomplete answer left worker: %v / %s", err, writes)
	}
	answer.Answers["q1_other"] = []string{"Custom Mode"}
	answer.Answers["q2_other"] = []string{"Lint"}
	if err := r.Respond(answer); err != nil {
		t.Fatal(err)
	}
	var reply packet
	if err := json.Unmarshal(writes.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if string(reply.ID) != `"question"` || string(reply.Result) != `{"action":"accept","content":{"q1_other":"Custom Mode","q2":["1"],"q2_other":"Lint"}}` {
		t.Fatalf("unexpected native response: %s", writes)
	}
	if event := (*events)[1]; event.Type != "request.resolve" || event.Request.Revision != 2 {
		t.Fatalf("request was not resolved: %+v", event)
	}
}

func callbackRuntime() (*runtime, *bytes.Buffer, *[]domain.Event) {
	var writes bytes.Buffer
	var events []domain.Event
	r := &runtime{
		session:  domain.Session{ID: "worker-session", ThreadID: "native"},
		process:  &rpc{in: callbackWriter{&writes}, done: make(chan struct{})},
		requests: make(map[string]nativeRequest),
		options: harness.StartOptions{OnEvent: func(e domain.Event) error {
			events = append(events, e)
			return nil
		}},
	}
	return r, &writes, &events
}

type callbackWriter struct{ *bytes.Buffer }

func (callbackWriter) Close() error { return nil }

func sendCallback(t *testing.T, r *runtime, method, params, id string) {
	t.Helper()
	message := packet{Method: method, Params: json.RawMessage(params)}
	if id != "" {
		message.ID, _ = json.Marshal(id)
	}
	if err := r.handle(message); err != nil {
		t.Fatal(err)
	}
}

func TestIdleApprovalRequiresKnownToolFromSuccessfulTurn(t *testing.T) {
	for _, outcome := range []domain.ExecutionState{domain.ExecutionCompleted, domain.ExecutionInterrupted} {
		t.Run(string(outcome), func(t *testing.T) {
			r, writes, events := callbackRuntime()
			r.active = &logicalTurn{
				turn:    domain.Turn{ID: "turn", StartedAt: new(time.Now())},
				outcome: outcome, tools: map[string]domain.Item{"native-tool": {ID: "worker-tool", TurnID: "turn", Kind: "tool", Status: "running"}},
			}
			if err := r.finishIfSettled(r.active); err != nil {
				t.Fatal(err)
			}
			approval := `{"sessionId":"native","toolCall":{"toolCallId":"native-tool","title":"Approve write"},"options":[{"optionId":"deny","name":"Deny","kind":"reject_once"}]}`
			sendCallback(t, r, "session/request_permission", strings.Replace(approval, "native-tool", "unknown-tool", 1), "unknown")
			if len(*events) != 2 || !strings.Contains(writes.String(), `"outcome":"cancelled"`) {
				t.Fatal("an unattributed idle request was retained")
			}
			writes.Reset()
			sendCallback(t, r, "session/request_permission", approval, "known")
			if outcome == domain.ExecutionInterrupted {
				if len(*events) != 2 || !strings.Contains(writes.String(), `"outcome":"cancelled"`) {
					t.Fatal("an interrupted tool reopened a request")
				}
				return
			}
			request := (*events)[2].Request
			if request == nil || request.TurnID != "turn" || !r.hasActiveTurn() {
				t.Fatalf("late approval did not protect idle admission: %+v", request)
			}
			if err := r.Respond(domain.Answer{RequestID: request.ID, Revision: request.Revision, Decision: "deny"}); err != nil {
				t.Fatal(err)
			}
			if len(*events) != 4 || (*events)[3].Type != "request.resolve" || r.hasActiveTurn() {
				t.Fatal("resolving the idle request did not release admission")
			}
		})
	}
}

func TestAntigravityAddedFileProvidesSourceWithoutHunkMarkers(t *testing.T) {
	r, _, events := callbackRuntime()
	r.active = &logicalTurn{turn: domain.Turn{ID: "turn"}, primary: make(chan error, 1), tools: map[string]domain.Item{}}
	sendCallback(t, r, "session/update", `{"sessionId":"native","update":{"sessionUpdate":"tool_call","toolCallId":"edit","title":"Write","kind":"edit","status":"completed","content":[{"type":"diff","path":"example.go","oldText":null,"newText":"package main\n"}]}}`, "")
	last := (*events)[len(*events)-1].Item
	if len(last.Files) != 1 || last.Files[0].Kind != "add" || last.Files[0].Patch != "package main\n" {
		t.Fatalf("the diff renderer received patch markers as file content: %+v", last.Files)
	}
}
