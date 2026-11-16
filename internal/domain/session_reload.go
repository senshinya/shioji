package domain

// SessionReloadRequest rebuilds an existing session's runtime without input.
type SessionReloadRequest struct {
	OperationID string `json:"operationId"`
	SessionID   string `json:"sessionId"`
	Revision    int64  `json:"revision"`
}

type SessionReloadOperation struct {
	Input SessionReloadRequest `json:"input"`
	State string               `json:"state"`
	Error string               `json:"error,omitempty"`
}

type SessionReloadResult struct {
	Operation SessionReloadOperation `json:"operation"`
	Session   Session                `json:"session"`
}
