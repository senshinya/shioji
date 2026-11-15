package attachments

import (
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
)

func TestArchiveResourceDeletionStaysInsideOwnedWorkspace(t *testing.T) {
	r := New(t.TempDir())
	workspace := domain.NewID()
	a, _ := Describe("private.txt", []byte("private bytes"))
	if _, err := r.Put(workspace, a, 0, []byte("private bytes")); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteArchived(workspace, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(workspace, a.ID, 0); !os.IsNotExist(err) {
		t.Fatal("deleted attachment still readable", err)
	}
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep")
	os.WriteFile(keep, []byte("project"), 0600)
	linked := domain.NewID()
	os.Symlink(outside, filepath.Join(r.Directory, linked))
	if err := r.DeleteArchivedWorkspace(linked); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("cleanup traversed a symlink", err)
	}
	if err := r.DeleteArchivedWorkspace("../"); err == nil {
		t.Fatal("unsafe identity accepted")
	}
}
