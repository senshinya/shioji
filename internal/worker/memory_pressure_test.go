package worker

import (
	"fmt"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"testing"
	"time"
)

func TestMemoryPressureCriticalPreservesUnacceptedInput(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	s.pressureSample = func() uint32 { return 4 }
	_, err := s.submit(domain.Submission{OperationID: "critical", WorkspaceID: w.ID, Text: "keep draft", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
	if err == nil {
		t.Fatal("critical pressure accepted new execution")
	}
	snapshot, e := s.store.Snapshot()
	if e != nil {
		t.Fatal(e)
	}
	if len(snapshot.Sessions) != 0 {
		t.Fatal("pressure refusal created an empty session")
	}
	if _, e = s.store.Operation("critical"); e == nil {
		t.Fatal("unaccepted input was persisted as consumed")
	}
}

func TestMemoryPressureReclaimsAtMostThreeAndProtectsPendingNativeRequests(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	var runtimes []harness.Runtime
	for i := 0; i < 6; i++ {
		d := submitIdleFixture(t, s, w, fmt.Sprint("old-", i), "")
		r := s.currentRuntime(d.Session.ID)
		if i == 0 {
			r = &pendingIdleRuntime{idleTestRuntime: r.(*idleTestRuntime)}
			s.runtimeMu.Lock()
			s.runtimes[d.Session.ID] = r
			s.runtimeMu.Unlock()
		}
		runtimes = append(runtimes, r)
	}
	s.pressureSample = func() uint32 { return 2 }
	submitIdleFixture(t, s, w, "new", "")
	stopped := 0
	for _, r := range runtimes {
		if r.Stopped() {
			stopped++
		}
	}
	if stopped != 3 || runtimes[0].Stopped() {
		t.Fatal("wrong pressure victims", stopped, runtimes[0].Stopped())
	}
}

func TestMemoryPressureSettlesEvictedBackgroundWork(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	_, err := s.store.Apply(d.Session.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "background", Kind: "background", TurnID: d.Turns[0].ID, State: "waiting", OriginRef: d.Session.ThreadID}})
	if err != nil {
		t.Fatal(err)
	}
	s.pressureSample = func() uint32 { return 2 }
	submitIdleFixture(t, s, w, "new", "")
	after, err := s.store.Session(d.Session.ID, 0, 100)
	if err != nil || len(after.Works) != 1 || after.Works[0].State != "stopped" {
		t.Fatal("reclaimed background still appears live", after.Works, err)
	}
}

func TestConcurrentMemoryAdmissionsShareReclamation(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	r := s.currentRuntime(d.Session.ID).(*idleTestRuntime)
	entered, release := make(chan struct{}, 3), make(chan struct{})
	r.closing, r.release = entered, release
	s.pressureSample = func() uint32 { return 2 }
	first, second := make(chan error, 1), make(chan error, 1)
	send := func(id string, result chan error) {
		_, e := s.submit(domain.Submission{OperationID: id, WorkspaceID: w.ID, Text: id, Config: d.Session.Config})
		result <- e
	}
	go send("one", first)
	<-entered
	go send("two", second)
	select {
	case <-second:
		t.Fatal("second admission bypassed in-progress pressure reclamation")
	case <-time.After(80 * time.Millisecond):
	}
	close(release)
	if e := <-first; e != nil {
		t.Fatal(e)
	}
	if e := <-second; e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
		t.Fatal("same pressure reclamation ran twice")
	default:
	}
}
func TestMemoryPressureWarningReclaimsIdleWithoutRefusingTurn(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	old := s.currentRuntime(d.Session.ID)
	s.pressureSample = func() uint32 { return 2 }
	submitIdleFixture(t, s, w, "new", "")
	if !old.Stopped() {
		t.Fatal("warning did not reclaim the eligible idle Agent")
	}
	after, e := s.store.Session(d.Session.ID, 0, 100)
	if e != nil || after.Session.ThreadID != d.Session.ThreadID || after.Session.Status != "completed" {
		t.Fatal("pressure reclamation changed old conversation", after.Session, e)
	}
}
func TestMemoryPressureRechecksAfterReclamation(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "old", "")
	old := s.currentRuntime(d.Session.ID)
	s.pressureSample = func() uint32 {
		if old.Stopped() {
			return 1
		}
		return 4
	}
	submitIdleFixture(t, s, w, "new", "")
	if !old.Stopped() {
		t.Fatal("critical pressure started new work before ordered reclamation")
	}
}
