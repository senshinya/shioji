package worker

import (
	"context"
	"encoding/json"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
	"testing"
)

func TestBackgroundHandoffCancellationIsBoundToPreparedGeneration(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "idle", "")
	scope, err := s.executions.Existing(d.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.build.Managed = true
	s.config.ServiceInstance = "captured"
	s.config.DisableBackground = func(context.Context, string) (domain.BackgroundService, error) {
		return domain.BackgroundService{Format: 1, State: "disabling"}, nil
	}
	result, err := s.disableBackground("stop-first")
	if err != nil || result.State != "disabling" || scope.Admitting() {
		t.Fatal(result, err)
	}
	control := func(action, id string) (any, error) {
		body, _ := json.Marshal(supervisor.Control{Instance: "captured", OperationID: id})
		return s.serviceControl(action, body)
	}
	if _, err = control("worker.local-background-cancel", "stop-other"); err == nil || scope.Admitting() {
		t.Fatal("foreign cancellation reopened prepared scope", err)
	}
	ready, err := control("worker.local-background-ready", "stop-first")
	if err != nil || !ready.(domain.WorkerUpdateResult).Ready {
		t.Fatal(ready, err)
	}
	if _, err = control("worker.local-background-cancel", "stop-first"); err != nil || !scope.Admitting() {
		t.Fatal("exact cancellation failed", err)
	}
	if _, err = s.submit(domain.Submission{OperationID: "continued", WorkspaceID: w.ID, Text: "continue", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}}); err != nil {
		t.Fatal(err)
	}
}

func TestBackgroundHandoffLateCancellationCannotReopenDrain(t *testing.T) {
	s, _ := memoryWorker(t, idleHarness{})
	s.build.Managed = true
	s.config.ServiceInstance = "captured"
	s.config.DisableBackground = func(context.Context, string) (domain.BackgroundService, error) {
		return domain.BackgroundService{Format: 1, State: "disabling"}, nil
	}
	if _, err := s.disableBackground("stop-first"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DrainService(supervisor.Control{Instance: "captured", OperationID: "host-stop", Replacement: domain.WorkerReplacement{Policy: "immediate", Explicit: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.cancelBackgroundStop("stop-first"); err == nil || !s.backgroundStopping.Load() || !s.updating.Load() {
		t.Fatal("late cancellation reopened draining worker", err)
	}
}

func TestBackgroundLegacyRPCRejectsBeforeSideEffects(t *testing.T) {
	s, _ := memoryWorker(t, idleHarness{})
	calls := 0
	s.config.DisableBackground = func(context.Context, string) (domain.BackgroundService, error) {
		calls++
		return domain.BackgroundService{}, errors.New("must not run")
	}
	if _, err := s.handle("worker.background.disable", []byte("null")); err == nil || calls != 0 {
		t.Fatal("legacy synchronous unregister executed", err, calls)
	}
	body, _ := json.Marshal(protocol.BackgroundStopInput{})
	if _, err := s.handle("worker.background.stop", body); err == nil || calls != 0 {
		t.Fatal("missing stable ID executed", err, calls)
	}
}
