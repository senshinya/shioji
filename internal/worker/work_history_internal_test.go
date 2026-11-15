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

func TestChildHistoryChunksKeepOneSnapshotAndFenceWork(t *testing.T) {
	dir, err := os.MkdirTemp("", "child-history-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ws, err := s.store.CreateWorkspace("child history", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.store.Accept(domain.Submission{OperationID: "child-snapshot", WorkspaceID: ws.ID, Text: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	w := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "root-turn", Title: "child", State: "running"}
	if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &w}); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("output ", 4096)
	for i := 0; i < 80; i++ {
		item := domain.Item{ID: fmt.Sprint(i), Kind: "assistant", Text: text, Final: true}
		if i == 0 {
			item.Kind = "user"
		}
		if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "work.item", WorkID: "child", WorkItem: &item}); err != nil {
			t.Fatal(err)
		}
	}
	var h connectionHistory
	defer h.close()
	call := func(q domain.WorkHistoryQuery) domain.WorkDetail {
		t.Helper()
		b, _ := json.Marshal(q)
		raw, err := h.handle(s, "session.work.history", b)
		if err != nil {
			t.Fatal(err)
		}
		out := raw.(domain.WorkDetail)
		wire, _ := json.Marshal(out)
		if len(wire) > 768<<10 {
			t.Fatal("chunk exceeds transport budget", len(wire))
		}
		return out
	}
	q := domain.WorkHistoryQuery{ID: "child", HistoryQuery: domain.HistoryQuery{SessionID: op.SessionID, Rounds: 1}}
	d := call(q)
	seq := d.Sequence
	count := len(d.Items)
	if h.snapshot == nil {
		t.Fatal("large round did not retain a connection snapshot")
	}
	for _, item := range []domain.Item{{ID: "79", Kind: "assistant", Text: "changed later", Final: true}, {ID: "later", Kind: "user", Text: "new round", Final: true}} {
		if _, err := s.store.Apply(op.SessionID, domain.Event{Type: "work.item", WorkID: "child", WorkItem: &item}); err != nil {
			t.Fatal(err)
		}
	}
	q.WindowFirst, q.WindowLast, q.ThreadID = d.History.WindowFirst, d.History.WindowLast, d.ThreadID
	for d.History.Last < q.WindowLast {
		q.After = d.History.Last
		d = call(q)
		count += len(d.Items)
		if d.Sequence != seq || d.History.Newer {
			t.Fatal("mixed snapshot", seq, d.Sequence)
		}
		for _, entry := range d.Items {
			if entry.Item.ID == "79" && entry.Item.Text != text {
				t.Fatal("newer body entered historical snapshot")
			}
		}
	}
	if count != 80 || h.snapshot != nil {
		t.Fatal("lost records or leaked read snapshot", count)
	}
	d = call(domain.WorkHistoryQuery{ID: "child", HistoryQuery: domain.HistoryQuery{SessionID: op.SessionID, Rounds: 1}})
	if len(d.Items) != 1 || d.Items[0].Item.ID != "later" || d.Sequence <= seq {
		t.Fatal("next read retained old snapshot", d.Sequence, len(d.Items))
	}
	// An otherwise matching continuation cannot be retargeted to another child.
	q = domain.WorkHistoryQuery{ID: "child", HistoryQuery: domain.HistoryQuery{SessionID: op.SessionID, Rounds: 2}}
	d = call(q)
	q.WindowFirst, q.WindowLast, q.After = d.History.WindowFirst, d.History.WindowLast, d.History.Last
	q.ID = "other-child"
	body, _ := json.Marshal(q)
	if _, err = h.handle(s, "session.work.history", body); err == nil || h.snapshot != nil {
		t.Fatal("cross-child continuation accepted or snapshot leaked", err)
	}
}
