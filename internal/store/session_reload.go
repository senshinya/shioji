package store

import (
	"database/sql"
	"errors"
	"shioji/internal/domain"
)

func (s *Store) SessionReload(in domain.SessionReloadRequest) (domain.SessionReloadOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sessionReload(s.db, in)
}

func sessionReload(q interface{ QueryRow(string, ...any) *sql.Row }, in domain.SessionReloadRequest) (domain.SessionReloadOperation, error) {
	op, err := load[domain.SessionReloadOperation](q.QueryRow("SELECT body FROM session_reloads WHERE id=?", in.OperationID))
	if err == nil && op.Input != in {
		return domain.SessionReloadOperation{}, errors.New("重新加载回执与原请求不一致")
	}
	return op, err
}

func (s *Store) AcceptSessionReload(in domain.SessionReloadRequest) (domain.SessionReloadOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var zero domain.SessionReloadOperation
	if in.OperationID == "" || len(in.OperationID) > 128 || in.SessionID == "" || in.Revision <= 0 {
		return zero, errors.New("无效的重新加载请求")
	}
	if op, err := sessionReload(s.db, in); err == nil {
		return op, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return zero, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", in.SessionID))
	if err != nil {
		return zero, err
	}
	if session.Revision != in.Revision || session.ContextChanged || session.ContextAction != nil && (session.ContextAction.State.Active() || session.ContextAction.State == "unknown") {
		return zero, errors.New("会话状态已改变或正在执行，请稍后重试")
	}
	if err := s.historyAvailable(tx, session); err != nil {
		return zero, err
	}
	var blocked bool
	err = tx.QueryRow(`SELECT
 EXISTS(SELECT 1 FROM turns WHERE session_id=?1 AND json_extract(body,'$.status') NOT IN ('completed','interrupted','failed','abandoned'))
 OR EXISTS(SELECT 1 FROM works WHERE session_id=?1 AND COALESCE(json_extract(body,'$.state'),'') NOT IN ('completed','stopped','failed') AND (COALESCE(json_extract(body,'$.originRef'),'')='' OR json_extract(body,'$.originRef')=?2))
 OR EXISTS(SELECT 1 FROM work_inputs WHERE session_id=?1 AND json_extract(body,'$.state') IN ('accepted','dispatched','indeterminate') AND COALESCE(json_extract(body,'$.executionStopped'),0)=0)
 OR EXISTS(SELECT 1 FROM queues WHERE session_id=?1 AND json_array_length(body,'$.items')>0 AND COALESCE(json_extract(body,'$.paused'),0)=0)
 OR EXISTS(SELECT 1 FROM history_operations WHERE json_extract(body,'$.state') IN ('prepared','forked') AND (json_extract(body,'$.source.id')=?1 OR json_extract(body,'$.session.id')=?1))
 OR EXISTS(SELECT 1 FROM session_reloads WHERE session_id=?1 AND json_extract(body,'$.state')='running')`, session.ID, session.ThreadID).Scan(&blocked)
	if err != nil {
		return zero, err
	}
	if blocked {
		return zero, errors.New("请先处理运行中的任务、待发队列或未完成操作")
	}
	op := domain.SessionReloadOperation{Input: in, State: "running"}
	if _, err = tx.Exec("INSERT INTO session_reloads(id,session_id,body) VALUES(?,?,?)", in.OperationID, in.SessionID, blob(op)); err != nil {
		return zero, err
	}
	return op, s.commit(tx)
}

func (s *Store) FinishSessionReload(in domain.SessionReloadRequest, cause error) (domain.SessionReloadOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := sessionReload(s.db, in)
	if err != nil || op.State != "running" {
		return op, err
	}
	op.State = "completed"
	if cause != nil {
		op.State, op.Error = "failed", cause.Error()
	}
	_, err = s.db.Exec("UPDATE session_reloads SET body=? WHERE id=?", blob(op), in.OperationID)
	return op, err
}
