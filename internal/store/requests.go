package store

import (
	"database/sql"
	"errors"
	"shioji/internal/domain"
)

// A request can refer only to work already accepted by this session and source
// execution context. Terminal request history remains readable after a stop.
func requestWork(q interface{ QueryRow(string, ...any) *sql.Row }, s domain.Session, r domain.PendingRequest) error {
	if r.WorkID == "" {
		return nil
	}
	w, err := load[domain.WorkActivity](q.QueryRow("SELECT body FROM works WHERE session_id=? AND id=?", s.ID, r.WorkID))
	if err != nil {
		return errors.New("请求来源不属于此会话")
	}
	if w.Kind != "agent" || w.TurnID != r.TurnID || w.OriginRef != "" && w.OriginRef != s.ThreadID {
		return errors.New("请求的执行来源已变化")
	}
	if r.State.AwaitingResolution() && (w.State == "stopped" || w.State == "failed" || w.State == "unknown") {
		return errors.New("请求来源已不可用")
	}
	return nil
}
func validateRequest(tx *sql.Tx, s domain.Session, r domain.PendingRequest) error {
	if r.ID == "" || r.SessionID != "" && r.SessionID != s.ID {
		return errors.New("请求不属于此会话")
	}
	if err := requestWork(tx, s, r); err != nil {
		return err
	}
	old, err := load[domain.PendingRequest](tx.QueryRow("SELECT body FROM requests WHERE session_id=? AND id=?", s.ID, r.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if old.WorkID != r.WorkID || old.TurnID != r.TurnID || old.ItemID != r.ItemID || old.Kind != r.Kind {
		return errors.New("请求来源不能更改")
	}
	if old.Revision > r.Revision || !old.State.AwaitingResolution() && r.State.AwaitingResolution() {
		return errors.New("请求状态已过期")
	}
	return nil
}
func mirrorRequestWork(tx *sql.Tx, sid string, e *domain.Event, item domain.Item) error {
	if item.WorkID == "" {
		return nil
	}
	child := item
	observation := domain.Event{Type: "work.item", WorkID: item.WorkID, WorkItem: &child}
	if err := applyWork(tx, sid, &observation); err != nil {
		return err
	}
	e.WorkID, e.WorkItem = item.WorkID, &child
	return nil
}

// ValidateAnswer is checked at the serialized worker dispatch boundary. The
// adapter still validates its actual choices and native runtime request token.
func (s *Store) ValidateAnswer(sid string, a domain.Answer) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", sid))
	if err != nil {
		return session, err
	}
	if session.ExecutionGuard != "" {
		return session, errors.New("执行状态尚待核对，暂不能提交回答")
	}
	r, err := load[domain.PendingRequest](s.db.QueryRow("SELECT body FROM requests WHERE session_id=? AND id=?", sid, a.RequestID))
	if err != nil {
		return session, errors.New("请求不属于此会话")
	}
	if r.State != "pending" || r.Revision != a.Revision {
		return session, errors.New("请求已提交或已失效")
	}
	return session, requestWork(s.db, session, r)
}
