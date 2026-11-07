package domain

import "slices"

// HistoryChange pins an explicit, device-owned conversation boundary.
type HistoryChange struct {
	References  *HistoryReferences `json:"references,omitempty"`
	OperationID string             `json:"operationId"`
	SessionID   string             `json:"sessionId"`
	Revision    int64              `json:"revision"`
	TurnID      string             `json:"turnId,omitempty"`
	ItemID      string             `json:"itemId,omitempty"`
	Restart     bool               `json:"restart"`
	Text        string             `json:"text,omitempty"`
	Config      RunConfig          `json:"config"`
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

// A nil References preserves legacy history-edit behavior. An explicit empty
// object removes all references; source IDs retain their owning-device scope.
type HistoryReferences struct {
	Attachments        []Attachment        `json:"attachments,omitempty"`
	Skills             []SkillReference    `json:"skills,omitempty"`
	ReferencePositions []ReferencePosition `json:"referencePositions,omitempty"`
}

func (a HistoryChange) Equal(b HistoryChange) bool {
	left, right := a.References, b.References
	a.References, b.References = nil, nil
	if a != b || (left == nil) != (right == nil) {
		return false
	}
	return left == nil || slices.Equal(left.Attachments, right.Attachments) && slices.Equal(left.Skills, right.Skills) && slices.Equal(left.ReferencePositions, right.ReferencePositions)
}
