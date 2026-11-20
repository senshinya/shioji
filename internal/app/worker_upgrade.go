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
	RepairOf        string `json:",omitempty"`
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
		if r.RepairOf != "" {
			state.OperationID, state.OperationPhase = r.RepairOf, "blocked"
		}
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
	case "migrating":
		state.Phase = WorkerUpdating
		state.Message = "正在备份并迁移设备数据库"
	case "applied":
		// A later update from a different client may have replaced this build.
		// Preserve the old receipt and compare the current build with the candidate.
		if current == nil {
			state.Phase = WorkerUpdating
			state.Message = "更新已提交，正在重新连接"
		}
	case "rolled-back":
		state.Phase = WorkerUpdateFailed
		state.Message = "候选版本未就绪，已恢复原版本，可重试更新"
		if op.Input.SchemaMigration && op.Error != "" {
			state.Message = "已恢复原数据库。迁移失败：" + op.Error
		}
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
		if r.Input.SchemaMigration && u.control != nil {
			return u.control(ctx, "status", id)
		}
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
	if err != nil && r.RepairOf == "" {
		// A second client may have joined the same target before its reply was
		// lost. Following an existing matching receipt never resends an update.
		if active, e := query(""); e == nil && active.Input.ID != "" && active.DeviceID == u.device && active.Input.ExpectedBuildID == r.Input.ExpectedBuildID && active.Input.BuildID == r.Input.BuildID && active.Input.Policy == r.Input.Policy && active.Input.SchemaMigration == r.Input.SchemaMigration {
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

// A disconnected remote uses the fixed service over SSH. Admission and
// candidate verification still occur on the owning device.
func (u workerUpgradeClient) retryDetached(ctx context.Context, operation string, candidate *domain.WorkerBuild) (*WorkerService, error) {
	prior, err := u.load()
	if err != nil {
		return nil, err
	}
	if prior != nil && !prior.Rejected && prior.Input.ID != operation && (prior.Operation == nil || !prior.Operation.Terminal()) {
		return u.observe(ctx, nil, candidate)
	}
	state := upgradeService(u.knownBuild, candidate, prior)
	active, err := u.control(ctx, "status", "")
	if err != nil {
		return state, err
	}
	if active.DeviceID != u.device {
		return state, errors.New("执行服务回执的设备身份已变化")
	}
	if active.Phase != "blocked" {
		return u.observe(ctx, nil, candidate)
	}
	if operation == "" || active.Input.ID != operation || active.Input.BuildID == "" {
		return state, errors.New("被阻塞的更新已变化，请重新检查执行服务")
	}
	input := domain.WorkerReplacement{
		ID: domain.NewID(), BuildID: active.Input.BuildID, ExpectedBuildID: active.Input.ExpectedBuildID,
		Explicit: true, Policy: "when-idle",
	}
	record := &workerUpgradeRecord{DeviceID: u.device, Input: input, RepairOf: operation}
	if err = u.save(record); err != nil {
		return state, err
	}
	op, err := u.repair(ctx, input)
	if err != nil {
		var refused *protocol.RemoteError
		if errors.As(err, &refused) && refused.Code == "notSent" {
			record.Rejected, record.Error = true, err.Error()
			err = errors.Join(err, u.save(record))
		}
		return upgradeService(u.knownBuild, candidate, record), err
	}
	if op.Input != input || op.DeviceID != u.device {
		return upgradeService(u.knownBuild, candidate, record), errors.New("修复回执不属于原设备和操作")
	}
	record.Operation = &op
	return upgradeService(u.knownBuild, candidate, record), u.save(record)
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
		if action == "cancel" {
			if prior, err := supervisor.Query(ctx, c.Directory, id); err == nil && prior.Legacy && prior.Input.SchemaMigration {
				return supervisor.CancelLegacy(ctx, c.Directory, id, device)
			}
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

func (u workerUpgradeClient) update(ctx context.Context, client *protocol.Client, candidate *domain.WorkerBuild, explicit bool, policy string, migrate bool) (*WorkerService, error) {
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
	if !repairing {
		crossSchema := current != nil && current.Schema > 0 && candidate.Schema > 0 && current.Schema != candidate.Schema
		switch {
		case crossSchema && (!migrate || !explicit || !candidate.MigratesSchema(current.Schema) || policy != "when-idle"):
			state := newWorkerService(current, candidate)
			state.Phase, state.Message = WorkerUpdateFailed, "此更新需要确认设备数据库迁移，或使用兼容版本"
			return state, nil
		case migrate && !crossSchema:
			state := newWorkerService(current, candidate)
			state.Phase, state.Message = WorkerUpdateFailed, "执行服务版本已变化，请重新检查"
			return state, nil
		}
	}
	r = &workerUpgradeRecord{DeviceID: u.device, Input: domain.WorkerReplacement{ID: domain.NewID(), ExpectedBuildID: func() string {
		if current != nil {
			return current.BuildID
		}
		return ""
	}(), BuildID: candidate.BuildID, Explicit: explicit, Policy: policy, SchemaMigration: migrate}}
	if err = u.save(r); err != nil {
		return nil, err
	}
	var result domain.WorkerUpdateResult
	if repairing {
		var op supervisor.Operation
		op, err = u.repair(ctx, r.Input)
		result = domain.WorkerUpdateResult{Ready: err == nil, OperationID: op.Input.ID, Phase: op.Phase, Reason: op.Error}
	} else if (migrate || client == nil || current.Lifecycle != 1) && u.legacy != nil {
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
		if active.DeviceID != u.device || active.Input.ExpectedBuildID != r.Input.ExpectedBuildID || active.Input.BuildID != r.Input.BuildID || active.Input.Policy != r.Input.Policy || active.Input.SchemaMigration != r.Input.SchemaMigration {
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
	if in.SchemaMigration {
		mode = []string{"--service-schema-upgrade", "start"}
	} else if repair {
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
