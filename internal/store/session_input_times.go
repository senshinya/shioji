package store

import (
	"database/sql"
	"time"
)

const sessionInputDate = "json_extract(body,'$.sentAt')"
const sessionInputEpoch = "unixepoch(" + sessionInputDate + ",'subsec')"
const recordedSessionInput = historyPromptSQL + " AND " + sessionInputEpoch + " IS NOT NULL"
const latestSessionInputDate = `(SELECT ` + sessionInputDate + ` FROM items
	WHERE session_id=s.id AND ` + recordedSessionInput + `
	ORDER BY ` + sessionInputEpoch + ` DESC,position DESC LIMIT 1)`

// The session catalog includes dates from existing history, without reading
// message bodies or changing their journal positions.
func (s *Store) snapshotSessionInputTimes() (map[string]time.Time, error) {
	rows, err := s.db.Query("SELECT s.id, " + latestSessionInputDate + " FROM sessions AS s")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dates := make(map[string]time.Time)
	for rows.Next() {
		var id string
		var stamp sql.NullString
		if err := rows.Scan(&id, &stamp); err != nil {
			return nil, err
		}
		if stamp.Valid {
			if value, err := time.Parse(time.RFC3339Nano, stamp.String); err == nil {
				dates[id] = value.UTC()
			}
		}
	}
	return dates, rows.Err()
}
