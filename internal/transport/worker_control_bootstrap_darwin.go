package transport

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"shioji/internal/supervisor"
	"syscall"
	"time"
)

func RecoverInterruptedSchemaControl(ctx context.Context, directory, binary, codex, device string) (bool, error) {
	prior, recoverable := supervisor.InterruptedSchemaControl(directory)
	if !recoverable {
		return false, nil
	}
	build, err := InspectWorker(ctx, binary)
	if err != nil {
		return false, err
	}
	if device == "" || prior.DeviceID != device || build.BuildID != prior.Input.BuildID || !build.MigratesSchema(prior.SourceSchema) {
		return false, &protocol.RemoteError{Code: "notSent", Message: "数据库恢复需要原迁移的软件包和设备身份"}
	}
	if err = supervisor.ReleaseInterruptedSchemaControl(ctx, directory, prior); err != nil {
		return false, err
	}
	if err = EnsureRepairControl(ctx, directory, binary, codex, device); err != nil {
		return false, err
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		call, cancel := context.WithTimeout(ctx, time.Second)
		status, e := supervisor.Call(call, directory, supervisor.Request{Action: "status", ID: prior.Input.ID})
		cancel()
		if e == nil && status.DeviceID == device && status.Input == prior.Input {
			switch status.Phase {
			case "rolled-back":
				return true, nil
			case "blocked":
				return false, errors.New(status.Error)
			}
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-tick.C:
		}
	}
}

// EnsureRepairControl restores only the fixed control entry. Its recovered
// blocked state opens no business writer; normal repair admission still applies.
func EnsureRepairControl(ctx context.Context, directory, binary, codex, device string) error {
	control, cancel := context.WithTimeout(ctx, time.Second)
	_, err := supervisor.Call(control, directory, supervisor.Request{Action: "status"})
	cancel()
	if err == nil {
		return nil
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	prior, err := supervisor.Query(probe, directory, "")
	if err != nil {
		cancel()
		return err
	}
	actual, _, err := store.ReadWorkerIdentity(probe, directory)
	cancel()
	if err != nil {
		return err
	}
	_, interruptedSchema := supervisor.InterruptedSchemaControl(directory)
	if device == "" || actual != device || prior.DeviceID != device || prior.Phase != "blocked" && !interruptedSchema {
		return &protocol.RemoteError{Code: "notSent", Message: "未确认该设备需要冷修复，原件已保留"}
	}
	lease, err := supervisor.AcquireLease(directory)
	if err == nil {
		// The launched fixed entry acquires this same lease before serving or
		// opening any child. Concurrent installers can establish only one owner.
		lease.Close()
		build, e := StageWorker(ctx, directory, binary)
		if e != nil {
			return e
		}
		path, e := StagedWorkerPath(directory, build.BuildID)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Join(directory, "supervisor"), 0700); e != nil {
			return e
		}
		log, e := os.OpenFile(filepath.Join(directory, "supervisor", "repair-host.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return e
		}
		defer log.Close()
		command := exec.Command(path, "--state-dir", directory, "--codex", codex)
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		command.Stdout, command.Stderr = log, log
		if e = command.Start(); e != nil {
			return e
		}
		_ = command.Process.Release()
	} else if !errors.Is(err, supervisor.ErrOwned) {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		call, stop := context.WithTimeout(wait, 200*time.Millisecond)
		ready, e := supervisor.Call(call, directory, supervisor.Request{Action: "status"})
		stop()
		if e == nil {
			if ready.DeviceID != device {
				return &protocol.RemoteError{Code: "notSent", Message: "固定服务设备身份已变化"}
			}
			return nil
		}
		select {
		case <-wait.Done():
			return errors.New("固定服务控制尚未就绪，请稍后重试；原操作与数据保留在设备上")
		case <-time.After(30 * time.Millisecond):
		}
	}
}
