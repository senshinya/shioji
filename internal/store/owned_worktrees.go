package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"shioji/internal/domain"
)

// OwnedWorktree records only checkouts created by this worker. It survives
// session deletion so a failed backup/removal remains retryable after restart.
type OwnedWorktree struct {
	Path, CommonDir, Branch, Head   string
	CreationOperation, CreationBase string
	Removed                         bool
	DeletedSessions                 []string
}

func (s *Store) OwnedWorktrees() ([]OwnedWorktree, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return rowsOf[OwnedWorktree](s.db, "SELECT body FROM owned_worktrees ORDER BY path")
}
func (s *Store) SaveOwnedWorktree(w OwnedWorktree) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := load[OwnedWorktree](s.db.QueryRow("SELECT body FROM owned_worktrees WHERE path=?", w.Path))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, id := range old.DeletedSessions {
		if !slices.Contains(w.DeletedSessions, id) {
			w.DeletedSessions = append(w.DeletedSessions, id)
		}
	}
	_, err = s.db.Exec("INSERT INTO owned_worktrees(path,body) VALUES(?,?) ON CONFLICT(path) DO UPDATE SET body=excluded.body", w.Path, blob(w))
	return err
}
func (s *Store) ForgetOwnedWorktree(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM owned_worktrees WHERE path=?", path)
	return err
}
func WorktreeContains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func WorktreeSession(w OwnedWorktree, s domain.Session) bool {
	for _, path := range append([]string{s.Context.CWD}, s.Context.AdditionalDirectories...) {
		if WorktreeContains(w.Path, path) {
			return true
		}
	}
	return false
}

// Persist the actual branch and backup commit before removing its only checkout.
// The worker gates every referencing session until this transaction completes.
func (s *Store) RecordWorktreeBackup(w OwnedWorktree) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := load[OwnedWorktree](s.db.QueryRow("SELECT body FROM owned_worktrees WHERE path=?", w.Path))
	if err != nil {
		return err
	}
	for _, id := range old.DeletedSessions {
		if !slices.Contains(w.DeletedSessions, id) {
			w.DeletedSessions = append(w.DeletedSessions, id)
		}
	}
	sessions, err := rowsOf[domain.Session](s.db, "SELECT body FROM sessions")
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE owned_worktrees SET body=? WHERE path=?", blob(w), w.Path); err != nil {
		return err
	}
	for _, session := range sessions {
		if !WorktreeContains(w.Path, session.Context.CWD) {
			continue
		}
		session.GitBranch, session.GitHead = w.Branch, w.Head
		session.ContextChanged = false
		session.Revision++
		if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), session.ID); err != nil {
			return err
		}
		if _, err = journal(tx, domain.Event{Type: "session", SessionID: session.ID, Session: &session}); err != nil {
			return err
		}
	}
	return s.commit(tx)
}

// Worker operation IDs are provenance for earlier checkouts whose directory is
// derived from the accepted creation receipt. Harness source IDs are unrelated.
func (s *Store) OperationIdentities() ([]domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT id,session_id FROM operations UNION SELECT id,session_id FROM deleted_operations WHERE session_id<>''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Operation
	for rows.Next() {
		var op domain.Operation
		if err = rows.Scan(&op.ID, &op.SessionID); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}
