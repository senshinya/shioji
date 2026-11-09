package worker

import (
	"context"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/testutil"
	"strings"
	"testing"
	"time"
)

func TestWorkerHistoryForkAndRestartUseOwningRuntimeAndNeverReplay(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sh-hist-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cwd, _ := filepath.EvalSymlinks(t.TempDir())
	w, err := s.store.CreateWorkspace("history", []string{cwd})
	if err != nil {
		t.Fatal(err)
	}
	cfg := domain.RunConfig{Model: s.currentRuntime().Readiness().Models[0].ID, Permission: "read-only"}
	wait := func(id string) domain.SessionDetail {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			d, e := s.store.Session(id, 0, 100)
			// Automatic naming is independent of turn completion. Capture the
			// fixture's settled title before asserting that history preserves it.
			if e == nil && d.Session.Status == "completed" && d.Session.SourceTitle != "" {
				return d
			}
			if time.Now().After(deadline) {
				t.Fatal("session did not complete", d, e)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	first, err := s.submit(domain.Submission{OperationID: "h1", WorkspaceID: w.ID, Text: "KEEP_FIRST", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	d := wait(first.SessionID)
	_, err = s.submit(domain.Submission{OperationID: "h2", WorkspaceID: w.ID, SessionID: d.Session.ID, Text: "REMOVE_SECOND", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	d = wait(first.SessionID)
	var t1 string
	for _, i := range d.Items {
		if i.ID == "input:h1" {
			t1 = i.TurnID
		}
	}
	forkInput := domain.HistoryChange{OperationID: "fork-action", SessionID: d.Session.ID, Revision: d.Session.Revision, TurnID: t1}
	fork, err := s.changeHistory(forkInput)
	if err != nil {
		t.Fatal(err)
	}
	if fork.Session.ID == d.Session.ID || len(p.Inputs()) != 2 {
		t.Fatal("fork submitted work or changed source")
	}
	// Identical action after a lost RPC response returns the existing branch.
	again, err := s.changeHistory(forkInput)
	if err != nil || again.Session.ID != fork.Session.ID {
		t.Fatal("fork retry duplicated branch", again, err)
	}
	original, _ := s.store.Session(d.Session.ID, 0, 100)
	oldThread := original.Session.ThreadID
	input := domain.HistoryChange{OperationID: "restart-action", SessionID: d.Session.ID, Revision: original.Session.Revision, ItemID: "input:h2", Restart: true, Text: "REVISED_SECOND", Config: cfg}
	result, err := s.changeHistory(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.ID != d.Session.ID || result.Session.ThreadID == oldThread {
		t.Fatal("restart identity incorrect", result)
	}
	updated := wait(d.Session.ID)
	if len(p.Inputs()) != 3 || !strings.Contains(p.Inputs()[2], "KEEP_FIRST") || strings.Contains(p.Inputs()[2], "REMOVE_SECOND") || !strings.Contains(p.Inputs()[2], "REVISED_SECOND") {
		t.Fatal("wrong restarted context", p.Inputs())
	}
	if updated.Session.Title != d.Session.Title || !updated.Queue.Paused {
		t.Fatal("metadata or queue was lost", updated)
	}
	if _, err = s.changeHistory(input); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if len(p.Inputs()) != 3 {
		t.Fatal("restart replayed")
	}
	for _, i := range updated.Items {
		if i.Text == "REMOVE_SECOND" {
			t.Fatal("old suffix survived")
		}
	}
}

func TestForkedRestartRevalidatesDirectoryBeforeRemovingHistory(t *testing.T) {
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sh-hpre-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := New(context.Background(), Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: p.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cwd, _ := filepath.EvalSymlinks(t.TempDir())
	w, err := s.store.CreateWorkspace("preflight", []string{cwd})
	if err != nil {
		t.Fatal(err)
	}
	cfg := domain.RunConfig{Model: s.currentRuntime().Readiness().Models[0].ID, Permission: "read-only"}
	accepted, err := s.submit(domain.Submission{OperationID: "original", WorkspaceID: w.ID, Text: "original", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	var d domain.SessionDetail
	deadline := time.Now().Add(10 * time.Second)
	for {
		d, _ = s.store.Session(accepted.SessionID, 0, 100)
		if d.Session.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source incomplete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	in := domain.HistoryChange{OperationID: "saved-fork", SessionID: d.Session.ID, Revision: d.Session.Revision, ItemID: "input:original", Restart: true, Text: "changed", Config: cfg}
	if _, err = s.store.PrepareHistory(in); err != nil {
		t.Fatal(err)
	}
	if err = s.store.SetHistoryThread(in.OperationID, "saved-fork-thread"); err != nil {
		t.Fatal(err)
	}
	moved := cwd + "-away"
	if err = os.Rename(cwd, moved); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(moved, cwd)
	if _, err = s.changeHistory(in); err == nil {
		t.Fatal("missing execution directory accepted")
	}
	after, _ := s.store.Session(d.Session.ID, 0, 100)
	if after.Session.ThreadID != d.Session.ThreadID || len(after.Items) != len(d.Items) {
		t.Fatal("history removed before revalidation")
	}
}
