package worker

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/runtimebuild"
	"shioji/internal/transport"
	"strconv"
	"strings"
)

type recoveryServiceCommand func(context.Context, ...string) ([]byte, error)

func nativeRecoveryServiceCommand(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "/bin/launchctl", args...).Output()
}
func serviceObservation(body []byte) (pid int, plist string) {
	for _, line := range strings.Split(string(body), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " = ")
		if !ok {
			continue
		}
		switch key {
		case "pid":
			pid, _ = strconv.Atoi(value)
		case "path":
			plist = value
		}
	}
	return
}
func inspectRecoveryService(ctx context.Context, label string, owner managedexec.ProcessInfo, run recoveryServiceCommand) (string, error) {
	if managedexec.ProcessState(owner) != "running" {
		return "", errors.New("执行服务实例已变化，请重新确认")
	}
	body, err := run(ctx, "print", label)
	if err != nil {
		return "", errors.New("无法核对执行服务注册实例，请重新检查")
	}
	pid, plist := serviceObservation(body)
	if pid != owner.Identity.PID || !filepath.IsAbs(plist) || filepath.Ext(plist) != ".plist" || managedexec.ProcessState(owner) != "running" {
		return "", errors.New("执行服务注册实例已变化，请重新确认")
	}
	return plist, nil
}
func suspendRecoveryService(ctx context.Context, label string, owner managedexec.ProcessInfo, run recoveryServiceCommand) (string, error) {
	// The exact owner is already SIGSTOP'd. Query the registered live instance
	// immediately before bootout; an exited/replaced owner grants no label authority.
	plist, err := inspectRecoveryService(ctx, label, owner, run)
	if err != nil {
		return "", err
	}
	if _, err = run(ctx, "bootout", label); err != nil {
		return "", errors.New("无法暂挂执行服务自动启动，请重新检查")
	}
	return plist, nil
}
func inheritRecoveryRestoration(previous deviceRecoveryRecord, next *deviceRecoveryRecord) {
	if previous.Operation.Phase == "completed" || previous.SourceBinary == "" {
		return
	}
	next.SourceBinary = previous.SourceBinary
	next.SourceDigest = previous.SourceDigest
	next.SourceError = previous.SourceError
	next.Service = previous.Service
	next.ServicePlist = previous.ServicePlist
	next.ServiceSuspended = previous.ServiceSuspended
	next.ServiceSuspendPending = previous.ServiceSuspendPending
}
func pinRecoverySource(ctx context.Context, dir string, r *deviceRecoveryRecord) {
	if r.SourceError != "" {
		return
	}
	digest, err := runtimebuild.Digest(r.SourceBinary)
	if err != nil || r.SourceDigest != "" && digest != r.SourceDigest {
		r.SourceError = "原执行构建不可用，关闭后需要明确选择替换构建"
		return
	}
	r.SourceDigest = digest
	build, err := transport.StageWorker(ctx, dir, r.SourceBinary)
	if err != nil || build.BuildID != digest {
		r.SourceError = "原执行构建无法验证，关闭后需要明确选择替换构建"
		return
	}
	r.SourceBinary, err = transport.StagedWorkerPath(dir, digest)
	if err != nil {
		r.SourceError = "原执行构建无法固定，请明确选择替换构建"
		return
	}
	r.SourceError = ""
}
func verifyRecoveryBuild(r deviceRecoveryRecord, build domain.WorkerBuild) error {
	expected := r.SourceDigest
	if r.Input.Update {
		expected = r.Input.BuildID
	}
	if expected == "" || build.BuildID != expected {
		return errors.New("重连后的执行服务构建与已确认恢复版本不一致")
	}
	return nil
}
