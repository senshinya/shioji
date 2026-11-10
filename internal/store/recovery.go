package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"shioji/internal/domain"
)

// GuardExecution quarantines a lost managed generation before its transport is
// closed. Only reconciliation with that generation's stop proof can release it.
func (s *Store) GuardExecution(id, generation string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	if err != nil {
		return err
	}
	if generation == "" || session.ExecutionGuard != "" && session.ExecutionGuard != generation {
		return errors.New("managed generation changed")
	}
	session.ExecutionGuard = generation
	session.Status = "unknown"
	session.Error = "正在确认旧工具已停止"
	session.Recovery = &domain.SessionRecovery{Phase: "checking"}
	session.Revision++
	turns, err := rowsOf[domain.Turn](tx, "SELECT body FROM turns WHERE session_id=? AND json_extract(body,'$.status')='running'", id)
	if err != nil {
		return err
	}
	for _, turn := range turns {
		turn.Status, turn.Error = "unknown", session.Error
		if _, err = tx.Exec("UPDATE turns SET body=? WHERE session_id=? AND id=?", blob(turn), id, turn.ID); err != nil {
			return err
		}
		if _, err = journal(tx, domain.Event{Type: "turn", SessionID: id, Turn: &turn}); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), id); err != nil {
		return err
	}
	q, err := load[domain.QueueState](tx.QueryRow("SELECT body FROM queues WHERE session_id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		q = domain.QueueState{SessionID: id}
		err = nil
	}
	if err == nil {
		q.Paused, q.Reason = true, "恢复后等待确认"
		q.Revision++
		if err = putQueue(tx, q); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = journal(tx, domain.Event{Type: "session", SessionID: id, Session: &session}); err != nil {
		return err
	}
	return s.commit(tx)
}

// Recovery is device-wide and independent of the UI history page.
func (s *Store) RecoveryTurns(id string) ([]domain.Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return rowsOf[domain.Turn](s.db, "SELECT body FROM turns WHERE session_id=? AND json_extract(body,'$.status') IN ('unknown','running') ORDER BY rowid", id)
}

func (s *Store) ActiveSessions() ([]domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return rowsOf[domain.Session](s.db, "SELECT body FROM sessions WHERE json_extract(body,'$.status') IN ('running','starting')")
}

// Reconcile commits history, exact input acknowledgements and queue preservation
// together. A stale client or missing source turn cannot clear the unknown state.
func (s *Store) Reconcile(id string, revision int64, source domain.RecoveryHistory, end bool) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var none domain.Session
	tx, err := s.db.Begin()
	if err != nil {
		return none, err
	}
	defer tx.Rollback()
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	if err != nil {
		return none, err
	}
	if session.Revision != revision || session.Status != "unknown" {
		return none, errors.New("会话状态已改变，请重新核对")
	}
	if session.ExecutionGuard != "" && session.ExecutionGuard != source.StoppedGeneration {
		return none, errors.New("旧执行尚未确认清理完成")
	}
	if source.State == "active" && !source.Attached || source.State == "failed" {
		return none, errors.New("旧轮仍在执行或无法确认停止，请先停止后重新核对")
	}
	targets := map[string]bool{}
	rows, err := tx.Query("SELECT body FROM turns WHERE session_id=?", id)
	if err != nil {
		return none, err
	}
	oldTurns := map[string]domain.Turn{}
	latestTurn := ""
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return none, err
		}
		var turn domain.Turn
		if err = json.Unmarshal(b, &turn); err != nil {
			rows.Close()
			return none, err
		}
		oldTurns[turn.ID] = turn
		latestTurn = turn.ID
		if turn.Status == "unknown" || turn.Status == "running" {
			targets[turn.ID] = true
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return none, err
	}
	rows.Close()
	if session.ExecutionGuard != "" && latestTurn != "" {
		targets[latestTurn] = true
	}
	ops := []domain.Operation{}
	abandoned := map[string]bool{}
	rows, err = tx.Query("SELECT id,session_id,state,digest,error FROM operations WHERE session_id=? AND state IN ('accepted','dispatched','indeterminate')", id)
	if err != nil {
		return none, err
	}
	for rows.Next() {
		var op domain.Operation
		if err = rows.Scan(&op.ID, &op.SessionID, &op.State, &op.Digest, &op.Error); err != nil {
			rows.Close()
			return none, err
		}
		ops = append(ops, op)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return none, err
	}
	rows.Close()
	for _, op := range ops {
		if turn := source.Inputs["input:"+op.ID]; turn != "" {
			targets[turn] = true
		}
	}
	found := map[string]bool{}
	status := domain.ExecutionState("")
	unresolved := false
	toolUnconfirmed := false
	for _, turn := range source.Turns {
		if !targets[turn.ID] {
			continue
		}
		found[turn.ID] = true
		if turn.Status == "running" && !source.Attached {
			return none, errors.New("旧轮仍在执行，请先停止后重新核对")
		}
		if turn.Status == "interrupted" && !source.Attached && !source.ManagedStoppedTurns[turn.ID] {
			toolUnconfirmed = true
			turn.Status = "unknown"
			turn.Error = "已找回中断历史，设备上的旧工具执行尚待确认"
			if end {
				turn.Status = "abandoned"
				turn.Error = "用户已确认设备上的旧工具停止；原执行结果未确认"
			}
		}
		old := oldTurns[turn.ID]
		if turn.StartedAt == nil {
			turn.StartedAt = old.StartedAt
		}
		if turn.CompletedAt == nil {
			turn.CompletedAt = old.CompletedAt
		}
		if turn.DurationMS == nil {
			turn.DurationMS = old.DurationMS
		}
		// Source history does not carry per-turn usage. Keep confirmed local counters.
		turn.InputTokens, turn.OutputTokens, turn.CachedInputTokens = old.InputTokens, old.OutputTokens, old.CachedInputTokens
		if err = putTurn(tx, id, &turn); err != nil {
			return none, err
		}
		if _, err = journal(tx, domain.Event{Type: "turn", SessionID: id, Turn: &turn}); err != nil {
			return none, err
		}
		status = turn.Status
	}
	for target := range targets {
		if !found[target] {
			unresolved = true
		}
	}
	for _, item := range source.Items {
		if !targets[item.TurnID] {
			continue
		}
		if err = putItem(tx, id, &item); err != nil {
			return none, err
		}
		if _, err = journal(tx, domain.Event{Type: "item.upsert", SessionID: id, Item: &item}); err != nil {
			return none, err
		}
	}
	for _, op := range ops {
		tid := source.Inputs["input:"+op.ID]
		if tid == "" || !found[tid] {
			unresolved = true
			if !end {
				continue
			}
			op.State = "abandoned"
			abandoned[op.ID] = true
			op.Error = "用户手动结束；旧输入的执行结果未确认"
		} else {
			op.State = "confirmed"
			op.Error = ""
		}
		if _, err = tx.Exec("UPDATE operations SET state=?,error=? WHERE id=?", op.State, op.Error, op.ID); err != nil {
			return none, err
		}
		if op.State == "confirmed" {
			if err = queueOutcome(tx, op); err != nil {
				return none, err
			}
		}
		item, e := load[domain.Item](tx.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", id, "input:"+op.ID))
		if e == nil {
			if tid != "" {
				item.TurnID = tid
			}
			item.Status = "sent"
			if op.State == "abandoned" {
				item.Status = "unknown"
			}
			if err = putItem(tx, id, &item); err != nil {
				return none, err
			}
			if _, err = journal(tx, domain.Event{Type: "item.upsert", SessionID: id, Item: &item}); err != nil {
				return none, err
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return none, e
		}
		if _, err = journal(tx, domain.Event{Type: "operation", SessionID: id, Operation: &op}); err != nil {
			return none, err
		}
	}
	if source.Attached && (source.State == "active" || status == "running") && unresolved {
		return none, errors.New("仍有输入投递结果待核对，请稍后重试")
	}
	if end && (unresolved || status == "") {
		for tid, old := range oldTurns {
			if !targets[tid] || found[tid] {
				continue
			}
			old.Status = "abandoned"
			old.Error = "手动结束，旧轮结果未确认"
			if _, err = tx.Exec("UPDATE turns SET body=? WHERE session_id=? AND id=?", blob(old), id, tid); err != nil {
				return none, err
			}
			if _, err = journal(tx, domain.Event{Type: "turn", SessionID: id, Turn: &old}); err != nil {
				return none, err
			}
		}
		status = "abandoned"
	} else if unresolved || status == "" {
		status = "unknown"
	}
	if toolUnconfirmed {
		status = "unknown"
		if end {
			status = "abandoned"
		}
	}
	session.Status = status
	if session.ExecutionGuard != "" && session.ExecutionGuard == source.StoppedGeneration {
		// A generation stop proof releases execution ownership, not uncertainty
		// about historical input delivery. The same operation can never replay.
		if _, err = tx.Exec(`UPDATE work_inputs SET body=json_set(body,'$.state','indeterminate','$.executionStopped',json('true')) WHERE session_id=? AND json_extract(body,'$.state') IN ('accepted','dispatched','indeterminate')`, id); err != nil {
			return none, err
		}
		works, e := rowsOf[domain.WorkActivity](tx, `SELECT body FROM works WHERE session_id=? AND (COALESCE(json_extract(body,'$.originRef'),'')='' OR json_extract(body,'$.originRef')=?) AND (json_extract(body,'$.state') IN ('starting','running','waiting') OR json_extract(body,'$.canSteer')=1 OR json_extract(body,'$.canContinue')=1)`, id, session.ThreadID)
		if e != nil {
			return none, e
		}
		for _, w := range works {
			if w.Active() {
				w.State = "stopped"
			}
			w.CanStop, w.CanSteer, w.CanContinue = false, false, false
			event := domain.Event{SessionID: id, Type: "work.upsert", Work: &w}
			if err = applyWork(tx, id, &event); err != nil {
				return none, err
			}
			if _, err = journal(tx, event); err != nil {
				return none, err
			}
		}
	}
	session.ExecutionGuard = ""
	session.Error = ""
	if status == "unknown" {
		session.Error = "部分输入或执行结果未能在源历史中确认"
		if toolUnconfirmed {
			session.Error = "已找回中断历史，请先核对设备上的旧工具已停止"
		}
	}
	if status == "abandoned" {
		session.Error = "用户已确认旧工具停止并手动结束；原执行结果未确认"
	}
	session.Recovery = nil
	if status == "unknown" {
		session.Recovery = &domain.SessionRecovery{Phase: "attention", Message: session.Error, CanEnd: true}
	}
	if status == "abandoned" || status == "interrupted" || status == "failed" {
		available, e := sourceContainsLatestInput(tx.QueryRow("SELECT body FROM items WHERE session_id=? AND json_extract(body,'$.kind')='user' ORDER BY position DESC LIMIT 1", id), source)
		if e != nil {
			return none, e
		}
		session.Recovery = &domain.SessionRecovery{Phase: "ready", Message: session.Error, CanContinue: available}
	}
	session.Revision++
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), id); err != nil {
		return none, err
	}
	// Reconciliation never authorizes dispatch. Even an acknowledged queued input
	// may have been consumed above, but all remaining inputs remain paused.
	q, e := load[domain.QueueState](tx.QueryRow("SELECT body FROM queues WHERE session_id=?", id))
	if errors.Is(e, sql.ErrNoRows) {
		q = domain.QueueState{SessionID: id}
		e = nil
	}
	if e == nil {
		for i := range q.Items {
			if abandoned[q.Items[i].Input.OperationID] {
				q.Items[i].State = "failed"
			}
		}
		q.Paused = true
		q.Reason = "恢复后等待确认"
		q.Revision++
		if err = putQueue(tx, q); err != nil {
			return none, err
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return none, e
	}
	if _, err = journal(tx, domain.Event{Type: "session", SessionID: id, Session: &session}); err != nil {
		return none, err
	}
	return session, s.commit(tx)
}

