package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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

func TestToolAudioCacheCopiesVerifiedBytesForSystemPlayback(t *testing.T) {
	wav := []byte("RIFF\x26\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x40\x1f\x00\x00\x80\x3e\x00\x00\x02\x00\x10\x00data\x02\x00\x00\x00\x00\x00")
	source, err := attachments.DescribeToolAudio("result.wav", wav)
	if err != nil {
		t.Fatal(err)
	}
	originals := attachments.New(t.TempDir())
	if _, err := originals.Put(attachments.StagingWorkspace, source, 0, wav); err != nil {
		t.Fatal(err)
	}
	cache := NewAttachmentCache(t.TempDir())
	directory := t.TempDir()
	link, err := cache.LinkedAudio(context.Background(), AttachmentTarget{}, originals, source, directory)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(link.Path)
	if err != nil || !bytes.Equal(data, wav) || filepath.Ext(link.Path) != ".wav" || link.SHA256 != source.SHA256 {
		t.Fatalf("system playback got an unverified snapshot: %+v, %v", link, err)
	}
	report, err := LinkedFileStorage(directory, true)
	if err != nil || report.FreedBytes != int64(len(wav)) {
		t.Fatalf("linked audio copy did not participate in cache cleanup: %+v, %v", report, err)
	}
}
