package store

import (
	"errors"
	"shioji/internal/domain"
)

func (s *Store) StopInitializing(id string) error {
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
	if session.Status != "starting" {
		return errors.New("session initialization already changed")
	}
	if _, err = tx.Exec("UPDATE operations SET state='rejected',error='初始化已停止，输入未发送' WHERE session_id=? AND state='accepted'", id); err != nil {
		return err
	}
	session.Status = "interrupted"
	session.Error = "初始化已停止，输入未发送"
	session.Revision++
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), id); err != nil {
		return err
	}
	if _, err = journal(tx, domain.Event{Type: "session", SessionID: id, Session: &session}); err != nil {
		return err
	}
	return s.commit(tx)
}
