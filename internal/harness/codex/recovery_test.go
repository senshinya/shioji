package codex

import (
	"context"
	"fmt"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/testutil"
	"testing"
	"time"
)

func TestInspectPagesHistoryPastOneHundredTurnsWithoutCallingModel(t *testing.T) {
	p := testutil.NewProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	completed := make(chan string, 128)
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
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := domain.Session{ID: "paged", Context: domain.LaunchContext{CWD: folder}, Config: domain.RunConfig{Model: r.Readiness().Models[0].ID, Permission: "read-only"}}
	s.ThreadID, err = r.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	var last string
	for i := 0; i < 101; i++ {
		last, err = r.Submit(ctx, s, fmt.Sprintf("page-%d", i), "hello")
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-completed:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	before := len(p.Inputs())
	history, err := r.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Turns) != 101 || history.Inputs["input:page-100"] != last || len(p.Inputs()) != before {
		t.Fatalf("history pagination missing tail or replayed input: turns=%d, last=%q", len(history.Turns), history.Inputs["input:page-100"])
	}
	wrong := s
	wrong.Context.CWD = "/wrong-device-path"
	if _, err = r.Inspect(ctx, wrong); err == nil {
		t.Fatal("foreign execution directory accepted")
	}
}
