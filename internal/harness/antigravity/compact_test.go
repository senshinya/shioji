package antigravity

import (
	"context"
	"os"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestCompactUsesTargetCommandAndEndsWithoutSynthesizingUsage(t *testing.T) {
	for _, advertised := range []bool{true, false} {
		t.Run(map[bool]string{true: "target advertises", false: "foreign session only"}[advertised], func(t *testing.T) {
			a, log := fixtureAdapter(t)
			if !advertised {
				if err := os.WriteFile(log+".foreign-compact", nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			events := make(chan domain.Event, 16)
			r, err := a.Start(ctx, harness.StartOptions{OnEvent: func(e domain.Event) error { events <- e; return nil }})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close(ctx)
			s := domain.Session{ID: "shioji-session", ThreadID: "native-session", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: "gemini-fixture", Effort: "high", Permission: "default", Plan: true}}
			if err := r.Resume(ctx, s); err != nil {
				t.Fatal(err)
			}
			compactor, ok := r.(harness.ContextCompactor)
			if !ok {
				t.Fatal("adapter did not expose context compaction")
			}
			err = compactor.Compact(ctx, s)
			if !advertised {
				if err == nil || hasMethod(log, "session/prompt") {
					t.Fatalf("foreign advertisement licensed a write: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var label string
			var usage *domain.ContextUsage
			for {
				select {
				case <-ctx.Done():
					t.Fatal("compaction did not settle")
				case event := <-events:
					if event.Item != nil && event.Item.Final {
						label = event.Item.Name
					}
					if event.ContextUsage != nil {
						usage = event.ContextUsage
					}
					if event.Turn != nil && !event.Turn.Status.Active() {
						if event.Turn.Presentation != "compaction" || event.Turn.Status != domain.ExecutionCompleted ||
							label != "压缩命令已结束" || usage == nil || usage.UsedTokens != nil || usage.LimitTokens != nil {
							t.Fatalf("terminal = %+v, label=%q, usage=%+v", event.Turn, label, usage)
						}
						return
					}
				}
			}
		})
	}
}

func TestCompactionWaitsForCancellationOrReportsTransportLoss(t *testing.T) {
	for _, mode := range []string{"hold-compact", "lose-compact"} {
		t.Run(mode, func(t *testing.T) {
			a, log := fixtureAdapter(t)
			if err := os.WriteFile(log+"."+mode, nil, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			events := make(chan domain.Event, 16)
			r, err := a.Start(ctx, harness.StartOptions{OnEvent: func(e domain.Event) error { events <- e; return nil }})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close(ctx)
			s := domain.Session{ID: "worker-session", ThreadID: "native-session", Context: domain.LaunchContext{CWD: t.TempDir()}, Config: domain.RunConfig{Model: "gemini-fixture", Effort: "high", Permission: "default", Plan: true}}
			if err := r.Resume(ctx, s); err != nil {
				t.Fatal(err)
			}
			if err := r.(harness.ContextCompactor).Compact(ctx, s); err != nil {
				t.Fatal(err)
			}
			for {
				select {
				case <-ctx.Done():
					t.Fatal("maintenance was not resolved")
				case event := <-events:
					if mode == "hold-compact" && event.Turn != nil && event.Turn.Status == domain.ExecutionRunning {
						if r.(harness.PendingRuntime).PendingOperations() != 1 {
							t.Fatal("compaction released ownership before the original reply")
						}
						if err := r.Interrupt(ctx, s, event.Turn.ID); err != nil {
							t.Fatal(err)
						}
					}
					if event.Turn != nil && event.Turn.Status == domain.ExecutionCompleted {
						t.Fatal("cancel or disconnect was reported as complete")
					}
					if mode == "hold-compact" && event.Turn != nil && event.Turn.Status == domain.ExecutionInterrupted ||
						mode == "lose-compact" && event.Type == "runtime.lost" {
						return
					}
				}
			}
		})
	}
}
