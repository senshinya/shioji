package domain

// ContextAction is a worker-owned maintenance operation on an existing session.
// Source references are opaque; only the adapter interprets history boundaries.
type ContextAction struct {
	RequestRevision int64          `json:"requestRevision"`
	OperationID     string         `json:"operationId"`
	SourceRef       string         `json:"sourceRef"`
	BeforeTurnID    string         `json:"beforeTurnId,omitempty"`
	TurnID          string         `json:"turnId,omitempty"`
	State           ExecutionState `json:"state"`
	PreviousStatus  ExecutionState `json:"previousStatus"`
	Error           string         `json:"error,omitempty"`
}
type ContextActionRequest struct {
	OperationID string `json:"operationId"`
	SessionID   string `json:"sessionId"`
	Revision    int64  `json:"revision"`
}

// A receipt includes the authoritative session revision so the UI can bridge
// a successful RPC and its later journal delivery without showing old state.
type ContextActionResult struct {
	Operation Operation `json:"operation"`
	Session   Session   `json:"session"`
}
type ContextObservation struct {
	OperationID string
	TurnID      string
	State       ExecutionState
}
