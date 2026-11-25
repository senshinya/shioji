package claudecode

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestForeignSDKOutputCannotConfirmOrFinishCurrentClaudeTurn(t *testing.T) {
	var events []domain.Event
	now := time.Now()
	receipt := make(chan error, 1)
	active := &activeTurn{
		uuid: "current-input", receipt: receipt, turn: domain.Turn{ID: "turn", StartedAt: &now},
		items: map[string]domain.Item{},
	}
	r := runtime{turn: active, opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
		events = append(events, e)
		return nil
	}}}
	for _, raw := range []string{
		`{"type":"assistant","user_message_uuid":"previous-input","message":{"id":"stale","content":[{"type":"text","text":"old"}]}}`,
		`{"type":"result","subtype":"success","user_message_uuids":["previous-input"]}`,
	} {
		var in frame
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
		if err := r.processFrame(in); err != nil {
			t.Fatal(err)
		}
	}
	if r.turn != active || len(receipt) != 0 || len(events) != 0 {
		t.Fatalf("foreign output settled the current turn: events=%d receipt=%d", len(events), len(receipt))
	}
	if err := r.processFrame(frame{Type: "command_lifecycle", CommandUUID: active.uuid, State: "started"}); err != nil {
		t.Fatal(err)
	}
	if len(receipt) != 1 || <-receipt != nil {
		t.Fatal("an exact native start should confirm its own input")
	}
}

func TestClaudeWithdrawnApprovalIsInvalidated(t *testing.T) {
	var events []domain.Event
	now := time.Now()
	r := runtime{
		turn: &activeTurn{turn: domain.Turn{ID: "turn", StartedAt: &now}},
		approvals: map[string]approval{
			"first":  {native: "withdrawn", request: domain.PendingRequest{ID: "first", TurnID: "turn", Revision: 1, State: domain.RequestPending}},
			"second": {native: "at-end", request: domain.PendingRequest{ID: "second", TurnID: "turn", Revision: 1, State: domain.RequestPending}},
		},
		opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
			events = append(events, e)
			return nil
		}},
	}
	for _, in := range []frame{{Type: "approval_cancelled", RequestID: "withdrawn"}, {Type: "result", Subtype: "success"}} {
		if err := r.processFrame(in); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 3 {
		t.Fatalf("expected two closed cards and one terminal event, got %d", len(events))
	}
	for _, e := range events[:2] {
		if e.Request == nil || e.Request.State != domain.RequestInvalidated || e.Request.Revision != 2 {
			t.Fatalf("withdrawn request treated as answered: %+v", e.Request)
		}
	}
}

func TestClaudeKeepsPrivateProfilesOutsideWorkspaceToolAccess(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js runs the bridge fixture")
	}
	cwd := t.TempDir()
	profiles := filepath.Join(cwd, "private-profiles")
	profile := filepath.Join(profiles, "current")
	sdk := filepath.Join(t.TempDir(), "fixture.mjs")
	body := `
export function query({ options }) {
  return {
    async initializationResult() {
      const base = ` + string(mustJSON(profiles)) + `;
      const deny = options.settings.permissions?.deny ?? [];
      const glob = "(//" + base.slice(1) + "/**)";
      const fs = options.settings.sandbox.filesystem;
      if (!deny.includes("Read"+glob) || !deny.includes("Edit"+glob) ||
          !fs.denyRead.includes(base) || !fs.denyWrite.includes(base)) throw new Error("missing private-profile rules");
    },
    async *[Symbol.asyncIterator]() { await new Promise(() => {}); },
    close() { process.stdin.destroy(); },
  };
}`
	if err := os.WriteFile(sdk, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	r := runtime{
		config: configuration{Endpoint: "http://127.0.0.1:1", Model: "fixture"},
		home:   profile, privateProfiles: profiles, node: node, sdk: sdk,
		opts: harness.StartOptions{SessionDirectory: t.TempDir()}, done: make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	defer func() { _ = r.Close(ctx) }()
	s := domain.Session{ID: domain.NewID(), Config: domain.RunConfig{Model: "fixture"}, Context: domain.LaunchContext{CWD: cwd}}
	if _, err := r.Create(ctx, s); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(value string) []byte {
	raw, _ := json.Marshal(value)
	return raw
}

func TestClaudeApprovalNeedsNativeAcknowledgment(t *testing.T) {
	read, write := io.Pipe()
	defer read.Close()
	defer write.Close()
	r := runtime{
		input: write, done: make(chan struct{}), turn: &activeTurn{},
		approvals: map[string]approval{"card": {native: "native", request: domain.PendingRequest{
			ID: "card", Revision: 1, State: domain.RequestPending,
		}}},
	}
	finished := make(chan error, 1)
	go func() { finished <- r.Respond(domain.Answer{RequestID: "card", Revision: 1, Decision: "accept"}) }()
	var wire map[string]any
	if err := json.NewDecoder(read).Decode(&wire); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		t.Fatalf("writing a decision to the pipe settled the request: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	r.mu.Lock()
	err := r.processFrame(frame{Type: "approval_cancelled", RequestID: "native"})
	r.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("the SDK withdrew the approval")
		}
	case <-time.After(time.Second):
		t.Fatal("the responder did not observe the withdrawal")
	}
}

func TestClaudeStopDoesNotRelabelANaturalResult(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "control-*")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	now := time.Now()
	s := domain.Session{ID: "session", ThreadID: "thread"}
	var terminal domain.ExecutionState
	r := runtime{session: s, input: out,
		turn: &activeTurn{uuid: "native", turn: domain.Turn{ID: "turn", StartedAt: &now}},
		opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
			if e.Turn != nil {
				terminal = e.Turn.Status
			}
			return nil
		}},
	}
	if err := r.Interrupt(context.Background(), s, "turn"); err != nil {
		t.Fatal(err)
	}
	if err := r.processFrame(frame{Type: "result", Subtype: "success", UserMessageUUID: "native"}); err != nil {
		t.Fatal(err)
	}
	if terminal != domain.ExecutionCompleted {
		t.Fatalf("the Stop request relabelled an already completed native result: %s", terminal)
	}
}

