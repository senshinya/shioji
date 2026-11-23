// Package domain contains device-owned data. It has no harness wire or UI dependencies.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

type Workspace struct {
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
	Archived   bool       `json:"archived,omitempty"`
	ID         string     `json:"id"`
	DeviceID   string     `json:"deviceId"`
	Name       string     `json:"name"`
	Folders    []string   `json:"folders"` // Primary first; paths resolved on the owning device.
	Revision   int64      `json:"revision"`
}
type RunConfig struct {
	Model      string `json:"model"`
	Effort     string `json:"effort,omitempty"`
	Permission string `json:"permission"`
	Plan       bool   `json:"plan,omitempty"`
	Fast       bool   `json:"fast,omitempty"`
}
type LaunchContext struct {
	DeviceID              string   `json:"deviceId"`
	CWD                   string   `json:"cwd"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
	Repository            string   `json:"repository,omitempty"`
	Branch                string   `json:"branch,omitempty"`
	Head                  string   `json:"head,omitempty"`
}
type Session struct {
	Goal           *Goal               `json:"goal,omitempty"`
	ArchivedAt     *time.Time          `json:"archivedAt,omitempty"`
	Completion     *ReplyCompletion    `json:"completion,omitempty"`
	Origin         *ConversationOrigin `json:"origin,omitempty"`
	ContextAction  *ContextAction      `json:"contextAction,omitempty"`
	ContextUsage   *ContextUsage       `json:"contextUsage,omitempty"`
	PortableFork   bool                `json:"portableFork,omitempty"`
	ForkedFrom     string              `json:"forkedFrom,omitempty"`
	ForkedTurn     string              `json:"forkedTurn,omitempty"`
	ExecutionGuard string              `json:"executionGuard,omitempty"`
	Recovery       *SessionRecovery    `json:"recovery,omitempty"`
	Archived       bool                `json:"archived,omitempty"`
	TitleOverride  bool                `json:"titleOverride,omitempty"`
	TitleRevision  int64               `json:"titleRevision"`
	SourceTitle    string              `json:"sourceTitle,omitempty"`
	ID             string              `json:"id"`
	WorkspaceID    string              `json:"workspaceId"`
	Title          string              `json:"title"`
	HarnessID      string              `json:"harnessId"`
	ThreadID       string              `json:"threadId,omitempty"` // Opaque source reference.
	Context        LaunchContext       `json:"context"`
	Config         RunConfig           `json:"config"`
	Status         ExecutionState      `json:"status"`
	GitHead        string              `json:"gitHead,omitempty"`
	GitBranch      string              `json:"gitBranch,omitempty"`
	ContextChanged bool                `json:"contextChanged,omitempty"`
	Revision       int64               `json:"revision"`
	Error          string              `json:"error,omitempty"`

	// ContextUsageAfterTurnOrder excludes reports from before a model change.
	ContextUsageAfterTurnOrder int64 `json:"contextUsageAfterTurnOrder,omitempty"`
}

// ReplyCompletion is owned by the device worker. ID fences acknowledgements
// against a newer reply or replacement history independently of source IDs.
type ReplyCompletion struct {
	// AutomaticContinuation suppresses delivery while keeping the reply's read state.
	AutomaticContinuation bool `json:"automaticContinuation,omitempty"`
	// Pending has no publication ID or unread signal until native work settles.
	Pending     bool           `json:"pending,omitempty"`
	Outcome     ExecutionState `json:"outcome,omitempty"`
	ID          string         `json:"id"`
	HistoryRef  string         `json:"historyRef,omitempty"`
	TurnID      string         `json:"turnId"`
	Order       int64          `json:"order"`
	CompletedAt time.Time      `json:"completedAt"`
	Unread      bool           `json:"unread"`
}

type Submission struct {
	QuestionReply      *QuestionReply      `json:"questionReply,omitempty"`
	GoalObjective      string              `json:"goalObjective,omitempty"`
	ReferencePositions []ReferencePosition `json:"referencePositions,omitempty"`
	Skills             []SkillReference    `json:"skills,omitempty"`
	HarnessID          string              `json:"harnessId,omitempty"`
	Continue           bool                `json:"continue,omitempty"`
	Attachments        []Attachment        `json:"attachments,omitempty"`
	OperationID        string              `json:"operationId"`
	WorkspaceID        string              `json:"workspaceId"`
	SessionID          string              `json:"sessionId,omitempty"`
	Text               string              `json:"text"`
	Config             RunConfig           `json:"config"`
	Git                GitChoice           `json:"git,omitempty,omitzero"`
}

// QueueState is device-owned. Queued inputs are not sent conversation items.
type QueueState struct {
	SessionID string      `json:"sessionId"`
	Revision  int64       `json:"revision"`
	Paused    bool        `json:"paused"`
	Reason    string      `json:"reason,omitempty"`
	Items     []QueueItem `json:"items"`
}
type QueueItem struct {
	State QueueItemState `json:"state,omitempty"`
	ID    string         `json:"id"`
	Input Submission     `json:"input"`
}
type Operation struct {
	Deleted     bool                  `json:"deleted,omitempty"`
	Kind        string                `json:"kind,omitempty"`
	ID          string                `json:"id"`
	SessionID   string                `json:"sessionId"`
	State       OperationState        `json:"state"`
	Preparation SubmissionPreparation `json:"preparation,omitempty"`
	Digest      string                `json:"-"`
	Error       string                `json:"error,omitempty"`
}
type Item struct {
	Questions          []Question          `json:"questions,omitempty"`
	ReplyTo            string              `json:"replyTo,omitempty"`
	WorkID             string              `json:"workId,omitempty"` // opaque session-owned source work
	ReferencePositions []ReferencePosition `json:"referencePositions,omitempty"`
	Skills             []SkillReference    `json:"skills,omitempty"`
	SentAt             *time.Time          `json:"sentAt,omitempty"`
	Attachments        []Attachment        `json:"attachments,omitempty"`
	Presentation       string              `json:"presentation,omitempty"`
	Parts              []ContentPart       `json:"parts,omitempty"`
	Files              []FileChange        `json:"files,omitempty"`
	Agents             []AgentActivity     `json:"agents,omitempty"`
	Truncated          bool                `json:"truncated,omitempty"`
	ID                 string              `json:"id"`
	TurnID             string              `json:"turnId,omitempty"`
	Kind               string              `json:"kind"`
	Text               string              `json:"text"`
	Name               string              `json:"name,omitempty"`
	Status             string              `json:"status,omitempty"`
	ExitCode           *int                `json:"exitCode,omitempty"`
	Final              bool                `json:"final"`
	Order              int64               `json:"order"`
	// Worker-owned order of the first observed activity start. Output and
	// completion updates preserve it; display/history position is independent.
	ActivityOrder int64 `json:"activityOrder,omitempty"`
}

type ContentPart struct {
	Attachment *Attachment `json:"attachment,omitempty"`
	Kind       string      `json:"kind"`
	Label      string      `json:"label,omitempty"`
	Text       string      `json:"text"`
}
type FileChange struct {
	Path        string `json:"path"`
	Destination string `json:"destination,omitempty"`
	Kind        string `json:"kind"`
	Patch       string `json:"patch"`
}
type AgentActivity struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}
type Question struct {
	ID      string   `json:"id"`
	Header  string   `json:"header"`
	Text    string   `json:"text"`
	Options []Option `json:"options,omitempty"`
	Custom  bool     `json:"custom"`
	Secret  bool     `json:"secret,omitempty"`
}

// QuestionReply associates a normal input with a nonblocking transcript question.
// The owning device supplies Title and validates the opaque QuestionID.
type QuestionReply struct {
	ItemID     string `json:"itemId"`
	QuestionID string `json:"questionId"`
	Title      string `json:"title,omitempty"`
}
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}
type PendingRequest struct {
	WorkID    string          `json:"workId,omitempty"` // empty for a session-level request
	Choices   []RequestChoice `json:"choices,omitempty"`
	Fields    []FormField     `json:"fields,omitempty"`
	URL       string          `json:"url,omitempty"`
	Notice    string          `json:"notice,omitempty"`
	Receipt   []ContentPart   `json:"receipt,omitempty"`
	ID        string          `json:"id"`
	SessionID string          `json:"sessionId"`
	TurnID    string          `json:"turnId"`
	ItemID    string          `json:"itemId"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Text      string          `json:"text"`
	Questions []Question      `json:"questions,omitempty"`
	State     RequestState    `json:"state"`
	Revision  int64           `json:"revision"`
}
type RequestChoice struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Restriction bool   `json:"restriction,omitempty"`
}
type FormOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
type FormField struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Type        string       `json:"type"`
	Required    bool         `json:"required"`
	Options     []FormOption `json:"options,omitempty"`
	Default     []string     `json:"default,omitempty"`
	Format      string       `json:"format,omitempty"`
	MinLength   *int         `json:"minLength,omitempty"`
	MaxLength   *int         `json:"maxLength,omitempty"`
	Minimum     *float64     `json:"minimum,omitempty"`
	Maximum     *float64     `json:"maximum,omitempty"`
	MinItems    *int         `json:"minItems,omitempty"`
	MaxItems    *int         `json:"maxItems,omitempty"`
}
type Answer struct {
	RequestID string              `json:"requestId"`
	Revision  int64               `json:"revision"`
	Decision  string              `json:"decision,omitempty"`
	Answers   map[string][]string `json:"answers,omitempty"`
}
type Turn struct {
	UsageLimit        *UsageLimit    `json:"usageLimit,omitempty"`
	Presentation      string         `json:"presentation,omitempty"`
	Order             int64          `json:"order"`
	ID                string         `json:"id"`
	Status            ExecutionState `json:"status"`
	StartedAt         *time.Time     `json:"startedAt,omitempty"`
	CompletedAt       *time.Time     `json:"completedAt,omitempty"`
	DurationMS        *int64         `json:"durationMs,omitempty"`
	InputTokens       *int64         `json:"inputTokens,omitempty"`
	OutputTokens      *int64         `json:"outputTokens,omitempty"`
	CachedInputTokens *int64         `json:"cachedInputTokens,omitempty"`
	Error             string         `json:"error,omitempty"`
}

