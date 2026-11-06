package worker_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/testutil"
	"shioji/internal/worker"
	"strings"
	"syscall"
	"testing"
	"time"
)

func startServer(t *testing.T) (*worker.Server, *protocol.Client, *testutil.Provider) {
	t.Helper()
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	server, err := worker.New(context.Background(), worker.Config{Directory: dir, CodexHome: p.Home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	go server.Serve()
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return server, client, p
}
func call(t *testing.T, c *protocol.Client, m string, in, out any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Call(ctx, m, in, out); err != nil {
		t.Fatal(err)
	}
}
func wait(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("condition timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A parked event read must wake on a committed change, without occupying the
// command connection or depending on the client's periodic housekeeping tick.
func TestEventWatchWakesOnCommitWithoutBlockingCommands(t *testing.T) {
	server, command, _ := startServer(t)
	watch := connect(t, server.Directory())
	var snapshot domain.Snapshot
	call(t, command, "snapshot", nil, &snapshot)
	type receipt struct {
		page protocol.EventPage
		err  error
	}
	received := make(chan receipt, 1)
	go func() {
		var page protocol.EventPage
		err := watch.Call(context.Background(), "events.watch", map[string]any{"after": snapshot.Sequence, "generation": snapshot.Generation, "waitMs": 1000}, &page)
		received <- receipt{page, err}
	}()
	var workspace domain.Workspace
	call(t, command, "workspace.create", worker.WorkspaceInput{Name: "Wake", Folders: []string{t.TempDir()}}, &workspace)
	select {
	case got := <-received:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.page.Generation != snapshot.Generation || len(got.page.Events) != 1 || got.page.Events[0].Workspace.ID != workspace.ID {
			t.Fatalf("watch lost the committed workspace: %+v", got.page)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("committed events still wait for a polling interval")
	}
}

func TestEventWatchWaitsWhenIdleAndRejectsInvalidCursor(t *testing.T) {
	_, client, _ := startServer(t)
	var snapshot domain.Snapshot
	call(t, client, "snapshot", nil, &snapshot)
	start := time.Now()
	var page protocol.EventPage
	call(t, client, "events.watch", map[string]any{"after": snapshot.Sequence, "generation": snapshot.Generation, "waitMs": 60}, &page)
	if time.Since(start) < 50*time.Millisecond || len(page.Events) != 0 {
		t.Fatal("idle watch spins instead of waiting")
	}
	for _, q := range []map[string]any{
		{"after": snapshot.Sequence + 1, "generation": snapshot.Generation, "waitMs": 1000},
		{"after": snapshot.Sequence, "generation": "replaced", "waitMs": 1000},
	} {
		if err := client.Call(context.Background(), "events.watch", q, &page); err == nil {
			t.Fatal("invalid cursor was accepted")
		}
	}
}
func TestWorkerDisconnectDoesNotStopParallelSessions(t *testing.T) {
	server, c, p := startServer(t)
	_ = server
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	if ready.State != "ready" {
		t.Fatalf("readiness: %+v", ready)
	}
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Test", Folders: []string{t.TempDir()}}, &w)
	var slow, fast domain.Operation
	cfg := domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}
	call(t, c, "session.submit", domain.Submission{OperationID: "slow", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: cfg}, &slow)
	select {
	case <-p.Started:
	case <-time.After(10 * time.Second):
		t.Fatal("slow not started")
	}
	call(t, c, "session.submit", domain.Submission{OperationID: "fast", WorkspaceID: w.ID, Text: "SHIOJI_FAST", Config: cfg}, &fast)
	c.Close()
	c2 := connect(t, server.Directory())
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c2, "session", worker.SessionQuery{ID: fast.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	call(t, c2, "session", worker.SessionQuery{ID: slow.SessionID}, &d)
	if d.Session.Status != "running" {
		t.Fatalf("disconnect stopped active session: %s", d.Session.Status)
	}
	close(p.Release)
	wait(t, func() bool {
		call(t, c2, "session", worker.SessionQuery{ID: slow.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	var snap domain.Snapshot
	call(t, c2, "snapshot", nil, &snap)
	if len(snap.Sessions) != 2 {
		t.Fatalf("sessions=%d", len(snap.Sessions))
	}
}
func connect(t *testing.T, dir string) *protocol.Client {
	t.Helper()
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func TestWorkerPendingQuestionSurvivesReconnectAndOnlyOneAnswerIsSent(t *testing.T) {
	server, c, _ := startServer(t)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Test", Folders: []string{t.TempDir()}}, &w)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "question", WorkspaceID: w.ID, Text: "SHIOJI_QUESTION", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only", Plan: true}}, &op)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &d)
		return len(d.Requests) > 0
	})
	c.Close()
	c2 := connect(t, server.Directory())
	call(t, c2, "session", worker.SessionQuery{ID: op.SessionID}, &d)
	r := d.Requests[0]
	answer := domain.Answer{RequestID: r.ID, Revision: r.Revision, Answers: map[string][]string{"q": {"Yes"}}}
	call(t, c2, "request.respond", worker.AnswerInput{SessionID: op.SessionID, Answer: answer}, nil)
	if err := c2.Call(context.Background(), "request.respond", worker.AnswerInput{SessionID: op.SessionID, Answer: answer}, nil); err == nil {
		t.Fatal("second answer accepted")
	}
	wait(t, func() bool {
		call(t, c2, "session", worker.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	if d.Requests[0].State != "answered" {
		t.Fatalf("request=%+v", d.Requests[0])
	}
}
func TestWrongDeviceAndOldEpochCannotMutateWorker(t *testing.T) {
	server, c, _ := startServer(t)
	conn, err := net.Dial("unix", filepath.Join(server.Directory(), "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	codec := protocol.NewCodec(conn, conn)
	nonce := "nonce"
	codec.Write(protocol.Frame{Type: "hello", Major: 1, Nonce: nonce})
	var welcome protocol.Frame
	if err = codec.Read(&welcome); err != nil {
		t.Fatal(err)
	}
	codec.Write(protocol.Frame{Type: "command", ID: "bad", Method: "workspace.create", DeviceID: c.DeviceID, Epoch: "old", Body: []byte(`{"name":"Bad","folders":["/tmp"]}`)})
	var response protocol.Frame
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if err = codec.Read(&response); err == nil && response.Error == "" {
		t.Fatal("old epoch accepted")
	}
	var snap domain.Snapshot
	call(t, c, "snapshot", nil, &snap)
	if len(snap.Workspaces) != 0 {
		t.Fatal("old epoch mutated state")
	}
}

func TestSessionAppServerDeathRecoversManagedExecutionWithoutReplayingInput(t *testing.T) {
	server, c, p := startServer(t)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Test", Folders: []string{t.TempDir()}}, &w)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "crash", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
	select {
	case <-p.Started:
	case <-time.After(10 * time.Second):
		t.Fatal("turn not dispatched")
	}
	b, err := os.ReadFile(filepath.Join(server.Directory(), "runtimes", op.SessionID, "runtime-process.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		PID int `json:"pid"`
	}
	if err = json.Unmarshal(b, &record); err != nil {
		t.Fatal(err)
	}
	if record.PID <= 0 {
		t.Fatal("no owned harness process")
	}
	if err = syscall.Kill(record.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	var detail domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &detail)
		return detail.Session.Status == "interrupted" && detail.Session.Recovery != nil && detail.Session.Recovery.CanContinue
	})
	for _, turn := range detail.Turns {
		if turn.Status == "running" {
			t.Fatal("dead harness still has a running turn")
		}
	}
	var again domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "crash", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &again)
	if again.SessionID != op.SessionID {
		t.Fatal("replayed into a new session")
	}
	select {
	case <-p.Started:
		t.Fatal("input was replayed")
	default:
	}
	close(p.Release)
}

func TestWorkerExecutionApprovalCanBeDenied(t *testing.T) {
	_, c, _ := startServer(t)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Test", Folders: []string{t.TempDir()}}, &w)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "approval", WorkspaceID: w.ID, Text: "SHIOJI_APPROVAL", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "default"}}, &op)
	var detail domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &detail)
		return len(detail.Requests) > 0
	})
	r := detail.Requests[0]
	if r.Kind != "approval" {
		t.Fatalf("request kind=%s", r.Kind)
	}
	decision := ""
	for _, choice := range r.Choices {
		if choice.ID == "decline" || choice.ID == "cancel" {
			decision = choice.ID
		}
	}
	if decision == "" {
		t.Fatal("runtime supplied no deny/cancel choice", r.Choices)
	}
	call(t, c, "request.respond", worker.AnswerInput{SessionID: op.SessionID, Answer: domain.Answer{RequestID: r.ID, Revision: r.Revision, Decision: decision}}, nil)
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &detail)
		return detail.Session.Status == "completed" || detail.Session.Status == "interrupted"
	})
	if detail.Requests[0].State != "answered" {
		t.Fatalf("request=%+v", detail.Requests[0])
	}
}

func TestGitSubmissionProtectsCheckoutAndCreatesIndependentWorktree(t *testing.T) {
	server, c, p := startServer(t)
	_ = server
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		b, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, b, err)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "-b", "main")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.invalid")
	os.WriteFile(filepath.Join(dir, "file"), []byte("original"), 0600)
	git("add", ".")
	git("commit", "-m", "initial")
	git("branch", "other")
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Git", Folders: []string{dir}}, &w)
	cfg := domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}
	var a, b domain.Operation
	call(t, c, "session.submit", map[string]any{"operationId": "git-a", "workspaceId": w.ID, "text": "SHIOJI_SLOW", "config": cfg, "git": map[string]any{"branch": "main"}}, &a)
	select {
	case <-p.Started:
	case <-time.After(10 * time.Second):
		t.Fatal("slow turn did not start")
	}
	blocked := map[string]any{"operationId": "git-blocked", "workspaceId": w.ID, "text": "SHIOJI_FAST", "config": cfg, "git": map[string]any{"branch": "other"}}
	if err := c.Call(context.Background(), "session.submit", blocked, nil); err == nil {
		t.Fatal("home branch intent bypassed busy checkout")
	}
	call(t, c, "session.submit", map[string]any{"operationId": "git-worktree", "workspaceId": w.ID, "text": "SHIOJI_FAST", "config": cfg, "git": map[string]any{"branch": "other", "newWorktree": true}}, &b)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: b.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	if d.Session.Context.CWD == w.Folders[0] || !strings.HasPrefix(d.Session.Context.CWD, canonicalTestPath(t, server.Directory())) {
		t.Fatalf("worktree launch scope %+v", d.Session.Context)
	}
	if git("branch", "--show-current") != "main" {
		t.Fatal("worktree creation switched shared checkout")
	}
	close(p.Release)
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: a.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	if err := c.Call(context.Background(), "session.submit", map[string]any{"operationId": "empty-git", "workspaceId": w.ID, "text": " ", "config": cfg, "git": map[string]any{"branch": "other"}}, nil); err == nil {
		t.Fatal("empty submission accepted")
	}
	if git("branch", "--show-current") != "main" {
		t.Fatal("invalid submission changed the checkout")
	}
	call(t, c, "git.checkout", map[string]any{"workspaceId": w.ID, "sessionId": a.SessionID, "branch": "other"}, nil)
	os.WriteFile(filepath.Join(dir, "untracked"), []byte("preserve"), 0600)
	call(t, c, "session.submit", domain.Submission{OperationID: "git-dirty-same", WorkspaceID: w.ID, SessionID: a.SessionID, Text: "SHIOJI_FAST", Config: cfg}, nil)
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: a.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	git("switch", "main")
	if err := c.Call(context.Background(), "session.submit", domain.Submission{OperationID: "git-context-changed", WorkspaceID: w.ID, SessionID: a.SessionID, Text: "SHIOJI_FAST", Config: cfg}, nil); err == nil {
		t.Fatal("external branch change silently accepted")
	}
	call(t, c, "git.checkout", map[string]any{"workspaceId": w.ID, "sessionId": a.SessionID, "branch": "main"}, nil)
	call(t, c, "session.submit", domain.Submission{OperationID: "git-confirmed", WorkspaceID: w.ID, SessionID: a.SessionID, Text: "SHIOJI_FAST", Config: cfg}, nil)
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: a.SessionID}, &d)
		return d.Session.Status == "completed"
	})
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDurableQueueDispatchesFIFOAfterRealCompletion(t *testing.T) {
	_, c, p := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", protocol.WorkspaceInput{Name: "queue", Folders: []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	cfg := domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: cfg}, &op)
	select {
	case <-p.Started:
	case <-time.After(10 * time.Second):
		t.Fatal("not running")
	}
	for _, text := range []string{"second", "third"} {
		var queued domain.Operation
		call(t, c, "turn.enqueue", domain.Submission{OperationID: text, WorkspaceID: w.ID, SessionID: op.SessionID, Text: text, Config: cfg}, &queued)
	}
	var d domain.SessionDetail
	call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
	if len(d.Queue.Items) != 2 {
		t.Fatal(d.Queue)
	}
	for _, item := range d.Items {
		if item.Kind == "user" && item.Text != "SHIOJI_SLOW" {
			t.Fatal("queued input in history")
		}
	}
	close(p.Release)
	wait(t, func() bool {
		call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed" && len(d.Queue.Items) == 0 && len(p.Inputs()) == 3
	})
	var texts []string
	for _, item := range d.Items {
		if item.Kind == "user" {
			texts = append(texts, item.Text)
		}
	}
	if strings.Join(texts, ",") != "SHIOJI_SLOW,second,third" {
		t.Fatal(texts)
	}
}

