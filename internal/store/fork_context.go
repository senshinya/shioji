package store

import (
	"slices"

	"shioji/internal/domain"
)

// PortableForkContext reads only the copied prefix in the fork itself. Once
// an input was handed off, a later input cannot seed the runtime again.
func (s *Store) PortableForkContext(id, input string) (domain.ForkContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var prefix domain.ForkContext
	session, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	if err != nil || !session.PortableFork {
		return prefix, err
	}
	var seeded bool
	err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM operations
		WHERE session_id=? AND id!=? AND state IN ('dispatched','confirmed','indeterminate','abandoned')
		AND kind=?)`, id, input, ordinarySubmissionKind).Scan(&seeded)
	if err != nil || seeded {
		return prefix, err
	}
	const maxEntries = 32
	rows, err := s.db.Query(`
		SELECT json_extract(i.body,'$.kind'), substr(json_extract(i.body,'$.text'),1,8193)
		FROM items i JOIN turns t ON t.session_id=i.session_id AND t.id=json_extract(i.body,'$.turnId')
		WHERE i.session_id=? AND json_extract(i.body,'$.kind') IN ('user','assistant')
			AND length(json_extract(i.body,'$.text'))>0
			AND json_extract(t.body,'$.order')<=(SELECT json_extract(body,'$.order') FROM turns WHERE session_id=? AND id=?)
		ORDER BY i.position DESC LIMIT ?`, id, id, session.ForkedTurn, maxEntries+1)
	if err != nil {
		return prefix, err
	}
	defer rows.Close()
	remaining := 32 << 10
	for rows.Next() {
		var entry domain.ForkContextEntry
		if err := rows.Scan(&entry.Role, &entry.Text); err != nil {
			return prefix, err
		}
		if len(prefix.Entries) == maxEntries || remaining <= 0 {
			prefix.Omitted = true
			break
		}
		limited := domain.ClipDisplayText(entry.Text, min(remaining, 8192))
		if limited != entry.Text {
			prefix.Omitted = true
		}
		entry.Text = limited
		remaining -= len(entry.Text)
		prefix.Entries = append(prefix.Entries, entry)
	}
	slices.Reverse(prefix.Entries)
	return prefix, rows.Err()
}
