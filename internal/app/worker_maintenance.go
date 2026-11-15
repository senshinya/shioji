package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

// Only the receipt identity is persisted. Reconnection never replays an action.
type WorkerMaintenanceClient struct {
	Diagnosis       *domain.WorkerUpdateDiagnosis
	Operation       *domain.WorkerMaintenance
	PendingID       string
	Busy, QueryOnly bool
	Error           string
	request         uint64
}

func (m *Model) maintenanceDevice(key string) (*DeviceState, error) {
	s := m.DeviceStates[key]
	if s == nil || !s.Connected || s.ConnectionEpoch == "" || s.Snapshot.DeviceID == "" {
		return nil, errors.New("请先重新连接设备")
	}
	if s.WorkerService == nil || s.WorkerService.Build == nil || !s.WorkerService.Build.Maintenance {
		return nil, errors.New("此执行服务尚不支持后台环境处理")
	}
	return s, nil
}
func (m *Model) MaintenanceClient(deviceID string) *WorkerMaintenanceClient {
	if m.WorkerMaintenanceIDs == nil {
		m.WorkerMaintenanceIDs = map[string]string{}
	}
	if m.WorkerMaintenanceClients == nil {
		m.WorkerMaintenanceClients = map[string]*WorkerMaintenanceClient{}
	}
	c := m.WorkerMaintenanceClients[deviceID]
	if c == nil {
		c = &WorkerMaintenanceClient{PendingID: m.WorkerMaintenanceIDs[deviceID]}
		c.QueryOnly = c.PendingID != ""
		m.WorkerMaintenanceClients[deviceID] = c
	}
	return c
}
func (m *Model) maintenanceJob(key, method string, input any, mutating bool) (Job, error) {
	s, err := m.maintenanceDevice(key)
	if err != nil {
		return Job{}, err
	}
	id, epoch := s.Snapshot.DeviceID, s.ConnectionEpoch
	c := m.MaintenanceClient(id)
	if c.Busy {
		return Job{}, errors.New("正在查询处理结果")
	}
	c.Busy = true
	c.Error = ""
	c.request++
	request := c.request
	job := Job{ConnectionID: key, ConnectionEpoch: epoch, Method: method, Input: input}
	job.Apply = func(model *Model, b json.RawMessage, err error) {
		current := model.MaintenanceClient(id)
		if current != c || current.request != request {
			return
		}
		current.Busy = false
		state := model.DeviceStates[key]
		if state == nil || state.Snapshot.DeviceID != id || state.ConnectionEpoch != epoch {
			current.Error = "连接已变化，请重新查询处理结果"
			if mutating {
				current.QueryOnly = true
			}
			return
		}
		diagnosis := method == "$worker.maintenance.inspect" || method == "worker.update.diagnose"
		if err == nil {
			if diagnosis {
				var d domain.WorkerUpdateDiagnosis
				if json.Unmarshal(b, &d) != nil || d.DeviceID != id || state.WorkerService == nil || state.WorkerService.Build == nil || d.BuildID != state.WorkerService.Build.BuildID {
					err = errors.New("诊断回执不属于当前设备或构建")
				} else {
					current.Diagnosis = &d
					if method == "$worker.maintenance.inspect" && (d.Operation == nil || !d.Operation.Phase.Active()) {
						current.Operation = nil
						current.PendingID = ""
						current.QueryOnly = false
					} else if d.Operation != nil {
						current.Operation = d.Operation
						current.PendingID = d.Operation.ID
						current.QueryOnly = false
					}
				}
			} else {
				var op domain.WorkerMaintenance
				if json.Unmarshal(b, &op) != nil || op.DeviceID != id || op.ID == "" || op.ID != current.PendingID {
					err = errors.New("处理回执与原操作不一致")
				} else {
					current.Operation = &op
					current.QueryOnly = false
				}
			}
		}
		if err != nil {
			current.Error = err.Error()
			var remote *protocol.RemoteError
			definite := errors.As(err, &remote) && (remote.Code == "notSent" || remote.Code == "notFound")
			if mutating {
				current.QueryOnly = !definite
			}
			if definite && current.Operation == nil {
				current.PendingID = ""
				current.QueryOnly = false
			}
			if !mutating && !diagnosis && definite && remote.Code == "notFound" {
				current.PendingID = ""
				current.QueryOnly = false
			}
		}
		if model.WorkerMaintenanceIDs == nil {
			model.WorkerMaintenanceIDs = map[string]string{}
		}
		if current.PendingID == "" {
			delete(model.WorkerMaintenanceIDs, id)
		} else {
			model.WorkerMaintenanceIDs[id] = current.PendingID
		}
	}
	return job, nil
}
func (m *Model) InspectWorkerMaintenance(key string, stage bool) (Job, error) {
	s, err := m.maintenanceDevice(key)
	if err != nil {
		return Job{}, err
	}
	c := m.MaintenanceClient(s.Snapshot.DeviceID)
	if c.QueryOnly && c.PendingID != "" {
		return m.QueryWorkerMaintenance(key)
	}
	method := "worker.update.diagnose"
	if stage {
		method = "$worker.maintenance.inspect"
	}
	return m.maintenanceJob(key, method, nil, false)
}
func (m *Model) QueryWorkerMaintenance(key string) (Job, error) {
	s, err := m.maintenanceDevice(key)
	if err != nil {
		return Job{}, err
	}
	c := m.MaintenanceClient(s.Snapshot.DeviceID)
	if c.PendingID == "" {
		return m.InspectWorkerMaintenance(key, false)
	}
	return m.maintenanceJob(key, "worker.maintenance.get", protocol.WorkerMaintenanceQuery{DeviceID: s.Snapshot.DeviceID, ID: c.PendingID}, false)
}
func (m *Model) ApplyWorkerMaintenance(key string, targets []domain.WorkerMaintenanceTarget, force bool) (Job, error) {
	s, err := m.maintenanceDevice(key)
	if err != nil {
		return Job{}, err
	}
	c := m.MaintenanceClient(s.Snapshot.DeviceID)
	if c.Busy || c.QueryOnly || len(targets) == 0 {
		return Job{}, errors.New("请先查询结果并选择后台环境")
	}
	in := domain.WorkerMaintenanceInput{ID: domain.NewID(), ActionID: domain.NewID(), Action: "stop", DeviceID: s.Snapshot.DeviceID, ExpectedBuildID: s.WorkerService.Build.BuildID, Targets: targets}
	if force {
		op := c.Operation
		if op == nil || op.Phase != domain.WorkerMaintenanceNeedsForce {
			return Job{}, errors.New("当前没有等待强制确认的处理")
		}
		in.ID = op.ID
		in.Action = "force"
		in.Revision = op.Revision
		in.ExpectedBuildID = op.ExpectedBuildID
		in.BuildID = op.BuildID
		for _, target := range targets {
			found := false
			for _, r := range op.Targets {
				expected := r.Target
				expected.ProcessRef = r.ProcessRef
				found = found || (r.State == "awaitingForce" && expected == target)
			}
			if !found {
				return Job{}, errors.New("强制结束对象已变化，请重新查询")
			}
		}
	} else {
		if c.Operation != nil && c.Operation.Phase.Active() {
			return Job{}, errors.New("设备已有处理，请查询原操作")
		}
		if c.Diagnosis == nil || c.Diagnosis.BuildID != in.ExpectedBuildID || s.WorkerService.Available == nil {
			return Job{}, errors.New("请重新检查候选更新")
		}
		in.BuildID = s.WorkerService.Available.BuildID
		for _, target := range targets {
			found := false
			for _, b := range c.Diagnosis.Blockers {
				found = found || (b.Target != nil && *b.Target == target)
			}
			if !found {
				return Job{}, errors.New("选择已过期，请重新检查")
			}
		}
	}
	c.PendingID = in.ID
	if !force {
		c.Operation = nil
	}
	if m.WorkerMaintenanceIDs == nil {
		m.WorkerMaintenanceIDs = map[string]string{}
	}
	m.WorkerMaintenanceIDs[s.Snapshot.DeviceID] = in.ID
	return m.maintenanceJob(key, "worker.maintenance.apply", in, true)
}
func (m *Model) CancelWorkerMaintenance(key string) (Job, error) {
	s, err := m.maintenanceDevice(key)
	if err != nil {
		return Job{}, err
	}
	c := m.MaintenanceClient(s.Snapshot.DeviceID)
	op := c.Operation
	if c.QueryOnly || op == nil || !op.Phase.Active() || op.Phase == domain.WorkerMaintenanceUpdating {
		return Job{}, fmt.Errorf("请先查询当前处理状态")
	}
	in := domain.WorkerMaintenanceInput{ID: op.ID, ActionID: domain.NewID(), Action: "cancel", DeviceID: op.DeviceID, Revision: op.Revision}
	return m.maintenanceJob(key, "worker.maintenance.cancel", in, true)
}
