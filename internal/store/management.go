package store

import (
	"database/sql"
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

// SessionRecord reads ownership/configuration without materializing transcript,
// work roster, requests or queue data needed only by a conversation view.
func (s *Store) SessionRecord(id string) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", id))
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
func (s *Store) saveSessionMetadataLocked(session domain.Session, retirements ...RuntimeRetirement) (domain.Session, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return session, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), session.ID); err != nil {
		return session, err
	}
	for _, retirement := range retirements {
		if retirement.Reason == "archive" {
			if err = pauseArchiveQueue(tx, retirement.SessionID); err != nil {
				return session, err
			}
		}
		if err = putRuntimeRetirement(tx, retirement); err != nil {
			return session, err
		}
	}
	if _, err = journal(tx, domain.Event{Type: "session", SessionID: session.ID, Session: &session}); err != nil {
		return session, err
	}
	return session, s.commit(tx)
}

func (s *Store) SetSessionArchived(id string, revision int64, archived bool, generation ...string) (domain.Session, error) {
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
	if !archived {
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
		withdrawReplyCompletion(&session)
	}
	session.Revision++
	if archived {
		gen := ""
		if len(generation) > 0 {
			gen = generation[0]
		}
		return s.saveSessionMetadataLocked(session, RuntimeRetirement{SessionID: id, Generation: gen, Revision: session.Revision, Reason: "archive"})
	}
	return s.saveSessionMetadataLocked(session)
}
func (s *Store) SetWorkspaceArchived(id string, revision int64, archived bool, generations ...map[string]string) (domain.Workspace, error) {
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
	var sessions []domain.Session
	if archived {
		var e error
		sessions, e = rowsOf[domain.Session](s.db, "SELECT body FROM sessions WHERE json_extract(body,'$.workspaceId')=?", id)
		if e != nil {
			return w, e
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
	if archived {
		for _, session := range sessions {
			if session.Completion != nil && session.Completion.Pending {
				withdrawReplyCompletion(&session)
				if err = saveCompletionSession(tx, &session); err != nil {
					return w, err
				}
			}
			gen := ""
			if len(generations) > 0 {
				gen = generations[0][session.ID]
			}
			if err = pauseArchiveQueue(tx, session.ID); err != nil {
				return w, err
			}
			if err = putRuntimeRetirement(tx, RuntimeRetirement{SessionID: session.ID, Generation: gen, Revision: w.Revision, Reason: "archive"}); err != nil {
				return w, err
			}
		}
	}
	if _, err = journal(tx, domain.Event{Type: "workspace", Workspace: &w}); err != nil {
		return w, err
	}
	return w, s.commit(tx)
}

func pauseArchiveQueue(tx *sql.Tx, id string) error {
	q, err := load[domain.QueueState](tx.QueryRow("SELECT body FROM queues WHERE session_id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		q, err = domain.QueueState{SessionID: id}, nil
	}
	if err != nil || q.Paused {
		return err
	}
	q.Paused, q.Reason = true, "archived"
	q.Revision++
	return putQueue(tx, q)
}
