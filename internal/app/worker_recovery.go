package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

type WorkerCloseSelection struct {
	Targets                     []domain.WorkerMaintenanceTarget
	ProcessPreviewRef           string
	ProcessRefs                 []string
	Force, Update, RiskAccepted bool
}

func (m *Model) CloseWorkerEnvironments(key string, selection WorkerCloseSelection) (Job, error) {
	s, err := m.maintenanceDevice(key)
	if err != nil {
		return Job{}, err
	}
	if !s.WorkerService.Build.ForceRecovery {
		return Job{}, errors.New("此执行服务需要独立恢复，请使用强制重启执行服务")
	}
	c := m.MaintenanceClient(s.Snapshot.DeviceID)
	if c.Busy || c.QueryOnly || len(selection.Targets)+len(selection.ProcessRefs) == 0 {
		return Job{}, errors.New("请先查询原操作并选择要关闭的环境")
	}
	in := domain.WorkerMaintenanceInput{ID: domain.NewID(), ActionID: domain.NewID(), Action: "close", DeviceID: s.Snapshot.DeviceID, ExpectedBuildID: s.WorkerService.Build.BuildID, Standalone: !selection.Update, RiskAccepted: selection.RiskAccepted, Targets: slices.Clone(selection.Targets), ProcessPreviewRef: selection.ProcessPreviewRef, ProcessRefs: slices.Clone(selection.ProcessRefs)}
	if selection.Force {
		in.Action = "force-close"
	}
	if op := c.Operation; op != nil && op.Phase.Active() {
		if op.Phase != domain.WorkerMaintenanceNeedsForce || !selection.Force {
			return Job{}, errors.New("设备已有处理，请查询或取消原操作")
		}
		in.ID = op.ID
		in.Action = "force"
		in.Revision = op.Revision
		in.ExpectedBuildID = op.ExpectedBuildID
		in.BuildID = op.BuildID
		in.Standalone = op.Standalone
		in.ProcessPreviewRef = op.ProcessPreviewRef
		for _, target := range in.Targets {
			found := false
			for _, r := range op.Targets {
				expected := r.Target
				expected.ProcessRef = r.ProcessRef
				found = found || r.State == "awaitingForce" && expected == target
			}
			if !found {
				return Job{}, errors.New("剩余环境已变化，请重新确认")
			}
		}
		for _, ref := range in.ProcessRefs {
			found := false
			for _, p := range op.Processes {
				found = found || p.Process.Ref == ref && p.State == "awaitingForce"
			}
			if !found {
				return Job{}, errors.New("剩余进程已变化，请重新确认")
			}
		}
	} else {
		if c.Diagnosis == nil || c.Diagnosis.BuildID != in.ExpectedBuildID {
			return Job{}, errors.New("请先重新检查运行环境")
		}
		for _, target := range in.Targets {
			found := false
			for _, b := range c.Diagnosis.Environments {
				found = found || b.ForceTarget != nil && *b.ForceTarget == target
			}
			if !found {
				return Job{}, errors.New("所选环境预览已过期，请重新检查")
			}
		}
		if selection.Update {
			if s.WorkerService.Available == nil {
				return Job{}, errors.New("当前没有可用更新，可取消更新选项后单独关闭")
			}
			in.BuildID = s.WorkerService.Available.BuildID
		}
		c.Operation = nil
	}
	c.PendingID = in.ID
	if m.WorkerMaintenanceIDs == nil {
		m.WorkerMaintenanceIDs = map[string]string{}
	}
	m.WorkerMaintenanceIDs[in.DeviceID] = in.ID
	job, err := m.maintenanceJob(key, "worker.maintenance.apply", in, true)
	if err != nil {
		return job, err
	}
	apply := job.Apply
	job.Apply = func(model *Model, b json.RawMessage, err error) {
		apply(model, b, err)
		client := model.MaintenanceClient(in.DeviceID)
		if op := client.Operation; op != nil {
			if op.Phase == domain.WorkerMaintenanceNeedsForce || op.Phase == domain.WorkerMaintenanceCompleted || op.Phase == domain.WorkerMaintenanceFailed {
				model.queueRecoveryNotice(key, op.ID+":"+string(op.Phase), op.Message)
			}
		}
	}
	return job, nil
}

