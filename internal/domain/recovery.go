package domain

import (
	"crypto/sha256"
	"encoding/hex"
)

// RecoveryHistory contains normalized source evidence, never harness wire data.
// Inputs maps the original client message identity to its source turn identity.
type RecoveryHistory struct {
	Goal         *Goal
	GoalObserved bool
	// CompleteItemTurns identifies completed source turns whose entire persisted
	// item set was read in this snapshot. Available or a newer locally merged
	// terminal state alone does not authorize treating absent items as discarded.
	CompleteItemTurns   []string
	ExplicitlyStopped   bool
	ContextAction       *ContextObservation
	StoppedGeneration   string
	ManagedStoppedTurns map[string]bool
	// Attached means the adapter still owns this thread and receives live events.
	Available bool
	Attached  bool
	State     SourceState
	Turns     []Turn
	Items     []Item
	Inputs    map[string]string
}

// SessionRecovery is device-owned presentation and continuation eligibility.
// It never replaces the execution status or grants permission to dispatch.
type SessionRecovery struct {
	AutoContinueTurn string         `json:"autoContinueTurn,omitempty"`
	Tasks            []RecoveryTask `json:"tasks,omitempty"`
	CanStopTasks     bool           `json:"canStopTasks,omitempty"`
	Phase            RecoveryPhase  `json:"phase"`
	Message          string         `json:"message,omitempty"`
	CanContinue      bool           `json:"canContinue,omitempty"`
	CanEnd           bool           `json:"canEnd,omitempty"`
	NeedsStop        bool           `json:"needsStop,omitempty"`
}

type RecoveryTask struct {
	ItemID         string `json:"itemId,omitempty"`
	PID            int    `json:"pid,omitempty"`
	ProcessGroup   int    `json:"processGroup,omitempty"`
	ProcessCommand string `json:"processCommand,omitempty"`
	ID             string `json:"id"`
	Command        string `json:"command"`
	State          string `json:"state"` // running, stopped, unknown
}

const ContinuationPrompt = "继续上一轮未完成的任务。先核对已有结果和仍在运行的工作，避免重复执行已生效的操作；无法确认时先说明情况。"

const RecoveryContinuationPrefix = "recovery-continue-"

func RecoveryContinuationID(sessionID, turnID string) string {
	key := sha256.Sum256([]byte(sessionID + "\x00" + turnID))
	return RecoveryContinuationPrefix + hex.EncodeToString(key[:])
}

// SourceState is normalized evidence, independent of the harness connection.
type SourceState string

const (
	SourceIdle        SourceState = "idle"
	SourceActive      SourceState = "active"
	SourceFailed      SourceState = "failed"
	SourceUnavailable SourceState = "unavailable"
)
