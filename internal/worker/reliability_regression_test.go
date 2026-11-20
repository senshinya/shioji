package worker

import (
	"context"
	"database/sql"
	"errors"
	"os/exec"
	"path/filepath"
	"shioji/internal/supervisor"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

type auditPrePromptAdapter struct {
	memoryHarness
	createEntered, release, submitted chan struct{}
}

func (a auditPrePromptAdapter) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, err := a.memoryHarness.Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &auditPrePromptRuntime{r.(*memoryRuntime), a.createEntered, a.release, a.submitted}, nil
}

type auditPrePromptRuntime struct {
	*memoryRuntime
	createEntered, release, submitted chan struct{}
}

func (r *auditPrePromptRuntime) Create(ctx context.Context, s domain.Session) (string, error) {
	close(r.createEntered)
	select {
	case <-r.release:
		return r.memoryRuntime.Create(ctx, s)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (r *auditPrePromptRuntime) Submit(ctx context.Context, s domain.Session, id string, in harness.TurnInput) (string, error) {
	close(r.submitted)
	return r.memoryRuntime.Submit(ctx, s, id, in)
}
func TestStopRevokesAcceptedInputBeforeNativeHandoff(t *testing.T) {
	entered, release, submitted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s, w := memoryWorker(t, auditPrePromptAdapter{memoryHarness: memoryHarness{id: "audit"}, createEntered: entered, release: release, submitted: submitted})
	op, err := s.submit(domain.Submission{OperationID: "stop-before-handoff", WorkspaceID: w.ID, Text: "temporary", Config: domain.RunConfig{Model: "audit-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("did not enter thread creation")
	}
	receipt, err := s.store.Operation(op.ID)
	if err != nil || receipt.State != domain.OperationAccepted {
		t.Fatalf("before Stop: %v %v", receipt.State, err)
	}
	if err = s.interruptTurn(op.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	q, err := s.store.Queue(op.SessionID)
	if err != nil || !q.Paused {
		t.Fatalf("Stop did not pause: %v %v", q.Paused, err)
	}
	close(release)
	select {
	case <-submitted:
		t.Fatal("input entered the harness after Stop")
	case <-time.After(100 * time.Millisecond):
	}
	receipt = awaitReceipt(t, s, op.ID)
	if receipt.State != domain.OperationRejected {
		t.Fatalf("stopped input was not rejected: %v", receipt.State)
	}
}

func (r *auditPrePromptRuntime) Resume(ctx context.Context, s domain.Session) error {
	close(r.createEntered)
	select {
	case <-r.release:
		return r.memoryRuntime.Resume(ctx, s)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestStopPreservesReservedQueueDraftBeforeNativeHandoff(t *testing.T) {
	entered, release, submitted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s, w := memoryWorker(t, auditPrePromptAdapter{memoryHarness: memoryHarness{id: "audit"}, createEntered: entered, release: release, submitted: submitted})
	cfg := domain.RunConfig{Model: "audit-model", Permission: "limited"}
	first, err := s.store.Accept(domain.Submission{OperationID: "previous", HarnessID: "audit", WorkspaceID: w.ID, Text: "previous", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	details, err := s.store.Session(first.SessionID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	previous := details.Session
	previous.ThreadID = "existing-thread"
	previous.Status = domain.ExecutionCompleted
	if _, err = s.store.Apply(previous.ID, domain.Event{Type: "session", Session: &previous}); err != nil {
		t.Fatal(err)
	}
	if err = s.store.SetOperation(first.ID, domain.OperationConfirmed, ""); err != nil {
		t.Fatal(err)
	}
	_, err = s.enqueue(domain.Submission{OperationID: "queued-draft", WorkspaceID: w.ID, SessionID: previous.ID, Text: "keep until explicit send", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("queue did not enter native resume")
	}
	q, err := s.store.Queue(previous.ID)
	if err != nil || len(q.Items) != 1 || q.Items[0].State != "dispatching" {
		t.Fatalf("not reserved: %+v %v", q, err)
	}
	delivery := q.Items[0].Input.OperationID
	if err = s.interruptTurn(previous.ID, "", first.ID); err == nil {
		t.Fatal("an old Stop request still matched the new input")
	}
	if op, err := s.store.Operation(delivery); err != nil || op.State != domain.OperationAccepted {
		t.Fatalf("stale Stop touched a new delivery: %+v %v", op, err)
	}
	if err = s.interruptTurn(previous.ID, "", delivery); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-submitted:
		t.Fatal("reserved queued input reached the harness after Stop")
	case <-time.After(100 * time.Millisecond):
	}
	receipt := awaitReceipt(t, s, delivery)
	after, err := s.store.Queue(previous.ID)
	if err != nil || receipt.State != domain.OperationRejected || !after.Paused || len(after.Items) != 1 || after.Items[0].Input.Text != "keep until explicit send" {
		t.Fatalf("after late delivery: %+v %+v %v", receipt, after, err)
	}
}

func TestManagedSafeDrainWaitsForLiveChildApproval(t *testing.T) {
	s, id, turn := nativeWorkReply(t)
	d, err := s.store.Session(id, 0, 10)
	if err != nil || d.Session.Status != domain.ExecutionCompleted || s.currentRuntime(id) == nil {
		t.Fatalf("bad live child fixture: %v %v", d.Session.Status, err)
	}
	req := domain.PendingRequest{ID: "audit-child-approval", WorkID: "child", TurnID: turn, Kind: "approval", State: "pending", Revision: 1, Choices: []domain.RequestChoice{{ID: "allow", Label: "允许"}}}
	if err = s.onEvent(domain.Event{Type: "request.open", SessionID: id, Request: &req}); err != nil {
		t.Fatal(err)
	}
	s.config.ServiceInstance = "audit-private"
	denied, err := s.store.PrepareWorkerStop("ordinary stop")
	if err != nil || denied.Ready {
		t.Fatalf("ordinary stop should protect the live approval: %+v %v", denied, err)
	}
	got, err := s.DrainService(supervisor.Control{Instance: "audit-private", OperationID: "audit-drain", Replacement: domain.WorkerReplacement{Policy: "whenIdle"}})
	if err != nil || got.Ready {
		t.Fatalf("managed drain failed to protect the live approval: %+v %v", got, err)
	}
}

func TestRejectedSubmitKeepsSharedCheckout(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "audit"})
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("/usr/bin/git", append([]string{"-C", w.Folders[0]}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "original")
	git("-c", "user.name=Audit", "-c", "user.email=audit@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	git("branch", "target")
	in := domain.Submission{OperationID: "rejected-git", WorkspaceID: w.ID, Text: strings.Repeat("<", 90<<10),
		Config: domain.RunConfig{Model: "audit-model", Permission: "limited"}, Git: domain.GitChoice{Branch: "target"}}
	if err := in.ValidateContent(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.submit(in); err == nil || !strings.Contains(err.Error(), "recoverable page budget") {
		t.Fatalf("unexpected submit: %v", err)
	}
	if _, err := s.store.Operation(in.OperationID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("submission has receipt: %v", err)
	}
	if head := git("symbolic-ref", "--short", "HEAD"); head != "original" {
		t.Fatalf("branch after rejection: %s", head)
	}
}

func TestQueueWaitOnOneSessionLeavesIndependentQueueRunnable(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "audit"})
	cfg := domain.RunConfig{Model: "audit-model", Permission: "limited"}
	differentDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.store.CreateWorkspace("other", []string{differentDir})
	if err != nil {
		t.Fatal(err)
	}
	start := func(id, workspace string) string {
		t.Helper()
		op, err := s.submit(domain.Submission{OperationID: id, WorkspaceID: workspace, Text: "seed", Config: cfg})
		if err != nil {
			t.Fatal(err)
		}
		awaitReceipt(t, s, op.ID)
		return op.SessionID
	}
	a, b := start("queue-a", w.ID), start("queue-b", other.ID)
	s.dispatchMu.Lock()
	release, err := s.beginSessionTransition(s.ctx, a)
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() { s.dispatchMu.Lock(); release(); s.dispatchMu.Unlock() }
	defer cleanup()
	for id, workspace := range map[string]string{a: w.ID, b: other.ID} {
		if _, err := s.store.Enqueue(domain.Submission{OperationID: "queued:" + id, WorkspaceID: workspace, SessionID: id, Text: "next", Config: cfg}); err != nil {
			t.Fatal(err)
		}
	}
	q, err := s.store.Queue(b)
	if err != nil || len(q.Items) != 1 {
		t.Fatalf("bad queue fixture: %+v %v", q, err)
	}
	opid := q.Items[0].Input.OperationID
	s.wakeQueue()
	time.Sleep(350 * time.Millisecond)
	if op, err := s.store.Operation(opid); err != nil || op.State != domain.OperationConfirmed {
		t.Fatalf("independent B did not dispatch while A gate was pending: %+v %v", op, err)
	}
	cleanup()
	if op := awaitReceipt(t, s, opid); op.State != domain.OperationConfirmed {
		t.Fatalf("queue B did not run after releasing A: %+v", op)
	}
}

func TestCheckoutPreparationFailureProducesDurableNegativeReceipt(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "audit"})
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("/usr/bin/git", append([]string{"-C", w.Folders[0]}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "original")
	git("-c", "user.name=Audit", "-c", "user.email=audit@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	git("branch", "target")
	db, err := sql.Open("sqlite", filepath.Join(s.config.Directory, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER audit_reject_operation BEFORE INSERT ON operations
		WHEN NEW.id='late-git-reject' BEGIN SELECT RAISE(ABORT, 'injected adoption failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	in := domain.Submission{OperationID: "late-git-reject", WorkspaceID: w.ID, Text: "keep",
		Config: domain.RunConfig{Model: "audit-model", Permission: "limited"}, Git: domain.GitChoice{Branch: "target"}}
	if _, err = s.submit(in); err == nil || !strings.Contains(err.Error(), "target") {
		t.Fatalf("submission did not disclose checkout preparation: %v", err)
	}
	if head := git("symbolic-ref", "--short", "HEAD"); head != "target" {
		t.Fatalf("fixture did not reach external checkout: %s", head)
	}
	query := protocol.OperationQuery{ID: in.OperationID, SealGeneration: s.store.Generation()}
	op, err := s.inputReceipt(query)
	if err != nil || op.State != domain.OperationRejected || !strings.Contains(op.Error, "target") {
		t.Fatalf("lost checkout notice on receipt: %+v %v", op, err)
	}
}
