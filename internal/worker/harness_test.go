package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

// This driver speaks only Shioji semantics. It has no Codex fixture, wire type,
// native request ID, executor or protocol-shaped response.
type memoryHarness struct {
	id                       string
	hold, uncertain, restart bool
}

func (a memoryHarness) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: a.id, Name: "Memory " + a.id}
}
func (a memoryHarness) Start(ctx context.Context, options harness.StartOptions) (harness.Runtime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &memoryRuntime{adapter: a, options: options, done: make(chan struct{})}, nil
}

type memoryRuntime struct {
	adapter memoryHarness
	options harness.StartOptions
	done    chan struct{}
	once    sync.Once
}

func (r *memoryRuntime) Readiness() domain.Readiness {
	return domain.Readiness{State: "ready", HarnessID: r.adapter.id, Harnesses: []domain.HarnessInfo{{ID: r.adapter.id, Name: "Memory " + r.adapter.id, State: "ready", Models: []domain.Model{{ID: r.adapter.id + "-model", Default: true}}, Permissions: []string{"limited"}, Capabilities: domain.HarnessCapabilities{Interrupt: true, Recovery: !r.adapter.uncertain, Restart: r.adapter.restart}}}, Models: []domain.Model{{ID: r.adapter.id + "-model", Default: true}}, Permissions: []string{"limited"}, Capabilities: domain.HarnessCapabilities{Interrupt: true, Recovery: !r.adapter.uncertain, Restart: r.adapter.restart}}
}
func (r *memoryRuntime) Validate(c domain.RunConfig) error {
	if c.Model != r.adapter.id+"-model" || c.Permission != "limited" || c.Fast || c.Plan || c.Effort != "" {
		return errors.New("unsupported memory configuration")
	}
	return nil
}
func (r *memoryRuntime) Create(_ context.Context, s domain.Session) (string, error) {
	return "opaque/" + r.adapter.id + "/" + s.ID, nil
}
func (r *memoryRuntime) Resume(context.Context, domain.Session) error { return nil }
func (r *memoryRuntime) Submit(_ context.Context, s domain.Session, op string, input harness.TurnInput) (string, error) {
	if r.adapter.uncertain {
		return "", &harness.DeliveryError{Uncertain: true, Message: "delivery unconfirmed"}
	}
	turn := "turn/" + op
	if err := r.options.OnEvent(domain.Event{SessionID: s.ID, Type: "turn", Turn: &domain.Turn{ID: turn, Status: "running"}}); err != nil {
		return "", err
	}
	if !r.adapter.hold {
		if err := r.options.OnEvent(domain.Event{SessionID: s.ID, Type: "item.upsert", Item: &domain.Item{ID: "reply/" + op, TurnID: turn, Kind: "assistant", Text: "from " + r.adapter.id + ": " + input.Text, Final: true}}); err != nil {
			return "", err
		}
		if err := r.options.OnEvent(domain.Event{SessionID: s.ID, Type: "turn", Turn: &domain.Turn{ID: turn, Status: "completed"}}); err != nil {
			return "", err
		}
	}
	return turn, nil
}
func (r *memoryRuntime) Steer(context.Context, domain.Session, string, string, harness.TurnInput) error {
	return harness.Unsupported("steer")
}
func (r *memoryRuntime) Respond(domain.Answer) error                             { return harness.Unsupported("answer") }
func (r *memoryRuntime) Interrupt(context.Context, domain.Session, string) error { return nil }
func (r *memoryRuntime) Inspect(context.Context, domain.Session) (domain.RecoveryHistory, error) {
	return domain.RecoveryHistory{Available: true, State: domain.SourceIdle, Inputs: map[string]string{}}, nil
}
func (r *memoryRuntime) Observes(domain.Session) bool { return !r.Stopped() }
func (r *memoryRuntime) PrepareHistory(_ context.Context, s domain.Session, b harness.HistoryBoundary) (string, error) {
	if !r.adapter.restart || !b.Restart {
		return "", harness.Unsupported("history")
	}
	// A revision-capable driver can keep its own opaque source reference without
	// exposing or implementing a fork operation for same-session restart.
	return s.ThreadID, nil
}
func (r *memoryRuntime) ActivateHistory(_ context.Context, _ domain.Session, _ string, commit func() error) error {
	return commit()
}
func (r *memoryRuntime) ExecutionReady() bool  { return !r.Stopped() }
func (r *memoryRuntime) ManagedCoverage() bool { return false }
func (r *memoryRuntime) Done() <-chan struct{} { return r.done }
func (r *memoryRuntime) Stopped() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}
func (r *memoryRuntime) Close(context.Context) error { r.once.Do(func() { close(r.done) }); return nil }

