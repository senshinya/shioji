package attachments

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Removing the age guard or treating complete uploads as temporary must fail this test.
func TestStorageCleanupOnlyExpiresIncompleteUploads(t *testing.T) {
	r := New(t.TempDir())
	complete, err := r.Save("draft.txt", []byte("unsent draft"))
	if err != nil {
		t.Fatal(err)
	}
	old, _ := Describe("old.txt", []byte("expired upload"))
	recent, _ := Describe("recent.txt", []byte("upload still in progress"))
	for _, a := range []struct {
		id  string
		old bool
	}{{old.ID, true}, {recent.ID, false}} {
		meta := old
		if !a.old {
			meta = recent
		}
		if _, err := r.Put(StagingWorkspace, meta, 0, []byte("u")); err != nil {
			t.Fatal(err)
		}
		if a.old {
			path := filepath.Join(r.Directory, StagingWorkspace, a.id+".part")
			age := time.Now().Add(-8 * 24 * time.Hour)
			if err := os.Chtimes(path, age, age); err != nil {
				t.Fatal(err)
			}
		}
	}
	usage, err := r.Storage(time.Now(), false, false)
	if err != nil || usage.Usage.UploadBytes <= 0 {
		t.Fatalf("expired upload not measured: %+v %v", usage, err)
	}
	report, err := r.Storage(time.Now(), true, false)
	if err != nil || report.FreedBytes <= 0 || report.Usage.UploadBytes != 0 {
		t.Fatalf("cleanup: %+v %v", report, err)
	}
	if _, err := r.Path(StagingWorkspace, complete); err != nil {
		t.Fatal("draft original was deleted", err)
	}
	if _, err := os.Stat(filepath.Join(r.Directory, StagingWorkspace, recent.ID+".part")); err != nil {
		t.Fatal("active upload was deleted", err)
	}
}

func TestStorageCleanupDoesNotFollowLinksOrDeleteUnknownFiles(t *testing.T) {
	r := New(t.TempDir())
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.Directory, StagingWorkspace)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Directory, "unrecognized"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Storage(time.Now(), true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal("followed symlink", err)
	}
	if _, err := os.Stat(filepath.Join(r.Directory, "unrecognized")); err != nil {
		t.Fatal("deleted unknown file", err)
	}
}
