package store

import (
	"reflect"
	"testing"

	"shioji/internal/domain"
)

func TestSessionReloadReceiptSurvivesRestartWithoutReplaying(t *testing.T) {
	s, _, session := archiveFixture(t)
	session.ThreadID = "saved-source"
	if _, err := s.Apply(session.ID, domain.Event{Type: "session", Session: &session}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Session(session.ID, 0, 100)
	session = before.Session
	in := domain.SessionReloadRequest{OperationID: "reload", SessionID: session.ID, Revision: session.Revision}
	if _, err := s.AcceptSessionReload(in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishSessionReload(in, nil); err != nil {
		t.Fatal(err)
	}
	path := s.path
	s.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Recover(); err != nil {
		t.Fatal(err)
	}
	op, err := reopened.SessionReload(in)
	if err != nil || op.State != "completed" {
		t.Fatal("lost completed reload receipt", op, err)
	}
	after, _ := reopened.Session(session.ID, 0, 100)
	if !reflect.DeepEqual(before.Items, after.Items) || before.Session.ThreadID != after.Session.ThreadID {
		t.Fatal("reload receipt changed history")
	}
	in.Revision++
	if _, err := reopened.SessionReload(in); err == nil {
		t.Fatal("reused operation accepted a different request")
	}
}

func TestInterruptedSessionReloadFailsWithoutMakingInputUnknown(t *testing.T) {
	s, _, session := archiveFixture(t)
	session.ThreadID = "saved-source"
	if _, err := s.Apply(session.ID, domain.Event{Type: "session", Session: &session}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Session(session.ID, 0, 100)
	session = before.Session
	in := domain.SessionReloadRequest{OperationID: "unfinished", SessionID: session.ID, Revision: session.Revision}
	if _, err := s.AcceptSessionReload(in); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	op, err := s.SessionReload(in)
	if err != nil || op.State != "failed" || op.Error == "" {
		t.Fatal("interrupted reload was not settled", op, err)
	}
	d, _ := s.Session(session.ID, 0, 100)
	if d.Session.Status != "completed" || d.Session.ThreadID != session.ThreadID || !reflect.DeepEqual(d.Items, before.Items) {
		t.Fatal("reload was recovered as unknown user input", d.Session, len(d.Items))
	}
}
