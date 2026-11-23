package worker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
	"shioji/internal/transport"
)

func StartSchemaUpgrade(ctx context.Context, directory, executable, device string, in domain.WorkerReplacement) (supervisor.Operation, error) {
	candidate, err := transport.VerifyStagedWorker(ctx, directory, in.BuildID)
	if err != nil {
		return supervisor.Operation{}, &protocol.RemoteError{Code: "notSent", Message: err.Error()}
	}
	path, err := transport.StagedWorkerPath(directory, in.BuildID)
	if err != nil {
		return supervisor.Operation{}, err
	}
	build, err := transport.InspectWorker(ctx, executable)
	if err != nil || build.BuildID != candidate.BuildID {
		return supervisor.Operation{}, &protocol.RemoteError{Code: "notSent", Message: "迁移助手与签名候选构建不一致"}
	}
	if pending, ok, err := supervisor.PendingInstallation(directory); err != nil {
		return supervisor.Operation{}, err
	} else if ok && pending.ExecutionRegistration != "" {
		if pending.Input != in || pending.DeviceID != device {
			return supervisor.Operation{}, &protocol.RemoteError{Code: "notSent", Message: "设备正在接续另一项执行服务更新"}
		}
		if err = transport.StartExecutionRuntime(ctx, directory); err != nil {
			return pending, err
		}
		return supervisor.Query(ctx, directory, in.ID)
	}
	registrationBuild := ""
	if in.RuntimeHandoff && candidate.RuntimeHost >= domain.RuntimeHostRegistrationRefresh {
		registrationBuild = candidate.BuildID
	}
	if err := transport.CheckInstallationRuntime(ctx, directory, registrationBuild); err != nil {
		return supervisor.Operation{}, &protocol.RemoteError{Code: "notSent", Message: err.Error()}
	}
	start := supervisor.StartSchemaMigration
	if in.RuntimeHandoff && !in.SchemaMigration {
		start = supervisor.StartRuntimeMigration
	}
	return start(ctx, directory, device, in, candidate, func(guard *os.File) error {
		log, err := os.OpenFile(filepath.Join(directory, "supervisor", "schema-bootstrap.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer log.Close()
		cmd := exec.Command(path, "--state-dir", directory, "--service-schema-upgrade", "run", "--service-id", in.ID, "--service-device", device)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.ExtraFiles = []*os.File{guard}
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			return err
		}
		_ = cmd.Process.Release()
		return nil
	})
}

func RunSchemaUpgrade(ctx context.Context, directory, executable, id, device string, guard *os.File) error {
	exe, err := transport.InspectWorker(ctx, executable)
	if err != nil {
		return err
	}
	build, err := transport.VerifyStagedWorker(ctx, directory, exe.BuildID)
	if err != nil {
		return err
	}
	path, err := transport.StagedWorkerPath(directory, build.BuildID)
	if err != nil {
		return err
	}
	if path != executable {
		return errors.New("迁移助手未从已验证的候选制品启动")
	}
	return supervisor.RunSchemaBootstrap(ctx, directory, id, device, path, build, guard, func(install supervisor.LegacyInstall, managed bool) error {
		return transport.HandoffWorkerInstallation(ctx, directory, install)
	})
}
