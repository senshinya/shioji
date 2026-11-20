package worker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"shioji/internal/attachments"
	"testing"
	"time"
)

func TestStorageWalkStopsBetweenFilesAndSkipsLinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("do not count"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "nested/b"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("123"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	open := func() *os.File {
		f, err := os.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	f := open()
	var bytes int64
	err := walkStorageFiles(context.Background(), f, "", func(_ string, size int64) error { bytes += size; return nil })
	f.Close()
	if err != nil || bytes != 6 {
		t.Fatal("walk followed a symlink or miscounted nested data", bytes, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	visits := 0
	f = open()
	defer f.Close()
	err = walkStorageFiles(ctx, f, "", func(_ string, _ int64) error { visits++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || visits != 1 {
		t.Fatal("walk continued after cancellation", visits, err)
	}
}

func TestStorageProducerDrainsBeforeShutdownReceipt(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "storage"})
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	s.wg.Add(1)
	go func() { defer s.wg.Done(); _, _ = s.storage(false) }()
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal("queued storage blocked worker drain", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker still waiting for cancelled storage producer")
	}
}

func BenchmarkStorageWorktreeWalk(b *testing.B) {
	path := os.Getenv("SHIOJI_STORAGE_BENCH_ROOT")
	if path == "" {
		b.Skip("set SHIOJI_STORAGE_BENCH_ROOT to a read-only tree")
	}
	for _, mode := range []string{"root-relative-baseline", "directory-relative"} {
		b.Run(mode, func(b *testing.B) {
			var count, total int64
			for range b.N {
				count, total = 0, 0
				root, err := os.OpenRoot(path)
				if err != nil {
					b.Fatal(err)
				}
				if mode == "root-relative-baseline" {
					err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, e error) error {
						if e != nil {
							return e
						}
						if name == "attachments" && d.IsDir() {
							return fs.SkipDir
						}
						if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
							return nil
						}
						st, e := root.Lstat(name)
						if e == nil && st.Mode().IsRegular() {
							count++
							total += st.Size()
						}
						return e
					})
				} else {
					f, e := root.Open(".")
					if e != nil {
						b.Fatal(e)
					}
					err = walkStorageFiles(context.Background(), f, "", func(_ string, size int64) error { count++; total += size; return nil })
					f.Close()
				}
				root.Close()
				if err != nil {
					b.Fatal(err)
				}
			}
			b.Log(fmt.Sprintf("files=%d logicalBytes=%d", count, total))
		})
	}
}

func BenchmarkStorageReportCache(b *testing.B) {
	path := os.Getenv("SHIOJI_STORAGE_BENCH_ROOT")
	if path == "" {
		b.Skip("set SHIOJI_STORAGE_BENCH_ROOT to a read-only tree")
	}
	s := &Server{ctx: context.Background(), config: Config{Directory: path}, attachments: attachments.New(filepath.Join(path, "attachments"))}
	report, err := s.storage(false)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := s.storage(false); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.Logf("cachedLogicalBytes=%d measuredAt=%s", report.Usage.Total(), report.MeasuredAt.Format(time.RFC3339Nano))
}