func TestRealSteerUsesActiveTurnAndLeavesQueueIntact(t *testing.T) {
	_, c, p := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", protocol.WorkspaceInput{Name: "steer", Folders: []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	cfg := domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "initial", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: cfg}, &op)
	select {
	case <-p.Started:
	case <-time.After(10 * time.Second):
		t.Fatal("not running")
	}
	var d domain.SessionDetail
	call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
	turn := ""
	for _, v := range d.Turns {
		if v.Status == "running" {
			turn = v.ID
		}
	}
	if turn == "" {
		t.Fatal("no turn")
	}
	var steered domain.Operation
	call(t, c, "turn.steer", protocol.SteerInput{TurnID: turn, Input: domain.Submission{OperationID: "steering", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "explain briefly", Config: cfg}}, &steered)
	wait(t, func() bool {
		call(t, c, "operation", map[string]string{"id": "steering"}, &steered)
		return steered.State == "confirmed" || steered.State == "rejected"
	})
	if steered.State != "confirmed" {
		t.Fatal(steered)
	}
	close(p.Release)
	wait(t, func() bool {
		call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	turns := map[string]bool{}
	for _, item := range d.Items {
		if item.Kind == "user" {
			turns[item.TurnID] = true
		}
	}
	if len(turns) != 1 || !turns[turn] {
		t.Fatal("steer created another turn", turns)
	}
}

func TestStopPreservesQueueUntilExplicitContinue(t *testing.T) {
	_, c, p := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", protocol.WorkspaceInput{Name: "stop", Folders: []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	cfg := domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: cfg}, &op)
	select {
	case <-p.Started:
	case <-time.After(10 * time.Second):
		t.Fatal("not running")
	}
	var queued domain.Operation
	call(t, c, "turn.enqueue", domain.Submission{OperationID: "next", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "retained next", Config: cfg}, &queued)
	var d domain.SessionDetail
	call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
	turn := ""
	for _, v := range d.Turns {
		if v.Status == "running" {
			turn = v.ID
		}
	}
	call(t, c, "turn.interrupt", map[string]string{"sessionId": op.SessionID, "turnId": turn}, nil)
	wait(t, func() bool {
		call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "interrupted"
	})
	if !d.Queue.Paused || len(d.Queue.Items) != 1 || len(p.Inputs()) != 1 {
		t.Fatal("Stop dispatched or deleted queue", d.Queue, p.Inputs())
	}
	close(p.Release)
	call(t, c, "queue.continue", protocol.QueueInput{SessionID: op.SessionID, Revision: d.Queue.Revision}, &d.Queue)
	wait(t, func() bool {
		call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed" && len(d.Queue.Items) == 0
	})
	if len(p.Inputs()) != 2 {
		t.Fatal("continue delivered more than once", p.Inputs())
	}
}

func TestQueuedInputDoesNotResolvePendingQuestion(t *testing.T) {
	_, c, _ := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", protocol.WorkspaceInput{Name: "questions", Folders: []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	cfg := domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only", Plan: true}
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "first", WorkspaceID: w.ID, Text: "SHIOJI_QUESTION", Config: cfg}, &op)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
		return len(d.Requests) > 0
	})
	r := d.Requests[0]
	var queued domain.Operation
	call(t, c, "turn.enqueue", domain.Submission{OperationID: "queued", WorkspaceID: w.ID, SessionID: op.SessionID, Text: "after answer", Config: cfg}, &queued)
	call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
	if len(d.Queue.Items) != 1 || d.Requests[0].State != "pending" {
		t.Fatal("ordinary queued input resolved question", d)
	}
	call(t, c, "request.respond", protocol.AnswerInput{SessionID: op.SessionID, Answer: domain.Answer{RequestID: r.ID, Revision: r.Revision, Answers: map[string][]string{"q": {"Yes"}}}}, nil)
	wait(t, func() bool {
		call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed" && len(d.Queue.Items) == 0
	})
}

