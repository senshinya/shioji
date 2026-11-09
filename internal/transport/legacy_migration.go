package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
)

func migrationArguments(directory, action string, in domain.WorkerMigrationInput) ([]string, error) {
	if !filepath.IsAbs(directory) || strings.ContainsAny(directory, "\x00\r\n") || in.ID == "" || in.DeviceID == "" {
		return nil, errors.New("迁移的设备、目录或操作标识不完整")
	}
	if action != "start" && action != "status" && action != "resume" {
		return nil, errors.New("无效的服务准备操作")
	}
	if action != "status" && in.OwnerRef == "" {
		return nil, errors.New("请重新核对旧服务身份")
	}
	return []string{"--state-dir", directory, "--legacy-migration", action, "--migration-id", in.ID, "--migration-device", in.DeviceID, "--migration-owner", in.OwnerRef}, nil
}
func decodeWorkerMigration(output []byte, in domain.WorkerMigrationInput) (domain.WorkerMigrationStatus, error) {
	var state domain.WorkerMigrationStatus
	var problem protocol.Frame
	if json.Unmarshal(output, &problem) == nil && problem.Type == "worker-migration-error" {
		if problem.ID != in.ID || problem.DeviceID != in.DeviceID {
			return state, errors.New("服务准备错误回执不属于指定设备或操作")
		}
		return state, &protocol.RemoteError{Code: problem.ErrorCode, Message: problem.Error}
	}
	if err := json.Unmarshal(output, &state); err != nil {
		return state, errors.New("设备未返回有效的服务准备进度")
	}
	if state.ID == "" || state.DeviceID != in.DeviceID {
		return state, errors.New("服务准备回执不属于指定设备")
	}
	if state.Phase == "" {
		return state, errors.New("服务准备状态未报告")
	}
	return state, nil
}
func RunWorkerMigration(ctx context.Context, binary, directory, action string, in domain.WorkerMigrationInput) (domain.WorkerMigrationStatus, error) {
	args, e := migrationArguments(directory, action, in)
	if e != nil {
		return domain.WorkerMigrationStatus{}, e
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	return runWorkerMigrationCommand(cmd, in)
}
func (s SSH) WorkerMigration(ctx context.Context, action string, in domain.WorkerMigrationInput) (domain.WorkerMigrationStatus, error) {
	if !filepath.IsAbs(s.Worker) || strings.ContainsAny(s.Worker, "\x00\r\n") {
		return domain.WorkerMigrationStatus{}, errors.New("设备准备程序路径无效")
	}
	args, e := migrationArguments(s.StateDirectory, action, in)
	if e != nil {
		return domain.WorkerMigrationStatus{}, e
	}
	remote := shellQuote(s.Worker)
	for _, arg := range args {
		remote += " " + shellQuote(arg)
	}
	cmd, e := s.command(ctx, remote)
	if e != nil {
		return domain.WorkerMigrationStatus{}, e
	}
	return runWorkerMigrationCommand(cmd, in)
}
func runWorkerMigrationCommand(cmd *exec.Cmd, in domain.WorkerMigrationInput) (domain.WorkerMigrationStatus, error) {
	var output, diagnostic bytes.Buffer
	cmd.Stdout = &boundedWriter{w: &output, remaining: 16 << 10}
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 4096}
	if e := cmd.Run(); e != nil {
		return domain.WorkerMigrationStatus{}, fmt.Errorf("无法核对设备准备进度：%s (%w)", strings.TrimSpace(diagnostic.String()), e)
	}
	return decodeWorkerMigration(output.Bytes(), in)
}

// InspectWorkerInfo never starts a service. It is safe for busy-state polling.
func InspectWorkerInfo(ctx context.Context, directory, binary, expected string) (WorkerInfo, error) {
	home, _ := os.UserHomeDir()
	info := WorkerInfo{Directory: directory, Worker: binary, Home: home, DeviceID: expected}
	pending, e := PendingWorkerMigration(directory)
	if e != nil {
		return info, e
	}
	if pending != nil && pending.Phase.Active() {
		info.DeviceID = pending.DeviceID
		info.Migration = pending
		return info, nil
	}
	client, e := protocol.Dial(ctx, func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(directory, "worker.sock"))
	}, expected)
	if e == nil {
		defer client.Close()
		info.DeviceID = client.DeviceID
		info.Minor = client.Minor
		info.Build = client.Worker
		return info, nil
	}
	var legacy *protocol.LegacyWorkerError
	if errors.As(e, &legacy) {
		inspection, e := InspectLegacyWorker(ctx, directory, expected)
		if e != nil {
			return info, e
		}
		info.DeviceID = inspection.Info.DeviceID
		info.Legacy = &inspection.Info
		return info, nil
	}
	if pending != nil {
		info.DeviceID = pending.DeviceID
		info.Migration = pending
		return info, nil
	}
	return info, e
}
func (s SSH) InspectWorker(ctx context.Context, expected string) (WorkerInfo, error) {
	var info WorkerInfo
	if !filepath.IsAbs(s.Worker) || !filepath.IsAbs(s.StateDirectory) {
		return info, errors.New("设备准备程序信息缺失")
	}
	remote := shellQuote(s.Worker) + " --state-dir " + shellQuote(s.StateDirectory) + " --legacy-migration inspect --migration-device " + shellQuote(expected)
	cmd, e := s.command(ctx, remote)
	if e != nil {
		return info, e
	}
	var output, diagnostic bytes.Buffer
	cmd.Stdout = &boundedWriter{w: &output, remaining: 16 << 10}
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 4096}
	if e = cmd.Run(); e != nil {
		return info, fmt.Errorf("无法检查设备服务：%s (%w)", strings.TrimSpace(diagnostic.String()), e)
	}
	if e = json.Unmarshal(output.Bytes(), &info); e != nil {
		return info, e
	}
	if info.DeviceID != expected {
		return info, errors.New("设备身份已改变")
	}
	return info, nil
}
