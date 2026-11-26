package claudecode

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/managedagent"
	"shioji/internal/managedexec"
)

func TestClaudeSDKBridgeStreamsPromptsAndRoutesToolDecisions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for the SDK bridge fixture")
	}
	sdk := filepath.Join(t.TempDir(), "sdk.mjs")
	fixture := `
export function query({ prompt, options }) {
  let turnSettings;
  return {
    async applyFlagSettings(settings) { turnSettings = settings; },
    async initializationResult() {
      if (options.resume !== "saved" || options.pathToClaudeCodeExecutable !== "/fixture/claude" ||
          options.permissionPrompts !== "host" || !Object.hasOwn(options.extraArgs, "restricted")) {
        throw new Error("unexpected session options");
      }
    },
    async getContextUsage({ detail }) {
      if (detail !== "summary") throw new Error("context should be local");
      return { totalTokens: 123, rawMaxTokens: 456 };
    },
    async *[Symbol.asyncIterator]() {
      for await (const message of prompt) {
        const expected = message.uuid === "first" ? {effortLevel:"low", fastMode:true} : {effortLevel:null, fastMode:null};
        if (JSON.stringify(turnSettings) !== JSON.stringify(expected)) throw new Error("unexpected per-turn settings");
        const outcome = await options.canUseTool("Write", { content: "fixture" },
          { signal: new AbortController().signal, toolUseID: "tool_fixture" });
        yield { type: "result", uuid: message.uuid, subtype: outcome.behavior };
      }
    },
    close() {},
  };
}`
	if err := os.WriteFile(sdk, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "agent-sdk.mjs", sdk)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = input.Close()
		_ = cmd.Wait()
	}()
	read := bufio.NewScanner(output)
	decode := func() map[string]any {
		t.Helper()
		if !read.Scan() {
			t.Fatalf("SDK helper stopped before emitting expected frame: %v", read.Err())
		}
		var frame map[string]any
		if err := json.Unmarshal(read.Bytes(), &frame); err != nil {
			t.Fatal(err)
		}
		return frame
	}
	write := func(data string) {
		t.Helper()
		if _, err := fmt.Fprintln(input, data); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"type":"start","binary":"/fixture/claude","thread":"saved","resume":true,"settings":{}}`)
	if frame := decode(); frame["type"] != "ready" {
		t.Fatalf("SDK did not initialize: %v", frame)
	}
	for _, id := range []string{"first", "second"} {
		settings := `{"effort":"low","fast":true}`
		if id == "second" {
			settings = `{"effort":"","fast":false}`
		}
		write(fmt.Sprintf(`{"type":"prompt","uuid":%q,"configuration":%s,"content":[{"type":"text","text":"hi"}]}`, id, settings))
		request := decode()
		if request["type"] != "approval" || request["tool_use_id"] != "tool_fixture" {
			t.Fatalf("SDK approval was not forwarded: %v", request)
		}
		write(fmt.Sprintf(`{"type":"approval","request_id":%q,"allow":true}`, request["request_id"]))
		if frame := decode(); frame["type"] != "approval_applied" || frame["request_id"] != request["request_id"] {
			t.Fatalf("SDK did not acknowledge the applied decision: %v", frame)
		}
		if frame := decode(); frame["type"] != "result" || frame["uuid"] != id || frame["subtype"] != "allow" {
			t.Fatalf("wrong result for %s: %v", id, frame)
		}
		if frame := decode(); frame["type"] != "context_usage" || frame["uuid"] != id || frame["limit_tokens"] != float64(456) {
			t.Fatalf("missing SDK context summary for %s: %v", id, frame)
		}
	}
}

func TestClaudeCodeProfileRunsAnIsolatedStreamingTurn(t *testing.T) {
	state := os.Getenv("SHIOJI_TEST_CLAUDE_STATE")
	if state == "" {
		t.Skip("set SHIOJI_TEST_CLAUDE_STATE to a private test directory for the pinned Claude toolchain")
	}
	setupCtx, stopSetup := context.WithTimeout(context.Background(), 8*time.Minute)
	defer stopSetup()
	if err := managedagent.EnsureClaudeToolchain(setupCtx, state, true, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	events := make(chan domain.Event, 64)
	model := "claude-sonnet-4-6"
	workspace := t.TempDir()
	profileHome := filepath.Join(workspace, "private-profile")
	profileFixture := filepath.Join(profileHome, "private-fixture.txt")
	runtimeFiles := t.TempDir()
	snapshot := []byte("fixture source bytes")
	digest := sha256.Sum256(snapshot)
	snapshotPath := filepath.Join(runtimeFiles, "claude-attachments", hex.EncodeToString(digest[:])+".txt")
	var protectedReadChecked, snapshotReadChecked atomic.Bool
	var sentThinkingFixture atomic.Bool
	thinkingBlock := func(event func(string, string)) {
		event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`)
		event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"isolated reasoning"}}`)
		event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"fixture-signature"}}`)
		event("content_block_stop", `{"type":"content_block_stop","index":0}`)
	}
	writePath := filepath.Join(workspace, "approval-fixture.txt")
	waitingForInterrupt := make(chan struct{}, 1)
	waitingForSteer := make(chan struct{}, 1)
	finishPrimary := make(chan struct{})
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
			Tools    json.RawMessage
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if strings.Contains(string(request.Messages), "read-attached-snapshot") &&
			!strings.Contains(string(request.Messages), "claude-attachments") &&
			!strings.Contains(string(request.Messages), "shioji-attachments") {
			t.Error("Claude did not receive the scoped attachment snapshot")
		}
		if strings.Contains(string(request.Messages), "invoke-native-skill") &&
			!strings.Contains(string(request.Messages), "SHIOJI_SKILL_BODY_FIXTURE") {
			t.Error("Claude Code did not expand the explicitly selected Skill")
		}
		if strings.Contains(string(request.Messages), "rewritten first message") &&
			strings.Contains(string(request.Messages), "Return a short response") {
			t.Error("edited first message retained the replaced conversation")
		}
		if strings.Contains(string(request.Messages), "Continue the legacy conversation") && !strings.Contains(string(request.Messages), "Legacy conversation") {
			t.Errorf("resume did not load the pre-SDK transcript")
		}
		if !request.Stream {
			fmt.Fprint(w, `{"id":"msg_fixture","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(string(request.Messages), "interrupt-fixture") {
			fmt.Fprint(w, "event: message_start\ndata: "+`{"type":"message_start","message":{"id":"msg_interrupt","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`+"\n\n")
			w.(http.Flusher).Flush()
			select {
			case waitingForInterrupt <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-ctx.Done():
			}
			return
		}
		if strings.Contains(string(request.Messages), "steer-primary-fixture") && !strings.Contains(string(request.Messages), "steer-clarification-fixture") {
			select {
			case waitingForSteer <- struct{}{}:
			default:
			}
			select {
			case <-finishPrimary:
			case <-r.Context().Done():
				return
			}
		}
		question := strings.Contains(string(request.Messages), "question-fixture")
		privateRead := strings.Contains(string(request.Messages), "check-private-profile-fixture")
		privateReturned := strings.Contains(string(request.Messages), `"tool_use_id":"toolu_private_read"`)
		snapshotRead := strings.Contains(string(request.Messages), "read-attached-snapshot")
		snapshotReturned := strings.Contains(string(request.Messages), `"tool_use_id":"toolu_snapshot_read"`)
		if privateRead && privateReturned {
			if strings.Contains(string(request.Messages), "PRIVATE_PROFILE_SENTINEL") {
				t.Error("native Read exposed a file from the private Profile")
			}
			protectedReadChecked.Store(true)
		}
		if snapshotRead && snapshotReturned && !snapshotReadChecked.Swap(true) {
			if !strings.Contains(string(request.Messages), string(snapshot)) {
				t.Error("native Read could not use the session-owned snapshot")
			}
		}
		messages := string(request.Messages)
		goal := strings.Contains(messages, "goal-fixture-test")
		plan := strings.Contains(messages, "review-plan-fixture")
		background := strings.Contains(messages, "start-background-fixture")
		child := strings.Contains(messages, "start-child-fixture")
		if goal && !strings.Contains(messages, `"tool_use_id":"toolu_goal"`) && !strings.Contains(string(request.Tools), "mcp__shioji_goal__report_goal") {
			t.Error("Claude did not offer the native Shioji Goal tool")
		}
		if question && strings.Contains(messages, `"tool_use_id":"toolu_question"`) && !strings.Contains(messages, "Keep, Refactor") {
			t.Error("Claude did not submit the selected labels to the gateway")
		}
		if (strings.Contains(messages, "write-fixture") && !strings.Contains(messages, `"tool_use_id":"toolu_fixture"`)) ||
			(question && !strings.Contains(messages, `"tool_use_id":"toolu_question"`)) ||
			(goal && !strings.Contains(messages, `"tool_use_id":"toolu_goal"`)) ||
			(plan && !strings.Contains(messages, `"tool_use_id":"toolu_plan"`)) ||
			(background && !strings.Contains(messages, `"tool_use_id":"toolu_background"`)) ||
			(child && !strings.Contains(messages, `"tool_use_id":"toolu_child"`)) ||
			(privateRead && !privateReturned) || (snapshotRead && !snapshotReturned) {
			toolID, toolName := "toolu_fixture", "Write"
			arg, _ := json.Marshal(map[string]string{"file_path": writePath, "content": "approved"})
			if question {
				toolID, toolName = "toolu_question", "AskUserQuestion"
				arg = []byte(`{"questions":[{"question":"Which changes?","header":"Changes","multiSelect":true,"options":[{"label":"Keep","description":"Keep behavior"},{"label":"Refactor","description":"Refactor implementation"}]}]}`)
			}
			if goal && !strings.Contains(messages, `"tool_use_id":"toolu_goal"`) {
				toolID, toolName = "toolu_goal", "mcp__shioji_goal__report_goal"
				_, rest, _ := strings.Cut(messages, "report_goal with key ")
				key, _, _ := strings.Cut(rest, " and outcome")
				arg, _ = json.Marshal(map[string]string{"key": key, "outcome": "complete"})
			}
			if plan && !strings.Contains(messages, `"tool_use_id":"toolu_plan"`) {
				toolID, toolName = "toolu_plan", "ExitPlanMode"
				arg = []byte(`{"plan":"## Verified plan\n- Inspect the fixture","allowedPrompts":[]}`)
			}
			if background && !strings.Contains(messages, `"tool_use_id":"toolu_background"`) {
				toolID, toolName = "toolu_background", "Bash"
				arg = []byte(`{"command":"/bin/sleep 15","description":"Wait for background fixture","run_in_background":true}`)
			}
			if child && !strings.Contains(messages, `"tool_use_id":"toolu_child"`) {
				toolID, toolName = "toolu_child", "Agent"
				arg = []byte(`{"description":"Read fixture task","prompt":"Answer only with a brief fixture reply","subagent_type":"general-purpose"}`)
			}
			if privateRead && !privateReturned {
				toolID, toolName = "toolu_private_read", "Read"
				arg, _ = json.Marshal(map[string]string{"file_path": profileFixture})
			}
			if snapshotRead && !snapshotReturned {
				toolID, toolName = "toolu_snapshot_read", "Read"
				arg, _ = json.Marshal(map[string]string{"file_path": snapshotPath})
			}
			event := func(name, payload string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload) }
			event("message_start", `{"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`)
			toolIndex := 0
			if toolID == "toolu_fixture" {
				thinkingBlock(event)
				toolIndex = 1
			}
			event("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":%q,"name":%q,"input":{}}}`, toolIndex, toolID, toolName))
			delta, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": toolIndex, "delta": map[string]string{"type": "input_json_delta", "partial_json": string(arg)}})
			event("content_block_delta", string(delta))
			event("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, toolIndex))
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":2}}`)
			event("message_stop", `{"type":"message_stop"}`)
			return
		}

		event := func(name, payload string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload) }
		event("message_start", `{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`)
		textIndex := 0
		childReply := strings.Contains(messages, "Answer only with a brief fixture reply") && !strings.Contains(messages, `"tool_use_id":"toolu_child"`)
		if sentThinkingFixture.CompareAndSwap(false, true) || childReply {
			thinkingBlock(event)
			textIndex = 1
		}
		event("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, textIndex))
		event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":"isolated reply"}}`, textIndex))
		event("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, textIndex))
		event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`)
		event("message_stop", `{"type":"message_stop"}`)
	}))
	defer service.Close()

	a := Adapter{Executable: managedagent.Claude.Find(state), State: state, id: domain.NewID(), home: profileHome}
	if _, err := a.ConfigureAPI(ctx, domain.APIConfiguration{Endpoint: service.URL, Model: model, Credential: "fixture-key"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profileFixture, []byte("PRIVATE_PROFILE_SENTINEL"), 0600); err != nil {
		t.Fatal(err)
	}
	mcp := filepath.Join(t.TempDir(), "fixture.mjs")
	server := `
import { createInterface } from "node:readline";
for await (const line of createInterface({ input: process.stdin })) {
  const request = JSON.parse(line);
  if (request.id == null) continue;
  const result = request.method === "initialize"
    ? { protocolVersion: request.params.protocolVersion, capabilities: { tools: {} }, serverInfo: { name: "fixture", version:"1.0" } }
    : request.method === "tools/list"
      ? { tools: [{ name: "describe_fixture", description: "Fixture description", inputSchema: { type: "object" } }] }
      : {};
  process.stdout.write(JSON.stringify({jsonrpc:"2.0", id:request.id, result}) + "\n");
}`
	if err := os.WriteFile(mcp, []byte(server), 0600); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"fixture": map[string]any{
		"type": "stdio", "command": managedagent.ClaudeNode.Find(state), "args": []string{mcp},
	}}})
	if err := savePrivate(filepath.Join(a.home, mcpFile), body); err != nil {
		t.Fatal(err)
	}
	hookMarker := filepath.Join(workspace, "verified-hook")
	startMarker := filepath.Join(workspace, "session-starts")
	hookBody, _ := json.Marshal(map[string]any{"hooks": map[string]any{
		"UserPromptSubmit": []map[string]any{
			{"hooks": []map[string]string{{"type": "command", "command": "/usr/bin/touch " + hookMarker}}},
		},
		"SessionStart": []map[string]any{
			{"hooks": []map[string]string{{"type": "command", "command": "/bin/echo start >> " + startMarker}}},
		},
	}})
	if err := savePrivate(filepath.Join(a.home, "settings.json"), hookBody); err != nil {
		t.Fatal(err)
	}
	configurationUI := &runtime{home: a.home}
	hooks, err := configurationUI.Hooks(ctx, domain.LaunchContext{}, "")
	if err != nil || len(hooks.Entries) != 2 {
		t.Fatalf("could not inspect profile Hooks: %+v %v", hooks, err)
	}
	var definitions []domain.HookDefinitionRef
	for _, hook := range hooks.Entries {
		definitions = append(definitions, domain.HookDefinitionRef{ID: hook.ID, DefinitionRevision: hook.DefinitionRevision})
	}
	if _, err := configurationUI.ChangeHook(ctx, domain.LaunchContext{}, domain.HookChange{
		Revision: hooks.Revision, Definitions: definitions, Action: domain.HookTrustDefinitions,
	}); err != nil {
		t.Fatal(err)
	}
	session := domain.Session{ID: domain.NewID(), Context: domain.LaunchContext{CWD: workspace}, Config: domain.RunConfig{Model: model, Permission: "default"}}
	var manager *managedexec.Manager
	if worker := os.Getenv("SHIOJI_TEST_CLAUDE_WORKER"); worker != "" {
		var err error
		manager, err = managedexec.New(t.TempDir(), worker)
		if err != nil {
			t.Fatal(err)
		}
	}
	start := func() (harness.Runtime, error) {
		opts := harness.StartOptions{SessionDirectory: runtimeFiles, OnEvent: func(e domain.Event) error { events <- e; return nil }}
		if manager != nil {
			scope, err := manager.Begin(session.ID)
			if err != nil {
				return nil, err
			}
			if err := scope.Bind("worker", os.Getpid()); err != nil {
				return nil, err
			}
			opts.Scope = scope
		}
		return a.Start(ctx, opts)
	}
	r, err := start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = r.Close(closeCtx)
	}()
	session.ThreadID, err = r.Create(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	firstTurn, err := r.Submit(ctx, session, "fixture", harness.TurnInput{Text: "Return a short response"})
	if err != nil {
		t.Fatal(err)
	}
	var observedWrite domain.Item
	done := func(approve bool, want domain.ExecutionState) {
		t.Helper()
		sawText, sawApproval := false, false
		firstTurnItems := map[string]domain.Item{}
		for {
			select {
			case e := <-events:
				if e.Type == "request.open" && e.Request != nil {
					if !approve || sawApproval {
						t.Fatalf("unexpected tool approval: %s", e.Request.Title)
					}
					answer := domain.Answer{RequestID: e.Request.ID, Revision: e.Request.Revision, Decision: "accept"}
					if e.Request.Kind == "form" {
						answer.Answers = map[string][]string{"0": {"Keep", "Refactor"}}
					}
					if err := r.Respond(answer); err != nil {
						t.Fatal(err)
					}
					sawApproval = true
				}
				if e.Item != nil && e.Item.Kind == "tool" && e.Item.Name == "Write" && e.Item.Final {
					observedWrite = *e.Item
				}
				if e.Item != nil && e.Item.Kind == "assistant" && e.Item.Final && e.Item.Text == "isolated reply" {
					sawText = true
				}
				if e.Item != nil && e.Item.TurnID == firstTurn {
					firstTurnItems[e.Item.ID] = *e.Item
				}
				if e.Turn != nil && e.Turn.Status != domain.ExecutionRunning {
					if e.Turn.Status != want || (want == domain.ExecutionCompleted && !sawText) || sawApproval != approve {
						t.Fatalf("terminal %v, text %v, approval %v", e.Turn, sawText, sawApproval)
					}
					if e.Turn.ID == firstTurn {
						reasoning, replies := 0, 0
						for _, item := range firstTurnItems {
							if domain.ReasoningSummary(item) && item.Text == "isolated reasoning" && item.Final {
								reasoning++
							}
							if item.Kind == "assistant" && item.Text == "isolated reply" && item.Final {
								replies++
							}
						}
						if reasoning != 1 || replies != 1 {
							t.Fatalf("expected one thinking summary and one reply from the native SDK, got %d and %d", reasoning, replies)
						}
					}
					return
				}
			case <-ctx.Done():
				t.Fatal("no Claude terminal event")
			}
		}
	}
	done(false, domain.ExecutionCompleted)
	if _, err := os.Stat(hookMarker); err != nil {
		t.Fatal("the explicitly trusted native Hook did not run", err)
	}
	if _, err := r.Submit(ctx, session, "private-read", harness.TurnInput{Text: "check-private-profile-fixture"}); err != nil {
		t.Fatal(err)
	}
	done(false, domain.ExecutionCompleted)
	if !protectedReadChecked.Load() {
		t.Fatal("native profile read policy was not exercised")
	}
	toolDeadline := time.Now().Add(3 * time.Second)
	for {
		inspector := r.(harness.ToolServiceInspector)
		list, err := inspector.InspectToolServices(ctx, session.Context, session.ThreadID)
		if err != nil {
			t.Fatal(err)
		}
		if list.RuntimeKnown && len(list.Entries) == 1 && list.Entries[0].State == domain.ToolServiceReady &&
			list.Entries[0].Tools == 1 && list.Entries[0].Definitions[0].Name == "describe_fixture" {
			break
		}
		if time.Now().After(toolDeadline) {
			t.Fatalf("SDK did not observe the configured MCP tool: %+v", list)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = r.Submit(ctx, session, "write", harness.TurnInput{Text: "write-fixture"}); err != nil {
		t.Fatal(err)
	}
	done(true, domain.ExecutionCompleted)
	if observedWrite.Presentation != "files" || observedWrite.Status != "completed" || len(observedWrite.Files) != 1 ||
		observedWrite.Files[0].Path != writePath || observedWrite.Files[0].Kind != "add" || !strings.Contains(observedWrite.Text, "approved") {
		t.Fatalf("Claude SDK Write result did not reach the native file row: %+v", observedWrite)
	}
	if data, err := os.ReadFile(writePath); err != nil || string(data) != "approved" {
		t.Fatal("approved tool did not create its file", err)
	}
	// Read the generated transcript through the same pinned SDK path used by
	// import. This also catches SDK versions that drop tool-result records.
	engine := r.(*runtime)
	history := &runtime{node: engine.node, sdk: engine.sdk, env: engine.env, externalHome: a.home,
		ready: domain.Readiness{HarnessID: a.id}, config: engine.config}
	previewed, err := history.ReadConversation(ctx, session.Context, session.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(previewed.Items, func(item domain.Item) bool {
		return item.Name == "Write" && item.Status == "completed" && len(item.Parts) > 0
	}) {
		t.Fatal("the SDK history preview lost the completed Write result")
	}
	if _, err = r.Submit(ctx, session, "questions", harness.TurnInput{Text: "question-fixture"}); err != nil {
		t.Fatal(err)
	}
	done(true, domain.ExecutionCompleted)
	fork, err := r.PrepareHistory(ctx, session, harness.HistoryBoundary{TurnID: firstTurn})
	if err != nil || fork == "" || fork == session.ThreadID {
		t.Fatal("native fork failed", err)
	}
	if _, err := r.PrepareHistory(ctx, session, harness.HistoryBoundary{TurnID: firstTurn, PreparedRef: fork}); err != nil {
		t.Fatal("could not read the verified native fork", err)
	}
	restart, err := r.PrepareHistory(ctx, session, harness.HistoryBoundary{TurnID: firstTurn, Restart: true})
	if err != nil {
		t.Fatal("could not prepare the first-message rewrite", err)
	}
	beforeStart, err := os.ReadFile(startMarker)
	if err != nil || len(beforeStart) == 0 {
		t.Fatal("the trusted SessionStart fixture did not run", err)
	}
	revoked := errors.New("fixture rejected the authoritative commit")
	if err := r.ActivateHistory(ctx, session, restart, func() error { return revoked }); !errors.Is(err, revoked) || !r.Observes(session) {
		t.Fatal("a rejected history commit replaced the bound native thread", err)
	}
	afterReject, err := os.ReadFile(startMarker)
	if err != nil || string(afterReject) != string(beforeStart) {
		t.Fatal("a rejected history edit started a new native session and ran its Hook", err)
	}
	committed := false
	if err := r.ActivateHistory(ctx, session, restart, func() error { committed = true; return nil }); err != nil || !committed {
		t.Fatal("could not activate the replacement history", err)
	}
	session.ThreadID = restart
	if !r.Observes(session) {
		t.Fatal("history activation changed the product session identity")
	}
	if _, err := r.Submit(ctx, session, "rewrite", harness.TurnInput{Text: "rewritten first message"}); err != nil {
		t.Fatal("could not submit the rewritten input", err)
	}
	done(false, domain.ExecutionCompleted)
	afterCommit, err := os.ReadFile(startMarker)
	if err != nil || string(afterCommit) != string(beforeStart)+"start\n" {
		t.Fatal("the accepted history edit should start exactly one native session", err)
	}
	planned := session
	planned.Config.Plan = true
	if _, err := r.Submit(ctx, planned, "plan", harness.TurnInput{Text: "review-plan-fixture"}); err != nil {
		t.Fatal("Claude Code could not send Plan input", err)
	}
	sawPlan := false
	for settled := false; !settled; {
		select {
		case event := <-events:
			if event.Plan != nil && strings.Contains(event.Plan.Narrative, "Inspect the fixture") {
				sawPlan = true
			}
			if event.Request != nil && event.Type == "request.open" {
				t.Fatal("Plan switched to execution permission")
			}
			if event.Turn != nil && !event.Turn.Status.Active() {
				if !sawPlan || event.Turn.Status != domain.ExecutionCompleted {
					t.Fatalf("native Plan did not project its narrative: projected=%v terminal=%v", sawPlan, event.Turn.Status)
				}
				settled = true
			}
		case <-ctx.Done():
			t.Fatal("no Plan result")
		}
	}
	skillPath := filepath.Join(workspace, ".claude", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("---\nname: review\ndescription: Fixture review\n---\nSHIOJI_SKILL_BODY_FIXTURE"), 0600); err != nil {
		t.Fatal(err)
	}
	skills, err := r.(harness.SkillProvider).Skills(ctx, session.Context)
	if err != nil || len(skills.Skills) != 1 {
		t.Fatalf("native Skill catalog: %+v %v", skills, err)
	}
	if _, err := r.Submit(ctx, session, "native-skill", harness.TurnInput{
		Text: "invoke-native-skill", Skills: []domain.SkillReference{skills.Skills[0].SkillReference},
	}); err != nil {
		t.Fatal(err)
	}
	done(false, domain.ExecutionCompleted)
	original := filepath.Join(t.TempDir(), "snapshot")
	if err := os.WriteFile(original, snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	file := harness.AttachmentInput{Path: original, Attachment: domain.Attachment{
		Name: "fixture.txt", ID: "public-fixture", Kind: domain.PathAttachmentResource,
		MIME: "application/octet-stream", Size: int64(len(snapshot)), SHA256: hex.EncodeToString(digest[:]),
	}}
	if _, err := r.Submit(ctx, session, "path-attachment", harness.TurnInput{
		Text: "read-attached-snapshot", Attachments: []harness.AttachmentInput{file},
	}); err != nil {
		t.Fatal(err)
	}
	done(false, domain.ExecutionCompleted)
	if !snapshotReadChecked.Load() {
		t.Fatal("native attachment Read was not exercised")
	}
	if err := r.(harness.ContextCompactor).Compact(ctx, session); err != nil {
		t.Fatal("could not hand off the compaction", err)
	}
	for received := false; !received; {
		select {
		case event := <-events:
			if event.Turn != nil && event.Turn.Presentation == "compaction" && !event.Turn.Status.Active() {
				if event.Turn.Status != domain.ExecutionCompleted {
					t.Fatal("fixture compaction was not confirmed", event.Turn.Status, event.Turn.Error)
				}
				received = true
			}
		case <-ctx.Done():
			t.Fatal("no fixture compaction result")
		}
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	r, err = start()
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Resume(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Submit(ctx, session, "resume", harness.TurnInput{Text: "Another response"}); err != nil {
		t.Fatal(err)
	}
	done(false, domain.ExecutionCompleted)
	primary, _, err := r.(harness.DeferredInputRuntime).SubmitDeferred(ctx, session, "steer-primary", harness.TurnInput{Text: "steer-primary-fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-waitingForSteer:
	case <-ctx.Done():
		t.Fatal("primary input did not reach the provider")
	}
	receipt, err := r.(harness.DeferredInputRuntime).SteerDeferred(ctx, session, primary, "steer-fixture",
		harness.TurnInput{Text: "steer-clarification-fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(100*time.Millisecond, func() { close(finishPrimary) })
	select {
	case err := <-receipt:
		if err != nil {
			t.Fatal("Claude did not acknowledge the exact steer", err)
		}
	case <-ctx.Done():
		t.Fatal("no exact Claude steer receipt")
	}
	done(false, domain.ExecutionCompleted)
	turn, err := r.Submit(ctx, session, "stop", harness.TurnInput{Text: "interrupt-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-waitingForInterrupt:
	case <-ctx.Done():
		t.Fatal("model request never started")
	}
	if err := r.Interrupt(ctx, session, turn); err != nil {
		t.Fatal(err)
	}
	done(false, domain.ExecutionInterrupted)
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}

	// A thread created by the old direct CLI path must keep its identity
	// when the SDK takes over.
	config, err := a.config()
	if err != nil {
		t.Fatal(err)
	}
	endpoint, token, closeProxy, err := config.connect()
	if err != nil {
		t.Fatal(err)
	}
	defer closeProxy()
	session.ThreadID = domain.NewID()
	legacy := exec.CommandContext(ctx, a.Executable, "--print", "--restricted", "--model", model, "--session-id", session.ThreadID, "Legacy conversation")
	legacy.Dir = workspace
	legacy.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_CONFIG_DIR=" + a.home,
		"ANTHROPIC_BASE_URL=" + endpoint, "ANTHROPIC_API_KEY=" + token, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1"}
	if output, err := legacy.Output(); err != nil || strings.TrimSpace(string(output)) != "isolated reply" {
		t.Fatalf("could not seed a pre-SDK Claude session: %s, %v", output, err)
	}
	r, err = start()
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Resume(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Submit(ctx, session, "legacy-resume", harness.TurnInput{Text: "Continue the legacy conversation"}); err != nil {
		t.Fatal(err)
	}
	done(false, domain.ExecutionCompleted)
	goal := domain.GoalProgress{GoalID: "goal-fixture", RunID: "goal-run"}
	if _, err := r.Submit(ctx, session, "goal-run", harness.TurnInput{Goal: &goal, Objective: "goal-fixture-test"}); err != nil {
		t.Fatal("could not send the managed goal", err)
	}
	for goal.Outcome == "" {
		select {
		case event := <-events:
			if event.GoalProgress != nil {
				goal.Outcome = event.GoalProgress.Outcome
			}
			if event.Turn != nil && !event.Turn.Status.Active() && goal.Outcome != domain.GoalComplete {
				t.Fatalf("goal did not receive the native tool report: %+v, result %+v", goal, event.Turn)
			}
		case <-ctx.Done():
			t.Fatal("no result from Claude Goal")
		}
	}

	reader := r.(*runtime)
	if _, err := r.Submit(ctx, session, "start-background", harness.TurnInput{Text: "start-background-fixture"}); err != nil {
		t.Fatal("could not start the background fixture", err)
	}
	done(true, domain.ExecutionCompleted)
	reader.mu.Lock()
	var target string
	for id, work := range reader.work {
		if work.Active() && work.CanStop {
			target = id
		}
	}
	reader.mu.Unlock()
	if target == "" {
		t.Fatal("the SDK did not register the live independent background task")
	}
	if err := reader.StopWork(ctx, session, target); err != nil {
		t.Fatal("could not stop the owned native background task", err)
	}
	stopped := time.NewTimer(4 * time.Second)
	defer stopped.Stop()
	for settled := false; !settled; {
		select {
		case event := <-events:
			settled = event.Work != nil && event.Work.Reference == target && event.Work.State == "interrupted"
		case <-stopped.C:
			t.Fatal("no native acknowledgement of the stopped background task")
		}
	}
	if _, err := r.Submit(ctx, session, "start-child", harness.TurnInput{Text: "start-child-fixture"}); err != nil {
		t.Fatal("could not run the native subagent fixture", err)
	}
	sawChild := false
	childItems := make(map[string]domain.Item)
	for settled := false; !settled; {
		select {
		case event := <-events:
			if event.Request != nil && event.Type == "request.open" {
				err := r.Respond(domain.Answer{RequestID: event.Request.ID, Revision: event.Request.Revision, Decision: "accept"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if event.Work != nil && event.Work.Kind == "agent" {
				sawChild = true
			}
			if event.WorkItem != nil {
				childItems[event.WorkItem.ID] = *event.WorkItem
			}
			if event.Turn != nil && !event.Turn.Status.Active() {
				sawChildText, sawChildThinking := false, false
				for _, item := range childItems {
					sawChildText = sawChildText || item.Kind == "assistant" && item.Text == "isolated reply"
					sawChildThinking = sawChildThinking || domain.ReasoningSummary(item) && item.Text == "isolated reasoning"
				}
				if !sawChild || !sawChildText || !sawChildThinking || event.Turn.Status != domain.ExecutionCompleted {
					t.Fatalf("native subagent lost distinct items: child=%v text=%v thinking=%v result=%v", sawChild, sawChildText, sawChildThinking, event.Turn.Status)
				}
				settled = true
			}
		case <-ctx.Done():
			t.Fatal("native subagent did not finish")
		}
	}
	private := &runtime{node: reader.node, sdk: reader.sdk, home: t.TempDir(), externalHome: a.home,
		ready: domain.Readiness{HarnessID: a.id}, config: reader.config}
	catalog, err := private.Conversations(ctx, []string{workspace}, "")
	if err != nil || len(catalog.Items) == 0 {
		t.Fatal("could not list device-local Claude history", err)
	}
	preview, err := private.ImportConversation(ctx, session.Context, session.ThreadID)
	if err != nil || preview.ResumeRef == "" || len(preview.Turns) != 5 {
		t.Fatalf("could not read complete external Claude history: %d turns, %v", len(preview.Turns), err)
	}
	imported := session
	imported.ThreadID = ""
	imported.Origin = &domain.ConversationOrigin{SourceRef: session.ThreadID, ResumeRef: preview.ResumeRef}
	copy, err := private.PrepareConversation(ctx, imported)
	if err != nil || copy == "" || copy == session.ThreadID {
		t.Fatal("could not prepare an independent imported Claude conversation", err)
	}
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
	r.receive(strings.NewReader(""), nil)
	if len(events) != 2 || events[0].Type != "request.resolve" || events[0].Request.State != domain.RequestInvalidated ||
		events[0].Request.Revision != 2 || events[1].Type != "runtime.lost" || events[1].SessionID != "session" {
		t.Fatalf("unexpected process-exit events: %#v", events)
	}
}

func TestClaudeSDKTitleUsesAnEphemeralToolFreeQuery(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for the SDK bridge fixture")
	}
	sdk := filepath.Join(t.TempDir(), "sdk.mjs")
	fixture := `
export function query({ prompt, options }) {
  return {
    async initializationResult() {
      if (options.persistSession !== false || options.tools.length || options.settingSources.length ||
          options.allowedTools?.length || options.maxTurns !== 1 || options.resume) {
        throw new Error("title query was not isolated");
      }
    },
    async getContextUsage() { return {}; },
    async *[Symbol.asyncIterator]() {
      for await (const message of prompt) {
        yield { type: "assistant", message: {id:"msg_title", content:[{type:"text", text:"修复并发请求"}]} };
        yield { type: "result", uuid: message.uuid, subtype: "success" };
      }
    },
    close() {},
  };
}`
	if err := os.WriteFile(sdk, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := runtime{binary: "/fixture/claude", node: node, sdk: sdk, config: configuration{Model: "fixture", Endpoint: "http://127.0.0.1:1"}}
	title, err := r.generateTitle(ctx, harness.TitleInput{Prompt: "请修复并发请求"})
	if err != nil || title != "修复并发请求" {
		t.Fatalf("isolated title result %q: %v", title, err)
	}
}