// Recovery progress is independent of execution. Source callbacks can finish a
// turn during a read; preserve that newer status rather than overwriting it.
func (s *Store) SetRecovery(id string, info *domain.SessionRecovery) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	if err != nil {
		return session, err
	}
	if !session.Status.NeedsRecovery() && session.Status != "interrupted" && session.Status != "failed" {
		info = nil
	}
	if reflect.DeepEqual(session.Recovery, info) {
		return session, nil
	}
	session.Recovery = info
	session.Revision++
	tx, err := s.db.Begin()
	if err != nil {
		return session, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), id); err != nil {
		return session, err
	}
	if _, err = journal(tx, domain.Event{Type: "session", SessionID: id, Session: &session}); err != nil {
		return session, err
	}
	return session, s.commit(tx)
}

// A readable thread is not enough if its latest local user input was never
// confirmed in source history. In that case use the reviewable handoff draft.
func sourceContainsLatestInput(row *sql.Row, source domain.RecoveryHistory) (bool, error) {
	item, err := load[domain.Item](row)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return source.Available && (item.Status == "sent" || source.Inputs[item.ID] != ""), nil
}
func (s *Store) CanContinue(id string, source domain.RecoveryHistory) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sourceContainsLatestInput(s.db.QueryRow("SELECT body FROM items WHERE session_id=? AND json_extract(body,'$.kind')='user' ORDER BY position DESC LIMIT 1", id), source)
}
