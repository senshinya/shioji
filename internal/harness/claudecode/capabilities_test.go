package claudecode

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/managedagent"
)

func TestClaudeInputHandoffKeepsNativeIdentityAcrossRuntimeStarts(t *testing.T) {
	home := t.TempDir()
	session := domain.Session{ID: domain.NewID(), ThreadID: domain.NewID()}
	other := domain.Session{ID: domain.NewID(), ThreadID: domain.NewID()}
	capture := func(s domain.Session, operation string) (string, string) {
		t.Helper()
		out, err := os.CreateTemp(t.TempDir(), "stdin-*")
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		r := &runtime{home: home, input: out, session: s, process: &exec.Cmd{}, done: make(chan struct{})}
		turn, _, err := r.SubmitDeferred(context.Background(), s, operation, harness.TurnInput{Text: "fixture"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Clean(out.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var wire struct{ UUID string }
		if err := json.Unmarshal(data, &wire); err != nil || wire.UUID == "" {
			t.Fatalf("missing native prompt identity: %v", err)
		}
		return turn, wire.UUID
	}
	turn, id := capture(session, "stable-operation")
	if nextTurn, nextID := capture(session, "stable-operation"); turn != nextTurn || id != nextID {
		t.Fatal("reopening the owned runtime changed the durable turn or input identity")
	}
	if _, next := capture(session, "different-operation"); id == next {
		t.Fatal("different operations shared a native input")
	}
	if _, next := capture(other, "stable-operation"); id == next {
		t.Fatal("the same operation identity crossed native threads")
	}
}

func TestClaudeContextSummaryAppliesOnlyToItsCompletedTurn(t *testing.T) {
	var events []domain.Event
	r := runtime{
		session: domain.Session{ID: "session", ThreadID: "thread"},
		opts: harness.StartOptions{OnEvent: func(event domain.Event) error {
			events = append(events, event)
			return nil
		}},
	}
	started := time.Now()
	r.turn = &activeTurn{turn: domain.Turn{ID: "first-turn", StartedAt: &started}}
	for _, input := range []string{
		`{"type":"result","session_id":"thread","subtype":"success","uuid":"result-first"}`,
		`{"type":"context_usage","session_id":"thread","uuid":"unrelated","used_tokens":99,"limit_tokens":50}`,
		`{"type":"context_usage","session_id":"thread","uuid":"result-first","used_tokens":12345,"limit_tokens":200000}`,
	} {
		var frame frame
		if err := json.Unmarshal([]byte(input), &frame); err != nil {
			t.Fatal(err)
		}
		if err := r.processFrame(frame); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 2 || events[0].Turn == nil || events[0].Turn.Status != domain.ExecutionCompleted {
		t.Fatalf("expected the terminal turn and a single matching context observation: %+v", events)
	}
	usage := events[1].ContextUsage
	if events[1].Type != "context.usage" || usage == nil || usage.TurnID != "first-turn" ||
		usage.UsedTokens == nil || *usage.UsedTokens != 12345 || usage.LimitTokens == nil || *usage.LimitTokens != 200000 {
		t.Fatalf("unexpected context observation: %+v", usage)
	}
}

func TestClaudeQuotaFailureUsesTypedResetWithoutLeakingProviderError(t *testing.T) {
	var events []domain.Event
	r := runtime{
		session: domain.Session{ID: "session", ThreadID: "thread"},
		opts: harness.StartOptions{OnEvent: func(event domain.Event) error {
			events = append(events, event)
			return nil
		}},
	}
	started := time.Now()
	r.turn = &activeTurn{turn: domain.Turn{ID: "turn", StartedAt: &started}, items: map[string]domain.Item{}}
	for _, input := range []string{
		`{"type":"rate_limit_event","session_id":"thread","rate_limit_info":{"status":"rejected","resetsAt":2000000000}}`,
		`{"type":"assistant","session_id":"thread","error":"rate_limit","message":{"id":"msg_error","content":[{"type":"text","text":"untrusted provider detail"}]}}`,
		`{"type":"result","session_id":"thread","is_error":true,"subtype":"error_during_execution"}`,
	} {
		var frame frame
		if err := json.Unmarshal([]byte(input), &frame); err != nil {
			t.Fatal(err)
		}
		if err := r.processFrame(frame); err != nil {
			t.Fatal(err)
		}
	}
	turn := events[len(events)-1].Turn
	reset := time.Unix(2000000000, 0)
	if turn == nil || turn.Status != domain.ExecutionFailed || turn.UsageLimit == nil ||
		turn.UsageLimit.ResetAt == nil || !turn.UsageLimit.ResetAt.Equal(reset) ||
		turn.Error != "Claude Code 已达到模型服务的用量限制" {
		t.Fatalf("expected a typed quota failure with reset time: %+v", turn)
	}
	for _, event := range events {
		if event.Item != nil && strings.Contains(event.Item.Text, "untrusted provider detail") {
			t.Fatal("raw provider error entered the transcript")
		}
	}
}

// The live gateway key is read from stdin by the test binary. The production
// credential broker receives it in memory; no Profile setup writes take place.
func TestClaudeLiveGatewayViaCredentialBroker(t *testing.T) {
	if os.Getenv("SHIOJI_TEST_CLAUDE_LIVE") != "1" {
		t.Skip("optional live gateway test reads its credential from stdin")
	}
	raw, err := bufio.NewReader(os.Stdin).ReadString('\n')
	key := strings.TrimSpace(raw)
	if err != nil || key == "" {
		t.Fatal("missing gateway credential on stdin")
	}
	state := os.Getenv("SHIOJI_TEST_CLAUDE_STATE")
	bin, node, sdk := managedagent.Claude.Find(state), managedagent.ClaudeNode.Find(state), managedagent.ClaudeSDK.Find(state)
	if bin == "" || node == "" || sdk == "" {
		t.Fatal("pinned Claude toolchain is missing in the test directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	workspace := t.TempDir()
	events := make(chan domain.Event, 96)
	config := configuration{Endpoint: "https://api.deepseek.com/anthropic", Model: "deepseek-flash", Key: key}
	r := &runtime{
		opts: harness.StartOptions{OnEvent: func(event domain.Event) error {
			select {
			case events <- event:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}},
		binary: bin, node: node, sdk: sdk, home: t.TempDir(), config: config,
		env: []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}, done: make(chan struct{}), approvals: map[string]approval{},
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := r.Close(cleanup); err != nil {
			t.Error("could not close the live Claude runtime")
		}
	}()
	session := domain.Session{ID: domain.NewID(), Context: domain.LaunchContext{CWD: workspace}, Config: domain.RunConfig{Model: config.Model}}
	if session.ThreadID, err = r.Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Submit(ctx, session, domain.NewID(), harness.TurnInput{Text: "Reply with only SHIOJI_OK. Do not use tools."}); err != nil {
		t.Fatal(err)
	}
	sawText, completed := false, false
	for {
		select {
		case event := <-events:
			if event.Item != nil && event.Item.Kind == "assistant" && strings.Contains(event.Item.Text, "SHIOJI_OK") {
				sawText = true
			}
			if event.Turn != nil && !event.Turn.Status.Active() {
				if event.Turn.Status != domain.ExecutionCompleted || !sawText {
					t.Fatal("the live gateway turn did not complete with the expected reply", event.Turn.Status)
				}
				completed = true
			}
			if completed && event.ContextUsage != nil && event.ContextUsage.UsedTokens != nil {
				t.Log("live reply and SDK context usage received")
				if err := r.Compact(ctx, session); err != nil {
					t.Fatal("live compaction was not handed off", err)
				}
				for {
					select {
					case next := <-events:
						if next.Turn != nil && next.Turn.Presentation == "compaction" && !next.Turn.Status.Active() {
							if next.Turn.Status != domain.ExecutionCompleted {
								t.Fatal("live compaction did not complete", next.Turn.Status, next.Turn.Error)
							}
							t.Log("live compaction emitted a native boundary")
							title, err := r.generateTitle(ctx, harness.TitleInput{Prompt: "Implement regression tests for parsing timestamps"})
							if err != nil || title == "" || len([]rune(title)) > 40 {
								t.Fatal("the independent live title request did not finish", err)
							}
							t.Log("independent live title request finished")
							return
						}
					case <-ctx.Done():
						t.Fatal("no terminal live compaction result")
					}
				}
			}
		case <-ctx.Done():
			t.Fatal("no terminal live gateway result with context usage")
		}
	}
}

func TestClaudeQuestionRoundTripUsesNativeLabels(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	var events []domain.Event
	r := runtime{input: out, session: domain.Session{ID: "session", ThreadID: "thread"}, approvals: map[string]approval{},
		opts: harness.StartOptions{OnEvent: func(event domain.Event) error {
			events = append(events, event)
			return nil
		}},
		turn: &activeTurn{turn: domain.Turn{ID: "turn"}},
	}
	in := frame{Type: "approval", RequestID: "native", ToolName: "AskUserQuestion",
		Input: json.RawMessage(`{"questions":[{"question":"What runs?","header":"Checks","options":[{"label":"Lint","description":"Lint sources"},{"label":"Unit","description":"Run unit tests"}],"multiSelect":true},{"question":"Which target?","header":"Target","options":[{"label":"Debug"},{"label":"Release"}]}]}`)}
	if err := r.processFrame(in); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Request == nil || events[0].Request.Kind != "form" {
		t.Fatalf("expected a structured form, got %+v", events)
	}
	req := *events[0].Request
	answer := domain.Answer{RequestID: req.ID, Revision: req.Revision, Decision: "accept",
		Answers: map[string][]string{"0": {"Lint", "Unit"}, "1:other": {"Device A"}}}
	if err := r.Respond(domain.Answer{RequestID: req.ID, Revision: req.Revision, Decision: "accept"}); err == nil {
		t.Fatal("an incomplete form was accepted")
	}
	receipt, err := r.submitApproval(answer)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.processFrame(frame{Type: "approval_applied", RequestID: "native"}); err != nil {
		t.Fatal(err)
	}
	if err := <-receipt; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Allow   bool
		Answers map[string]string
	}
	if err := json.Unmarshal(data, &reply); err != nil || !reply.Allow ||
		reply.Answers["What runs?"] != "Lint, Unit" || reply.Answers["Which target?"] != "Device A" {
		t.Fatalf("wrong SDK callback response: %s (%v)", data, err)
	}
	if len(events) != 2 || events[1].Request.State != domain.RequestResolved {
		t.Fatal("question callback did not resolve")
	}
}

func TestClaudeCompactionRequiresNativeBoundary(t *testing.T) {
	for _, boundary := range []bool{false, true} {
		t.Run(map[bool]string{false: "no proof", true: "native proof"}[boundary], func(t *testing.T) {
			var events []domain.Event
			r := runtime{session: domain.Session{ID: "session", ThreadID: "thread"},
				opts: harness.StartOptions{OnEvent: func(event domain.Event) error {
					events = append(events, event)
					return nil
				}},
			}
			started := time.Now()
			r.turn = &activeTurn{turn: domain.Turn{ID: "compact", StartedAt: &started, Presentation: "compaction"}}
			if boundary {
				if err := r.processFrame(frame{Type: "system", Subtype: "compact_boundary", SessionID: "thread"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.processFrame(frame{Type: "result", Subtype: "success", SessionID: "thread"}); err != nil {
				t.Fatal(err)
			}
			turn := events[len(events)-1].Turn
			if turn == nil || (turn.Status == domain.ExecutionCompleted) != boundary {
				t.Fatalf("compaction result ignored native evidence: %+v", turn)
			}
		})
	}
}

func TestClaudeRecoveryRetainsOnlyObservedNativeInputAndResult(t *testing.T) {
	session := domain.Session{ID: domain.NewID(), ThreadID: domain.NewID()}
	home := t.TempDir()
	newRuntime := func() *runtime {
		return &runtime{home: home, session: session, process: &exec.Cmd{}, done: make(chan struct{})}
	}
	bound := newRuntime()
	out, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	bound.input = out
	ctx := context.Background()
	turnID, _, err := bound.SubmitDeferred(ctx, session, "observed-op", harness.TurnInput{Text: "test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	catalog := newRuntime()
	catalog.process = nil
	before, err := catalog.Inspect(ctx, session)
	if err != nil || before.Inputs["input:observed-op"] != "" {
		t.Fatalf("writing stdin falsely proved input receipt: %+v %v", before, err)
	}
	if err := bound.processFrame(frame{Type: "user", SessionID: session.ThreadID, UUID: turnID, IsReplay: true}); err != nil {
		t.Fatal(err)
	}
	received, err := catalog.Inspect(ctx, session)
	if err != nil || received.Inputs["input:observed-op"] != turnID || len(received.Turns) != 1 || received.Turns[0].Status != domain.ExecutionUnknown {
		t.Fatalf("missing native receipt without a result: %+v %v", received, err)
	}
	if err := bound.processFrame(frame{Type: "result", SessionID: session.ThreadID, Subtype: "success"}); err != nil {
		t.Fatal(err)
	}
	after, err := catalog.Inspect(ctx, session)
	if err != nil || after.Inputs["input:observed-op"] != turnID || len(after.Turns) != 1 || after.Turns[0].Status != domain.ExecutionCompleted {
		t.Fatalf("missing proved result after reopening: %+v %v", after, err)
	}
	another := session
	another.ThreadID = domain.NewID()
	other, err := catalog.Inspect(ctx, another)
	if err != nil || other.Available || len(other.Inputs) != 0 {
		t.Fatalf("receipt crossed native threads: %+v %v", other, err)
	}
}

func TestClaudePlanIsProjectedWithoutApprovingExecution(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	var events []domain.Event
	r := runtime{input: out, session: domain.Session{ID: "session", ThreadID: "thread"}, approvals: map[string]approval{},
		opts: harness.StartOptions{OnEvent: func(event domain.Event) error {
			events = append(events, event)
			return nil
		}},
		turn: &activeTurn{turn: domain.Turn{ID: "turn"}, plan: true},
	}
	for _, in := range []frame{
		{Type: "approval", RequestID: "write", ToolName: "Write", Input: json.RawMessage(`{}`)},
		{Type: "approval", RequestID: "plan", ToolName: "ExitPlanMode", Input: json.RawMessage(`{"plan":"## Plan\n- Inspect the tests"}`)},
	} {
		if err := r.processFrame(in); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 1 || events[0].Plan == nil || !strings.Contains(events[0].Plan.Narrative, "Inspect the tests") {
		t.Fatalf("missing plan without an execution approval: %+v", events)
	}
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(out)
	for _, id := range []string{"write", "plan"} {
		var reply struct {
			ID    string `json:"request_id"`
			Allow bool
		}
		if err := decoder.Decode(&reply); err != nil || reply.Allow || reply.ID != id {
			t.Fatalf("unexpected plan response: %+v %v", reply, err)
		}
	}
}

func TestClaudeHistoryForkEndsAtTheSelectedTurn(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for the SDK history fixture")
	}
	sdk := filepath.Join(t.TempDir(), "sdk.mjs")
	const source = "f2e4bf78-97b9-4a1e-8147-01d907a39f6a"
	const child = "5962ae01-49e4-4177-adbb-ae2bf33c7997"
	const first = "f58770ab-333a-4f97-bd1f-13be75d6f3ba"
	const response = "1f47d32e-04c1-4d38-aab8-8c2baad69462"
	const next = "14e54d96-006a-4055-8ad5-4e9198356a89"
	const remapped = "4464c528-24c5-458d-9ea3-a4e49a87bd78"
	const childResponse = "3b8fb184-0e9b-4d67-b32b-a3350db25b9c"
	fixture := `
const source = [record("user", "` + first + `", "first"), record("assistant", "` + response + `", "answer"), record("user", "` + next + `", "later")];
const child = [record("user", "` + remapped + `", "first"), record("assistant", "` + childResponse + `", "answer")];
function record(type, uuid, content) {
  return { type, uuid, message: { role: type, content: [{type:"text", text: content}] } };
}
export function getSessionMessages(id) { return id === "` + source + `" ? source : id === "` + child + `" ? child : []; }
export function forkSession(id, { upToMessageId }) {
  if (id !== "` + source + `" || upToMessageId !== "` + response + `") throw new Error("wrong history boundary");
  return { sessionId: "` + child + `" };
}`
	if err := os.WriteFile(sdk, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := runtime{node: node, sdk: sdk, home: t.TempDir()}
	s := domain.Session{ThreadID: source, Context: domain.LaunchContext{CWD: t.TempDir()}}
	request := harness.HistoryBoundary{TurnID: first}
	got, err := r.PrepareHistory(ctx, s, request)
	if err != nil || got != child {
		t.Fatalf("native verified prefix: %s %v", got, err)
	}
	request.PreparedRef = got
	if got, err := r.PrepareHistory(ctx, s, request); err != nil || got != child {
		t.Fatalf("could not reuse the prepared prefix: %s %v", got, err)
	}
	request.TurnID = next
	if _, err := r.PrepareHistory(ctx, s, request); err == nil {
		t.Fatal("saved prefix was accepted for a different turn")
	}
	restart, err := r.PrepareHistory(ctx, s, harness.HistoryBoundary{TurnID: next, Restart: true})
	if err != nil || restart != child {
		t.Fatalf("native restart should keep only the preceding turn: %s %v", restart, err)
	}
	empty, err := r.PrepareHistory(ctx, s, harness.HistoryBoundary{TurnID: first, Restart: true})
	if err != nil || empty == "" || empty == child || empty == source {
		t.Fatalf("editing the first input should have its own empty source thread: %s %v", empty, err)
	}
}

func TestClaudeSteerWaitsForItsOwnNativeReceipt(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	session := domain.Session{ID: domain.NewID(), ThreadID: domain.NewID()}
	var terminal []domain.Turn
	r := runtime{session: session, home: t.TempDir(), process: &exec.Cmd{}, input: out, done: make(chan struct{}),
		opts: harness.StartOptions{OnEvent: func(event domain.Event) error {
			if event.Turn != nil && !event.Turn.Status.Active() {
				terminal = append(terminal, *event.Turn)
			}
			return nil
		}},
	}
	ctx := context.Background()
	turn, _, err := r.SubmitDeferred(ctx, session, "first", harness.TurnInput{Text: "first"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := r.SteerDeferred(ctx, session, turn, "steer", harness.TurnInput{Text: "clarification"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.processFrame(frame{Type: "result", Subtype: "success", UUID: "primary-result"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-receipt:
		t.Fatal("an unrelated result confirmed a steer")
	default:
	}
	if len(terminal) != 0 {
		t.Fatal("the result ended a logical turn while the steer was still awaiting disposition")
	}
	native := nativeInputID(session.ThreadID, "steer")
	for _, raw := range []string{
		`{"type":"command_lifecycle","state":"started","command_uuid":"` + native + `"}`,
		`{"type":"result","subtype":"success","user_message_uuid":"` + native + `"}`,
	} {
		var in frame
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
		if err := r.processFrame(in); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-receipt:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("the exact native command did not confirm the steer")
	}
	if len(terminal) != 1 || terminal[0].ID != turn || terminal[0].Status != domain.ExecutionCompleted {
		t.Fatalf("steer changed logical turn identity: %+v", terminal)
	}
	source, err := r.Inspect(ctx, session)
	if err != nil || source.Inputs["input:first"] != turn || source.Inputs["input:steer"] != turn || len(source.Turns) != 1 {
		t.Fatalf("recovery lost primary or steer evidence: %+v (%v)", source, err)
	}
}

func TestClaudeMCPConfigurationPreservesPrivateValues(t *testing.T) {
	r := runtime{home: t.TempDir()}
	ctx := context.Background()
	catalog, err := r.ToolServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg := domain.ToolServiceConfiguration{Name: "Review", Transport: "http", URL: "https://example.test/mcp", Enabled: true,
		Headers: []domain.ToolServiceValue{{Name: "Authorization", Value: "fixture-private-header"}}}
	save := domain.ToolServiceChange{Action: "save", Revision: catalog.Revision, Configuration: &cfg}
	if _, err := r.ChangeToolService(ctx, save); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ChangeToolService(ctx, save); err == nil {
		t.Fatal("the stale revision replaced the current configuration")
	}
	catalog, err = r.ToolServices(ctx)
	if err != nil || len(catalog.Services) != 1 {
		t.Fatalf("invalid MCP catalog: %+v %v", catalog, err)
	}
	cfg = *catalog.Services[0].Configuration
	if len(cfg.Headers) != 1 || !cfg.Headers[0].Retained || cfg.Headers[0].Value != "" {
		t.Fatal("the private MCP header was returned to the client")
	}
	save.Revision, save.ServiceID = catalog.Revision, cfg.Name
	cfg.URL = "https://example.test/updated"
	if _, err := r.ChangeToolService(ctx, save); err != nil {
		t.Fatal(err)
	}
	doc, err := r.mcpConfiguration()
	if err != nil || !strings.Contains(string(doc.servers[cfg.Name]), "fixture-private-header") {
		t.Fatal("editing the target path dropped the retained header", err)
	}
	catalog, _ = r.ToolServices(ctx)
	save.Revision = catalog.Revision
	cfg.URL = "https://different.test/mcp"
	if _, err := r.ChangeToolService(ctx, save); err == nil {
		t.Fatal("a private header moved to a different origin")
	}
}

func TestClaudeBackgroundNotificationSettlesOnlyItsOwnedTask(t *testing.T) {
	var works []domain.WorkActivity
	now := time.Now()
	r := runtime{session: domain.Session{ID: "session", ThreadID: "thread"},
		turn: &activeTurn{turn: domain.Turn{ID: "turn", StartedAt: &now}, tools: map[string]string{"bash": "call"}},
		opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
			if e.Work != nil {
				works = append(works, *e.Work)
			}
			return nil
		}},
	}
	for _, raw := range []string{
		`{"type":"system","subtype":"task_started","task_id":"native","tool_use_id":"bash","task_type":"local_bash","is_backgrounded":true,"description":"Waiting"}`,
		`{"type":"result","subtype":"success"}`,
		`{"type":"system","subtype":"task_notification","task_id":"other","status":"completed","summary":"Unrelated"}`,
		`{"type":"system","subtype":"task_notification","task_id":"native","status":"completed","summary":"Done"}`,
	} {
		var in frame
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
		if err := r.processFrame(in); err != nil {
			t.Fatal(err)
		}
	}
	if len(works) != 2 || !works[0].Active() || works[0].SourceItemID != "call" ||
		works[0].ID != works[1].ID || works[1].State != "completed" || works[1].CanStop {
		t.Fatalf("wrong background task lifecycle: %+v", works)
	}
}

func TestClaudeToolResultSnapshotsInlineImage(t *testing.T) {
	var got domain.Item
	r := runtime{session: domain.Session{ID: "session", ThreadID: "thread"},
		opts: harness.StartOptions{
			ImportResource: func(source harness.OutputResource) (domain.Attachment, error) {
				if source.SessionID != "session" || source.ItemID != "call" || source.Kind != "image" || string(source.Data) != "image-fixture" {
					t.Fatalf("wrong resource boundary: %+v", source)
				}
				return domain.Attachment{ID: "owned-image"}, nil
			},
			OnEvent: func(e domain.Event) error { got = *e.Item; return nil },
		},
		turn: &activeTurn{turn: domain.Turn{ID: "turn"}, tools: map[string]string{"t": "call"}, items: map[string]domain.Item{"call": {ID: "call", Kind: "tool"}}},
	}
	in := frame{Type: "user"}
	in.Message.Content = []content{{Type: "tool_result", ToolUseID: "t",
		Content: json.RawMessage(`[{"type":"text","text":"rendered"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1hZ2UtZml4dHVyZQ=="}}]`)}}
	if err := r.processFrame(in); err != nil {
		t.Fatal(err)
	}
	if len(got.Parts) != 2 || got.Parts[0].Text != "rendered" || got.Parts[1].Attachment == nil || got.Parts[1].Attachment.ID != "owned-image" {
		t.Fatalf("wrong tool output projection: %+v", got.Parts)
	}
}

func TestClaudeSkillUsesAValidatedPrivatePlugin(t *testing.T) {
	workspace, home := t.TempDir(), t.TempDir()
	path := filepath.Join(workspace, ".claude", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: review\ndescription: Review a change\n---\nUnique skill instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	r := runtime{home: home}
	session := domain.Session{Context: domain.LaunchContext{CWD: workspace}}
	catalog, err := r.Skills(context.Background(), session.Context)
	if err != nil || len(catalog.Skills) != 1 {
		t.Fatalf("skill catalog: %+v %v", catalog, err)
	}
	in := harness.TurnInput{Text: "Review the fixture", Skills: []domain.SkillReference{catalog.Skills[0].SkillReference}}
	prepared, err := r.prepareInput(context.Background(), session, in)
	if err != nil || len(prepared.Plugins) != 1 || !strings.HasSuffix(prepared.Command, ":review") {
		t.Fatalf("skill was not prepared through the native command: %+v %v", prepared, err)
	}
	inventory, err := r.SkillInventory(context.Background(), session.Context)
	if err != nil {
		t.Fatal(err)
	}
	change := domain.SkillChange{ID: catalog.Skills[0].ID, Revision: inventory.Revision}
	if _, err := r.ChangeSkill(context.Background(), session.Context, change); err != nil {
		t.Fatal(err)
	}
	if _, err := r.prepareInput(context.Background(), session, in); err == nil {
		t.Fatal("a disabled skill was prepared from an older draft")
	}
	inventory, err = r.SkillInventory(context.Background(), session.Context)
	if err != nil {
		t.Fatal(err)
	}
	change.Revision, change.Enabled = inventory.Revision, true
	if _, err := r.ChangeSkill(context.Background(), session.Context, change); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: review\n---\nChanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.prepareInput(context.Background(), session, in); err == nil {
		t.Fatal("the changed skill resolved from an older draft reference")
	}
}

func TestClaudeGoalProgressRequiresItsCurrentToolReport(t *testing.T) {
	var progress *domain.GoalProgress
	r := runtime{opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
		if e.GoalProgress != nil {
			progress = e.GoalProgress
		}
		return nil
	}}}
	s := domain.Session{Context: domain.LaunchContext{CWD: t.TempDir()}}
	goal := &domain.GoalProgress{GoalID: "goal", RunID: "run"}
	input, err := r.prepareInput(context.Background(), s, harness.TurnInput{Objective: "Check the fixture", Goal: goal})
	if err != nil || input.GoalKey == "" {
		t.Fatalf("goal input was not prepared: %v", err)
	}
	now := time.Now()
	r.turn = &activeTurn{turn: domain.Turn{ID: "turn", StartedAt: &now}, goal: input.Goal, goalKey: input.GoalKey}
	if r.recordGoal("other", "complete") || !r.recordGoal(input.GoalKey, "blocked") {
		t.Fatal("the goal report used the wrong receipt identity")
	}
	if err := r.processFrame(frame{Type: "result", Subtype: "success"}); err != nil {
		t.Fatal(err)
	}
	if progress == nil || progress.GoalID != "goal" || progress.RunID != "run" || progress.Outcome != domain.GoalBlocked {
		t.Fatalf("wrong accepted goal report: %+v", progress)
	}
}

func TestClaudePathAttachmentStagesOnlyTheVerifiedSnapshot(t *testing.T) {
	dir, storage := t.TempDir(), t.TempDir()
	source := filepath.Join(storage, "resource")
	if err := os.WriteFile(source, []byte("immutable"), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("immutable"))
	attachment := harness.AttachmentInput{Path: source, Attachment: domain.Attachment{
		ID: "fixture", Name: "backup.zip", Kind: domain.PathAttachmentResource, MIME: "application/octet-stream",
		Size: 9, SHA256: hex.EncodeToString(sum[:]),
	}}
	r := runtime{home: dir}
	s := domain.Session{ID: "session", Context: domain.LaunchContext{CWD: t.TempDir()}}
	prepared, err := r.prepareInput(context.Background(), s, harness.TurnInput{Attachments: []harness.AttachmentInput{attachment}})
	if err != nil {
		t.Fatal(err)
	}
	text := prepared.Blocks[0]["text"].(string)
	if strings.Contains(text, source) || !strings.Contains(text, r.attachmentDirectory(s)) {
		t.Fatal("the prompt exposed the worker attachment store instead of the isolated snapshot")
	}
	if err := os.WriteFile(source, []byte("different"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.prepareInput(context.Background(), s, harness.TurnInput{Attachments: []harness.AttachmentInput{attachment}}); err == nil {
		t.Fatal("an attachment no longer matching its accepted digest was staged")
	}
}

func TestClaudeHooksLoadOnlyExplicitlyTrustedDefinitions(t *testing.T) {
	r := runtime{home: t.TempDir()}
	file := filepath.Join(r.home, "settings.json")
	write := func(command string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"hooks": map[string]any{"UserPromptSubmit": []map[string]any{
			{"hooks": []map[string]string{{"type": "command", "command": command}}},
		}}})
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("echo fixture")
	ctx := context.Background()
	list, err := r.Hooks(ctx, domain.LaunchContext{}, "")
	if err != nil || len(list.Entries) != 1 || !list.Entries[0].CanTrust || list.Entries[0].Enabled {
		t.Fatalf("unknown hook should require trust: %+v %v", list, err)
	}
	entry := list.Entries[0]
	change := domain.HookChange{ID: entry.ID, DefinitionRevision: entry.DefinitionRevision, Revision: list.Revision, Action: domain.HookTrustDefinition}
	if _, err := r.ChangeHook(ctx, domain.LaunchContext{}, change); err != nil {
		t.Fatal(err)
	}
	config, err := r.executionHooks()
	if err != nil || len(config["UserPromptSubmit"]) != 1 {
		t.Fatal("trusted hook was not selected", err)
	}
	write("echo replaced")
	config, err = r.executionHooks()
	if err != nil || len(config) != 0 {
		t.Fatal("altered command inherited trust", err)
	}
}

func TestClaudeModelOptionsRequireExactNativeModelAndProvider(t *testing.T) {
	model := domain.Model{ID: "claude-sonnet-4-6", Name: "Claude", Default: true}
	native := []nativeModel{
		{Value: "default", ResolvedModel: "claude-opus-5-5", SupportsFastMode: true, SupportsEffort: true, SupportedEffortLevels: []string{"low", "high"}},
		{Value: "claude-sonnet-4-6", ResolvedModel: "claude-sonnet-4-6", SupportsEffort: true, SupportedEffortLevels: []string{"low", "high", "invalid"}},
	}
	settings := configuration{Model: model.ID}
	r := runtime{config: settings, ready: domain.Readiness{Models: []domain.Model{mergeModelOptions(model, native)}}}
	if err := r.Validate(domain.RunConfig{Model: model.ID, Effort: "high"}); err != nil {
		t.Fatal("the exact model's native effort was refused", err)
	}
	if err := r.Validate(domain.RunConfig{Model: model.ID, Fast: true}); err == nil {
		t.Fatal("Fast leaked from a different model")
	}
	if officialEndpoint("https://api.anthropic.com.attacker.example") || officialEndpoint("https://api.deepseek.com/anthropic") ||
		!officialEndpoint("https://api.anthropic.com/v1") {
		t.Fatal("the native Anthropic catalog was attributed to another API gateway")
	}
}
