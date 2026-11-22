package app

import (
	"context"
	"errors"
	"time"

	"shioji/internal/supervisor"
	"shioji/internal/transport"
)

// EnsureWorker brings up the local service after the business socket is absent.
func EnsureWorker(ctx context.Context, directory, binary string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	probe, done := context.WithTimeout(ctx, time.Second)
	status, err := supervisor.Call(probe, directory, supervisor.Request{Action: "status"})
	done()
	if err == nil {
		if status.Phase == "blocked" && status.Error != "" {
			return errors.New(status.Error)
		}
		return transport.WaitLocalWorker(ctx, directory)
	}
	service, checkErr := transport.RuntimeService(ctx, directory, binary, false)
	if checkErr != nil {
		return checkErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if service.State == "requiresApproval" {
		return errors.New("请在系统设置中允许 Shioji 后台运行")
	}
	if service.State == "enabled" {
		if err := transport.StartRuntimeService(ctx); err != nil {
			return err
		}
		return transport.WaitLocalWorker(ctx, directory)
	}
	_, err = transport.EnsureLocalWorker(ctx, directory, binary, "")
	return err
}
