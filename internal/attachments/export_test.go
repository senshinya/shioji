package attachments

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExportCopiesSnapshotAndPreservesDestinationOnInvalidSource(t *testing.T) {
	r := New(t.TempDir())
	a, err := r.Save("hello.go", []byte("package hello\n"))
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "chosen.go")
	if err = r.Export(a, destination); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(destination)
	if string(data) != "package hello\n" {
		t.Fatal("export changed snapshot", string(data))
	}
	source, _ := r.Path(StagingWorkspace, a)
	os.WriteFile(source, []byte("package other\n"), 0600)
	if err = r.Export(a, destination); err == nil {
		t.Fatal("exported corrupt local cache")
	}
	data, _ = os.ReadFile(destination)
	if string(data) != "package hello\n" {
		t.Fatal("failed export destroyed existing destination")
	}
}
