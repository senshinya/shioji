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
	ID       string   `json:"id"`
	DeviceID string   `json:"deviceId"`
	Name     string   `json:"name"`
	Folders  []string `json:"folders"` // Primary first; paths resolved on the owning device.
	Revision int64    `json:"revision"`
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
}
type Session struct {
	ID          string        `json:"id"`
	WorkspaceID string        `json:"workspaceId"`
	Title       string        `json:"title"`
	HarnessID   string        `json:"harnessId"`
	ThreadID    string        `json:"threadId,omitempty"` // Opaque source reference.
	Context     LaunchContext `json:"context"`
	Config      RunConfig     `json:"config"`
	Status      string        `json:"status"`
	Revision    int64         `json:"revision"`
	Error       string        `json:"error,omitempty"`
}
type Submission struct {
	OperationID string    `json:"operationId"`
	WorkspaceID string    `json:"workspaceId"`
	SessionID   string    `json:"sessionId,omitempty"`
	Text        string    `json:"text"`
	Config      RunConfig `json:"config"`
}
type Operation struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	State     string `json:"state"`
	Digest    string `json:"-"`
	Error     string `json:"error,omitempty"`
}
type Item struct {
	ID       string `json:"id"`
	TurnID   string `json:"turnId,omitempty"`
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Name     string `json:"name,omitempty"`
	Status   string `json:"status,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Final    bool   `json:"final"`
	Order    int64  `json:"order"`
}
type Question struct {
	ID      string   `json:"id"`
	Header  string   `json:"header"`
	Text    string   `json:"text"`
	Options []Option `json:"options,omitempty"`
	Custom  bool     `json:"custom"`
	Secret  bool     `json:"secret,omitempty"`
}
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}
type PendingRequest struct {
	ID        string     `json:"id"`
	SessionID string     `json:"sessionId"`
	TurnID    string     `json:"turnId"`
	ItemID    string     `json:"itemId"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Text      string     `json:"text"`
	Questions []Question `json:"questions,omitempty"`
	State     string     `json:"state"`
	Revision  int64      `json:"revision"`
}
type Answer struct {
	RequestID string              `json:"requestId"`
	Revision  int64               `json:"revision"`
	Decision  string              `json:"decision,omitempty"`
	Answers   map[string][]string `json:"answers,omitempty"`
}
type Turn struct {
	ID                string     `json:"id"`
	Status            string     `json:"status"`
	StartedAt         *time.Time `json:"startedAt,omitempty"`
	CompletedAt       *time.Time `json:"completedAt,omitempty"`
	DurationMS        *int64     `json:"durationMs,omitempty"`
	InputTokens       *int64     `json:"inputTokens,omitempty"`
	OutputTokens      *int64     `json:"outputTokens,omitempty"`
	CachedInputTokens *int64     `json:"cachedInputTokens,omitempty"`
	Error             string     `json:"error,omitempty"`
}
type Event struct {
	Sequence  int64           `json:"sequence"`
	SessionID string          `json:"sessionId,omitempty"`
	Type      string          `json:"type"`
	ItemID    string          `json:"itemId,omitempty"`
	Text      string          `json:"text,omitempty"`
	Workspace *Workspace      `json:"workspace,omitempty"`
	Session   *Session        `json:"session,omitempty"`
	Operation *Operation      `json:"operation,omitempty"`
	Item      *Item           `json:"item,omitempty"`
	Request   *PendingRequest `json:"request,omitempty"`
	Turn      *Turn           `json:"turn,omitempty"`
}
type Snapshot struct {
	DeviceID   string      `json:"deviceId"`
	Generation string      `json:"generation"`
	Sequence   int64       `json:"sequence"`
	Workspaces []Workspace `json:"workspaces"`
	Sessions   []Session   `json:"sessions"`
	Operations []Operation `json:"operations"`
}
type SessionDetail struct {
	Session  Session          `json:"session"`
	Items    []Item           `json:"items"`
	Requests []PendingRequest `json:"requests"`
	Turns    []Turn           `json:"turns"`
	HasMore  bool             `json:"hasMore"`
	Sequence int64            `json:"sequence"`
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
	State  string  `json:"state"`
	Error  string  `json:"error,omitempty"`
	Models []Model `json:"models,omitempty"`
	Plan   bool    `json:"plan"`
}
