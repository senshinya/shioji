package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func reloadCall(t *testing.T, s *Server, method, operation string, session domain.Session) (map[string]json.RawMessage, error) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"operationId": operation, "sessionId": session.ID, "revision": session.Revision})
	out, err := s.handle(method, b)
	if err != nil {
		return nil, err
	}
	b, err = json.Marshal(out)
	var result map[string]json.RawMessage
	if err == nil {
		err = json.Unmarshal(b, &result)
	}
	return result, err
}

func TestSessionReloadRejectsMissingDirectoryBeforeRetirement(t *testing.T) {
	s, w := memoryWorker(t, reloadHarness{})
	d := submitIdleFixture(t, s, w, "original", "")
	previous := s.currentRuntime(d.Session.ID)
	if err := os.Rename(w.Folders[0], w.Folders[0]+"-moved"); err != nil {
		t.Fatal(err)
	}
	if _, err := reloadCall(t, s, "session.reload", "missing-directory", d.Session); err == nil {
		t.Fatal("reload accepted a missing execution directory")
	}
	if previous.Stopped() {
		t.Fatal("invalid directory retired the usable runtime")
	}
}

// Fails if reload only refreshes history, sends a turn, drains a paused queue,
// replaces the thread, or retires a different session's process.
func TestSessionReloadReplacesOnlyItsRuntimeWithoutSending(t *testing.T) {
	s, w := memoryWorker(t, reloadHarness{})
	d := submitIdleFixture(t, s, w, "first", "")
	other := submitIdleFixture(t, s, w, "sibling", "")
	if _, err := s.store.PauseQueue(d.Session.ID, "user paused"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Enqueue(domain.Submission{OperationID: "queued", SessionID: d.Session.ID, WorkspaceID: w.ID, Text: "keep queued", Config: d.Session.Config}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.store.Session(d.Session.ID, 0, 100)
	old, sibling, catalog := s.currentRuntime(d.Session.ID), s.currentRuntime(other.Session.ID), s.currentRuntime()
	out, err := reloadCall(t, s, "session.reload", "reload-once", before.Session)
	if err != nil {
		t.Fatal("reload unavailable:", err)
	}
	var receipt struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(out["operation"], &receipt); err != nil || receipt.State != "completed" {
		t.Fatal("reload not confirmed", receipt, err)
	}
	if !old.Stopped() || s.currentRuntime(d.Session.ID) == old || !s.currentRuntime(d.Session.ID).Observes(d.Session) {
		t.Fatal("reload did not resume on a fresh runtime")
	}
	if sibling.Stopped() || catalog.Stopped() {
		t.Fatal("reload stopped another runtime")
	}
	after, _ := s.store.Session(d.Session.ID, 0, 100)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("reload changed history, queue, title, config or execution context", before, after)
	}
	current := s.currentRuntime(d.Session.ID)
	if _, err := reloadCall(t, s, "session.reload", "reload-once", before.Session); err != nil || s.currentRuntime(d.Session.ID) != current {
		t.Fatal("same receipt restarted the runtime again", err)
	}
	if _, err := reloadCall(t, s, "session.reload.operation", "reload-once", before.Session); err != nil || s.currentRuntime(d.Session.ID) != current {
		t.Fatal("receipt query caused execution", err)
	}
	if _, err := reloadCall(t, s, "session.reload", "reload-once", other.Session); err == nil {
		t.Fatal("operation identity crossed sessions")
	}
}

func TestSessionReloadRejectsBusyStaleAndUncertainExecution(t *testing.T) {
	for _, state := range []domain.ExecutionState{"running", "starting", "unknown", "waiting"} {
		t.Run(string(state), func(t *testing.T) {
			s, w := memoryWorker(t, reloadHarness{})
			d := submitIdleFixture(t, s, w, "initial", "")
			old := s.currentRuntime(d.Session.ID)
			d.Session.Status = state
			if _, err := s.store.Apply(d.Session.ID, domain.Event{Type: "session", Session: &d.Session}); err != nil {
				t.Fatal(err)
			}
			d, _ = s.store.Session(d.Session.ID, 0, 100)
			if _, err := reloadCall(t, s, "session.reload", "blocked", d.Session); err == nil {
				t.Fatal("reloaded active execution")
			}
			if old.Stopped() {
				t.Fatal("rejected reload stopped the runtime")
			}
		})
	}
	s, w := memoryWorker(t, reloadHarness{})
	d := submitIdleFixture(t, s, w, "initial", "")
	old := s.currentRuntime(d.Session.ID)
	stale := d.Session
	stale.Revision--
	if _, err := reloadCall(t, s, "session.reload", "stale", stale); err == nil {
		t.Fatal("accepted stale revision")
	}
	if _, err := s.store.Apply(d.Session.ID, domain.Event{Type: "request.open", Request: &domain.PendingRequest{ID: "question", Kind: "questions", State: "pending"}}); err != nil {
		t.Fatal(err)
	}
	d, _ = s.store.Session(d.Session.ID, 0, 100)
	if _, err := reloadCall(t, s, "session.reload", "question", d.Session); err == nil || old.Stopped() {
		t.Fatal("reload discarded a pending question", err)
	}
}

func TestSessionReloadRejectsContinueOnlyRuntimeBeforeAcceptance(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "continue-only", "")
	old := s.currentRuntime(d.Session.ID)
	if _, err := reloadCall(t, s, "session.reload", "unsupported-reload", d.Session); err == nil {
		t.Fatal("accepted reload on a runtime that only supports history continuation")
	}
	if old.Stopped() || s.currentRuntime(d.Session.ID) != old {
		t.Fatal("unsupported reload retired a healthy runtime")
	}
	after, err := s.store.Session(d.Session.ID, 0, 100)
	if err != nil || !reflect.DeepEqual(d, after) {
		t.Fatal("unsupported reload changed session history or execution state", after, err)
	}
	_, err = s.store.SessionReload(domain.SessionReloadRequest{OperationID: "unsupported-reload", SessionID: d.Session.ID, Revision: d.Session.Revision})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unsupported reload was durably accepted", err)
	}
}

