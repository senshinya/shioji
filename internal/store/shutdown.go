package store

import (
	"context"
	"shioji/internal/domain"
	"time"
)

// The shutdown coordinator writes independently of a stuck runtime finalizer.
// The caller's last one-second budget also bounds mutex/SQLite acquisition.
func (s *Store) RecordShutdownUnknown(ctx context.Context) error {
	for !s.mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE operations SET state='indeterminate', error='执行服务退出未确认；输入不会重发' WHERE state IN ('accepted','dispatched')"); err != nil {
		return err
	}
	sessions, err := rowsOf[domain.Session](tx, "SELECT body FROM sessions WHERE json_extract(body,'$.status') IN ('running','starting','unknown')")
	if err != nil {
		return err
	}
	for _, session := range sessions {
		session.Status = "unknown"
		session.Error = "执行服务退出未确认；正在核对来源历史"
		session.Revision++
		if _, err = tx.ExecContext(ctx, "UPDATE sessions SET body=? WHERE id=?", blob(session), session.ID); err != nil {
			return err
		}
		if _, err = journal(tx, domain.Event{Type: "session", SessionID: session.ID, Session: &session}); err != nil {
			return err
		}
		q, e := load[domain.QueueState](tx.QueryRowContext(ctx, "SELECT body FROM queues WHERE session_id=?", session.ID))
		if e != nil {
			q = domain.QueueState{SessionID: session.ID}
		}
		q.Paused = true
		q.Reason = "退出未确认，等待核对"
		q.Revision++
		if err = putQueue(tx, q); err != nil {
			return err
		}
	}
	return s.commit(tx)
}