func TestClaudeMCPInspectionRequiresMatchingNativeConfiguration(t *testing.T) {
	s := domain.Session{ThreadID: "thread", Context: domain.LaunchContext{CWD: t.TempDir()}}
	r := runtime{home: t.TempDir(), session: s}
	path := filepath.Join(r.home, mcpFile)
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"service":{"type":"http","url":"https://old.example.test/mcp"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := r.mcpConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"type": "mcp_status", "session_id": s.ThreadID, "mcp_revision": doc.revision,
		"servers": []mcpObservation{{Name: "service", Status: "connected"}}})
	var status frame
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	if err := r.processFrame(status); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"service":{"type":"http","url":"https://new.example.test/mcp"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	inspection, err := r.InspectToolServices(context.Background(), s.Context, s.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.RuntimeKnown || len(inspection.Entries) != 1 || inspection.Entries[0].State == domain.ToolServiceReady {
		t.Fatalf("a newly configured server inherited an old native connection: %+v", inspection)
	}
}

func TestClaudeMCPRetainedValuesStayWithTheirOriginalCommand(t *testing.T) {
	cfg := domain.ToolServiceConfiguration{
		Name: "service", Transport: "command", Enabled: true, Command: "/usr/bin/other",
		Environment: []domain.ToolServiceValue{{Name: "PRIVATE", Retained: true}},
	}
	old := nativeMCP{Command: "/usr/bin/original", Env: map[string]string{"PRIVATE": "fixture"}}
	if err := editMCP(map[string]json.RawMessage{}, cfg, old); err == nil {
		t.Fatal("a changed command inherited the private configuration of another command")
	}
}

func TestClaudeCatalogVersionTracksRuntimeImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude-fixture")
	if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	a := Adapter{Executable: path, home: dir}
	before, err := a.CatalogVersion()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("updated fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	after, err := a.CatalogVersion()
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("the installed Claude runtime changed without refreshing the catalog")
	}
}

func TestClaudeSubagentApprovalBelongsToItsIndependentWork(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "control-*")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	var got []domain.PendingRequest
	r := runtime{input: out, session: domain.Session{ID: "session", ThreadID: "thread"}, approvals: map[string]approval{},
		work: map[string]domain.WorkActivity{"native-child": {ID: "child", TurnID: "parent-turn", Kind: "agent", State: "running"}},
		opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
			if e.Request != nil {
				got = append(got, *e.Request)
			}
			return nil
		}},
	}
	var in frame
	if err := json.Unmarshal([]byte(`{"type":"approval","agent_id":"native-child","request_id":"native-approval","tool_name":"Read","input":{"file_path":"fixture.txt"}}`), &in); err != nil {
		t.Fatal(err)
	}
	if err := r.processFrame(in); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].WorkID != "child" || got[0].TurnID != "parent-turn" {
		t.Fatalf("the child approval was lost or assigned to the foreground conversation: %+v", got)
	}
	if err := r.processFrame(frame{Type: "system", Subtype: "task_notification", TaskID: "native-child", Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].State != domain.RequestInvalidated {
		t.Fatalf("finished child left an unanswerable approval: %+v", got)
	}
}

func TestClaudeProjectHookInspectionStaysInsideTheWorkspace(t *testing.T) {
	work, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "settings.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo private fixture"}]}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(work, ".claude")); err != nil {
		t.Fatal(err)
	}
	r := runtime{home: t.TempDir()}
	catalog, err := r.Hooks(context.Background(), domain.LaunchContext{CWD: work}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Entries) != 0 {
		t.Fatal("Hook inspection read a configuration outside the workspace")
	}
}

func TestClaudeImportedMessageKeepsItsNativeTimestamp(t *testing.T) {
	r := runtime{}
	var recorded historyRecord
	if err := json.Unmarshal([]byte(`{"UUID":"native","Type":"user","User":true,"Timestamp":"2026-01-03T15:11:10Z","Message":{"content":"fixture"}}`), &recorded); err != nil {
		t.Fatal(err)
	}
	got := r.externalItems(recorded, "turn")
	if len(got) != 1 || got[0].SentAt == nil || got[0].SentAt.Format(time.RFC3339) != recorded.Timestamp {
		t.Fatalf("the imported user message lost its timestamp: %+v", got)
	}
	recorded.Timestamp = ""
	got = r.externalItems(recorded, "turn")
	if len(got) != 1 || got[0].SentAt != nil {
		t.Fatalf("an unknown timestamp should remain absent: %+v", got)
	}
}

func TestClaudeLargeFileEditStillOffersApproval(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "control-*")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	var opened *domain.PendingRequest
	r := runtime{input: out, turn: &activeTurn{turn: domain.Turn{ID: "turn"}},
		opts: harness.StartOptions{OnEvent: func(e domain.Event) error {
			opened = e.Request
			return nil
		}},
	}
	input, _ := json.Marshal(map[string]string{"file_path": "/workspace/example.txt", "content": strings.Repeat("fixture line\n", 1000)})
	if err := r.processFrame(frame{Type: "approval", ToolName: "Write", RequestID: "native", Input: input}); err != nil {
		t.Fatal(err)
	}
	if opened == nil || !strings.Contains(opened.Text, "/workspace/example.txt") || len(opened.Text) > 4096 {
		t.Fatalf("the native file write did not get a bounded, path-specific approval: %+v", opened)
	}
}
