package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryIndexesAreAvailableAfterReopeningExistingStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"works_history_roster", "items_history_round_starts"} {
		if _, err = s.db.Exec("DROP INDEX IF EXISTS " + name); err != nil {
			s.Close()
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tt := range []struct {
		index string
		query string
		args  []any
	}{
		{"works_history_roster", workRosterSQL, []any{"session"}},
		{"items_history_round_starts", `SELECT position FROM items WHERE session_id=? AND ` + historyRoundStartSQL + ` AND position<=? ORDER BY position DESC LIMIT ?`, []any{"session", 10000, 21}},
	} {
		t.Run(tt.index, func(t *testing.T) {
			rows, err := s.db.Query("EXPLAIN QUERY PLAN "+tt.query, tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			detail := strings.Join(plan, "\n")
			if !strings.Contains(detail, tt.index) || strings.Contains(detail, "TEMP B-TREE") {
				t.Fatalf("history lookup does not use its ordered index: %s", detail)
			}
		})
	}
}
