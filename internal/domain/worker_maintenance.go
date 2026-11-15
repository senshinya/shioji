package domain

import "time"

type WorkerBlockerKind string

const (
	WorkerBlockerExecution   WorkerBlockerKind = "execution"
	WorkerBlockerEnvironment WorkerBlockerKind = "environment"
	WorkerBlockerUnknown     WorkerBlockerKind = "unknown"
	WorkerBlockerRuntime     WorkerBlockerKind = "runtime"
)

// ProcessRef and Generation are owning-worker references, never client PIDs.
type WorkerMaintenanceTarget struct {
	SessionID     string `json:"sessionId"`
	Generation    string `json:"generation"`
	ProcessRef    string `json:"processRef"`
	Revision      int64  `json:"revision"`
	QueueRevision int64  `json:"queueRevision"`
}

// Diagnostics deliberately omit argv, environment and executable paths.
type WorkerProcessSummary struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

type WorkerUpdateBlocker struct {
	ID            string                   `json:"id"`
	Kind          WorkerBlockerKind        `json:"kind"`
	SessionID     string                   `json:"sessionId,omitempty"`
	WorkspaceID   string                   `json:"workspaceId,omitempty"`
	SessionName   string                   `json:"sessionName,omitempty"`
	WorkspaceName string                   `json:"workspaceName,omitempty"`
	HarnessID     string                   `json:"harnessId,omitempty"`
	Status        ExecutionState           `json:"status,omitempty"`
	Reason        string                   `json:"reason"`
	Target        *WorkerMaintenanceTarget `json:"target,omitempty"`
	Processes     []WorkerProcessSummary   `json:"processes,omitempty"`
}

type WorkerUpdateDiagnosis struct {
	DeviceID  string                `json:"deviceId"`
	BuildID   string                `json:"buildId"`
	Blockers  []WorkerUpdateBlocker `json:"blockers"`
	Operation *WorkerMaintenance    `json:"operation,omitempty"`
	CheckedAt time.Time             `json:"checkedAt"`
}

type WorkerMaintenancePhase string

const (
	WorkerMaintenanceStopping   WorkerMaintenancePhase = "stopping"
	WorkerMaintenanceNeedsForce WorkerMaintenancePhase = "needsForce"
	WorkerMaintenanceWaiting    WorkerMaintenancePhase = "waitingIdle"
	WorkerMaintenanceUpdating   WorkerMaintenancePhase = "updating"
	WorkerMaintenanceCompleted  WorkerMaintenancePhase = "completed"
	WorkerMaintenanceCancelled  WorkerMaintenancePhase = "cancelled"
	WorkerMaintenanceFailed     WorkerMaintenancePhase = "failed"
)

func (p WorkerMaintenancePhase) Active() bool {
	return p != WorkerMaintenanceCompleted && p != WorkerMaintenanceCancelled && p != WorkerMaintenanceFailed
}

type WorkerMaintenanceResult struct {
	Target        WorkerMaintenanceTarget `json:"target"`
	SessionName   string                  `json:"sessionName"`
	WorkspaceName string                  `json:"workspaceName"`
	State         string                  `json:"state"`
	Message       string                  `json:"message,omitempty"`
	Processes     []WorkerProcessSummary  `json:"processes,omitempty"`
	ProcessRef    string                  `json:"processRef,omitempty"`
}

type WorkerMaintenanceAction struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

type WorkerMaintenance struct {
	ID              string                    `json:"id"`
	DeviceID        string                    `json:"deviceId"`
	ExpectedBuildID string                    `json:"expectedBuildId"`
	BuildID         string                    `json:"buildId"`
	Phase           WorkerMaintenancePhase    `json:"phase"`
	Revision        int64                     `json:"revision"`
	Targets         []WorkerMaintenanceResult `json:"targets"`
	Actions         []WorkerMaintenanceAction `json:"actions"`
	Message         string                    `json:"message,omitempty"`
	Backup          string                    `json:"backup,omitempty"`
	UpdatedAt       time.Time                 `json:"updatedAt"`
}

type WorkerMaintenanceInput struct {
	ID              string                    `json:"id"`
	ActionID        string                    `json:"actionId"`
	Action          string                    `json:"action"`
	DeviceID        string                    `json:"deviceId"`
	ExpectedBuildID string                    `json:"expectedBuildId"`
	BuildID         string                    `json:"buildId"`
	Revision        int64                     `json:"revision"`
	Targets         []WorkerMaintenanceTarget `json:"targets"`
}