func memoryWorker(t *testing.T, adapters ...harness.Adapter) (*Server, domain.Workspace) {
	t.Helper()
	dir, err := os.MkdirTemp("", "sh-harness-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: adapters})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	// Pressure-specific tests inject their own verdict; unrelated fixtures must
	// not depend on the developer Mac's current kernel pressure.
	s.dispatchMu.Lock()
	s.pressureSample = func() uint32 { return 1 }
	s.dispatchMu.Unlock()
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.store.CreateWorkspace("test", []string{folder})
	if err != nil {
		t.Fatal(err)
	}
	return s, w
}
func awaitReceipt(t *testing.T, s *Server, id string) domain.Operation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		op, err := s.store.Operation(id)
		if err == nil && op.State.ReceiptObserved() {
			return op
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation never settled", id)
	return domain.Operation{}
}
func TestHarnessRoutingKeepsSessionIdentityAndCatalogsIndependent(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"}, memoryHarness{id: "beta"})
	for _, id := range []string{"alpha", "beta"} {
		in := domain.Submission{HarnessID: id, OperationID: id, WorkspaceID: w.ID, Text: "hello", Config: domain.RunConfig{Model: id + "-model", Permission: "limited"}}
		op, err := s.submit(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := awaitReceipt(t, s, in.OperationID); got.State != "confirmed" {
			t.Fatal(got)
		}
		d, err := s.store.Session(op.SessionID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		if d.Session.HarnessID != id || len(d.Items) != 2 || d.Items[1].Text != "from "+id+": hello" {
			t.Fatalf("wrong adapter or lost normalized output: %+v", d)
		}
		in.OperationID = "changed-" + id
		in.SessionID = op.SessionID
		in.HarnessID = map[string]string{"alpha": "beta", "beta": "alpha"}[id]
		if _, err = s.submit(in); err == nil {
			t.Fatal("existing session accepted a different harness")
		}
		if _, err = s.store.Operation(in.OperationID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("rejected harness change persisted an operation", err)
		}
	}
	ready := s.runtimeReadiness()
	if len(ready.Harnesses) != 2 || ready.Harnesses[0].Models[0].ID != "alpha-model" || ready.Harnesses[1].Models[0].ID != "beta-model" {
		t.Fatalf("catalogs were merged or lost: %+v", ready)
	}
}
func TestUnknownHarnessNeverFallsBackToDefault(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"})
	_, err := s.submit(domain.Submission{HarnessID: "missing", OperationID: "unknown", WorkspaceID: w.ID, Text: "do not send", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}})
	if err == nil {
		t.Fatal("unknown harness silently used default")
	}
	snapshot, _ := s.store.Snapshot()
	if len(snapshot.Sessions) != 0 || len(snapshot.Operations) != 0 {
		t.Fatal("unavailable selection created work", snapshot)
	}
}
func TestUnsupportedHarnessOperationIsRejectedBeforeAcceptance(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha", hold: true})
	in := domain.Submission{HarnessID: "alpha", OperationID: "initial", WorkspaceID: w.ID, Text: "hold", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}}
	op, err := s.submit(in)
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, in.OperationID)
	in.SessionID, in.OperationID, in.Text = op.SessionID, "steer", "guidance"
	_, err = s.steer(protocol.SteerInput{Input: in, TurnID: "turn/initial"})
	if err == nil {
		t.Fatal("unsupported steering accepted")
	}
	if _, err = s.store.Operation("steer"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unsupported action persisted", err)
	}
}
func TestHarnessDeliveryUncertaintyPreservesQueuedDrafts(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha", uncertain: true})
	in := domain.Submission{HarnessID: "alpha", OperationID: "uncertain", WorkspaceID: w.ID, Text: "retain", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}}
	op, err := s.submit(in)
	if err != nil {
		t.Fatal(err)
	}
	receipt := awaitReceipt(t, s, in.OperationID)
	if receipt.State != "indeterminate" {
		t.Fatalf("uncertain send became %s", receipt.State)
	}
	d, err := s.store.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if d.Session.Status != "unknown" || d.Items[0].Text != "retain" || !d.Queue.Paused {
		t.Fatal(fmt.Sprintf("uncertain send lost input or safety state: %+v", d))
	}
}

