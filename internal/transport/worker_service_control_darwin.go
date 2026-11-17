package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
)

// WorkerServiceControl uses the existing authenticated SSH command channel and
// a verified immutable helper; it needs neither a business socket nor a PID UI.
func (s SSH) WorkerServiceControl(ctx context.Context, binary, action, id, device string) (supervisor.Operation, error) {
	if action != "status" && action != "cancel" {
		return supervisor.Operation{}, errors.New("未知服务控制操作")
	}
	path, err := s.workerServiceHelper(ctx, binary)
	if err != nil {
		return supervisor.Operation{}, err
	}
	remote := shellQuote(path) + " --state-dir " + shellQuote(s.StateDirectory) + " --service-control " + shellQuote(action) + " --service-id " + shellQuote(id) + " --service-device " + shellQuote(device)
	cmd, err := s.command(ctx, remote)
	if err != nil {
		return supervisor.Operation{}, err
	}
	var output, diagnostic bytes.Buffer
	cmd.Stdout = &boundedWriter{w: &output, remaining: 1 << 20}
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 2048}
	if err = cmd.Run(); err != nil {
		return supervisor.Operation{}, fmt.Errorf("执行服务控制未完成：%s (%w)", diagnostic.String(), err)
	}
	var response struct {
		Operation   supervisor.Operation `json:"operation"`
		Error, Code string
	}
	if err = json.Unmarshal(output.Bytes(), &response); err != nil {
		return response.Operation, err
	}
	if response.Error != "" {
		return response.Operation, &protocol.RemoteError{Code: response.Code, Message: response.Error}
	}
	return response.Operation, nil
}

// BackgroundStopStatus queries an accepted action through the independent host;
// it cannot invoke native unregister a second time.
func (s SSH) BackgroundStopStatus(ctx context.Context, binary, id, device string) (domain.BackgroundService, error) {
	path, err := s.workerServiceHelper(ctx, binary)
	if err != nil {
		return domain.BackgroundService{}, err
	}
	remote := shellQuote(path) + " --state-dir " + shellQuote(s.StateDirectory) + " --service-control background-status --service-id " + shellQuote(id) + " --service-device " + shellQuote(device)
	cmd, err := s.command(ctx, remote)
	if err != nil {
		return domain.BackgroundService{}, err
	}
	var output, diagnostic bytes.Buffer
	cmd.Stdout = &boundedWriter{w: &output, remaining: 1 << 20}
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 2048}
	if err = cmd.Run(); err != nil {
		return domain.BackgroundService{}, fmt.Errorf("后台关闭回执暂不可用：%s (%w)", diagnostic.String(), err)
	}
	var out struct {
		Background domain.BackgroundService `json:"background"`
		Error      string                   `json:"error"`
	}
	if err = json.Unmarshal(output.Bytes(), &out); err != nil {
		return out.Background, err
	}
	if out.Error != "" {
		return out.Background, errors.New(out.Error)
	}
	return out.Background, nil
}

func (s SSH) workerServiceHelper(ctx context.Context, binary string) (string, error) {
	build, err := InspectWorker(ctx, binary)
	if err != nil {
		return "", err
	}
	if build.Lifecycle != 1 {
		return "", errors.New("当前应用未包含固定服务控制程序")
	}
	path, err := StagedWorkerPath(s.StateDirectory, build.BuildID)
	if err != nil {
		return "", err
	}
	check := "set -eu; root=" + shellQuote(s.StateDirectory) + "; [ -d \"$root\" ] && [ ! -L \"$root\" ]; [ \"$(/usr/bin/stat -f %u \"$root\")\" = \"$(/usr/bin/id -u)\" ]; [ \"$(/usr/bin/stat -f %Lp \"$root\")\" = 700 ]; /usr/bin/codesign --verify --strict " + shellQuote(path) + "; actual=$(/usr/bin/shasum -a 256 " + shellQuote(path) + "); [ \"${actual%% *}\" = " + shellQuote(build.BuildID) + " ]"
	cached, err := s.command(ctx, "/bin/sh -c "+shellQuote(check))
	if err != nil {
		return "", err
	}
	if cached.Run() != nil {
		if _, err = s.StageWorker(ctx, binary); err != nil {
			return "", err
		}
	}
	return path, nil
}

func (s SSH) StartLegacyWorkerUpgrade(ctx context.Context, binary, device string, input domain.WorkerReplacement) (supervisor.Operation, error) {
	return s.workerInstallerOperation(ctx, binary, device, input, false)
}
func (s SSH) RepairWorkerService(ctx context.Context, binary, device string, input domain.WorkerReplacement) (supervisor.Operation, error) {
	return s.workerInstallerOperation(ctx, binary, device, input, true)
}
func (s SSH) workerInstallerOperation(ctx context.Context, binary, device string, input domain.WorkerReplacement, repair bool) (supervisor.Operation, error) {
	path, err := s.workerServiceHelper(ctx, binary)
	if err != nil {
		return supervisor.Operation{}, &protocol.RemoteError{Code: "notSent", Message: err.Error()}
	}
	mode := " --service-migrate start"
	if repair {
		mode = " --service-control repair"
	}
	command := shellQuote(path) + " --state-dir " + shellQuote(s.StateDirectory) + mode + " --service-device " + shellQuote(device)
	cmd, err := s.command(ctx, command)
	if err != nil {
		return supervisor.Operation{}, &protocol.RemoteError{Code: "notSent", Message: err.Error()}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return supervisor.Operation{}, err
	}
	cmd.Stdin = bytes.NewReader(raw)
	var output, diagnostic bytes.Buffer
	cmd.Stdout = &boundedWriter{w: &output, remaining: 1 << 20}
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 2048}
	if err = cmd.Run(); err != nil {
		return supervisor.Operation{}, fmt.Errorf("旧服务更新回执未确认：%s (%w)", diagnostic.String(), err)
	}
	var response struct {
		Operation   supervisor.Operation `json:"operation"`
		Error, Code string
	}
	if err = json.Unmarshal(output.Bytes(), &response); err != nil {
		return response.Operation, err
	}
	if response.Error != "" {
		return response.Operation, &protocol.RemoteError{Code: response.Code, Message: response.Error}
	}
	return response.Operation, nil
}
