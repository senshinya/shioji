package store

import (
	"path/filepath"
	"strings"
	"testing"

	"shioji/internal/domain"
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
	project, err := s.CreateWorkspace("旧工作区", []string{t.TempDir()})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	input, err := s.Accept(domain.Submission{OperationID: "existing", WorkspaceID: project.ID, Text: "消息已保存在旧数据库里", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	work := domain.WorkActivity{ID: "existing-work", Kind: "background", TurnID: "existing-turn", State: "running"}
	if _, err := s.Apply(input.SessionID, domain.Event{Type: "work.upsert", Work: &work}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	page, err := s.SessionHistory(domain.HistoryQuery{SessionID: input.SessionID, Rounds: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Text != "消息已保存在旧数据库里" || len(page.Works) != 1 || page.Works[0].ID != work.ID {
		t.Fatalf("reopening the store must preserve the history and its work roster: %+v, %v", page, err)
	}
	for _, tt := range []struct {
		index string
		query string
		args  []any
	}{
		{"works_history_roster", workRosterSQL, []any{input.SessionID}},
		{"items_history_round_starts", `SELECT position FROM items WHERE session_id=? AND ` + historyRoundMarkerSQL + ` AND position<=? ORDER BY position DESC LIMIT ?`, []any{input.SessionID, 10000, 21}},
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