type WorkerRecoveryClient struct {
	Preview         *domain.WorkerRecoveryPreview
	Operation       *domain.WorkerRecoveryOperation
	PendingID       string
	Busy, QueryOnly bool
	Error           string
	request         uint64
}

type WorkerRecoveryAction struct {
	Action       string
	ConnectionID string
	SSH          *transport.SSH
	Input        domain.WorkerRecoveryInput
}

func (m *Model) RecoveryClient(id string) *WorkerRecoveryClient {
	if m.WorkerRecoveryClients == nil {
		m.WorkerRecoveryClients = map[string]*WorkerRecoveryClient{}
	}
	c := m.WorkerRecoveryClients[id]
	if c == nil {
		c = &WorkerRecoveryClient{PendingID: m.WorkerRecoveryIDs[id]}
		c.QueryOnly = c.PendingID != ""
		m.WorkerRecoveryClients[id] = c
	}
	return c
}

func (m *Model) recoveryDevice(key string) (string, *transport.SSH, error) {
	if key == "" || key == "local" {
		id := m.LocalDeviceID
		if s := m.DeviceStates["local"]; s != nil && s.Snapshot.DeviceID != "" {
			id = s.Snapshot.DeviceID
		}
		if id == "" {
			return "", nil, errors.New("请先确认本机执行设备身份")
		}
		return id, nil, nil
	}
	for _, c := range m.Connections {
		if c.ID == key && c.DeviceID != "" {
			ssh := c.ActiveSSH()
			return c.DeviceID, &ssh, nil
		}
	}
	return "", nil, errors.New("设备连接信息已移除，请重新连接")
}

func (m *Model) WorkerRecoveryJob(key, action string, in domain.WorkerRecoveryInput) (Job, error) {
	id, ssh, err := m.recoveryDevice(key)
	if err != nil {
		return Job{}, err
	}
	c := m.RecoveryClient(id)
	if c.Busy {
		return Job{}, errors.New("正在读取设备恢复结果")
	}
	if c.QueryOnly && action == "inspect" {
		action = "status"
	}
	if action == "start" {
		if c.QueryOnly || c.Operation != nil && c.Operation.Active() {
			return Job{}, errors.New("请先查询原恢复操作，不会重复强制关闭")
		}
		if in.DeviceID != id || in.ID == "" || !in.RiskAccepted || c.Preview == nil || in.PreviewRef != c.Preview.Ref {
			return Job{}, errors.New("请重新检查并确认恢复范围与风险")
		}
		c.PendingID = in.ID
		c.QueryOnly = true
		c.Operation = nil
	}
	in.DeviceID = id
	if action == "status" || action == "cancel" {
		in.ID = c.PendingID
		if in.ID == "" {
			return Job{}, errors.New("没有待查询的恢复操作")
		}
	}
	if action != "inspect" && action != "start" && action != "status" && action != "cancel" {
		return Job{}, errors.New("未知恢复操作")
	}
	if action == "inspect" {
		c.Preview = nil
	}
	c.Busy = true
	c.Error = ""
	c.request++
	request := c.request
	if m.WorkerRecoveryIDs == nil {
		m.WorkerRecoveryIDs = map[string]string{}
	}
	if c.PendingID != "" {
		m.WorkerRecoveryIDs[id] = c.PendingID
	}
	job := Job{ConnectionID: key, Method: "$worker.recovery", Input: WorkerRecoveryAction{Action: action, ConnectionID: key, SSH: ssh, Input: in}}
	job.Apply = func(model *Model, b json.RawMessage, err error) {
		current := model.RecoveryClient(id)
		if current != c || current.request != request {
			return
		}
		current.Busy = false
		actual, _, identityErr := model.recoveryDevice(key)
		if identityErr != nil || actual != id {
			current.Error = "设备身份或连接信息已变化，请重新检查"
			return
		}
		if s := model.DeviceStates[key]; s != nil && s.Snapshot.DeviceID != "" && s.Snapshot.DeviceID != id {
			current.Error = "设备身份已变化，旧恢复回执已忽略"
			return
		}
		if err == nil {
			if action == "inspect" {
				var preview domain.WorkerRecoveryPreview
				if json.Unmarshal(b, &preview) != nil || preview.DeviceID != id || preview.Ref == "" {
					err = errors.New("恢复预览不属于当前设备")
				} else {
					current.Preview = &preview
					current.Operation = nil
					current.QueryOnly = false
					if preview.Operation != nil && preview.Operation.Active() {
						current.Operation = preview.Operation
						current.PendingID = preview.Operation.ID
						current.QueryOnly = preview.Operation.Active()
					}
					if len(preview.Warnings) > 0 {
						model.queueRecoveryNotice(key, "preview:"+preview.DeviceID, "执行服务需要处理，可检查并强制关闭后台环境")
					}
				}
			} else {
				var op domain.WorkerRecoveryOperation
				if json.Unmarshal(b, &op) != nil || op.DeviceID != id || op.ID == "" || (op.ID != current.PendingID && !op.Conflict) {
					err = errors.New("恢复结果不属于原设备或操作")
				} else {
					current.Operation = &op
					current.PendingID = op.ID
					current.QueryOnly = false
					if !op.Active() {
						model.queueRecoveryNotice(key, op.ID+":"+op.Phase, op.Message)
					}
				}
			}
		}
		if err != nil {
			current.Error = err.Error()
			var remote *protocol.RemoteError
			if errors.As(err, &remote) && (remote.Code == "notFound" || remote.Code == "notSent") {
				current.PendingID = ""
				current.QueryOnly = false
			} else if action == "start" {
				current.QueryOnly = true
			}
		}
		if current.PendingID == "" {
			delete(model.WorkerRecoveryIDs, id)
		} else {
			model.WorkerRecoveryIDs[id] = current.PendingID
		}
	}
	return job, nil
}