// UsageLimit describes a provider quota failure. A nil reset time means the
// provider did not give a usable estimate.
type UsageLimit struct {
	ResetAt *time.Time `json:"resetAt,omitempty"`
}

type PlanStep struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}
type Plan struct {
	Narrative   string     `json:"narrative,omitempty"`
	Truncated   bool       `json:"truncated,omitempty"`
	TurnID      string     `json:"turnId"`
	Explanation string     `json:"explanation,omitempty"`
	Steps       []PlanStep `json:"steps"`
}
type Event struct {
	GoalProgress     *GoalProgress    `json:"-"`
	Goal             *Goal            `json:"goal,omitempty"`
	GoalSource       string           `json:"goalSource,omitempty"`
	ActivityStart    bool             `json:"activityStart,omitempty"`
	ArchivePolicy    *ArchivePolicy   `json:"archivePolicy,omitempty"`
	Deletion         *ArchiveDeletion `json:"deletion,omitempty"`
	Work             *WorkActivity    `json:"work,omitempty"`
	WorkID           string           `json:"workId,omitempty"`
	WorkItem         *Item            `json:"workItem,omitempty"`
	ContextUsage     *ContextUsage    `json:"contextUsage,omitempty"`
	HistoryTurnOrder *int64           `json:"historyTurnOrder,omitempty"`
	Plan             *Plan            `json:"plan,omitempty"`
	Queue            *QueueState      `json:"queue,omitempty"`
	Sequence         int64            `json:"sequence"`
	SessionID        string           `json:"sessionId,omitempty"`
	Type             string           `json:"type"`
	ItemID           string           `json:"itemId,omitempty"`
	Text             string           `json:"text,omitempty"`
	Workspace        *Workspace       `json:"workspace,omitempty"`
	Session          *Session         `json:"session,omitempty"`
	Operation        *Operation       `json:"operation,omitempty"`
	Item             *Item            `json:"item,omitempty"`
	Request          *PendingRequest  `json:"request,omitempty"`
	Turn             *Turn            `json:"turn,omitempty"`
}
type Snapshot struct {
	SessionInputTimes map[string]time.Time `json:"sessionInputTimes,omitempty"`
	ArchivePolicy     *ArchivePolicy       `json:"archivePolicy,omitempty"`
	ObservedAt        time.Time            `json:"observedAt,omitempty"`
	PendingRequests   []PendingRequest     `json:"pendingRequests,omitempty"`
	DeviceID          string               `json:"deviceId"`
	Generation        string               `json:"generation"`
	Sequence          int64                `json:"sequence"`
	Workspaces        []Workspace          `json:"workspaces"`
	Sessions          []Session            `json:"sessions"`
	Operations        []Operation          `json:"operations"`
}
type SessionDetail struct {
	History *HistoryWindow `json:"history,omitempty"`
	// HistoryLoaded records a client-side baseline even while its window is
	// being refreshed, or when a legacy worker returned history without a window.
	HistoryLoaded  bool             `json:"-"`
	WorksTruncated bool             `json:"worksTruncated,omitempty"`
	Works          []WorkActivity   `json:"works,omitempty"`
	Plan           *Plan            `json:"plan,omitempty"`
	Queue          QueueState       `json:"queue"`
	Session        Session          `json:"session"`
	Items          []Item           `json:"items"`
	Requests       []PendingRequest `json:"requests"`
	Turns          []Turn           `json:"turns"`
	HasMore        bool             `json:"hasMore"`
	Sequence       int64            `json:"sequence"`
}

