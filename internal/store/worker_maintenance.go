package store

import (
	"database/sql"
	"errors"
	"reflect"
	"time"

	"shioji/internal/domain"
)

type MaintenanceSessionState struct {
	Session domain.Session
	Queue   domain.QueueState
	Idle    bool
}

type maintenanceQuerier interface{ QueryRow(string, ...any) *sql.Row }

// Queued messages are allowed: admission pauses them in the same transaction.
const maintenanceIdleQuery = `SELECT
 COALESCE(json_extract(s.body,'$.status'),'') IN ('idle','completed','interrupted','failed')
 AND COALESCE(json_extract(s.body,'$.executionGuard'),'')=''
 AND COALESCE(json_extract(s.body,'$.contextAction.state'),'') IN ('','completed','interrupted','failed')
 AND NOT EXISTS(SELECT 1 FROM operations WHERE session_id=s.id AND state IN ('accepted','dispatched','indeterminate'))
 AND NOT EXISTS(SELECT 1 FROM requests WHERE session_id=s.id AND json_extract(body,'$.state') NOT IN ('answered','resolved','invalidated'))
 AND NOT EXISTS(SELECT 1 FROM turns WHERE session_id=s.id AND json_extract(body,'$.status') NOT IN ('completed','interrupted','failed','abandoned'))
 AND NOT EXISTS(SELECT 1 FROM works WHERE session_id=s.id AND COALESCE(json_extract(body,'$.state'),'') NOT IN ('completed','stopped','failed') AND (COALESCE(json_extract(body,'$.originRef'),'')='' OR json_extract(body,'$.originRef')=json_extract(s.body,'$.threadId')))
 AND NOT EXISTS(SELECT 1 FROM work_inputs WHERE session_id=s.id AND json_extract(body,'$.state') IN ('accepted','dispatched','indeterminate') AND COALESCE(json_extract(body,'$.executionStopped'),0)=0)
 AND NOT EXISTS(SELECT 1 FROM history_operations WHERE json_extract(body,'$.state') IN ('prepared','forked') AND json_extract(body,'$.source.id')=?1)
 AND NOT EXISTS(SELECT 1 FROM history_operations WHERE json_extract(body,'$.state') IN ('prepared','forked') AND json_extract(body,'$.session.id')=?1)
 AND NOT EXISTS(SELECT 1 FROM title_jobs WHERE session_id=s.id AND state='running')
 FROM sessions s WHERE s.id=?1`

