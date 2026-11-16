package worker

import (
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/supervisor"
)

func (s *Server) serviceControl(method string, body []byte) (any, error) {
	in, err := decode[supervisor.Control](body)
	if err != nil {
		return nil, err
	}
	if s.config.ServiceInstance == "" || in.Instance != s.config.ServiceInstance {
		return nil, errors.New("service instance authentication failed")
	}
	switch method {
	case "worker.local-ready":
		return supervisor.Ready{PID: os.Getpid(), Instance: s.config.ServiceInstance, DeviceID: s.store.DeviceID(), Build: s.build, Paused: s.updating.Load()}, nil
	case "worker.local-backup":
		if in.OperationID == "" || filepath.Base(in.OperationID) != in.OperationID || len(in.OperationID) > 128 {
			return nil, errors.New("invalid backup operation")
		}
		return s.store.BackupWorker(filepath.Join(s.config.Directory, "update-backup-"+in.OperationID+".sqlite"))
	case "worker.prepare-drain":
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		if in.Replacement.Policy == "immediate" && in.Replacement.Explicit {
			return domain.WorkerUpdateResult{Ready: true}, nil
		}
		return s.businessStopReady()
	case "worker.drain":
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		s.drainMu.Lock()
		defer s.drainMu.Unlock()
		if s.drainOperation != "" {
			if s.drainOperation == in.OperationID {
				return domain.WorkerUpdateResult{Ready: true}, nil
			}
			return nil, errors.New("another drain already accepted")
		}
		if in.Replacement.Policy != "immediate" {
			ready, e := s.businessStopReady()
			if e != nil || !ready.Ready {
				return ready, e
			}
		}
		intent := supervisor.DrainIntent{ID: in.OperationID, Instance: s.config.ServiceInstance, DeviceID: s.store.DeviceID(), BuildID: s.build.BuildID}
		snapshot, e := s.store.Snapshot()
		if e != nil {
			return nil, e
		}
		for _, session := range snapshot.Sessions {
			q, e := s.store.Queue(session.ID)
			if e != nil {
				return nil, e
			}
			if !q.Paused {
				intent.Queues = append(intent.Queues, session.ID)
			}
		}
		if err = supervisor.SaveDrainIntent(s.config.Directory, intent); err != nil {
			return nil, err
		}
		s.drainOperation = in.OperationID
		s.updating.Store(true)
		return domain.WorkerUpdateResult{Ready: true, OperationID: in.OperationID, Phase: "switching"}, nil
	case "worker.resume":
		s.dispatchMu.Lock()
		defer s.dispatchMu.Unlock()
		if in.OperationID == "" || in.OperationID != s.config.ServiceOperation {
			return nil, errors.New("candidate operation mismatch")
		}
		intent, e := supervisor.ReadDrainIntent(s.config.Directory, in.OperationID)
		if e != nil {
			return nil, e
		}
		if intent.DeviceID != s.store.DeviceID() {
			return nil, errors.New("drain intent belongs to another device")
		}
		for _, id := range intent.Queues {
			q, e := s.store.Queue(id)
			if e != nil {
				return nil, e
			}
			if q.Paused && q.Reason == "worker restarted" {
				detail, e := s.store.Session(id, 0, 1)
				if e != nil {
					return nil, e
				}
				if detail.Session.ExecutionGuard != "" || detail.Session.Status.BlocksCheckout() {
					continue
				}
				safe := true
				for _, item := range q.Items {
					if item.State != "" {
						safe = false
					}
				}
				if !safe {
					continue
				}
				if _, e = s.store.ContinueQueue(id, q.Revision); e != nil {
					return nil, e
				}
			}
		}
		s.updating.Store(false)
		s.wakeRecovery()
		s.wakeCatalog()
		select {
		case s.titleWake <- struct{}{}:
		default:
		}
		s.wakeQueue()
		return nil, nil
	default:
		return nil, errors.New("unknown private service control")
	}
}

func (s *Server) DrainedReceipt() (supervisor.Drained, bool) {
	s.drainMu.Lock()
	defer s.drainMu.Unlock()
	if s.drainOperation == "" || s.closeErr != nil {
		return supervisor.Drained{}, false
	}
	return supervisor.Drained{PID: os.Getpid(), Instance: s.config.ServiceInstance, OperationID: s.drainOperation, DeviceID: s.store.DeviceID(), BuildID: s.build.BuildID}, true
}

func (s *Server) businessStopReady() (domain.WorkerUpdateResult, error) {
	result, err := s.store.WorkerStopReady()
	if err != nil || !result.Ready {
		return result, err
	}
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	for _, r := range s.runtimes {
		if pending, ok := r.(harness.PendingRuntime); ok && pending.PendingOperations() > 0 {
			return domain.WorkerUpdateResult{Reason: "等待原始执行请求完成"}, nil
		}
	}
	return result, nil
}
