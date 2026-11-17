package store_test

import (
	"shioji/internal/domain"
	"shioji/internal/store"
	"testing"
)

func deferredReply(t *testing.T) (*store.Store, domain.Workspace, string, domain.WorkActivity) {
	t.Helper()
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "reply", WorkspaceID: w.ID, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	// Confirm the original input so a later accepted turn is independently valid.
	if err = s.SetOperation(op.ID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	work := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "turn", State: "running"}
	for _, e := range []domain.Event{{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "running"}}, {Type: "work.upsert", Work: &work}, {Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "completed"}}} {
		if _, err = s.Apply(op.SessionID, e); err != nil {
			t.Fatal(err)
		}
	}
	return s, w, op.SessionID, work
}
func TestDeferredCompletionWaitsForAllNativeWorkAndPublishesOnce(t *testing.T) {
	s, _, id, work := deferredReply(t)
	d, err := s.Session(id, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if c := d.Session.Completion; c != nil && (c.Unread || c.ID != "") {
		t.Fatal("parent published completion while child still active", c)
	}
	second := work
	second.ID = "background"
	second.Kind = "background"
	if _, err = s.Apply(id, domain.Event{Type: "work.upsert", Work: &second}); err != nil {
		t.Fatal(err)
	}
	work.State = "completed"
	e, err := s.Apply(id, domain.Event{Type: "work.upsert", Work: &work})
	if err != nil {
		t.Fatal(err)
	}
	if c := e.Session.Completion; c != nil && c.Unread {
		t.Fatal("another native work still active", c)
	}
	second.State = "completed"
	e, err = s.Apply(id, domain.Event{Type: "work.upsert", Work: &second})
	if err != nil {
		t.Fatal(err)
	}
	c := e.Session.Completion
	if c == nil || c.ID == "" || !c.Unread || c.TurnID != "turn" {
		t.Fatal("last settlement did not publish", c)
	}
	receipt := c.ID
	if _, err = s.ReadCompletion(id, receipt); err != nil {
		t.Fatal(err)
	}
	e, err = s.Apply(id, domain.Event{Type: "work.upsert", Work: &second})
	if err != nil {
		t.Fatal(err)
	}
	if c = e.Session.Completion; c == nil || c.ID != receipt || c.Unread {
		t.Fatal("duplicate work resurrected completion", c)
	}
}
func TestDeferredCompletionWithdrawalPreventsLatePublication(t *testing.T) {
	for _, action := range []string{"new-turn", "archive", "workspace-archive", "restart", "history-change"} {
		t.Run(action, func(t *testing.T) {
			s, w, id, work := deferredReply(t)
			d, _ := s.Session(id, 0, 10)
			var err error
			switch action {
			case "new-turn":
				_, err = s.Accept(domain.Submission{OperationID: "next", WorkspaceID: w.ID, SessionID: id, Text: "next"})
			case "archive":
				_, err = s.SetSessionArchived(id, d.Session.Revision, true)
			case "workspace-archive":
				_, err = s.SetWorkspaceArchived(w.ID, w.Revision, true)
			case "restart":
				err = s.Recover()
			case "history-change":
				d.Session.ThreadID = "successor"
				_, err = s.Apply(id, domain.Event{Type: "session", Session: &d.Session})
			}
			if err != nil {
				t.Fatal(err)
			}
			work.State = "completed"
			e, err := s.Apply(id, domain.Event{Type: "work.upsert", Work: &work})
			if err != nil {
				t.Fatal(err)
			}
			if c := e.Session.Completion; c != nil && c.Unread {
				t.Fatal("withdrawn wait published stale completion", action, c)
			}
		})
	}
}
func TestDeferredCompletionRuntimeLossPublishesInterruptedOnce(t *testing.T) {
	s, _, id, _ := deferredReply(t)
	if err := s.ReleaseRuntime(id, "", "exact runtime lost", false); err != nil {
		t.Fatal(err)
	}
	d, err := s.Session(id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if c := d.Session.Completion; c == nil || c.ID == "" || !c.Unread {
		t.Fatal("loss did not settle deferred completion", c)
	}
	warnings := 0
	for _, item := range d.Items {
		if item.Kind == "warning" {
			warnings++
		}
	}
	if warnings != 1 {
		t.Fatal("missing persisted native-work interruption warning", d.Items)
	}
	if d.Turns[0].Status != "completed" {
		t.Fatal("runtime loss rewrote natural parent reply", d.Turns)
	}
}

func TestDeferredCompletionPendingWorkInputSettlesOnReceipt(t *testing.T) {
	s, _, id, work := deferredReply(t)
	work.CanSteer = true
	if _, err := s.Apply(id, domain.Event{Type: "work.upsert", Work: &work}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AcceptWorkInput(domain.WorkInput{OperationID: "steer", SessionID: id, WorkID: work.ID, Text: "continue", Mode: "steer"}); err != nil {
		t.Fatal(err)
	}
	work.State = "completed"
	e, err := s.Apply(id, domain.Event{Type: "work.upsert", Work: &work})
	if err != nil {
		t.Fatal(err)
	}
	if e.Session.Completion.Unread {
		t.Fatal("input still unconfirmed")
	}
	if err = s.SetWorkInput("steer", "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	d, err := s.Session(id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if c := d.Session.Completion; c == nil || !c.Unread || c.ID == "" {
		t.Fatal("input receipt did not release completion", c)
	}
}
func TestDeferredCompletionIgnoresWorkFromAnotherHistory(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, err := s.Accept(domain.Submission{OperationID: "reply", WorkspaceID: w.ID, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	work := domain.WorkActivity{ID: "old", Kind: "agent", TurnID: "old", OriginRef: "other", State: "running"}
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &work}); err != nil {
		t.Fatal(err)
	}
	e, err := s.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "t", Status: "completed"}})
	if err != nil {
		t.Fatal(err)
	}
	if c := e.Session.Completion; c == nil || c.Pending || !c.Unread {
		t.Fatal("other history blocked current reply", c)
	}
}
func TestDeferredCompletionWithdrawnWatermarkRejectsLateTurnRepetition(t *testing.T) {
	s, w, id, _ := deferredReply(t)
	if _, err := s.Accept(domain.Submission{OperationID: "next", WorkspaceID: w.ID, SessionID: id, Text: "next"}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []domain.ExecutionState{"running", "completed"} {
		e, err := s.Apply(id, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: state}})
		if err != nil {
			t.Fatal(err)
		}
		if c := e.Session.Completion; c != nil && (c.Pending || c.Unread) {
			t.Fatal("old turn revived withdrawn publication", c)
		}
	}
}
