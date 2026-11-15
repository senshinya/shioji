package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestHistoryWindowStartsAtLatestAndReadsOlderOnDemand(t *testing.T) {
	dir, e := os.MkdirTemp("", "hw-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, e := New(context.Background(), Config{Directory: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	w, e := s.store.CreateWorkspace("history", []string{t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	op, e := s.store.Accept(domain.Submission{OperationID: "window", WorkspaceID: w.ID, Text: "original question", Config: domain.RunConfig{Model: "test"}})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 350; i++ {
		_, e = s.store.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: fmt.Sprint("tool-", i), Kind: "tool", Name: "command", Text: strings.Repeat("output ", 30), Final: true}})
		if e != nil {
			t.Fatal(e)
		}
		if i == 10 {
			if _, e = s.store.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "invisible-spacer", Kind: "assistant", Text: "\n\t　", Final: true}}); e != nil {
				t.Fatal(e)
			}
		}
		if i == 15 {
			if _, e = s.store.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "legacy-queued", Kind: "queued", Text: "hidden queue"}}); e != nil {
				t.Fatal(e)
			}
		}
	}
	type window = domain.HistoryWindow
	read := func(input map[string]any) (domain.SessionDetail, *window) {
		t.Helper()
		input["sessionId"] = op.SessionID
		b, _ := json.Marshal(input)
		out, e := s.handle("session.history", b)
		if e != nil {
			t.Fatal(e)
		}
		b, _ = json.Marshal(out)
		var result domain.SessionDetail
		if e = json.Unmarshal(b, &result); e != nil {
			t.Fatal(e)
		}
		if result.History == nil {
			t.Fatal("history window metadata missing")
		}
		return result, result.History
	}
	tail, h := read(map[string]any{"limit": 100})
	if len(tail.Items) != 101 || tail.Items[0].Text != "original question" || tail.Items[1].ID != "tool-250" || tail.Items[100].ID != "tool-349" || !h.Older || h.Newer {
		t.Fatal("initial history is not a bounded latest window", len(tail.Items), h)
	}
	if len(h.Groups) != 1 || h.Groups[0].ID != "tool-0" || h.Groups[0].Count != 350 {
		t.Fatal("partial tool group lost its identity or total", h.Groups)
	}
	older, prev := read(map[string]any{"before": h.First, "limit": 100})
	if len(older.Items) != 101 || older.Items[1].ID != "tool-150" || prev.Last >= h.First || !prev.Newer {
		t.Fatal("older page is not adjacent", prev)
	}
	around, target := read(map[string]any{"aroundId": "tool-30", "limit": 100})
	if len(around.Items) != 101 || around.Items[1].ID != "tool-30" || around.Items[100].ID != "tool-129" || !target.Older || !target.Newer {
		t.Fatal("targeted history downloaded an intervening prefix", target, len(around.Items))
	}
}

func TestHistorySearchReadsOutsideTheLoadedWindow(t *testing.T) {
	dir, e := os.MkdirTemp("", "hs-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	s, e := New(context.Background(), Config{Directory: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	w, e := s.store.CreateWorkspace("search", []string{t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	op, e := s.store.Accept(domain.Submission{OperationID: "search", WorkspaceID: w.ID, Text: "ÉARLY needle needle", Config: domain.RunConfig{Model: "test"}})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(map[string]any{"sessionId": op.SessionID, "query": "éarly"})
	result, e := s.handle("session.matches", b)
	if e != nil {
		t.Fatal(e)
	}
	b, _ = json.Marshal(result)
	var found struct {
		Matches []struct {
			ID         string
			Count      int
			Start, End int
		}
	}
	if e = json.Unmarshal(b, &found); e != nil {
		t.Fatal(e)
	}
	if len(found.Matches) != 1 || found.Matches[0].ID != "input:search" || found.Matches[0].Count != 1 || found.Matches[0].Start != 0 || found.Matches[0].End != 6 {
		t.Fatal("full-history Unicode match was lost", string(b))
	}
}
