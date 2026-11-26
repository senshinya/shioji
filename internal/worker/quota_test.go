package worker

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
)

type quotaTestAdapter struct {
	configurableMemory
	id      string
	calls   *atomic.Int32
	gate    chan struct{}
	snapFn  func() (domain.QuotaSnapshot, error)
}

func (a quotaTestAdapter) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: a.id, Name: a.id}
}

func (a quotaTestAdapter) Check(context.Context) (domain.Readiness, error) {
	return domain.Readiness{
		State: "ready",
		Harnesses: []domain.HarnessInfo{
			{ID: a.id, Name: a.id, State: "ready"},
		},
	}, nil
}

func (a quotaTestAdapter) Quota(ctx context.Context, _ *managedexec.Scope) (domain.QuotaSnapshot, error) {
	a.calls.Add(1)
	if a.gate != nil {
		select {
		case <-a.gate:
		case <-ctx.Done():
			return domain.QuotaSnapshot{}, ctx.Err()
		}
	}
	return a.snapFn()
}

func startQuotaTestWorker(t *testing.T, adapters ...harness.Adapter) (*Server, *protocol.Client) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wq-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	srv, err := New(context.Background(), Config{
		Directory: dir,
		Harnesses: adapters,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	go srv.Serve()
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return srv, client
}

func TestWorkerQuotaCacheForceSingleFlightAndPushWake(t *testing.T) {
	var calls atomic.Int32
	rem80 := 80.0
	gate := make(chan struct{})
	adapter := quotaTestAdapter{
		id:    domain.QuotaKindCodex,
		calls: &calls,
		gate:  gate,
		snapFn: func() (domain.QuotaSnapshot, error) {
			return domain.QuotaSnapshot{
				HarnessID: domain.QuotaKindCodex,
				Kind:      domain.QuotaKindCodex,
				Source:    domain.QuotaSourcePoll,
				FetchedAt: time.Now().UTC(),
				Windows:   []domain.QuotaWindow{{ID: "primary", RemainingPercent: &rem80}},
			}, nil
		},
	}

	srv, client := startQuotaTestWorker(t, adapter)

	// 1. Single-flight deduplication: 3 concurrent calls hit provider only once.
	var wg sync.WaitGroup
	results := make([]protocol.QuotaResult, 3)
	for i := range 3 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := client.Call(ctx, "runtime.quota", protocol.QuotaQuery{HarnessID: domain.QuotaKindCodex}, &results[idx]); err != nil {
				t.Errorf("call %d failed: %v", idx, err)
			}
		}(i)
	}
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	close(gate)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("single-flight failed: provider called %d times, want 1", got)
	}
	for i, res := range results {
		if res.Snapshot == nil || *res.Snapshot.Windows[0].RemainingPercent != 80 {
			t.Fatalf("result %d unexpected: %+v", i, res)
		}
	}

	// 2. Subsequent call within 15s TTL hits cache (no extra provider call).
	var cached protocol.QuotaResult
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Call(ctx, "runtime.quota", protocol.QuotaQuery{HarnessID: domain.QuotaKindCodex}, &cached); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("cache miss within TTL: calls=%d", calls.Load())
	}

	// 3. Force=true bypasses 15s cache TTL.
	var forced protocol.QuotaResult
	if err := client.Call(ctx, "runtime.quota", protocol.QuotaQuery{HarnessID: domain.QuotaKindCodex, Force: true}, &forced); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("Force=true did not bypass cache: calls=%d", calls.Load())
	}

	snap0, err := srv.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	// 4. Real-time push wakes parked events.watch and delivers QuotaUpdates without SQLite bloat.
	watchDone := make(chan protocol.EventPage, 1)
	watchErr := make(chan error, 1)
	go func() {
		var page protocol.EventPage
		wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer wcancel()
		err := client.Call(wctx, "events.watch", protocol.EventsQuery{
			After:      snap0.Sequence,
			AfterQuota: forced.Snapshot.Version,
			Generation: snap0.Generation,
			WaitMS:     4000,
		}, &page)
		if err != nil {
			watchErr <- err
			return
		}
		watchDone <- page
	}()
	time.Sleep(50 * time.Millisecond)
	rem42 := 42.0
	_ = srv.onHarnessEvent(domain.QuotaKindCodex, "session-1", domain.Event{
		Type: "quota.updated",
		Quota: &domain.QuotaSnapshot{
			HarnessID: domain.QuotaKindCodex,
			Kind:      domain.QuotaKindCodex,
			Windows:   []domain.QuotaWindow{{ID: "primary", RemainingPercent: &rem42}},
		},
	})
	select {
	case err := <-watchErr:
		t.Fatalf("events.watch failed: %v", err)
	case page := <-watchDone:
		if page.QuotaVersion <= forced.Snapshot.Version || len(page.QuotaUpdates) != 1 {
			t.Fatalf("push wake missing quota updates: %+v", page)
		}
		if *page.QuotaUpdates[0].Windows[0].RemainingPercent != 42 || page.QuotaUpdates[0].Source != domain.QuotaSourcePush {
			t.Fatalf("unexpected pushed snapshot: %+v", page.QuotaUpdates[0])
		}
		if len(page.Events) != 0 {
			t.Fatalf("quota push should not bloat SQLite event journal: %+v", page.Events)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("events.watch did not wake on quota push")
	}
}
