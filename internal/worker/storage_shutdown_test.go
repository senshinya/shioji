package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestStorageWaitIsCancelledByWorkerShutdown(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "storage"})
	s.storageMu.Lock()
	done := make(chan error, 1)
	go func() { _, err := s.storage(false); done <- err }()
	s.cancel()
	select {
	case err := <-done:
		s.storageMu.Unlock()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("storage did not report cancellation", err)
		}
	case <-time.After(250 * time.Millisecond):
		s.storageMu.Unlock()
		<-done
		t.Fatal("storage request ignored worker cancellation while waiting behind a scan")
	}
}

func TestCancelledStorageCannotDeleteRotatedLogs(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "storage"})
	path := filepath.Join(s.Directory(), "worker.log.1")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	s.cancel()
	if _, err := s.storage(true); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled scan continued", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
		t.Fatal("cancelled cleanup deleted a rotated log", string(data), err)
	}
}

func TestStorageReusesMeasuredReportAndExplicitRefreshRecounts(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "storage"})
	first, err := s.storage(false)
	if err != nil {
		t.Fatal(err)
	}
	if first.MeasuredAt.IsZero() {
		t.Fatal("statistics have no measurement time")
	}
	if err = os.WriteFile(filepath.Join(s.Directory(), "retained-data"), []byte("new bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	cached, err := s.storage(false)
	if err != nil || !cached.MeasuredAt.Equal(first.MeasuredAt) || cached.Usage.Total() != first.Usage.Total() {
		t.Fatal("repeat read did not reuse the completed scan", cached, err)
	}
	fresh, err := s.storage(false, true)
	if err != nil || !fresh.MeasuredAt.After(first.MeasuredAt) || fresh.Usage.RetainedBytes <= first.Usage.RetainedBytes {
		t.Fatal("explicit refresh returned stale statistics", fresh, err)
	}
}

func TestStorageCleanupInvalidatesCachedReclaimableBytes(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "storage"})
	path := filepath.Join(s.Directory(), "worker.log.1")
	if err := os.WriteFile(path, []byte("old log"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := s.storage(false)
	if err != nil || before.Usage.LogBytes != 7 {
		t.Fatal(before, err)
	}
	cleaned, err := s.storage(true)
	if err != nil || cleaned.FreedBytes != 7 || cleaned.Usage.LogBytes != 0 {
		t.Fatal("cleanup reused old totals", cleaned, err)
	}
	after, err := s.storage(false)
	if err != nil || after.Usage.LogBytes != 0 || after.FreedBytes != 0 {
		t.Fatal("cached cleanup replayed reclaimed bytes", after, err)
	}
}

func TestLegacyStorageReadStillRefreshesWithoutCacheOption(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "storage"})
	before, err := s.storage(false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Directory(), "new-retained-file"), []byte("new data"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := s.handle("storage.usage", []byte("null"))
	if err != nil {
		t.Fatal(err)
	}
	after := value.(domain.StorageReport)
	if after.Usage.RetainedBytes <= before.Usage.RetainedBytes {
		t.Fatal("older client's explicit refresh was silently cached", before, after)
	}
}