func (d SessionDetail) HasLoadedHistory() bool {
	return d.HistoryLoaded || d.History != nil && !d.History.Invalidated
}

type Model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Default       bool     `json:"default"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"defaultEffort"`
	Fast          bool     `json:"fast"`
}
type Readiness struct {
	Account           *RuntimeAccount        `json:"account,omitempty"`
	Sessions          map[string]HarnessInfo `json:"sessions,omitempty"`
	HarnessID         string                 `json:"harnessId,omitempty"`
	Harnesses         []HarnessInfo          `json:"harnesses,omitempty"`
	Capabilities      HarnessCapabilities    `json:"capabilities,omitempty,omitzero"`
	PermissionChoices []OptionChoice         `json:"permissionChoices,omitempty"`
	Permissions       []string               `json:"permissions,omitempty"`
	State             ReadinessState         `json:"state"`
	Error             string                 `json:"error,omitempty"`
	Models            []Model                `json:"models,omitempty"`
	Plan              bool                   `json:"plan"`
}

// GitChoice is draft intent, validated on the owning device at acceptance.
type GitChoice struct {
	Branch      string `json:"branch,omitempty"`
	NewWorktree bool   `json:"newWorktree,omitempty"`
}
type GitStatus struct {
	Applicable    bool     `json:"applicable"`
	Repository    string   `json:"repository,omitempty"`
	Checkout      string   `json:"checkout,omitempty"`
	CommonDir     string   `json:"commonDir,omitempty"`
	Branch        string   `json:"branch,omitempty"`
	Head          string   `json:"head,omitempty"`
	DefaultBranch string   `json:"defaultBranch,omitempty"`
	Branches      []string `json:"branches,omitempty"`
	Dirty         bool     `json:"dirty"`
}
type ChangedFile struct {
	Path    string `json:"path"`
	Kind    string `json:"kind,omitempty"`
	Added   *int   `json:"added,omitempty"`
	Removed *int   `json:"removed,omitempty"`
}
type FileVersions struct {
	Unchanged  bool           `json:"unchanged,omitempty"`
	Snapshot   Attachment     `json:"snapshot,omitempty,omitzero"`
	Comparison DiffComparison `json:"comparison,omitempty,omitzero"`
	Path       string         `json:"path"`
	Old        string         `json:"old"`
	New        string         `json:"new"`
}
type SearchResult struct {
	ItemID      string `json:"itemId,omitempty"`
	MatchStart  int    `json:"matchStart,omitempty"`
	MatchEnd    int    `json:"matchEnd,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
	SessionID   string `json:"sessionId"`
	WorkspaceID string `json:"workspaceId"`
	Title       string `json:"title"`
	Snippet     string `json:"snippet"`
}
