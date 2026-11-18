package store

import (
	"database/sql"
	"errors"
	"shioji/internal/domain"
	"time"
)

type ArchiveCandidate struct {
	WorkspaceID, SessionID string
	Revision               int64
}

func (s *Store) archivePolicyLocked() (domain.ArchivePolicy, error) {
	return load[domain.ArchivePolicy](s.db.QueryRow("SELECT body FROM archive_policy WHERE id=1"))
}

func (s *Store) ArchivePolicy() (domain.ArchivePolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.archivePolicyLocked()
}

func (s *Store) ConfigureArchive(days int, revision int64) (domain.ArchivePolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, err := s.archivePolicyLocked()
	if err != nil {
		return policy, err
	}
	if !domain.ValidArchiveDays(days) {
		return policy, errors.New("归档保留时间只支持 7 天、14 天、30 天或永不")
	}
	// A receipt lost during reconnect is safe to confirm when the intended
	// value is already authoritative, without reapplying a stale mutation.
	if policy.Days == days {
		return policy, nil
	}
	if policy.Revision != revision {
		return policy, errors.New("设备的归档保留时间已改变，请重新选择后再试")
	}
	policy.Days = days
	policy.Revision++
	tx, err := s.db.Begin()
	if err != nil {
		return policy, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE archive_policy SET body=? WHERE id=1", blob(policy)); err != nil {
		return policy, err
	}
	if _, err = journal(tx, domain.Event{Type: "archive.policy", ArchivePolicy: &policy}); err != nil {
		return policy, err
	}
	return policy, s.commit(tx)
}

func (s *Store) ArchiveCandidates(now time.Time) ([]ArchiveCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, err := s.archivePolicyLocked()
	if err != nil || policy.Days == 0 {
		return nil, err
	}
	workspaces, err := rowsOf[domain.Workspace](s.db, "SELECT body FROM workspaces WHERE json_extract(body,'$.archived')=1")
	if err != nil {
		return nil, err
	}
	var out []ArchiveCandidate
	expired := func(at *time.Time) bool {
		return at != nil && !now.Before(at.Add(time.Duration(policy.Days)*24*time.Hour))
	}
	whole := map[string]bool{}
	for _, w := range workspaces {
		if expired(w.ArchivedAt) {
			whole[w.ID] = true
			out = append(out, ArchiveCandidate{WorkspaceID: w.ID, Revision: w.Revision})
		}
	}
	sessions, err := rowsOf[domain.Session](s.db, "SELECT body FROM sessions WHERE json_extract(body,'$.archived')=1")
	if err != nil {
		return nil, err
	}
	for _, session := range sessions {
		if !whole[session.WorkspaceID] && expired(session.ArchivedAt) {
			out = append(out, ArchiveCandidate{SessionID: session.ID, Revision: session.Revision})
		}
	}
	return out, nil
}

func (s *Store) DeleteArchivedSession(id string, revision int64) (domain.ArchiveDeletion, error) {
	return s.deleteArchivedSession(id, revision, false)
}

func (s *Store) ForceDeleteArchivedSession(id string) (domain.ArchiveDeletion, error) {
	return s.deleteArchivedSession(id, 0, true)
}

func (s *Store) deleteArchivedSession(id string, revision int64, force bool) (domain.ArchiveDeletion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var zero domain.ArchiveDeletion
	session, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	if err != nil {
		return zero, err
	}
	if !force && session.Revision != revision {
		return zero, errors.New("会话已改变，请重新载入后再试")
	}
	w, err := load[domain.Workspace](s.db.QueryRow("SELECT body FROM workspaces WHERE id=?", session.WorkspaceID))
	if err != nil {
		return zero, err
	}
	if !session.Archived && !w.Archived {
		return zero, errors.New("只能彻底删除已归档会话")
	}
	return s.deleteArchivedLocked([]domain.Session{session}, "")
}

func (s *Store) DeleteArchivedWorkspace(id string, revision int64) (domain.ArchiveDeletion, error) {
	return s.deleteArchivedWorkspace(id, revision, false)
}

func (s *Store) ForceDeleteArchivedWorkspace(id string) (domain.ArchiveDeletion, error) {
	return s.deleteArchivedWorkspace(id, 0, true)
}

