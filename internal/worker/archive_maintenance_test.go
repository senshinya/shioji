package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

// Keep a late provider reply after cancellation to prove that revocation, rather
// than cooperation from native I/O, protects the successor instance.
type lateUnloadRuntime struct{ *blockingUnloadRuntime }

func (r *lateUnloadRuntime) Unload(context.Context, domain.Session) error {
	close(r.entered)
	<-r.release
	return nil
}

// Force retirement must not queue behind native Unload. Restoration and a new
// input must also work while that old native reply is still withheld.
func TestArchivePreemptsNativeMaintenance(t *testing.T) {
	for _, scenario := range []struct {
		name            string
		workspace, late bool
	}{{name: "session"}, {name: "workspace", workspace: true}, {name: "late-reply", late: true}} {
		t.Run(scenario.name, func(t *testing.T) {
			s, w := memoryWorker(t, reloadHarness{})
			d := submitIdleFixture(t, s, w, "before-archive", "")
			entered, release := make(chan struct{}), make(chan struct{})
			old := s.currentRuntime(d.Session.ID)
			s.runtimeMu.Lock()
			blocked := &blockingUnloadRuntime{Runtime: old, entered: entered, release: release}
			var runtime harness.Runtime = blocked
			if scenario.late {
				runtime = &lateUnloadRuntime{blocked}
			}
			s.runtimes[d.Session.ID] = runtime
			s.runtimeMu.Unlock()
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			done := make(chan error, 1)
			request := domain.SessionReloadRequest{OperationID: "held-reload", SessionID: d.Session.ID, Revision: d.Session.Revision}
			go func() { _, err := s.reloadSession(request, false); done <- err }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("reload did not enter native unload")
			}
			archive := func(value bool) {
				t.Helper()
				var method string
				var input any
				if scenario.workspace {
					latest, err := s.store.Workspace(w.ID)
					if err != nil {
						t.Fatal(err)
					}
					method, input = "workspace.archive", protocol.WorkspaceArchiveInput{WorkspaceID: w.ID, Revision: latest.Revision, Archived: value}
				} else {
					latest, err := s.store.Session(d.Session.ID, 0, 1)
					if err != nil {
						t.Fatal(err)
					}
					method, input = "session.archive", protocol.SessionChangeInput{SessionID: d.Session.ID, Revision: latest.Session.Revision, Archived: value}
				}
				body, _ := json.Marshal(input)
				if _, err := s.handle(method, body); err != nil {
					t.Fatal(err)
				}
			}
			archive(true)
			select {
			case <-old.Done():
			case <-time.After(time.Second):
				t.Fatal("archive force retirement waited for native maintenance")
			}
			archive(false)
			accepted := make(chan domain.Operation, 1)
			failed := make(chan error, 1)
			go func() {
				op, err := s.submit(domain.Submission{OperationID: "after-archive", SessionID: d.Session.ID, WorkspaceID: w.ID, Text: "after-archive", Config: d.Session.Config})
				if err != nil {
					failed <- err
				} else {
					accepted <- op
				}
			}()
			select {
			case err := <-failed:
				t.Fatal(err)
			case op := <-accepted:
				if receipt := awaitReceipt(t, s, op.ID); receipt.State != "confirmed" {
					t.Fatal(receipt)
				}
			case <-time.After(time.Second):
				t.Fatal("old native maintenance still holds the restored execution slot")
			}
			// A new reload must not be vetoed by the retired running receipt.
			latest, err := s.store.Session(d.Session.ID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.reloadSession(domain.SessionReloadRequest{OperationID: "successor-reload", SessionID: d.Session.ID, Revision: latest.Session.Revision}, false); err != nil {
				t.Fatal("retired reload still blocks maintenance", err)
			}
			successor := s.currentRuntime(d.Session.ID)
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("old reply did not settle")
			}
			if successor.Stopped() || s.currentRuntime(d.Session.ID) != successor || !successor.Observes(d.Session) {
				t.Fatal("late old reload replaced the successor")
			}
			prior, err := s.store.SessionReload(request)
			if err != nil || prior.State != "failed" {
				t.Fatal("old reload was reported successful", prior, err)
			}
			latest, err = s.store.Session(d.Session.ID, 0, 100)
			if err != nil || latest.Session.ThreadID != d.Session.ThreadID {
				t.Fatal("archive lost or replayed history", latest.Session, len(latest.Items), err)
			}
			counts := map[string]int{}
			for _, item := range latest.Items {
				if item.Kind == "user" {
					counts[item.Text]++
				}
			}
			if counts["before-archive"] != 1 || counts["after-archive"] != 1 {
				t.Fatal("archive lost or replayed input", counts)
			}
		})
	}
}

