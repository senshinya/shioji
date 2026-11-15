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

func (s *Store) ArchiveResourceReferenced(workspace, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, table := range []string{"items", "queues", "work_items", "work_inputs", "history_operations"} {
		var found bool
		where := "session_id IN (SELECT id FROM sessions WHERE json_extract(body,'$.workspaceId')=?)"
		if table == "history_operations" {
			where = "(json_extract(body,'$.source.workspaceId')=? OR json_extract(body,'$.session.workspaceId')=?1)"
		}
		if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM "+table+" WHERE "+where+" AND instr(CAST(body AS TEXT),?2)>0)", workspace, id).Scan(&found); err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) ScheduleArchiveResources(workspace string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err = tx.Exec("INSERT OR IGNORE INTO archive_resources(workspace_id,attachment_id) VALUES(?,?)", workspace, id); err != nil {
			return err
		}
	}
	return s.commit(tx)
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
