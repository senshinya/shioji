package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
	"shioji/internal/transport"
)

type workerUpgradeRecord struct {
	Rejected        bool
	Error, JoinedID string
	DeviceID        string
	Input           domain.WorkerReplacement
	Operation       *supervisor.Operation
}
type workerUpgradeClient struct {
	directory, device string
	knownBuild        *domain.WorkerBuild
	control           func(context.Context, string, string) (supervisor.Operation, error)
	legacy            func(context.Context, domain.WorkerReplacement) (supervisor.Operation, error)
	repair            func(context.Context, domain.WorkerReplacement) (supervisor.Operation, error)
}

func (u workerUpgradeClient) path() string {
	key := sha256.Sum256([]byte(u.device))
	return filepath.Join(u.directory, "client-updates", hex.EncodeToString(key[:])+".json")
}
func (u workerUpgradeClient) load() (*workerUpgradeRecord, error) {
	if !filepath.IsAbs(u.directory) {
		return nil, errors.New("客户端更新状态目录无效")
	}
	raw, err := os.ReadFile(u.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r workerUpgradeRecord
	if len(raw) > 1<<20 || json.Unmarshal(raw, &r) != nil || r.DeviceID != u.device || r.Input.ID == "" || filepath.Base(r.Input.ID) != r.Input.ID || r.Input.ID == "." || r.Input.ID == ".." || len(r.Input.ID) > 128 {
		return nil, errors.New("更新意图记录损坏，保留原件后使用兼容安装器修复")
	}
	return &r, nil
}
func (u workerUpgradeClient) save(r *workerUpgradeRecord) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	// Retain each stable ID as well as the current pointer; retries never erase
	// a prior unknown or failed receipt.
	archive := filepath.Join(filepath.Dir(u.path()), "receipts", r.Input.ID+".json")
	if err = WriteState(archive, raw); err != nil {
		return err
	}
	if err = WriteState(u.path(), raw); err != nil {
		return err
	}
	for _, path := range []string{filepath.Dir(archive), filepath.Dir(u.path())} {
		dir, e := os.Open(path)
		if e != nil {
			return e
		}
		e = dir.Sync()
		dir.Close()
		if e != nil {
			return e
		}
	}
	return nil
}
func upgradeService(current, available *domain.WorkerBuild, r *workerUpgradeRecord) *WorkerService {
	state := newWorkerService(current, available)
	if r == nil {
		return state
	}
	state.OperationID = r.Input.ID
	if r.Rejected {
		state.Phase = WorkerUpdateFailed
		state.Message = r.Error
		return state
	}
	if r.Operation == nil {
		state.Phase = WorkerUpdating
		state.Message = "正在查询原更新结果，会话和草稿已保留"
		return state
	}
	op := r.Operation
	state.OperationPhase = op.Phase
	switch op.Phase {
	case "waiting":
		state.Phase = WorkerWaiting
		state.Message = "等待实际任务完成，空闲工具会自动回收"
	case "applied":
		if current != nil && current.BuildID == op.Input.BuildID {
			if available == nil || available.BuildID == op.Input.BuildID {
				state.Phase = WorkerCurrent
			}
		} else {
			state.Phase = WorkerUpdating
			state.Message = "更新已提交，正在重新连接"
		}
	case "rolled-back":
		state.Phase = WorkerUpdateFailed
		state.Message = "候选版本未就绪，已恢复原版本，可重试更新"
	case "failed", "blocked":
		state.Phase = WorkerUpdateFailed
		state.Message = op.Error
	default:
		state.Phase = WorkerUpdating
		state.Message = "正在准备、切换或核对执行服务"
	}
	return state
}
func (u workerUpgradeClient) observe(ctx context.Context, client *protocol.Client, candidate *domain.WorkerBuild) (*WorkerService, error) {
	current := u.knownBuild
	if client != nil {
		current = client.Worker
	}
	r, err := u.load()
	if err != nil {
		return nil, err
	}
	if r == nil {
		return newWorkerService(current, candidate), nil
	}
	if r.Rejected {
		return upgradeService(current, candidate, r), nil
	}
	query := func(id string) (supervisor.Operation, error) {
		var op supervisor.Operation
		var e error
		if client != nil {
			e = client.Call(ctx, "worker.update.status", struct {
				ID string `json:"id"`
			}{id}, &op)
		} else {
			e = errors.New("business writer unavailable")
		}
		if e != nil && u.control != nil {
			op, e = u.control(ctx, "status", id)
		}
		return op, e
	}
	op, err := query(r.Input.ID)
	if err != nil {
		// A second client may have joined the same target before its reply was
		// lost. Following an existing matching receipt never resends an update.
		if active, e := query(""); e == nil && active.Input.ID != "" && active.DeviceID == u.device && active.Input.ExpectedBuildID == r.Input.ExpectedBuildID && active.Input.BuildID == r.Input.BuildID && active.Input.Policy == r.Input.Policy {
			r.JoinedID = active.Input.ID
			if e = u.save(r); e != nil {
				return upgradeService(current, candidate, r), e
			}
			r = &workerUpgradeRecord{DeviceID: u.device, Input: active.Input, Operation: &active}
			op = active
			err = nil
		}
	}
	if err != nil {
		return upgradeService(current, candidate, r), err
	}
	if op.Input != r.Input || op.DeviceID != u.device {
		return upgradeService(current, candidate, r), errors.New("更新回执不属于原设备和操作")
	}
	r.Operation = &op
	if err = u.save(r); err != nil {
		return upgradeService(current, candidate, r), err
	}
	state := upgradeService(current, candidate, r)
	if actual, e := query(""); e == nil && actual.DeviceID == u.device && actual.Phase == "blocked" {
		state.Phase, state.Message = WorkerUpdateFailed, actual.Error
	}
	return state, nil
}

