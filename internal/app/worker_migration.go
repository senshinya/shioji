package app

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/transport"
)

type MigrationRequestState string

const (
	MigrationSubmitting MigrationRequestState = "submitting"
	MigrationObserving  MigrationRequestState = "observing"
	MigrationResolved   MigrationRequestState = "resolved"
)

type WorkerMigrationTarget struct {
	ConnectionID string                        `json:"connectionId"`
	Name         string                        `json:"name"`
	Setup        bool                          `json:"setup"`
	SSH          *transport.SSH                `json:"ssh,omitempty"`
	Info         transport.WorkerInfo          `json:"info"`
	Input        domain.WorkerMigrationInput   `json:"input"`
	Status       *domain.WorkerMigrationStatus `json:"status,omitempty"`
	Request      MigrationRequestState         `json:"request"`
}
type WorkerMigrationAction struct {
	Action string
	Target WorkerMigrationTarget
}

func (c *Controller) inspectWorkerMigration(ctx context.Context, target WorkerMigrationTarget) (transport.WorkerInfo, error) {
	if target.SSH != nil {
		ssh := *target.SSH
		if target.Info.Worker != "" {
			ssh.Worker = target.Info.Worker
			ssh.StateDirectory = target.Info.Directory
			return ssh.InspectWorker(ctx, target.Info.DeviceID)
		}
		return ssh.Bootstrap(ctx, c.fleet.workerBinary)
	}
	if target.ConnectionID != "" && target.ConnectionID != "local" {
		return transport.WorkerInfo{}, errors.New("设备连接信息已移除")
	}
	return transport.InspectWorkerInfo(ctx, c.Directory, c.fleet.workerBinary, target.Info.DeviceID)
}
func (c *Controller) runWorkerMigration(ctx context.Context, in WorkerMigrationAction) (domain.WorkerMigrationStatus, error) {
	target := in.Target
	if target.Info.DeviceID == "" || target.Info.DeviceID != target.Input.DeviceID {
		return domain.WorkerMigrationStatus{}, errors.New("服务准备设备身份不一致")
	}
	if target.SSH != nil {
		ssh := *target.SSH
		ssh.Worker = target.Info.Worker
		ssh.StateDirectory = target.Info.Directory
		return ssh.WorkerMigration(ctx, in.Action, target.Input)
	}
	return transport.RunWorkerMigration(ctx, target.Info.Worker, target.Info.Directory, in.Action, target.Input)
}
