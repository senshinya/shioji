package worker_test

import (
	"context"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"testing"
)

func TestStorageCleanupPreservesWorkerStateActiveLogsAndAttachmentOriginals(t *testing.T) {
	s, c, _ := startServer(t)
	r := attachments.New(filepath.Join(s.Directory(), "attachments"))
	a, err := r.Save("draft.txt", []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"worker.log": "active", "worker.log.1": "old log", "worker.log.not-a-rotation": "keep"} {
		if err := os.WriteFile(filepath.Join(s.Directory(), name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "keep")
	os.WriteFile(outside, []byte("outside"), 0600)
	os.Symlink(outside, filepath.Join(s.Directory(), "worker.log.2"))
	var before, after domain.StorageReport
	call(t, c, "storage.usage", nil, &before)
	if before.Usage.LogBytes != 7 || before.Usage.RetainedBytes <= 0 {
		t.Fatalf("usage %+v", before)
	}
	call(t, c, "storage.clean", nil, &after)
	if after.FreedBytes != 7 || after.Usage.LogBytes != 0 {
		t.Fatalf("cleanup %+v", after)
	}
	for _, name := range []string{"worker.log", "worker.log.not-a-rotation", "state.sqlite"} {
		if _, err := os.Stat(filepath.Join(s.Directory(), name)); err != nil {
			t.Fatal("removed retained data", name, err)
		}
	}
	if _, err := r.Path(attachments.StagingWorkspace, a); err != nil {
		t.Fatal("removed original", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("followed link", err)
	}
}

func TestStorageTargetUsesOwningDeviceOnIndependentConnection(t *testing.T) {
	s, c, _ := startServer(t)
	m := app.NewModel()
	var snapshot domain.Snapshot
	call(t, c, "snapshot", nil, &snapshot)
	m.LocalDeviceID = c.DeviceID
	m.ApplyDeviceUpdate("local", func(p *app.Model) { p.Connected = true; p.ConnectionEpoch = c.Epoch; p.ApplySnapshot(snapshot) })
	target, err := m.StorageTarget(s.Directory(), "local")
	if err != nil {
		t.Fatal(err)
	}
	report, err := target.Read(context.Background(), false)
	if err != nil || report.Usage.RetainedBytes <= 0 {
		t.Fatalf("independent authenticated storage read failed: %+v %v", report, err)
	}
	// A new UI connection to the same device has an independent epoch too.
	other := connect(t, s.Directory())
	m.ApplyDeviceUpdate("local", func(p *app.Model) { p.ConnectionEpoch = other.Epoch })
	target, err = m.StorageTarget(s.Directory(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.Read(context.Background(), true); err != nil {
		t.Fatal("reconnected storage request failed", err)
	}
}

func TestClientDisplayCacheCleanupPreservesDraftOriginalAndCanRedownload(t *testing.T) {
	s, c, _ := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", struct {
		Name    string   `json:"name"`
		Folders []string `json:"folders"`
	}{"Cache", []string{t.TempDir()}}, &w)
	originals := attachments.New(t.TempDir())
	a, err := originals.Save("draft.txt", []byte("retain this draft"))
	if err != nil {
		t.Fatal(err)
	}
	target := app.AttachmentTarget{Directory: s.Directory(), DeviceID: c.DeviceID, WorkspaceID: w.ID}
	if err := target.Upload(context.Background(), originals, a); err != nil {
		t.Fatal(err)
	}
	cache := app.NewAttachmentCache(t.TempDir())
	consume := func(path string) error {
		data, err := os.ReadFile(path)
		if err == nil && string(data) != "retain this draft" {
			t.Fatal("wrong bytes")
		}
		return err
	}
	if err := cache.Read(context.Background(), target, attachments.New(t.TempDir()), a, consume); err != nil {
		t.Fatal(err)
	}
	before, err := cache.Storage(false)
	if err != nil || before.Usage.PreviewBytes <= 0 {
		t.Fatalf("cache %+v %v", before, err)
	}
	after, err := cache.Storage(true)
	if err != nil || after.Usage.PreviewBytes != 0 || after.FreedBytes != before.Usage.PreviewBytes {
		t.Fatalf("cleanup %+v %v", after, err)
	}
	if _, err := originals.Path(attachments.StagingWorkspace, a); err != nil {
		t.Fatal("original deleted", err)
	}
	if err := cache.Read(context.Background(), target, attachments.New(t.TempDir()), a, consume); err != nil {
		t.Fatal("redownload failed", err)
	}
}
