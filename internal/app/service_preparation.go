package app

import (
	"context"
	"encoding/json"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"time"
)

type ServicePreparationPhase string

const (
	ServiceStaging       ServicePreparationPhase = "staging"
	ServiceReadyToUpdate ServicePreparationPhase = "readyToUpdate"
	ServiceWaiting       ServicePreparationPhase = "waiting"
	ServiceDispatching   ServicePreparationPhase = "dispatching"
	ServiceReconnecting  ServicePreparationPhase = "reconnecting"
	ServiceObserving     ServicePreparationPhase = "observing"
	ServiceInterrupted   ServicePreparationPhase = "interrupted"
	ServiceFailed        ServicePreparationPhase = "failed"
	ServiceReady         ServicePreparationPhase = "ready"
)

// This is a client intent, not an admission receipt. The owning worker retains
// exclusive authority to fence execution, back up its database and replace itself.
type ServicePreparation struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	SSH         transport.SSH           `json:"ssh"`
	Info        transport.WorkerInfo    `json:"info"`
	Candidate   domain.WorkerBuild      `json:"candidate"`
	Phase       ServicePreparationPhase `json:"phase"`
	Message     string                  `json:"message,omitempty"`
	RequestedAt time.Time               `json:"requestedAt,omitempty"`
}
type PrepareService struct {
	Action      string
	Preparation ServicePreparation
}
type ServicePreparationResult struct {
	Preparation ServicePreparation
	Check       *DeviceSetupCheck `json:",omitempty"`
}

func (p ServicePreparation) NextAction() string {
	switch p.Phase {
	case ServiceStaging:
		return "stage"
	case ServiceReadyToUpdate, ServiceWaiting:
		return "update"
	case ServiceDispatching, ServiceReconnecting, ServiceObserving:
		return "observe"
	}
	return ""
}

func planServicePreparation(info transport.WorkerInfo, candidate domain.WorkerBuild) (*ServicePreparation, error) {
	current := info.Build
	required := info.Minor < 16
	if current == nil || current.BuildID == "" || !current.SafeUpdate {
		if required {
			return nil, errors.New("设备服务未提供安全更新能力，请先更新设备服务")
		}
		return nil, nil
	}
	if current.BuildID == candidate.BuildID {
		return nil, nil
	}
	if current.BuiltAt > 0 && candidate.BuiltAt > 0 && current.BuiltAt > candidate.BuiltAt {
		if required {
			return nil, errors.New("设备正在使用较新服务，但未提供所需的 Agent 配置能力")
		}
		return nil, nil
	}
	if candidate.BuildID == "" || !candidate.SafeUpdate || candidate.Schema < current.Schema {
		return nil, errors.New("随应用提供的服务无法安全更新当前设备")
	}
	if !required && (current.BuiltAt == 0 || candidate.BuiltAt <= current.BuiltAt) {
		return nil, nil
	}
	return &ServicePreparation{ID: domain.NewID(), Info: info, Candidate: candidate, Phase: ServiceStaging}, nil
}

// A successful handshake alone is insufficient: the accepted target build (or
// a compatible newer replacement) must actually be running in the same directory.
func (p ServicePreparation) ReadyAt(info transport.WorkerInfo) bool {
	if info.DeviceID != p.Info.DeviceID || info.Directory != p.Info.Directory || info.Minor < 16 || info.Build == nil {
		return false
	}
	actual := info.Build
	return actual.BuildID == p.Candidate.BuildID || actual.BuildID != p.Info.Build.BuildID && actual.BuiltAt > 0 && p.Candidate.BuiltAt > 0 && actual.BuiltAt >= p.Candidate.BuiltAt
}

func updatePreparedService(ctx context.Context, c *protocol.Client, p ServicePreparation) (ServicePreparation, error) {
	if c.DeviceID != p.Info.DeviceID || c.Worker == nil || p.Info.Build == nil || c.Worker.BuildID != p.Info.Build.BuildID || !c.Worker.SafeUpdate {
		return p, &protocol.RemoteError{Code: "notSent", Message: "设备服务身份或版本已改变，请重新检查"}
	}
	var result domain.WorkerUpdateResult
	err := c.Call(ctx, "worker.update", domain.WorkerReplacement{ExpectedBuildID: p.Info.Build.BuildID, BuildID: p.Candidate.BuildID}, &result)
	if err != nil {
		var remote *protocol.RemoteError
		if errors.As(err, &remote) {
			return p, err
		}
		// Bytes may have reached the old owner. Never turn a lost receipt into
		// permission to retry; even seeing the old build again is not that proof.
		p.Phase = ServiceObserving
		p.Message = ""
		return p, nil
	}
	if !result.Ready {
		p.Phase = ServiceWaiting
		p.Message = result.Reason
	} else {
		p.Phase = ServiceReconnecting
		p.Message = ""
	}
	return p, nil
}