func (c *Controller) upgradeObserver(device string, known *domain.WorkerBuild) workerUpgradeClient {
	return workerUpgradeClient{directory: c.workerUpdateDirectory, device: device, knownBuild: known, repair: func(ctx context.Context, in domain.WorkerReplacement) (supervisor.Operation, error) {
		return c.runWorkerInstaller(ctx, device, in, true)
	}, legacy: func(ctx context.Context, in domain.WorkerReplacement) (supervisor.Operation, error) {
		return c.startLegacyWorkerUpgrade(ctx, device, in)
	}, control: func(ctx context.Context, action, id string) (supervisor.Operation, error) {
		if ssh := c.currentSSH(); ssh != nil {
			return ssh.WorkerServiceControl(ctx, c.WorkerBinary, action, id, device)
		}
		if action == "status" {
			return supervisor.Query(ctx, c.Directory, id)
		}
		op, e := supervisor.Call(ctx, c.Directory, supervisor.Request{Action: action, ID: id, DeviceID: device})
		if e != nil && action == "cancel" {
			if prior, read := supervisor.Query(ctx, c.Directory, id); read == nil && prior.Legacy {
				return supervisor.CancelLegacy(ctx, c.Directory, id, device)
			}
		}
		return op, e
	}}
}

func (u workerUpgradeClient) update(ctx context.Context, client *protocol.Client, candidate *domain.WorkerBuild, explicit bool, policy string) (*WorkerService, error) {
	current := u.knownBuild
	if client != nil {
		current = client.Worker
	}
	repairing := false
	if explicit && u.repair != nil && u.control != nil {
		if actual, e := u.control(ctx, "status", ""); e == nil && actual.Phase == "blocked" && actual.DeviceID == u.device {
			repairing = true
		}
	}
	r, err := u.load()
	if err != nil {
		return nil, err
	}
	if r != nil {
		if !repairing && !r.Rejected && (r.Operation == nil || !r.Operation.Terminal()) {
			return u.observe(ctx, client, candidate)
		}
		if !repairing && (r.Operation != nil && r.Operation.Phase == "blocked" || !explicit && (candidate == nil || candidate.BuildID == r.Input.BuildID)) {
			return upgradeService(current, candidate, r), nil
		}
	}
	if candidate == nil || current == nil && u.legacy == nil {
		return nil, errors.New("执行服务版本未确认")
	}
	r = &workerUpgradeRecord{DeviceID: u.device, Input: domain.WorkerReplacement{ID: domain.NewID(), ExpectedBuildID: func() string {
		if current != nil {
			return current.BuildID
		}
		return ""
	}(), BuildID: candidate.BuildID, Explicit: explicit, Policy: policy}}
	if err = u.save(r); err != nil {
		return nil, err
	}
	var result domain.WorkerUpdateResult
	if repairing {
		var op supervisor.Operation
		op, err = u.repair(ctx, r.Input)
		result = domain.WorkerUpdateResult{Ready: err == nil, OperationID: op.Input.ID, Phase: op.Phase, Reason: op.Error}
	} else if (client == nil || current.Lifecycle != 1) && u.legacy != nil {
		var op supervisor.Operation
		op, err = u.legacy(ctx, r.Input)
		result = domain.WorkerUpdateResult{Ready: err == nil, OperationID: op.Input.ID, Phase: op.Phase, Reason: op.Error}
	} else {
		err = client.Call(ctx, "worker.update", r.Input, &result)
	}
	if err != nil {
		var refused *protocol.RemoteError
		if errors.As(err, &refused) && refused.Code == "notSent" {
			r.Rejected = true
			r.Error = err.Error()
			err = errors.Join(err, u.save(r))
		}
		return upgradeService(current, candidate, r), err
	}
	if result.OperationID != r.Input.ID {
		var active supervisor.Operation
		var e error
		if client != nil && current.Lifecycle == 1 {
			e = client.Call(ctx, "worker.update.status", struct {
				ID string `json:"id"`
			}{result.OperationID}, &active)
		} else {
			active, e = u.control(ctx, "status", result.OperationID)
		}
		if e != nil {
			return upgradeService(current, candidate, r), e
		}
		if active.DeviceID != u.device || active.Input.ExpectedBuildID != r.Input.ExpectedBuildID || active.Input.BuildID != r.Input.BuildID || active.Input.Policy != r.Input.Policy {
			return upgradeService(current, candidate, r), errors.New("另一个更新与请求目标不一致")
		}
		r.JoinedID = active.Input.ID
		if e := u.save(r); e != nil {
			return upgradeService(current, candidate, r), e
		}
		r = &workerUpgradeRecord{DeviceID: u.device, Input: active.Input, Operation: &active}
		if e := u.save(r); e != nil {
			return upgradeService(current, candidate, r), e
		}
	}
	if result.OperationID != r.Input.ID {
		return upgradeService(current, candidate, r), errors.New("更新接纳回执标识不一致；只查询原操作")
	}
	return u.observe(ctx, client, candidate)
}

