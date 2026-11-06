package attachments

import (
	"bytes"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
)

func TestChunkUploadIsImmutableScopedAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	workspace := domain.NewID()
	data := bytes.Repeat([]byte("hello\n"), 12000)
	ref, err := Describe("notes.txt", data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Put(workspace, ref, 0, data[:65536]); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Path(workspace, ref); err == nil {
		t.Fatal("partial upload became usable")
	}
	if _, err = r.Put(workspace, ref, 0, data[:65536]); err != nil {
		t.Fatal("lost-receipt retry failed", err)
	}
	if _, err = r.Put(workspace, ref, 65536, data[65536:]); err != nil {
		t.Fatal(err)
	}
	r = New(dir)
	path, err := r.Path(workspace, ref)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, data) {
		t.Fatal("resource changed")
	}
	if _, err = r.Path(domain.NewID(), ref); err == nil {
		t.Fatal("cross-workspace resource accepted")
	}
	changed := ref
	changed.Name = "other.txt"
	if _, err = r.Put(workspace, changed, 0, data[:1]); err == nil {
		t.Fatal("metadata replacement accepted")
	}
	if _, err = r.Get(workspace, "../outside", 0); err == nil {
		t.Fatal("path traversal accepted")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0077 != 0 {
		t.Fatal("attachment is not private")
	}
	if filepath.Dir(path) == dir {
		t.Fatal("resource was not workspace-scoped")
	}
}

func TestAttachmentRejectsBinaryAndFalseDigest(t *testing.T) {
	if _, err := Describe("archive.bin", []byte{0, 1, 2}); err == nil {
		t.Fatal("unsupported binary accepted")
	}
	ref, _ := Describe("readme.md", []byte("real"))
	r := New(t.TempDir())
	if _, err := r.Put(domain.NewID(), ref, 0, []byte("fake")); err == nil {
		t.Fatal("digest mismatch accepted")
	}
}