func (c *Controller) runWorkerRecoveryJob(ctx context.Context, job Job) {
	go func() {
		in, ok := job.Input.(WorkerRecoveryAction)
		var result json.RawMessage
		var err error
		if !ok {
			err = errors.New("无效的设备恢复请求")
		}
		if err == nil && job.Prepare != nil {
			if e := job.Prepare(); e != nil {
				err = &protocol.RemoteError{Code: "notSent", Message: "无法保存草稿，恢复未发送：" + e.Error()}
			}
		}
		if err == nil {
			call, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			binary := c.WorkerBinary
			if c.fleet != nil {
				binary = c.fleet.workerBinary
			}
			if in.SSH != nil {
				ssh, e := c.TailnetSSH(*in.SSH)
				err = e
				if err == nil {
					result, err = ssh.WorkerRecovery(call, binary, in.Action, in.Input)
				}
			} else if in.ConnectionID == "" || in.ConnectionID == "local" {
				result, err = transport.RunWorkerRecovery(call, binary, c.Directory, in.Action, in.Input)
			} else {
				err = errors.New("设备连接信息已移除")
			}
		}
		if job.Apply != nil {
			c.post(func(m *Model) { job.Apply(m, result, err) })
		}
	}()
}

func (m *Model) queueRecoveryNotice(key, event, message string) {
	p := m.Preferences.Normalized().Notifications
	if !p.Enabled || !p.Attention {
		return
	}
	hash := sha256.Sum256([]byte(key + ":" + event))
	id := "recovery:" + hex.EncodeToString(hash[:])
	if slices.Contains(m.notificationSeen, id) {
		return
	}
	name := "此 Mac"
	for _, c := range m.Connections {
		if c.ID == key {
			name = c.Name
		}
	}
	if message == "" {
		message = "执行服务需要处理"
	}
	m.Notifications = append(m.Notifications, SessionNotification{ID: id, ConnectionID: key, Kind: "recovery", Title: name + " 的执行服务", Body: message})
	if len(m.Notifications) > 64 {
		m.Notifications = m.Notifications[len(m.Notifications)-64:]
	}
	m.notificationSeen = append(m.notificationSeen, id)
	if len(m.notificationSeen) > 256 {
		m.notificationSeen = m.notificationSeen[len(m.notificationSeen)-256:]
	}
}
