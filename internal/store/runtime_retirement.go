package store

import "database/sql"

type RuntimeRetirement struct {
	SessionID, Generation, Reason, Error string
	Revision                             int64
}

func putRuntimeRetirement(tx *sql.Tx, r RuntimeRetirement) error {
	_, err := tx.Exec("INSERT OR IGNORE INTO runtime_retirements(session_id,generation,revision,reason) VALUES(?,?,?,?)", r.SessionID, r.Generation, r.Revision, r.Reason)
	return err
}
func (s *Store) PendingRuntimeRetirements() ([]RuntimeRetirement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT session_id,generation,revision,reason,error FROM runtime_retirements ORDER BY session_id,revision")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuntimeRetirement
	for rows.Next() {
		var r RuntimeRetirement
		if err = rows.Scan(&r.SessionID, &r.Generation, &r.Revision, &r.Reason, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) CompleteRuntimeRetirement(r RuntimeRetirement, cause error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cause != nil {
		_, err := s.db.Exec("UPDATE runtime_retirements SET error=? WHERE session_id=? AND generation=? AND revision=?", cause.Error(), r.SessionID, r.Generation, r.Revision)
		return err
	}
	_, err := s.db.Exec("DELETE FROM runtime_retirements WHERE session_id=? AND generation=? AND revision=?", r.SessionID, r.Generation, r.Revision)
	return err
}

func (s *Store) RuntimeRetirementPending(r RuntimeRetirement) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var pending bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM runtime_retirements WHERE session_id=? AND generation=? AND revision=?)", r.SessionID, r.Generation, r.Revision).Scan(&pending)
	return pending, err
}
