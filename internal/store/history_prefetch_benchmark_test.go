package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"shioji/internal/domain"
)

func BenchmarkOlderHistoryPageWithinLongSession(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "history.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	session := domain.Session{ID: "history"}
	tx, err := s.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO sessions(id,body) VALUES(?,?)", session.ID, blob(session)); err != nil {
		b.Fatal(err)
	}
	output := strings.Repeat("x", 8192)
	for i := 1; i <= 6000; i++ {
		item := domain.Item{ID: fmt.Sprint(i), Order: int64(i), Kind: "assistant", Text: output, Final: true}
		if i%10 == 1 {
			item.Kind, item.Text = "user", "prompt"
		}
		if _, err := tx.Exec("INSERT INTO items(session_id,id,position,body) VALUES(?,?,?,?)", session.ID, item.ID, item.Order, blob(item)); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	q := domain.HistoryQuery{SessionID: session.ID, Rounds: 5, Before: 5851}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, err := s.SessionHistory(q)
		if err != nil || len(page.Items) != 50 || page.History.First != 5801 || page.History.Last != 5850 {
			b.Fatalf("unexpected page of %d items (%+v): %v", len(page.Items), page.History, err)
		}
	}
}
