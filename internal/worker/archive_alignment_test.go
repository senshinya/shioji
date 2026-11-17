package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"testing"
	"time"
)

func auditArchiveRecord(t *testing.T, s *Server, d domain.SessionDetail) store.RuntimeRetirement {
	t.Helper()
	gen, e := s.archiveGeneration(d.Session.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true, gen); e != nil {
		t.Fatal(e)
	}
	rows, e := s.store.PendingRuntimeRetirements()
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range rows {
		if row.SessionID == d.Session.ID {
			return row
		}
	}
	t.Fatal("archive retirement missing")
	return store.RuntimeRetirement{}
}
func TestArchiveAuditClosedRuntimeMustNotRemainInIdleCache(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	old := s.currentRuntime(d.Session.ID)
	sweepIdle(t, s, time.Now())
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	row := auditArchiveRecord(t, s, d)
	if e := s.cleanRuntimeRetirement(row); e != nil {
		t.Fatal(e)
	}
	if !old.Stopped() {
		t.Fatal("fixture runtime did not stop")
	}
	if observation, ok := s.idleRuntimes[d.Session.ID]; ok && observation.runtime != nil {
		t.Fatal("archived runtime was removed from live map but remains strongly owned by idleRuntimes")
	}
}
func TestArchiveAuditStaleRetirementMustNotAbandonRestoredInput(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	captured := auditArchiveRecord(t, s, d)
	if e := s.cleanRuntimeRetirement(captured); e != nil {
		t.Fatal(e)
	}
	d, e := s.store.Session(d.Session.ID, 0, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, false); e != nil {
		t.Fatal(e)
	}
	next, e := s.submitLocked(domain.Submission{OperationID: "restored-input", SessionID: d.Session.ID, WorkspaceID: w.ID, Text: "keep", Config: d.Session.Config}, 0, false)
	if e != nil {
		t.Fatal(e)
	}
	// A second collector selected this row before waiting for the first collector.
	if e = s.cleanRuntimeRetirement(captured); e != nil {
		t.Fatal(e)
	}
	receipt, e := s.store.Operation(next.ID)
	if e != nil {
		t.Fatal(e)
	}
	if receipt.State != "accepted" {
		t.Fatalf("already-completed retirement mutated successor reservation: state=%s", receipt.State)
	}
}
func TestArchiveAuditDeletionMustNotLeaveRetirementPoisoningSweep(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	s.dispatchMu.Lock()
	_ = auditArchiveRecord(t, s, d)
	latest, _ := s.store.Session(d.Session.ID, 0, 1)
	_, e := s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: d.Session.ID, Revision: latest.Session.Revision, CloseRuntime: true, Force: true, OperationID: "force-audit"})
	s.dispatchMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if e = s.cleanupArchives(time.Now()); e != nil {
		t.Fatalf("deleted session's retirement prevents the rest of the sweep: %v", e)
	}
	rows, e := s.store.PendingRuntimeRetirements()
	if e != nil || len(rows) != 0 {
		t.Fatal("deleted session retained permanent retirement", rows, e)
	}
}
func TestArchiveAuditPausedQueueMustNotRequireExtraForcedDeletion(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	if _, e := s.store.PauseQueue(d.Session.ID, "paused"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.store.Enqueue(domain.Submission{OperationID: "queued", SessionID: d.Session.ID, WorkspaceID: w.ID, Text: "queued", Config: d.Session.Config}); e != nil {
		t.Fatal(e)
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	d, _ = s.store.Session(d.Session.ID, 0, 1)
	row := auditArchiveRecord(t, s, d)
	if e := s.cleanRuntimeRetirement(row); e != nil {
		t.Fatal(e)
	}
	d, _ = s.store.Session(d.Session.ID, 0, 1)
	_, e := s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: d.Session.ID, Revision: d.Session.Revision})
	if e != nil {
		t.Fatalf("ordinary permanent deletion is blocked by preserved paused input: %v", e)
	}
}
func TestArchiveAuditGarbageCloseMustNotBlockOtherSessions(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	r := s.currentRuntime(d.Session.ID).(*idleTestRuntime)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	r.closing, r.release = entered, release
	s.dispatchMu.Lock()
	_ = auditArchiveRecord(t, s, d)
	scope, fenceErr := s.executions.Existing(d.Session.ID)
	if fenceErr != nil || scope == nil {
		s.dispatchMu.Unlock()
		t.Fatal(fenceErr)
	}
	if fenceErr = scope.Fence(); fenceErr != nil {
		s.dispatchMu.Unlock()
		t.Fatal(fenceErr)
	}
	_, e := s.store.ForceDeleteArchivedSession(d.Session.ID)
	if gate := s.runtimeEvents[d.Session.ID]; gate != nil {
		gate.Store(false)
	}
	s.dispatchMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	cleaned := make(chan error, 1)
	go func() {
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		cleaned <- s.cleanArchiveGarbageItem(store.ArchiveGarbage{SessionID: d.Session.ID, WorkspaceID: w.ID})
	}()
	select {
	case <-entered:
	case e := <-cleaned:
		close(release)
		t.Fatal("fixture never entered native Close", e)
	case <-time.After(time.Second):
		close(release)
		t.Fatal("cleanup did not reach Close")
	}
	submitted := make(chan error, 1)
	go func() {
		_, e := s.submit(domain.Submission{OperationID: "other", WorkspaceID: w.ID, Text: "independent", Config: d.Session.Config})
		submitted <- e
	}()
	blocked := false
	select {
	case e := <-submitted:
		if e != nil {
			t.Error(e)
		}
	case <-time.After(250 * time.Millisecond):
		blocked = true
	}
	close(release)
	if e := <-cleaned; e != nil {
		t.Log("cleanup tail", e)
	}
	if blocked {
		<-submitted
		t.Fatal("deleted runtime's Close held device dispatch and blocked unrelated session")
	}
}
func TestArchiveAuditConcurrentWarningMustUseFreshVerdictWithoutAwaitingReclamation(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	r := s.currentRuntime(d.Session.ID).(*idleTestRuntime)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	r.closing, r.release = entered, release
	s.pressureSample = func() uint32 { return 2 }
	send := func(id string, out chan error) {
		_, e := s.submit(domain.Submission{OperationID: id, WorkspaceID: w.ID, Text: id, Config: d.Session.Config})
		out <- e
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go send("first", first)
	<-entered
	go send("second", second)
	blocked := false
	select {
	case e := <-second:
		if e != nil {
			t.Error(e)
		}
	case <-time.After(250 * time.Millisecond):
		blocked = true
	}
	close(release)
	if e := <-first; e != nil {
		t.Error(e)
	}
	if blocked {
		<-second
		t.Fatal("concurrent WARNING turn waited for another reclamation; Lody refreshes verdict without awaiting it")
	}
}
func TestArchiveAuditFailedVictimsMustNotConsumeSuccessfulEvictionLimit(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	var healthy *idleTestRuntime
	for i := 0; i < 4; i++ {
		d := submitIdleFixture(t, s, w, fmt.Sprint("old-", i), "")
		r := s.currentRuntime(d.Session.ID).(*idleTestRuntime)
		if i < 3 {
			bad := &archiveFailClose{idleTestRuntime: r}
			bad.fail.Store(true)
			t.Cleanup(func() { bad.fail.Store(false) })
			s.runtimeMu.Lock()
			s.runtimes[d.Session.ID] = bad
			s.runtimeMu.Unlock()
		} else {
			healthy = r
		}
		s.dispatchMu.Lock()
		if s.idleRuntimes == nil {
			s.idleRuntimes = map[string]idleRuntimeObservation{}
		}
		s.idleRuntimes[d.Session.ID] = idleRuntimeObservation{runtime: s.currentRuntime(d.Session.ID), since: time.Now().Add(time.Duration(i) * time.Minute)}
		s.dispatchMu.Unlock()
	}
	s.pressureSample = func() uint32 {
		if healthy.Stopped() {
			return 1
		}
		return 4
	}
	_, e := s.submit(domain.Submission{OperationID: "pressure-new", WorkspaceID: w.ID, Text: "keep", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
	if e != nil {
		t.Fatalf("three failed victims prevented trying the healthy fourth: %v", e)
	}
}

func TestArchiveAuditPermanentDeletionMustRemoveOwnedRuntimeRecord(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	row := auditArchiveRecord(t, s, d)
	if e := s.cleanRuntimeRetirement(row); e != nil {
		t.Fatal(e)
	}
	d, _ = s.store.Session(d.Session.ID, 0, 1)
	if _, e := s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: d.Session.ID, Revision: d.Session.Revision}); e != nil {
		t.Fatal(e)
	}
	if e := s.cleanArchiveGarbage(); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.sessionRuntimeDirectory(d.Session.ID), "runtime-process.json")
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatalf("deleted session still retains worker-owned runtime identity file: %v", e)
	}
}