func (c *Controller) prepareService(ctx context.Context, in PrepareService) (ServicePreparationResult, error) {
	p := in.Preparation
	result := ServicePreparationResult{Preparation: p}
	if p.ID == "" || p.Info.DeviceID == "" || p.Info.Build == nil || p.Info.Build.BuildID == "" || p.Candidate.BuildID == "" {
		return result, errors.New("设备服务准备记录不完整")
	}
	ssh, routeErr := c.TailnetSSH(p.SSH)
	if routeErr != nil {
		return result, routeErr
	}
	ssh.Worker = p.Info.Worker
	ssh.StateDirectory = p.Info.Directory
	conn, err := ssh.Dial(ctx)
	if err != nil {
		return result, &protocol.RemoteError{Code: "notSent", Message: err.Error()}
	}
	client, err := protocol.Connect(ctx, conn, p.Info.DeviceID)
	if err != nil {
		conn.Close()
		return result, &protocol.RemoteError{Code: "notSent", Message: err.Error()}
	}
	defer client.Close()
	switch in.Action {
	case "stage":
		if p.Phase != ServiceStaging {
			return result, errors.New("更新文件准备状态已改变")
		}
		if client.Worker == nil || client.Worker.BuildID != p.Info.Build.BuildID {
			return result, &protocol.RemoteError{Code: "notSent", Message: "服务版本已改变，请重新检查设备"}
		}
		candidate, err := ssh.StageWorker(ctx, c.fleet.workerBinary)
		if err != nil {
			return result, &protocol.RemoteError{Code: "notSent", Message: err.Error()}
		}
		if candidate.BuildID != p.Candidate.BuildID {
			return result, &protocol.RemoteError{Code: "notSent", Message: "更新程序已变化，请重新检查设备"}
		}
		result.Preparation.Phase = ServiceReadyToUpdate
		return result, nil
	case "update":
		if p.Phase != ServiceDispatching {
			return result, errors.New("更新请求未保存为待发送状态")
		}
		result.Preparation, err = updatePreparedService(ctx, client, p)
		return result, err
	case "observe":
		if p.NextAction() != "observe" {
			return result, errors.New("此状态不能核对更新")
		}
		if client.Worker == nil {
			return result, errors.New("设备未报告当前服务版本")
		}
		actual := client.Worker
		observed := p.Info
		observed.Build = actual
		observed.Minor = client.Minor
		if p.ReadyAt(observed) {
			var ready domain.Readiness
			if err = client.Call(ctx, "runtime.catalog", nil, &ready); err != nil {
				return result, err
			}
			result.Preparation.Info.Build = actual
			result.Preparation.Info.Minor = client.Minor
			result.Preparation.Phase = ServiceReady
			result.Check = &DeviceSetupCheck{Info: result.Preparation.Info, Catalog: ready.Harnesses}
			return result, nil
		}
		result.Preparation.Phase = ServiceObserving
		if !p.RequestedAt.IsZero() && time.Since(p.RequestedAt) > 30*time.Second {
			result.Preparation.Phase = ServiceInterrupted
		}
		return result, nil
	default:
		return result, errors.New("无效的服务准备操作")
	}
}

func (c *Controller) runServicePreparationJob(ctx context.Context, job Job) {
	in, ok := job.Input.(PrepareService)
	if !ok {
		c.postContext(ctx, func(m *Model) {
			if job.Apply != nil {
				job.Apply(m, nil, errors.New("无效的服务准备请求"))
			}
		})
		return
	}
	go func() {
		callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		var out ServicePreparationResult
		var err error
		if job.Prepare != nil {
			if e := job.Prepare(); e != nil {
				err = &protocol.RemoteError{Code: "notSent", Message: "无法保存准备进度，操作未发送：" + e.Error()}
			}
		}
		if err == nil {
			out, err = c.prepareService(callCtx, in)
		}
		body, _ := json.Marshal(out)
		if job.Apply != nil {
			c.postContext(ctx, func(m *Model) { job.Apply(m, body, err) })
		}
	}()
}
