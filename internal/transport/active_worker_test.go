package transport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedWorkerSelectionPersistsOnlyVerifiedBuildIdentity(t *testing.T) {
	dir := t.TempDir()
	digest := strings.Repeat("a", 64)
	if err := SaveActiveWorker(dir, digest); err != nil {
		t.Fatal(err)
	}
	got, err := ActiveWorkerBuild(dir)
	if err != nil || got != digest {
		t.Fatal(got, err)
	}
	if err = SaveActiveWorker(dir, "../escape"); err == nil {
		t.Fatal("invalid selection accepted")
	}
	got, _ = ActiveWorkerBuild(dir)
	if got != digest {
		t.Fatal("bad selection replaced good one")
	}
	os.WriteFile(filepath.Join(dir, "active-worker.json"), []byte(`{"buildId":"broken"}`), 0600)
	if _, err = ActiveWorkerBuild(dir); err == nil {
		t.Fatal("corrupt selection silently fell back")
	}
}