func TestHarnessEventsCannotCrossSessionOrChangeWorkerOwnedState(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"}, memoryHarness{id: "beta"})
	var sessions []string
	for _, id := range []string{"alpha", "beta"} {
		op, err := s.submit(domain.Submission{HarnessID: id, OperationID: id, WorkspaceID: w.ID, Text: "hello", Config: domain.RunConfig{Model: id + "-model", Permission: "limited"}})
		if err != nil {
			t.Fatal(err)
		}
		awaitReceipt(t, s, id)
		sessions = append(sessions, op.SessionID)
	}
	beta := s.currentRuntime(sessions[1]).(*memoryRuntime)
	if err := beta.options.OnEvent(domain.Event{SessionID: sessions[0], Type: "item.upsert", Item: &domain.Item{ID: "foreign", Kind: "assistant", Text: "wrong owner"}}); err == nil {
		t.Fatal("foreign adapter changed another session")
	}
	before, _ := s.store.Session(sessions[1], 0, 100)
	changed := before.Session
	changed.HarnessID = "alpha"
	if err := beta.options.OnEvent(domain.Event{SessionID: sessions[1], Type: "session", Session: &changed}); err == nil {
		t.Fatal("adapter changed worker-owned launch identity")
	}
	after, _ := s.store.Session(sessions[1], 0, 100)
	if after.Session.HarnessID != "beta" {
		t.Fatal("adapter replaced accepted harness identity")
	}
}

type stalledHarness struct{ memoryHarness }

func (a stalledHarness) Start(ctx context.Context, _ harness.StartOptions) (harness.Runtime, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestCatalogStartupTimeoutDoesNotDisableAnotherHarness(t *testing.T) {
	s, _ := memoryWorker(t, stalledHarness{memoryHarness{id: "stalled"}}, memoryHarness{id: "healthy"})
	ready, ok := s.runtimeReadiness().ForHarness("healthy")
	if !ok || ready.State != "ready" {
		t.Fatal("healthy harness inherited expired startup budget", ready)
	}
}

type nonComparableHarness struct{ memoryHarness }
type nonComparableRuntime struct {
	*memoryRuntime
	metadata []string
}

func (a nonComparableHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, e := a.memoryHarness.Start(ctx, o)
	if e != nil {
		return nil, e
	}
	return nonComparableRuntime{r.(*memoryRuntime), []string{"valid value runtime"}}, nil
}
func TestRuntimeIdentityDoesNotRequireComparableAdapterValues(t *testing.T) {
	s, w := memoryWorker(t, nonComparableHarness{memoryHarness{id: "alpha"}})
	in := domain.Submission{OperationID: "value", WorkspaceID: w.ID, Text: "work", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}}
	_, e := s.submit(in)
	if e != nil {
		t.Fatal(e)
	}
	if op := awaitReceipt(t, s, "value"); op.State != "confirmed" {
		t.Fatal(op)
	}
}
func TestCatalogOwnershipDirectoryStaysBoundAcrossDefaultChanges(t *testing.T) {
	dir := t.TempDir()
	s := &Server{config: Config{Directory: dir, Harnesses: []harness.Adapter{memoryHarness{id: "alpha"}, memoryHarness{id: "beta"}}}}
	if err := s.configureAdapters(); err != nil {
		t.Fatal(err)
	}
	alpha, beta := s.catalogDirectory("alpha"), s.catalogDirectory("beta")
	next := &Server{config: Config{Directory: dir, DefaultHarnessID: "beta", Harnesses: s.config.Harnesses}}
	if err := next.configureAdapters(); err != nil {
		t.Fatal(err)
	}
	if next.catalogDirectory("alpha") != alpha || next.catalogDirectory("beta") != beta {
		t.Fatal("default selection reassigned process ownership")
	}
}

func TestRestartOnlyHarnessDoesNotNeedToImplementFork(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha", restart: true})
	cfg := domain.RunConfig{Model: "alpha-model", Permission: "limited"}
	op, e := s.submit(domain.Submission{OperationID: "original", WorkspaceID: w.ID, Text: "old", Config: cfg})
	if e != nil {
		t.Fatal(e)
	}
	awaitReceipt(t, s, "original")
	// A receipt acknowledges delivery before dispatch finishes updating the
	// input's turn/status metadata. Wait for that existing critical section
	// before choosing the revision for this separate history operation.
	s.dispatchMu.Lock()
	d, e := s.store.Session(op.SessionID, 0, 100)
	s.dispatchMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if len(d.Items) == 0 || d.Items[0].Status != "sent" || d.Items[0].TurnID == "" {
		t.Fatal("history fixture read unfinished dispatch metadata", d.Items)
	}
	result, e := s.changeHistory(domain.HistoryChange{OperationID: "restart", SessionID: op.SessionID, Revision: d.Session.Revision, ItemID: "input:original", Restart: true, Text: "new", Config: cfg})
	if e != nil {
		t.Fatal(e)
	}
	if result.Session.ID != op.SessionID || result.Session.ThreadID != d.Session.ThreadID {
		t.Fatal("restart changed logical identity", result)
	}
	if got := awaitReceipt(t, s, "restart"); got.State != "confirmed" {
		t.Fatal(got)
	}
}

