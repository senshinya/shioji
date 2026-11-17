package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"sync"
	"testing"
	"time"
)

func TestCommittedSelectionRetriesTemporaryStartup(t *testing.T) {
	dir, options := regressionOptions(t)
	options.RetryMin = 5 * time.Millisecond
	options.RetryMax = 10 * time.Millisecond
	if err := os.WriteFile(filepath.Join(dir, "startup-exit"), []byte("2"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h := &host{ctx: ctx, options: options, instance: "supervisor", wake: make(chan struct{}, 1)}
	var once sync.Once
	var firstFailed = make(chan struct{})
	verify := options.Verify
	h.options.Verify = func(ctx context.Context, build string) (string, domain.WorkerBuild, error) {
		path, b, e := verify(ctx, build)
		once.Do(func() { close(firstFailed) })
		return path, b, e
	}
	go func() {
		<-firstFailed
		time.Sleep(80 * time.Millisecond)
		_ = os.Remove(filepath.Join(dir, "startup-exit"))
	}()
	err := h.startSelectedRetry(options.InitialPath, true, "already-committed")
	if h.child != nil {
		defer func() { _ = h.stopExact(h.child, 100*time.Millisecond) }()
	}
	t.Logf("committedPausedStartError=%v", err)
	if err != nil {
		t.Fatal("a committed build's temporary startup failure was not retried", err)
	}
}

func TestOrdinarySelectionKeepsStartupRetry(t *testing.T) {
	dir, options := regressionOptions(t)
	options.RetryMin = 5 * time.Millisecond
	options.RetryMax = 10 * time.Millisecond
	if err := os.WriteFile(filepath.Join(dir, "startup-exit"), []byte("2"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h := &host{ctx: ctx, options: options, instance: "supervisor", wake: make(chan struct{}, 1)}
	var once sync.Once
	var firstFailed = make(chan struct{})
	verify := options.Verify
	h.options.Verify = func(ctx context.Context, build string) (string, domain.WorkerBuild, error) {
		path, b, e := verify(ctx, build)
		once.Do(func() { close(firstFailed) })
		return path, b, e
	}
	go func() {
		<-firstFailed
		time.Sleep(80 * time.Millisecond)
		_ = os.Remove(filepath.Join(dir, "startup-exit"))
	}()
	err := h.startSelectedRetry(options.InitialPath, false, "already-committed")
	if h.child != nil {
		defer func() { _ = h.stopExact(h.child, 100*time.Millisecond) }()
	}
	t.Logf("committedPausedStartError=%v", err)
	if err != nil {
		t.Fatal("a committed build's temporary startup failure was not retried", err)
	}
}
