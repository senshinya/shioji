package protocol

import "shioji/internal/domain"

type ConversationQuery struct {
	WorkspaceID string `json:"workspaceId"`
	Revision    int64  `json:"revision"`
	HarnessID   string `json:"harnessId"`
	SourceRef   string `json:"sourceRef,omitempty"`
	Directory   string `json:"directory,omitempty"`
	Cursor      string `json:"cursor,omitempty"`
}
type ConversationImportInput struct {
	ConversationQuery
	Config domain.RunConfig `json:"config"`
}

type WorkspaceInput struct {
	Name    string   `json:"name"`
	Folders []string `json:"folders"`
}
type SessionChangeInput struct {
	TitleRevision int64  `json:"titleRevision"`
	SessionID     string `json:"sessionId"`
	Revision      int64  `json:"revision"`
	Title         string `json:"title,omitempty"`
	Archived      bool   `json:"archived"`
}
type WorkspaceArchiveInput struct {
	WorkspaceID string `json:"workspaceId"`
	Revision    int64  `json:"revision"`
	Archived    bool   `json:"archived"`
}
type SessionQuery struct {
	ID    string `json:"id"`
	After int64  `json:"after"`
	Limit int    `json:"limit"`
}
type EventsQuery struct {
	After      int64  `json:"after"`
	Generation string `json:"generation"`
	WaitMS     int    `json:"waitMs,omitempty"`
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

// QueueInput applies a revision-checked mutation to the owning worker's queue.
type QueueInput struct {
	ReferencePositions []domain.ReferencePosition `json:"referencePositions"`
	Skills             []domain.SkillReference    `json:"skills"`
	Attachments        []domain.Attachment        `json:"attachments,omitempty"`
	BeforeID           string                     `json:"beforeId,omitempty"`
	TurnID             string                     `json:"turnId,omitempty"`
	SessionID          string                     `json:"sessionId"`
	ID                 string                     `json:"id,omitempty"`
	Revision           int64                      `json:"revision"`
	Text               string                     `json:"text,omitempty"`
}
type AttachmentPut struct {
	WorkspaceID string            `json:"workspaceId"`
	Attachment  domain.Attachment `json:"attachment"`
	Offset      int64             `json:"offset"`
	Data        []byte            `json:"data"`
}
type AttachmentGet struct {
	WorkspaceID string `json:"workspaceId"`
	ID          string `json:"id"`
	Offset      int64  `json:"offset"`
}
type SteerInput struct {
	Input  domain.Submission `json:"input"`
	TurnID string            `json:"turnId"`
}

// RecoveryInput is revision checked on the session's owning device.
type RecoveryInput struct {
	Targets   []string `json:"targets,omitempty"`
	SessionID string   `json:"sessionId"`
	Revision  int64    `json:"revision"`
	Action    string   `json:"action,omitempty"` // check, end, interrupt, stop-runtime, stop-tasks
}
type RecoveryResult struct {
	Tasks        []domain.RecoveryTask `json:"tasks,omitempty"`
	CanStopTasks bool                  `json:"canStopTasks,omitempty"`
	CanContinue  bool                  `json:"canContinue,omitempty"`
	SessionID    string                `json:"sessionId"`
	Status       string                `json:"status"`
	Message      string                `json:"message"`
	CanEnd       bool                  `json:"canEnd,omitempty"`
	NeedsStop    bool                  `json:"needsStop,omitempty"`
	Readiness    domain.Readiness      `json:"readiness"`
}

type HistoryOperationQuery struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
}

type FileSearchQuery struct {
	GitQuery
	Query string `json:"query"`
}
type FileSnapshotQuery struct {
	GitQuery
	Root string `json:"root"`
	Path string `json:"path"`
}

type SkillQuery struct {
	GitQuery
	HarnessID string `json:"harnessId,omitempty"`
}

type SkillManagementInput struct {
	WorkspaceID string `json:"workspaceId,omitempty"`
	HarnessID   string `json:"harnessId"`
	domain.SkillChange
}

// WorkQuery addresses accepted work inside one owning-worker session.
type WorkQuery struct {
	SessionID string `json:"sessionId"`
	ID        string `json:"id"`
}
