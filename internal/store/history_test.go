package store_test

import (
	"shioji/internal/domain"
	"testing"
)

func TestHistoryRestartAtomicallyReplacesSuffixAndPreservesQueue(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	var sid string
	for i, text := range []string{"keep", "old question", "old tail"} {
		in := domain.Submission{OperationID: []string{"one", "two", "three"}[i], SessionID: sid, WorkspaceID: w.ID, Text: text}
		op, err := s.Accept(in)
		if err != nil {
			t.Fatal(err)
		}
		sid = op.SessionID
		id := []string{"t1", "t2", "t3"}[i]
		s.SetOperation(op.ID, "confirmed", "")
		s.Apply(sid, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:" + op.ID, TurnID: id, Kind: "user", Text: text, Status: "sent", Final: true}})
		s.Apply(sid, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "reply:" + id, TurnID: id, Kind: "assistant", Text: "reply " + text, Final: true}})
		s.Apply(sid, domain.Event{Type: "turn", Turn: &domain.Turn{ID: id, Status: "completed"}})
	}
	d, _ := s.Session(sid, 0, 100)
	d.Session.ThreadID = "old-thread"
	s.Apply(sid, domain.Event{Type: "session", Session: &d.Session})
	s.Apply(sid, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "late", TurnID: "t1", Kind: "tool", Text: "late output from first turn", Final: true}})
	s.Enqueue(domain.Submission{OperationID: "queued", SessionID: sid, WorkspaceID: w.ID, Text: "keep queued"})
	for _, turn := range []string{"t1", "t2"} {
		work := domain.WorkActivity{ID: "work-" + turn, Kind: "agent", TurnID: turn, Title: turn, State: "completed", CanContinue: true}
		if _, err := s.Apply(sid, domain.Event{Type: "work.upsert", Work: &work}); err != nil {
			t.Fatal(err)
		}
	}
	d, _ = s.Session(sid, 0, 100)
	in := domain.HistoryChange{OperationID: "restart", SessionID: sid, Revision: d.Session.Revision, ItemID: "input:two", Restart: true, Text: "new question", Config: d.Session.Config}
	op, err := s.PrepareHistory(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareHistory(in); err != nil {
		t.Fatal("intent is not idempotent", err)
	}
	if err = s.SetHistoryThread(op.ID, "new-thread"); err != nil {
		t.Fatal(err)
	}
	result, err := s.CommitHistory(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.ID != sid || result.Session.ThreadID != "new-thread" {
		t.Fatal("restart changed session identity", result)
	}
	d, _ = s.Session(sid, 0, 100)
	if len(d.Works) != 1 || d.Works[0].TurnID != "t1" {
		t.Fatal("history reset retained removed work", d.Works)
	}
	if d.Works[0].CanContinue {
		t.Fatal("history replacement retained old source control")
	}
	if len(d.Items) != 4 || d.Items[0].Text != "keep" || d.Items[3].Text != "new question" || len(d.Turns) != 1 || !d.Queue.Paused || len(d.Queue.Items) != 1 {
		t.Fatal("suffix or queue incorrect", d)
	}
	cancelled, err := s.CancelHistory(op.ID, sid)
	if err != nil || cancelled.State != "complete" {
		t.Fatal("cancel changed accepted restart", cancelled, err)
	}
	again, err := s.CommitHistory(op.ID)
	if err != nil || again.Session.ThreadID != "new-thread" {
		t.Fatal("commit is not idempotent", again, err)
	}
	s.SetOperation("restart", "confirmed", "")
	d, _ = s.Session(sid, 0, 100)
	if len(d.Items) != 4 {
		t.Fatal("repeated commit duplicated input")
	}
}

func TestForkCopiesOnlySelectedPrefixAndKeepsSource(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "fork-source", WorkspaceID: w.ID, Text: "source"})
	s.SetOperation(op.ID, "confirmed", "")
	s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:" + op.ID, Kind: "user", Text: "source", TurnID: "t1", Status: "sent", Final: true}})
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t1", Status: "completed"}})
	d, _ := s.Session(op.SessionID, 0, 100)
	d.Session.ThreadID = "thread"
	s.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session})
	child := domain.WorkActivity{ID: "child", Reference: "child-ref", OriginRef: "thread", Kind: "agent", TurnID: "t1", State: "completed", CanContinue: true}
	s.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &child})
	d, _ = s.Session(op.SessionID, 0, 100)
	in := domain.HistoryChange{OperationID: "fork", SessionID: op.SessionID, Revision: d.Session.Revision, TurnID: "t1"}
	fork, err := s.PrepareHistory(in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetHistoryThread(fork.ID, "forked-thread"); err != nil {
		t.Fatal(err)
	}
	got, err := s.CommitHistory(fork.ID)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := s.Session(got.Session.ID, 0, 100)
	if err != nil || copied.Session.ID == op.SessionID || len(copied.Items) != 1 || copied.Session.ForkedFrom != op.SessionID || len(copied.Queue.Items) > 0 {
		t.Fatal(copied, err)
	}
	if len(copied.Works) != 1 || copied.Works[0].CanContinue {
		t.Fatal("fork inherited live child input capability", copied.Works)
	}
	original, _ := s.Session(op.SessionID, 0, 100)
	if original.Session.ThreadID != "thread" || len(original.Items) != 1 {
		t.Fatal("fork changed original")
	}
	in.OperationID = "stale"
	in.Revision--
	if _, err = s.PrepareHistory(in); err == nil {
		t.Fatal("stale source accepted")
	}
	in.OperationID = "fork"
	in.Revision++
	in.TurnID = "other"
	if _, err = s.PrepareHistory(in); err == nil {
		t.Fatal("operation ID reused with another boundary")
	}
}

func TestUnconfirmedHistoryIntentFailsOnWorkerRestartWithoutChangingSource(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "source", WorkspaceID: w.ID, Text: "source"})
	s.SetOperation(op.ID, "confirmed", "")
	s.Apply(op.SessionID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:source", TurnID: "t", Kind: "user", Text: "source", Status: "sent", Final: true}})
	s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed"}})
	d, _ := s.Session(op.SessionID, 0, 100)
	d.Session.ThreadID = "original-thread"
	s.Apply(op.SessionID, domain.Event{Type: "session", Session: &d.Session})
	d, _ = s.Session(op.SessionID, 0, 100)
	in := domain.HistoryChange{OperationID: "unfinished-fork", SessionID: d.Session.ID, Revision: d.Session.Revision, TurnID: "t"}
	if _, err := s.PrepareHistory(in); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	intent, err := s.HistoryOperation(in.OperationID)
	if err != nil || intent.State != "failed" {
		t.Fatal("restart left intent permanently pending", intent, err)
	}
	after, _ := s.Session(d.Session.ID, 0, 100)
	if after.Session.ThreadID != d.Session.ThreadID || len(after.Items) != len(d.Items) {
		t.Fatal("intent recovery changed original")
	}
}
