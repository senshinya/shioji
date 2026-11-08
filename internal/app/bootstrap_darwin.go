package app

import (
	"context"
	"errors"
	"shioji/internal/transport"
	"time"
)

// EnsureWorker starts the bundled developer worker only when no local owner is
// reachable. Release builds use the runtime service host.
func EnsureWorker(ctx context.Context, directory, binary string) error {
	service, checkErr := transport.RuntimeService(ctx, directory, binary, false)
	if checkErr != nil {
		return checkErr
	}
	if service.State == "requiresApproval" {
		return errors.New("请在系统设置中允许 Shioji 后台运行")
	}
	if service.State == "enabled" {
		wait, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		return transport.WaitLocalWorker(wait, directory)
	}
	_, err := transport.EnsureLocalWorker(ctx, directory, binary, "")
	return err
}
