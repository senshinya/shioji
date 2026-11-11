package domain

import "time"

// SourceRef and ResumeRef belong to the adapter. Shared code only stores and
// compares them; directory and identity validation stays on the owning device.
type ConversationCandidate struct {
	SourceRef         string    `json:"sourceRef"`
	HarnessID         string    `json:"harnessId"`
	Title             string    `json:"title"`
	Model             string    `json:"model,omitempty"`
	Directory         string    `json:"directory"`
	UpdatedAt         time.Time `json:"updatedAt"`
	Active            bool      `json:"active,omitempty"`
	ImportedSessionID string    `json:"importedSessionId,omitempty"`
}
type ConversationCatalog struct {
	Items  []ConversationCandidate `json:"items"`
	Cursor string                  `json:"cursor,omitempty"`
}
type ConversationPreview struct {
	Candidate ConversationCandidate `json:"candidate"`
	ResumeRef string                `json:"resumeRef,omitempty"`
	Reason    string                `json:"reason,omitempty"`
	Truncated bool                  `json:"truncated,omitempty"`
	Items     []Item                `json:"items"`
	Turns     []Turn                `json:"turns"`
}
type ConversationOrigin struct {
	SourceRef string `json:"sourceRef"`
	ResumeRef string `json:"resumeRef,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}
