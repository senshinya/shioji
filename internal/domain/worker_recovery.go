package domain

import "time"

// Refs are opaque to clients. PIDs and timestamps are diagnostic presentation;
// only an owning-device preview can authorize its exact observed identities.
type WorkerRecoveryProcess struct {
	Ref       string    `json:"ref"`
	PID       int       `json:"pid"`
	Name      string    `json:"name"`
	StartedAt time.Time `json:"startedAt"`
	Evidence  string    `json:"evidence"`
	SessionID string    `json:"sessionId,omitempty"`
	Protected bool      `json:"protected,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}

type WorkerRecoveryPreview struct {
	CandidateBuildID string                   `json:"candidateBuildId,omitempty"`
	RestoreBuildID   string                   `json:"restoreBuildId,omitempty"`
	ScopeUnknown     bool                     `json:"scopeUnknown,omitempty"`
	Operation        *WorkerRecoveryOperation `json:"operation,omitempty"`
	DeviceID         string                   `json:"deviceId"`
	BuildID          string                   `json:"buildId,omitempty"`
	Ref              string                   `json:"ref"`
	CheckedAt        time.Time                `json:"checkedAt"`
	ExpiresAt        time.Time                `json:"expiresAt"`
	Processes        []WorkerRecoveryProcess  `json:"processes"`
	Sessions         []Session                `json:"sessions,omitempty"`
	Warnings         []string                 `json:"warnings,omitempty"`
}

type WorkerRecoveryInput struct {
	ID           string   `json:"id"`
	ActionID     string   `json:"actionId"`
	Action       string   `json:"action"`
	DeviceID     string   `json:"deviceId"`
	PreviewRef   string   `json:"previewRef,omitempty"`
	ProcessRefs  []string `json:"processRefs,omitempty"`
	RiskAccepted bool     `json:"riskAccepted"`
	Update       bool     `json:"update,omitempty"`
	BuildID      string   `json:"buildId,omitempty"`
}

type WorkerRecoveryResult struct {
	Sent    bool                  `json:"sent,omitempty"`
	Process WorkerRecoveryProcess `json:"process"`
	State   string                `json:"state"`
	Message string                `json:"message,omitempty"`
}

type WorkerRecoveryOperation struct {
	Conflict  bool                   `json:"conflict,omitempty"`
	ID        string                 `json:"id"`
	DeviceID  string                 `json:"deviceId"`
	Phase     string                 `json:"phase"`
	Revision  int64                  `json:"revision"`
	BuildID   string                 `json:"buildId,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Results   []WorkerRecoveryResult `json:"results,omitempty"`
	UpdatedAt time.Time              `json:"updatedAt"`
	Backup    string                 `json:"backup,omitempty"`
}

func (o WorkerRecoveryOperation) Active() bool {
	return o.Phase != "completed" && o.Phase != "failed" && o.Phase != "cancelled" && o.Phase != "partial"
}
