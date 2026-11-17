package worker

import (
	"context"
	"shioji/internal/domain"
	"testing"
)

func TestBackgroundDisableRefusesActiveExecutionBeforeSystemUnregister(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "other", hold: true})
	called := false
	s.build.Managed = true
	s.config.DisableBackground = func(context.Context, string) (domain.BackgroundService, error) {
		called = true
		return domain.BackgroundService{Format: 1, State: "notRegistered"}, nil
	}
	op, err := s.submit(domain.Submission{OperationID: "active-background", WorkspaceID: w.ID, Text: "keep running", Config: domain.RunConfig{Model: "other-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	result, err := s.disableBackground()
	if err != nil || result.State != "enabled" || result.Message == "" || called {
		t.Fatal("active service unregistered", result, err, called)
	}
	if s.currentRuntime(op.SessionID).Stopped() {
		t.Fatal("active turn stopped")
	}
}
func TestBackgroundDisableFencesNewSubmissionsUntilOwnerExits(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "other"})
	s.build.Managed = true
	s.config.DisableBackground = func(context.Context, string) (domain.BackgroundService, error) {
		return domain.BackgroundService{Format: 1, State: "notRegistered"}, nil
	}
	result, err := s.disableBackground()
	if err != nil || result.State != "notRegistered" {
		t.Fatal(result, err)
	}
	if _, err = s.submit(domain.Submission{OperationID: "late-background", WorkspaceID: w.ID, Text: "late", Config: domain.RunConfig{Model: "other-model", Permission: "limited"}}); err == nil {
		t.Fatal("accepted work after background stop admission")
	}
}

func TestBackgroundDisableKnownUnavailableRestoresAdmission(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "other"})
	s.build.Managed = true
	s.config.DisableBackground = func(context.Context, string) (domain.BackgroundService, error) {
		return domain.BackgroundService{Format: 1, State: "unavailable", Message: "private directory"}, nil
	}
	result, err := s.disableBackground()
	if err != nil || result.State != "unavailable" {
		t.Fatal(result, err)
	}
	if _, err = s.submit(domain.Submission{OperationID: "still-admitting", WorkspaceID: w.ID, Text: "work", Config: domain.RunConfig{Model: "other-model", Permission: "limited"}}); err != nil {
		t.Fatal("known non-operation left worker fenced", err)
	}
}

func TestBackgroundCancellationAcknowledgesExactCompletedOperation(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	s.build.Managed = true
	s.config.DisableBackground = func(context.Context, string) (domain.BackgroundService, error) {
		return domain.BackgroundService{}, nil
	}
	if _, err := s.prepareBackgroundStop("cancel-once"); err != nil {
		t.Fatal(err)
	}
	if err := s.cancelBackgroundStop("cancel-once"); err != nil {
		t.Fatal(err)
	}
	// The first result was lost. The exact retry must confirm the old undo.
	if err := s.cancelBackgroundStop("cancel-once"); err != nil {
		t.Fatal("successful cancellation could not be acknowledged again", err)
	}
	if err := s.cancelBackgroundStop("foreign"); err == nil {
		t.Fatal("unrelated operation acknowledged")
	}
	submitIdleFixture(t, s, w, "after-cancel-ack", "")
	if _, err := s.prepareBackgroundStop("next-stop"); err != nil {
		t.Fatal(err)
	}
	if err := s.cancelBackgroundStop("cancel-once"); err == nil || !s.backgroundStopping.Load() {
		t.Fatal("old undo affected the new stop intent", err)
	}
	s.dispatchMu.Lock()
	s.drainOperation = "accepted-drain"
	s.dispatchMu.Unlock()
	if err := s.cancelBackgroundStop("next-stop"); err == nil || !s.backgroundStopping.Load() {
		t.Fatal("undo reopened a draining worker", err)
	}
}
