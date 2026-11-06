package codex

import (
	"context"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/testutil"
	"strings"
	"testing"
	"time"
)

func TestForkUsesRealHistoryBoundaryWithoutStartingModel(t *testing.T) {
	p := testutil.NewProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	completed := make(chan string, 8)
	r, err := Start(ctx, Options{Home: p.Home, OnEvent: func(e domain.Event) error {
		if e.Turn != nil && e.Turn.Status == "completed" {
			completed <- e.Turn.ID
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := domain.Session{ID: "original", Context: domain.LaunchContext{CWD: cwd}, Config: domain.RunConfig{Model: r.Readiness().Models[0].ID, Permission: "read-only"}}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	var turns []string
	for _, text := range []string{"KEEP_FIRST_CONTEXT", "REMOVE_SECOND_CONTEXT"} {
		id, e := r.Submit(ctx, s, domain.NewID(), text)
		if e != nil {
			t.Fatal(e)
		}
		select {
		case got := <-completed:
			if got != id {
				t.Fatal(got, id)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		turns = append(turns, id)
	}
	for _, before := range []bool{false, true} {
		boundary := turns[0]
		if before {
			boundary = turns[1]
		}
		count := len(p.Inputs())
		thread, e := r.Fork(ctx, s, boundary, before)
		if e != nil {
			t.Fatal(e)
		}
		if thread == s.ThreadID || thread == "" || len(p.Inputs()) != count {
			t.Fatal("fork changed source or called model", thread, len(p.Inputs()))
		}
		fork := s
		fork.ID = domain.NewID()
		fork.ThreadID = thread
		if e = r.Resume(ctx, fork); e != nil {
			t.Fatal(e)
		}
		if _, e = r.Submit(ctx, fork, domain.NewID(), "NEW_BRANCH_CONTEXT"); e != nil {
			t.Fatal(e)
		}
		select {
		case <-completed:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		inputs := p.Inputs()
		last := inputs[len(inputs)-1]
		if !strings.Contains(last, "KEEP_FIRST_CONTEXT") || strings.Contains(last, "REMOVE_SECOND_CONTEXT") {
			t.Fatal("fork boundary leaked excluded history", last)
		}
		// Each branch adds exactly one explicitly submitted request.
		if !before && len(inputs) != 3 || before && len(inputs) != 4 {
			t.Fatal("fork triggered extra model work", len(inputs))
		}
	}
}