func savedUnknownSession(t *testing.T, s *Server, w domain.Workspace) domain.Session {
	t.Helper()
	op, e := s.store.Accept(domain.Submission{HarnessID: "alpha", OperationID: "recover", WorkspaceID: w.ID, Text: "saved", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}})
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.store.Session(op.SessionID, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	d.Session.Status = "unknown"
	d.Session.ThreadID = "ref"
	if _, e = s.store.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session}); e != nil {
		t.Fatal(e)
	}
	return d.Session
}
func TestSessionRecoveryFailurePreservesOtherHarnessCatalogs(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha"}, memoryHarness{id: "beta"})
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	session := savedUnknownSession(t, s, w)
	dir := s.sessionRuntimeDirectory(session.ID)
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "runtime-process.json"), []byte("invalid"), 0600); e != nil {
		t.Fatal(e)
	}
	result, e := s.reconcileLocked(context.Background(), protocol.RecoveryInput{SessionID: session.ID})
	if e != nil {
		t.Fatal(e)
	}
	ready, ok := result.Readiness.ForHarness("beta")
	if !ok || ready.State != "ready" {
		t.Fatal("session recovery erased healthy catalogs", result)
	}
}

type noInterruptHarness struct{ memoryHarness }
type noInterruptRuntime struct{ *memoryRuntime }

func (a noInterruptHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, e := a.memoryHarness.Start(ctx, o)
	if e != nil {
		return nil, e
	}
	return &noInterruptRuntime{r.(*memoryRuntime)}, nil
}
func (r *noInterruptRuntime) Readiness() domain.Readiness {
	ready := r.memoryRuntime.Readiness()
	ready.Capabilities.Interrupt = false
	return ready
}
func (r *noInterruptRuntime) Inspect(context.Context, domain.Session) (domain.RecoveryHistory, error) {
	return domain.RecoveryHistory{Available: true, State: domain.SourceActive, Turns: []domain.Turn{{ID: "live", Status: "running"}}}, nil
}
func (r *noInterruptRuntime) Interrupt(context.Context, domain.Session, string) error {
	return errors.New("unsupported interrupt was called")
}
func TestRecoveredCatalogChecksInterruptBeforeCallingAdapter(t *testing.T) {
	s, w := memoryWorker(t, noInterruptHarness{memoryHarness{id: "alpha"}})
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	session := savedUnknownSession(t, s, w)
	d, _ := s.store.Session(session.ID, 0, 100)
	s.runtimeMu.Lock()
	old := s.runtime
	s.runtime = nil
	delete(s.catalogs, "alpha")
	s.runtimeMu.Unlock()
	old.Close(context.Background())
	if err := os.Remove(filepath.Join(s.catalogDirectory("alpha"), "runtime-process.json")); err != nil {
		t.Fatal(err)
	}
	_, e := s.reconcileLocked(context.Background(), protocol.RecoveryInput{SessionID: session.ID, Revision: d.Session.Revision, Action: "interrupt"})
	var unsupported *harness.UnsupportedError
	if !errors.As(e, &unsupported) {
		t.Fatal("recovered adapter capability was not checked", e)
	}
}

func TestUnsupportedCapabilityHasNeutralProtocolErrorCode(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "alpha", hold: true})
	in := domain.Submission{OperationID: "run", WorkspaceID: w.ID, Text: "hold", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}}
	op, err := s.submit(in)
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, in.OperationID)
	go s.Serve()
	conn, err := net.Dial("unix", filepath.Join(s.Directory(), "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	in.SessionID = op.SessionID
	in.OperationID = "unsupported"
	err = client.Call(context.Background(), "turn.steer", protocol.SteerInput{Input: in, TurnID: "turn/run"}, nil)
	var remote *protocol.RemoteError
	if !errors.As(err, &remote) || remote.Code != "unsupportedCapability" {
		t.Fatal("capability leaked an unclassified error", err)
	}
}