func maintenanceSessionState(q maintenanceQuerier, id string) (out MaintenanceSessionState, err error) {
	out.Session, err = load[domain.Session](q.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	if err != nil {
		return
	}
	out.Queue, err = load[domain.QueueState](q.QueryRow("SELECT body FROM queues WHERE session_id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		out.Queue = domain.QueueState{SessionID: id}
		err = nil
	}
	if err != nil {
		return
	}
	err = q.QueryRow(maintenanceIdleQuery, id).Scan(&out.Idle)
	return
}

func (s *Store) MaintenanceSessionState(id string) (MaintenanceSessionState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maintenanceSessionState(s.db, id)
}

func maintenanceRecord(q maintenanceQuerier, id string) (domain.WorkerMaintenance, error) {
	if id == "" {
		return load[domain.WorkerMaintenance](q.QueryRow("SELECT body FROM worker_maintenance ORDER BY rowid DESC LIMIT 1"))
	}
	return load[domain.WorkerMaintenance](q.QueryRow("SELECT body FROM worker_maintenance WHERE id=?", id))
}

func (s *Store) WorkerMaintenance(id string) (domain.WorkerMaintenance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maintenanceRecord(s.db, id)
}

func sameMaintenanceIntent(a, b domain.WorkerMaintenance) bool {
	if a.ProcessPreviewRef != b.ProcessPreviewRef || len(a.Processes) != len(b.Processes) {
		return false
	}
	for i := range a.Processes {
		if a.Processes[i].Process != b.Processes[i].Process {
			return false
		}
	}
	if a.ID != b.ID || a.Explicit != b.Explicit || a.Standalone != b.Standalone || a.DeviceID != b.DeviceID || a.ExpectedBuildID != b.ExpectedBuildID || a.BuildID != b.BuildID || len(a.Targets) != len(b.Targets) {
		return false
	}
	for i := range a.Targets {
		if !reflect.DeepEqual(a.Targets[i].Target, b.Targets[i].Target) {
			return false
		}
	}
	return true
}

func (s *Store) AcceptWorkerMaintenance(op domain.WorkerMaintenance) (domain.WorkerMaintenance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var none domain.WorkerMaintenance
	if op.ID == "" || len(op.ID) > 256 || op.DeviceID != s.deviceID || op.ExpectedBuildID == "" || (!op.Standalone && op.BuildID == "") || len(op.Targets)+len(op.Processes) == 0 || len(op.Targets) > 32 || len(op.Processes) > 128 {
		return none, errors.New("无效的后台环境处理请求")
	}
	if existing, err := maintenanceRecord(s.db, op.ID); err == nil {
		if !sameMaintenanceIntent(existing, op) {
			return none, errors.New("处理标识已用于其他更新")
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return none, err
	}
	if existing, err := maintenanceRecord(s.db, ""); err == nil && existing.Phase.Active() {
		return none, errors.New("此设备已有后台环境处理正在进行")
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return none, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return none, err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	var queues []domain.QueueState
	for _, result := range op.Targets {
		t := result.Target
		if t.SessionID == "" || t.Generation == "" || t.ProcessRef == "" || seen[t.SessionID] {
			return none, errors.New("处理范围无效或重复")
		}
		seen[t.SessionID] = true
		state, err := maintenanceSessionState(tx, t.SessionID)
		if err != nil {
			return none, err
		}
		if (!op.Explicit && !state.Idle) || state.Session.Revision != t.Revision || state.Queue.Revision != t.QueueRevision {
			return none, errors.New("会话或队列已变化，请重新检查并确认")
		}
		state.Queue.Paused = true
		state.Queue.Reason = "正在结束后台环境，待发消息已保留"
		state.Queue.Revision++
		queues = append(queues, state.Queue)
		if op.Explicit && !state.Idle {
			if err = quarantineMaintenance(tx, state.Session, t.Generation); err != nil {
				return none, err
			}
		}
	}
	for _, q := range queues {
		if err = putQueue(tx, q); err != nil {
			return none, err
		}
	}
	op.Revision = 1
	op.Phase = domain.WorkerMaintenanceStopping
	op.UpdatedAt = time.Now().UTC()
	op.StartedAt = op.UpdatedAt
	if _, err = tx.Exec("INSERT INTO worker_maintenance VALUES(?,?)", op.ID, blob(op)); err != nil {
		return none, err
	}
	// Keep a small receipt history; live generation guards are stored separately.
	if _, err = tx.Exec("DELETE FROM worker_maintenance WHERE id NOT IN (SELECT id FROM worker_maintenance ORDER BY rowid DESC LIMIT 20)"); err != nil {
		return none, err
	}
	return op, s.commit(tx)
}

// Acceptance keeps uncertain execution distinct from completion, invalidates
// requests in transcript order, and fences late events before any signal.
func quarantineMaintenance(tx *sql.Tx, session domain.Session, generation string) error {
	session.Status = "unknown"
	session.ExecutionGuard = generation
	session.Revision++
	session.Error = "运行环境由用户关闭，执行结果待核对"
	session.Recovery = &domain.SessionRecovery{Phase: "attention", Message: session.Error}
	if _, err := tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), session.ID); err != nil {
		return err
	}
	if _, err := journal(tx, domain.Event{Type: "session", SessionID: session.ID, Session: &session}); err != nil {
		return err
	}
	turns, err := rowsOf[domain.Turn](tx, "SELECT body FROM turns WHERE session_id=? AND json_extract(body,'$.status') NOT IN ('completed','interrupted','failed','abandoned')", session.ID)
	if err != nil {
		return err
	}
	for _, turn := range turns {
		turn.Status = "unknown"
		turn.Error = session.Error
		if _, err = tx.Exec("UPDATE turns SET body=? WHERE session_id=? AND id=?", blob(turn), session.ID, turn.ID); err != nil {
			return err
		}
		if _, err = journal(tx, domain.Event{Type: "turn", SessionID: session.ID, Turn: &turn}); err != nil {
			return err
		}
	}
	requests, err := rowsOf[domain.PendingRequest](tx, "SELECT body FROM requests WHERE session_id=? AND json_extract(body,'$.state') IN ('pending','submitting')", session.ID)
	if err != nil {
		return err
	}
	for _, request := range requests {
		request.State = domain.RequestInvalidated
		request.Revision++
		if _, err = tx.Exec("UPDATE requests SET body=? WHERE session_id=? AND id=?", blob(request), session.ID, request.ID); err != nil {
			return err
		}
		if _, err = journal(tx, domain.Event{Type: "request.resolve", SessionID: session.ID, Request: &request}); err != nil {
			return err
		}
	}
	_, err = tx.Exec("UPDATE operations SET state='indeterminate',error=? WHERE session_id=? AND state IN ('accepted','dispatched')", session.Error, session.ID)
	return err
}

func (s *Store) SaveWorkerMaintenance(op domain.WorkerMaintenance, expected int64) (domain.WorkerMaintenance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var none domain.WorkerMaintenance
	existing, err := maintenanceRecord(s.db, op.ID)
	if err != nil {
		return none, err
	}
	if existing.Revision != expected || !sameMaintenanceIntent(existing, op) {
		return none, errors.New("处理状态已变化，请重新查询")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return none, err
	}
	defer tx.Rollback()
	op.Revision = expected + 1
	op.UpdatedAt = time.Now().UTC()
	if _, err = tx.Exec("UPDATE worker_maintenance SET body=? WHERE id=?", blob(op), op.ID); err != nil {
		return none, err
	}
	return op, s.commit(tx)
}

func (s *Store) MaintenanceSessionBlocked(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var blocked bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM worker_maintenance m,json_each(m.body,'$.targets') t WHERE json_extract(m.body,'$.phase') IN ('stopping','needsForce') AND json_extract(t.value,'$.target.sessionId')=? AND json_extract(t.value,'$.state')!='stopped')`, id).Scan(&blocked)
	return blocked, err
}