func (c *Controller) startLegacyWorkerUpgrade(ctx context.Context, device string, in domain.WorkerReplacement) (supervisor.Operation, error) {
	return c.runWorkerInstaller(ctx, device, in, false)
}
func (c *Controller) runWorkerInstaller(ctx context.Context, device string, in domain.WorkerReplacement, repair bool) (supervisor.Operation, error) {
	if ssh := c.currentSSH(); ssh != nil {
		if repair {
			return ssh.RepairWorkerService(ctx, c.WorkerBinary, device, in)
		}
		return ssh.StartLegacyWorkerUpgrade(ctx, c.WorkerBinary, device, in)
	}
	build, err := transport.InspectWorker(ctx, c.WorkerBinary)
	if err != nil {
		return supervisor.Operation{}, &protocol.RemoteError{Code: "notSent", Message: err.Error()}
	}
	if build.Lifecycle != 1 {
		return supervisor.Operation{}, &protocol.RemoteError{Code: "notSent", Message: "当前应用未包含新版服务安装程序"}
	}
	raw, _ := json.Marshal(in)
	mode := []string{"--service-migrate", "start"}
	if repair {
		mode = []string{"--service-control", "repair"}
	}
	args := append([]string{"--state-dir", c.Directory}, mode...)
	args = append(args, "--service-device", device)
	cmd := exec.CommandContext(ctx, c.WorkerBinary, args...)
	cmd.Stdin = bytes.NewReader(raw)
	data, err := cmd.Output()
	if err != nil {
		return supervisor.Operation{}, err
	}
	var reply struct {
		Operation   supervisor.Operation `json:"operation"`
		Error, Code string
	}
	if err = json.Unmarshal(data, &reply); err != nil {
		return reply.Operation, err
	}
	if reply.Error != "" {
		return reply.Operation, &protocol.RemoteError{Code: reply.Code, Message: reply.Error}
	}
	return reply.Operation, nil
}