// Reload is independent of continuation; this driver explicitly supports both.
type reloadHarness struct{ idleHarness }

func (reloadHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, err := (idleHarness{}).Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &reloadTestRuntime{Runtime: r}, nil
}

type reloadTestRuntime struct{ harness.Runtime }

func (r *reloadTestRuntime) ManagedCoverage() bool { return true }
func (r *reloadTestRuntime) Readiness() domain.Readiness {
	d := r.Runtime.Readiness()
	d.Capabilities.Reload = true
	d.Harnesses[0].Capabilities.Reload = true
	return d
}

type continueOnlyReloadRuntime struct{ harness.Runtime }

func (r continueOnlyReloadRuntime) Readiness() domain.Readiness {
	d := r.Runtime.Readiness()
	d.Capabilities.Reload = false
	d.Harnesses[0].Capabilities.Reload = false
	return d
}
func TestSessionReloadRejectsOlderSessionRuntimeBeforeAcceptance(t *testing.T) {
	s, w := memoryWorker(t, reloadHarness{})
	d := submitIdleFixture(t, s, w, "old-runtime", "")
	previous := s.currentRuntime(d.Session.ID)
	old := &continueOnlyReloadRuntime{Runtime: previous}
	s.runtimeMu.Lock()
	s.runtimes[d.Session.ID] = old
	s.runtimeMu.Unlock()
	if _, err := reloadCall(t, s, "session.reload", "old-unload", d.Session); err == nil {
		t.Fatal("catalog capability substituted for actual old runtime support")
	}
	after, err := s.store.Session(d.Session.ID, 0, 100)
	if previous.Stopped() || err != nil || !reflect.DeepEqual(d, after) {
		t.Fatal("unsupported old runtime changed", after, err)
	}
	_, err = s.store.SessionReload(domain.SessionReloadRequest{OperationID: "old-unload", SessionID: d.Session.ID, Revision: d.Session.Revision})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unsupported old runtime was accepted", err)
	}
}
