package store

// ArchiveGarbage is a durable cleanup outbox. Only worker-owned paths are
// represented; user repositories and harness-owned source history are excluded.
type ArchiveGarbage struct {
	SessionID, WorkspaceID, AttachmentID string
}

func (s *Store) ArchiveGarbage() ([]ArchiveGarbage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id,workspace_id,'' FROM deleted_sessions WHERE cleaned=0
 UNION ALL SELECT '',id,'' FROM deleted_workspaces WHERE cleaned=0
 UNION ALL SELECT '',workspace_id,attachment_id FROM archive_resources LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ArchiveGarbage
	for rows.Next() {
		var item ArchiveGarbage
		if err = rows.Scan(&item.SessionID, &item.WorkspaceID, &item.AttachmentID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ArchiveResourceReferenced(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, table := range []string{"items", "queues", "work_items", "work_inputs", "history_operations"} {
		var found bool
		if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM "+table+" WHERE instr(CAST(body AS TEXT),?)>0)", id).Scan(&found); err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) FinishArchiveGarbage(item ArchiveGarbage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	switch {
	case item.SessionID != "":
		_, err = s.db.Exec("UPDATE deleted_sessions SET cleaned=1 WHERE id=?", item.SessionID)
	case item.AttachmentID != "":
		_, err = s.db.Exec("DELETE FROM archive_resources WHERE workspace_id=? AND attachment_id=?", item.WorkspaceID, item.AttachmentID)
	default:
		_, err = s.db.Exec("UPDATE deleted_workspaces SET cleaned=1 WHERE id=?", item.WorkspaceID)
	}
	return err
}
