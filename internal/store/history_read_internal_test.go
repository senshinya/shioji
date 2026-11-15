package store

import (
	"path/filepath"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestDisplayHistoryReadsDoNotHoldTheExecutionMutex(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	w, e := s.CreateWorkspace("fixture", []string{t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	op, e := s.Accept(domain.Submission{OperationID: "fixture", WorkspaceID: w.ID, Text: "needle", Config: domain.RunConfig{Model: "test"}})
	if e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	done := make(chan error, 1)
	go func() {
		if _, e := s.SessionHistory(domain.HistoryQuery{SessionID: op.SessionID}); e != nil {
			done <- e
			return
		}
		if _, e := s.HistoryOutline(domain.HistoryQuery{SessionID: op.SessionID}); e != nil {
			done <- e
			return
		}
		_, e := s.HistoryMatches(domain.HistorySearchQuery{HistoryQuery: domain.HistoryQuery{SessionID: op.SessionID}, Query: "needle"})
		done <- e
	}()
	select {
	case e := <-done:
		s.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		s.mu.Unlock()
		<-done
		t.Fatal("display history waited for the execution writer mutex")
	}
}