func (s *Store) deleteArchivedWorkspace(id string, revision int64, force bool) (domain.ArchiveDeletion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var zero domain.ArchiveDeletion
	w, err := load[domain.Workspace](s.db.QueryRow("SELECT body FROM workspaces WHERE id=?", id))
	if err != nil {
		return zero, err
	}
	if !force && w.Revision != revision {
		return zero, errors.New("工作区已改变，请重新载入后再试")
	}
	if !w.Archived {
		return zero, errors.New("只能彻底删除已归档工作区")
	}
	sessions, err := rowsOf[domain.Session](s.db, "SELECT body FROM sessions WHERE json_extract(body,'$.workspaceId')=? ORDER BY rowid", id)
	if err != nil {
		return zero, err
	}
	return s.deleteArchivedLocked(sessions, id)
}

const redactSessionJournal = "UPDATE journal SET body=? WHERE json_extract(body,'$.sessionId')=? OR json_extract(body,'$.session.id')=? OR json_extract(body,'$.operation.sessionId')=?"
const redactWorkspaceJournal = "UPDATE journal SET body=? WHERE json_extract(body,'$.workspace.id')=?"

func (s *Store) deleteArchivedLocked(sessions []domain.Session, workspace string) (domain.ArchiveDeletion, error) {
	out := domain.ArchiveDeletion{DeviceID: s.deviceID, WorkspaceID: workspace, SessionIDs: []string{}}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for _, session := range sessions {
		if err = deleteSessionRows(tx, session); err != nil {
			return out, err
		}
		out.SessionIDs = append(out.SessionIDs, session.ID)
	}
	if workspace != "" {
		if _, err = tx.Exec("INSERT OR IGNORE INTO deleted_workspaces(id) VALUES(?)", workspace); err != nil {
			return out, err
		}
		if _, err = tx.Exec(redactWorkspaceJournal, blob(domain.Event{Type: "archive.redacted"}), workspace); err != nil {
			return out, err
		}
		if _, err = tx.Exec("DELETE FROM workspaces WHERE id=?", workspace); err != nil {
			return out, err
		}
	}
	if _, err = journal(tx, domain.Event{Type: "archive.deleted", Deletion: &out}); err != nil {
		return out, err
	}
	return out, s.commit(tx)
}

func deleteSessionRows(tx *sql.Tx, session domain.Session) error {
	owned, err := rowsOf[OwnedWorktree](tx, "SELECT body FROM owned_worktrees")
	if err != nil {
		return err
	}
	for _, w := range owned {
		if WorktreeSession(w, session) {
			w.DeletedSessions = append(w.DeletedSessions, session.ID)
			if _, err = tx.Exec("UPDATE owned_worktrees SET body=? WHERE path=?", blob(w), w.Path); err != nil {
				return err
			}
		}
	}
	for _, table := range []string{"items", "queues", "work_items", "work_inputs"} {
		object := "CASE WHEN j.type='object' THEN j.value ELSE '{}' END"
		query := "INSERT OR IGNORE INTO archive_resources SELECT ?,json_extract(" + object + ",'$.id') FROM " + table + " AS r,json_tree(r.body) AS j WHERE r.session_id=? AND json_type(" + object + ",'$.sha256')='text' AND json_type(" + object + ",'$.id')='text'"
		if _, err := tx.Exec(query, session.WorkspaceID, session.ID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("INSERT OR IGNORE INTO deleted_sessions(id,workspace_id) VALUES(?,?)", session.ID, session.WorkspaceID); err != nil {
		return err
	}
	for _, table := range []string{"operations", "work_inputs"} {
		if _, err := tx.Exec("INSERT OR IGNORE INTO deleted_operations SELECT id,session_id FROM "+table+" WHERE session_id=?", session.ID); err != nil {
			return err
		}
	}
	historyWhere := "json_extract(body,'$.source.id')=? OR json_extract(body,'$.session.id')=?"
	if _, err := tx.Exec("INSERT OR IGNORE INTO deleted_operations SELECT id,? FROM history_operations WHERE "+historyWhere, session.ID, session.ID, session.ID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM history_operations WHERE "+historyWhere, session.ID, session.ID); err != nil {
		return err
	}
	// The tombstone takes over runtime cleanup, including a retirement already
	// selected by a collector. Deleted sessions must never poison that queue.
	for _, table := range []string{"runtime_retirements", "work_inputs", "works", "title_jobs", "plans", "queues", "requests", "turns", "items", "operations"} {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE session_id=?", session.ID); err != nil {
			return err
		}
	}
	// Redact in place rather than dropping sequence numbers: reconnecting
	// clients can still consume every journal cursor without retaining text.
	if _, err := tx.Exec(redactSessionJournal, blob(domain.Event{Type: "archive.redacted"}), session.ID, session.ID, session.ID); err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM sessions WHERE id=?", session.ID)
	return err
}
