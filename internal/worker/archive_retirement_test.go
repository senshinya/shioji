package worker

import (
	"context"
	"encoding/json"
	"errors"
	"shioji/internal/domain"
	"sync/atomic"
	"testing"
	"time"
)

func TestArchiveRetiresImmediatelyAndRestoreDoesNotLaunch(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	before := submitIdleFixture(t, s, w, "archive-runtime", "")
	id := before.Session.ID
	r := s.currentRuntime(id)
	raw, _ := json.Marshal(map[string]any{"sessionId": id, "revision": before.Session.Revision, "archived": true})
	out, err := s.handle("session.archive", raw)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("archive did not retire its runtime immediately")
	}
	archived := out.(domain.Session)
	raw, _ = json.Marshal(map[string]any{"sessionId": id, "revision": archived.Revision, "archived": false})
	if _, err = s.handle("session.archive", raw); err != nil {
		t.Fatal(err)
	}
	after, err := s.store.Session(id, 0, 100)
	if err != nil || len(after.Items) != len(before.Items) {
		t.Fatal("archive/restore changed conversation", err)
	}
	if current := s.currentRuntime(id); current != nil && !current.Stopped() {
		t.Fatal("restore relaunched execution")
	}
}

type archiveFailClose struct {
	*idleTestRuntime
	fail atomic.Bool
}

func (r *archiveFailClose) Close(ctx context.Context) error {
	if r.fail.Load() {
		return errors.New("captured archive cleanup failed")
	}
	return r.idleTestRuntime.Close(ctx)
}
func TestArchiveCleanupFailureCannotAbandonRestoredInput(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	old := &archiveFailClose{idleTestRuntime: s.currentRuntime(d.Session.ID).(*idleTestRuntime)}
	old.fail.Store(true)
	t.Cleanup(func() { old.fail.Store(false) })
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = old
	s.runtimeMu.Unlock()
	archive := func(archived bool) {
		t.Helper()
		d, err := s.store.Session(d.Session.ID, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(map[string]any{"sessionId": d.Session.ID, "revision": d.Session.Revision, "archived": archived})
		if _, err = s.handle("session.archive", b); err != nil {
			t.Fatal(err)
		}
	}
	archive(true)
	archive(false)
	next := submitIdleFixture(t, s, w, "restored", d.Session.ID)
	if next.Session.ThreadID != d.Session.ThreadID || s.currentRuntime(d.Session.ID) == old {
		t.Fatal("restored input reused the retired owner", next.Session)
	}
	if op, err := s.store.Operation("restored"); err != nil || op.State != "confirmed" {
		t.Fatal("late archive release abandoned successor", op, err)
	}
	if pending, err := s.store.PendingRuntimeRetirements(); err != nil || len(pending) != 0 {
		t.Fatal("cleanup failure permanently pinned archive acceptance", pending, err)
	}
}

func TestArchiveAcceptsActiveSessionAndPreservesQueue(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "archive-active", hold: true})
	config := domain.RunConfig{Model: "archive-active-model", Permission: "limited"}
	op, err := s.submit(domain.Submission{OperationID: "active", WorkspaceID: w.ID, Text: "active", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	old := s.currentRuntime(op.SessionID)
	if _, err = s.store.PauseQueue(op.SessionID, "fixture queue paused"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.Enqueue(domain.Submission{OperationID: "waiting", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "preserve", Config: config}); err != nil {
		t.Fatal(err)
	}
	d, _ := s.store.Session(op.SessionID, 0, 100)
	raw, _ := json.Marshal(map[string]any{"sessionId": op.SessionID, "revision": d.Session.Revision, "archived": true})
	if _, err = s.handle("session.archive", raw); err != nil {
		t.Fatal("archive waited for execution instead of accepting retirement", err)
	}
	select {
	case <-old.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("archive did not retire active runtime")
	}
	for until := time.Now().Add(time.Second); ; {
		d, _ = s.store.Session(op.SessionID, 0, 100)
		if d.Session.Status == "interrupted" {
			break
		}
		if time.Now().After(until) {
			t.Fatal("archived running turn was not settled", d.Session)
		}
		time.Sleep(time.Millisecond)
	}
	q, _ := s.store.Queue(op.SessionID)
	if !d.Session.Archived || !q.Paused || len(q.Items) != 1 || q.Items[0].Input.Text != "preserve" {
		t.Fatal("archive lost queued input", d.Session, q)
	}
	raw, _ = json.Marshal(map[string]any{"sessionId": op.SessionID, "revision": d.Session.Revision, "archived": false})
	if _, err = s.handle("session.archive", raw); err != nil {
		t.Fatal(err)
	}
	q, _ = s.store.Queue(op.SessionID)
	if !q.Paused || len(q.Items) != 1 {
		t.Fatal("restore resumed the pending queue", q)
	}
	if current := s.currentRuntime(op.SessionID); current != nil && !current.Stopped() {
		t.Fatal("restore launched execution")
	}
}

func TestArchiveSettlesReservationBeforeRuntimeExists(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	s.dispatchMu.Lock()
	op, err := s.store.AcceptForHarness(domain.Submission{OperationID: "reserved-before-launch", WorkspaceID: w.ID, Text: "never send", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}}, "idle", domain.LaunchContext{DeviceID: s.store.DeviceID(), CWD: w.Folders[0]})
	if err != nil {
		s.dispatchMu.Unlock()
		t.Fatal(err)
	}
	release := s.reserveDispatch(op)
	defer release()
	s.dispatchMu.Unlock()
	d, err := s.store.Session(op.SessionID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"sessionId": op.SessionID, "revision": d.Session.Revision, "archived": true})
	if _, err = s.handle("session.archive", b); err != nil {
		t.Fatal(err)
	}
	s.dispatchMu.Lock()
	err = s.cleanRuntimeRetirements(op.SessionID)
	if err == nil {
		_, err = s.sessionRuntime(s.ctx, op.SessionID)
		if err == nil {
			t.Error("archived reservation started an Agent")
		}
	}
	s.dispatchMu.Unlock()
	current, err := s.store.Operation(op.ID)
	if err != nil || current.State != "abandoned" {
		t.Fatal("archive left an unstarted reservation active", current, err)
	}
}

func TestArchiveDuringOldRuntimeCleanupCannotLaunchSuccessor(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	r := s.currentRuntime(d.Session.ID).(*idleTestRuntime)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	r.closing, r.release = entered, release
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = &pendingIdleRuntime{idleTestRuntime: r}
	s.runtimeMu.Unlock()
	done := make(chan error, 1)
	go func() {
		s.dispatchMu.Lock()
		_, err := s.sessionRuntime(s.ctx, d.Session.ID)
		s.dispatchMu.Unlock()
		done <- err
	}()
	<-entered
	s.dispatchMu.Lock()
	latest, err := s.store.Session(d.Session.ID, 0, 1)
	if err == nil {
		var gen string
		gen, err = s.archiveGeneration(d.Session.ID)
		if err == nil {
			_, err = s.store.SetSessionArchived(d.Session.ID, latest.Session.Revision, true, gen)
		}
	}
	s.dispatchMu.Unlock()
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("archive arriving during old cleanup launched a fresh Agent")
	}
}
