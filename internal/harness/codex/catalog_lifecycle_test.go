package codex

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/testutil"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestCatalogDiscoveryRetainsNoAppServer(t *testing.T) {
	provider := testutil.NewProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var mu sync.Mutex
	var pids []int
	r, err := (Adapter{Home: provider.Home}).Start(ctx, harness.StartOptions{CatalogOnly: true, OnStart: func(pid int) error { mu.Lock(); pids = append(pids, pid); mu.Unlock(); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	check := func() {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		for _, pid := range pids {
			if syscall.Kill(pid, 0) == nil {
				t.Fatalf("catalog retained app-server %d after the probe finished", pid)
			}
		}
	}
	check()
	ready := r.Readiness()
	if ready.State != "ready" || r.Stopped() {
		t.Fatal("discovery lost the cached catalog", ready)
	}
	if err = r.Validate(domain.RunConfig{Model: ready.Models[0].ID, Permission: ready.Permissions[0]}); err != nil {
		t.Fatal("cached configuration validation lost readiness", err)
	}
	tools, ok := r.(harness.ToolServiceRuntime)
	if !ok {
		t.Fatal("catalog lost its native tool-service boundary")
	}
	if _, err = tools.ToolServices(ctx); err != nil {
		t.Fatal(err)
	}
	check()
	mu.Lock()
	started := len(pids)
	mu.Unlock()
	if started < 2 {
		t.Fatal("tool-service query did not use a fresh bounded probe", started)
	}
	canceled, abort := context.WithCancel(ctx)
	abort()
	if _, err = tools.ToolServices(canceled); err == nil {
		t.Fatal("canceled query succeeded")
	}
	if ready = r.Readiness(); ready.State != "ready" {
		t.Fatal("one canceled query invalidated cached discovery", ready)
	}
}

func TestCatalogCloseCancelsInFlightProbe(t *testing.T) {
	provider := testutil.NewProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := (Adapter{Home: provider.Home}).Start(ctx, harness.StartOptions{CatalogOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	c := r.(*catalogRuntime)
	defer c.Close(context.Background())
	entered := make(chan int, 1)
	finished := make(chan error, 1)
	go func() {
		_, err := catalogQuery(ctx, c, func(ctx context.Context, r *adapterRuntime) (struct{}, error) {
			entered <- r.cmd.Process.Pid
			<-ctx.Done()
			return struct{}{}, ctx.Err()
		})
		finished <- err
	}()
	pid := <-entered
	stop, stopCancel := context.WithTimeout(ctx, 5*time.Second)
	defer stopCancel()
	if err = c.Close(stop); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err == nil {
		t.Fatal("closing catalog did not cancel its operation")
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatal("closed catalog retained its query process")
	}
}

func TestCatalogCloseRetriesAfterEarlierCleanupFinishes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	nativeDone := make(chan struct{})
	close(nativeDone)
	cleanupDone := make(chan struct{})
	close(cleanupDone)
	native := &adapterRuntime{Runtime: &Runtime{done: nativeDone}, closing: &adapterClose{done: cleanupDone}}
	prior := &catalogProbe{runtime: native, reaping: true}
	c := &catalogRuntime{ctx: ctx, cancel: cancel, active: map[*adapterRuntime]*catalogProbe{native: prior}, done: make(chan struct{})}
	go func() { time.Sleep(20 * time.Millisecond); c.mu.Lock(); prior.reaping = false; c.mu.Unlock() }()
	wait, stop := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer stop()
	if err := c.Close(wait); err != nil {
		t.Fatal("retry missed the earlier cleanup's completion", err)
	}
}

func TestCatalogAuthorizationOwnsProbeUntilCompletionOrCancellation(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			server := localToolService(t, true)
			provider := testutil.NewProvider(t)
			path := filepath.Join(provider.Home, "config.toml")
			config, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			config = append([]byte("mcp_oauth_credentials_store=\"file\"\n"), config...)
			config = append(config, []byte(fmt.Sprintf("\n[mcp_servers.fixture]\nurl=%q\n", server.URL+"/mcp"))...)
			if err = os.WriteFile(path, config, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			r, err := (Adapter{Home: provider.Home}).Start(ctx, harness.StartOptions{CatalogOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			c := r.(*catalogRuntime)
			defer c.Close(context.Background())
			auth, challenge, err := c.BeginToolServiceAuthorization(ctx, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			c.mu.Lock()
			active := len(c.active)
			c.mu.Unlock()
			if active != 1 {
				t.Fatal("authorization lost its physical probe", active)
			}
			if complete {
				response, err := http.Get(challenge.URL)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if err = auth.Wait(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = c.Close(ctx); err != nil {
					t.Fatal(err)
				}
			}
			c.mu.Lock()
			active = len(c.active)
			c.mu.Unlock()
			if active != 0 {
				t.Fatal("completed or abandoned authorization retained its probe", active)
			}
		})
	}
}
