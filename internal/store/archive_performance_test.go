package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
)

// Deleting one archive must not parse every other conversation's event body.
func TestArchiveRedactionUsesOwnershipIndexes(t *testing.T) {
	s, _, _ := archiveFixture(t)
	assertArchiveRedactionIndexes(t, s)
}

func assertArchiveRedactionIndexes(t *testing.T, s *Store) {
	t.Helper()
	for _, query := range []string{redactSessionJournal, redactWorkspaceJournal} {
		args := []any{blob(domain.Event{Type: "archive.redacted"}), "target"}
		if strings.Contains(query, " OR ") {
			args = append(args, "target", "target")
		}
		rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN journal") {
				t.Error("archive deletion scans unrelated history:", detail)
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
}

func TestArchiveIndexesBackfillExistingJournalWithoutChangingCursors(t *testing.T) {
	s, _, session := archiveFixture(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"journal_session", "journal_session_object", "journal_operation_session", "journal_workspace"} {
		if _, err = tx.Exec("DROP INDEX " + name); err != nil {
			t.Fatal(err)
		}
	}
	var ids []int64
	for _, event := range []domain.Event{
		{Type: "item.upsert", SessionID: session.ID, Text: "private archive text"},
		{Type: "session", Session: &session, Text: "private archive text"},
		{Type: "operation", Operation: &domain.Operation{SessionID: session.ID}, Text: "private archive text"},
	} {
		e, err := journal(tx, event)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.Sequence)
	}
	kept, err := journal(tx, domain.Event{Type: "item.upsert", SessionID: "keep", Text: "retained history"})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertArchiveRedactionIndexes(t, reopened)
	archived, err := reopened.SetSessionArchived(session.ID, session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.DeleteArchivedSession(session.ID, archived.Revision); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		e, err := load[domain.Event](reopened.db.QueryRow("SELECT body FROM journal WHERE sequence=?", id))
		if err != nil || e.Type != "archive.redacted" || e.Text != "" {
			t.Fatal("indexed deletion missed a legacy ownership shape", id, e, err)
		}
	}
	e, err := load[domain.Event](reopened.db.QueryRow("SELECT body FROM journal WHERE sequence=?", kept.Sequence))
	if err != nil || e.Text != "retained history" {
		t.Fatal("indexed deletion redacted another session", e, err)
	}
}

// Keep fixture creation outside the measurement. The retained conversation
// carries 80 MiB of history, while each of the 16 deleted sessions is small.
func BenchmarkArchiveDeleteWithUnrelatedHistory(b *testing.B) {
	b.Run("unindexed", func(b *testing.B) { benchmarkArchiveDelete(b, false) })
	b.Run("indexed", func(b *testing.B) { benchmarkArchiveDelete(b, true) })
}

func benchmarkArchiveDelete(b *testing.B, indexed bool) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		s, err := Open(filepath.Join(b.TempDir(), "state.sqlite"))
		if err != nil {
			b.Fatal(err)
		}
		if !indexed {
			for _, name := range []string{"journal_session", "journal_session_object", "journal_operation_session", "journal_workspace"} {
				if _, err = s.db.Exec("DROP INDEX " + name); err != nil {
					b.Fatal(err)
				}
			}
		}
		w, err := s.CreateWorkspace("Keep", []string{b.TempDir()})
		if err != nil {
			b.Fatal(err)
		}
		tx, err := s.db.Begin()
		if err != nil {
			b.Fatal(err)
		}
		body := blob(domain.Event{Type: "item.upsert", SessionID: "keep", Item: &domain.Item{ID: "keep", Text: strings.Repeat("x", 8192)}})
		for j := 0; j < 10000; j++ {
			if _, err = tx.Exec("INSERT INTO journal(body) VALUES(?)", body); err != nil {
				b.Fatal(err)
			}
		}
		var sessions []domain.Session
		for j := 0; j < 16; j++ {
			session := domain.Session{ID: fmt.Sprint("delete-", j), WorkspaceID: w.ID, Archived: true, Revision: 1}
			if _, err = tx.Exec("INSERT INTO sessions(id,body) VALUES(?,?)", session.ID, blob(session)); err != nil {
				b.Fatal(err)
			}
			if _, err = journal(tx, domain.Event{Type: "session", SessionID: session.ID, Session: &session}); err != nil {
				b.Fatal(err)
			}
			sessions = append(sessions, session)
		}
		if err = tx.Commit(); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		start := time.Now()
		for _, session := range sessions {
			if _, err = s.DeleteArchivedSession(session.ID, session.Revision); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(time.Since(start).Microseconds())/16, "us/session")
		b.StopTimer()
		s.Close()
	}
}
