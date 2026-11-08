package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
)

func TestIdleUpdateRejectsNewInputsAfterAcceptance(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "other"})
	s.config.ReplaceWorker = func(domain.WorkerReplacement) error { return nil }
	body, _ := json.Marshal(domain.WorkerReplacement{ExpectedBuildID: s.build.BuildID, BuildID: "candidate"})
	out, err := s.handle("worker.update", body)
	if err != nil || !out.(domain.WorkerUpdateResult).Ready {
		t.Fatal("idle update rejected", out, err)
	}
	in := domain.Submission{OperationID: "late", WorkspaceID: w.ID, HarnessID: "other", Text: "must not run", Config: domain.RunConfig{Model: "other-model", Permission: "limited"}}
	if _, err = s.submit(in); err == nil {
		t.Fatal("update accepted a new execution")
	}
	snap, _ := s.store.Snapshot()
	if len(snap.Sessions) != 0 {
		t.Fatal("late submission created a session", snap)
	}
}

func TestWorkerUpdateSerializesRacingSubmission(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "other"})
	prepared, release := make(chan struct{}), make(chan struct{})
	s.config.ReplaceWorker = func(domain.WorkerReplacement) error { close(prepared); <-release; return nil }
	body, _ := json.Marshal(domain.WorkerReplacement{ExpectedBuildID: s.build.BuildID, BuildID: "candidate"})
	updated := make(chan error, 1)
	go func() { _, err := s.handle("worker.update", body); updated <- err }()
	<-prepared
	late := make(chan error, 1)
	go func() {
		_, err := s.submit(domain.Submission{OperationID: "racing", WorkspaceID: w.ID, HarnessID: "other", Text: "keep draft", Config: domain.RunConfig{Model: "other-model", Permission: "limited"}})
		late <- err
	}()
	close(release)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if err := <-late; err == nil {
		t.Fatal("racing submission crossed the update boundary")
	}
	snap, _ := s.store.Snapshot()
	if len(snap.Sessions) != 0 {
		t.Fatal("racing input created a session", snap)
	}
}

func TestRepeatedClosePreservesCleanupFailureForReplacement(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "other"})
	op, err := s.submit(domain.Submission{OperationID: "cleanup", WorkspaceID: w.ID, HarnessID: "other", Text: "work", Config: domain.RunConfig{Model: "other-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	scope, err := s.executions.Existing(op.SessionID)
	if err != nil || scope == nil {
		t.Fatal("missing scope", err)
	}
	if err = os.WriteFile(filepath.Join(scope.Directory, "scope.json"), []byte("unreadable"), 0600); err != nil {
		t.Fatal(err)
	}
	first := s.Close()
	if first == nil {
		t.Fatal("cleanup failure was hidden")
	}
	if again := s.Close(); again == nil || again.Error() != first.Error() {
		t.Fatal("repeat close lost failure; replacement could proceed", first, again)
	}
}
