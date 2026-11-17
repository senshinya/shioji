package worker

import (
	"shioji/internal/domain"
	"shioji/internal/supervisor"
	"testing"
)

func TestCommittedResumeCannotReopenAcceptedDrain(t *testing.T) {
	s, _ := memoryWorker(t, idleHarness{})
	s.config.ServiceInstance, s.config.ServiceOperation = "candidate", "committed"
	if err := supervisor.SaveDrainIntent(s.config.Directory, supervisor.DrainIntent{ID: "committed", Instance: "source", DeviceID: s.store.DeviceID()}); err != nil {
		t.Fatal(err)
	}
	s.updating.Store(true)
	for _, in := range []supervisor.Control{{Instance: "foreign", OperationID: "committed"}, {Instance: "candidate", OperationID: "foreign"}} {
		if err := s.ResumeService(in); err == nil || !s.updating.Load() {
			t.Fatal("foreign resume changed acceptance", in, err)
		}
	}
	valid := supervisor.Control{Instance: "candidate", OperationID: "committed"}
	for range 2 {
		if err := s.ResumeService(valid); err != nil || s.updating.Load() {
			t.Fatal("committed resume is not idempotent", err)
		}
	}
	result, err := s.DrainService(supervisor.Control{Instance: "candidate", OperationID: "later-drain", Replacement: domain.WorkerReplacement{Policy: "when-idle"}})
	if err != nil || !result.Ready {
		t.Fatal("drain not accepted", result, err)
	}
	if err = s.ResumeService(valid); err == nil || !s.updating.Load() {
		t.Fatal("late committed resume undid accepted drain", err)
	}
}
