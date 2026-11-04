package protocol

import "shioji/internal/domain"

type WorkspaceInput struct {
	Name    string   `json:"name"`
	Folders []string `json:"folders"`
}
type SessionQuery struct {
	ID    string `json:"id"`
	After int64  `json:"after"`
	Limit int    `json:"limit"`
}
type EventsQuery struct {
	After      int64  `json:"after"`
	Generation string `json:"generation"`
}
type EventPage struct {
	Generation string         `json:"generation"`
	Events     []domain.Event `json:"events"`
}
type AnswerInput struct {
	SessionID string        `json:"sessionId"`
	Answer    domain.Answer `json:"answer"`
}
type RemoteError struct {
	Code    string
	Message string
}

func (e *RemoteError) Error() string { return e.Message }

type GitQuery struct {
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId,omitempty"`
}
type CheckoutInput struct {
	GitQuery
	Branch string `json:"branch"`
}
type FileQuery struct {
	GitQuery
	Path string `json:"path"`
}
type SearchQuery struct {
	Query string `json:"query"`
}
