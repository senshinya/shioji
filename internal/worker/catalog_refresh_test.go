package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

// The neutral test driver has two installed generations with different models
// and capabilities. Real worker admission, sessions, queues and polling run.
type changingCatalog struct {
	version                        atomic.Int32
	versionChecks                  chan bool
	versionTimeout                 atomic.Bool
	titleStarted, titleRelease     chan struct{}
	refreshStarted, refreshRelease chan struct{}
	keepOld                        bool
	onSessionClose                 func(string)
}

func (a *changingCatalog) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: "changing", Name: "Changing"}
}
func (a *changingCatalog) CatalogVersion() (string, error) {
	timedOut := a.versionTimeout.Load()
	if a.versionChecks != nil {
		select {
		case a.versionChecks <- timedOut:
		default:
		}
	}
	if timedOut {
		return "", context.DeadlineExceeded
	}
	return fmt.Sprint(a.version.Load()), nil
}
func (a *changingCatalog) Start(ctx context.Context, opts harness.StartOptions) (harness.Runtime, error) {
	version := a.version.Load()
	if version == 2 && opts.Scope == nil && a.refreshStarted != nil {
		select {
		case a.refreshStarted <- struct{}{}:
		default:
		}
		select {
		case <-a.refreshRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if version == 3 {
		return nil, errors.New("new installation is incompatible")
	}
	return &changingRuntime{memoryRuntime: memoryRuntime{adapter: memoryHarness{id: "changing", hold: true, restart: true}, options: opts, done: make(chan struct{})}, version: version, source: a}, nil
}

type changingRuntime struct {
	memoryRuntime
	version int32
	source  *changingCatalog
}

func (r *changingRuntime) Close(context.Context) error {
	r.once.Do(func() {
		if r.options.Scope != nil && r.source.onSessionClose != nil {
			r.source.onSessionClose(r.options.Scope.Session)
		}
		close(r.done)
	})
	return nil
}
func (r *changingRuntime) CatalogVersion() (string, error) { return fmt.Sprint(r.version), nil }
func (r *changingRuntime) Readiness() domain.Readiness {
	ready := r.memoryRuntime.Readiness()
	ready.Models = []domain.Model{{ID: fmt.Sprintf("model-%d", r.version), Default: true}}
	if r.source.keepOld && r.version != 1 {
		ready.Models = append(ready.Models, domain.Model{ID: "model-1"})
	}
	ready.Capabilities.Steer = r.version == 1
	ready.Harnesses[0].Models = ready.Models
	ready.Harnesses[0].Capabilities = ready.Capabilities
	return ready
}
func (r *changingRuntime) Validate(config domain.RunConfig) error {
	if (config.Model != fmt.Sprintf("model-%d", r.version) && !(r.source.keepOld && config.Model == "model-1")) || config.Permission != "limited" {
		return errors.New("wrong installed generation")
	}
	return nil
}
func (r *changingRuntime) GenerateTitle(ctx context.Context, in harness.TitleInput) (string, error) {
	if r.source.titleStarted != nil {
		select {
		case r.source.titleStarted <- struct{}{}:
		default:
		}
		select {
		case <-r.source.titleRelease:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if r.Stopped() {
			return "", errors.New("catalog closed during title generation")
		}
	}
	return "Generated title", nil
}
func refreshCatalogByReadiness(t *testing.T, s *Server, wantModel string, wantState domain.ReadinessState) domain.Readiness {
	t.Helper()
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		out, err := s.handle("readiness", nil)
		if err != nil {
			t.Fatal(err)
		}
		ready := out.(domain.Readiness)
		if ready.State == wantState && len(ready.Models) > 0 && ready.Models[0].ID == wantModel {
			return ready
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("catalog did not refresh", s.runtimeReadiness())
	return domain.Readiness{}
}

func TestInstalledCatalogRemainsReadyWhenVersionCheckTimesOut(t *testing.T) {
	a := &changingCatalog{versionChecks: make(chan bool, 4)}
	a.version.Store(1)
	s, _ := memoryWorker(t, a)
	a.versionTimeout.Store(true)
	s.wakeCatalog()
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
	defer cancel()
	// The second timed-out check begins after the first one has published its
	// result. A remote client sees that first result via readiness.
	for timedOut := 0; timedOut < 2; {
		select {
		case failed := <-a.versionChecks:
			if failed {
				timedOut++
			}
			s.wakeCatalog()
		case <-ctx.Done():
			t.Fatal("background version checks did not run")
		}
	}
	out, err := s.handle("readiness", nil)
	if err != nil {
		t.Fatal(err)
	}
	ready := out.(domain.Readiness)
	if ready.State != domain.RuntimeReady || ready.Error != "" || len(ready.Models) != 1 || ready.Models[0].ID != "model-1" {
		t.Fatalf("version check timeout invalidated confirmed catalog: state=%q error=%q models=%v", ready.State, ready.Error, ready.Models)
	}
	a.version.Store(2)
	a.versionTimeout.Store(false)
	refreshCatalogByReadiness(t, s, "model-2", domain.RuntimeReady)
}

func TestInstalledCatalogRefreshKeepsExecutingSessionAndUsesNewModelsForNewSessions(t *testing.T) {
	a := &changingCatalog{}
	a.version.Store(1)
	s, w := memoryWorker(t, a)
	oldCatalog := s.catalogRuntime("changing")
	first, err := s.submit(domain.Submission{OperationID: "old-input", WorkspaceID: w.ID, HarnessID: "changing", Text: "keep working", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, first.ID)
	oldSession := s.currentRuntime(first.SessionID)
	a.version.Store(2)
	refreshCatalogByReadiness(t, s, "model-2", domain.RuntimeReady)
	if oldSession.Stopped() || !oldCatalog.Stopped() {
		t.Fatal("refresh interrupted execution or leaked retired catalog")
	}
	detail, err := s.store.Session(first.SessionID, 0, 100)
	if err != nil || detail.Session.Status != "running" {
		t.Fatal("refresh changed execution state", detail.Session, err)
	}
	query, _ := json.Marshal(map[string]any{"sessionIds": []string{first.SessionID}})
	out, err := s.handle("readiness", query)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var profiles struct{ Sessions map[string]domain.HarnessInfo }
	json.Unmarshal(raw, &profiles)
	profile, ok := profiles.Sessions[first.SessionID]
	if !ok || len(profile.Models) != 1 || profile.Models[0].ID != "model-1" || !profile.Capabilities.Steer {
		t.Fatal("old session inherited new catalog", profiles)
	}
	next, err := s.submit(domain.Submission{OperationID: "new-input", WorkspaceID: w.ID, HarnessID: "changing", Text: "new generation", Config: domain.RunConfig{Model: "model-2", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, next.ID)
	if s.currentRuntime(next.SessionID).Readiness().Models[0].ID != "model-2" {
		t.Fatal("new session did not use new installation")
	}
	// Existing session submissions must continue validating against their own runtime.
	runtime, err := s.inputRuntime(domain.Submission{SessionID: first.SessionID})
	if err != nil || runtime.Validate(domain.RunConfig{Model: "model-1", Permission: "limited"}) != nil {
		t.Fatal("old execution lost its accepted model", err)
	}
}
func TestCatalogRefreshDefersUntilTitleReaderFinishes(t *testing.T) {
	a := &changingCatalog{titleStarted: make(chan struct{}, 1), titleRelease: make(chan struct{})}
	a.version.Store(1)
	s, w := memoryWorker(t, a)
	old := s.catalogRuntime("changing")
	_, err := s.submit(domain.Submission{OperationID: "title-input", WorkspaceID: w.ID, HarnessID: "changing", Text: "work", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.titleStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("title did not start")
	}
	a.version.Store(2)
	for i := 0; i < 6; i++ {
		s.handle("readiness", nil)
		time.Sleep(20 * time.Millisecond)
	}
	if old.Stopped() {
		close(a.titleRelease)
		t.Fatal("catalog closed while borrowed by title task")
	}
	close(a.titleRelease)
	refreshCatalogByReadiness(t, s, "model-2", domain.RuntimeReady)
	if !old.Stopped() {
		t.Fatal("old catalog not retired after title task finished")
	}
}
func TestIncompatibleCatalogUpgradeDoesNotBlockExistingSessionAndRecoversOnNextInstall(t *testing.T) {
	a := &changingCatalog{}
	a.version.Store(1)
	s, w := memoryWorker(t, a)
	first, err := s.submit(domain.Submission{OperationID: "stable-input", WorkspaceID: w.ID, HarnessID: "changing", Text: "work", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, first.ID)
	active := s.currentRuntime(first.SessionID)
	a.version.Store(3)
	ready := refreshCatalogByReadiness(t, s, "model-1", domain.RuntimeUnavailable)
	if ready.Error == "" || active.Stopped() {
		t.Fatal("incompatible update lost clear state or stopped old session", ready)
	}
	if _, err = s.submit(domain.Submission{OperationID: "not-accepted", WorkspaceID: w.ID, Text: "wait", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}}); err == nil {
		t.Fatal("new work accepted using obsolete catalog")
	}
	a.version.Store(4)
	refreshCatalogByReadiness(t, s, "model-4", domain.RuntimeReady)
	snap, _ := s.store.Snapshot()
	if len(snap.Sessions) != 1 || active.Stopped() {
		t.Fatal("retry replayed input or replaced execution", snap.Sessions)
	}
}

func TestIdleSessionContinuesWithNewModelOnNewInstallation(t *testing.T) {
	a := &changingCatalog{}
	a.version.Store(1)
	s, w := memoryWorker(t, a)
	first, err := s.submit(domain.Submission{OperationID: "before-upgrade", WorkspaceID: w.ID, HarnessID: "changing", Text: "first", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, first.ID)
	old := s.currentRuntime(first.SessionID)
	detail, err := s.store.Session(first.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.Apply(first.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: detail.Turns[0].ID, Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	a.version.Store(2)
	refreshCatalogByReadiness(t, s, "model-2", domain.RuntimeReady)
	next, err := s.submit(domain.Submission{OperationID: "after-upgrade", SessionID: first.SessionID, WorkspaceID: w.ID, HarnessID: "changing", Text: "continue with selected replacement", Config: domain.RunConfig{Model: "model-2", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, next.ID)
	current := s.currentRuntime(first.SessionID)
	if current == old || !old.Stopped() || current.Readiness().Models[0].ID != "model-2" {
		t.Fatal("idle session kept obsolete runtime after model change")
	}
	after, _ := s.store.Session(first.SessionID, 0, 100)
	if len(after.Turns) != 2 || len(after.Items) != 2 || after.Session.ID != first.SessionID {
		t.Fatal("runtime migration lost or duplicated history", after.Session, len(after.Turns), len(after.Items))
	}
}

func TestCatalogRefreshTemporarilyDefersQueuedDispatchWithoutPausingOrReplaying(t *testing.T) {
	a := &changingCatalog{refreshStarted: make(chan struct{}, 1), refreshRelease: make(chan struct{}), keepOld: true}
	a.version.Store(1)
	released := false
	defer func() {
		if !released {
			close(a.refreshRelease)
		}
	}()
	s, w := memoryWorker(t, a)
	first, err := s.submit(domain.Submission{OperationID: "queue-base", WorkspaceID: w.ID, Text: "first", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, first.ID)
	queued, err := s.enqueue(domain.Submission{OperationID: "queue-after-refresh", SessionID: first.SessionID, WorkspaceID: w.ID, Text: "next", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := s.store.Session(first.SessionID, 0, 100)
	if _, err = s.store.Apply(first.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: d.Turns[0].ID, Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	a.version.Store(2)
	s.handle("readiness", nil)
	select {
	case <-a.refreshStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh did not start")
	}
	s.wakeQueue()
	time.Sleep(120 * time.Millisecond)
	q, err := s.store.Queue(first.SessionID)
	if err != nil || q.Paused || len(q.Items) != 1 {
		t.Fatal("temporary refresh paused or consumed queue", q, err)
	}
	close(a.refreshRelease)
	released = true
	refreshCatalogByReadiness(t, s, "model-2", domain.RuntimeReady)
	op := awaitReceipt(t, s, queued.ID)
	end := time.Now().Add(2 * time.Second)
	for {
		q, err = s.store.Queue(first.SessionID)
		if err != nil || len(q.Items) == 0 || q.Paused || time.Now().After(end) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || op.State != "confirmed" || len(q.Items) != 0 {
		t.Fatal("queue did not resume after refresh", op, q, err)
	}
	d, _ = s.store.Session(first.SessionID, 0, 100)
	if len(d.Turns) != 2 || len(d.Items) != 2 {
		t.Fatal("refresh duplicated queued input", len(d.Turns), len(d.Items))
	}
}

func TestCatalogRecoveryDoesNotReacquireMaintenanceBehindDispatch(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "other"})
	s.dispatchMu.Lock()
	s.maintenance.Lock()
	s.catalogRuntime("other").Close(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.recoveryRuntime(context.Background(), false, "other")
		s.dispatchMu.Unlock()
		done <- err
	}()
	blocked := false
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(150 * time.Millisecond):
		blocked = true
	}
	s.maintenance.Unlock()
	if blocked {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	if blocked {
		t.Fatal("catalog recovery waits for maintenance while holding dispatch; worker update would deadlock")
	}
}

func TestHistoryRestartAfterUpgradeAcceptsSelectedReplacementModel(t *testing.T) {
	a := &changingCatalog{}
	a.version.Store(1)
	s, w := memoryWorker(t, a)
	first, err := s.submit(domain.Submission{OperationID: "history-before", WorkspaceID: w.ID, Text: "first", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, first.ID)
	d, _ := s.store.Session(first.SessionID, 0, 100)
	if _, err = s.store.Apply(first.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: d.Turns[0].ID, Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	a.version.Store(2)
	refreshCatalogByReadiness(t, s, "model-2", domain.RuntimeReady)
	d, _ = s.store.Session(first.SessionID, 0, 100)
	_, err = s.changeHistory(domain.HistoryChange{OperationID: "history-after", SessionID: first.SessionID, Revision: d.Session.Revision, ItemID: "input:" + first.ID, Restart: true, Text: "revised", Config: domain.RunConfig{Model: "model-2", Permission: "limited"}})
	if err != nil {
		t.Fatal("new model rejected against obsolete history source", err)
	}
	op := awaitReceipt(t, s, "history-after")
	if op.State != "confirmed" {
		t.Fatal("revised input failed", op)
	}
}

func TestStaleModelSubmissionIsRejectedWithActionableMessage(t *testing.T) {
	a := &changingCatalog{}
	a.version.Store(1)
	s, w := memoryWorker(t, a)
	a.version.Store(2)
	refreshCatalogByReadiness(t, s, "model-2", domain.RuntimeReady)
	_, err := s.submit(domain.Submission{OperationID: "obsolete-choice", WorkspaceID: w.ID, Text: "keep draft", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if err == nil || err.Error() != "当前模型已不可用，请更换模型" {
		t.Fatal("model removal lacks actionable message", err)
	}
	snapshot, _ := s.store.Snapshot()
	if len(snapshot.Sessions) != 0 {
		t.Fatal("unavailable model created a conversation")
	}
}

func TestLateLossDuringRetirementCannotStartAnotherGeneration(t *testing.T) {
	a := &changingCatalog{}
	a.version.Store(1)
	s, w := memoryWorker(t, a)
	first, err := s.submit(domain.Submission{OperationID: "late-loss-first", WorkspaceID: w.ID, Text: "first", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, first.ID)
	d, _ := s.store.Session(first.SessionID, 0, 100)
	s.store.Apply(first.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: d.Turns[0].ID, Status: "completed"}})
	old := s.currentRuntime(first.SessionID)
	a.version.Store(2)
	refreshCatalogByReadiness(t, s, "model-2", domain.RuntimeReady)
	// This is a previously entered loss callback completing while Close drains.
	a.onSessionClose = func(id string) {
		if e := s.guardManagedExecution(id); e != nil {
			t.Error(e)
		}
	}
	next, err := s.submit(domain.Submission{OperationID: "late-loss-next", SessionID: first.SessionID, WorkspaceID: w.ID, Text: "next", Config: domain.RunConfig{Model: "model-2", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	op := awaitReceipt(t, s, next.ID)
	if op.State == "confirmed" || s.currentRuntime(first.SessionID) != old {
		t.Fatal("a new generation started after concurrent execution loss", op)
	}
}

func TestRuntimeUpgradeKeepsActiveChildAfterParentCompletes(t *testing.T) {
	a := &changingCatalog{}
	a.version.Store(1)
	s, w := memoryWorker(t, a)
	op, err := s.submit(domain.Submission{OperationID: "child-upgrade", WorkspaceID: w.ID, HarnessID: "changing", Text: "first", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	old := s.currentRuntime(op.SessionID)
	d, _ := s.store.Session(op.SessionID, 0, 100)
	s.store.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: d.Turns[0].ID, Status: "completed"}})
	child := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: d.Turns[0].ID, Title: "thinking", State: "running", OriginRef: d.Session.ThreadID}
	if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &child}); err != nil {
		t.Fatal(err)
	}
	a.version.Store(2)
	refreshCatalogByReadiness(t, s, "model-2", domain.RuntimeReady)
	current, err := s.sessionRuntime(context.Background(), op.SessionID)
	if err != nil || current != old || old.Stopped() {
		t.Fatal("runtime upgrade killed independently running child", err)
	}
}
