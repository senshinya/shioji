package store_test

import (
	"shioji/internal/domain"
	"testing"
)

func TestHarnessNameSurvivesStaleLifecycleAndIsSearchable(t *testing.T) {
	s := open(t)
	w := workspace(t, s)
	op, _ := s.Accept(domain.Submission{OperationID: "name", WorkspaceID: w.ID, Text: "long initial request"})
	before, _ := s.Session(op.SessionID, 0, 100)
	event, err := s.Apply(op.SessionID, domain.Event{Type: "session.title", Text: "任务侧栏接入"})
	if err != nil {
		t.Fatal(err)
	}
	if event.Session == nil || event.Session.Title != "任务侧栏接入" || event.Session.Context.CWD != before.Session.Context.CWD || event.Session.Status != before.Session.Status {
		t.Fatal("rename changed session context/status", event.Session)
	}
	stale := before.Session
	stale.Status = "running"
	if _, err = s.Apply(op.SessionID, domain.Event{Type: "session", Session: &stale}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Session(op.SessionID, 0, 100)
	if after.Session.Title != "任务侧栏接入" || after.Session.Status != "running" {
		t.Fatal("stale lifecycle overwrote harness name", after.Session)
	}
	results, err := s.Search("任务侧栏")
	if err != nil || len(results) != 1 || results[0].Title != "任务侧栏接入" {
		t.Fatal("updated title is not searchable", results, err)
	}
}
