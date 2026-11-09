package domain

import "time"

// OwnerRef is opaque outside the owning device's migration helper.
type LegacyWorkerInfo struct {
	DeviceID  string `json:"deviceId"`
	OwnerRef  string `json:"ownerRef"`
	Major     int    `json:"major"`
	Minor     int    `json:"minor"`
	Supported bool   `json:"supported"`
	Uncertain bool   `json:"uncertain,omitempty"`
	Busy      bool   `json:"busy"`
	Message   string `json:"message,omitempty"`
}
type WorkerMigrationPhase string

const (
	MigrationPrepared  WorkerMigrationPhase = "prepared"
	MigrationStopping  WorkerMigrationPhase = "stopping"
	MigrationBackingUp WorkerMigrationPhase = "backingUp"
	MigrationStarting  WorkerMigrationPhase = "starting"
	MigrationVerifying WorkerMigrationPhase = "verifying"
	MigrationCompleted WorkerMigrationPhase = "completed"
	MigrationFailed    WorkerMigrationPhase = "failed"
	MigrationBlocked   WorkerMigrationPhase = "blocked"
)

func (p WorkerMigrationPhase) Active() bool {
	switch p {
	case MigrationPrepared, MigrationStopping, MigrationBackingUp, MigrationStarting, MigrationVerifying:
		return true
	}
	return false
}

type WorkerMigrationStatus struct {
	OwnerRef  string               `json:"ownerRef,omitempty"`
	ID        string               `json:"id"`
	DeviceID  string               `json:"deviceId"`
	Phase     WorkerMigrationPhase `json:"phase"`
	Message   string               `json:"message,omitempty"`
	CanResume bool                 `json:"canResume,omitempty"`
	Backup    string               `json:"backup,omitempty"`
	UpdatedAt time.Time            `json:"updatedAt"`
}

type WorkerMigrationInput struct {
	ID       string `json:"id"`
	DeviceID string `json:"deviceId"`
	OwnerRef string `json:"ownerRef"`
}
