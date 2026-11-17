package store

import (
	"database/sql"
	"errors"
	"shioji/internal/domain"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Store) WorkInput(id string) (domain.WorkInputOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return load[domain.WorkInputOperation](s.db.QueryRow("SELECT body FROM work_inputs WHERE id=?", id))
}
func (s *Store) AcceptWorkInput(in domain.WorkInput) (domain.WorkInputOperation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var none domain.WorkInputOperation
	if in.OperationID == "" || len(in.OperationID) > 256 || in.SessionID == "" || in.WorkID == "" || len(in.Text) > 64<<10 || !utf8.ValidString(in.Text) || strings.TrimSpace(in.Text) == "" || (in.Mode != "steer" && in.Mode != "continue") {
		return none, false, errors.New("无效的子 Agent 消息")
	}
	old, err := load[domain.WorkInputOperation](s.db.QueryRow("SELECT body FROM work_inputs WHERE id=?", in.OperationID))
	if err == nil {
		if old.Input != in {
			return none, false, errors.New("操作标识已用于其他输入")
		}
		return old, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return none, false, err
	}
	if _, err = operation(s.db, in.OperationID); !errors.Is(err, sql.ErrNoRows) {
		return none, false, errors.New("操作标识已被使用，不能重放已删除会话的输入")
	}
	w, err := load[domain.WorkActivity](s.db.QueryRow("SELECT body FROM works WHERE session_id=? AND id=?", in.SessionID, in.WorkID))
	if err != nil {
		return none, false, err
	}
	if w.Kind != "agent" {
		return none, false, errors.New("只能向子 Agent 发送消息")
	}
	if in.Mode == "steer" && (!w.Active() || !w.CanSteer) || in.Mode == "continue" && (!w.CanContinue || (w.State != "completed" && w.State != "stopped")) {
		return none, false, errors.New("子 Agent 的执行状态已变化")
	}
	var unsettled bool
	if err = s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM work_inputs WHERE session_id=? AND work_id=? AND json_extract(body,'$.state') IN ('accepted','dispatched','indeterminate'))", in.SessionID, in.WorkID).Scan(&unsettled); err != nil {
		return none, false, err
	}
	if unsettled {
		return none, false, errors.New("上一条消息尚未确认")
	}
	op := domain.WorkInputOperation{Input: in, State: "accepted", SentAt: time.Now().UTC()}
	tx, err := s.db.Begin()
	if err != nil {
		return none, false, err
	}
	defer tx.Rollback()
	// Reserve transcript order before the adapter can emit a fast reply.
	if in.Mode == "continue" {
		w.State = "starting"
		w.CanContinue = false
		w.CanSteer = false
	}
	event := domain.Event{SessionID: in.SessionID, Type: "work.upsert", Work: &w}
	if err = applyWork(tx, in.SessionID, &event); err != nil {
		return none, false, err
	}
	event, err = journal(tx, event)
	if err != nil {
		return none, false, err
	}
	op.Order = event.Sequence
	if _, err = tx.Exec("INSERT INTO work_inputs VALUES(?,?,?,?)", in.OperationID, in.SessionID, in.WorkID, blob(op)); err != nil {
		return none, false, err
	}
	err = s.commit(tx)
	return op, err == nil, err
}
func (s *Store) SetWorkInput(id string, state domain.OperationState, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := load[domain.WorkInputOperation](s.db.QueryRow("SELECT body FROM work_inputs WHERE id=?", id))
	if err != nil {
		return err
	}
	if op.State == "confirmed" || op.State == "rejected" || op.State == "abandoned" {
		if op.State == state {
			return nil
		}
		return errors.New("消息接纳结果已确定")
	}
	op.State, op.Error = state, message
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE work_inputs SET body=? WHERE id=?", blob(op), id); err != nil {
		return err
	}
	if state == "confirmed" {
		w, e := load[domain.WorkActivity](tx.QueryRow("SELECT body FROM works WHERE session_id=? AND id=?", op.Input.SessionID, op.Input.WorkID))
		if e != nil {
			return e
		}
		event := domain.Event{SessionID: op.Input.SessionID, Type: "work.item", WorkID: op.Input.WorkID, WorkItem: &domain.Item{ID: "input:" + id, TurnID: w.TurnID, Kind: "user", Text: op.Input.Text, Status: "sent", Final: true, SentAt: &op.SentAt}}
		if err = applyWork(tx, event.SessionID, &event); err != nil {
			return err
		}
		event.WorkItem.Order = op.Order
		if _, err = tx.Exec("UPDATE work_items SET position=?,body=? WHERE session_id=? AND work_id=? AND id=?", op.Order, blob(event.WorkItem), op.Input.SessionID, op.Input.WorkID, event.WorkItem.ID); err != nil {
			return err
		}
		if _, err = journal(tx, event); err != nil {
			return err
		}
	}
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", op.Input.SessionID))
	if err != nil {
		return err
	}
	changed, err := s.settleReplyCompletion(tx, &session, false)
	if err != nil {
		return err
	}
	if changed {
		if err = saveCompletionSession(tx, &session); err != nil {
			return err
		}
	}
	return s.commit(tx)
}
