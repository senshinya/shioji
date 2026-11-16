package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
)

type RecoveryFence struct{ ID, DeviceID string }

func PendingDeviceRecovery(directory string) (*RecoveryFence, error) {
	b, err := os.ReadFile(filepath.Join(directory, "worker-recovery", "fence.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f RecoveryFence
	if json.Unmarshal(b, &f) != nil || f.ID == "" || f.DeviceID == "" {
		return nil, errors.New("执行服务恢复记录不完整，请打开强制恢复")
	}
	return &f, nil
}

func recoveryArguments(directory, action string, in domain.WorkerRecoveryInput) ([]string, error) {
	if !filepath.IsAbs(directory) || strings.ContainsAny(directory, "\x00\r\n") || in.DeviceID == "" {
		return nil, errors.New("恢复设备或目录信息不完整")
	}
	if action != "inspect" && action != "start" && action != "status" && action != "cancel" {
		return nil, errors.New("未知恢复操作")
	}
	return []string{"--state-dir", directory, "--recovery-helper", action, "--recovery-device", in.DeviceID, "--recovery-id", in.ID}, nil
}

func recoveryCommand(cmd *exec.Cmd, in domain.WorkerRecoveryInput) (json.RawMessage, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = bytes.NewReader(b)
	var output, diagnostic bytes.Buffer
	cmd.Stdout = &boundedWriter{w: &output, remaining: 4 << 20}
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 2048}
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("设备恢复失败：%s (%w)", strings.TrimSpace(diagnostic.String()), err)
	}
	var result struct {
		DeviceID  string `json:"deviceId"`
		ID        string `json:"id"`
		Error     string `json:"error"`
		ErrorCode string `json:"errorCode"`
		Conflict  bool   `json:"conflict"`
	}
	if json.Unmarshal(output.Bytes(), &result) != nil || result.DeviceID != in.DeviceID {
		return nil, errors.New("恢复回执不属于指定设备")
	}
	if result.Error != "" {
		return nil, &protocol.RemoteError{Code: result.ErrorCode, Message: result.Error}
	}
	if in.ID != "" && result.ID != in.ID && !result.Conflict {
		return nil, errors.New("恢复回执不属于原操作")
	}
	return json.RawMessage(append([]byte(nil), output.Bytes()...)), nil
}

func RunWorkerRecovery(ctx context.Context, binary, directory, action string, in domain.WorkerRecoveryInput) (json.RawMessage, error) {
	args, err := recoveryArguments(directory, action, in)
	if err != nil {
		return nil, err
	}
	b, err := InspectWorker(ctx, binary)
	if err != nil {
		return nil, err
	}
	if !b.RecoveryHelper {
		return nil, errors.New("当前应用未携带恢复程序，请更新控制端应用")
	}
	if action == "start" && in.Update {
		if in.BuildID != b.BuildID {
			return nil, &protocol.RemoteError{Code: "notSent", Message: "恢复构建已变化，请重新检查"}
		}
		if _, err = StageWorker(ctx, directory, binary); err != nil {
			return nil, &protocol.RemoteError{Code: "notSent", Message: "恢复构建暂存未完成"}
		}
	}
	return recoveryCommand(exec.CommandContext(ctx, binary, args...), in)
}

// Staging does not touch the live owner and needs no capability from old RPC.
func (s SSH) WorkerRecovery(ctx context.Context, binary, action string, in domain.WorkerRecoveryInput) (json.RawMessage, error) {
	b, err := InspectWorker(ctx, binary)
	if err != nil {
		return nil, err
	}
	if !b.RecoveryHelper {
		return nil, errors.New("当前应用未携带恢复程序")
	}
	path, err := StagedWorkerPath(s.StateDirectory, b.BuildID)
	if err != nil {
		return nil, err
	}
	// Polling reuses the immutable helper only after checking its signature and digest.
	check := "set -eu; root=" + shellQuote(s.StateDirectory) + "; [ -d \"$root\" ] && [ ! -L \"$root\" ]; [ \"$(/usr/bin/stat -f %u \"$root\")\" = \"$(/usr/bin/id -u)\" ]; [ \"$(/usr/bin/stat -f %Lp \"$root\")\" = 700 ]; /usr/bin/codesign --verify --strict " + shellQuote(path) + "; actual=$(/usr/bin/shasum -a 256 " + shellQuote(path) + "); [ \"${actual%% *}\" = " + shellQuote(b.BuildID) + " ]"
	cached, e := s.command(ctx, "/bin/sh -c "+shellQuote(check))
	if e != nil {
		return nil, e
	}
	if cached.Run() != nil {
		if _, err = s.StageWorker(ctx, binary); err != nil {
			return nil, err
		}
	}
	args, err := recoveryArguments(s.StateDirectory, action, in)
	if err != nil {
		return nil, err
	}
	remote := shellQuote(path)
	for _, arg := range args {
		remote += " " + shellQuote(arg)
	}
	cmd, err := s.command(ctx, remote)
	if err != nil {
		return nil, err
	}
	return recoveryCommand(cmd, in)
}
