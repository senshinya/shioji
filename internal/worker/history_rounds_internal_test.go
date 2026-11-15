package worker

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestRoundHistoryChunksShareOneSnapshotAndReleaseAtCompletion(t *testing.T) {
	dir, err := os.MkdirTemp("", "hr-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	go s.Serve()
	w, err := s.store.CreateWorkspace("rounds", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.store.Accept(domain.Submission{OperationID: "round-snapshot", WorkspaceID: w.ID, Text: "question", Config: domain.RunConfig{Model: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("output ", 4096)
	for i := 0; i < 70; i++ {
		if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: fmt.Sprint("tool-", i), Kind: "tool", TurnID: "turn", Text: body, Final: true}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		request := domain.PendingRequest{ID: fmt.Sprint("resolved-", i), TurnID: "turn", Kind: "questions", State: domain.RequestResolved, Text: strings.Repeat("d", 40<<10)}
		if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "request.open", Request: &request}); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	q := domain.HistoryQuery{SessionID: op.SessionID, Rounds: 1}
	var d domain.SessionDetail
	if err = client.Call(ctx, "session.history", q, &d); err != nil {
		t.Fatal(err)
	}
	sequence := d.Sequence
	seenRequests := map[string]bool{}
	for _, r := range d.Requests {
		seenRequests[r.ID] = true
	}
	q.WindowFirst, q.WindowLast, q.After = d.History.WindowFirst, d.History.WindowLast, d.History.Last
	if q.After >= q.WindowLast {
		t.Fatal("fixture must span transport chunks")
	}
	if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "tool-69", Kind: "tool", TurnID: "turn", Text: "changed after snapshot", Final: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "inserted", Kind: "user", Text: "new round", Final: true}}); err != nil {
		t.Fatal(err)
	}
	for q.After < q.WindowLast {
		if err = client.Call(ctx, "session.history", q, &d); err != nil {
			t.Fatal(err)
		}
		if d.Sequence != sequence || d.History.Newer {
			t.Fatal("round chunks mixed snapshots", sequence, d.Sequence, d.History)
		}
		for _, item := range d.Items {
			if item.ID == "tool-69" && item.Text != body {
				t.Fatal("later chunk observed a newer body", item.Text)
			}
		}
		for _, r := range d.Requests {
			seenRequests[r.ID] = true
		}
		if d.History.Last <= q.After {
			t.Fatal("no continuation progress")
		}
		q.After = d.History.Last
	}
	if len(seenRequests) != 20 {
		t.Fatal("large request metadata was not fully chunked", len(seenRequests))
	}
	if err = client.Call(ctx, "session.history", domain.HistoryQuery{SessionID: op.SessionID, Rounds: 1}, &d); err != nil {
		t.Fatal(err)
	}
	if d.Items[0].ID != "inserted" || d.Sequence <= sequence {
		t.Fatal("completed round window retained a stale snapshot", d)
	}
}