func TestQueueReorderChangesRealDispatchOrder(t *testing.T) {
	_, c, p := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", protocol.WorkspaceInput{Name: "reorder", Folders: []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	cfg := domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "initial", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: cfg}, &op)
	select {
	case <-p.Started:
	case <-time.After(10 * time.Second):
		t.Fatal("not running")
	}
	for _, id := range []string{"one", "two"} {
		var queued domain.Operation
		call(t, c, "turn.enqueue", domain.Submission{OperationID: id, SessionID: op.SessionID, WorkspaceID: w.ID, Text: id, Config: cfg}, &queued)
	}
	var d domain.SessionDetail
	call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
	var q domain.QueueState
	call(t, c, "queue.reorder", protocol.QueueInput{SessionID: op.SessionID, ID: "two", BeforeID: "one", Revision: d.Queue.Revision}, &q)
	if q.Items[0].ID != "two" {
		t.Fatal(q)
	}
	close(p.Release)
	wait(t, func() bool {
		call(t, c, "session", protocol.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed" && len(d.Queue.Items) == 0 && len(p.Inputs()) == 3
	})
	var texts []string
	for _, item := range d.Items {
		if item.Kind == "user" {
			texts = append(texts, item.Text)
		}
	}
	if strings.Join(texts, ",") != "SHIOJI_SLOW,two,one" {
		t.Fatal("runtime ignored saved order", texts)
	}
}
