package domain

import "testing"

func TestExecutionLifecycleGuardsMatchAcceptedBehavior(t *testing.T) {
	for _, raw := range []string{"", "idle", "starting", "running", "completed", "interrupted", "failed", "unknown", "abandoned", "future-state"} {
		s := ExecutionState(raw)
		if s.Active() != (raw == "running" || raw == "starting") {
			t.Fatal(raw, "active")
		}
		if s.CanSubmit() != (raw == "idle" || raw == "completed" || raw == "interrupted" || raw == "failed" || raw == "abandoned") {
			t.Fatal(raw, "submit")
		}
		if s.BlocksCheckout() != (raw == "running" || raw == "starting" || raw == "unknown") {
			t.Fatal(raw, "checkout")
		}
		if s.NeedsRecovery() != (raw == "unknown" || raw == "abandoned") {
			t.Fatal(raw, "recovery")
		}
	}
}

func TestDeliveryOutcomeKeepsExecutionAndInputConsistent(t *testing.T) {
	for _, c := range []struct {
		failed, uncertain bool
		operation         OperationState
		input             string
		execution         ExecutionState
	}{{false, false, OperationConfirmed, "sent", ""}, {false, true, OperationConfirmed, "sent", ""}, {true, false, OperationRejected, "failed", ExecutionFailed}, {true, true, OperationIndeterminate, "unknown", ExecutionUnknown}} {
		got := DeliveryOutcome(c.failed, c.uncertain)
		if got.Operation != c.operation || got.Input != c.input || got.Execution != c.execution {
			t.Fatal(c, got)
		}
	}
}

func TestRequestAndOperationLifecyclesKeepUncertaintyDistinct(t *testing.T) {
	for _, raw := range []string{"pending", "submitting", "answered", "resolved", "invalidated", "future-state", ""} {
		if RequestState(raw).AwaitingResolution() != (raw == "pending" || raw == "submitting") {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{"accepted", "dispatched", "confirmed", "rejected", "indeterminate", "abandoned", "future-state", ""} {
		if OperationState(raw).Unsettled() != (raw == "accepted" || raw == "dispatched" || raw == "indeterminate") {
			t.Fatal(raw, "unsettled")
		}
		if OperationState(raw).ReceiptObserved() != (raw == "confirmed" || raw == "rejected" || raw == "indeterminate" || raw == "abandoned") {
			t.Fatal(raw, "observed")
		}
	}
}
