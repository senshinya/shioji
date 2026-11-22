package worker

import (
	"encoding/json"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestHomeInputReceiptPrecedesGitPreparation(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "prep"})
	gitTestRun(t, w.Folders[0], "init", "-b", "main")
	gate, release := holdLifecycleGit(t, "status")
	in := domain.Submission{OperationID: "preparing-home-input", WorkspaceID: w.ID, Text: "save this input",
		Config: domain.RunConfig{Model: "prep-model", Permission: "limited"}}
	body, err := json.Marshal(protocol.SubmissionInput{Submission: in, ReceiveBeforePrepare: true})
	if err != nil {
		t.Fatal(err)
	}
	type response struct {
		value any
		err   error
	}
	replied := make(chan response, 1)
	go func() {
		out, err := s.handle("session.submit", body)
		replied <- response{out, err}
	}()
	select {
	case got := <-replied:
		op, ok := got.value.(domain.Operation)
		if got.err != nil || !ok || op.ID != in.OperationID || op.SessionID != "" || op.State != "preparing" {
			t.Fatalf("invalid receipt before preparation: %+v, %v", got.value, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker receipt waited for Git preparation")
	}
	awaitLifecycleGit(t, gate)
	receipt, err := s.inputReceipt(protocol.OperationQuery{ID: in.OperationID, SealGeneration: s.store.Generation()})
	if err != nil || receipt.State != "preparing" {
		t.Fatalf("lost command reply could not recover the durable receipt: %+v, %v", receipt, err)
	}
	snapshot, err := s.store.Snapshot()
	if err != nil || len(snapshot.Sessions) != 0 {
		t.Fatalf("session was visible before Git validation: %d, %v", len(snapshot.Sessions), err)
	}
	release()
	if final := awaitReceipt(t, s, in.OperationID); final.State != domain.OperationConfirmed || final.SessionID == "" {
		t.Fatalf("received input did not become a confirmed session: %+v", final)
	}
}
