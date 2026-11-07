package domain

// Lifecycle values keep their existing JSON spelling. Observation of a newer
// harness value is preserved; only known states grant an action's eligibility.
type ExecutionState string

const (
	ExecutionIdle        ExecutionState = "idle"
	ExecutionStarting    ExecutionState = "starting"
	ExecutionRunning     ExecutionState = "running"
	ExecutionCompleted   ExecutionState = "completed"
	ExecutionInterrupted ExecutionState = "interrupted"
	ExecutionFailed      ExecutionState = "failed"
	ExecutionUnknown     ExecutionState = "unknown"
	ExecutionAbandoned   ExecutionState = "abandoned"
)

func (s ExecutionState) Active() bool { return s == ExecutionStarting || s == ExecutionRunning }
func (s ExecutionState) CanSubmit() bool {
	switch s {
	case ExecutionIdle, ExecutionCompleted, ExecutionInterrupted, ExecutionFailed, ExecutionAbandoned:
		return true
	}
	return false
}
func (s ExecutionState) BlocksCheckout() bool { return s.Active() || s == ExecutionUnknown }
func (s ExecutionState) NeedsRecovery() bool  { return s == ExecutionUnknown || s == ExecutionAbandoned }

type OperationState string

const (
	OperationAccepted      OperationState = "accepted"
	OperationDispatched    OperationState = "dispatched"
	OperationConfirmed     OperationState = "confirmed"
	OperationRejected      OperationState = "rejected"
	OperationIndeterminate OperationState = "indeterminate"
	OperationAbandoned     OperationState = "abandoned"
)

func (s OperationState) Unsettled() bool {
	return s == OperationAccepted || s == OperationDispatched || s == OperationIndeterminate
}

// ReceiptObserved ends client polling, including an explicit uncertain receipt.
// An uncertain receipt still remains unsettled for execution and checkout.
func (s OperationState) ReceiptObserved() bool {
	switch s {
	case OperationConfirmed, OperationRejected, OperationIndeterminate, OperationAbandoned:
		return true
	}
	return false
}

type RequestState string

const (
	RequestPending     RequestState = "pending"
	RequestSubmitting  RequestState = "submitting"
	RequestAnswered    RequestState = "answered"
	RequestResolved    RequestState = "resolved"
	RequestInvalidated RequestState = "invalidated"
)

func (s RequestState) AwaitingResolution() bool { return s == RequestPending || s == RequestSubmitting }

type QueueItemState string

const (
	QueueItemWaiting     QueueItemState = ""
	QueueItemDispatching QueueItemState = "dispatching"
	QueueItemFailed      QueueItemState = "failed"
	QueueItemUnknown     QueueItemState = "unknown"
)

func (s QueueItemState) Reserved() bool { return s == QueueItemDispatching || s == QueueItemUnknown }

type HistoryPhase string

const (
	HistoryPrepared HistoryPhase = "prepared"
	HistoryForked   HistoryPhase = "forked"
	HistoryComplete HistoryPhase = "complete"
	HistoryFailed   HistoryPhase = "failed"
)

type RecoveryPhase string

const (
	RecoveryChecking  RecoveryPhase = "checking"
	RecoveryAttention RecoveryPhase = "attention"
	RecoveryReady     RecoveryPhase = "ready"
)

type ReadinessState string

const (
	RuntimeReady       ReadinessState = "ready"
	RuntimeUnavailable ReadinessState = "unavailable"
)

// Delivery is one outcome of an external dispatch, with its coupled projections.
// It deliberately does not claim that successful delivery completes execution.
type Delivery struct {
	Operation OperationState
	Input     string
	Execution ExecutionState
}

func DeliveryOutcome(failed, uncertain bool) Delivery {
	if !failed {
		return Delivery{Operation: OperationConfirmed, Input: "sent"}
	}
	if uncertain {
		return Delivery{Operation: OperationIndeterminate, Input: "unknown", Execution: ExecutionUnknown}
	}
	return Delivery{Operation: OperationRejected, Input: "failed", Execution: ExecutionFailed}
}
