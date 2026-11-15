package store_test

import (
	"encoding/json"
	"fmt"
	"os"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestHistoryWindowByteBudgetAndSourceFences(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "bounded-history", WorkspaceID: w.ID, Text: "question", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 120; i++ {
		_, err = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: fmt.Sprint(i), Kind: "assistant", Text: strings.Repeat("body ", 4096), Final: true}})
		if err != nil {
			t.Fatal(err)
		}
	}
	d, err := s.SessionHistory(domain.HistoryQuery{SessionID: op.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(d)
	if len(b) > 768<<10 || len(d.Items) >= 101 || d.Items[len(d.Items)-1].ID != "119" || !d.History.Older || d.History.Newer {
		t.Fatal("latest byte budget did not preserve the newest item", len(b), len(d.Items), d.History)
	}
	for _, q := range []domain.HistoryQuery{{SessionID: op.SessionID, Generation: "wrong-generation"}, {SessionID: op.SessionID, ThreadID: "wrong-source"}} {
		if _, err = s.SessionHistory(q); err == nil {
			t.Fatal("page accepted obsolete source", q)
		}
		if _, err = s.HistoryOutline(q); err == nil {
			t.Fatal("outline accepted obsolete source", q)
		}
		if _, err = s.HistoryMatches(domain.HistorySearchQuery{HistoryQuery: q, Query: "body"}); err == nil {
			t.Fatal("search accepted obsolete source", q)
		}
	}
	outline, err := s.HistoryOutline(domain.HistoryQuery{SessionID: op.SessionID, Limit: 1})
	if err != nil || len(outline.Prompts) != 1 || outline.Prompts[0].Text != "question" {
		t.Fatal(outline, err)
	}
}

func TestHistorySearchDoesNotAcquireTheExecutionStoreLock(t *testing.T) {
	// The test also checks a real WAL reader can run while writes are committed.
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "parallel-search", WorkspaceID: w.ID, Text: "needle", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 40; i++ {
			if _, e := s.HistoryMatches(domain.HistorySearchQuery{HistoryQuery: domain.HistoryQuery{SessionID: op.SessionID}, Query: "needle"}); e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 40; i++ {
		if _, err = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: fmt.Sprint(i), Kind: "assistant", Text: "ongoing"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestHistoryWindowMeasuredPayload(t *testing.T) {
	if os.Getenv("SHIOJI_HISTORY_MEASURE") == "" {
		t.Skip("opt-in payload measurement")
	}
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "measure", WorkspaceID: w.ID, Text: "Review the long running task", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8000; i++ {
		_, err = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: fmt.Sprint(i), Kind: "tool", Name: "go test ./internal/...", Text: strings.Repeat("fixture command output\n", 140), Final: true}})
		if err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	pages, bytes := 0, 0
	var cursor int64
	for {
		d, e := s.Session(op.SessionID, cursor, 100)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(d)
		pages++
		bytes += len(b)
		if !d.HasMore {
			break
		}
		cursor = d.Items[len(d.Items)-1].Order
	}
	oldTime := time.Since(start)
	start = time.Now()
	d, err := s.SessionHistory(domain.HistoryQuery{SessionID: op.SessionID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(d)
	t.Logf("fixture=8001 records legacy=%d pages %d bytes %s latest=%d records %d bytes %s tool-total=%d", pages, bytes, oldTime, len(d.Items), len(b), time.Since(start), d.History.Groups[0].Count)
}

func TestHistoryBudgetRetainsThePromptAtItsFinalBoundary(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, e := s.Accept(domain.Submission{OperationID: "context", WorkspaceID: w.ID, Text: "earlier question", Config: domain.RunConfig{Model: "test"}})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 110; i++ {
		item := domain.Item{ID: fmt.Sprint(i), Kind: "assistant", Text: strings.Repeat("output ", 8192), Final: true}
		if i == 40 {
			item.Kind = "user"
			item.Text = "correct current question"
		}
		if _, e = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &item}); e != nil {
			t.Fatal(e)
		}
	}
	d, e := s.SessionHistory(domain.HistoryQuery{SessionID: op.SessionID})
	if e != nil {
		t.Fatal(e)
	}
	if d.History.ContextID != "40" || d.Items[0].Text != "correct current question" || d.Items[len(d.Items)-1].ID != "109" {
		t.Fatal("trimming kept the earlier turn context", d.History)
	}
}

func TestHistoryOutlinePagesOnlyUserMetadata(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, e := s.Accept(domain.Submission{OperationID: "outline", WorkspaceID: w.ID, Text: "first", Config: domain.RunConfig{Model: "test"}})
	if e != nil {
		t.Fatal(e)
	}
	for i := 1; i < 4; i++ {
		if _, e = s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: fmt.Sprint(i), Kind: "user", Text: strings.Repeat("问", 200)}}); e != nil {
			t.Fatal(e)
		}
	}
	q := domain.HistoryQuery{SessionID: op.SessionID, Limit: 2}
	a, e := s.HistoryOutline(q)
	if e != nil || len(a.Prompts) != 2 || !a.Older || len([]rune(a.Prompts[0].Text)) != 120 {
		t.Fatal(a, e)
	}
	q.Before = a.Prompts[len(a.Prompts)-1].Order
	b, e := s.HistoryOutline(q)
	if e != nil || len(b.Prompts) != 2 || b.Older || b.Prompts[1].Text != "first" {
		t.Fatal(b, e)
	}
}