func TestArchiveDeletionReceiptDoesNotWaitForNativeClose(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "delete-receipt", "")
	r := s.currentRuntime(d.Session.ID).(*idleTestRuntime)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	r.closing, r.release = entered, release
	archiveOwnedFixture(t, s, d.Session.ID)
	latest, _ := s.store.Session(d.Session.ID, 0, 1)
	done := make(chan error, 1)
	go func() {
		s.dispatchMu.Lock()
		_, e := s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: d.Session.ID, Revision: latest.Session.Revision})
		s.dispatchMu.Unlock()
		done <- e
	}()
	select {
	case e := <-done:
		if e != nil {
			close(release)
			t.Fatal(e)
		}
	case <-time.After(250 * time.Millisecond):
		close(release)
		<-done
		t.Fatal("accepted deletion receipt waited for native shutdown")
	}
	close(release)
	if _, e := s.store.Session(d.Session.ID, 0, 1); e == nil {
		t.Fatal("receipt preceded durable tombstone")
	}
}

func TestArchiveDeletionRetriesProducerAfterRecordRepair(t *testing.T) {
	s, w := memoryWorker(t, archiveShutdownHarness{})
	d := submitIdleFixture(t, s, w, "repair-record", "")
	maintenanceTool(t, s, d.Session.ID)
	scope, e := s.executions.Existing(d.Session.ID)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(scope.Directory, "scope.json")
	original, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	archived, e := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
	if e != nil {
		t.Fatal(e)
	}
	s.dispatchMu.Lock()
	if e = os.WriteFile(path, []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	result, e := s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: d.Session.ID, Revision: archived.Revision})
	if e != nil {
		s.dispatchMu.Unlock()
		t.Fatal(e)
	}
	if result.Warning == "" {
		s.dispatchMu.Unlock()
		t.Fatal("missing unresolved ownership warning")
	}
	if e = os.WriteFile(path, original, 0600); e != nil {
		s.dispatchMu.Unlock()
		t.Fatal(e)
	}
	s.dispatchMu.Unlock()
	if e = s.cleanupArchives(time.Now()); e != nil {
		t.Fatal("cleanup never retries restored ownership", e)
	}
	if _, e = os.Stat(scope.Directory); !os.IsNotExist(e) {
		t.Fatal("repaired producer retained", e)
	}
}
