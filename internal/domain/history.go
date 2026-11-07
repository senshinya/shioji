package domain

// HistoryChange pins an explicit, device-owned conversation boundary.
type HistoryChange struct {
	OperationID string    `json:"operationId"`
	SessionID   string    `json:"sessionId"`
	Revision    int64     `json:"revision"`
	TurnID      string    `json:"turnId,omitempty"`
	ItemID      string    `json:"itemId,omitempty"`
	Restart     bool      `json:"restart"`
	Text        string    `json:"text,omitempty"`
	Config      RunConfig `json:"config"`
}

type HistoryOperation struct {
	KeepOrder  int64         `json:"keepOrder"`
	ID         string        `json:"id"`
	Input      HistoryChange `json:"input"`
	Source     Session       `json:"source"`
	Session    Session       `json:"session"`
	TurnID     string        `json:"turnId"`
	ThreadID   string        `json:"threadId,omitempty"`
	State      HistoryPhase  `json:"state"`
	Error      string        `json:"error,omitempty"`
	Submission Submission    `json:"submission"`
}
