package app

import (
	"context"
	"os"
	"shioji/internal/attachments"
	"testing"
	"time"
)

func TestDisplayCacheCleanupWaitsForPinnedReader(t *testing.T) {
	cache := NewAttachmentCache(t.TempDir())
	a, err := cache.repository.Save("cached.txt", []byte("downloaded copy"))
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan string, 1), make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		readDone <- cache.Read(context.Background(), AttachmentTarget{}, attachments.New(t.TempDir()), a, func(path string) error { entered <- path; <-release; _, err := os.ReadFile(path); return err })
	}()
	path := <-entered
	cleanDone := make(chan error, 1)
	go func() { _, err := cache.Storage(true); cleanDone <- err }()
	select {
	case err := <-cleanDone:
		t.Fatal("cleaned a pinned file", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("reader lost its file", err)
	}
	close(release)
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	if err := <-cleanDone; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cache was not reclaimed after read", err)
	}
}
