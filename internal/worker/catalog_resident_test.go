package worker

import (
	"bufio"
	"context"
	"os/exec"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestInstallationReplacementRetiresResidentKernels(t *testing.T) {
	a := &changingCatalog{}
	a.version.Store(1)
	s, w := memoryWorker(t, a)
	first, err := s.submit(domain.Submission{OperationID: "audit-catalog-v1", WorkspaceID: w.ID, HarnessID: "changing", Text: "first", Config: domain.RunConfig{Model: "model-1", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, first.ID)
	detail, err := s.store.Session(first.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.store.Apply(first.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: detail.Turns[0].ID, Status: "completed"}})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := s.executions.Existing(first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	script := `require('node:child_process').spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{stdio:'ignore'});console.log('ready');process.stdin.resume();setInterval(()=>{},1000)`
	cmd, in, out, err := scope.StartOwned(ctx, "worker", []string{node, "-e", script}, []string{"PATH=/opt/homebrew/bin:/usr/bin:/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scope.Stop(context.Background()); in.Close(); cmd.Wait() })
	if _, err = bufio.NewReader(out).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	a.version.Store(2)
	refreshCatalogByReadiness(t, s, "model-2", domain.RuntimeReady)
	detail, err = s.store.Session(first.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Session.Status != "completed" {
		t.Fatal("fixture is not business idle", detail.Session.Status)
	}
	busy, err := s.store.HasActiveWork(first.SessionID)
	if err != nil || busy {
		t.Fatal("fixture has active native work", busy, err)
	}
	s.dispatchMu.Lock()
	runtime, err := s.sessionRuntime(s.ctx, first.SessionID)
	s.dispatchMu.Unlock()
	t.Logf("sessionStatus=%s activeWork=%v target=model-2 migrationError=%v", detail.Session.Status, busy, err)
	if err != nil {
		t.Fatal("resident idle Node process still blocks adopting new runtime", err)
	}
	if runtime.Readiness().Models[0].ID != "model-2" {
		t.Fatal("old runtime retained")
	}
}
