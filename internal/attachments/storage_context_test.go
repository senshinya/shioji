package attachments

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStorageCancellationDoesNotWaitForRepositoryLock(t *testing.T) {
	r := New(t.TempDir())
	r.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { _, err := r.StorageContext(ctx, time.Now(), false, false); done <- err }()
	select {
	case err := <-done:
		r.mu.Unlock()
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		r.mu.Unlock()
		<-done
		t.Fatal("cancelled statistics waited for repository lock")
	}
}
