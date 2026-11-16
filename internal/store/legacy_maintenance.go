package store

// Original receipt JSON is retained byte-for-byte in a separate row. json_set
// updates only the obsolete active workflow, preserving every unknown field.
func (s *Store) MigrateLegacyMaintenance(operation string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("CREATE TABLE IF NOT EXISTS legacy_worker_maintenance (id TEXT PRIMARY KEY, body BLOB NOT NULL)"); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT OR IGNORE INTO legacy_worker_maintenance SELECT id,body FROM worker_maintenance"); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE worker_maintenance SET body=json_set(body,'$.phase','failed','$.message',?,'$.revision',COALESCE(json_extract(body,'$.revision'),0)+1) WHERE json_extract(body,'$.phase') NOT IN ('completed','cancelled','failed')`, "旧流程已由固定服务更新 "+operation+" 接续；原始回执已保留"); err != nil {
		return err
	}
	return s.commit(tx)
}
