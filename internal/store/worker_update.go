package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/domain"
)

// Schema 15 retains worker-owned worktrees through archive/deletion cleanup.
const SchemaVersion = 15

// The caller fences dispatch and commands before this final database check.
// VACUUM INTO reads SQLite's committed snapshot, including its WAL.
func (s *Store) PrepareWorkerUpdate(backup string) (domain.WorkerUpdateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.prepareWorkerStop("执行服务更新，队列已保留")
	if err != nil || !result.Ready {
		return result, err
	}
	return s.backupWorkerLocked(backup)
}

func (s *Store) BackupWorker(backup string) (domain.WorkerUpdateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backupWorkerLocked(backup)
}
func (s *Store) backupWorkerLocked(backup string) (domain.WorkerUpdateResult, error) {
	f, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	f.Close()
	if _, err = s.db.Exec("VACUUM INTO ?", backup); err != nil {
		os.Remove(backup)
		return domain.WorkerUpdateResult{}, err
	}
	file, err := os.OpenFile(backup, os.O_RDWR, 0600)
	if err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	err = file.Sync()
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	dir, err := os.Open(filepath.Dir(backup))
	if err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	return domain.WorkerUpdateResult{Ready: true, Backup: backup}, nil
}

func (s *Store) PrepareWorkerStop(reason string) (domain.WorkerUpdateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepareWorkerStop(reason)
}
func (s *Store) prepareWorkerStop(reason string) (domain.WorkerUpdateResult, error) {
	var blocked bool
	err := s.db.QueryRow(workerStopBlockQuery).Scan(&blocked)
	if err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	if blocked {
		return domain.WorkerUpdateResult{Reason: "有任务、待处理请求或执行状态尚未确认，请空闲后再试"}, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT body FROM queues")
	if err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	var queues []domain.QueueState
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return domain.WorkerUpdateResult{}, err
		}
		var q domain.QueueState
		if err = json.Unmarshal(body, &q); err != nil {
			rows.Close()
			return domain.WorkerUpdateResult{}, err
		}
		if len(q.Items) > 0 && !q.Paused {
			queues = append(queues, q)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	for _, q := range queues {
		q.Paused = true
		q.Reason = reason
		q.Revision++
		if err = putQueue(tx, q); err != nil {
			return domain.WorkerUpdateResult{}, err
		}
	}
	if err = s.commit(tx); err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	return domain.WorkerUpdateResult{Ready: true}, nil
}

const workerStopBlockQuery = `SELECT
 EXISTS(SELECT 1 FROM sessions WHERE COALESCE(json_extract(body,'$.status'),'') NOT IN ('idle','completed','interrupted','failed','abandoned') OR COALESCE(json_extract(body,'$.executionGuard'),'')!='')
 OR EXISTS(SELECT 1 FROM operations WHERE state IN ('accepted','dispatched','indeterminate'))
 OR EXISTS(SELECT 1 FROM work_inputs WHERE json_extract(body,'$.state') IN ('accepted','dispatched','indeterminate') AND COALESCE(json_extract(body,'$.executionStopped'),0)=0)
 OR EXISTS(SELECT 1 FROM requests WHERE json_extract(body,'$.state') IN ('pending','submitting'))
 OR EXISTS(SELECT 1 FROM history_operations WHERE json_extract(body,'$.state') IN ('prepared','forked'))
 OR EXISTS(SELECT 1 FROM works w JOIN sessions s ON w.session_id=s.id WHERE json_extract(w.body,'$.state') IN ('starting','running','waiting') AND (COALESCE(json_extract(w.body,'$.originRef'),'')='' OR json_extract(w.body,'$.originRef')=json_extract(s.body,'$.threadId')))
 OR EXISTS(SELECT 1 FROM title_jobs WHERE state='running')`

func (s *Store) WorkerStopReady(liveSessions []string) (domain.WorkerUpdateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Managed replacement owns cleanup after admission. Only a live turn or
	// its live initialization reserves admission. Historical records alone do
	// not represent a lease held by this worker instance.
	for _, id := range liveSessions {
		var blocked bool
		err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND json_extract(body,'$.status') IN ('starting','running'))`, id).Scan(&blocked)
		if err != nil {
			return domain.WorkerUpdateResult{}, err
		}
		if blocked {
			return domain.WorkerUpdateResult{Reason: "等待正在初始化或执行的会话轮次"}, nil
		}
	}
	return domain.WorkerUpdateResult{Ready: true}, nil
}