func setArchiveState(t *testing.T, s *Server, id string, value bool) {
	t.Helper()
	d, err := s.store.Session(id, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(protocol.SessionChangeInput{SessionID: id, Revision: d.Session.Revision, Archived: value})
	if _, err = s.handle("session.archive", body); err != nil {
		t.Fatal(err)
	}
}

func TestStopReleasesMaintenanceSlotBeforeLateReply(t *testing.T) {
	s, id, r := compactWorker(t)
	d, _ := s.store.Session(id, 0, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	r.before = func(domain.Session) { close(entered); <-release }
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := compactCall(s, "session.context.compact", domain.ContextActionRequest{OperationID: "stop-held-compact", SessionID: id, Revision: d.Session.Revision})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("compaction did not start")
	}
	if err := s.interruptTurn(id, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.Done():
	case <-time.After(7 * time.Second):
		t.Fatal("Stop did not retire native maintenance")
	}
	accepted := make(chan error, 1)
	go func() {
		_, err := s.submit(domain.Submission{OperationID: "after-stop-maintenance", SessionID: id, WorkspaceID: d.Session.WorkspaceID, Text: "after stop", Config: d.Session.Config})
		accepted <- err
	}()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("retired maintenance retained Stop's logical slot")
	}
	if op := awaitReceipt(t, s, "after-stop-maintenance"); op.State != "confirmed" {
		t.Fatal(op)
	}
	successor := s.currentRuntime(id)
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("late compaction did not settle")
	}
	if successor.Stopped() || s.currentRuntime(id) != successor {
		t.Fatal("late compaction affected successor")
	}
}

type lateHistoryRuntime struct {
	harness.Runtime
	entered, release chan struct{}
}

func (r *lateHistoryRuntime) PrepareHistory(context.Context, domain.Session, harness.HistoryBoundary) (string, error) {
	close(r.entered)
	<-r.release
	return "late-history-result", nil
}

func TestArchiveAbandonsHistoryPreparationBeforeLateReply(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "history", restart: true})
	op, err := s.submit(domain.Submission{OperationID: "original-history", WorkspaceID: w.ID, Text: "original", Config: domain.RunConfig{Model: "history-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	s.dispatchMu.Lock()
	d, err := s.store.Session(op.SessionID, 0, 100)
	if err != nil {
		s.dispatchMu.Unlock()
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	old := s.currentRuntime(op.SessionID)
	s.runtimeMu.Lock()
	s.runtimes[op.SessionID] = &lateHistoryRuntime{Runtime: old, entered: entered, release: release}
	s.runtimeMu.Unlock()
	s.dispatchMu.Unlock()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := s.changeHistory(domain.HistoryChange{OperationID: "abandoned-history", SessionID: op.SessionID, Revision: d.Session.Revision, ItemID: "input:original-history", Restart: true, Text: "must not replace", Config: d.Session.Config})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("history preparation did not enter")
	}
	setArchiveState(t, s, op.SessionID, true)
	select {
	case <-old.Done():
	case <-time.After(time.Second):
		t.Fatal("history preparation delayed retirement")
	}
	setArchiveState(t, s, op.SessionID, false)
	accepted := make(chan error, 1)
	go func() {
		_, err := s.submit(domain.Submission{OperationID: "after-history-retirement", SessionID: op.SessionID, WorkspaceID: w.ID, Text: "keep original history", Config: d.Session.Config})
		accepted <- err
	}()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("abandoned history still owns restored session")
	}
	if receipt := awaitReceipt(t, s, "after-history-retirement"); receipt.State != "confirmed" {
		t.Fatal(receipt)
	}
	successor := s.currentRuntime(op.SessionID)
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("late history did not settle")
	}
	prior, err := s.store.HistoryOperation("abandoned-history")
	if err != nil || prior.State != "failed" {
		t.Fatal("abandoned history was revived", prior, err)
	}
	latest, err := s.store.Session(op.SessionID, 0, 100)
	if err != nil || latest.Session.ThreadID != d.Session.ThreadID || successor.Stopped() || s.currentRuntime(op.SessionID) != successor {
		t.Fatal("late history changed successor", latest.Session, err)
	}
}

func TestArchiveDeletionPreemptsNativeMaintenance(t *testing.T) {
	s, w := memoryWorker(t, reloadHarness{})
	d := submitIdleFixture(t, s, w, "delete-during-reload", "")
	entered, release := make(chan struct{}), make(chan struct{})
	old := s.currentRuntime(d.Session.ID)
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = &blockingUnloadRuntime{Runtime: old, entered: entered, release: release}
	s.runtimeMu.Unlock()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := s.reloadSession(domain.SessionReloadRequest{OperationID: "deleted-reload", SessionID: d.Session.ID, Revision: d.Session.Revision}, false)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("native unload not entered")
	}
	// Commit archive and delete in the real serialized domain boundary, before
	// the asynchronous archive collector can consume its pending retirement.
	s.dispatchMu.Lock()
	gen, err := s.archiveGeneration(d.Session.ID)
	if err == nil {
		_, err = s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true, gen)
	}
	if err == nil {
		latest, e := s.store.Session(d.Session.ID, 0, 1)
		err = e
		if err == nil {
			_, err = s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: d.Session.ID, Revision: latest.Session.Revision, OperationID: "delete-held-reload"})
		}
	}
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-old.Done():
	case <-time.After(time.Second):
		t.Fatal("deleted runtime waits for native maintenance")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old native reply did not settle")
	}
	if _, err := s.store.Session(d.Session.ID, 0, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("late maintenance recreated deleted session", err)
	}
	if current := s.currentRuntime(d.Session.ID); current != nil && !current.Stopped() {
		t.Fatal("late maintenance recreated deleted runtime")
	}
}
