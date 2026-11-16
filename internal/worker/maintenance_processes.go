package worker

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
)

func (s *Server) maintenanceProcessSelection(in domain.WorkerMaintenanceInput) ([]domain.WorkerRecoveryResult, error) {
	if !in.RiskAccepted {
		return nil, errors.New("请明确确认所选进程的关闭风险")
	}
	var preview recoveryPreviewRecord
	if err := recoveryRead(s.config.Directory, "preview-"+in.ProcessPreviewRef, &preview); err != nil {
		return nil, errors.New("进程预览已失效，请重新检查")
	}
	if preview.Preview.DeviceID != s.store.DeviceID() || time.Now().After(preview.Preview.ExpiresAt) {
		return nil, errors.New("进程预览已过期，请重新确认")
	}
	selected := map[string]bool{}
	var out []domain.WorkerRecoveryResult
	for _, ref := range in.ProcessRefs {
		if selected[ref] {
			return nil, errors.New("进程选择重复")
		}
		selected[ref] = true
		found := false
		for _, p := range preview.Preview.Processes {
			if p.Ref != ref {
				continue
			}
			found = true
			if p.Protected || recoveryProtected(preview.Targets[ref]) {
				return nil, errors.New("恢复控制连接不能关闭")
			}
			if p.SessionID != "" {
				return nil, errors.New("该进程属于会话，请在确认中选择完整会话环境")
			}
			if managedexec.ProcessState(preview.Targets[ref]) != "running" {
				return nil, errors.New("所选进程身份已变化，请重新确认")
			}
			out = append(out, domain.WorkerRecoveryResult{Process: p, State: "stopping"})
		}
		if !found {
			return nil, errors.New("进程不属于本次预览")
		}
	}
	return out, nil
}

func (s *Server) stopMaintenanceProcesses(id string) {
	s.maintenance.Lock()
	defer s.maintenance.Unlock()
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	op, err := s.store.WorkerMaintenance(id)
	if err != nil || op.Phase != domain.WorkerMaintenanceStopping {
		return
	}
	var preview recoveryPreviewRecord
	if recoveryRead(s.config.Directory, "preview-"+op.ProcessPreviewRef, &preview) != nil {
		return
	}
	for i := range op.Processes {
		if op.Processes[i].State != "stopping" || op.Processes[i].Sent {
			continue
		}
		ref := op.Processes[i].Process.Ref
		op.Processes[i].Sent = true
		op, err = s.store.SaveWorkerMaintenance(op, op.Revision)
		if err != nil {
			return
		}
		exact, ok := preview.Targets[ref]
		if !ok {
			continue
		}
		sig := unix.SIGTERM
		if op.Force {
			sig = unix.SIGKILL
		}
		if managedexec.ProcessState(exact) == "running" {
			if e := managedexec.SignalExact(exact, sig); e != nil {
				op.Processes[i].State = "awaitingForce"
				op.Processes[i].Message = e.Error()
				op, err = s.store.SaveWorkerMaintenance(op, op.Revision)
				if err != nil {
					return
				}
			}
		}
	}
}

func (s *Server) observeMaintenanceProcesses(op *domain.WorkerMaintenance, now time.Time) (bool, error) {
	if len(op.Processes) == 0 {
		return true, nil
	}
	var preview recoveryPreviewRecord
	if err := recoveryRead(s.config.Directory, "preview-"+op.ProcessPreviewRef, &preview); err != nil {
		for i := range op.Processes {
			if op.Processes[i].State != "stopped" {
				op.Processes[i].State = "unknown"
				op.Processes[i].Message = "进程观察记录无法读取，可重新选择或恢复执行服务"
			}
		}
		return false, nil
	}
	all := true
	for i := range op.Processes {
		p := &op.Processes[i]
		if p.State == "stopped" {
			continue
		}
		exact, ok := preview.Targets[p.Process.Ref]
		if !ok {
			p.State = "unknown"
			p.Message = "原进程身份记录缺失"
			all = false
			continue
		}
		switch managedexec.ProcessState(exact) {
		case "exited", "changed":
			p.State = "stopped"
			p.Message = "原进程已退出"
		case "running":
			all = false
			if now.Sub(op.StartedAt) >= normalMaintenanceTimeout || op.Phase == domain.WorkerMaintenanceNeedsForce {
				p.State = "awaitingForce"
				if p.Message == "" {
					p.Message = "进程仍存活，可重新确认强制关闭"
				}
			}
		default:
			all = false
			p.State = "unknown"
			p.Message = "进程暂不可观察，可重新检查或恢复执行服务"
		}
	}
	return all, nil
}
