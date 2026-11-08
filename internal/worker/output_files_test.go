package worker

import (
	"context"
	"os"
	"testing"

	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
)

func TestToolFileSnapshotSurvivesHistoryAndIndependentDownload(t *testing.T) {
	s, w := memoryWorker(t, imageHarness{memoryHarness: memoryHarness{id: "files"}, data: []byte("package hello\n"), kind: "file", name: "hello.go"})
	go s.Serve()
	op, err := s.submit(domain.Submission{WorkspaceID: w.ID, HarnessID: "files", OperationID: "make-file", Text: "write", Config: domain.RunConfig{Model: "files-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := awaitReceipt(t, s, op.ID); got.State != "confirmed" {
		t.Fatal(got)
	}
	d, err := s.store.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var ref *domain.Attachment
	for _, item := range d.Items {
		for _, part := range item.Parts {
			if part.Kind == "file" {
				ref = part.Attachment
			}
		}
	}
	if ref == nil || ref.Name != "hello.go" || ref.MIME != "text/plain" {
		t.Fatal("file snapshot missing", ref)
	}
	target := app.AttachmentTarget{Directory: s.Directory(), DeviceID: s.store.DeviceID(), WorkspaceID: w.ID}
	path, err := target.Download(context.Background(), attachments.New(t.TempDir()), *ref)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "package hello\n" {
		t.Fatal("changed file content", err)
	}
}
