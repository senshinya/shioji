package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness/codex"
	"shioji/internal/testutil"
	"strings"
	"testing"
	"time"
)

func TestRealCodexUpgradeKeepsActiveTurnThenContinuesSameSessionWithoutGuardOrReplay(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex unavailable")
	}
	p := testutil.NewProvider(t)
	dir := t.TempDir()
	oldPath, nextPath, alias := filepath.Join(dir, "old"), filepath.Join(dir, "new"), filepath.Join(dir, "codex")
	wrapper := []byte("#!/bin/sh\nexec '" + strings.ReplaceAll(binary, "'", "'\\''") + "' \"$@\"\n")
	for _, path := range []string{oldPath, nextPath} {
		if err = os.WriteFile(path, wrapper, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Symlink(oldPath, alias); err != nil {
		t.Fatal(err)
	}
	s, w := memoryWorker(t, codex.Adapter{Executable: alias, Home: p.Home})
	ready := s.runtimeReadiness()
	if len(ready.Models) < 2 {
		t.Fatal("live catalog lacks test models", ready)
	}
	config := domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}
	first, err := s.submit(domain.Submission{OperationID: "real-before-upgrade", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Started:
	case <-time.After(8 * time.Second):
		t.Fatal("real response never started")
	}
	// Title leasing has its own regression. Let this independent auxiliary
	// task settle before testing a catalog upgrade during the held real turn.
	titleDeadline := time.Now().Add(25 * time.Second)
	for {
		d, _ := s.store.Session(first.SessionID, 0, 1)
		if d.Session.SourceTitle != "" {
			break
		}
		if time.Now().After(titleDeadline) {
			close(p.Release)
			t.Fatal("auxiliary title did not settle before upgrade")
		}
		time.Sleep(20 * time.Millisecond)
	}
	oldCatalog, oldSession := s.catalogRuntime("codex"), s.currentRuntime(first.SessionID)
	temp := filepath.Join(dir, "replacement")
	if err = os.Symlink(nextPath, temp); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(temp, alias); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(8 * time.Second)
	for s.catalogRuntime("codex") == oldCatalog || s.runtimeReadiness().State != domain.RuntimeReady {
		s.handle("readiness", nil)
		time.Sleep(20 * time.Millisecond)
		if time.Now().After(until) {
			close(p.Release)
			t.Fatal("real catalog did not refresh", s.runtimeReadiness().State, "title inputs", len(p.TitleInputs()), "turn inputs", len(p.Inputs()))
		}
	}
	if oldSession.Stopped() || s.currentRuntime(first.SessionID) != oldSession {
		close(p.Release)
		t.Fatal("installation refresh stopped executing session")
	}
	close(p.Release)
	waitDone := func() domain.SessionDetail {
		t.Helper()
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) {
			d, e := s.store.Session(first.SessionID, 0, 100)
			if e == nil && d.Session.Status == "completed" {
				return d
			}
			time.Sleep(15 * time.Millisecond)
		}
		d, _ := s.store.Session(first.SessionID, 0, 100)
		t.Fatal("real session did not finish", d.Session)
		return d
	}
	waitDone()
	config.Model = ready.Models[1].ID
	second, err := s.submit(domain.Submission{OperationID: "real-after-upgrade", SessionID: first.SessionID, WorkspaceID: w.ID, Text: "continue", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	receipt := awaitReceipt(t, s, second.ID)
	after := waitDone()
	if receipt.State != "confirmed" || after.Session.ExecutionGuard != "" || after.Session.Recovery != nil && after.Session.Recovery.Phase == domain.RecoveryAttention {
		t.Fatal("planned retirement was treated as lost execution", receipt, after.Session)
	}
	if !oldSession.Stopped() || s.currentRuntime(first.SessionID) == oldSession || len(after.Turns) != 2 || len(after.Items) != 4 || len(p.Inputs()) != 2 {
		t.Fatal("upgrade lost history or replayed input", len(after.Turns), len(after.Items), len(p.Inputs()))
	}
}
