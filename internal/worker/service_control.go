package worker

import (
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/supervisor"
	"time"
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
		if !in.ExpiresAt.IsZero() && !time.Now().Before(in.ExpiresAt) {
			return nil, errors.New("prepared update expired")
		}
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
		if !in.ExpiresAt.IsZero() && !time.Now().Before(in.ExpiresAt) {
			return nil, errors.New("prepared update expired")
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
		if !in.ExpiresAt.IsZero() && !time.Now().Before(in.ExpiresAt) {
			return nil, errors.New("prepared update expired")
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
	s.mu.Lock()
	reserved := len(s.dispatchReservations) > 0
	s.mu.Unlock()
	if reserved {
		return domain.WorkerUpdateResult{Reason: "等待已经接纳的会话轮次释放"}, nil
	}
	s.runtimeMu.RLock()
	if len(s.runtimeStarts) > 0 {
		s.runtimeMu.RUnlock()
		return domain.WorkerUpdateResult{Reason: "等待会话初始化结束"}, nil
	}
	live := make([]string, 0, len(s.runtimes))
	for id, r := range s.runtimes {
		if r.Stopped() {
			continue
		}
		if pending, ok := r.(harness.PendingRuntime); ok && pending.PendingOperations() > 0 {
			s.runtimeMu.RUnlock()
			return domain.WorkerUpdateResult{Reason: "等待原始执行请求完成"}, nil
		}
		live = append(live, id)
	}
	s.runtimeMu.RUnlock()
	return s.store.WorkerStopReady(live)
}

// Reserve synchronously before the first initialization await. Detaching a
// failed Agent releases its leases even if an old native call never returns.
func (s *Server) reserveDispatch(op domain.Operation) func() {
	s.mu.Lock()
	if s.dispatchReservations == nil {
		s.dispatchReservations = map[string]string{}
	}
	s.dispatchReservations[op.ID] = op.SessionID
	if s.dispatchEpochs == nil {
		s.dispatchEpochs = map[string]uint64{}
	}
	s.dispatchEpochs[op.SessionID]++
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.dispatchReservations, op.ID)
		s.mu.Unlock()
	}
}
