package app

import (
	"context"
	"errors"
	"io"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
	"shioji/internal/transport"
)

func (c *Controller) changeWorkerBackground(ctx context.Context, worker *protocol.Client, build *domain.WorkerBuild, remote transport.RemoteWorker, ssh *transport.SSH, action string) (domain.BackgroundService, error) {
	if remote.Run != nil && action == "unregister" && build == nil {
		return domain.BackgroundService{}, errors.New("请先重新核对设备的执行服务版本")
	}
	registration := func(action string) (domain.BackgroundService, error) {
		if remote.Run != nil {
			artifact := c.AuthenticatedRuntimeArtifact
			if artifact != nil {
				artifact = func(ctx context.Context) (io.ReadCloser, string, error) {
					bundle := c.AuthenticatedWorkerBuild
					if build == nil || bundle == nil || build.Schema == 0 || build.Schema != bundle.Schema {
						return nil, "", errors.New("后台宿主与设备数据库版本不一致，请先完成执行服务更新")
					}
					return c.AuthenticatedRuntimeArtifact(ctx)
				}
			}
			return remote.RuntimeRegistration(ctx, action, artifact)
		}
		if ssh != nil {
			if action == "unregister" {
				return ssh.DisableRuntimeService(ctx, c.WorkerBinary)
			}
			return ssh.RuntimeService(ctx, c.WorkerBinary, action == "register")
		}
		if action == "unregister" {
			return transport.DisableRuntimeService(ctx, c.Directory, c.WorkerBinary)
		}
		return transport.RuntimeService(ctx, c.Directory, c.WorkerBinary, action == "register")
	}
	if action != "unregister" || worker == nil || build == nil || !build.Managed {
		return registration(action)
	}
	if worker.Minor < 48 {
		return domain.BackgroundService{}, errors.New("请先更新执行服务，再关闭后台运行")
	}
	id := domain.NewID()
	var state domain.BackgroundService
	err := worker.Call(ctx, "worker.background.stop", protocol.BackgroundStopInput{ID: id}, &state)
	var refused *protocol.RemoteError
	if errors.As(err, &refused) && refused.Code == "notSent" {
		return state, err
	}
	if err != nil || state.State == "disabling" {
		// A lost RPC response can only trigger observation of its original ID.
		for {
			switch {
			case remote.Run != nil:
				state, err = remote.BackgroundStopStatus(ctx, id, worker.DeviceID)
			case ssh != nil:
				state, err = ssh.BackgroundStopStatus(ctx, c.WorkerBinary, id, worker.DeviceID)
			default:
				state, err = supervisor.BackgroundCall(ctx, c.Directory, supervisor.Request{Action: "background-status", ID: id, DeviceID: worker.DeviceID})
			}
			if err != nil {
				if state.NotStarted {
					return state, err
				}
				cause := err
				state, err = registration("status")
				if err == nil && state.State != "notRegistered" && state.State != "notInstalled" {
					state.State, state.Message = "disabling", cause.Error()
				}
			}
			if err == nil && state.State != "disabling" {
				break
			}
			select {
			case <-ctx.Done():
				return state, ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	if state.State == "notRegistered" || state.State == "notInstalled" {
		if remote.Run != nil {
			err = remote.EnsureDetached(ctx, worker.DeviceID)
		} else {
			err = transport.EnsureDetachedWorker(ctx, c.Directory, c.WorkerBinary, worker.DeviceID, ssh)
		}
	}
	return state, err
}
