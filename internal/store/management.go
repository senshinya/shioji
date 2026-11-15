package store

import (
	"errors"
	"shioji/internal/domain"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Store) Workspace(id string) (domain.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return load[domain.Workspace](s.db.QueryRow("SELECT body FROM workspaces WHERE id=?", id))
}

func (s *Store) RenameSession(id, title string, revision int64, useSource bool) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	if err != nil {
		return session, err
	}
	if session.TitleRevision != revision {
		return session, errors.New("会话已改变，请重新载入后再试")
	}
	if useSource {
		if session.SourceTitle == "" {
			return session, errors.New("Agent 尚未提供会话名称")
		}
		title = session.SourceTitle
	}
	title = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(title))
	if !utf8.ValidString(title) || title == "" || len(title) > 4096 {
		return session, errors.New("请输入有效的会话名称（最多 4096 字节）")
	}
	session.Title, session.TitleOverride = title, !useSource
	session.TitleRevision++
	session.Revision++
	return s.saveSessionMetadataLocked(session)
}
func (s *Store) saveSessionMetadataLocked(session domain.Session) (domain.Session, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return session, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), session.ID); err != nil {
		return session, err
	}
	if _, err = journal(tx, domain.Event{Type: "session", SessionID: session.ID, Session: &session}); err != nil {
		return session, err
	}
	return session, s.commit(tx)
}
func (s *Store) archivableLocked(session domain.Session) error {
	switch session.Status {
	case "idle", "completed", "interrupted", "failed", "abandoned":
	default:
		return errors.New("会话仍在运行或执行状态待核对，请先处理")
	}
	var pending bool
	if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM requests WHERE session_id=? AND json_extract(body,'$.state') IN ('pending','submitting'))", session.ID).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return errors.New("会话仍有待处理请求")
	}
	if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM operations WHERE session_id=? AND state IN ('accepted','dispatched','indeterminate'))", session.ID).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return errors.New("会话仍有未核实的投递")
	}
	q, err := s.queueLocked(session.ID)
	if err != nil {
		return err
	}
	if len(q.Items) > 0 {
		return errors.New("会话仍有待发送消息，请先处理队列")
	}
	return nil
}
func (s *Store) SetSessionArchived(id string, revision int64, archived bool) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	if err != nil {
		return session, err
	}
	if session.Revision != revision {
		return session, errors.New("会话已改变，请重新载入后再试")
	}
	if session.Archived == archived {
		return session, nil
	}
	if archived {
		if err = s.archivableLocked(session); err != nil {
			return session, err
		}
	} else {
		w, e := load[domain.Workspace](s.db.QueryRow("SELECT body FROM workspaces WHERE id=?", session.WorkspaceID))
		if e != nil {
			return session, e
		}
		if w.Archived {
			return session, errors.New("请先恢复所属工作区")
		}
	}
	session.Archived = archived
	session.ArchivedAt = nil
	if archived {
		now := time.Now().UTC()
		session.ArchivedAt = &now
	}
	session.Revision++
	return s.saveSessionMetadataLocked(session)
}
func (s *Store) SetWorkspaceArchived(id string, revision int64, archived bool) (domain.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := load[domain.Workspace](s.db.QueryRow("SELECT body FROM workspaces WHERE id=?", id))
	if err != nil {
		return w, err
	}
	if w.Revision != revision {
		return w, errors.New("工作区已改变，请重新载入后再试")
	}
	if w.Archived == archived {
		return w, nil
	}
	if archived {
		sessions, e := rowsOf[domain.Session](s.db, "SELECT body FROM sessions WHERE json_extract(body,'$.workspaceId')=?", id)
		if e != nil {
			return w, e
		}
		for _, session := range sessions {
			if e = s.archivableLocked(session); e != nil {
				return w, e
			}
		}
	}
	w.Archived = archived
	w.ArchivedAt = nil
	if archived {
		now := time.Now().UTC()
		w.ArchivedAt = &now
	}
	w.Revision++
	tx, err := s.db.Begin()
	if err != nil {
		return w, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE workspaces SET body=? WHERE id=?", blob(w), id); err != nil {
		return w, err
	}
	if _, err = journal(tx, domain.Event{Type: "workspace", Workspace: &w}); err != nil {
		return w, err
	}
	return w, s.commit(tx)
}
