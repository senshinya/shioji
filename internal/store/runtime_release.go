package store

import (
	"database/sql"
	"errors"
	"shioji/internal/domain"
)

// ReleaseRuntime retires an execution owner; stopped is true only after the
// captured runtime and registered groups confirmed shutdown. Input delivery is
// recorded separately and original operation IDs remain non-replayable.
func (s *Store) ReleaseRuntime(id, generation, message string, stopped bool) error {
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
	if session.ExecutionGuard != "" && session.ExecutionGuard != generation {
		return errors.New("managed generation changed")
	}
	ops, err := rowsOf[domain.Operation](tx, `SELECT json_object('id',id,'sessionId',session_id,'state',state,'digest',digest,'kind',kind) FROM operations WHERE session_id=? AND state IN ('accepted','dispatched','indeterminate')`, id)
	if err != nil {
		return err
	}
	for _, op := range ops {
		op.State, op.Error = "abandoned", message
		if _, err = tx.Exec("UPDATE operations SET state=?,error=? WHERE id=?", op.State, op.Error, op.ID); err != nil {
			return err
		}
		if _, err = journal(tx, domain.Event{Type: "operation", SessionID: id, Operation: &op}); err != nil {
			return err
		}
		item, e := load[domain.Item](tx.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", id, "input:"+op.ID))
		if e == nil {
			item.Status, item.Final = "unknown", true
			if err = putItem(tx, id, &item); err != nil {
				return err
			}
			if _, err = journal(tx, domain.Event{Type: "item.upsert", SessionID: id, Item: &item}); err != nil {
				return err
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
	}
	turns, err := rowsOf[domain.Turn](tx, `SELECT body FROM turns WHERE session_id=? AND json_extract(body,'$.status') IN ('running','unknown')`, id)
	if err != nil {
		return err
	}
	for _, turn := range turns {
		turn.Status, turn.Error = "abandoned", message
		if stopped {
			turn.Status, turn.Error = "interrupted", ""
		}
		if _, err = tx.Exec("UPDATE turns SET body=? WHERE session_id=? AND id=?", blob(turn), id, turn.ID); err != nil {
			return err
		}
		if _, err = journal(tx, domain.Event{Type: "turn", SessionID: id, Turn: &turn}); err != nil {
			return err
		}
	}
	works, err := rowsOf[domain.WorkActivity](tx, `SELECT body FROM works WHERE session_id=? AND json_extract(body,'$.state') IN ('starting','running','waiting')`, id)
	if err != nil {
		return err
	}
	for _, work := range works {
		work.State, work.CanStop, work.CanSteer, work.CanContinue = "unknown", false, false, false
		if stopped {
			work.State = "stopped"
		}
		event := domain.Event{Type: "work.upsert", SessionID: id, Work: &work}
		if err = applyWork(tx, id, &event); err != nil {
			return err
		}
		if _, err = journal(tx, event); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`UPDATE work_inputs SET body=json_set(body,'$.state','abandoned','$.error',?) WHERE session_id=? AND json_extract(body,'$.state') IN ('accepted','dispatched','indeterminate')`, message, id); err != nil {
		return err
	}
	requests, err := rowsOf[domain.PendingRequest](tx, `SELECT body FROM requests WHERE session_id=? AND json_extract(body,'$.state') IN ('pending','submitting')`, id)
	if err != nil {
		return err
	}
	for _, request := range requests {
		request.State = "invalidated"
		if _, err = tx.Exec("UPDATE requests SET body=? WHERE session_id=? AND id=?", blob(request), id, request.ID); err != nil {
			return err
		}
		anchor := domain.Item{ID: "request:" + request.ID, WorkID: request.WorkID, TurnID: request.TurnID, Kind: "request", Text: request.Title, Status: "invalidated", Final: true}
		if err = putItem(tx, id, &anchor); err != nil {
			return err
		}
		event := domain.Event{Type: "request.resolve", SessionID: id, Request: &request, Item: &anchor}
		if err = mirrorRequestWork(tx, id, &event, anchor); err != nil {
			return err
		}
		if _, err = journal(tx, event); err != nil {
			return err
		}
	}
	q, err := load[domain.QueueState](tx.QueryRow("SELECT body FROM queues WHERE session_id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		q, err = domain.QueueState{SessionID: id}, nil
	}
	if err != nil {
		return err
	}
	q.Paused, q.Reason = true, "旧执行结果未确认，队列保持暂停"
	if stopped {
		q.Reason = "stopped"
	}
	q.Revision++
	if err = putQueue(tx, q); err != nil {
		return err
	}
	if action := session.ContextAction; action != nil && (action.State.Active() || action.State == "unknown") {
		action.State, action.Error = "failed", message
	}
	if _, err = s.settleReplyCompletion(tx, &session, true); err != nil {
		return err
	}
	session.ExecutionGuard, session.Recovery = "", nil
	session.Status, session.Error = "failed", message
	if stopped {
		session.Status, session.Error = "interrupted", ""
	}
	session.Revision++
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), id); err != nil {
		return err
	}
	if _, err = journal(tx, domain.Event{Type: "session", SessionID: id, Session: &session}); err != nil {
		return err
	}
	return s.commit(tx)
}
