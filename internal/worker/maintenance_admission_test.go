package worker

import (
	"context"
	"database/sql"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/supervisor"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type auditCompactResume struct {
	*compactRuntime
	entered, release chan struct{}
}

func (r *auditCompactResume) Resume(ctx context.Context, s domain.Session) error {
	close(r.entered)
	<-r.release
	return r.compactRuntime.Resume(ctx, s)
}

func auditImmediateDrain(t *testing.T, s *Server) {
	t.Helper()
	out, e := s.DrainService(supervisor.Control{Instance: "complete-audit", OperationID: "audit-drain", Replacement: domain.WorkerReplacement{Policy: "immediate", Explicit: true}})
	if e != nil || !out.Ready {
		t.Fatal(out, e)
	}
}
func auditEntered(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("native wait did not enter")
	}
}
func TestCompleteAuditCompactResumeCannotCrossDrain(t *testing.T) {
	s, sid, r := compactWorker(t)
	s.dispatchMu.Lock()
	s.config.ServiceInstance = "complete-audit"
	r.forceResume = true
	held := &auditCompactResume{compactRuntime: r, entered: make(chan struct{}), release: make(chan struct{})}
	s.runtimeMu.Lock()
	s.runtimes[sid] = held
	s.runtimeMu.Unlock()
	d, _ := s.store.Session(sid, 0, 100)
	s.dispatchMu.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { close(held.release) }) }
	defer release()
	done := make(chan error, 1)
	go func() {
		_, e := s.compactContext(domain.ContextActionRequest{OperationID: "audit-compact", SessionID: sid, Revision: d.Session.Revision})
		done <- e
	}()
	auditEntered(t, held.entered)
	auditImmediateDrain(t, s)
	release()
	e := <-done
	op, lookup := s.store.Operation("audit-compact")
	t.Logf("drain accepted; compactReturn=%v operation=%s nativeCalls=%d", e, op.State, r.calls)
	if e == nil || !errors.Is(lookup, sql.ErrNoRows) || r.calls != 0 {
		t.Fatal("compaction accepted/sent after preparation crossed closed admission")
	}
}

type auditStartHarness struct {
	memoryHarness
	held             atomic.Bool
	entered, release chan struct{}
}

func (a *auditStartHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	if a.held.Load() && o.Scope != nil {
		close(a.entered)
		<-a.release
	}
	return a.memoryHarness.Start(ctx, o)
}

type auditColdRuntime struct{ harness.Runtime }

func (*auditColdRuntime) ExecutionReady() bool { return false }
func TestCompleteAuditHistoryStartCannotCommitAcrossDrain(t *testing.T) {
	a := &auditStartHarness{memoryHarness: memoryHarness{id: "alpha", restart: true}, entered: make(chan struct{}), release: make(chan struct{})}
	s, w := memoryWorker(t, a)
	op, e := s.submit(domain.Submission{OperationID: "audit-source", WorkspaceID: w.ID, Text: "original preserved", Config: domain.RunConfig{Model: "alpha-model", Permission: "limited"}})
	if e != nil {
		t.Fatal(e)
	}
	awaitReceipt(t, s, "audit-source")
	s.dispatchMu.Lock()
	s.config.ServiceInstance = "complete-audit"
	d, e := s.store.Session(op.SessionID, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	s.runtimeMu.Lock()
	s.runtimes[op.SessionID] = &auditColdRuntime{Runtime: s.runtimes[op.SessionID]}
	s.runtimeMu.Unlock()
	a.held.Store(true)
	s.dispatchMu.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { close(a.release) }) }
	defer release()
	input := domain.HistoryChange{OperationID: "audit-history", SessionID: op.SessionID, Revision: d.Session.Revision, ItemID: "input:audit-source", Restart: true, Text: "replaced after drain", Config: d.Session.Config}
	done := make(chan error, 1)
	go func() { _, e := s.changeHistory(input); done <- e }()
	auditEntered(t, a.entered)
	auditImmediateDrain(t, s)
	release()
	e = <-done
	hist, _ := s.store.HistoryOperation(input.OperationID)
	current, _ := s.store.Session(op.SessionID, 0, 100)
	accepted, lookup := s.store.Operation(input.OperationID)
	original := false
	for _, i := range current.Items {
		if i.Text == "original preserved" {
			original = true
		}
	}
	t.Logf("drain accepted; historyReturn=%v history=%s input=%s originalPreserved=%v", e, hist.State, accepted.State, original)
	if e == nil || !errors.Is(lookup, sql.ErrNoRows) || !original {
		t.Fatal("history committed/accepted replacement after startup crossed closed admission")
	}
	if current := s.currentRuntime(op.SessionID); current != nil && !current.Stopped() {
		t.Fatal("startup published a reusable runtime after drain closed admission")
	}
}
